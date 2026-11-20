package compiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/lang"
)

// The helmet sample compiles its geometry (milestone M3, first part). Its
// looks (the import, plug, texture, palette material and paint) are M3b, so
// these tests build it with the geometry-only lowering: every material is a
// plain grey pbr, the cells are left out.

const (
	helmetPath      = "../examples/helmet/helmet.gw"
	helmetCandidate = "../testdata/compiler/helmet-geometry.candidate.txt"
	yard            = 0.9144 // metres in the asset's length unit
)

func loadGeometry(t testing.TB, path string) *ast.File {
	t.Helper()
	src, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	return lowerGeometry(t, src)
}

func lowerGeometry(t testing.TB, src []byte) *ast.File {
	t.Helper()
	f, ds := ast.LowerWith(lang.Parse(src), ast.Options{GeometryOnly: true})
	if diag.HasErrors(ds) {
		t.Fatalf("lower: %v", ds)
	}
	return f
}

var (
	helmetOnce    sync.Once
	helmetProduct *Product
	helmetDiags   []diag.Diagnostic
)

// builtHelmet builds the helmet once for the tests that only read it.
func builtHelmet(t testing.TB) *Product {
	t.Helper()
	helmetOnce.Do(func() {
		src, err := os.ReadFile(filepath.FromSlash(helmetPath))
		if err != nil {
			panic(err)
		}
		f, _ := ast.LowerWith(lang.Parse(src), ast.Options{GeometryOnly: true})
		helmetProduct, helmetDiags = Build(f, "")
	})
	if helmetProduct == nil {
		t.Fatalf("the helmet did not build: %v", helmetDiags)
	}
	return helmetProduct
}

func helmetFile(t testing.TB, name string) *File {
	t.Helper()
	for _, o := range builtHelmet(t).Outputs {
		for _, f := range o.Files {
			if f.Name == name {
				return f
			}
		}
	}
	t.Fatalf("no file %s", name)
	return nil
}

func decode(t testing.TB, f *File) *gltf.Document {
	t.Helper()
	d, err := gltf.Decode(f.GLB)
	if err != nil {
		t.Fatalf("%s: %v", f.Name, err)
	}
	return d
}

func nodeByName(t testing.TB, d *gltf.Document, name string) (int, *gltf.Node) {
	t.Helper()
	for i := range d.Nodes {
		if d.Nodes[i].Name == name {
			return i, &d.Nodes[i]
		}
	}
	t.Fatalf("no node %s", name)
	return -1, nil
}

// primMesh turns a decoded primitive into a mesh with one corner per written
// vertex, so welding by position finds the shells.
func primMesh(p gltf.Primitive) *mesh.Mesh {
	m := &mesh.Mesh{}
	for i := 0; i+2 < len(p.Positions); i += 3 {
		m.Positions = append(m.Positions, mesh.Vec3{float64(p.Positions[i]), float64(p.Positions[i+1]), float64(p.Positions[i+2])})
		c := mesh.Corner{Pos: int32(i / 3)}
		if len(p.Normals) > i+2 {
			c.Normal = mesh.Vec3{float64(p.Normals[i]), float64(p.Normals[i+1]), float64(p.Normals[i+2])}
		}
		m.Corners = append(m.Corners, c)
	}
	for i := 0; i+2 < len(p.Indices); i += 3 {
		m.Tris = append(m.Tris, mesh.Tri{int32(p.Indices[i]), int32(p.Indices[i+1]), int32(p.Indices[i+2])})
		m.Shell = append(m.Shell, 0)
	}
	return m
}

// nodeMesh merges the primitives of a node.
func nodeMesh(t testing.TB, d *gltf.Document, n *gltf.Node) *mesh.Mesh {
	t.Helper()
	if n.Mesh == nil {
		return &mesh.Mesh{}
	}
	var ms []*mesh.Mesh
	for _, p := range d.Meshes[*n.Mesh].Primitives {
		ms = append(ms, primMesh(p))
	}
	return mesh.Merge(ms...)
}

// shell is one connected component of a node's mesh.
type shell struct {
	tris      int
	closed    bool
	openEdges int
	volume    float64
}

// shellsOf splits a mesh into the components that share welded vertices.
func shellsOf(m *mesh.Mesh) []shell {
	comp, n := m.Components()
	var out []shell
	for c := 0; c < n; c++ {
		sub := &mesh.Mesh{Positions: m.Positions, Corners: m.Corners}
		for i, t := range m.Tris {
			if int(comp[i]) == c {
				sub.Tris = append(sub.Tris, t)
				sub.Shell = append(sub.Shell, 0)
			}
		}
		out = append(out, shell{tris: len(sub.Tris), closed: sub.IsClosed(), openEdges: sub.OpenEdges(), volume: sub.SignedVolume()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].tris != out[j].tris {
			return out[i].tris > out[j].tris
		}
		return out[i].openEdges < out[j].openEdges
	})
	return out
}

func boundsOf(m *mesh.Mesh) (lo, hi mesh.Vec3) {
	lo, hi, _ = m.Bounds()
	return
}

// ---- counts -----------------------------------------------------------------------

