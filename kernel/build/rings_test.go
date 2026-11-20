package build

import (
	"math"
	"reflect"
	"testing"

	"m31labs.dev/greenware/kernel/mesh"
)

// fullRings builds k default-rule rings of n columns at heights ys with the
// given radii.
func fullRings(n int, ys, rs []float64) [][]mesh.Vec3 {
	out := make([][]mesh.Vec3, len(ys))
	for i := range ys {
		out[i] = LoftRing(0, ys[i], 0, rs[i], rs[i], n, false, 0, 0)
	}
	return out
}

// outward checks that every corner's normal agrees in sign with the direction
// away from the axis through (cx, cz).
func outwardAboutY(t *testing.T, m *mesh.Mesh, name string) {
	t.Helper()
	for i, c := range m.Corners {
		p := m.Positions[c.Pos]
		radial := mesh.Vec3{p[0], 0, p[2]}
		if mesh.Len(radial) < 1e-9 {
			continue
		}
		if mesh.Dot(c.Normal, radial) < 0 && math.Abs(c.Normal[1]) < 0.99 {
			t.Fatalf("%s: corner %d normal %v points inward at %v", name, i, c.Normal, p)
		}
	}
}

func TestLoftCounts(t *testing.T) {
	ys := []float64{0, 1, 2, 3}
	rs := []float64{1, 1.2, 1.2, 0.8}
	for _, n := range []int{3, 5, 24} {
		k := len(ys)
		for _, c := range []struct {
			caps Caps
			ends int
		}{{CapsOff, 0}, {CapsStart, 1}, {CapsEnd, 1}, {CapsBoth, 2}} {
			m, err := Loft(LoftParams{Rings: fullRings(n, ys, rs), Caps: c.caps})
			check(t, "loft", m, err)
			want := 2*n*(k-1) + c.ends*(n-2)
			if m.TriangleCount() != want {
				t.Errorf("n=%d caps=%d: %d triangles, want %d", n, c.caps, m.TriangleCount(), want)
			}
			if c.caps == CapsBoth && !m.IsClosed() {
				t.Errorf("n=%d: capped loft is not closed", n)
			}
			if c.caps == CapsOff && m.OpenEdges() != 2*n {
				t.Errorf("n=%d: open loft has %d open edges, want %d", n, m.OpenEdges(), 2*n)
			}
			if m.NumShells() != 1 {
				t.Errorf("%d shells", m.NumShells())
			}
			outwardAboutY(t, m, "loft")
		}
	}
}

func TestLoftWindingAndCaps(t *testing.T) {
	m, err := Loft(LoftParams{Rings: fullRings(12, []float64{0, 1, 2}, []float64{1, 1.5, 1}), Caps: CapsBoth})
	check(t, "loft", m, err)
	if v := m.SignedVolume(); v <= 0 {
		t.Fatalf("signed volume %v: faces point inward", v)
	}
	// The top cap faces +Y and the bottom cap -Y, with flat corner normals.
	var up, down int
	for _, c := range m.Corners {
		switch c.Normal {
		case mesh.Vec3{0, 1, 0}:
			up++
		case mesh.Vec3{0, -1, 0}:
			down++
		}
	}
	if up != 12 || down != 12 {
		t.Fatalf("%d up and %d down cap corners, want 12 each", up, down)
	}
	// Rows keep their order as written: reversing them flips the winding.
	rev, err := Loft(LoftParams{Rings: fullRings(12, []float64{2, 1, 0}, []float64{1, 1.5, 1}), Caps: CapsOff})
	check(t, "rev", rev, err)
	for i, c := range rev.Corners {
		p := rev.Positions[c.Pos]
		if mesh.Dot(c.Normal, mesh.Vec3{p[0], 0, p[2]}) > 0 {
			t.Fatalf("corner %d of a loft written top to bottom faces outward", i)
		}
	}
}

func TestLoftPoleAndSeam(t *testing.T) {
	n := 8
	rings := fullRings(n, []float64{0, 1, 2}, []float64{1, 1, 0})
	m, err := Loft(LoftParams{Rings: rings})
	check(t, "pole", m, err)
	// A pole row joins with one triangle per interval: 2n + n.
	if m.TriangleCount() != 3*n {
		t.Fatalf("%d triangles, want %d", m.TriangleCount(), 3*n)
	}
	if len(m.Positions) != 2*n+1 {
		t.Fatalf("%d positions, want %d", len(m.Positions), 2*n+1)
	}
	if _, err := Loft(LoftParams{Rings: rings, Caps: CapsEnd}); err == nil {
		t.Fatal("a cap on a pole ring should fail")
	}
	// The seam: each ring's last corner repeats its first position at u = 1.
	seams := 0
	for _, c := range m.Corners {
		if c.UV[0] == 1 {
			seams++
		}
	}
	if seams != 2 {
		t.Fatalf("%d seam corners, want 2", seams)
	}
}

