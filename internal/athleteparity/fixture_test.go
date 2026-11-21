package athleteparity

import (
	"math"
	"testing"

	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/gltf"
)

// The synthetic athlete: a stack of square rings, so every span can be read
// off the numbers below by hand. Lengths are written in yards and stored in
// metres, as a compiled athlete is.
//
//	shape            rings (y yd: half width x, half depth z)                  joint
//	torso            1.00 and 1.15: .30 .20   1.33: .35 .22   1.60: .40 .25     pelvis
//	shoulder_pad     1.55 and 1.62: .50 .20                                     pelvis
//	helmet           1.70 and 1.90: .135 .15                                    head
//	hips             .93 and .97: .25 .15                                       pelvis
//	thigh_l (x>0)    .78 and .82: x .05 to .25, z -.10 to .10                   pelvis
//	thigh_r (x<0)    the mirror                                                 pelvis
//	sole             0 and .05: .10 .20                                         pelvis
//
// so the widths at the identity pose are: height 1.9, helmet .27, shoulder
// 1.0, ribs .70 (deep .44), waist .60 (deep .40), seat .50 (deep .30), thigh
// .20 (deep .20).
type fixture struct {
	doc   *gltf.Document
	parts []compiler.CensusPart
	model *Model
}

const yd = MetresPerYard

type ringSpec struct{ y, x0, x1, z0, z1 float64 }

func sq(y, hx, hz float64) ringSpec { return ringSpec{y, -hx, hx, -hz, hz} }

// shapeSpec is one shape of the fixture.
type shapeSpec struct {
	name  string
	rings []ringSpec
	joint uint16
}

var fixtureShapes = []shapeSpec{
	{"body/torso", []ringSpec{sq(1.00, .30, .20), sq(1.15, .30, .20), sq(1.33, .35, .22), sq(1.60, .40, .25)}, 0},
	{"body/shoulder_pad", []ringSpec{sq(1.55, .50, .20), sq(1.62, .50, .20)}, 0},
	{"body/helmet", []ringSpec{sq(1.70, .135, .15), sq(1.90, .135, .15)}, 1},
	{"body/hips", []ringSpec{sq(.93, .25, .15), sq(.97, .25, .15)}, 0},
	{"body/thigh_l", []ringSpec{{.78, .05, .25, -.10, .10}, {.82, .05, .25, -.10, .10}}, 0},
	{"body/thigh_r", []ringSpec{{.78, -.25, -.05, -.10, .10}, {.82, -.25, -.05, -.10, .10}}, 0},
	{"body/sole", []ringSpec{sq(0, .10, .20), sq(.05, .10, .20)}, 0},
}

// newFixture builds the synthetic athlete. withMass adds the morph target
// "mass": the two lower rings of the torso move .05 yd outward on each side.
func newFixture(t testing.TB, withMass bool, clipQuat *[4]float32) *fixture {
	t.Helper()
	var pr gltf.Primitive
	var parts []compiler.CensusPart
	var mass []float32
	for _, s := range fixtureShapes {
		v0, t0 := len(pr.Positions)/3, len(pr.Indices)/3
		for ri, r := range s.rings {
			for _, c := range [4][2]float64{{r.x0, r.z0}, {r.x1, r.z0}, {r.x1, r.z1}, {r.x0, r.z1}} {
				pr.Positions = append(pr.Positions, float32(c[0]*yd), float32(r.y*yd), float32(c[1]*yd))
				pr.Joints0 = append(pr.Joints0, s.joint, 0, 0, 0)
				pr.Weights0 = append(pr.Weights0, 1, 0, 0, 0)
				d := float32(0)
				if s.name == "body/torso" && ri <= 1 {
					d = float32(math.Copysign(.05*yd, c[0]))
				}
				mass = append(mass, d, 0, 0)
			}
			if ri == 0 {
				continue
			}
			a, b := uint32(v0+4*(ri-1)), uint32(v0+4*ri)
			for k := uint32(0); k < 4; k++ {
				k1 := (k + 1) % 4
				pr.Indices = append(pr.Indices, a+k, a+k1, b+k, a+k1, b+k1, b+k)
			}
		}
		parts = append(parts, compiler.CensusPart{Name: s.name, Triangles: len(pr.Indices)/3 - t0, Vertices: len(pr.Positions)/3 - v0, Material: "m"})
	}
	pr.Material = gltf.Int(0)
	mesh := gltf.Mesh{Name: "fixture", Primitives: []gltf.Primitive{pr}}
	if withMass {
		mesh.Primitives[0].Targets = []gltf.MorphTarget{{Positions: mass}}
		mesh.TargetNames = []string{"mass"}
		mesh.Weights = []float32{0}
	}
	doc := &gltf.Document{
		Materials: []gltf.Material{{Name: "m"}},
		Meshes:    []gltf.Mesh{mesh},
		Nodes: []gltf.Node{
			{Name: "body", Mesh: gltf.Int(0), Skin: gltf.Int(0)},
			{Name: "pelvis", Children: []int{2}},
			{Name: "head", Translation: &[3]float32{0, float32(1.7 * yd), 0}},
		},
		Skins: []gltf.Skin{{
			Joints:              []int{1, 2},
			InverseBindMatrices: [][16]float32{{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}, {1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, float32(-1.7 * yd), 0, 1}},
		}},
		Scenes: []gltf.Scene{{Nodes: []int{0, 1}}},
	}
	if clipQuat != nil {
		doc.Animations = []gltf.Animation{{
			Name:     "ready",
			Channels: []gltf.Channel{{Node: 1, Path: gltf.PathRotation, Sampler: 0}},
			Samplers: []gltf.AnimSampler{{Input: []float32{0, 1}, Output: append(append([]float32{}, clipQuat[:]...), 0, 0, 0, 1)}},
		}}
	}
	m, err := NewModel("fixture.glb", doc, parts, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{doc: doc, parts: parts, model: m}
}

func near(t testing.TB, what string, got, want, tol float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > tol {
		t.Errorf("%s = %.6f, want %.6f (tolerance %g)", what, got, want, tol)
	}
}

// look builds a look with the given morph weight, head scale and body scale.
func look(pos string, mass, head, body float64) Look {
	return Look{Pos: pos, ShapeWeights: map[string]float64{"mass": mass}, JointScales: map[string]float64{"head": head}, BodyScale: body}
}