// The triangle counts the spec's rules give, worked from the source. Segments
// n, rows R: a full-turn loft has 2n(R-1) side triangles and n-2 per capped
// end; an ellipsoid has R latitude rows including both ends: 2n(R-1) + 2(n-2);
// a tube of n segments through p points has 2n(p-1) and no caps.
func tubeTris(n, p int) int      { return 2 * n * (p - 1) }
func ellipsoidTris(n, r int) int { return 2*n*(r-1) + 2*(n-2) }

func TestHelmetLevelZeroCountsFollowTheCountingRules(t *testing.T) {
	// Level 0, either shell style.
	shell := 2 * 24 * (7 - 1) // seven rings, caps off
	pads := 4 * ellipsoidTris(12, 7)
	straps := 2 * tubeTris(7, 3)
	chin := ellipsoidTris(12, 5)
	bumper := ellipsoidTris(10, 4)
	root := shell + pads + straps + chin + bumper
	rail := tubeTris(7, 11)
	cage := 3*rail + 2*tubeTris(7, 3)
	twoBar := 2 * rail
	stripe := tubeTris(7, 8)
	visor := 2 * 16 * (3 - 1) // arc, caps off
	if root != 1192 || cage != 476 || twoBar != 280 || stripe != 98 || visor != 64 {
		t.Fatalf("hand count: root %d cage %d two_bar %d stripe %d visor %d", root, cage, twoBar, stripe, visor)
	}
	for _, name := range []string{"helmet-classic.glb", "helmet-aero.glb"} {
		f := helmetFile(t, name)
		if want := root + cage + stripe; f.Triangles != want {
			t.Errorf("%s: %d triangles in the default look, want %d", name, f.Triangles, want)
		}
		// The worst look has one facemask: the cage, the larger of the two.
		if want := root + cage + stripe + visor; f.TrianglesWorst != want {
			t.Errorf("%s: %d triangles in the worst look, want %d", name, f.TrianglesWorst, want)
		}
		if want := root + cage + twoBar + stripe + visor; f.TrianglesStored != want {
			t.Errorf("%s: %d stored triangles, want %d", name, f.TrianglesStored, want)
		}
		// plan.md 4.1 row M3: three draws in the default look, four in the worst.
		if f.Draws != 3 || f.DrawsWorst != 4 {
			t.Errorf("%s: %d and %d draws, want 3 and 4", name, f.Draws, f.DrawsWorst)
		}
		if f.Alternates != 2 || f.Materials != 2 {
			t.Errorf("%s: %d alternates, %d materials", name, f.Alternates, f.Materials)
		}
	}
}

func TestHelmetBudgetsHold(t *testing.T) {
	// The file's own budget block is checked on every file of both outputs:
	// triangles 2200, worst 2400, stored 2700, draws 3 and 4, two materials.
	p := builtHelmet(t)
	n := 0
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			n++
			if f.Triangles > 2200 || f.TrianglesWorst > 2400 || f.TrianglesStored > 2700 || f.Draws > 3 || f.DrawsWorst > 4 || f.Materials > 2 {
				t.Errorf("%s breaks a budget: %+v", f.Name, *f)
			}
		}
	}
	if n != 4 {
		t.Fatalf("%d files, want classic and aero at two levels each", n)
	}
}

func TestHelmetOutputsAndLevels(t *testing.T) {
	p := builtHelmet(t)
	var names []string
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			names = append(names, o.Name+"/"+f.Name)
		}
	}
	want := []string{"classic/helmet-classic.glb", "classic/helmet-classic-lod1.glb", "aero/helmet-aero.glb", "aero/helmet-aero-lod1.glb"}
	if strings.Join(names, " ") != strings.Join(want, " ") {
		t.Fatalf("files %v, want %v", names, want)
	}
	if p.Outputs[0].FirSHA256 == p.Outputs[1].FirSHA256 {
		t.Error("classic and aero have the same meaning hash")
	}
}