func TestLoftArcIsAnOpenSheet(t *testing.T) {
	n := 8
	lo, hi := 110*math.Pi/180, 250*math.Pi/180
	rings := make([][]mesh.Vec3, 3)
	for i := range rings {
		rings[i] = LoftRing(0, float64(i), 0, 1, 1, n, true, lo, hi)
	}
	m, err := Loft(LoftParams{Rings: rings, Arc: true})
	check(t, "arc", m, err)
	if m.TriangleCount() != 2*n*2 {
		t.Fatalf("%d triangles, want %d", m.TriangleCount(), 4*n)
	}
	// n+1 distinct columns per ring, not joined at the ends.
	if len(m.Positions) != 3*(n+1) {
		t.Fatalf("%d positions", len(m.Positions))
	}
	if got, want := m.OpenEdges(), 2*n+2*2; got != want {
		t.Fatalf("%d open edges, want %d (two rows and two sides)", got, want)
	}
	// The arc spans 110..250 degrees: toward -z, centred on 180 degrees.
	for _, p := range m.Positions {
		if p[2] >= 0 {
			t.Fatalf("point %v is not on the front half", p)
		}
	}
	if _, err := Loft(LoftParams{Rings: rings, Arc: true, Caps: CapsBoth}); err == nil {
		t.Fatal("caps on an open arc should fail")
	}
	outwardAboutY(t, m, "arc")
}

