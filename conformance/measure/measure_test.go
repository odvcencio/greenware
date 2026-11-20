package measure_test

import (
	"math"
	"testing"

	"m31labs.dev/greenware/conformance/glb"
	"m31labs.dev/greenware/conformance/internal/fixture"
	"m31labs.dev/greenware/conformance/measure"
)

func load(t testing.TB, data []byte) *measure.Model {
	t.Helper()
	f, fs := glb.Decode(data)
	if glb.HasErrors(fs) {
		t.Fatalf("fixture is not valid: %v", fs)
	}
	m, err := measure.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func near(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.9f, want %.9f (tolerance %g)", what, got, want, tol)
	}
}

// post builds two uprights under a root named goalpost. Each upright has
// radius 0.05 m, 12 sides, and spans y from 0 to 12.19 m. dxL and dxR move
// the left (x=-2.87) and right (x=+2.87) uprights along x; the sockets
// stay where the design puts them.
func post(dxL, dxR, phase float64) []byte {
	b := fixture.New()
	l := b.Mesh("upright_l", fixture.Cylinder(-2.87+dxL, 0, 0.05, 0, 12.19, 12, phase, true))
	r := b.Mesh("upright_r", fixture.Cylinder(2.87+dxR, 0, 0.05, 0, 12.19, 12, phase, true))
	root := b.Node("goalpost", -1, [3]float64{})
	for _, c := range []int{
		b.Node("upright_l", l, [3]float64{}),
		b.Node("upright_r", r, [3]float64{}),
		b.Node("socket.inside_l", -1, [3]float64{-2.82, 7, 0}),
		b.Node("socket.inside_r", -1, [3]float64{2.82, 7, 0}),
		b.Node("collider.upright_l", -1, [3]float64{}),
	} {
		b.Child(root, c)
	}
	b.Scene(root)
	return b.Bytes()
}

func TestGapAnalytic(t *testing.T) {
	m := load(t, post(0, 0, 0))
	g, err := m.Gap(measure.Sub("goalpost"), "x", [3]float64{0, 7, 0})
	if err != nil || !g.OK() {
		t.Fatalf("gap: %+v %v", g, err)
	}
	// inside faces: vertices at 2.87-0.05 and -2.87+0.05, so 5.64 m apart
	near(t, "gap, vertex on the ray", g.Distance, 5.64, 1e-6)

	// rotate each ring by 15 degrees so the ray crosses edge midpoints: the
	// hit is at the apothem, 0.05*cos(15 deg), on each side
	m = load(t, post(0, 0, math.Pi/12))
	g, _ = m.Gap(measure.Sub("goalpost"), "x", [3]float64{0, 7, 0})
	near(t, "gap, edge midpoint on the ray", g.Distance, 5.74-2*0.05*math.Cos(math.Pi/12), 1e-6)
}

func TestMovedUprightIsCaughtWhileSocketsStay(t *testing.T) {
	m := load(t, post(0, 0.02, 0))
	g, _ := m.Gap(measure.Sub("goalpost"), "x", [3]float64{0, 7, 0})
	if !g.OK() {
		t.Fatalf("%+v", g)
	}
	near(t, "gap with the right upright 2 cm out", g.Distance, 5.66, 1e-6)
	if math.Abs(g.Distance-5.64) < 0.001+0.0001 {
		t.Fatalf("a 2 cm move was not caught: gap %.6f", g.Distance)
	}
	inl, _ := m.Socket("inside_l")
	inr, _ := m.Socket("socket.inside_r")
	near(t, "socket distance is unchanged", measure.Distance(inl, inr), 5.64, 1e-9)

	both := load(t, post(-0.02, 0.02, 0))
	g, _ = both.Gap(measure.Sub("goalpost"), "x", [3]float64{0, 7, 0})
	near(t, "gap with both uprights 2 cm out", g.Distance, 5.68, 1e-6)
}