func TestHelmetLevelOneDropsAndReduces(t *testing.T) {
	for _, style := range []string{"classic", "aero"} {
		f0, f1 := helmetFile(t, "helmet-"+style+".glb"), helmetFile(t, "helmet-"+style+"-lod1.glb")
		d1 := decode(t, f1)
		_, root := nodeByName(t, d1, "helmet")
		shells := shellsOf(nodeMesh(t, d1, root))
		// chin_cup, ear_l and ear_r are dropped: what remains of the root is the
		// shell and the bumper. The bumper is the one closed shell.
		closed := 0
		for _, s := range shells {
			if s.closed {
				closed++
			}
		}
		if len(shells) != 2 || closed != 1 {
			t.Errorf("%s lod 1: %d shells, %d closed: %+v", style, len(shells), closed, shells)
		}
		if f1.Triangles >= f0.Triangles/2 || f1.Draws != 3 || f1.DrawsWorst != 4 {
			t.Errorf("%s lod 1: %d triangles (lod 0 has %d), %d and %d draws", style, f1.Triangles, f0.Triangles, f1.Draws, f1.DrawsWorst)
		}
		// Tubes reach 3 segments at 3 mm: the rails 2*3*10 and the bars 2*3*2
		// each; the stripe's larger radius needs 4.
		_, cage := nodeByName(t, d1, "mask_cage")
		if got := nodeMesh(t, d1, cage).TriangleCount(); got != 3*tubeTris(3, 11)+2*tubeTris(3, 3) {
			t.Errorf("%s lod 1 cage: %d triangles", style, got)
		}
		_, stripe := nodeByName(t, d1, "crown_stripe")
		if got := nodeMesh(t, d1, stripe).TriangleCount(); got != tubeTris(4, 8) {
			t.Errorf("%s lod 1 stripe: %d triangles", style, got)
		}
		// The visor is an arc sheet of 7 intervals at 3 mm, and both rings stay.
		_, visor := nodeByName(t, d1, "visor_shield")
		if got := nodeMesh(t, d1, visor).TriangleCount(); got != 2*7*2 {
			t.Errorf("%s lod 1 visor: %d triangles", style, got)
		}
		// The shell's segments are the fewest whose chord error at its largest
		// ring radius is within 3 mm: 16 for the classic radius 0.167 yd, 17 for
		// the aero 0.178 yd. No ring is within 3 mm of its neighbours' strip, so
		// all seven rows stay and there are six bands.
		radius := map[string]float64{"classic": 0.167, "aero": 0.178}[style] * yard
		segs := 24
		for n := 3; n < 24; n++ {
			if radius*(1-math.Cos(math.Pi/float64(n))) <= 0.003*(1+1e-9) {
				segs = n
				break
			}
		}
		var shellTris int
		for _, s := range shells {
			if !s.closed {
				shellTris = s.tris
			}
		}
		if want := 2 * segs * 6; shellTris != want || (style == "classic") != (segs == 16) {
			t.Errorf("%s lod 1 shell has %d triangles, want %d (%d segments)", style, shellTris, want, segs)
		}
	}
}

// ---- structure ----------------------------------------------------------------------

func TestHelmetNodesAndScenes(t *testing.T) {
	d := decode(t, helmetFile(t, "helmet-classic.glb"))
	var names []string
	for _, n := range d.Nodes {
		names = append(names, n.Name)
	}
	if got, want := strings.Join(names, " "), "helmet mask_two_bar mask_cage crown_stripe visor_shield"; got != want {
		t.Fatalf("nodes %q, want %q", got, want)
	}
	if len(d.Scenes) != 2 || d.Scene == nil || *d.Scene != 0 {
		t.Fatalf("scenes %+v, default %v", d.Scenes, d.Scene)
	}
	// The default scene reaches the root, the cage and the stripe. The
	// two-bar mask and the visor are roots of the alternates scene, outside it.
	reach := map[string]bool{}
	var walk func(i int)
	walk = func(i int) {
		reach[d.Nodes[i].Name] = true
		for _, c := range d.Nodes[i].Children {
			walk(c)
		}
	}
	for _, r := range d.Scenes[0].Nodes {
		walk(r)
	}
	if !reach["helmet"] || !reach["mask_cage"] || !reach["crown_stripe"] || reach["mask_two_bar"] || reach["visor_shield"] {
		t.Fatalf("default scene reaches %v", reach)
	}
	alt := d.Scenes[1]
	if alt.Name != "alternates" || len(alt.Nodes) != 2 || d.Nodes[alt.Nodes[0]].Name != "mask_two_bar" || d.Nodes[alt.Nodes[1]].Name != "visor_shield" {
		t.Fatalf("alternates scene %+v", alt)
	}
	// KHR_node_visibility on every node a runtime `when` controls.
	want := map[string]string{"mask_two_bar": `{"visible":false}`, "mask_cage": `{"visible":true}`, "crown_stripe": `{"visible":true}`, "visor_shield": `{"visible":false}`}
	for name, ext := range want {
		_, n := nodeByName(t, d, name)
		if got := string(n.Extensions["KHR_node_visibility"]); got != ext {
			t.Errorf("%s: KHR_node_visibility %s, want %s", name, got, ext)
		}
	}
	if _, n := nodeByName(t, d, "helmet"); len(n.Extensions) != 0 {
		t.Errorf("the root carries %v", n.Extensions)
	}
	if !bytes.Contains(f0GLBJSON(t, "helmet-classic.glb"), []byte(`"extensionsUsed":["KHR_node_visibility"]`)) {
		t.Error("extensionsUsed does not list KHR_node_visibility")
	}
}

// f0GLBJSON returns the JSON chunk of a built file.
func f0GLBJSON(t testing.TB, name string) []byte {
	t.Helper()
	b := helmetFile(t, name).GLB
	n := int(b[12]) | int(b[13])<<8 | int(b[14])<<16 | int(b[15])<<24
	return b[20 : 20+n]
}

type bindingJSON struct {
	Kind        string     `json:"kind"`
	Node        string     `json:"node"`
	Parent      string     `json:"parent"`
	Scene       string     `json:"scene"`
	Visible     bool       `json:"visible"`
	Translation [3]float64 `json:"translation"`
	Test        struct {
		Op    string `json:"op"`
		Value string `json:"value"`
	} `json:"test"`
}

