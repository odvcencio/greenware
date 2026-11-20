package compiler

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"m31labs.dev/greenware/gltf"
)

// Two mirrored arms: the reflected copy takes the pair names, reflected binds,
// reflected sockets and reflected ranges, and a symmetric pose moves the two
// sides as mirror images.
const mirrorSrc = `greenware 1

asset m { root = body length = m angle = deg idle = rest }

skeleton s {
  joint hips {
    at = (0, 1, 0)
    mirror {
      plane = x
      joint (arml, armr) {
        at = (0.3, 1, 0)
        orient = rot(0, 0, 10)
        swing_x = -20..100
        twist = -30..40
        swing_z = -10..35
        joint (handl, handr) {
          at = (0.7, 1, 0)
          socket (gl, gr) { kind = hand at = (0.75, 1.02, 0) }
        }
      }
    }
  }
}

part body {
  joint = hips
  mirror {
    plane = x
    lathe (upper_l, upper_r) {
      joint = arml
      segments = 8
      point (0, 0.3)
      point (0.05, 0.3)
      point (0.05, 0.5)
      point (0, 0.5)
      at = (0, 0, 0)
      axis = x
    }
    ellipsoid (fist_l, fist_r) { joint = handl centre = (0.75, 1, 0) radii = (0.05, 0.05, 0.05) segments = 8 }
  }
}

skin {
  max_influences = 2
  mirror {
    plane = x
    blend (b_l, b_r) { parent = arml child = handl centre = joint(handl) + (-0.1, 0, 0) axis = (1, 0, 0) width = 0.1 }
  }
}

clip rest {
  duration = 1s
  keys = 3
  pose {
    mirror {
      plane = x
      arml = rot(30, 15, 20)
      handl = rot(0, 0, 25)
    }
  }
}

output m { file = "m.glb" }
`

func TestMirroredRigIsMirrored(t *testing.T) {
	p, d, ds := buildFixture(t, mirrorSrc)
	_ = p
	_ = ds
	var order []string
	for _, j := range d.Skins[0].Joints {
		order = append(order, d.Nodes[j].Name)
	}
	if got := strings.Join(order, " "); got != "hips arml handl armr handr" {
		t.Fatalf("joint order %q", got)
	}
	node := func(n string) gltf.Node { return d.Nodes[nodeNamed(t, d, n)] }
	// Local rests: the copy is the mirror image of the original in model space.
	ev := rigEval{d}
	T, Q := ev.locals("", 0)
	// Use the idle pose's own world transforms at rest: rebuild rest worlds from inverse binds.
	_ = T
	_ = Q
	sk := d.Skins[0]
	world := func(j int) m4 {
		// The inverse of the inverse bind is the rest world.
		var m m4
		for k, v := range sk.InverseBindMatrices[j] {
			m[k] = float64(v)
		}
		return invRigid(m)
	}
	l, r := world(1), world(3)
	if math.Abs(l[12]+r[12]) > 1e-6 || math.Abs(l[13]-r[13]) > 1e-6 || math.Abs(l[14]-r[14]) > 1e-6 {
		t.Errorf("arm binds %v %v are not mirror images", l[12:15], r[12:15])
	}
	// The bind orientation of the copy is the reflected rotation: the 10 degree turn about z reverses.
	rl := [3]float64{l[0], l[1], l[2]} // the image of +x
	rr := [3]float64{r[0], r[1], r[2]}
	if math.Abs(rl[0]-rr[0]) > 1e-6 || math.Abs(rl[1]+rr[1]) > 1e-6 {
		t.Errorf("bind x axes %v %v: a mirrored frame keeps x's x and reverses its y", rl, rr)
	}
	if node("socket.gr").Name == "" || node("socket.gl").Name == "" {
		t.Error("sockets")
	}
	// The sockets are mirror images in the idle pose: positions x reversed.
	w := ev.worlds(ev.locals("rest", 0))
	gl, gr := w[nodeNamed(t, d, "socket.gl")].point([3]float64{}), w[nodeNamed(t, d, "socket.gr")].point([3]float64{})
	if math.Abs(gl[0]+gr[0]) > 1e-5 || math.Abs(gl[1]-gr[1]) > 1e-5 || math.Abs(gl[2]-gr[2]) > 1e-5 {
		t.Errorf("sockets at %v and %v in the idle pose", gl, gr)
	}
	// Skinned vertices: for every left-side vertex there is a right-side vertex at its mirror position, and the posed positions mirror too.
	bind := bindPositions(d)
	posed := ev.posed("rest", 0.5, nil)
	paired, worst, moved := 0, 0.0, 0.0
	for i := range bind {
		if bind[i][0] < 0.2 {
			continue
		}
		moved = math.Max(moved, dist3(bind[i], posed[i]))
		for j := range bind {
			if dist3([3]float64{-bind[i][0], bind[i][1], bind[i][2]}, bind[j]) < 1e-6 {
				paired++
				m := [3]float64{-posed[i][0], posed[i][1], posed[i][2]}
				worst = math.Max(worst, dist3(m, posed[j]))
				break
			}
		}
	}
	if paired < 20 {
		t.Fatalf("only %d vertices have a mirror partner", paired)
	}
	if moved < 0.05 {
		t.Errorf("the pose moves nothing (%g m): the test would pass for any mirror", moved)
	}
	if worst > 5e-5 {
		t.Errorf("posed mirror vertices differ by %g", worst)
	}
	t.Logf("%d mirror pairs agree to %.3g m", paired, worst)
}

// invRigid inverts a rotation-and-translation matrix.
func invRigid(m m4) m4 {
	var o m4
	// transpose the rotation
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			o[c*4+r] = m[r*4+c]
		}
	}
	o[15] = 1
	for r := 0; r < 3; r++ {
		o[12+r] = -(o[r]*m[12] + o[4+r]*m[13] + o[8+r]*m[14])
	}
	return o
}

// A collider names the joint it follows; the copy of a mirror takes the pair.
func TestColliderNamesItsJoint(t *testing.T) {
	src := strings.Replace(mirrorSrc, "output m {", `collision {
  mirror {
    plane = x
    capsule (hit_l, hit_r) { joint = arml from = (0.3, 1, 0) to = (0.7, 1, 0) radius = 0.05 }
  }
}

output m {`, 1)
	_, d, _ := buildFixture(t, src)
	for _, want := range []struct{ node, joint string }{{"collider.hit_l", "arml"}, {"collider.hit_r", "armr"}} {
		n := d.Nodes[nodeNamed(t, d, want.node)]
		var ex struct {
			Greenware struct{ Joint string }
		}
		if err := json.Unmarshal(n.Extras, &ex); err != nil {
			t.Fatal(err)
		}
		if ex.Greenware.Joint != want.joint {
			t.Errorf("%s follows %q, want %q", want.node, ex.Greenware.Joint, want.joint)
		}
	}
	// An unknown joint is a front-end error.
	bad := strings.Replace(src, "joint = arml from", "joint = armm from", 1)
	if p, ds := Build(lowerSource(t, []byte(bad)), ""); p != nil || len(ds) == 0 {
		t.Errorf("a collider on an unknown joint built: %v", ds)
	}
}
