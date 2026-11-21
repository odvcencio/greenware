package build

import (
	"testing"

	"m31labs.dev/greenware/kernel/mesh"
)

// distinctCorners counts the corners that differ in position, normal or UV:
// the vertices a writer would emit.
func distinctCorners(m *mesh.Mesh) int {
	type key struct {
		p, n mesh.Vec3
		uv   mesh.Vec2
	}
	seen := map[key]bool{}
	for _, c := range m.Corners {
		seen[key{m.Positions[c.Pos], c.Normal, c.UV}] = true
	}
	return len(seen)
}

func TestLoftNoSeamAndShareCaps(t *testing.T) {
	ys := []float64{0, 1, 2}
	rs := []float64{1, 1.2, 0.8}
	const n, k = 8, 3
	rings := func() [][]mesh.Vec3 { return fullRings(n, ys, rs) }
	cases := []struct {
		name               string
		noSeam, share      bool
		wantVerts, wantTri int
	}{
		{"default", false, false, k*(n+1) + 2*n, 2*n*(k-1) + 2*(n-2)},
		{"no seam", true, false, k*n + 2*n, 2*n*(k-1) + 2*(n-2)},
		{"shared caps", false, true, k * (n + 1), 2*n*(k-1) + 2*(n-2)},
		{"both", true, true, k * n, 2*n*(k-1) + 2*(n-2)},
	}
	for _, c := range cases {
		m, err := Loft(LoftParams{Rings: rings(), Caps: CapsBoth, NoSeam: c.noSeam, ShareCaps: c.share})
		check(t, c.name, m, err)
		if m.TriangleCount() != c.wantTri {
			t.Errorf("%s: %d triangles, want %d", c.name, m.TriangleCount(), c.wantTri)
		}
		if got := distinctCorners(m); got != c.wantVerts {
			t.Errorf("%s: %d distinct vertices, want %d", c.name, got, c.wantVerts)
		}
		if !m.IsClosed() {
			t.Errorf("%s: capped loft is not closed", c.name)
		}
	}
}

// Without the seam the last band of each ring joins column n-1 to column 0, so
// u runs from (n-1)/n back to 0 across it, as a plain ring stack does.
func TestLoftNoSeamWrapsU(t *testing.T) {
	const n = 6
	m, err := Loft(LoftParams{Rings: fullRings(n, []float64{0, 1}, []float64{1, 1}), Caps: CapsOff, NoSeam: true})
	check(t, "loft", m, err)
	if len(m.Corners) != 2*n {
		t.Fatalf("%d corners, want %d", len(m.Corners), 2*n)
	}
	for _, c := range m.Corners {
		if c.UV[0] >= 1 {
			t.Errorf("a corner has u = %v: a seamless ring has none at 1", c.UV[0])
		}
	}
}

func TestEllipsoidAndTubeNoSeam(t *testing.T) {
	e, err := Ellipsoid(EllipsoidParams{Radii: mesh.Vec3{1, 2, 1}, Segments: 12, Rings: 8, Floor: 0.0002, NoSeam: true, ShareCaps: true})
	check(t, "ellipsoid", e, err)
	if got, want := distinctCorners(e), 8*12; got != want {
		t.Errorf("ellipsoid: %d vertices, want %d", got, want)
	}
	if want := 2*12*7 + 2*10; e.TriangleCount() != want {
		t.Errorf("ellipsoid: %d triangles, want %d", e.TriangleCount(), want)
	}
	path := []mesh.Vec3{{0, 0, 0}, {0, 1, 0}, {0.2, 2, 0}}
	tb, err := Tube(TubeParams{Radius: 0.1, Segments: 5, Path: path, NoSeam: true})
	check(t, "tube", tb, err)
	if got, want := len(tb.Corners), 3*5; got != want {
		t.Errorf("tube: %d corners, want %d", got, want)
	}
	if want := 2 * 5 * 2; tb.TriangleCount() != want {
		t.Errorf("tube: %d triangles, want %d", tb.TriangleCount(), want)
	}
}
