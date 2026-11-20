package accept

import (
	"encoding/json"
	"math"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/lang"
)

// This file builds tiny GLB documents by hand with gltf.Encode. It does not
// use the compiler: the acceptance evaluator must be tested against meshes
// whose answers are known from geometry.

// geom is a triangle soup under construction.
type geom struct {
	pos []float32
	idx []uint32
}

func (g *geom) vert(x, y, z float64) uint32 {
	g.pos = append(g.pos, float32(x), float32(y), float32(z))
	return uint32(len(g.pos)/3 - 1)
}

func (g *geom) tri(a, b, c uint32) { g.idx = append(g.idx, a, b, c) }

// capKind says how a prism end is closed.
type capKind int

const (
	capNone   capKind = iota // left open
	capCentre                // a fan around a centre vertex
	capFan                   // a fan from the first ring vertex, no centre
)

// prismY adds an n-gon prism along y. The ring has circumradius r and its
// first vertex lies at angle phase from +x toward +z. The ring repeats its
// first position at the seam and the caps have vertices of their own, so a
// weld on exactly equal positions has to close it.
func (g *geom) prismY(cx, cz, r, y0, y1 float64, n int, phase float64, bottomCap, topCap capKind) {
	ring := func(k int) (x, z float64) {
		a := phase + 2*math.Pi*float64(k)/float64(n)
		return cx + r*math.Cos(a), cz + r*math.Sin(a)
	}
	// Side faces, outward. Seam vertex k = n repeats k = 0.
	bot := make([]uint32, n+1)
	top := make([]uint32, n+1)
	for k := 0; k <= n; k++ {
		x, z := ring(k % n)
		if k == n {
			// Compute the seam copy from k = 0 so the bits are identical.
			x, z = ring(0)
		}
		bot[k] = g.vert(x, y0, z)
		top[k] = g.vert(x, y1, z)
	}
	for k := 0; k < n; k++ {
		g.tri(bot[k], top[k+1], bot[k+1])
		g.tri(bot[k], top[k], top[k+1])
	}
	closeEnd := func(y float64, up bool, kind capKind) {
		switch kind {
		case capCentre:
			c := g.vert(cx, y, cz)
			var first, prev uint32
			for k := 0; k < n; k++ {
				x, z := ring(k)
				v := g.vert(x, y, z)
				if k == 0 {
					first = v
				} else if up {
					g.tri(c, v, prev)
				} else {
					g.tri(c, prev, v)
				}
				prev = v
			}
			if up {
				g.tri(c, first, prev)
			} else {
				g.tri(c, prev, first)
			}
		case capFan:
			vs := make([]uint32, n)
			for k := 0; k < n; k++ {
				x, z := ring(k)
				vs[k] = g.vert(x, y, z)
			}
			for k := 1; k+1 < n; k++ {
				if up {
					g.tri(vs[0], vs[k+1], vs[k])
				} else {
					g.tri(vs[0], vs[k], vs[k+1])
				}
			}
		}
	}
	closeEnd(y1, true, topCap)
	closeEnd(y0, false, bottomCap)
}

// swapXY exchanges the x and y coordinates of every vertex added since
// start and reverses those triangles' winding, turning a y prism into an x
// prism.
func (g *geom) swapXY(vertStart, idxStart int) {
	for v := vertStart; v < len(g.pos)/3; v++ {
		g.pos[3*v], g.pos[3*v+1] = g.pos[3*v+1], g.pos[3*v]
	}
	for i := idxStart; i+2 < len(g.idx); i += 3 {
		g.idx[i+1], g.idx[i+2] = g.idx[i+2], g.idx[i+1]
	}
}