type paramJSON struct {
	Name     string        `json:"name"`
	Type     string        `json:"type"`
	Default  any           `json:"default"`
	Values   []string      `json:"values"`
	Bindings []bindingJSON `json:"bindings"`
}

func runtimeParams(t testing.TB, d *gltf.Document) []paramJSON {
	t.Helper()
	var x struct {
		Greenware struct {
			Format  string `json:"format"`
			Runtime struct {
				Format string      `json:"format"`
				Params []paramJSON `json:"params"`
			} `json:"runtime"`
		} `json:"greenware"`
	}
	if err := json.Unmarshal(d.Extras, &x); err != nil {
		t.Fatal(err)
	}
	if x.Greenware.Runtime.Format != "greenware.runtime/1" {
		t.Fatalf("runtime table format %q", x.Greenware.Runtime.Format)
	}
	return x.Greenware.Runtime.Params
}

func TestHelmetRuntimeBindingTable(t *testing.T) {
	d := decode(t, helmetFile(t, "helmet-aero.glb"))
	ps := runtimeParams(t, d)
	if len(ps) != 3 {
		t.Fatalf("%d runtime parameters in the table: %+v", len(ps), ps)
	}
	by := map[string]paramJSON{}
	for _, p := range ps {
		by[p.Name] = p
	}
	fm := by["facemask"]
	if fm.Type != "enum" || fm.Default != "cage" || strings.Join(fm.Values, ",") != "open,two_bar,cage" || len(fm.Bindings) != 2 {
		t.Fatalf("facemask %+v", fm)
	}
	cage, two := fm.Bindings[0], fm.Bindings[1]
	if cage.Node != "mask_cage" || cage.Kind != "visibility" || cage.Parent != "helmet" || cage.Scene != "default" || !cage.Visible || cage.Test.Op != "eq" || cage.Test.Value != "cage" {
		t.Errorf("cage binding %+v", cage)
	}
	if two.Node != "mask_two_bar" || two.Scene != "alternates" || two.Visible || two.Test.Op != "eq" || two.Test.Value != "two_bar" || two.Parent != "helmet" {
		t.Errorf("two_bar binding %+v", two)
	}
	st, vz := by["stripe"], by["visor"]
	if st.Type != "switch" || st.Default != true || len(st.Bindings) != 1 || st.Bindings[0].Node != "crown_stripe" || st.Bindings[0].Test.Op != "is" || !st.Bindings[0].Visible {
		t.Errorf("stripe %+v", st)
	}
	if vz.Default != false || len(vz.Bindings) != 1 || vz.Bindings[0].Scene != "alternates" || vz.Bindings[0].Visible {
		t.Errorf("visor %+v", vz)
	}
	// The branch nodes sit at the root's origin, so their local transform is zero.
	for _, b := range []bindingJSON{cage, two, st.Bindings[0], vz.Bindings[0]} {
		if b.Translation != [3]float64{} {
			t.Errorf("%s translation %v", b.Node, b.Translation)
		}
	}
	// Colour parameters have no binding in a geometry-only build.
	for _, n := range []string{"shell_color", "visor_tint", "stripe_color"} {
		if _, ok := by[n]; ok {
			t.Errorf("%s is in the table without a material binding", n)
		}
	}
}

// ---- metres -------------------------------------------------------------------------

func TestHelmetIsWrittenInMetresFromYards(t *testing.T) {
	// The source is authored in yards (asset length = yd) and every number in it
	// is a bare yard value. Output is metres: converted once, at the boundary.
	const tol = 2e-7 // float32
	for _, c := range []struct {
		name     string
		maxX     float64 // the ear pad's outer ellipsoid: 0.145 + 0.009 yd
		minY     float64 // chin cup: 1.71 - 0.026 yd
		maxZ     float64 // the shell's rear at ring 2: rz - the loft's z offset
		minZ     float64
		topShell float64 // top of the shell, ring 6, in the root's own mesh
	}{
		{"helmet-classic.glb", 0.154 * yard, 1.684 * yard, (0.167 - 0.012) * yard, 0, 1.999 * yard},
		{"helmet-aero.glb", 0.154 * yard, 1.684 * yard, (0.178 - 0.020) * yard, 0, 1.986 * yard},
	} {
		d := decode(t, helmetFile(t, c.name))
		_, root := nodeByName(t, d, "helmet")
		lo, hi := boundsOf(nodeMesh(t, d, root))
		if math.Abs(hi[0]-c.maxX) > tol || math.Abs(lo[0]+c.maxX) > tol {
			t.Errorf("%s: x runs %v..%v m, want +-%v", c.name, lo[0], hi[0], c.maxX)
		}
		if math.Abs(lo[1]-c.minY) > tol {
			t.Errorf("%s: lowest point %v m, want %v", c.name, lo[1], c.minY)
		}
		if math.Abs(hi[2]-c.maxZ) > tol {
			t.Errorf("%s: rearmost point %v m, want %v", c.name, hi[2], c.maxZ)
		}
		// The shell alone: its top ring is the highest point of the loft.
		shellTop := math.Inf(-1)
		m := nodeMesh(t, d, root)
		for _, p := range m.Positions {
			if p[1] > shellTop {
				shellTop = p[1]
			}
		}
		if math.Abs(shellTop-c.topShell) > tol {
			t.Errorf("%s: root top %v m, want %v", c.name, shellTop, c.topShell)
		}
		// Every vertex is inside a head-sized box: a yard-valued mistake would be
		// 9 percent too large, so this is the check that the scale was applied once.
		if hi[1] > 2.1*yard || lo[1] < 1.6*yard || math.Abs(lo[0]) > 0.2 {
			t.Errorf("%s: bounds %v..%v", c.name, lo, hi)
		}
		// The crown stripe passes over the crown at y = 2.002 yd (centre line).
		_, stripe := nodeByName(t, d, "crown_stripe")
		_, sh := boundsOf(nodeMesh(t, d, stripe))
		if c.name == "helmet-classic.glb" && math.Abs(sh[1]-(2.002+0.008)*yard) > 0.001*yard {
			t.Errorf("%s: stripe top %v m", c.name, sh[1])
		}
	}
}

