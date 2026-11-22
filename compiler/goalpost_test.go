package compiler

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/internal/pbrcolor"
	"m31labs.dev/greenware/kernel/build"
	"m31labs.dev/greenware/kernel/mesh"
)

// The predictions of plan.md section 4.2 and spec.md Appendix A.2: triangles
// and draws of each file of the goal post.
var predicted = []struct {
	output, file string
	tris, draws  int
}{
	{"stadium", "goalpost.glb", 1340, 3},
	{"stadium", "goalpost-lod1.glb", 696, 3},
	{"stadium", "goalpost-lod2.glb", 112, 1},
	{"practice", "goalpost-practice.glb", 1052, 2},
	{"practice", "goalpost-practice-lod1.glb", 504, 2},
	{"practice", "goalpost-practice-lod2.glb", 112, 1},
}

func TestMeasuredTrianglesAndDraws(t *testing.T) {
	p := buildGoalPost(t)
	var got []*File
	for _, o := range p.Outputs {
		got = append(got, o.Files...)
	}
	if len(got) != len(predicted) {
		t.Fatalf("built %d files, want %d", len(got), len(predicted))
	}
	for i, want := range predicted {
		f := got[i]
		if f.Name != want.file || p.Outputs[i/3].Name != want.output {
			t.Errorf("file %d is %s/%s, want %s/%s", i, p.Outputs[i/3].Name, f.Name, want.output, want.file)
		}
		if f.Triangles != want.tris || f.Draws != want.draws {
			t.Errorf("%s: %d triangles and %d draws, want %d and %d", f.Name, f.Triangles, f.Draws, want.tris, want.draws)
		}
		// The measured figures must also be what the encoded file says.
		d, err := gltf.Decode(f.GLB)
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		tris, draws := 0, 0
		for _, n := range d.Nodes {
			if n.Mesh == nil {
				continue
			}
			for _, pr := range d.Meshes[*n.Mesh].Primitives {
				tris += len(pr.Indices) / 3
				draws++
			}
		}
		if tris != f.Triangles || draws != f.Draws {
			t.Errorf("%s: decoded %d triangles and %d draws, measured %d and %d", f.Name, tris, draws, f.Triangles, f.Draws)
		}
		if f.Size != len(f.GLB) {
			t.Errorf("%s: size %d, bytes %d", f.Name, f.Size, len(f.GLB))
		}
	}
}

func TestEveryFileDecodesAndReencodes(t *testing.T) {
	for _, o := range buildGoalPost(t).Outputs {
		for _, f := range o.Files {
			d, err := gltf.Decode(f.GLB)
			if err != nil {
				t.Errorf("%s: decode: %v", f.Name, err)
				continue
			}
			again, err := gltf.Encode(d)
			if err != nil {
				t.Errorf("%s: re-encode: %v", f.Name, err)
				continue
			}
			if !bytes.Equal(again, f.GLB) {
				t.Errorf("%s: decode then encode changes the bytes", f.Name)
			}
		}
	}
}

// nodeNames returns the node names of a file in index order.
func nodeNames(d *gltf.Document) []string {
	var out []string
	for _, n := range d.Nodes {
		out = append(out, n.Name)
	}
	return out
}

