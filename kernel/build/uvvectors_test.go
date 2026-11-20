package build

import (
	"math"
	"testing"

	"m31labs.dev/greenware/kernel/mesh"
)

// The open +Y lathe of the register (language reference 4.5): four angular
// segments, profile (1,0), (1,1), (1,4), the builder's own chart. At angular
// column 1 the three UVs are (1/4,0), (1/4,1/2), (1/4,1): the row share, not
// the profile length, sets v. The seam has corner copies at u = 0 and u = 1,
// and the two bands are 16 triangles before and after the corners split.
func TestLatheUVRegisterVector(t *testing.T) {
	m, err := Lathe(LatheParams{Axis: AxisY, Segments: 4, Rows: []mesh.Vec2{{1, 0}, {1, 1}, {1, 4}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tris) != 16 {
		t.Fatalf("%d triangles, want 16", len(m.Tris))
	}
	type uv [2]float64
	atCol := map[uv]bool{}
	seamLo, seamHi := map[float64]bool{}, map[float64]bool{}
	for _, c := range m.Corners {
		if math.Abs(c.UV[0]-0.25) < 1e-12 {
			atCol[uv{0.25, c.UV[1]}] = true
		}
		p := m.Positions[c.Pos]
		if math.Abs(p[0]) < 1e-12 && p[2] > 0 { // the seam column
			if c.UV[0] == 0 {
				seamLo[c.UV[1]] = true
			}
			if math.Abs(c.UV[0]-1) < 1e-12 {
				seamHi[c.UV[1]] = true
			}
		}
	}
	for _, v := range []float64{0, 0.5, 1} {
		if !atCol[uv{0.25, v}] {
			t.Errorf("no corner at (1/4, %v): %v", v, atCol)
		}
		if !seamLo[v] || !seamHi[v] {
			t.Errorf("the seam has no corner copies at u = 0 and u = 1 for v = %v", v)
		}
	}
}