// ---- closed, open, winding -----------------------------------------------------------

func TestHelmetShellsAreClosedOrOpenAsTheSpecSays(t *testing.T) {
	for _, name := range []string{"helmet-classic.glb", "helmet-aero.glb"} {
		d := decode(t, helmetFile(t, name))
		_, root := nodeByName(t, d, "helmet")
		shells := shellsOf(nodeMesh(t, d, root))
		// Ellipsoids are capped, so they are closed: two pads on each side, the
		// chin cup and the bumper. The loft has caps = off and the straps are
		// tubes, so they are open.
		var closedTris, openTris []int
		for _, s := range shells {
			if s.closed {
				closedTris = append(closedTris, s.tris)
				if s.volume <= 0 {
					t.Errorf("%s: a closed shell winds inward (volume %v)", name, s.volume)
				}
			} else {
				openTris = append(openTris, s.tris)
			}
		}
		if fmt.Sprint(closedTris) != fmt.Sprint([]int{164, 164, 164, 164, 116, 76}) {
			t.Errorf("%s: closed shells %v", name, closedTris)
		}
		if fmt.Sprint(openTris) != fmt.Sprint([]int{288, 28, 28}) {
			t.Errorf("%s: open shells %v", name, openTris)
		}
		for _, s := range shells {
			switch {
			case s.tris == 288 && s.openEdges != 2*24:
				t.Errorf("%s: the shell has %d open edges, want 48 (two open rings)", name, s.openEdges)
			case s.tris == 28 && s.openEdges != 2*7:
				t.Errorf("%s: a strap has %d open edges, want 14 (no end caps)", name, s.openEdges)
			}
		}
		// The facemask, the stripe and the visor are open by the same rules.
		for _, nn := range []string{"mask_cage", "mask_two_bar", "crown_stripe", "visor_shield"} {
			_, n := nodeByName(t, d, nn)
			for _, s := range shellsOf(nodeMesh(t, d, n)) {
				if s.closed {
					t.Errorf("%s: a shell of %s is closed", name, nn)
				}
			}
		}
		_, cage := nodeByName(t, d, "mask_cage")
		if got := len(shellsOf(nodeMesh(t, d, cage))); got != 5 {
			t.Errorf("%s: the cage has %d tubes, want 3 rails and 2 bars", name, got)
		}
		_, visor := nodeByName(t, d, "visor_shield")
		if vs := shellsOf(nodeMesh(t, d, visor)); len(vs) != 1 || vs[0].openEdges != 2*16+2*2 {
			t.Errorf("%s: visor shells %+v, want one sheet with 36 open edges", name, vs)
		}
	}
}

func TestHelmetNormalsAndWinding(t *testing.T) {
	for _, name := range []string{"helmet-classic.glb", "helmet-aero.glb", "helmet-classic-lod1.glb", "helmet-aero-lod1.glb"} {
		d := decode(t, helmetFile(t, name))
		for _, nd := range d.Nodes {
			if nd.Mesh == nil {
				continue
			}
			for _, p := range d.Meshes[*nd.Mesh].Primitives {
				m := primMesh(p)
				if len(p.Normals) != len(p.Positions) {
					t.Fatalf("%s %s: no normals", name, nd.Name)
				}
				for i, c := range m.Corners {
					if l := mesh.Len(c.Normal); math.Abs(l-1) > 1e-5 {
						t.Fatalf("%s %s: vertex %d normal has length %v", name, nd.Name, i, l)
					}
				}
				// A triangle's geometric normal agrees in sign with its vertices'
				// normals: no triangle winds against its own surface. The shell's
				// rings that collapse in front (the face opening) have almost no
				// area, and a sliver may disagree; none may disagree by a face.
				bad := 0
				for i := range m.Tris {
					a, b, c := m.TriPositions(i)
					n := mesh.Cross(mesh.Sub(b, a), mesh.Sub(c, a))
					if mesh.Len(n) < 1e-12 {
						continue
					}
					avg := mesh.Add(mesh.Add(m.Corners[m.Tris[i][0]].Normal, m.Corners[m.Tris[i][1]].Normal), m.Corners[m.Tris[i][2]].Normal)
					if mesh.Dot(n, avg) < 0 {
						bad++
					}
				}
				// A tube recomputes its frame at every point by the tube rule, which
				// turns discontinuously where the reference direction changes
				// (language.md section 3.6): the cage bars twist at their last ring,
				// and at 3 segments one facet there can fold. That is the rule, not
				// a fault, so a few facets are allowed in tubes.
				if bad > len(m.Tris)/50 {
					t.Errorf("%s %s: %d of %d triangles wind against their normals", name, nd.Name, bad, len(m.Tris))
				}
			}
		}
	}
}