// box adds a closed axis-aligned box with its own vertices.
func (g *geom) box(x0, y0, z0, x1, y1, z1 float64) {
	// Faces with outward winding.
	type face [4][3]float64
	f := []face{
		{{x0, y0, z1}, {x1, y0, z1}, {x1, y1, z1}, {x0, y1, z1}}, // +z
		{{x1, y0, z0}, {x0, y0, z0}, {x0, y1, z0}, {x1, y1, z0}}, // -z
		{{x1, y0, z1}, {x1, y0, z0}, {x1, y1, z0}, {x1, y1, z1}}, // +x
		{{x0, y0, z0}, {x0, y0, z1}, {x0, y1, z1}, {x0, y1, z0}}, // -x
		{{x0, y1, z1}, {x1, y1, z1}, {x1, y1, z0}, {x0, y1, z0}}, // +y
		{{x0, y0, z0}, {x1, y0, z0}, {x1, y0, z1}, {x0, y0, z1}}, // -y
	}
	// Shared corner vertices, so welding is by position equality alone.
	corner := map[[3]float64]uint32{}
	get := func(p [3]float64) uint32 {
		if v, ok := corner[p]; ok {
			return v
		}
		v := g.vert(p[0], p[1], p[2])
		corner[p] = v
		return v
	}
	for _, q := range f {
		a, b, c, d := get(q[0]), get(q[1]), get(q[2]), get(q[3])
		g.tri(a, b, c)
		g.tri(a, c, d)
	}
}

func (g *geom) primitive() gltf.Primitive {
	return gltf.Primitive{Positions: g.pos, Indices: g.idx}
}

// goalpostModel describes a synthetic goal post.
type goalpostModel struct {
	halfGap    float64 // x of each upright's inside face at its nearest point
	r          float64 // upright circumradius
	n          int     // upright segments
	phase      float64 // ring phase of the upright prisms
	movedRight float64 // extra outward shift of the right upright, mesh only
	crossbar   bool
	pad        bool
	base       bool
	extraDraw  bool  // a fourth drawn primitive
	hidePad    bool  // pad kept in the file, hidden by KHR_node_visibility
	drop       []int // triangle indices of the frame to drop (an open hole)
	// stack adds a second prism that sits exactly on the top of the left
	// upright (same ring, same plane), the exact-coincidence rim control.
	stack bool
	// floater adds a separate box at the given gap above the frame; 0 = none.
	floaterGap float64
	// noSocketMove keeps the sockets where the rule puts them even when the
	// mesh moved (the regression control).
}

func defaultModel() goalpostModel {
	return goalpostModel{halfGap: 2.82, r: 0.05, n: 12, phase: 0, crossbar: true, pad: true, base: true}
}

const (
	crossY = 3.05
	topY   = 12.19
)

func (m goalpostModel) frame() *geom {
	g := &geom{}
	cx := m.halfGap + m.r // centre of the right upright
	g.prismY(-cx, 0, m.r, crossY-0.06, topY, m.n, m.phase, capCentre, capCentre)
	g.prismY(cx+m.movedRight, 0, m.r, crossY-0.06, topY, m.n, m.phase, capCentre, capCentre)
	if m.crossbar {
		v0, i0 := len(g.pos)/3, len(g.idx)
		g.prismY(0, 0, 0.06, -cx, cx, 16, 0, capCentre, capCentre)
		g.swapXY(v0, i0)
		// swapXY put the bar's axis on x and its centre line at y = 0, z = 0;
		// lift it to the crossbar height. (The ring of the y prism ran in x-z,
		// now it runs in y-z.)
		for v := v0; v < len(g.pos)/3; v++ {
			g.pos[3*v+1] += float32(crossY)
		}
	}
	if m.stack {
		// A second capped prism standing exactly on the left upright's top
		// cap: same ring positions, same plane.
		g.prismY(-cx, 0, m.r, topY, topY+0.5, m.n, m.phase, capFan, capCentre)
	}
	if m.floaterGap > 0 {
		y0 := topY + 0.0 + m.floaterGap
		g.box(-0.1, y0, -0.1, 0.1, y0+0.2, 0.1)
	}
	if len(m.drop) > 0 {
		drop := map[int]bool{}
		for _, d := range m.drop {
			drop[d] = true
		}
		var keep []uint32
		for t := 0; 3*t < len(g.idx); t++ {
			if !drop[t] {
				keep = append(keep, g.idx[3*t:3*t+3]...)
			}
		}
		g.idx = keep
	}
	return g
}

