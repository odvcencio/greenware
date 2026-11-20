package build

import (
	"reflect"
	"testing"

	"m31labs.dev/greenware/kernel/mesh"
)

func TestBoxPlain(t *testing.T) {
	m, err := Box(BoxParams{Size: mesh.Vec3{0.1016, 1.2, 0.1016}})
	m = check(t, "box", m, err)
	if got := m.TriangleCount(); got != 12 {
		t.Errorf("triangles = %d, want 12", got)
	}
	if got := len(m.Corners); got != 24 {
		t.Errorf("corners = %d, want 24", got)
	}
	if got := len(m.Positions); got != 8 {
		t.Errorf("positions = %d, want 8", got)
	}
	if !m.IsClosed() {
		t.Error("box is not closed")
	}
	if v := m.SignedVolume(); !near(v, 0.1016*1.2*0.1016, 1e-12) {
		t.Errorf("signed volume = %g, want %g (outward winding)", v, 0.1016*1.2*0.1016)
	}
	lo, hi, _ := m.Bounds()
	if lo != (mesh.Vec3{-0.0508, -0.6, -0.0508}) || hi != (mesh.Vec3{0.0508, 0.6, 0.0508}) {
		t.Errorf("bounds = %v %v", lo, hi)
	}
	// Every corner normal is a face normal and agrees with its triangle.
	for i, tr := range m.Tris {
		a, b, c := m.TriPositions(i)
		n := mesh.Normalize(mesh.Cross(mesh.Sub(b, a), mesh.Sub(c, a)))
		for _, ci := range tr {
			if mesh.Dot(n, m.Corners[ci].Normal) < 1-1e-12 {
				t.Fatalf("triangle %d normal %v != corner normal %v", i, n, m.Corners[ci].Normal)
			}
		}
	}
	// One unit square of UV per face.
	for _, c := range m.Corners {
		for _, x := range c.UV {
			if x != 0 && x != 1 {
				t.Fatalf("corner uv %v is not on the unit square corners", c.UV)
			}
		}
	}
}

func TestBoxFaceUVOrientation(t *testing.T) {
	m, err := Box(BoxParams{Size: mesh.Vec3{2, 4, 6}})
	m = check(t, "box", m, err)
	// The +X face projects on YZ: u from y, v from z. The -X face negates u.
	for _, c := range m.Corners {
		p := m.Positions[c.Pos]
		switch c.Normal {
		case mesh.Vec3{1, 0, 0}:
			if c.UV != (mesh.Vec2{(p[1] + 2) / 4, (p[2] + 3) / 6}) {
				t.Fatalf("+X corner %v has uv %v", p, c.UV)
			}
		case mesh.Vec3{-1, 0, 0}:
			if c.UV != (mesh.Vec2{1 - (p[1]+2)/4, (p[2] + 3) / 6}) {
				t.Fatalf("-X corner %v has uv %v", p, c.UV)
			}
		}
	}
}

func TestBoxSegments(t *testing.T) {
	for _, g := range []int{2, 3, 4} {
		m, err := Box(BoxParams{Size: mesh.Vec3{1, 2, 3}, Segments: g})
		m = check(t, "box", m, err)
		if got, want := m.TriangleCount(), 12*g*g; got != want {
			t.Errorf("segments %d: triangles = %d, want %d", g, got, want)
		}
		if !m.IsClosed() {
			t.Errorf("segments %d: not closed", g)
		}
		if v := m.SignedVolume(); !near(v, 6, 1e-9) {
			t.Errorf("segments %d: volume %g", g, v)
		}
	}
	// Even divisions put a lattice line exactly on the centre plane.
	m, _ := Box(BoxParams{Size: mesh.Vec3{1, 2, 3}, Segments: 2})
	found := false
	for _, p := range m.Positions {
		if p == (mesh.Vec3{0, 0, 1.5}) {
			found = true
		}
	}
	if !found {
		t.Error("no vertex at the centre of the +Z face")
	}
}

func TestBoxBevel(t *testing.T) {
	for b := 1; b <= 4; b++ {
		m, err := Box(BoxParams{Size: mesh.Vec3{1, 2, 3}, Bevel: &BoxBevel{Width: 0.1, Segments: b}})
		m = check(t, "bevel box", m, err)
		if got, want := m.TriangleCount(), 12+24*b+8*b*b; got != want {
			t.Errorf("bevel %d: triangles = %d, want %d", b, got, want)
		}
		if b == 1 && m.TriangleCount() != 44 {
			t.Errorf("a one-segment bevel has %d triangles, want 44 (language reference 3.10)", m.TriangleCount())
		}
		if !m.IsClosed() {
			t.Errorf("bevel %d: not closed (open edges %d)", b, m.OpenEdges())
		}
		lo, hi, _ := m.Bounds()
		if lo != (mesh.Vec3{-0.5, -1, -1.5}) || hi != (mesh.Vec3{0.5, 1, 1.5}) {
			t.Errorf("bevel %d: bounds %v %v", b, lo, hi)
		}
		if v := m.SignedVolume(); !(v > 0 && v < 6) {
			t.Errorf("bevel %d: volume %g, want inside (0, 6)", b, v)
		}
		for i, tr := range m.Tris {
			a, bb, c := m.TriPositions(i)
			n := mesh.Cross(mesh.Sub(bb, a), mesh.Sub(c, a))
			if mesh.Dot(n, m.Corners[tr[0]].Normal) <= 0 {
				t.Fatalf("bevel %d: triangle %d faces inward", b, i)
			}
		}
	}
}

func TestBoxBevelRefusals(t *testing.T) {
	size := mesh.Vec3{1, 2, 3}
	for name, p := range map[string]BoxParams{
		"consumes a face":   {Size: size, Bevel: &BoxBevel{Width: 0.5, Segments: 1}},
		"face grid":         {Size: size, Segments: 2, Bevel: &BoxBevel{Width: 0.1, Segments: 1}},
		"zero size":         {Size: mesh.Vec3{1, 0, 1}},
		"negative segments": {Size: size, Segments: -1},
	} {
		if _, err := Box(p); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestBoxDeterministic(t *testing.T) {
	p := BoxParams{Centre: mesh.Vec3{1, 2, 3}, Size: mesh.Vec3{0.3, 0.2, 0.7}, Bevel: &BoxBevel{Width: 0.03, Segments: 3}}
	a, _ := Box(p)
	b, _ := Box(p)
	if !reflect.DeepEqual(a, b) {
		t.Error("two builds differ")
	}
}