func TestHelmetEveryFileDecodesAndReencodes(t *testing.T) {
	for _, o := range builtHelmet(t).Outputs {
		for _, f := range o.Files {
			d := decode(t, f)
			again, err := gltf.Encode(d)
			if err != nil {
				t.Errorf("%s: re-encode: %v", f.Name, err)
				continue
			}
			if !bytes.Equal(again, f.GLB) {
				t.Errorf("%s: decode then encode does not give the same bytes", f.Name)
			}
			if err := d.Validate(); err != nil {
				t.Errorf("%s: %v", f.Name, err)
			}
			if f.Size != len(f.GLB) || f.Size > maxEncodeBytes {
				t.Errorf("%s: %d bytes", f.Name, f.Size)
			}
			// What the build measured is what the file holds.
			def, all := 0, 0
			for _, nd := range d.Nodes {
				if nd.Mesh == nil {
					continue
				}
				for _, p := range d.Meshes[*nd.Mesh].Primitives {
					all += len(p.Indices) / 3
				}
			}
			for _, r := range d.Scenes[0].Nodes {
				var walk func(i int)
				walk = func(i int) {
					if nd := d.Nodes[i]; nd.Mesh != nil {
						for _, p := range d.Meshes[*nd.Mesh].Primitives {
							def += len(p.Indices) / 3
						}
					}
					for _, c := range d.Nodes[i].Children {
						walk(c)
					}
				}
				walk(r)
			}
			if def != f.Triangles || all != f.TrianglesStored {
				t.Errorf("%s: decoded %d default and %d stored triangles, measured %d and %d", f.Name, def, all, f.Triangles, f.TrianglesStored)
			}
		}
	}
}

func TestHelmetShellStylesDiffer(t *testing.T) {
	c := decode(t, helmetFile(t, "helmet-classic.glb"))
	a := decode(t, helmetFile(t, "helmet-aero.glb"))
	_, cr := nodeByName(t, c, "helmet")
	_, ar := nodeByName(t, a, "helmet")
	cm, am := nodeMesh(t, c, cr), nodeMesh(t, a, ar)
	if cm.TriangleCount() != am.TriangleCount() {
		t.Fatal("the shells differ in size")
	}
	has := func(m *mesh.Mesh, x, y, z float64) bool {
		for _, p := range m.Positions {
			if math.Abs(p[0]-x) < 2e-7 && math.Abs(p[1]-y) < 2e-7 && math.Abs(p[2]-z) < 2e-7 {
				return true
			}
		}
		return false
	}
	// The vertex rule, worked by hand at a = 0 (the back) and a = 180deg (the
	// front), in yards, then converted once:
	//   classic ring 1: (rx sin a, y + drop max(0,-cos a)^3, rz cos a - 0.012)
	//   aero ring 1:    (..., y + drop max(0,-cos a)^3 + ridge max(0,cos a)^6, rz cos a - 0.02)
	cases := []struct {
		m       *mesh.Mesh
		what    string
		x, y, z float64
	}{
		{cm, "classic back, ring 1", 0, 1.752, 0.153 - 0.012},
		{cm, "classic front, ring 0", 0, 1.715 + 0.175, -0.121 - 0.012},
		{cm, "classic side, ring 2", 0.147, 1.848, -0.012},
		{am, "aero back, ring 1 with its ridge", 0, 1.752 + 0.010, 0.162 - 0.02},
		{am, "aero front, ring 1", 0, 1.752 + 0.144, -0.162 - 0.02},
		{am, "aero side, ring 2", 0.150, 1.840, -0.02},
	}
	for _, c := range cases {
		if !has(c.m, c.x*yard, c.y*yard, c.z*yard) {
			t.Errorf("%s: no vertex at (%v, %v, %v) yd", c.what, c.x, c.y, c.z)
		}
	}
	// The aero shell has no vertex where the classic one has its back rim.
	if has(am, 0, 1.752*yard, (0.153-0.012)*yard) {
		t.Error("the aero shell has the classic back ring")
	}
	// What follows the shell is the same in both styles: the closed shells.
	closed := func(ss []shell) []shell {
		var out []shell
		for _, s := range ss {
			if s.closed {
				out = append(out, s)
			}
		}
		return out
	}
	if cs, as := closed(shellsOf(cm)), closed(shellsOf(am)); len(cs) != 6 || fmt.Sprint(cs) != fmt.Sprint(as) {
		t.Errorf("the closed pieces differ between the styles:\n%v\n%v", cs, as)
	}
}

// ---- determinism and candidates ---------------------------------------------------------

func helmetDigests(p *Product) string {
	var b strings.Builder
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			b.WriteString(f.SHA256 + " " + f.Name + "\n")
		}
	}
	return b.String()
}

