package compiler

import (
	"math"
	"os"
	"strings"
	"testing"

	"m31labs.dev/greenware/kernel/mesh"
)

const pylonBoxPath = "../testdata/compiler/pylon-box.gw"

func pylonBoxSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(pylonBoxPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func buildPylon(t *testing.T, src string) (*mesh.Mesh, *File, *Product) {
	t.Helper()
	p, ds := Build(lowerSource(t, []byte(src)), "")
	if p == nil {
		t.Fatalf("build failed: %v", ds)
	}
	f := p.Outputs[0].Files[0]
	d := decode(t, f)
	_, n := nodeByName(t, d, "pylon")
	return nodeMesh(t, d, n), f, p
}

// The pylon of the game: 4 in = 0.1016 m wide, 18 in = 0.4572 m tall, written
// with `4in` and `18in`, as a box turned 45 degrees about y.
func TestPylonBoxMatchesTheGamesNumbers(t *testing.T) {
	m, f, _ := buildPylon(t, pylonBoxSource(t))
	if f.Triangles != 12 || f.Draws != 1 {
		t.Errorf("%d triangles and %d draws, want 12 and 1", f.Triangles, f.Draws)
	}
	if !m.IsClosed() || m.OpenEdges() != 0 {
		t.Errorf("not closed: %d open edges", m.OpenEdges())
	}
	if v, want := m.SignedVolume(), 0.1016*0.1016*0.4572; math.Abs(v-want) > 1e-7 {
		t.Errorf("volume %g, want %g", v, want)
	}
	lo, hi := boundsOf(m)
	// Turned 45 degrees, the corners are on the axes at half the diagonal.
	diag := 0.1016 / 2 * math.Sqrt2
	if !nearVec(lo, mesh.Vec3{-diag, 0, -diag}, 1e-6) || !nearVec(hi, mesh.Vec3{diag, 0.4572, diag}, 1e-6) {
		t.Errorf("bounds %v %v, want x and z +-%g and y in [0, 0.4572]", lo, hi, diag)
	}
	// The corner socket is on the axis at the top.
	p, _ := Build(lowerSource(t, []byte(pylonBoxSource(t))), "")
	d := decode(t, p.Outputs[0].Files[0])
	_, s := nodeByName(t, d, "socket.corner")
	if s.Translation == nil || math.Abs(float64(s.Translation[1])-0.4572) > 1e-6 {
		t.Errorf("socket %v", s.Translation)
	}
}

// Without `rot` the faces lie on the axes: the half width is exactly 4 in / 2.
func TestPylonBoxWithoutRotHasFacesOnTheAxes(t *testing.T) {
	src := strings.Replace(pylonBoxSource(t), "    rot = (0, 45deg, 0)\n", "", 1)
	m, _, _ := buildPylon(t, src)
	lo, hi := boundsOf(m)
	if !nearVec(lo, mesh.Vec3{-0.0508, 0, -0.0508}, 1e-7) || !nearVec(hi, mesh.Vec3{0.0508, 0.4572, 0.0508}, 1e-7) {
		t.Errorf("bounds %v %v", lo, hi)
	}
}

// The inch literal is the same number as the metric spelling: the file from
// `4in` and the file from `0.1016m` are the same bytes.
func TestPylonBoxInchEqualsMetres(t *testing.T) {
	_, fa, _ := buildPylon(t, pylonBoxSource(t))
	src := strings.Replace(strings.Replace(pylonBoxSource(t), "4in", "101.6mm", 1), "18in", "457.2mm", 1)
	_, fb, _ := buildPylon(t, src)
	if fa.SHA256 != fb.SHA256 {
		t.Error("`4in` and `101.6mm` give different files")
	}
}

// Rounded edges: the box bevel gives 12 + 24b + 8b^2 triangles and stays closed.
func TestPylonBoxWithABevelIsClosed(t *testing.T) {
	src := strings.Replace(pylonBoxSource(t), "    rot = (0, 45deg, 0)\n", "    rot = (0, 45deg, 0)\n    bevel { width = 8mm segments = 2 }\n", 1)
	m, f, _ := buildPylon(t, src)
	if f.Triangles != 12+48+32 {
		t.Errorf("%d triangles, want 92", f.Triangles)
	}
	if !m.IsClosed() {
		t.Errorf("open edges %d", m.OpenEdges())
	}
}

func TestPylonBoxIsRepeatable(t *testing.T) {
	_, a, _ := buildPylon(t, pylonBoxSource(t))
	_, b, _ := buildPylon(t, pylonBoxSource(t))
	if string(a.GLB) != string(b.GLB) {
		t.Error("two builds differ")
	}
}
