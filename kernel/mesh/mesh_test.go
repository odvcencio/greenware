package mesh

import (
	"math"
	"testing"
)

// tetra returns a closed tetrahedron with outward winding, shell 0.
func tetra(offset Vec3) *Mesh {
	m := &Mesh{}
	m.Positions = []Vec3{
		Add(Vec3{0, 0, 0}, offset), Add(Vec3{1, 0, 0}, offset),
		Add(Vec3{0, 1, 0}, offset), Add(Vec3{0, 0, 1}, offset),
	}
	faces := [][3]int32{{0, 2, 1}, {0, 1, 3}, {0, 3, 2}, {1, 2, 3}}
	for _, f := range faces {
		base := int32(len(m.Corners))
		for _, p := range f {
			m.Corners = append(m.Corners, Corner{Pos: p})
		}
		m.Tris = append(m.Tris, Tri{base, base + 1, base + 2})
		m.Shell = append(m.Shell, 0)
	}
	m.ComputeNormals()
	return m
}

func TestTetraBasics(t *testing.T) {
	m := tetra(Vec3{})
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if !m.IsClosed() || m.OpenEdges() != 0 {
		t.Fatal("tetrahedron should be closed")
	}
	if v := m.SignedVolume(); math.Abs(v-1.0/6) > 1e-15 {
		t.Fatalf("volume %v", v)
	}
	if _, n := m.Components(); n != 1 {
		t.Fatalf("components %d", n)
	}
	lo, hi, ok := m.Bounds()
	if !ok || lo != (Vec3{0, 0, 0}) || hi != (Vec3{1, 1, 1}) {
		t.Fatal("bounds", lo, hi)
	}
	// Dropping a face opens three edges.
	open := m.Clone()
	open.Tris, open.Shell = open.Tris[:3], open.Shell[:3]
	if open.IsClosed() || open.OpenEdges() != 3 {
		t.Fatalf("open edges %d", open.OpenEdges())
	}
}

func TestWeldIsExact(t *testing.T) {
	m := &Mesh{Positions: []Vec3{{0, 0, 0}, {math.Copysign(0, -1), 0, 0}, {1e-12, 0, 0}, {0, 0, 0}}}
	w := m.Weld()
	if w.Count != 2 || w.ID[0] != w.ID[1] || w.ID[0] != w.ID[3] || w.ID[2] == w.ID[0] {
		t.Fatalf("weld ids %v count %d", w.ID, w.Count)
	}
}