func TestNodeTreeShape(t *testing.T) {
	sockets := []string{"socket.crossbar_centre", "socket.ribbon_l", "socket.ribbon_r", "socket.inside_l", "socket.inside_r"}
	colliders := []string{"collider.post_lower", "collider.crossbar", "collider.upright_l", "collider.upright_r"}
	p := buildGoalPost(t)
	for _, c := range []struct {
		output string
		level  int
		meshes []string
	}{
		{"stadium", 0, []string{"goalpost", "base", "pad"}},
		{"stadium", 1, []string{"goalpost", "base", "pad"}},
		{"stadium", 2, []string{"goalpost"}},
		{"practice", 0, []string{"goalpost", "base"}},
		{"practice", 1, []string{"goalpost", "base"}},
		{"practice", 2, []string{"goalpost"}},
	} {
		var f *File
		for _, o := range p.Outputs {
			if o.Name == c.output {
				f = o.Files[c.level]
			}
		}
		d := f.Doc
		name := fmt.Sprintf("%s lod %d", c.output, c.level)
		var meshNodes []string
		have := map[string]int{}
		for i, n := range d.Nodes {
			have[n.Name] = i
			if n.Mesh != nil {
				meshNodes = append(meshNodes, n.Name)
				if len(d.Meshes[*n.Mesh].Primitives) == 0 {
					t.Errorf("%s: mesh node %s has no primitives", name, n.Name)
				}
			}
		}
		if strings.Join(meshNodes, ",") != strings.Join(c.meshes, ",") {
			t.Errorf("%s: mesh nodes %v, want %v", name, meshNodes, c.meshes)
		}
		if len(d.Nodes) != len(c.meshes)+len(sockets)+len(colliders) {
			t.Errorf("%s: %d nodes: %v", name, len(d.Nodes), nodeNames(d))
		}
		// Sockets are children of goalpost; colliders have no mesh.
		root := d.Nodes[0]
		if root.Name != "goalpost" || len(d.Scenes) != 1 || len(d.Scenes[0].Nodes) != 1 || d.Scenes[0].Nodes[0] != 0 {
			t.Fatalf("%s: root is %q, scenes %+v", name, root.Name, d.Scenes)
		}
		kids := map[int]bool{}
		for _, k := range root.Children {
			kids[k] = true
		}
		for _, s := range sockets {
			i, ok := have[s]
			if !ok || !kids[i] || d.Nodes[i].Mesh != nil {
				t.Errorf("%s: %s missing, not a child of goalpost, or has a mesh", name, s)
			}
		}
		for _, cn := range colliders {
			i, ok := have[cn]
			if !ok || d.Nodes[i].Mesh != nil || len(d.Nodes[i].Extras) == 0 {
				t.Errorf("%s: %s missing, has a mesh, or has no shape data", name, cn)
			}
		}
		if (c.meshes[len(c.meshes)-1] == "pad") != (have["pad"] != 0) {
			t.Errorf("%s: pad presence", name)
		}
	}
}

func TestSocketAndColliderValues(t *testing.T) {
	p := buildGoalPost(t)
	d := p.Outputs[0].Files[0].Doc
	pos := map[string][3]float32{}
	for _, n := range d.Nodes {
		if strings.HasPrefix(n.Name, "socket.") && n.Translation != nil {
			pos[n.Name] = *n.Translation
		}
	}
	want := map[string][3]float32{
		"socket.crossbar_centre": {0, 3.05, 0},
		"socket.ribbon_l":        {2.87, 12.19, 0},
		"socket.ribbon_r":        {-2.87, 12.19, 0},
		"socket.inside_l":        {2.82, 3.05, 0},
		"socket.inside_r":        {-2.82, 3.05, 0},
	}
	for k, w := range want {
		g := pos[k]
		for i := range w {
			if math.Abs(float64(g[i]-w[i])) > 1e-6 {
				t.Errorf("%s at %v, want %v", k, g, w)
			}
		}
	}
	// The practice post's uprights are 6.1 m high.
	pd := p.Outputs[1].Files[0].Doc
	for _, n := range pd.Nodes {
		if n.Name == "socket.ribbon_l" && math.Abs(float64(n.Translation[1])-9.15) > 1e-5 {
			t.Errorf("practice ribbon_l at %v", *n.Translation)
		}
	}
	// A collider's data is in its extras.
	for _, n := range d.Nodes {
		if n.Name == "collider.upright_r" && !strings.Contains(string(n.Extras), `"from":[-2.87,3.05,0]`) {
			t.Errorf("collider.upright_r extras: %s", n.Extras)
		}
	}
}

func TestRuntimeBinding(t *testing.T) {
	p := buildGoalPost(t)
	for _, i := range []int{0, 1} { // stadium lod 0 and 1 carry the pad
		extras := string(p.Outputs[0].Files[i].Doc.Extras)
		for _, want := range []string{
			`"format":"greenware.runtime/1"`, `"name":"pad_color"`, `"type":"color"`, `"default":"#1d4e89"`,
			`"field":"baseColorFactor"`, `"kind":"material"`, `"map":"srgb_to_linear"`, `"material":"pad_cover"`,
		} {
			if !strings.Contains(extras, want) {
				t.Errorf("stadium lod %d extras lack %s: %s", i, want, extras)
			}
		}
	}
	// No file without the pad material keeps a binding.
	for _, f := range []*File{p.Outputs[0].Files[2], p.Outputs[1].Files[0], p.Outputs[1].Files[1], p.Outputs[1].Files[2]} {
		if strings.Contains(string(f.Doc.Extras), "runtime") {
			t.Errorf("%s keeps a runtime table: %s", f.Name, f.Doc.Extras)
		}
	}
	// pad_cover's factor is the default colour in linear light.
	for _, m := range p.Outputs[0].Files[0].Doc.Materials {
		if m.Name != "pad_cover" {
			continue
		}
		want := [4]float64{0.0123, 0.0762, 0.2502, 1}
		for i := range want {
			if math.Abs(float64(m.BaseColor[i])-want[i]) > 5e-5 {
				t.Errorf("pad_cover baseColorFactor %v, want about %v", m.BaseColor, want)
			}
		}
	}
}