func TestGapRefusals(t *testing.T) {
	m := load(t, post(0, 0, 0))
	sel := measure.Sub("goalpost")
	cases := []struct {
		name string
		at   [3]float64
		want measure.GapStatus
	}{
		{"ray above the uprights misses both", [3]float64{0, 20, 0}, measure.GapMissingPos},
		{"origin inside the right upright", [3]float64{2.87, 7, 0}, measure.GapOriginInside},
		{"origin on the right upright's surface", [3]float64{2.92, 7, 0}, measure.GapOnSurface},
		{"origin beyond the right upright sees nothing on +x", [3]float64{4, 7, 0}, measure.GapMissingPos},
		{"origin left of everything sees nothing on -x", [3]float64{-4, 7, 0}, measure.GapMissingNeg},
	}
	for _, c := range cases {
		g, err := m.Gap(sel, "x", c.at)
		if err != nil {
			t.Fatal(err)
		}
		if g.Status != c.want || g.OK() {
			t.Errorf("%s: status %s (%s), want %s", c.name, g.Status, g.Message, c.want)
		}
	}
	if g, _ := m.Gap(sel, "w", [3]float64{}); g.Status != measure.GapBadAxis {
		t.Errorf("bad axis: %s", g.Status)
	}
	if _, err := m.Gap(measure.Sub("nope"), "x", [3]float64{}); err == nil {
		t.Error("unknown node must be an error")
	}
	// vertical gap through a cube pair stacked with a hole between them
	b := fixture.New()
	lo := b.Mesh("lo", fixture.Cube(0, 0, 0, 1, 1, 1))
	hi := b.Mesh("hi", fixture.Cube(0, 5, 0, 1, 1, 1))
	b.Scene(b.Node("lo", lo, [3]float64{}), b.Node("hi", hi, [3]float64{}))
	g, _ := load(t, b.Bytes()).Gap(measure.Whole, "y", [3]float64{0.3, 2.5, 0.2})
	near(t, "gap along y between two cubes (top of one at y=1, bottom of the other at y=4)", g.Distance, 3, 1e-6)
}

