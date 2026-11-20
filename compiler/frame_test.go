package compiler

import (
	"math"
	"strings"
	"testing"

	"m31labs.dev/greenware/kernel/mesh"
)

func TestShapeRotBoxFortyFiveDegrees(t *testing.T) {
	m, f := buildBoxMesh(t, boxAsset("box { size = (0.2, 1, 0.4) rot = (0, 45deg, 0) }"))
	if f.Triangles != 12 {
		t.Errorf("triangles = %d, want the unturned box's 12", f.Triangles)
	}
	if !m.IsClosed() {
		t.Error("turned box is not closed")
	}
	if v := m.SignedVolume(); math.Abs(v-0.08) > 1e-6 {
		t.Errorf("volume %g, want 0.08 (a turn keeps volume and winding)", v)
	}
	h := math.Sqrt(0.5)
	sides := 0
	for _, c := range m.Corners {
		n := c.Normal
		switch {
		case math.Abs(n[1]) > 0.5: // top and bottom stay vertical
			if math.Abs(math.Abs(n[1])-1) > 1e-6 || n[0] != 0 || n[2] != 0 {
				t.Fatalf("cap normal %v", n)
			}
		default:
			sides++
			if math.Abs(math.Abs(float64(n[0]))-h) > 1e-6 || math.Abs(math.Abs(float64(n[2]))-h) > 1e-6 {
				t.Fatalf("side normal %v is not at 45 degrees", n)
			}
		}
	}
	if sides != 16 {
		t.Errorf("%d side corners, want 16", sides)
	}
	// The half extent along x of a 0.2 x 0.4 rectangle turned 45 degrees.
	lo, hi := boundsOf(m)
	want := (0.1 + 0.2) * h
	if math.Abs(hi[0]-want) > 1e-6 || math.Abs(lo[0]+want) > 1e-6 || math.Abs(hi[2]-want) > 1e-6 {
		t.Errorf("bounds %v %v, want x and z half extent %g", lo, hi, want)
	}
}

func TestShapeRotCylinderLiesAlongZ(t *testing.T) {
	m, _ := buildBoxMesh(t, boxAsset("cylinder { at = (0, 1, 0) radius = 0.1 height = 2 segments = 12 rot = (90deg, 0, 0) }"))
	lo, hi := boundsOf(m)
	// Exact quarter turn: the ends are at +-1 on z with no rounding noise, and
	// the axis is at y = 1 with radius 0.1.
	if lo[2] != -1 || hi[2] != 1 {
		t.Errorf("z bounds %g %g, want -1 and 1", lo[2], hi[2])
	}
	if !nearVec(mesh.Vec3{lo[0], lo[1], 0}, mesh.Vec3{-0.1, 0.9, 0}, 1e-6) || !nearVec(mesh.Vec3{hi[0], hi[1], 0}, mesh.Vec3{0.1, 1.1, 0}, 1e-6) {
		t.Errorf("bounds %v %v", lo, hi)
	}
}

func TestShapeRotKeepsAtUnturned(t *testing.T) {
	// A 1 x 1 x 3 box at x = 1, turned 90 degrees about y: its 3 m side now
	// runs along x about the point (1, 0, 0), and `at` was not turned.
	m, _ := buildBoxMesh(t, boxAsset("box { at = (1, 0, 0) size = (1, 1, 3) rot = (0, 90deg, 0) }"))
	lo, hi := boundsOf(m)
	if lo[0] != -0.5 || hi[0] != 2.5 || lo[2] != -0.5 || hi[2] != 0.5 {
		t.Errorf("bounds %v %v", lo, hi)
	}
}

func TestRotIsEulerXThenYThenZ(t *testing.T) {
	// rot = (90, 90, 0) turns +X to +Y: Y first (+X to -Z), then X (-Z to +Y).
	m, _ := buildBoxMesh(t, boxAsset("box { size = (2, 0.2, 0.2) rot = (90deg, 90deg, 0) }"))
	lo, hi := boundsOf(m)
	if hi[1] != 1 || lo[1] != -1 || !nearVec(mesh.Vec3{hi[0], 0, hi[2]}, mesh.Vec3{0.1, 0, 0.1}, 1e-6) {
		t.Errorf("bounds %v %v: the long side should lie along y", lo, hi)
	}
}

func fileNodes(t *testing.T, src string) map[string][3]float32 {
	t.Helper()
	p, ds := Build(lowerSource(t, []byte(src)), "")
	if p == nil {
		t.Fatalf("build failed: %v", ds)
	}
	d := decode(t, p.Outputs[0].Files[0])
	out := map[string][3]float32{}
	for _, n := range d.Nodes {
		if n.Translation != nil {
			out[n.Name] = *n.Translation
		}
	}
	return out
}