func TestSRGBConversion(t *testing.T) {
	for b := 0; b < 256; b++ {
		c := float64(b) / 255
		want := c / 12.92
		if c > 0.04045 {
			want = math.Pow((c+0.055)/1.055, 2.4)
		}
		if got := pbrcolor.Linear(uint8(b)); math.Abs(got-want) > 1e-14 {
			t.Errorf("pbrcolor.Linear(%d) = %.17g, want %.17g", b, got, want)
		}
	}
	// The crate colour of spec Appendix A.1: #8a6b48 -> (0.2542, 0.1470, 0.0648).
	got := [3]float64{pbrcolor.Linear(0x8a), pbrcolor.Linear(0x6b), pbrcolor.Linear(0x48)}
	for i, w := range [3]float64{0.2542, 0.1470, 0.0648} {
		if math.Abs(got[i]-w) > 5e-5 {
			t.Errorf("crate channel %d = %v, want about %v", i, got[i], w)
		}
	}
}

// trisOf decodes a primitive as a list of triangles, nine floats each.
func trisOf(p gltf.Primitive) [][9]float32 {
	var out [][9]float32
	for i := 0; i+2 < len(p.Indices); i += 3 {
		var t [9]float32
		for k := 0; k < 3; k++ {
			v := int(p.Indices[i+k])
			copy(t[3*k:], p.Positions[3*v:3*v+3])
		}
		out = append(out, t)
	}
	return out
}

func refTris(m *mesh.Mesh) [][9]float32 {
	var out [][9]float32
	for _, tri := range m.Tris {
		var t [9]float32
		for k, ci := range tri {
			f := m.Positions[m.Corners[ci].Pos].F32()
			copy(t[3*k:], f[:])
		}
		out = append(out, t)
	}
	return out
}

// The compiler builds from the resolved source values; kernel/build's goal
// post helper builds from hand-coded numbers. They must agree triangle by
// triangle, to float32 rounding.
func TestAgreesWithKernelReference(t *testing.T) {
	p := buildGoalPost(t)
	for _, c := range []struct {
		output string
		rise   float64
		noPad  bool
	}{{"stadium", 0, false}, {"practice", 6.1, true}} {
		var op *OutputProduct
		for _, o := range p.Outputs {
			if o.Name == c.output {
				op = o
			}
		}
		for lvl, f := range op.Files {
			opts := build.GoalPostLevel(lvl)
			opts.UprightRise = c.rise
			opts.NoPadding = c.noPad
			ref, err := build.GoalPost(opts)
			if err != nil {
				t.Fatal(err)
			}
			mats := map[string][][9]float32{}
			for _, n := range f.Doc.Nodes {
				if n.Mesh == nil {
					continue
				}
				for _, pr := range f.Doc.Meshes[*n.Mesh].Primitives {
					mats[f.Doc.Materials[*pr.Material].Name] = trisOf(pr)
				}
			}
			want := map[string][][9]float32{"paint": refTris(ref.Frame())}
			if b := ref.Base(); b != nil {
				want["steel"] = refTris(b)
			}
			if ref.Pad != nil {
				want["pad_cover"] = refTris(ref.Pad)
			}
			if len(mats) != len(want) {
				t.Errorf("%s lod %d: %d materials, want %d", c.output, lvl, len(mats), len(want))
			}
			for name, w := range want {
				g := mats[name]
				if len(g) != len(w) {
					t.Errorf("%s lod %d %s: %d triangles, want %d", c.output, lvl, name, len(g), len(w))
					continue
				}
				for i := range w {
					for k := range w[i] {
						if math.Abs(float64(g[i][k]-w[i][k])) > 2e-6 {
							t.Fatalf("%s lod %d %s triangle %d differs: %v vs %v", c.output, lvl, name, i, g[i], w[i])
						}
					}
				}
			}
		}
	}
}

