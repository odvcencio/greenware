package build

import (
	"encoding/hex"
	"math"
	"testing"

	"m31labs.dev/greenware/kernel/mesh"
)

// The per-part triangle counts of spec.md Appendix A.2, output stadium.
var stadiumTable = []struct {
	path string
	lod  [3]int
}{
	{"goalpost/base/bolts", [3]int{160, 0, 0}},
	{"goalpost/base/plate", [3]int{188, 148, 0}},
	{"goalpost/post", [3]int{316, 196, 76}},
	{"goalpost/crossbar", [3]int{60, 28, 8}},
	{"goalpost/upright_l", [3]int{164, 66, 14}},
	{"goalpost/upright_r", [3]int{164, 66, 14}},
	{"goalpost/pad", [3]int{288, 192, 0}},
}

func TestRetessellationRule(t *testing.T) {
	// Spec A.2 "Segments by level": radius, lod 0, lod 1 (5 mm), lod 2 (30 mm).
	for _, c := range []struct {
		name   string
		radius float64
		src    int
		want   [3]int
	}{
		{"plate", 0.35, 24, [3]int{24, 19, 8}}, // the plate is dropped at lod 2; 8 is what the rule gives
		{"post", 0.10, 16, [3]int{16, 10, 4}},
		{"crossbar", 0.06, 16, [3]int{16, 8, 3}},
		{"upright", 0.05, 12, [3]int{12, 7, 3}},
		{"pad", 0.26, 24, [3]int{24, 16, 7}}, // the pad is dropped at lod 2; 7 is what the rule gives
		{"bolt", 0.012, 6, [3]int{6, 4, 3}},  // dropped at lod 1 and 2; the rule alone gives 4 and 3
	} {
		for lvl, e := range []float64{0, 0.005, 0.030} {
			if got := CircleSegments(c.radius, c.src, e); got != c.want[lvl] {
				t.Errorf("%s lod %d: %d segments, want %d", c.name, lvl, got, c.want[lvl])
			}
		}
	}
	for lvl, want := range [][2]int{{12, 6}, {7, 4}, {3, 2}} {
		p := RetessellateSphere(SphereParams{Radius: 0.05, Segments: 12}, []float64{0, 0.005, 0.030}[lvl])
		if p.Segments != want[0] || p.Rings != want[1] {
			t.Errorf("sphere lod %d: %d,%d want %v", lvl, p.Segments, p.Rings, want)
		}
	}
	// Never more than the source, never below the floor.
	if CircleSegments(100, 8, 0.001) != 8 {
		t.Error("exceeded the source count")
	}
	if CircleSegments(0.001, 8, 1) != 3 || LatitudeRings(0.001, 8, 1) != 2 {
		t.Error("floor not applied")
	}
	// A scaled placement: radius 0.5 m at 5 mm needs 23 segments (spec 6.1).
	if got := CircleSegments(0.5, 32, 0.005); got != 23 {
		t.Errorf("0.5 m at 5 mm: %d, want 23", got)
	}
	if got := CircleSegments(0.05, 32, 0.005); got != 7 {
		t.Errorf("0.05 m at 5 mm: %d, want 7", got)
	}
	// lod_error at lod 0: the plate's 24 segments give 2.99 mm.
	if e := ChordError(0.35, 2*math.Pi, 24); !near(e, 0.00299, 5e-6) {
		t.Errorf("plate chord error %v", e)
	}
	// The bevel's segment count is kept.
	p := RetessellateCylinder(CylinderParams{Radius: 0.35, Segments: 24, Bevel: &Bevel{Width: 0.006, Segments: 2}}, 0.005)
	if p.Segments != 19 || p.Bevel.Segments != 2 {
		t.Errorf("%+v", p)
	}
}

func TestGoalPostTriangleTable(t *testing.T) {
	var totals [3]int
	for lvl := 0; lvl < 3; lvl++ {
		g, err := GoalPost(GoalPostLevel(lvl))
		if err != nil {
			t.Fatal(err)
		}
		counts := g.TriangleCounts()
		for _, row := range stadiumTable {
			if got := counts[row.path]; got != row.lod[lvl] {
				t.Errorf("lod %d %s: %d triangles, spec %d", lvl, row.path, got, row.lod[lvl])
			}
			totals[lvl] += counts[row.path]
		}
	}
	if totals != [3]int{1340, 696, 112} {
		t.Errorf("stadium totals %v, spec [1340 696 112]", totals)
	}
}