func TestPartRotMovesChildrenAndSockets(t *testing.T) {
	src := boxAsset(`  part arm {
    export = on
    at = (0, 2, 0)
    rot = (0, 90deg, 0)
    box { at = (1, 0, 0) size = (0.2, 0.2, 0.2) }
    socket tip { kind = tip at = (1, 0.5, 0) }
  }`)
	p, ds := Build(lowerSource(t, []byte(src)), "")
	if p == nil {
		t.Fatalf("build failed: %v", ds)
	}
	d := decode(t, p.Outputs[0].Files[0])
	_, arm := nodeByName(t, d, "arm")
	if arm.Translation == nil || *arm.Translation != [3]float32{0, 2, 0} {
		t.Errorf("arm translation %v, want (0, 2, 0)", arm.Translation)
	}
	// Ry(90) takes (1, 0, 0) to (0, 0, -1): the box and the socket go together.
	m := nodeMesh(t, d, arm)
	lo, hi := boundsOf(m)
	if !nearVec(lo, mesh.Vec3{-0.1, -0.1, -1.1}, 1e-6) || !nearVec(hi, mesh.Vec3{0.1, 0.1, -0.9}, 1e-6) {
		t.Errorf("child bounds %v %v", lo, hi)
	}
	_, sock := nodeByName(t, d, "socket.tip")
	if sock.Translation == nil || *sock.Translation != [3]float32{0, 0.5, -1} {
		t.Errorf("socket at %v, want (0, 0.5, -1)", sock.Translation)
	}
}

func TestPartScaleAndPivot(t *testing.T) {
	// Doubling x about the pivot (1, 0, 0) leaves the centre of a box there.
	m, _ := buildBoxMesh(t, boxAsset(`  part wide {
    pivot = (1, 0, 0)
    scale = (2, 1, 1)
    box { at = (1, 0, 0) size = (1, 1, 1) }
  }`))
	lo, hi := boundsOf(m)
	if lo[0] != 0 || hi[0] != 2 || lo[1] != -0.5 || hi[1] != 0.5 {
		t.Errorf("bounds %v %v, want x in [0, 2]", lo, hi)
	}
	// Turning about a pivot keeps the pivot fixed.
	m, _ = buildBoxMesh(t, boxAsset(`  part swing {
    pivot = (1, 0, 0)
    rot = (0, 0, 90deg)
    box { at = (1, 0, 0) size = (1, 1, 1) }
  }`))
	lo, hi = boundsOf(m)
	if lo[0] != 0.5 || hi[0] != 1.5 || lo[1] != -0.5 || hi[1] != 0.5 {
		t.Errorf("bounds %v %v: the box centre should stay at (1, 0, 0)", lo, hi)
	}
}

func TestInstanceRotAndScale(t *testing.T) {
	src := "greenware 1\nasset b { root = b length = m angle = deg }\nmaterial m = pbr { base = #ffffff }\n" +
		"part peg { box { at = (1, 0, 0) size = (0.2, 0.2, 0.2) } }\n" +
		"part b { material = m instance { of = peg rot = (0, 90deg, 0) scale = (1, 2, 1) at = (0, 0, 5) } }\n" +
		"output o { file = \"b.glb\" }\n"
	m, _ := buildBoxMesh(t, src)
	lo, hi := boundsOf(m)
	// Scale is applied before the turn: x is 1 +- 0.1, then x goes to -z.
	if !nearVec(lo, mesh.Vec3{-0.1, -0.2, 3.9}, 1e-6) || !nearVec(hi, mesh.Vec3{0.1, 0.2, 4.1}, 1e-6) {
		t.Errorf("bounds %v %v", lo, hi)
	}
}

func TestTurnedAndScaledNormalsStayPerpendicular(t *testing.T) {
	// A flat box under a nonuniform scale: the corner normals use the inverse
	// transpose, so each still equals the normal of its own face.
	m, _ := buildBoxMesh(t, boxAsset(`  part skew {
    rot = (0, 0, 30deg)
    scale = (1, 4, 1)
    box { size = (1, 1, 1) at = (2, 0, 0) rot = (20deg, 10deg, 0) }
  }`))
	if m.TriangleCount() != 12 {
		t.Fatalf("%d triangles", m.TriangleCount())
	}
	for i, tr := range m.Tris {
		a, b, c := m.TriPositions(i)
		n := mesh.Normalize(mesh.Cross(mesh.Sub(b, a), mesh.Sub(c, a)))
		for _, ci := range tr {
			if d := mesh.Dot(n, m.Corners[ci].Normal); d < 1-1e-5 {
				t.Fatalf("triangle %d: normal %v disagrees with its face %v (dot %g)", i, m.Corners[ci].Normal, n, d)
			}
		}
	}
}

func TestRotBytesAreRepeatable(t *testing.T) {
	src := boxAsset(`  part a { rot = (10deg, 20deg, 30deg) scale = (1, 2, 3) pivot = (1, 1, 1)
    box { size = (1, 1, 1) rot = (45deg, 45deg, 45deg) bevel { width = 0.1 segments = 2 } }
    cylinder { radius = 1 height = 2 segments = 7 rot = (0, 0, 30deg) }
  }`)
	_, a := buildBoxMesh(t, src)
	_, b := buildBoxMesh(t, src)
	if string(a.GLB) != string(b.GLB) {
		t.Error("two builds differ")
	}
}

func TestRotOnASocketStaysUnsupported(t *testing.T) {
	src := boxAsset("socket s { kind = k rot = (0, 30deg, 0) }")
	p, ds := Build(lowerSource(t, []byte(src)), "")
	if p != nil {
		t.Fatal("built")
	}
	found := false
	for _, d := range ds {
		found = found || strings.Contains(d.Message, "socket")
	}
	if !found {
		t.Errorf("no socket diagnostic in %v", ds)
	}
}