func TestLoftRejects(t *testing.T) {
	r := fullRings(8, []float64{0, 1}, []float64{1, 1})
	bad := []LoftParams{
		{Rings: r[:1]},
		{Rings: fullRings(8, []float64{0, 0}, []float64{1, 1})},
		{Rings: [][]mesh.Vec3{r[0], r[1][:7]}},
		{Rings: fullRings(2, []float64{0, 1}, []float64{1, 1})},
		{Rings: r, V: []float64{0}},
	}
	for i, p := range bad {
		if _, err := Loft(p); err == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
}

func TestLoftRingDefaultRule(t *testing.T) {
	ring := LoftRing(1, 2, 3, 0.5, 0.25, 4, false, 0, 0)
	want := []mesh.Vec3{{1, 2, 3.25}, {1.5, 2, 3}, {1, 2, 2.75}, {0.5, 2, 3}}
	if !reflect.DeepEqual(ring, want) {
		t.Fatalf("ring %v, want %v", ring, want)
	}
	arc := LoftRing(0, 0, 0, 1, 1, 2, true, 0, math.Pi)
	if len(arc) != 3 || arc[0][2] != 1 || math.Abs(arc[1][0]-1) > 1e-15 || math.Abs(arc[2][2]+1) > 1e-15 {
		t.Fatalf("arc ring %v", arc)
	}
}

func TestLoftKeepRowsFixture(t *testing.T) {
	// language.md section 6.1: four columns, heights 0..3, radii
	// 1, 1.125, 1.125, 1, max_error 0.08. Either interior row alone scores
	// exactly 0.0625; row 1 goes by the tie rule, and removing row 2 as well
	// would score 0.125 against the original, so [0, 2, 3] survive.
	grid := fullRings(4, []float64{0, 1, 2, 3}, []float64{1, 1.125, 1.125, 1})
	if got := LoftKeepRows(grid, true, 0.08); !reflect.DeepEqual(got, []int{0, 2, 3}) {
		t.Fatalf("survivors %v, want [0 2 3]", got)
	}
	// The score is exact: 0.0625 passes, a hair under it does not.
	if got := LoftKeepRows(grid, true, 0.0625); !reflect.DeepEqual(got, []int{0, 2, 3}) {
		t.Fatalf("at the exact score: %v", got)
	}
	if got := LoftKeepRows(grid, true, 0.0624999); !reflect.DeepEqual(got, []int{0, 1, 2, 3}) {
		t.Fatalf("below the score: %v", got)
	}
	// A looser bound still keeps the end rows and stops at them.
	if got := LoftKeepRows(grid, true, 1); !reflect.DeepEqual(got, []int{0, 3}) {
		t.Fatalf("loose bound: %v", got)
	}
	// No bound keeps everything.
	if got := LoftKeepRows(grid, true, 0); !reflect.DeepEqual(got, []int{0, 1, 2, 3}) {
		t.Fatalf("zero bound: %v", got)
	}
}

func TestLoftKeepRowsStraightRowsGo(t *testing.T) {
	// Collinear rows lie on the line between their neighbours: they go at any
	// positive bound.
	grid := fullRings(6, []float64{0, 1, 2, 3, 4}, []float64{1, 1, 1, 1, 1})
	if got := LoftKeepRows(grid, true, 1e-9); !reflect.DeepEqual(got, []int{0, 4}) {
		t.Fatalf("survivors %v", got)
	}
	// An arc strip works the same way on n+1 columns.
	arc := make([][]mesh.Vec3, 5)
	for i := range arc {
		arc[i] = LoftRing(0, float64(i), 0, 1, 1, 6, true, 0, 1)
	}
	if got := LoftKeepRows(arc, false, 1e-9); !reflect.DeepEqual(got, []int{0, 4}) {
		t.Fatalf("arc survivors %v", got)
	}
}

func TestEllipsoidCountsAndClosure(t *testing.T) {
	floor := 0.0002 * 0.9144
	for _, c := range []struct{ n, rows int }{{12, 7}, {12, 5}, {10, 4}, {3, 2}, {24, 3}} {
		m, err := Ellipsoid(EllipsoidParams{Centre: mesh.Vec3{1, 2, 3}, Radii: mesh.Vec3{0.013, 0.04, 0.03}, Segments: c.n, Rings: c.rows, Floor: floor})
		check(t, "ellipsoid", m, err)
		want := 2*c.n*(c.rows-1) + 2*(c.n-2)
		if m.TriangleCount() != want {
			t.Errorf("n=%d rows=%d: %d triangles, want %d", c.n, c.rows, m.TriangleCount(), want)
		}
		if !m.IsClosed() {
			t.Errorf("n=%d rows=%d: not closed", c.n, c.rows)
		}
		if m.SignedVolume() <= 0 {
			t.Errorf("n=%d rows=%d: faces point inward", c.n, c.rows)
		}
		if m.NumShells() != 1 {
			t.Errorf("%d shells", m.NumShells())
		}
	}
}

func TestEllipsoidShape(t *testing.T) {
	c := mesh.Vec3{1, 2, 3}
	r := mesh.Vec3{0.5, 2, 1}
	m, err := Ellipsoid(EllipsoidParams{Centre: c, Radii: r, Segments: 12, Rings: 7, Floor: 0.001})
	check(t, "ellipsoid", m, err)
	lo, hi, _ := m.Bounds()
	// 7 rows put one on the equator; 12 segments reach the radii on x and z.
	for k := 0; k < 3; k++ {
		if !near(lo[k], c[k]-r[k], 1e-12) || !near(hi[k], c[k]+r[k], 1e-12) {
			t.Fatalf("axis %d bounds %v..%v", k, lo[k], hi[k])
		}
	}
	// The end rows are floored polygons, not points.
	for _, y := range []float64{c[1] - r[1], c[1] + r[1]} {
		n := 0
		for _, p := range m.Positions {
			if p[1] == y {
				n++
				if h := math.Hypot(p[0]-c[0], p[2]-c[2]); h < 0.001-1e-12 {
					t.Fatalf("end ring at radius %v is below the floor", h)
				}
			}
		}
		if n != 12 {
			t.Fatalf("%d end positions at y=%v, want 12", n, y)
		}
	}
	// Corner normals are unit; side normals of an ellipsoid point outward from
	// the centre in each octant.
	for i, cn := range m.Corners {
		p := mesh.Sub(m.Positions[cn.Pos], c)
		if mesh.Dot(cn.Normal, p) <= 0 {
			t.Fatalf("corner %d normal %v is not outward at %v", i, cn.Normal, p)
		}
	}
	// A sphere-like ellipsoid has about the volume of its inscribed solid.
	s, err := Ellipsoid(EllipsoidParams{Radii: mesh.Vec3{1, 1, 1}, Segments: 48, Rings: 25, Floor: 1e-6})
	check(t, "unit", s, err)
	if v := s.SignedVolume(); !near(v, 4*math.Pi/3, 0.05) {
		t.Fatalf("volume %v", v)
	}
}

func TestEllipsoidFloorZeroLeavesPoles(t *testing.T) {
	m, err := Ellipsoid(EllipsoidParams{Radii: mesh.Vec3{1, 2, 1}, Segments: 8, Rings: 5})
	check(t, "poles", m, err)
	if m.TriangleCount() != 2*8*(5-3)+2*8 {
		t.Fatalf("%d triangles", m.TriangleCount())
	}
	if !m.IsClosed() {
		t.Fatal("not closed")
	}
}

func TestEllipsoidMirrorKeepsOutward(t *testing.T) {
	// An off-centre ellipsoid with unequal radii, reflected once, has positive
	// volume and a closed shell (language.md section 3.7).
	m, err := Ellipsoid(EllipsoidParams{Centre: mesh.Vec3{0.148, 1.015, 0.002}, Radii: mesh.Vec3{0.02, 0.05, 0.03}, Segments: 12, Rings: 7, Floor: 0.0001})
	check(t, "ellipsoid", m, err)
	r := m.Mirror(mesh.PlaneX)
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if !r.IsClosed() || r.SignedVolume() <= 0 || !near(r.SignedVolume(), m.SignedVolume(), 1e-15) {
		t.Fatalf("mirror volume %v, original %v", r.SignedVolume(), m.SignedVolume())
	}
	if lo, hi, _ := r.Bounds(); !near(hi[0], -0.128, 1e-12) || !near(lo[0], -0.168, 1e-12) {
		t.Fatalf("mirror landed at x %v..%v", lo[0], hi[0])
	}
}

func TestEllipsoidRejects(t *testing.T) {
	bad := []EllipsoidParams{
		{Radii: mesh.Vec3{0, 1, 1}, Segments: 8, Rings: 5},
		{Radii: mesh.Vec3{1, 1, 1}, Segments: 2, Rings: 5},
		{Radii: mesh.Vec3{1, 1, 1}, Segments: 8, Rings: 1},
		{Radii: mesh.Vec3{1, 1, 1}, Segments: 8, Rings: 5, Floor: -1},
	}
	for i, p := range bad {
		if _, err := Ellipsoid(p); err == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
}

func TestTubeCounts(t *testing.T) {
	path := []mesh.Vec3{{0, 0, 0}, {1, 0, 0}, {1, 1, 0}, {2, 1, 1}}
	for _, n := range []int{3, 7, 16} {
		m, err := Tube(TubeParams{Radius: 0.1, Segments: n, Path: path})
		check(t, "tube", m, err)
		if m.TriangleCount() != 2*n*(len(path)-1) {
			t.Errorf("n=%d: %d triangles", n, m.TriangleCount())
		}
		if m.OpenEdges() != 2*n {
			t.Errorf("n=%d: %d open edges, want %d (no end caps)", n, m.OpenEdges(), 2*n)
		}
		if m.NumShells() != 1 {
			t.Errorf("%d shells", m.NumShells())
		}
	}
	ring := []mesh.Vec3{{1, 0, 0}, {0, 0, 1}, {-1, 0, 0}, {0, 0, -1}}
	m, err := Tube(TubeParams{Radius: 0.1, Segments: 8, Path: ring, Closed: true})
	check(t, "closed", m, err)
	if m.TriangleCount() != 2*8*4 {
		t.Fatalf("closed: %d triangles, want %d", m.TriangleCount(), 2*8*4)
	}
	if !m.IsClosed() {
		t.Fatal("a closed tube has open edges")
	}
	// A torus-like surface: V - E + F = 0.
	w := m.Weld()
	edges := len(m.EdgeUses())
	if chi := w.Count - edges + m.TriangleCount(); chi != 0 {
		t.Fatalf("euler characteristic %d, want 0", chi)
	}
}

func TestTubeFrameRule(t *testing.T) {
	// language.md 3.6: a two-point path; the last ring equals the first
	// translated by P1-P0; the side winds outward; u = normalize(t x up).
	p0, p1 := mesh.Vec3{0, 0, 0}, mesh.Vec3{0, 0, -2}
	m, err := Tube(TubeParams{Radius: 0.5, Segments: 8, Path: []mesh.Vec3{p0, p1}})
	check(t, "two", m, err)
	for j := 0; j < 8; j++ {
		if got, want := m.Positions[8+j], mesh.Add(m.Positions[j], mesh.Sub(p1, p0)); got != want {
			t.Fatalf("last ring vertex %d = %v, want %v", j, got, want)
		}
	}
	// t = -z, up = +y: u = (-z) x y = (1,0,0)*? : (0,0,-1)x(0,1,0) = (1,0,0).
	if m.Positions[0] != (mesh.Vec3{0.5, 0, 0}) {
		t.Fatalf("vertex 0 at %v, want (0.5, 0, 0)", m.Positions[0])
	}
	for i, c := range m.Corners {
		p := m.Positions[c.Pos]
		if mesh.Dot(c.Normal, mesh.Vec3{p[0], p[1], 0}) <= 0 {
			t.Fatalf("corner %d normal %v is not radial at %v", i, c.Normal, p)
		}
	}
	// Triangle winding agrees with the normals.
	for i := range m.Tris {
		a, b, c := m.TriPositions(i)
		n := mesh.Cross(mesh.Sub(b, a), mesh.Sub(c, a))
		cen := mesh.Scale(mesh.Add(mesh.Add(a, b), c), 1.0/3)
		if mesh.Dot(n, mesh.Vec3{cen[0], cen[1], 0}) <= 0 {
			t.Fatalf("triangle %d winds inward", i)
		}
	}
	// The reference direction flips to +x for a vertical tangent.
	v, err := Tube(TubeParams{Radius: 1, Segments: 4, Path: []mesh.Vec3{{0, 0, 0}, {0, 1, 0}}})
	check(t, "vertical", v, err)
	// t = +y, up = +x: u = y x x = (0,0,-1).
	if v.Positions[0] != (mesh.Vec3{0, 0, -1}) {
		t.Fatalf("vertical tube vertex 0 at %v", v.Positions[0])
	}
}

func TestTubeUV(t *testing.T) {
	m, err := Tube(TubeParams{Radius: 0.1, Segments: 4, Path: []mesh.Vec3{{0, 0, 0}, {0, 0, 1}, {0, 0, 4}}})
	check(t, "uv", m, err)
	var vs []float64
	for _, c := range m.Corners {
		if c.UV[0] == 0 {
			vs = append(vs, c.UV[1])
		}
	}
	if !reflect.DeepEqual(vs, []float64{0, 0.25, 1}) {
		t.Fatalf("path fractions %v", vs)
	}
}

func TestTubeRejects(t *testing.T) {
	line := []mesh.Vec3{{0, 0, 0}, {1, 0, 0}}
	bad := []TubeParams{
		{Radius: 0, Segments: 7, Path: line},
		{Radius: 1, Segments: 2, Path: line},
		{Radius: 1, Segments: 7, Path: line[:1]},
		{Radius: 1, Segments: 7, Path: []mesh.Vec3{{0, 0, 0}, {0, 0, 0}, {1, 0, 0}}},
		{Radius: 1, Segments: 7, Path: line, Closed: true},
		{Radius: 1, Segments: 7, Path: []mesh.Vec3{{0, 0, 0}, {1, 0, 0}, {0, 0, 0}}, Closed: true},
	}
	for i, p := range bad {
		if _, err := Tube(p); err == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
}

func TestRetessellateEllipsoidTube(t *testing.T) {
	e := EllipsoidParams{Radii: mesh.Vec3{0.012, 0.037, 0.027}, Segments: 12, Rings: 7}
	got := RetessellateEllipsoid(e, 0.005)
	if got.Segments >= 12 || got.Segments < 3 || got.Rings >= 7 || got.Rings < 3 {
		t.Fatalf("retessellated to %d segments and %d rows", got.Segments, got.Rings)
	}
	if ChordError(0.027, 2*math.Pi, got.Segments) > 0.005*(1+1e-9) {
		t.Fatalf("segments %d exceed the bound", got.Segments)
	}
	if same := RetessellateEllipsoid(e, 0); same != e {
		t.Fatalf("no bound changed the shape: %v", same)
	}
	tb := RetessellateTube(TubeParams{Radius: 0.006, Segments: 7, Path: []mesh.Vec3{{}, {1, 0, 0}}}, 0.003)
	if tb.Segments != 3 {
		t.Fatalf("tube segments %d, want 3", tb.Segments)
	}
	if n := LoftSegments(0.15, false, 0, 24, 0.003); n >= 24 || ChordError(0.15, 2*math.Pi, n) > 0.003*(1+1e-9) {
		t.Fatalf("loft segments %d", n)
	}
	if n := LoftSegments(0.15, true, 140*math.Pi/180, 16, 0.003); n >= 16 || n < 1 {
		t.Fatalf("arc intervals %d", n)
	}
}

func TestRingStackDeterministic(t *testing.T) {
	build := func() *mesh.Mesh {
		m, err := Loft(LoftParams{Rings: fullRings(24, []float64{0, 1, 2, 3}, []float64{1, 1.2, 1.2, 0.8}), Caps: CapsBoth})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	if a, b := build(), build(); !reflect.DeepEqual(a, b) {
		t.Fatal("two builds differ")
	}
}
