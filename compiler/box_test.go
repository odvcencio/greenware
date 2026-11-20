package compiler

import (
	"strings"
	"testing"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/kernel/mesh"
)

// boxAsset is a one-part asset around a shape body.
func boxAsset(body string) string {
	return "greenware 1\nasset b { root = b length = m angle = deg }\nmaterial m = pbr { base = #ffffff }\npart b {\n  material = m\n" + body + "\n}\noutput o { file = \"b.glb\" }\n"
}

// buildBoxMesh builds the source and returns the merged mesh of its root node
// as written in the file (float32 positions).
func buildBoxMesh(t *testing.T, src string) (*mesh.Mesh, *File) {
	t.Helper()
	p, ds := Build(lowerSource(t, []byte(src)), "")
	if p == nil {
		t.Fatalf("build failed: %v", ds)
	}
	f := p.Outputs[0].Files[0]
	d := decode(t, f)
	_, n := nodeByName(t, d, "b")
	return nodeMesh(t, d, n), f
}

// nearVec compares within tol: the file stores float32 positions.
func nearVec(a, b mesh.Vec3, tol float64) bool {
	for i := range a {
		if d := a[i] - b[i]; d > tol || d < -tol {
			return false
		}
	}
	return true
}

func TestBoxShape(t *testing.T) {
	m, f := buildBoxMesh(t, boxAsset("box { size = (0.2, 1, 0.4) }"))
	if f.Triangles != 12 {
		t.Errorf("triangles = %d, want 12", f.Triangles)
	}
	lo, hi := boundsOf(m)
	if !nearVec(lo, mesh.Vec3{-0.1, -0.5, -0.2}, 1e-7) || !nearVec(hi, mesh.Vec3{0.1, 0.5, 0.2}, 1e-7) {
		t.Errorf("bounds %v %v", lo, hi)
	}
	// 24 corners: flat faces keep their own normals in the file.
	if f.Vertices != 24 {
		t.Errorf("vertices = %d, want 24", f.Vertices)
	}
	if !m.IsClosed() || m.SignedVolume() < 0.0799 || m.SignedVolume() > 0.0801 {
		t.Errorf("closed %v volume %g", m.IsClosed(), m.SignedVolume())
	}
}

func TestBoxAtSegmentsAndMaterialUnits(t *testing.T) {
	m, f := buildBoxMesh(t, boxAsset("box { at = (0, 500mm, 0) size = (100mm, 1m, 0.1m) segments = 2 }"))
	if f.Triangles != 48 {
		t.Errorf("triangles = %d, want 48", f.Triangles)
	}
	lo, hi := boundsOf(m)
	if lo[1] != 0 || hi[1] != 1 {
		t.Errorf("y bounds %g %g, want 0 and 1", lo[1], hi[1])
	}
}

func TestBoxBevelCounts(t *testing.T) {
	for _, c := range []struct {
		body string
		tris int
	}{
		{"box { size = (1, 1, 1) bevel { width = 0.1 } }", 44},
		{"box { size = (1, 1, 1) bevel { width = 0.1 segments = 2 } }", 92},
		{"box { size = (1, 2, 1) bevel { width = 0.1 segments = 3 edges = all } }", 156},
	} {
		m, f := buildBoxMesh(t, boxAsset(c.body))
		if f.Triangles != c.tris {
			t.Errorf("%s: %d triangles, want %d", c.body, f.Triangles, c.tris)
		}
		if !m.IsClosed() {
			t.Errorf("%s: not closed", c.body)
		}
	}
}

func TestBoxRefusals(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"edges top", "box { size = (1, 1, 1) bevel { width = 0.1 edges = top } }", "edges = top"},
		{"face grid", "box { size = (1, 1, 1) segments = 2 bevel { width = 0.1 } }", "segments = 1"},
		{"wide bevel", "box { size = (1, 1, 1) bevel { width = 0.5 } }", "consumes"},
	} {
		p, ds := Build(lowerSource(t, []byte(boxAsset(c.body))), "")
		if p != nil {
			t.Errorf("%s: built", c.name)
			continue
		}
		found := false
		for _, d := range ds {
			if strings.Contains(d.Message, c.want) && (d.Code == diag.CodeUnsupported || d.Code == diag.CodeFieldRange) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no diagnostic naming %q in %v", c.name, c.want, ds)
		}
	}
}

func TestBoxIsRepeatable(t *testing.T) {
	src := boxAsset("box { size = (0.1016, 0.4572, 0.1016) bevel { width = 8mm segments = 2 } }")
	_, a := buildBoxMesh(t, src)
	_, b := buildBoxMesh(t, src)
	if string(a.GLB) != string(b.GLB) {
		t.Error("two builds differ")
	}
}