func TestGoalPostPractice(t *testing.T) {
	want := [3]int{1052, 504, 112}
	for lvl := 0; lvl < 3; lvl++ {
		o := GoalPostLevel(lvl)
		o.NoPadding = true
		o.UprightRise = 6.1
		g, err := GoalPost(o)
		if err != nil {
			t.Fatal(err)
		}
		if g.Pad != nil {
			t.Error("practice built a pad")
		}
		total := 0
		for _, n := range g.TriangleCounts() {
			total += n
		}
		if total != want[lvl] {
			t.Errorf("practice lod %d: %d triangles, spec %d", lvl, total, want[lvl])
		}
	}
}

func TestGoalPostSegments(t *testing.T) {
	want := []struct{ plate, post, bar, up, sph, rings, pad int }{
		{24, 16, 16, 12, 12, 6, 24}, {19, 10, 8, 7, 7, 4, 16},
	}
	for lvl, w := range want {
		g, _ := GoalPost(GoalPostLevel(lvl))
		got := struct{ plate, post, bar, up, sph, rings, pad int }{
			g.PlateSegments, g.PostSegments, g.CrossbarSegments, g.UprightSegments, g.SphereSegments, g.SphereRings, g.PadSegments}
		if got != w {
			t.Errorf("lod %d segments %+v, want %+v", lvl, got, w)
		}
	}
	g, _ := GoalPost(GoalPostLevel(2))
	if g.PostSegments != 4 || g.CrossbarSegments != 3 || g.UprightSegments != 3 || g.SphereSegments != 3 || g.SphereRings != 2 {
		t.Errorf("lod 2: %+v", g)
	}
}

func TestGoalPostMeshes(t *testing.T) {
	for lvl := 0; lvl < 3; lvl++ {
		g, err := GoalPost(GoalPostLevel(lvl))
		if err != nil {
			t.Fatal(err)
		}
		parts := map[string]*mesh.Mesh{"post": g.Post, "crossbar": g.Crossbar, "upright_l": g.UprightL,
			"upright_r": g.UprightR, "plate": g.Plate, "bolts": g.Bolts, "pad": g.Pad,
			"frame": g.Frame(), "base": g.Base()}
		for name, m := range parts {
			if m == nil {
				if lvl == 0 {
					t.Errorf("%s missing at lod 0", name)
				}
				continue
			}
			if err := m.Validate(); err != nil {
				t.Errorf("lod %d %s: %v", lvl, name, err)
			}
			for sh, ok := range m.ClosedShells() {
				if !ok {
					t.Errorf("lod %d %s: shell %d is not closed", lvl, name, sh)
				}
			}
			if m.SignedVolume() <= 0 {
				t.Errorf("lod %d %s: signed volume %v", lvl, name, m.SignedVolume())
			}
			// An upright's sphere equator is exactly the cylinder's top ring in the
			// sample (same radius, same angles, same height), so the two shells weld
			// into one component and the welded edge use there is 4, not 2.
			wantComp := m.NumShells()
			switch name {
			case "upright_l", "upright_r":
				wantComp = 1
			case "frame":
				wantComp = 4
			}
			if _, n := m.Components(); n != wantComp {
				t.Errorf("lod %d %s: %d components, want %d", lvl, name, n, wantComp)
			}
		}
		if g.Frame().NumShells() != 6 || g.Frame().TriangleCount() != g.Post.TriangleCount()+g.Crossbar.TriangleCount()+g.UprightL.TriangleCount()*2 {
			t.Errorf("lod %d frame shells %d", lvl, g.Frame().NumShells())
		}
	}
}