func TestHelmetBytesAreRepeatableAcrossGOMAXPROCS(t *testing.T) {
	build := func(procs int) string {
		prev := runtime.GOMAXPROCS(procs)
		defer runtime.GOMAXPROCS(prev)
		src, err := os.ReadFile(filepath.FromSlash(helmetPath))
		if err != nil {
			t.Fatal(err)
		}
		p, ds := Build(lowerGeometry(t, src), "")
		if p == nil {
			t.Fatalf("build: %v", ds)
		}
		return helmetDigests(p)
	}
	one, eight, again := build(1), build(8), build(1)
	if one != eight || one != again {
		t.Fatalf("the files change with GOMAXPROCS or between runs:\n%s\n%s\n%s", one, eight, again)
	}
	if one != helmetDigests(builtHelmet(t)) {
		t.Fatal("a fresh build differs from the shared one")
	}
}

func TestHelmetBytesDoNotDependOnPathOrWrite(t *testing.T) {
	// Writing to two folders gives the same bytes, and the record is last.
	p := builtHelmet(t)
	a, b := t.TempDir(), filepath.Join(t.TempDir(), "deeper", "dir")
	if err := Write(p, a); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, b); err != nil {
		t.Fatal(err)
	}
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			x, _ := os.ReadFile(filepath.Join(a, f.Name))
			y, _ := os.ReadFile(filepath.Join(b, f.Name))
			if !bytes.Equal(x, y) || !bytes.Equal(x, f.GLB) {
				t.Errorf("%s differs between folders", f.Name)
			}
		}
	}
	rec, err := os.ReadFile(filepath.Join(a, p.RecordName()))
	if err != nil || !bytes.Contains(rec, []byte(`"triangles_worst"`)) || !bytes.Contains(rec, []byte(`"alternates": 2`)) {
		t.Errorf("record: %v\n%s", err, rec)
	}
}

func helmetCandidateText(p *Product) string {
	var b strings.Builder
	b.WriteString("# CANDIDATE, not approved: counts and digests of the helmet's geometry, built from examples/helmet/helmet.gw with the geometry-only lowering\n")
	b.WriteString("# (grey pbr materials, no cells, no import, plug or texture). A reviewer approves it; no test flag does.\n")
	b.WriteString("# columns: output file level triangles draws triangles_worst draws_worst triangles_stored vertices bytes sha256\n")
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			fmt.Fprintf(&b, "%s %s %d %d %d %d %d %d %d %d %s\n", o.Name, f.Name, f.Level, f.Triangles, f.Draws, f.TrianglesWorst, f.DrawsWorst, f.TrianglesStored, f.Vertices, f.Size, f.SHA256)
		}
	}
	return b.String()
}

func TestHelmetGeometryCandidate(t *testing.T) {
	got := helmetCandidateText(builtHelmet(t))
	if os.Getenv("GREENWARE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(filepath.FromSlash(helmetCandidate), []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(filepath.FromSlash(helmetCandidate))
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("the helmet geometry changed.\nrecorded:\n%s\nbuilt:\n%s", want, got)
	}
}

func TestHelmetDigestsAreNotTheGoalPostsNorEachOthers(t *testing.T) {
	seen := map[string]string{}
	for _, o := range builtHelmet(t).Outputs {
		for _, f := range o.Files {
			sum := sha256.Sum256(f.GLB)
			if hex.EncodeToString(sum[:]) != f.SHA256 {
				t.Errorf("%s: digest does not match the bytes", f.Name)
			}
			if prev, dup := seen[f.SHA256]; dup {
				t.Errorf("%s and %s have the same digest", f.Name, prev)
			}
			seen[f.SHA256] = f.Name
		}
	}
}

// ---- the front end on the real file ---------------------------------------------------------

func TestHelmetBudgetCatchesAnOverStatedWorstLook(t *testing.T) {
	src, err := os.ReadFile(filepath.FromSlash(helmetPath))
	if err != nil {
		t.Fatal(err)
	}
	// The worst look draws four primitives: the root, one mask, the stripe and
	// the visor. Three is too few.
	bad := strings.Replace(string(src), "draw_calls_worst = 4", "draw_calls_worst = 3", 1)
	if bad == string(src) {
		t.Fatal("sample has no draw_calls_worst = 4")
	}
	p, ds := Build(lowerGeometry(t, []byte(bad)), "")
	if p != nil || !hasBudget(ds, "the worst look makes 4 draws") {
		t.Fatalf("worst draws not caught: %v", ds)
	}
	// The default look: mask_cage and the stripe make it three.
	bad = strings.Replace(string(src), "draw_calls = 3", "draw_calls = 2", 1)
	if p, ds := Build(lowerGeometry(t, []byte(bad)), ""); p != nil || !hasBudget(ds, "the default look makes 3 draws") {
		t.Fatalf("default draws not caught: %v", ds)
	}
	// A tighter stored-triangle budget is caught on the built files.
	bad = strings.Replace(string(src), "triangles_stored = 2700", "triangles_stored = 2000", 1)
	if p, ds := Build(lowerGeometry(t, []byte(bad)), ""); p != nil || !hasBudget(ds, "`triangles_stored` measures 2110") {
		t.Fatalf("stored triangles not caught: %v", ds)
	}
	bad = strings.Replace(string(src), "triangles_worst = 2400", "triangles_worst = 1800", 1)
	bad = strings.Replace(bad, "triangles = 2200", "triangles = 1766", 1)
	if p, ds := Build(lowerGeometry(t, []byte(bad)), ""); p != nil || !hasBudget(ds, "`triangles_worst` measures 1830") {
		t.Fatalf("worst triangles not caught: %v", ds)
	}
}