// ---- determinism --------------------------------------------------------------

func readDir(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = b
	}
	return out
}

func sameDirs(t *testing.T, what string, a, b map[string][]byte) {
	t.Helper()
	if len(a) != len(b) {
		t.Errorf("%s: %d files against %d", what, len(a), len(b))
	}
	for name, x := range a {
		if !bytes.Equal(x, b[name]) {
			t.Errorf("%s: %s differs", what, name)
		}
	}
}

func TestWriteIsByteIdenticalAcrossRunsAndPaths(t *testing.T) {
	base := t.TempDir()
	// A second copy of the source at another path: the bytes must not depend
	// on where the checkout is.
	src, err := os.ReadFile(goalpostPath)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(base, "some", "other", "checkout", "assets")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "goalpost.gw"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	dirs := []string{filepath.Join(base, "one"), filepath.Join(base, "two"), filepath.Join(base, "three")}
	for i, dir := range dirs {
		path := goalpostPath
		if i == 2 {
			path = filepath.Join(other, "goalpost.gw")
		}
		p, ds := Build(loadFile(t, path), "")
		if p == nil {
			t.Fatal(ds)
		}
		if err := Write(p, dir); err != nil {
			t.Fatal(err)
		}
	}
	a := readDir(t, dirs[0])
	if len(a) != 7 {
		t.Fatalf("wrote %d files, want 6 GLBs and the record", len(a))
	}
	sameDirs(t, "second run", a, readDir(t, dirs[1]))
	sameDirs(t, "other path", a, readDir(t, dirs[2]))
	// No temporary file is left behind.
	for name := range a {
		if strings.HasPrefix(name, ".") {
			t.Errorf("leftover %s", name)
		}
	}
}

// TestSubprocessBuild is the child of TestBytesDoNotDependOnGOMAXPROCS.
func TestSubprocessBuild(t *testing.T) {
	dir := os.Getenv("GREENWARE_TEST_OUT")
	if dir == "" {
		t.Skip("child only")
	}
	p, ds := Build(loadFile(t, goalpostPath), "")
	if p == nil {
		t.Fatal(ds)
	}
	if err := Write(p, dir); err != nil {
		t.Fatal(err)
	}
}

func TestBytesDoNotDependOnGOMAXPROCS(t *testing.T) {
	base := t.TempDir()
	var outs []map[string][]byte
	for _, procs := range []string{"1", "8"} {
		dir := filepath.Join(base, "p"+procs)
		cmd := exec.Command(os.Args[0], "-test.run=^TestSubprocessBuild$", "-test.count=1")
		cmd.Env = append(os.Environ(), "GOMAXPROCS="+procs, "GREENWARE_TEST_OUT="+dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("GOMAXPROCS=%s: %v\n%s", procs, err, out)
		}
		outs = append(outs, readDir(t, dir))
	}
	if len(outs[0]) != 7 {
		t.Fatalf("child wrote %d files", len(outs[0]))
	}
	sameDirs(t, "GOMAXPROCS 1 against 8", outs[0], outs[1])
	// And against this process.
	here := filepath.Join(base, "here")
	if err := Write(buildGoalPost(t), here); err != nil {
		t.Fatal(err)
	}
	sameDirs(t, "child against parent", outs[0], readDir(t, here))
}

// ---- budget ----------------------------------------------------------------

func TestBudgetViolationStopsPublication(t *testing.T) {
	src := goalpostSource(t, "triangles = 1600          // drawn at lod 0 in the default look", "triangles = 1300")
	p, ds := Build(lowerSource(t, src), "")
	if p != nil {
		t.Fatal("a product came back for a violated budget")
	}
	var got *diag.Diagnostic
	for i := range ds {
		if ds[i].Code == diag.CodeBudget {
			got = &ds[i]
		}
	}
	if got == nil {
		t.Fatalf("no GW-BUDGET in %v", ds)
	}
	if !strings.Contains(got.Message, "1340") || !strings.Contains(got.Message, "1300") {
		t.Errorf("message does not give the measure and the limit: %s", got.Message)
	}
	if got.Span.Len() == 0 || !strings.Contains(string(src[got.Span.Start:got.Span.End]), "1300") {
		t.Errorf("span %v does not cover the limit", got.Span)
	}
	dir := filepath.Join(t.TempDir(), "out")
	if err := Write(p, dir); err == nil {
		t.Error("Write accepted a nil product")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the output directory was created")
	}
}