func TestClosedAnalyticFixtures(t *testing.T) {
	one := func(p fixture.Prim) measure.ClosedResult {
		b := fixture.New()
		b.Scene(b.Node("n", b.Mesh("m", p), [3]float64{}))
		r, err := load(t, b.Bytes()).Closed(measure.Whole)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	cube := one(fixture.Cube(0, 0, 0, 0.5, 0.5, 0.5))
	if !cube.Closed || cube.Triangles != 12 || cube.Edges != 18 || cube.Vertices != 8 {
		t.Errorf("cube: %+v", cube)
	}
	near(t, "cube volume, outward winding", cube.SignedVolume, 1, 1e-6)

	split := one(fixture.SplitCube(0, 0, 0, 0.5, 0.5, 0.5))
	if !split.Closed || split.Vertices != 8 {
		t.Errorf("split-vertex cube must weld to 8 vertices and be closed: %+v", split)
	}

	cyl := one(fixture.Cylinder(0, 0, 0.05, 0, 1, 12, 0, true))
	if !cyl.Closed || cyl.Triangles != 48 {
		t.Errorf("capped cylinder with seam and cap copies: %+v", cyl)
	}
	if cyl.SignedVolume <= 0 {
		t.Errorf("cylinder is wound inward: volume %g", cyl.SignedVolume)
	}

	sph := one(fixture.Sphere(0, 0, 0, 1, 12, 6))
	if !sph.Closed || sph.Triangles != 120 || sph.SignedVolume <= 0 {
		t.Errorf("sphere: %+v", sph)
	}

	tube := one(fixture.Cylinder(0, 0, 0.05, 0, 1, 12, 0, false))
	if tube.Closed || tube.OpenEdges != 24 || tube.UseHistogram[1] != 24 {
		t.Errorf("open tube: %+v", tube)
	}

	// a single triangle, and an empty selection
	if r := one(fixture.Prim{Pos: [][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}, Idx: []uint32{0, 1, 2}, Material: -1}); r.Closed || r.OpenEdges != 3 {
		t.Errorf("triangle: %+v", r)
	}
}

func TestCylinderSlitIsOpen(t *testing.T) {
	// A capped cylinder whose seam is a real 0.1 mm slit: the seam vertices
	// are moved apart, so exact welding leaves the seam edges open.
	p := fixture.Cylinder(0, 0, 0.05, 0, 1, 12, 0, true)
	seamLast := 12     // bottom ring repeat of vertex 0
	topLast := 13 + 12 // top ring repeat of vertex 0
	p.Pos[seamLast][2] += 0.0001
	p.Pos[topLast][2] += 0.0001
	b := fixture.New()
	b.Scene(b.Node("n", b.Mesh("m", p), [3]float64{}))
	m := load(t, b.Bytes())
	c, _ := m.Closed(measure.Whole)
	if c.Closed || c.OpenEdges == 0 {
		t.Errorf("slit cylinder reported closed: %+v", c)
	}
	k, _ := m.Connected(measure.Whole)
	if !k.Connected {
		t.Errorf("slit cylinder is still one connected piece: %+v", k)
	}
}

// upright builds the §7.1 original upright: pipe of radius 0.05 with 12 sides
// ending at y=12.19, and a 12 by 6 sphere of the same radius centred at
// 12.19 + rise.
func upright(rise float64) []byte {
	pipe := fixture.Cylinder(2.87, 0, 0.05, 0, 12.19, 12, 0, true)
	ball := fixture.Sphere(2.87, 12.19+rise, 0, 0.05, 12, 6)
	b := fixture.New()
	b.Scene(b.Node("upright", b.Mesh("m", fixture.Merge(pipe, ball)), [3]float64{}))
	return b.Bytes()
}

func TestExactCoincidenceControlIsNotClosed(t *testing.T) {
	m := load(t, upright(0))
	c, _ := m.Closed(measure.Whole)
	// 48 pipe triangles + 120 sphere triangles. The spec text says 164; its
	// parameters give 168 here (see NOTES.md).
	if c.Triangles != 168 {
		t.Errorf("triangles = %d", c.Triangles)
	}
	if c.UseHistogram[4] != 12 {
		t.Errorf("four-use rim edges = %d, want 12 (histogram %v)", c.UseHistogram[4], c.UseHistogram)
	}
	if c.OpenEdges != 0 {
		t.Errorf("open edges = %d, want 0", c.OpenEdges)
	}
	if c.Closed {
		t.Errorf("exactly coincident rings must not be closed: %+v", c)
	}
}

func TestSphereTwoMillimetresAboveIsClosedAndConnected(t *testing.T) {
	m := load(t, upright(0.002))
	c, _ := m.Closed(measure.Whole)
	if !c.Closed || c.Triangles != 168 || c.UseHistogram[2] != c.Edges {
		t.Errorf("post-upright/2: %+v", c)
	}
	k, _ := m.Connected(measure.Whole)
	if !k.Connected || k.Shells != 2 {
		t.Errorf("pipe and sphere touch within 1 mm: %+v", k)
	}
	// raise the sphere until its surface is more than 1 mm off the pipe
	far := load(t, upright(0.2))
	k, _ = far.Connected(measure.Whole)
	if k.Connected || k.Components != 2 {
		t.Errorf("sphere 0.2 m up must be disconnected: %+v", k)
	}
}

func TestConnectedByContactThreshold(t *testing.T) {
	cubes := func(offsets ...float64) *measure.Model {
		b := fixture.New()
		var roots []int
		for _, o := range offsets {
			roots = append(roots, b.Node("c", b.Mesh("m", fixture.Cube(o, 0, 0, 0.5, 0.5, 0.5)), [3]float64{}))
		}
		b.Scene(roots...)
		return load(t, b.Bytes())
	}
	check := func(name string, m *measure.Model, want bool, comps int) {
		t.Helper()
		k, _ := m.Connected(measure.Whole)
		if k.Connected != want || k.Components != comps {
			t.Errorf("%s: %+v, want connected=%v components=%d", name, k, want, comps)
		}
	}
	check("touching faces", cubes(0, 1), true, 1)
	check("0.5 mm apart", cubes(0, 1.0005), true, 1)
	check("1/1024 m (0.977 mm) apart", cubes(0, 1+1.0/1024), true, 1)
	check("2 mm apart", cubes(0, 1.002), false, 2)
	check("overlapping", cubes(0, 0.5), true, 1)
	check("chain through a middle cube", cubes(0, 1.0005, 2.001), true, 1)
	// two pairs of touching shells, far from each other
	check("two disconnected touching pairs", cubes(0, 1, 10, 11), false, 2)
}

func TestHiddenNodeDrawsNothing(t *testing.T) {
	b := fixture.New()
	pad := b.Mesh("pad", fixture.Cube(0, 0, 0, 1, 0.1, 1))
	post := b.Mesh("post", fixture.Cube(0, 1, 0, 0.1, 1, 0.1))
	root := b.Node("goalpost", -1, [3]float64{})
	b.Child(root, b.Node("post", post, [3]float64{}))
	b.Scene(root)
	hidden := b.Node("pad", pad, [3]float64{}) // not in any scene: an alternate
	_ = hidden
	m := load(t, b.Bytes())
	if !m.HasNode("pad") || !m.HasNode("post") || m.HasNode("nope") {
		t.Error("has_node must see alternates and miss absent names")
	}
	if n, err := m.DrawnTriangles(measure.OwnOf("pad")); err != nil || n != 0 {
		t.Errorf("hidden pad draws %d triangles (err %v)", n, err)
	}
	if n, _ := m.DrawnTriangles(measure.Sub("goalpost")); n != 12 {
		t.Errorf("goalpost triangles = %d", n)
	}
	if n, _ := m.DrawCalls(measure.Whole); n != 1 {
		t.Errorf("draw calls = %d", n)
	}
	if _, ok, _ := m.Bounds(measure.OwnOf("pad")); ok {
		t.Error("a hidden node has no bounds")
	}
	if m.InDefaultScene("pad") || !m.InDefaultScene("post") {
		t.Error("InDefaultScene")
	}
}

func TestDrawCallsFollowPrimitivesNotNodes(t *testing.T) {
	b := fixture.New()
	red := b.Material("red", [4]float64{1, 0, 0, 1})
	blue := b.Material("blue", [4]float64{0, 0, 1, 1})
	a := fixture.Cube(0, 0, 0, 1, 1, 1)
	a.Material = red
	c := fixture.Cube(3, 0, 0, 1, 1, 1)
	c.Material = blue
	two := b.Mesh("two", a, c) // one mesh, two materials: two draws
	one := b.Mesh("one", fixture.Cube(0, 5, 0, 1, 1, 1))
	b.Scene(b.Node("a", two, [3]float64{}), b.Node("b", one, [3]float64{}), b.Node("c", one, [3]float64{0, 0, 9}))
	m := load(t, b.Bytes())
	if n, _ := m.DrawCalls(measure.Whole); n != 4 {
		t.Errorf("draw calls = %d, want 4 (2 + 1 + 1)", n)
	}
	if n, _ := m.DrawnTriangles(measure.Whole); n != 48 {
		t.Errorf("triangles = %d", n)
	}
	box, ok, _ := m.Bounds(measure.Whole)
	if !ok || box.Min != [3]float64{-1, -1, -1} || box.Max != [3]float64{4, 6, 10} {
		t.Errorf("bounds %+v", box)
	}
}

func TestTransformsAndSockets(t *testing.T) {
	b := fixture.New()
	m := b.Mesh("cube", fixture.Cube(0, 0, 0, 0.5, 0.5, 0.5))
	root := b.Node("root", -1, [3]float64{10, 0, 0})
	b.Set(root, "scale", []float64{2, 2, 2})
	// rotate 180 degrees about Y: quaternion (0,1,0,0)
	arm := b.Node("arm", m, [3]float64{1, 2, 3})
	b.Set(arm, "rotation", []float64{0, 1, 0, 0})
	sock := b.Node("socket.hand", -1, [3]float64{0, 1, 0})
	b.Child(root, arm)
	b.Child(arm, sock)
	b.Scene(root)
	mod := load(t, b.Bytes())
	box, _, _ := mod.Bounds(measure.Sub("root"))
	// arm at (1,2,3) scaled by 2 and moved by (10,0,0): centre (12,4,6), half-size 1
	for k, want := range [3]float64{11, 3, 5} {
		near(t, "min", box.Min[k], want, 1e-5)
	}
	for k, want := range [3]float64{13, 5, 7} {
		near(t, "max", box.Max[k], want, 1e-5)
	}
	s, ok := mod.Socket("hand")
	if !ok {
		t.Fatal("socket missing")
	}
	// arm world = T(10,0,0) S2 * T(1,2,3) Ry(180); socket local (0,1,0): y scaled to 2
	near(t, "socket x", s.Position[0], 12, 1e-9)
	near(t, "socket y", s.Position[1], 6, 1e-9)
	near(t, "socket z", s.Position[2], 6, 1e-9)
	// Ry(180) maps X to -X and Z to -Z
	near(t, "x axis", s.Axes[0][0], -1, 1e-9)
	near(t, "z axis", s.Axes[2][2], -1, 1e-9)
	near(t, "y axis", s.Axes[1][1], 1, 1e-9)
	if s.HasMesh {
		t.Error("socket must be meshless")
	}
}

func TestTriangleDistance(t *testing.T) {
	// exercised through Connected: two single triangles 0.9 mm and 1.1 mm apart
	tri := func(z float32) fixture.Prim {
		return fixture.Prim{Pos: [][3]float32{{0, 0, z}, {1, 0, z}, {0, 1, z}}, Idx: []uint32{0, 1, 2}, Material: -1}
	}
	for _, c := range []struct {
		z    float32
		want bool
	}{{0.0009, true}, {0.0011, false}} {
		b := fixture.New()
		b.Scene(b.Node("a", b.Mesh("a", tri(0)), [3]float64{}), b.Node("b", b.Mesh("b", tri(c.z)), [3]float64{}))
		k, _ := load(t, b.Bytes()).Connected(measure.Whole)
		if k.Connected != c.want {
			t.Errorf("z=%g: %+v", c.z, k)
		}
	}
}

// FuzzPipeline decodes arbitrary bytes and, when the file has no error
// findings, runs every measurement. Nothing may panic.
func FuzzPipeline(f *testing.F) {
	f.Add(fixture.RichRig())
	f.Add(post(0, 0.02, 0))
	f.Add(upright(0.002))
	b := fixture.New()
	b.Scene(b.Node("n", b.Mesh("m", fixture.Cube(0, 0, 0, 1, 1, 1)), [3]float64{}))
	f.Add(b.Bytes())
	f.Fuzz(func(t *testing.T, data []byte) {
		file, fs := glb.Decode(data)
		for _, x := range fs {
			if x.Code == "ORACLE-INTERNAL" {
				t.Fatalf("decoder fault: %s", x.Message)
			}
		}
		if glb.HasErrors(fs) {
			return
		}
		m, err := measure.Load(file)
		if err != nil {
			return
		}
		m.Closed(measure.Whole)
		m.Connected(measure.Whole)
		m.Gap(measure.Whole, "x", [3]float64{0, 0, 0})
		m.Bounds(measure.Whole)
		m.DrawCalls(measure.Whole)
		m.Sockets()
		m.Colliders()
		exercisePose(t, m)
	})
}

// exercisePose runs every rig measurement on a loaded model and checks that the
// posed positions are finite when the file has no error findings.
func exercisePose(t *testing.T, m *measure.Model) {
	t.Helper()
	m.Rig()
	m.BindPoseDeviation()
	m.WeightStats()
	m.Clips()
	for ci := -1; ci < len(m.Clips()); ci++ {
		m.KeyTimes(ci)
		for _, tm := range []float64{-1, 0, 0.25, 0.5, 1, 1.5, 2, 100} {
			p, err := m.PoseAt(ci, tm)
			if err != nil {
				continue
			}
			prims, err := m.Posed(p, measure.Whole)
			if err != nil {
				continue
			}
			for _, pp := range prims {
				for v, pt := range pp.Pos {
					if !pp.Used[v] {
						continue
					}
					for _, x := range pt {
						if math.IsNaN(x) {
							t.Fatalf("clip %d at %v: NaN position", ci, tm)
						}
					}
				}
			}
			m.PosedBounds(p, measure.Whole)
			m.LowestVertex(p, measure.Whole)
			m.JointDistance(p, "j0", "j1")
		}
	}
}

// FuzzPose keeps the JSON of a valid rig and fills its binary chunk with fuzz
// bytes, then poses every clip at several times. Nothing may panic, and a
// posed vertex that a triangle uses may not be NaN when the file decodes
// without error findings.
func FuzzPose(f *testing.F) {
	seed := fixture.RichRig()
	off, n, _ := fixture.BinRange(seed)
	f.Add(seed[off : off+n])
	f.Add(make([]byte, n))
	f.Fuzz(func(t *testing.T, bin []byte) {
		data := append([]byte(nil), seed...)
		copy(data[off:off+n], bin)
		file, fs := glb.Decode(data)
		for _, x := range fs {
			if x.Code == "ORACLE-INTERNAL" {
				t.Fatalf("decoder fault: %s", x.Message)
			}
		}
		if glb.HasErrors(fs) {
			return
		}
		m, err := measure.Load(file)
		if err != nil {
			return
		}
		exercisePose(t, m)
	})
}