func TestMergeRenumbersShells(t *testing.T) {
	a, b := tetra(Vec3{}), tetra(Vec3{5, 0, 0})
	m := Merge(a, b, tetra(Vec3{9, 0, 0}))
	if m.NumShells() != 3 || m.TriangleCount() != 12 {
		t.Fatalf("shells %d tris %d", m.NumShells(), m.TriangleCount())
	}
	if m.Shell[0] != 0 || m.Shell[4] != 1 || m.Shell[8] != 2 {
		t.Fatalf("shell ids %v", m.Shell)
	}
	if _, n := m.Components(); n != 3 {
		t.Fatalf("components %d", n)
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	// Merge of two meshes with two shells each renumbers to four.
	if got := Merge(m, m).NumShells(); got != 6 {
		t.Fatalf("shells %d", got)
	}
}

func TestMirrorFlipsWindingAndKeepsUV(t *testing.T) {
	m := tetra(Vec3{1, 0, 0})
	for i := range m.Corners {
		m.Corners[i].UV = Vec2{float64(i), 2}
	}
	r := m.Mirror(PlaneX)
	if v := r.SignedVolume(); v <= 0 {
		t.Fatalf("mirrored volume %v", v)
	}
	if r.Corners[3].UV != m.Corners[3].UV {
		t.Fatal("uv changed")
	}
	if r.Positions[1] != (Vec3{-2, 0, 0}) {
		t.Fatal(r.Positions[1])
	}
	// The mirrored normal of the face on x=offset reflects across the plane.
	for i, c := range r.Corners {
		want := m.Corners[i].Normal
		want[0] = -want[0]
		if Len(Sub(c.Normal, want)) > 1e-12 {
			t.Fatalf("corner %d normal %v want %v", i, c.Normal, want)
		}
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNonuniformScaleUsesInverseTranspose(t *testing.T) {
	// A face in the plane x + y = 1 scaled by (2, 1, 1) has plane x/2 + y = 1,
	// whose normal is (1, 2, 0)/sqrt(5), not the scaled old normal (2, 1, 0).
	m := &Mesh{
		Positions: []Vec3{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}},
		Corners:   []Corner{{Pos: 0}, {Pos: 1}, {Pos: 2}},
		Tris:      []Tri{{0, 1, 2}},
		Shell:     []int32{0},
	}
	m.ComputeNormals()
	s := m.Scaled(Vec3{2, 1, 1})
	n := s.Corners[0].Normal
	// The triangle spans (1,0,0), (0,1,0), (0,0,1): normal along (1,1,1).
	// After scaling the plane is x/2 + y + z = 1, so the normal is (1,2,2)/3.
	want := Vec3{1.0 / 3, 2.0 / 3, 2.0 / 3}
	if Len(Sub(n, want)) > 1e-12 {
		t.Fatalf("normal %v want %v", n, want)
	}
	a, b, c := s.TriPositions(0)
	fn := Normalize(Cross(Sub(b, a), Sub(c, a)))
	if Len(Sub(fn, n)) > 1e-12 {
		t.Fatalf("face normal %v vs corner normal %v", fn, n)
	}
}

func TestRotateAndTranslate(t *testing.T) {
	m := tetra(Vec3{})
	r := m.Rotated(Vec3{0, 0, 1}, math.Pi/2).Translate(Vec3{1, 2, 3})
	p := r.Positions[1] // (1,0,0) -> (0,1,0) -> (1,3,3)
	if Len(Sub(p, Vec3{1, 3, 3})) > 1e-12 {
		t.Fatal(p)
	}
	if math.Abs(r.SignedVolume()-m.SignedVolume()) > 1e-12 {
		t.Fatal("volume changed")
	}
}

func TestNormalsAverageWithinShellOnly(t *testing.T) {
	// Two triangles share an edge at an angle in shell 0. A copy of the first
	// triangle's positions sits in shell 1 with a different face direction.
	m := &Mesh{}
	m.Positions = []Vec3{
		{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {0, 0, 1}, // shell 0 uses 0,1,2 then 0,3,1
		{0, 0, 0}, {0, 0, 1}, {1, 0, 0}, // shell 1: same coordinates as 0, 3, 1, reversed winding
	}
	add := func(shell int32, p0, p1, p2 int32) {
		b := int32(len(m.Corners))
		m.Corners = append(m.Corners, Corner{Pos: p0}, Corner{Pos: p1}, Corner{Pos: p2})
		m.Tris = append(m.Tris, Tri{b, b + 1, b + 2})
		m.Shell = append(m.Shell, shell)
	}
	add(0, 0, 1, 2)
	add(0, 0, 3, 1) // faces +Y
	add(1, 4, 5, 6)
	m.ComputeNormals()
	// Shell 0 corner at (0,0,0) averages the +Z and +Y face area vectors.
	n0 := m.Corners[0].Normal
	if n0[0] != 0 || n0[1] <= 0 || n0[2] <= 0 {
		t.Fatalf("shell 0 normal %v", n0)
	}
	// Corners at the same position in the first triangle and the second agree.
	if m.Corners[0].Normal != m.Corners[3].Normal {
		t.Fatal("welded corners differ")
	}
	// Shell 1 follows its own single face only.
	a, b, c := m.TriPositions(2)
	want := Normalize(Cross(Sub(b, a), Sub(c, a)))
	if m.Corners[6].Normal != want {
		t.Fatalf("shell 1 normal %v want %v", m.Corners[6].Normal, want)
	}
}

func TestSinCosTurn(t *testing.T) {
	for _, n := range []int{3, 4, 6, 12, 16, 24, 32, 7} {
		for j := 0; j < n; j++ {
			s, c := SinCosTurn(j, n)
			if math.Abs(s*s+c*c-1) > 1e-15 {
				t.Fatalf("n=%d j=%d not unit", n, j)
			}
			if want := math.Sin(2 * math.Pi * float64(j) / float64(n)); math.Abs(s-want) > 1e-14 {
				t.Fatalf("n=%d j=%d sin %v want %v", n, j, s, want)
			}
			if want := math.Cos(2 * math.Pi * float64(j) / float64(n)); math.Abs(c-want) > 1e-14 {
				t.Fatalf("n=%d j=%d cos %v want %v", n, j, c, want)
			}
		}
	}
	if s, c := SinCosTurn(3, 12); s != 1 || c != 0 {
		t.Fatalf("quarter turn not exact: %v %v", s, c)
	}
	if s, c := SinCosTurn(9, 12); s != -1 || c != 0 {
		t.Fatalf("three-quarter turn not exact: %v %v", s, c)
	}
}

func TestCanonicalBytesFoldNegativeZero(t *testing.T) {
	a := tetra(Vec3{})
	b := a.Clone()
	b.Positions[0] = Vec3{math.Copysign(0, -1), 0, 0}
	if a.SHA256() != b.SHA256() {
		t.Fatal("negative zero changed the hash")
	}
	b.Positions[1][0] = 1.0000000000000002
	if a.SHA256() == b.SHA256() {
		t.Fatal("a one-ulp change did not change the hash")
	}
}

func TestFloat32Accessors(t *testing.T) {
	m := tetra(Vec3{})
	if len(m.PositionsF32()) != 12 || len(m.NormalsF32()) != 36 || len(m.UVsF32()) != 24 {
		t.Fatal("lengths")
	}
}