// document builds the glTF document. Sockets are meshless nodes named
// socket.<name>, children of the root node "goalpost".
func (m goalpostModel) document() *gltf.Document {
	doc := &gltf.Document{Asset: gltf.Asset{Generator: "accept test"}}
	frame := m.frame()
	doc.Meshes = append(doc.Meshes, gltf.Mesh{Name: "frame", Primitives: []gltf.Primitive{frame.primitive()}})
	root := gltf.Node{Name: "goalpost", Mesh: gltf.Int(0)}
	addChild := func(n gltf.Node) {
		doc.Nodes = append(doc.Nodes, n)
		root.Children = append(root.Children, len(doc.Nodes)-1) // root is index 0
	}
	doc.Nodes = append(doc.Nodes, gltf.Node{}) // placeholder for root
	sock := func(name string, x, y, z float64) {
		addChild(gltf.Node{Name: "socket." + name, Translation: &[3]float32{float32(x), float32(y), float32(z)}})
	}
	sock("crossbar_centre", 0, crossY, 0)
	sock("inside_l", -m.halfGap, crossY, 0)
	sock("inside_r", m.halfGap, crossY, 0)
	if m.base {
		b := &geom{}
		b.prismY(0, 1.83, 0.35, 0, 0.03, 24, 0, capCentre, capCentre)
		doc.Meshes = append(doc.Meshes, gltf.Mesh{Name: "base", Primitives: []gltf.Primitive{b.primitive()}})
		addChild(gltf.Node{Name: "base", Mesh: gltf.Int(len(doc.Meshes) - 1)})
	}
	if m.pad {
		p := &geom{}
		p.prismY(0, 1.83, 0.26, 0, 1.8, 24, 0, capCentre, capCentre)
		doc.Meshes = append(doc.Meshes, gltf.Mesh{Name: "pad", Primitives: []gltf.Primitive{p.primitive()}})
		n := gltf.Node{Name: "pad", Mesh: gltf.Int(len(doc.Meshes) - 1)}
		if m.hidePad {
			n.Extensions = map[string]json.RawMessage{"KHR_node_visibility": json.RawMessage(`{"visible":false}`)}
		}
		addChild(n)
	}
	if m.extraDraw {
		e := &geom{}
		e.box(5, 0, 5, 5.1, 0.1, 5.1)
		doc.Meshes = append(doc.Meshes, gltf.Mesh{Name: "extra", Primitives: []gltf.Primitive{e.primitive()}})
		addChild(gltf.Node{Name: "extra", Mesh: gltf.Int(len(doc.Meshes) - 1)})
	}
	doc.Nodes[0] = root
	doc.Scenes = []gltf.Scene{{Nodes: []int{0}}}
	doc.Scene = gltf.Int(0)
	return doc
}

// roundTrip encodes the document to a GLB and decodes it again, so the tests
// run on what a file reader sees.
func roundTrip(t testing.TB, doc *gltf.Document) *gltf.Document {
	t.Helper()
	b, err := gltf.Encode(doc)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := gltf.Decode(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func encode(t testing.TB, doc *gltf.Document) []byte {
	t.Helper()
	b, err := gltf.Encode(doc)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}

// parseAccept lowers acceptance source text.
func parseAccept(t testing.TB, src string) *ast.Accept {
	t.Helper()
	f, ds := ast.Lower(lang.Parse([]byte(src)))
	if len(ds) != 0 || len(f.Accepts) != 1 {
		t.Fatalf("accept source: %v (accepts %d)", ds, len(f.Accepts))
	}
	return f.Accepts[0]
}

// chordError is the largest chord error of a regular n-gon of circumradius r.
func chordError(r float64, n int) float64 {
	return r * (1 - math.Cos(math.Pi/float64(n)))
}

// sanity: the prisms wind outward, so the signed volume is positive.
func signedVolume(g *geom) float64 {
	var tris []tri
	for i := 0; i+2 < len(g.idx); i += 3 {
		var t tri
		for k := 0; k < 3; k++ {
			v := g.idx[i+k]
			t[k] = mesh.Vec3{float64(g.pos[3*v]), float64(g.pos[3*v+1]), float64(g.pos[3*v+2])}
		}
		tris = append(tris, t)
	}
	return meshOf(tris).SignedVolume()
}