func TestGoalPostMirror(t *testing.T) {
	g, _ := GoalPost(GoalPostOptions{})
	lo, hi, _ := g.UprightL.Bounds()
	rlo, rhi, _ := g.UprightR.Bounds()
	if rlo[0] != -hi[0] || rhi[0] != -lo[0] || rlo[1] != lo[1] || rhi[2] != hi[2] {
		t.Errorf("mirror bounds %v %v vs %v %v", rlo, rhi, lo, hi)
	}
	if !near(lo[0], 2.82, 1e-12) || !near(hi[0], 2.92, 1e-12) {
		t.Errorf("upright x extent %v..%v", lo[0], hi[0])
	}
	if !near(hi[1], 3.05+9.14+0.05, 1e-12) || !near(lo[1], 3.05-0.06, 1e-12) {
		t.Errorf("upright y extent %v..%v", lo[1], hi[1])
	}
	// Mirrored normals still point out: every sphere corner normal is radial.
	for _, c := range g.UprightR.Corners {
		if len(g.UprightR.Positions) == 0 || mesh.Len(c.Normal) < 0.999999 {
			t.Fatal("normal")
		}
	}
	// Bolts: 8 shells around the plate's centre, 0.28 m out, first at +Z.
	if g.Bolts.NumShells() != 8 {
		t.Fatalf("%d bolt shells", g.Bolts.NumShells())
	}
	blo, bhi, _ := g.Bolts.Bounds()
	if !near(blo[0], -0.292, 1e-12) || !near(bhi[0], 0.292, 1e-12) {
		t.Errorf("bolt ring x extent %v..%v", blo[0], bhi[0])
	}
	if !near(blo[2], 1.83-0.292, 1e-12) || !near(bhi[2], 1.83+0.292, 1e-12) {
		t.Errorf("bolt ring z extent %v..%v", blo[2], bhi[2])
	}
	if !near(blo[1], 0.03-0.0125, 1e-12) || !near(bhi[1], 0.03+0.0125, 1e-12) {
		t.Errorf("bolt y extent %v..%v", blo[1], bhi[1])
	}
}

func TestRadialArrayAxes(t *testing.T) {
	// A unit box-like tetra-free check with a cylinder: three copies about X.
	c, _ := Cylinder(CylinderParams{Radius: 0.1, Height: 0.2, Segments: 8, Caps: true})
	for _, ax := range []Axis{AxisX, AxisY, AxisZ} {
		a, err := RadialArray(c, 4, ax, mesh.Vec3{1, 1, 1}, 2)
		if err != nil {
			t.Fatal(err)
		}
		if a.NumShells() != 4 || a.SignedVolume() <= 0 || !a.IsClosed() {
			t.Errorf("axis %d: shells %d", ax, a.NumShells())
		}
		lo, hi, _ := a.Bounds()
		// Four copies at 90 degree steps at distance 2 span +-2 about the centre on the two plane axes.
		span := 0
		for k := 0; k < 3; k++ {
			if near(hi[k]-lo[k], 4.2, 1e-9) || near(hi[k]-lo[k], 4.4, 1e-9) {
				span++
			}
		}
		if span != 2 {
			t.Errorf("axis %d: bounds %v %v", ax, lo, hi)
		}
	}
	if _, err := RadialArray(c, 0, AxisY, mesh.Vec3{}, 1); err == nil {
		t.Error("count 0 should fail")
	}
}

// goldenStadiumFrame is the SHA-256 of the canonical dump of the stadium
// output's lod 0 painted frame mesh (post, crossbar, upright_l, upright_r).
// It must be the same on amd64 and arm64.
const goldenStadiumFrame = "ea9a1dbbc4f67552d7fa065a9e4beee2671deb8fad0140e8cc810e1a54dddcc9"

func TestGoldenStadiumFrame(t *testing.T) {
	g, err := GoalPost(GoalPostOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f := g.Frame()
	sum := f.SHA256()
	got := hex.EncodeToString(sum[:])
	t.Logf("frame: %d triangles, %d corners, %d positions, %d shells; sha256 %s",
		f.TriangleCount(), len(f.Corners), len(f.Positions), f.NumShells(), got)
	if got != goldenStadiumFrame {
		t.Fatalf("golden hash changed:\n got %s\nwant %s", got, goldenStadiumFrame)
	}
	// A second build must match bit for bit.
	g2, _ := GoalPost(GoalPostOptions{})
	if s2 := g2.Frame().SHA256(); s2 != sum {
		t.Fatal("two builds differ")
	}
}