func hasBudget(ds []diag.Diagnostic, msg string) bool {
	for _, d := range ds {
		if d.Code == diag.CodeBudget && strings.Contains(d.Message, msg) {
			return true
		}
	}
	return false
}

// ---- cost tickets --------------------------------------------------------------------------

func TestHelmetCostTickets(t *testing.T) {
	f := loadGeometry(t, helmetPath)
	p, rc, ds := BuildWith(f, "", Options{})
	if p == nil {
		t.Fatalf("build: %v", ds)
	}
	count := map[string]int{}
	for _, c := range rc.Charges {
		count[c.Ticket]++
	}
	// Two outputs of two levels: one snapshot, two plans, two selects (each has
	// a runtime when), four surface_branches (not surface), four encodes, one
	// publish.
	want := map[string]int{"snapshot": 1, "plan": 2, "select": 2, "surface_branches": 4, "encode": 4, "publish": 1}
	if fmt.Sprint(count) != fmt.Sprint(want) {
		t.Fatalf("tickets %v, want %v", count, want)
	}
	const total = 1_000_000 + 2*100_000 + 2*100_000 + 4*400_000 + 4*100_000 + 50_000
	if rc.Total != total {
		t.Fatalf("total_work %d, want %d", rc.Total, total)
	}
	// Exactly the total passes; one below refuses the last ticket, publish.
	if p, rc, _ := BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: total}}); p == nil || rc.Total != total {
		t.Fatal("the exact total is refused")
	}
	p, rc, ds = BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: total - 1}})
	if p != nil || rc.Refused == nil || rc.Refused.Ticket != TicketPublish || rc.Refused.Requested != 50_000 || rc.Refused.Remaining != 49_999 {
		t.Fatalf("one below the total: %+v %v", rc.Refused, ds)
	}
	// The classic output alone: the M2 tickets are not used for a loft.
	_, rc, _ = BuildWith(f, "classic", Options{})
	for _, c := range rc.Charges {
		if c.Ticket == TicketSurface {
			t.Error("the helmet is charged the M2 surface ticket")
		}
	}
	// The M2 table is untouched and the new tickets are listed apart.
	if len(Tickets()) != 8 || len(BranchTickets()) != 2 || BranchTickets()[0].Name != "select" || BranchTickets()[1].Name != "surface_branches" {
		t.Fatalf("tables %v %v", Tickets(), BranchTickets())
	}
}

func TestHelmetBranchClassBounds(t *testing.T) {
	src, err := os.ReadFile(filepath.FromSlash(helmetPath))
	if err != nil {
		t.Fatal(err)
	}
	// Seventeen runtime branches are over the class of 16.
	var extra strings.Builder
	for i := 0; i < 14; i++ {
		fmt.Fprintf(&extra, "  part extra_%d {\n    when stripe {\n      tube { radius = 0.006 point (0, 0, 0) point (0, 0.1, 0) }\n    }\n  }\n", i)
	}
	s := strings.Replace(string(src), "  part visor_shield {", extra.String()+"  part visor_shield {", 1)
	s = strings.Replace(s, "draw_calls = 3", "draw_calls = 30", 1)
	s = strings.Replace(s, "draw_calls_worst = 4", "draw_calls_worst = 40", 1)
	p, rc, ds := BuildWith(lowerGeometry(t, []byte(s)), "classic", Options{})
	if p != nil || rc == nil || rc.Refused == nil || rc.Refused.Ticket != TicketSelect || !strings.Contains(rc.Refused.Bound, "selectable branches") {
		t.Fatalf("17 branches: %+v %v", rc, ds)
	}
	if len(limitDiags(ds)) != 1 {
		t.Fatalf("diagnostics %v", ds)
	}
	// Over 4096 stored triangles in a level is over the surface_branches class.
	s = strings.Replace(string(src), "segments = 24\n        caps = off\n        columns = (y, rx, rz, drop)", "segments = 600\n        caps = off\n        columns = (y, rx, rz, drop)", 1)
	for _, r := range [][2]string{{"triangles = 2200", "triangles = 20000"}, {"triangles_worst = 2400", "triangles_worst = 24000"}, {"triangles_stored = 2700", "triangles_stored = 27000"}} {
		s = strings.Replace(s, r[0], r[1], 1)
	}
	p, rc, ds = BuildWith(lowerGeometry(t, []byte(s)), "classic", Options{})
	if p != nil || rc.Refused == nil || rc.Refused.Ticket != TicketSurfaceBranches || !strings.Contains(rc.Refused.Bound, "the class allows 4096") {
		t.Fatalf("a large loft: %+v %v", rc, ds)
	}
}