func TestOutputBudgetOverridesAndFileSize(t *testing.T) {
	// The practice post's own budget (draw_calls = 2) holds; tighten it to 1
	// and the build fails at the practice output only.
	src := goalpostSource(t, "budget { materials = 2 draw_calls = 2 draw_calls_worst = 2 }", "budget { materials = 2 draw_calls = 1 draw_calls_worst = 1 }")
	p, ds := Build(lowerSource(t, src), "stadium")
	if p == nil {
		t.Fatalf("stadium alone should build: %v", ds)
	}
	if p, ds := Build(lowerSource(t, src), "practice"); p != nil || !diag.HasErrors(ds) {
		t.Errorf("practice with draw_calls = 1 built: %v", ds)
	}
	// file_size is measured on the encoded file.
	src = goalpostSource(t, "file_size = 96KiB", "file_size = 40000")
	p, ds = Build(lowerSource(t, src), "")
	if p != nil {
		t.Fatal("a 47100-byte file passed a 40000-byte limit")
	}
	if len(ds) == 0 || ds[0].Code != diag.CodeBudget || !strings.Contains(ds[0].Message, "file_size") {
		t.Errorf("diagnostics: %v", ds)
	}
}

// ---- interrupted writes -------------------------------------------------------

func TestInterruptedWriteLeavesNoRecord(t *testing.T) {
	p := buildGoalPost(t)
	dir := t.TempDir()
	// An earlier, complete build leaves an old record.
	if err := Write(p, dir); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(dir, p.RecordName())
	if _, err := os.Stat(record); err != nil {
		t.Fatal(err)
	}
	var order []string
	calls := 0
	old := renameFile
	defer func() { renameFile = old }()
	renameFile = func(from, to string) error {
		calls++
		if calls == 4 {
			return fmt.Errorf("simulated failure")
		}
		order = append(order, filepath.Base(to))
		return old(from, to)
	}
	if err := Write(p, dir); err == nil {
		t.Fatal("the simulated failure was not reported")
	}
	if _, err := os.Stat(record); err == nil {
		t.Error("an interrupted build left a record")
	}
	want := []string{"goalpost.glb", "goalpost-lod1.glb", "goalpost-lod2.glb"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("files written before the failure: %v, want %v", order, want)
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file %s left behind", e.Name())
		}
	}
}

func TestWriteOrderIsFixedAndRecordIsLast(t *testing.T) {
	p := buildGoalPost(t)
	var order []string
	old := renameFile
	defer func() { renameFile = old }()
	renameFile = func(from, to string) error {
		order = append(order, filepath.Base(to))
		return old(from, to)
	}
	if err := Write(p, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	want := "goalpost.glb,goalpost-lod1.glb,goalpost-lod2.glb,goalpost-practice.glb,goalpost-practice-lod1.glb,goalpost-practice-lod2.glb,goalpost.build.json"
	if strings.Join(order, ",") != want {
		t.Errorf("order %v", order)
	}
}

// ---- golden hashes -------------------------------------------------------------

const goldenPath = "../testdata/compiler/goalpost.candidate.sha256"

func goldenText(p *Product) string {
	var b strings.Builder
	b.WriteString("# sha256 bytes name: the six goal post files from goalpost.gw (candidate; a reviewer approves it)\n")
	var lines []string
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			lines = append(lines, fmt.Sprintf("%s %d %s\n", f.SHA256, len(f.GLB), f.Name))
		}
	}
	sortStrings(lines, func(l string) string { return l[strings.LastIndex(l, " ")+1:] })
	for _, l := range lines {
		b.WriteString(l)
	}
	return b.String()
}

func sortStrings(ss []string, key func(string) string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && key(ss[j]) < key(ss[j-1]); j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}

func TestGoldenHashes(t *testing.T) {
	got := goldenText(buildGoalPost(t))
	if os.Getenv("GREENWARE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("the goal post files changed.\nrecorded:\n%s\nbuilt:\n%s", want, got)
	}
}
