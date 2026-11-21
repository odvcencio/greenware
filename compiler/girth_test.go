package compiler

import (
	"math"
	"strings"
	"testing"

	"m31labs.dev/greenware/diag"
)

// A one-joint figure with a loft of three level rings (heights 0, 1 and 2, the
// centre line on the y axis, radius 1) and a morph that gives it a girth.
func girthSrc(morphBody string) string {
	return `greenware 1

asset g {
  root = body
  length = m
  angle = deg
  idle = rest
}

param amount : ratio = 1 in 0..1 runtime

skeleton s {
  joint base { at = (0, 0, 0) }
}

part body {
  joint = base
  loft tube8 {
    segments = 8
    ring (0, 0, 0, 1, 1)
    ring (0, 1, 0, 1, 1)
    ring (0, 2, 0, 1, 1)
  }
}

morph fat {
  weight = amount
` + morphBody + `
}

clip rest { duration = 1s  pose { base = rot(0, 0, 0) } }

output g { file = "g.glb" }
`
}

// girth moves each position by the amounts at its own height: side along x,
// front and back along z, about the ring's centre, and never in y.
func TestGirthMorphMovesRingsByHeight(t *testing.T) {
	_, d, _ := buildFixture(t, girthSrc("  girth { parts = [tube8]  side = 0.5 * y  front = 0.25  back = 0.1 + y / 10 }"))
	prim := d.Meshes[0].Primitives[0]
	if len(prim.Targets) != 1 {
		t.Fatalf("%d targets", len(prim.Targets))
	}
	delta := prim.Targets[0].Positions
	if len(delta) != len(prim.Positions) {
		t.Fatalf("%d delta values for %d position values", len(delta), len(prim.Positions))
	}
	moved := 0
	for i := 0; i+2 < len(prim.Positions); i += 3 {
		x, y, z := float64(prim.Positions[i]), float64(prim.Positions[i+1]), float64(prim.Positions[i+2])
		wx := x * 0.5 * y
		wz := z * (0.1 + y/10)
		if z < 0 {
			wz = z * 0.25
		}
		if math.Abs(float64(delta[i])-wx) > 1e-5 || delta[i+1] != 0 || math.Abs(float64(delta[i+2])-wz) > 1e-5 {
			t.Fatalf("vertex %d at (%.3f, %.3f, %.3f) moves by %v, want (%.5f, 0, %.5f)", i/3, x, y, z, delta[i:i+3], wx, wz)
		}
		if wx != 0 || wz != 0 {
			moved++
		}
	}
	if moved == 0 {
		t.Error("nothing moved")
	}
}

// An amount that cannot be evaluated is reported where it is written; one that
// reads a runtime parameter is refused; a shape without a centre line is
// outside the compiler's subset.
func TestGirthMorphRefusals(t *testing.T) {
	for _, c := range []struct {
		name, body, code, frag string
	}{
		{"unknown name", "  girth { parts = [tube8] side = 0.5 * height }", diag.CodeUnknownName, "height"},
		{"runtime parameter", "  girth { parts = [tube8] side = amount }", diag.CodeRuntime, "runtime parameter"},
		{"no amount", "  girth { parts = [tube8] }", diag.CodeMissing, "side"},
		{"not a number", "  girth { parts = [tube8] side = (1, 2) }", diag.CodeType, ""},
	} {
		p, ds := Build(lowerSource(t, []byte(girthSrc(c.body))), "")
		if p != nil {
			t.Errorf("%s: built", c.name)
			continue
		}
		if hasCode(ds, c.code, c.frag) == nil {
			t.Errorf("%s: no %s mentioning %q in %v", c.name, c.code, c.frag, ds)
		}
	}
	// A box has no centre line.
	src := strings.Replace(girthSrc("  girth { parts = [slab] side = 0.1 }"), "  loft tube8 {", "  box slab { size = (1, 1, 1) }\n  loft tube8 {", 1)
	p, ds := Build(lowerSource(t, []byte(src)), "")
	if p != nil || hasCode(ds, diag.CodeUnsupported, "girth") == nil {
		t.Errorf("a girth of a box: %v", ds)
	}
}

// seam = off writes n corners a ring and weld_caps = on fans each cap over the
// end ring's own corners, so a capped stack of k rings has k*n vertices.
func TestRingStackSeamAndWeldCapsCountVertices(t *testing.T) {
	const n, k = 8, 3
	for _, c := range []struct {
		name, fields string
		verts        int
	}{
		{"default", "", k*(n+1) + 2*n},
		{"seam off", "seam = off", k*n + 2*n},
		{"weld caps", "weld_caps = on", k * (n + 1)},
		{"both", "seam = off weld_caps = on", k * n},
	} {
		src := strings.Replace(girthSrc("  girth { parts = [tube8] side = 0.1 }"), "    segments = 8\n", "    segments = 8\n    "+c.fields+"\n", 1)
		_, d, _ := buildFixture(t, src)
		prim := d.Meshes[0].Primitives[0]
		if got := len(prim.Positions) / 3; got != c.verts {
			t.Errorf("%s: %d vertices, want %d", c.name, got, c.verts)
		}
		if got, want := len(prim.Indices)/3, 2*n*(k-1)+2*(n-2); got != want {
			t.Errorf("%s: %d triangles, want %d", c.name, got, want)
		}
	}
}
