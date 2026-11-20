package mesh_test

import (
	"math"
	"testing"

	"m31labs.dev/greenware/kernel/build"
	"m31labs.dev/greenware/kernel/mesh"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

// panel is the slanted panel of the register: corners (0,0,0), (2,0,1),
// (3,2,1), (1,2,0) as two triangles.
func panel() *mesh.Mesh {
	m := &mesh.Mesh{
		Positions: []mesh.Vec3{{0, 0, 0}, {2, 0, 1}, {3, 2, 1}, {1, 2, 0}},
	}
	for i := 0; i < 4; i++ {
		m.Corners = append(m.Corners, mesh.Corner{Pos: int32(i), Normal: mesh.Vec3{0, 0, 1}})
	}
	m.Tris = []mesh.Tri{{0, 1, 2}, {0, 2, 3}}
	m.Shell = []int32{0, 0}
	return m
}

func TestPlanarPanelVector(t *testing.T) {
	out, err := panel().ProjectUV(mesh.ProjPlanar, mesh.ProjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tris) != 2 {
		t.Fatalf("the panel has two triangles, got %d", len(out.Tris))
	}
	want := map[int32][2]float64{0: {0, 0}, 1: {2.0 / 3, 0}, 2: {1, 1}, 3: {1.0 / 3, 1}}
	for _, c := range out.Corners {
		w := want[c.Pos]
		if !near(c.UV[0], w[0]) || !near(c.UV[1], w[1]) {
			t.Errorf("position %d: uv %v, want %v (local XY, normalised over [0,3] x [0,2])", c.Pos, c.UV, w)
		}
	}
	// The projection uses local X and Y only: z does not matter.
	if err := out.Validate(); err != nil {
		t.Error(err)
	}
}

func TestPlanarZeroWidthIsHalf(t *testing.T) {
	m := panel()
	for i := range m.Positions {
		m.Positions[i][0] = 5 // every x the same
	}
	out, _ := m.ProjectUV(mesh.ProjPlanar, mesh.ProjectOptions{})
	for _, c := range out.Corners {
		if c.UV[0] != 0.5 {
			t.Errorf("a zero-width dimension is 0.5, got %v", c.UV)
		}
	}
}

func TestSphericalPoles(t *testing.T) {
	m, err := build.Sphere(build.SphereParams{Radius: 1, Segments: 4, Rings: 2})
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.ProjectUV(mesh.ProjSpherical, mesh.ProjectOptions{Axis: 1, HasCentre: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tris) != len(m.Tris) {
		t.Fatalf("a projection changes corners, never triangles: %d != %d", len(out.Tris), len(m.Tris))
	}
	// Collect pole corners by position: v is 1 at the north pole and 0 at the south.
	north, south := map[float64]bool{}, map[float64]bool{}
	for i := range out.Tris {
		for _, ci := range out.Tris[i] {
			c := out.Corners[ci]
			p := out.Positions[c.Pos]
			switch {
			case near(p[1], 1) && near(p[0], 0) && near(p[2], 0):
				if !near(c.UV[1], 1) {
					t.Errorf("north pole v = %v, want 1", c.UV[1])
				}
				north[math.Round(c.UV[0]*8)/8] = true
			case near(p[1], -1) && near(p[0], 0) && near(p[2], 0):
				if !near(c.UV[1], 0) {
					t.Errorf("south pole v = %v, want 0", c.UV[1])
				}
				south[math.Round(c.UV[0]*8)/8] = true
			default:
				if !near(c.UV[1], 0.5) {
					t.Errorf("an equator corner has v = %v, want 0.5", c.UV[1])
				}
			}
		}
	}
	// Four longitudes: the pole corner of each pole triangle sits at the middle
	// of its interval, 1/8, 3/8, 5/8 and 7/8. The last interval is 7/8, not 3/8.
	for _, set := range []map[float64]bool{north, south} {
		for _, w := range []float64{0.125, 0.375, 0.625, 0.875} {
			if !set[w] {
				t.Errorf("pole longitudes %v lack %v", set, w)
			}
		}
	}
	// The triangle that adjoins longitudes 0 and 1/4 has its pole corner at 1/8,
	// and the one across the seam spans 3/4 to 1.
	for i := range out.Tris {
		var us []float64
		hasNorth := false
		for _, ci := range out.Tris[i] {
			c := out.Corners[ci]
			p := out.Positions[c.Pos]
			us = append(us, c.UV[0])
			if near(p[1], 1) {
				hasNorth = true
			}
		}
		if !hasNorth {
			continue
		}
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, u := range us {
			lo, hi = math.Min(lo, u), math.Max(hi, u)
		}
		if !(near(hi-lo, 0.25)) {
			t.Errorf("a pole triangle spans a quarter turn, got %v..%v", lo, hi)
		}
		if near(lo, 0.75) && !near(hi, 1) {
			t.Errorf("the last interval is 3/4..1, got %v..%v", lo, hi)
		}
	}
}

func TestSeamChangesCornersNotTriangles(t *testing.T) {
	m, _ := build.Sphere(build.SphereParams{Radius: 1, Segments: 8, Rings: 4})
	out, _ := m.ProjectUV(mesh.ProjSpherical, mesh.ProjectOptions{Axis: 1, HasCentre: true})
	if len(out.Positions) != len(m.Positions) || len(out.Tris) != len(m.Tris) {
		t.Fatal("positions and triangles must not change")
	}
	for i := range m.Tris {
		for k := 0; k < 3; k++ {
			if out.Corners[out.Tris[i][k]].Pos != m.Corners[m.Tris[i][k]].Pos {
				t.Fatalf("triangle %d corner %d uses another position", i, k)
			}
		}
	}
	// The triangles across the meridian lift the corner at longitude 0 to u = 1.
	over := false
	for _, c := range out.Corners {
		if near(c.UV[0], 1) {
			over = true
		}
		if c.UV[0] < -1e-12 || c.UV[0] > 1+1e-12 || c.UV[1] < -1e-12 || c.UV[1] > 1+1e-12 {
			t.Fatalf("uv %v out of range", c.UV)
		}
	}
	if !over {
		t.Error("the triangles across the meridian carry u = 1 on the lifted side")
	}
	if err := out.Validate(); err != nil {
		t.Error(err)
	}
}

func TestCylindricalAndBox(t *testing.T) {
	m, err := build.Cylinder(build.CylinderParams{Radius: 1, Height: 2, Segments: 8, Axis: build.AxisY, Caps: false})
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.ProjectUV(mesh.ProjCylindrical, mesh.ProjectOptions{Axis: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range out.Corners {
		p := out.Positions[c.Pos]
		if !near(c.UV[1], (p[1]+1)/2) {
			t.Errorf("height %v gave v = %v", p[1], c.UV[1])
		}
	}
	cube := cubeMesh()
	bo, err := cube.ProjectUV(mesh.ProjBox, mesh.ProjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range bo.Tris {
		for _, ci := range bo.Tris[i] {
			c := bo.Corners[ci]
			p := bo.Positions[c.Pos]
			if c.UV[0] < 0 || c.UV[0] > 1 || c.UV[1] < 0 || c.UV[1] > 1 {
				t.Errorf("box uv %v out of the unit square", c.UV)
			}
			// The +X face uses (y, z); the -X face uses (-y, z).
			if near(p[0], 1) && cubeFace(bo, i) == "+x" && (!near(c.UV[0], (p[1]+1)/2) || !near(c.UV[1], (p[2]+1)/2)) {
				t.Errorf("+x face corner %v has uv %v", p, c.UV)
			}
			if near(p[0], -1) && cubeFace(bo, i) == "-x" && (!near(c.UV[0], (-p[1]+1)/2) || !near(c.UV[1], (p[2]+1)/2)) {
				t.Errorf("-x face corner %v has uv %v", p, c.UV)
			}
		}
	}
}

// cubeMesh is a cube of side 2 centred on the origin, outward winding.
func cubeMesh() *mesh.Mesh {
	m := &mesh.Mesh{}
	quads := [][4]mesh.Vec3{
		{{1, -1, -1}, {1, 1, -1}, {1, 1, 1}, {1, -1, 1}},     // +x
		{{-1, -1, 1}, {-1, 1, 1}, {-1, 1, -1}, {-1, -1, -1}}, // -x
		{{-1, 1, -1}, {-1, 1, 1}, {1, 1, 1}, {1, 1, -1}},     // +y
		{{-1, -1, 1}, {-1, -1, -1}, {1, -1, -1}, {1, -1, 1}}, // -y
		{{-1, -1, 1}, {1, -1, 1}, {1, 1, 1}, {-1, 1, 1}},     // +z
		{{1, -1, -1}, {-1, -1, -1}, {-1, 1, -1}, {1, 1, -1}}, // -z
	}
	for _, q := range quads {
		base := int32(len(m.Positions))
		m.Positions = append(m.Positions, q[:]...)
		for k := int32(0); k < 4; k++ {
			m.Corners = append(m.Corners, mesh.Corner{Pos: base + k, Normal: mesh.Vec3{0, 0, 1}})
		}
		m.Tris = append(m.Tris, mesh.Tri{base, base + 1, base + 2}, mesh.Tri{base, base + 2, base + 3})
		m.Shell = append(m.Shell, 0, 0)
	}
	return m
}

// cubeFace names the face of triangle i from its first position's normal.
func cubeFace(m *mesh.Mesh, i int) string {
	a, b, c := m.TriPositions(i)
	n := mesh.Cross(mesh.Sub(b, a), mesh.Sub(c, a))
	switch {
	case n[0] > 0:
		return "+x"
	case n[0] < 0:
		return "-x"
	case n[1] > 0:
		return "+y"
	case n[1] < 0:
		return "-y"
	case n[2] > 0:
		return "+z"
	}
	return "-z"
}

func TestProjectUVErrors(t *testing.T) {
	if _, err := panel().ProjectUV(mesh.Projection(99), mesh.ProjectOptions{}); err == nil {
		t.Error("an unknown projection must fail")
	}
	if _, err := panel().ProjectUV(mesh.ProjPlanar, mesh.ProjectOptions{Axis: 3}); err == nil {
		t.Error("a bad axis must fail")
	}
	if p, ok := mesh.ProjectionOf("spherical"); !ok || p != mesh.ProjSpherical {
		t.Error("ProjectionOf")
	}
	if _, ok := mesh.ProjectionOf("auto"); ok {
		t.Error("auto is the builder's chart, not a projection")
	}
}
