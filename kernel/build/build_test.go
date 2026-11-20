package build

import (
	"math"
	"testing"

	"m31labs.dev/greenware/kernel/mesh"
)

func check(t *testing.T, name string, m *mesh.Mesh, err error) *mesh.Mesh {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestCylinderCounts(t *testing.T) {
	cases := []struct {
		name string
		p    CylinderParams
		tris int
	}{
		{"bolt", CylinderParams{Radius: 0.012, Height: 0.025, Segments: 6, Caps: true}, 20},
		{"capped 12", CylinderParams{Radius: 0.05, Height: 1, Segments: 12, Caps: true}, 44},
		{"uncapped 12", CylinderParams{Radius: 0.05, Height: 1, Segments: 12}, 24},
		{"plate", CylinderParams{Radius: 0.35, Height: 0.03, Segments: 24, Caps: true,
			Bevel: &Bevel{Width: 0.006, Segments: 2, Edges: BevelTop}}, 188},
		{"plate 19", CylinderParams{Radius: 0.35, Height: 0.03, Segments: 19, Caps: true,
			Bevel: &Bevel{Width: 0.006, Segments: 2, Edges: BevelTop}}, 148},
		{"both edges", CylinderParams{Radius: 0.35, Height: 0.03, Segments: 24, Caps: true,
			Bevel: &Bevel{Width: 0.006, Segments: 2}}, 48 + 44 + 2*96},
		{"bottom edge", CylinderParams{Radius: 0.35, Height: 0.03, Segments: 24, Caps: true,
			Bevel: &Bevel{Width: 0.006, Segments: 1, Edges: BevelBottom}}, 48 + 44 + 48},
	}
	for _, c := range cases {
		for _, ax := range []Axis{AxisY, AxisX, AxisZ} {
			p := c.p
			p.Axis = ax
			p.Centre = mesh.Vec3{1, 2, 3}
			m, err := Cylinder(p)
			check(t, c.name, m, err)
			if m.TriangleCount() != c.tris {
				t.Errorf("%s axis %d: %d triangles, want %d", c.name, ax, m.TriangleCount(), c.tris)
			}
			if p.Caps != m.IsClosed() {
				t.Errorf("%s axis %d: closed=%v, want %v", c.name, ax, m.IsClosed(), p.Caps)
			}
			if m.NumShells() != 1 {
				t.Errorf("%s: %d shells", c.name, m.NumShells())
			}
			if _, n := m.Components(); n != 1 {
				t.Errorf("%s: %d components", c.name, n)
			}
			if p.Caps && m.SignedVolume() <= 0 {
				t.Errorf("%s axis %d: signed volume %v", c.name, ax, m.SignedVolume())
			}
			if !p.Caps && m.OpenEdges() != 2*p.Segments {
				t.Errorf("%s: %d open edges", c.name, m.OpenEdges())
			}
		}
	}
}

func TestCylinderGeometry(t *testing.T) {
	// A plain capped cylinder has the volume of its inscribed prism.
	n := 12
	r, h := 0.5, 2.0
	m, err := Cylinder(CylinderParams{Radius: r, Height: h, Segments: n, Caps: true, Axis: AxisX, Centre: mesh.Vec3{4, 5, 6}})
	check(t, "cyl", m, err)
	want := float64(n) / 2 * r * r * math.Sin(2*math.Pi/float64(n)) * h
	if !near(m.SignedVolume(), want, 1e-12) {
		t.Fatalf("volume %v want %v", m.SignedVolume(), want)
	}
	lo, hi, _ := m.Bounds()
	// Axis X: extent along x is the height; 12 segments reach +-r exactly on y and z.
	if lo != (mesh.Vec3{3, 4.5, 5.5}) || hi != (mesh.Vec3{5, 5.5, 6.5}) {
		t.Fatalf("bounds %v %v", lo, hi)
	}
	// Side normals are radial and unit; cap normals point along the axis.
	for _, c := range m.Corners {
		if !near(mesh.Len(c.Normal), 1, 1e-12) {
			t.Fatal("normal not unit")
		}
	}
	// The seam: the last corner of a ring repeats position 0 at u = 1.
	seams := 0
	for _, c := range m.Corners {
		if c.UV[0] == 1 && c.Normal[0] == 0 { // a side corner: its normal has no component along the x axis
			seams++
		}
	}
	if seams != 2 {
		t.Fatalf("%d seam corners, want 2 (one per side ring)", seams)
	}
}

func TestCylinderRejects(t *testing.T) {
	bad := []CylinderParams{
		{Radius: 0, Height: 1, Segments: 6},
		{Radius: 1, Height: 0, Segments: 6},
		{Radius: 1, Height: 1, Segments: 2},
		{Radius: 1, Height: 1, Segments: 6, Bevel: &Bevel{Width: 0.1}},                // open end
		{Radius: 1, Height: 1, Segments: 6, Caps: true, Bevel: &Bevel{Width: 1}},      // consumes the cap
		{Radius: 1, Height: 0.1, Segments: 6, Caps: true, Bevel: &Bevel{Width: 0.06}}, // both edges meet
		{Radius: 1, Height: 0.1, Segments: 6, Caps: true, Bevel: &Bevel{Width: 0.2, Edges: BevelTop}},
	}
	for i, p := range bad {
		if _, err := Cylinder(p); err == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
}

func TestBevelProfile(t *testing.T) {
	// The top ring of a beveled plate is inset by the width, and the side
	// ends width below the top.
	m, err := Cylinder(CylinderParams{Radius: 0.35, Height: 0.03, Segments: 24, Caps: true,
		Bevel: &Bevel{Width: 0.006, Segments: 2, Edges: BevelTop}})
	check(t, "plate", m, err)
	_, hi, _ := m.Bounds()
	if !near(hi[1], 0.015, 1e-15) {
		t.Fatal(hi)
	}
	var top, shoulder bool
	for _, p := range m.Positions {
		if p[1] == hi[1] && near(math.Hypot(p[0], p[2]), 0.344, 1e-12) {
			top = true
		}
		if near(p[1], 0.009, 1e-12) && near(math.Hypot(p[0], p[2]), 0.35, 1e-12) {
			shoulder = true
		}
	}
	if !top || !shoulder {
		t.Fatalf("top ring %v shoulder ring %v", top, shoulder)
	}
}

func TestSphere(t *testing.T) {
	for _, c := range []struct{ n, r, tris int }{{12, 6, 120}, {7, 4, 42}, {3, 2, 6}, {12, 0, 120}} {
		m, err := Sphere(SphereParams{Centre: mesh.Vec3{1, 2, 3}, Radius: 0.05, Segments: c.n, Rings: c.r})
		check(t, "sphere", m, err)
		if m.TriangleCount() != c.tris {
			t.Errorf("n=%d r=%d: %d triangles, want %d", c.n, c.r, m.TriangleCount(), c.tris)
		}
		if !m.IsClosed() {
			t.Errorf("n=%d r=%d: not closed", c.n, c.r)
		}
		if m.SignedVolume() <= 0 {
			t.Errorf("n=%d r=%d: inward winding", c.n, c.r)
		}
		for i, c2 := range m.Corners {
			p := m.Positions[c2.Pos]
			rad := mesh.Normalize(mesh.Sub(p, mesh.Vec3{1, 2, 3}))
			if mesh.Len(mesh.Sub(rad, c2.Normal)) > 1e-12 {
				t.Fatalf("corner %d normal %v not radial %v", i, c2.Normal, rad)
			}
		}
	}
	// Poles are single positions: 2 + (r-1)*n positions.
	m, _ := Sphere(SphereParams{Radius: 1, Segments: 12, Rings: 6})
	if len(m.Positions) != 2+5*12 {
		t.Fatalf("%d positions", len(m.Positions))
	}
	if _, err := Sphere(SphereParams{Radius: 1, Segments: 12, Rings: 1}); err == nil {
		t.Fatal("rings 1 should fail")
	}
}

func TestSweepFixtures(t *testing.T) {
	g, err := GoalPost(GoalPostOptions{})
	if err != nil {
		t.Fatal(err)
	}
	path := g.PostPath
	want := []mesh.Vec3{
		{0, 0, 1.83}, {0, 1.22, 1.83}, {0, 1.57702, 1.79484}, {0, 1.92031, 1.69070},
		{0, 2.23669, 1.52159}, {0, 2.51401, 1.29401}, {0, 2.74159, 1.01669},
		{0, 2.91070, 0.70031}, {0, 3.01484, 0.35702}, {0, 3.05, 0},
	}
	if len(path) != len(want) {
		t.Fatalf("%d path points", len(path))
	}
	for i := range want {
		if mesh.Len(mesh.Sub(path[i], want[i])) > 1e-5 {
			t.Errorf("path point %d = %v, want %v", i, path[i], want[i])
		}
	}
	fr, err := TransportFrames(path, FrameMinimal)
	if err != nil {
		t.Fatal(err)
	}
	if fr[0].U != (mesh.Vec3{0, 0, -1}) || fr[0].V != (mesh.Vec3{-1, 0, 0}) {
		t.Errorf("start frame u=%v v=%v", fr[0].U, fr[0].V)
	}
	end := fr[9]
	for i, w := range []float64{0, 0.098008, -0.995186} {
		if !near(end.Axis[i], w, 2e-5) {
			t.Errorf("end axis %v", end.Axis)
		}
	}
	for i, w := range []float64{0, -0.995186, -0.098008} {
		if !near(end.U[i], w, 2e-5) {
			t.Errorf("end u %v", end.U)
		}
	}
	if mesh.Len(mesh.Sub(end.V, mesh.Vec3{-1, 0, 0})) > 1e-12 {
		t.Errorf("end v %v: the frame twisted", end.V)
	}
	// Ring vertex fixtures: ring 0 vertex 0 and 4; ring 9 vertex 0 and 4.
	m := g.Post
	vtx := func(ring, j int) mesh.Vec3 { return m.Positions[ring*16+j] }
	checkV := func(got, want mesh.Vec3) {
		t.Helper()
		if mesh.Len(mesh.Sub(got, want)) > 2e-5 {
			t.Errorf("ring vertex %v, want %v", got, want)
		}
	}
	checkV(vtx(0, 0), mesh.Vec3{0, 0, 1.73})
	checkV(vtx(0, 4), mesh.Vec3{-0.1, 0, 1.83})
	checkV(vtx(9, 0), mesh.Vec3{0, 2.95048, -0.00980})
	checkV(vtx(9, 4), mesh.Vec3{-0.1, 3.05, 0})
	// Tube rule: the reference vector switches between points 3 and 4 and the
	// rings turn by 90 degrees; minimal transport must not.
	tube, _ := TransportFrames(path, FrameTube)
	if mesh.Dot(tube[3].V, tube[4].V) > 0.5 {
		t.Errorf("tube rule did not switch: %v %v", tube[3].V, tube[4].V)
	}
	for i := 1; i < len(fr); i++ {
		if mesh.Dot(fr[i-1].V, fr[i].V) < 0.999999 {
			t.Errorf("minimal frame twisted at point %d", i)
		}
	}
}

func TestSweepCountsAndTopology(t *testing.T) {
	path := []mesh.Vec3{{0, 0, 0}, {0, 1, 0}, {0.5, 2, 0}, {1.5, 2.5, 0.5}}
	for _, c := range []struct {
		caps Caps
		tris int
		open int
	}{{CapsOff, 2 * 8 * 3, 16}, {CapsBoth, 2*8*3 + 2*6, 0}, {CapsStart, 2*8*3 + 6, 8}, {CapsEnd, 2*8*3 + 6, 8}} {
		for _, rule := range []FrameRule{FrameMinimal, FrameTube} {
			m, err := Sweep(SweepParams{Radius: 0.1, Segments: 8, Path: path, Caps: c.caps, Frame: rule})
			check(t, "sweep", m, err)
			if m.TriangleCount() != c.tris {
				t.Errorf("caps %d: %d triangles, want %d", c.caps, m.TriangleCount(), c.tris)
			}
			if m.OpenEdges() != c.open {
				t.Errorf("caps %d: %d open edges, want %d", c.caps, m.OpenEdges(), c.open)
			}
			if c.caps == CapsBoth && (!m.IsClosed() || m.SignedVolume() <= 0) {
				t.Errorf("capped sweep closed=%v volume=%v", m.IsClosed(), m.SignedVolume())
			}
		}
	}
	if _, err := Sweep(SweepParams{Radius: 0.1, Segments: 8, Path: []mesh.Vec3{{0, 0, 0}, {0, 0, 0}}}); err == nil {
		t.Error("coincident path points should fail")
	}
	if _, err := Sweep(SweepParams{Radius: 0.1, Segments: 8, Path: []mesh.Vec3{{0, 0, 0}}}); err == nil {
		t.Error("one point should fail")
	}
	// Two-point path: the second ring is the first moved along the path.
	m, _ := Sweep(SweepParams{Radius: 1, Segments: 8, Path: []mesh.Vec3{{0, 0, 0}, {0, 0, 5}}, Caps: CapsBoth})
	for j := 0; j < 8; j++ {
		if mesh.Sub(m.Positions[8+j], m.Positions[j]) != (mesh.Vec3{0, 0, 5}) {
			t.Fatalf("ring 1 vertex %d", j)
		}
	}
	if m.SignedVolume() <= 0 {
		t.Fatal("two-point sweep winds inward")
	}
}

func TestLathe(t *testing.T) {
	pad := []mesh.Vec2{{0.11, 0}, {0.25, 0.02}, {0.26, 0.10}, {0.26, 1.70}, {0.25, 1.78}, {0.11, 1.80}}
	for _, ax := range []Axis{AxisY, AxisX, AxisZ} {
		closed, err := Lathe(LatheParams{Rows: pad, Segments: 24, Closed: true, Axis: ax, Centre: mesh.Vec3{0, 0, 1.83}})
		check(t, "closed lathe", closed, err)
		if closed.TriangleCount() != 288 || !closed.IsClosed() || closed.SignedVolume() <= 0 {
			t.Errorf("axis %d: closed lathe tris=%d closed=%v vol=%v", ax, closed.TriangleCount(), closed.IsClosed(), closed.SignedVolume())
		}
	}
	open, err := Lathe(LatheParams{Rows: pad, Segments: 24})
	check(t, "open lathe", open, err)
	if open.TriangleCount() != 240 || open.IsClosed() || open.OpenEdges() != 48 {
		t.Errorf("open lathe tris=%d closed=%v open edges=%d", open.TriangleCount(), open.IsClosed(), open.OpenEdges())
	}
	// A profile with poles at both ends is a closed solid: a cone-like spindle.
	spindle, err := Lathe(LatheParams{Rows: []mesh.Vec2{{0, 0}, {1, 1}, {0, 2}}, Segments: 8})
	check(t, "spindle", spindle, err)
	if spindle.TriangleCount() != 16 || !spindle.IsClosed() || spindle.SignedVolume() <= 0 {
		t.Errorf("spindle tris=%d closed=%v vol=%v", spindle.TriangleCount(), spindle.IsClosed(), spindle.SignedVolume())
	}
	// The closed profile splits at its seam: the first row's positions are
	// used by corners with v = 0 and by corners with v = 1.
	cl, _ := Lathe(LatheParams{Rows: pad, Segments: 24, Closed: true})
	vs := map[float64]bool{}
	for _, c := range cl.Corners {
		if c.Pos < 24 {
			vs[c.UV[1]] = true
		}
	}
	if len(vs) != 2 || !vs[0] || !vs[1] {
		t.Errorf("first-row corner v values %v, want {0, 1}", vs)
	}
	for _, bad := range []LatheParams{
		{Rows: pad[:1], Segments: 8},
		{Rows: []mesh.Vec2{{1, 0}, {1, 0}}, Segments: 8},
		{Rows: []mesh.Vec2{{-1, 0}, {1, 1}}, Segments: 8},
		{Rows: []mesh.Vec2{{0, 0}, {1, 1}}, Segments: 8, Closed: true},
	} {
		if _, err := Lathe(bad); err == nil {
			t.Errorf("expected an error for %v", bad.Rows)
		}
	}
}
