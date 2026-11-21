package compiler

import (
	"math"
	"testing"

	"m31labs.dev/greenware/gltf"
)

// chunkMeans builds src and returns the mean position of each of n equal runs
// of the first primitive's vertices.
func chunkMeans(t *testing.T, src string, n int) [][3]float64 {
	t.Helper()
	p, _, ds := buildOne(t, src, 0, nil)
	if p == nil {
		t.Fatalf("build failed: %v", ds)
	}
	d, err := gltf.Decode(p.Outputs[0].Files[0].GLB)
	if err != nil {
		t.Fatal(err)
	}
	pos := d.Meshes[0].Primitives[0].Positions
	nv := len(pos) / 3
	if nv%n != 0 {
		t.Fatalf("%d vertices do not split in %d", nv, n)
	}
	per := nv / n
	out := make([][3]float64, n)
	for c := 0; c < n; c++ {
		for v := c * per; v < (c+1)*per; v++ {
			for k := 0; k < 3; k++ {
				out[c][k] += float64(pos[3*v+k]) / float64(per)
			}
		}
	}
	return out
}

// An array that names its `index` resolves its body once for each copy with
// the index bound to the copy's number, so a copy can differ from the first.
func TestArrayIndexIsBoundPerCopy(t *testing.T) {
	src := `greenware 1
asset a { root = a }
part a {
  array bars {
    mode = linear
    count = 3
    step = (0.1m, 0m, 0m)
    index = n
    ellipsoid { centre = (0m, 0.01m * n * n, 0m) radii = (0.02m, 0.02m, 0.02m) segments = 6 rings = 4 seam = off }
  }
}
output demo { file = "a.glb" }
`
	m := chunkMeans(t, src, 3)
	for c := 0; c < 3; c++ {
		wantX, wantY := 0.1*float64(c), 0.01*float64(c*c)
		if math.Abs(m[c][0]-wantX) > 1e-6 || math.Abs(m[c][1]-wantY) > 1e-6 {
			t.Errorf("copy %d centred at (%.6f, %.6f), want (%.6f, %.6f)", c, m[c][0], m[c][1], wantX, wantY)
		}
	}
	// Without a named index the copies stay translations of the first.
	m = chunkMeans(t, `greenware 1
asset a { root = a }
part a {
  array bars {
    mode = linear
    count = 3
    step = (0.1m, 0m, 0m)
    ellipsoid { centre = (0m, 0m, 0m) radii = (0.02m, 0.02m, 0.02m) segments = 6 rings = 4 seam = off }
  }
}
output demo { file = "a.glb" }
`, 3)
	for c := 0; c < 3; c++ {
		if math.Abs(m[c][1]) > 1e-9 || math.Abs(m[c][0]-0.1*float64(c)) > 1e-6 {
			t.Errorf("unindexed copy %d at (%.6f, %.6f)", c, m[c][0], m[c][1])
		}
	}
}

// `reflect = inputs` builds the far side's shape from reflected inputs: the
// ring columns keep their direction, so column 1 of the right-hand copy lies
// at the same offset from its centre as column 1 of the left-hand one (a
// reflected mesh would put it at the opposite offset).
func TestMirrorReflectInputsKeepsColumnDirection(t *testing.T) {
	build := func(mode string) []float32 {
		src := `greenware 1
asset a { root = a }
part a {
  mirror {
    plane = x
    ` + mode + `
    ellipsoid (e_l, e_r) { centre = (0.3m, 0m, 0m) radii = (0.05m, 0.05m, 0.02m) segments = 6 rings = 4 seam = off }
  }
}
output demo { file = "a.glb" }
`
		p, _, ds := buildOne(t, src, 0, nil)
		if p == nil {
			t.Fatalf("build failed: %v", ds)
		}
		d, err := gltf.Decode(p.Outputs[0].Files[0].GLB)
		if err != nil {
			t.Fatal(err)
		}
		return d.Meshes[0].Primitives[0].Positions
	}
	off := func(pos []float32, shape, vertex int) float64 {
		per := len(pos) / 3 / 2
		return float64(pos[3*(shape*per+vertex)])
	}
	rin, rmesh := build("reflect = inputs"), build("")
	// vertex 7 is a column-1 vertex of ring 1 (6 columns a ring)
	if l, r := off(rin, 0, 7), off(rin, 1, 7); math.Abs((l-0.3)-(-0.3-r)) > 1e-6 && math.Abs((l-0.3)+(-0.3-r)) > 1e-6 {
		t.Errorf("unexpected offsets %.6f %.6f", l, r)
	}
	if l, r := off(rin, 0, 7), off(rin, 1, 7); math.Abs((l-0.3)-(r+0.3)) > 1e-6 {
		t.Errorf("inputs: the right copy's column 1 is at %.6f from its centre, the left's at %.6f; want the same", r+0.3, l-0.3)
	}
	if l, r := off(rmesh, 0, 7), off(rmesh, 1, 7); math.Abs((l-0.3)+(r+0.3)) > 1e-6 {
		t.Errorf("mesh: the reflected copy's column 1 is at %.6f from its centre, the left's at %.6f; want opposite", r+0.3, l-0.3)
	}
}
