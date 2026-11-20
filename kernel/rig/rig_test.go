package rig

import (
	"math"
	"testing"

	"m31labs.dev/greenware/kernel/mesh"
)

func near(a, b Vec, tol float64) bool {
	for i := range a {
		if math.Abs(a[i]-b[i]) > tol {
			return false
		}
	}
	return true
}

func nearf(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func rad(deg float64) float64 { return float64(deg*math.Pi) / 180 }

func mustSkeleton(t *testing.T, js ...Joint) *Skeleton {
	t.Helper()
	s, err := NewSkeleton(js)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestQuatRotate(t *testing.T) {
	// rot(0, 0, 90deg) turns +x into +y.
	q := QEuler(0, 0, rad(90))
	if got := q.Rotate(Vec{1, 0, 0}); !near(got, Vec{0, 1, 0}, 1e-12) {
		t.Fatalf("rot z 90: +x -> %v", got)
	}
	// The product is q_x q_y q_z, so z acts first: x 90 then... check with z 90, x 90.
	q = QEuler(rad(90), 0, rad(90))
	// z first: +x -> +y; then x 90: +y -> +z.
	if got := q.Rotate(Vec{1, 0, 0}); !near(got, Vec{0, 0, 1}, 1e-12) {
		t.Fatalf("rot x90 z90: +x -> %v", got)
	}
	// Mat3 agrees with Rotate.
	for _, v := range []Vec{{1, 2, 3}, {-1, 0.5, 2}} {
		if a, b := q.Mat3().MulVec(v), q.Rotate(v); !near(a, b, 1e-12) {
			t.Fatalf("Mat3 %v != Rotate %v", a, b)
		}
	}
}

func TestReflectQuat(t *testing.T) {
	// A rotation about x seen in the x mirror keeps its sense about x; about y or z it reverses.
	m := mesh.MirrorAffine(mesh.PlaneX).M
	q := QEuler(rad(30), rad(20), rad(10))
	r := q.Reflect(mesh.PlaneX)
	for _, v := range []Vec{{1, 0, 0}, {0.3, -2, 1}} {
		want := m.MulVec(q.Rotate(m.MulVec(v)))
		if got := r.Rotate(v); !near(got, want, 1e-12) {
			t.Fatalf("reflect: got %v want %v", got, want)
		}
	}
}

func TestSlerpShortestArc(t *testing.T) {
	a := QEuler(0, 0, 0)
	b := QEuler(0, 0, rad(90)).Neg() // the same rotation, far sign
	h := Slerp(a, b, 0.5)
	want := QEuler(0, 0, rad(45))
	if h.Dot(want) < 1-1e-12 {
		t.Fatalf("slerp midpoint %v, want %v", h, want)
	}
	if Slerp(a, b, 0) != a {
		t.Fatal("t=0 must be exactly a")
	}
}

func chain(t *testing.T, orient bool) *Skeleton {
	var r Quat = QIdentity
	if orient {
		r = QEuler(rad(20), rad(-35), rad(50))
	}
	return mustSkeleton(t,
		Joint{Name: "a", Parent: -1, Pos: Vec{0, 1, 0}, Rot: r, TwistAxis: -1},
		Joint{Name: "b", Parent: 0, Pos: Vec{0, 1.5, 0.1}, Rot: QEuler(rad(10), 0, rad(-70)), TwistAxis: -1},
		Joint{Name: "c", Parent: 1, Pos: Vec{0.2, 1.9, 0.1}, Rot: r.Mul(QEuler(0, rad(33), 0)), TwistAxis: -1},
	)
}

func TestRestComposesToBind(t *testing.T) {
	for _, orient := range []bool{false, true} {
		s := chain(t, orient)
		w := s.World(s.Rest())
		for i, j := range s.Joints {
			if !near(w[i].T, j.Pos, 1e-12) {
				t.Errorf("orient=%v joint %d: world T %v, bind %v", orient, i, w[i].T, j.Pos)
			}
			if w[i].R.Dot(j.Rot) < 1-1e-12 {
				t.Errorf("orient=%v joint %d: world R %v, bind %v", orient, i, w[i].R, j.Rot)
			}
		}
	}
}

// A child's local rest is B_p^-1 B_j: with an oriented parent the local
// translation is the parent-frame view of the model-space offset.
func TestLocalRestOfOrientedParent(t *testing.T) {
	// Parent turned 90 degrees about z: a child 1 m along model +x sits 1 m along parent -y.
	s := mustSkeleton(t,
		Joint{Name: "p", Parent: -1, Pos: Vec{0, 0, 0}, Rot: QEuler(0, 0, rad(90)), TwistAxis: -1},
		Joint{Name: "c", Parent: 0, Pos: Vec{1, 0, 0}, Rot: QEuler(0, 0, rad(90)), TwistAxis: -1},
	)
	rest := s.Rest()
	if !near(rest[1].T, Vec{0, -1, 0}, 1e-12) {
		t.Fatalf("local translation %v, want (0,-1,0)", rest[1].T)
	}
	if rest[1].R.Dot(QIdentity) < 1-1e-12 {
		t.Fatalf("local rotation %v, want identity", rest[1].R)
	}
}

func TestInverseBindIsExactInverseNotNegatedTranslation(t *testing.T) {
	s := chain(t, true)
	ibm := InverseBinds(s.World(s.Rest()))
	for i, j := range s.Joints {
		// I_j maps the joint's own bind position to the origin.
		if got := ibm[i].Apply(j.Pos); !near(got, Vec{}, 1e-12) {
			t.Errorf("joint %d: I*pos = %v", i, got)
		}
		// A rotated inverse bind is not a negated translation.
		if j.Rot.Dot(QIdentity) < 1-1e-9 && near(ibm[i].T, mesh.Neg(j.Pos), 1e-6) {
			t.Errorf("joint %d: inverse bind translation equals -pos for a rotated bind", i)
		}
	}
}

func TestSkinAtRestIsIdentity(t *testing.T) {
	for _, orient := range []bool{false, true} {
		s := chain(t, orient)
		rest := s.Rest()
		skin := SkinMatrices(s.World(rest), InverseBinds(s.World(rest)))
		pos := []Vec{{0, 1, 0}, {0.3, 1.7, -0.2}, {-1, 0, 2}}
		infl := [][]Influence{{{0, 1}}, {{1, 0.25}, {2, 0.75}}, {{0, 0.5}, {1, 0.25}, {2, 0.25}}}
		got := Skin(pos, infl, skin)
		for i := range pos {
			if !near(got[i], pos[i], 1e-12) {
				t.Errorf("orient=%v pos %d: %v != %v", orient, i, got[i], pos[i])
			}
		}
	}
}

// Hand computation: an elbow at (1,0,0) turned 90 degrees about +z carries a
// vertex at (2,0,0) to (1,1,0); a vertex half on the elbow and half on the
// shoulder lands at the mean of that and its unmoved place.
func TestSkinHandComputed(t *testing.T) {
	s := mustSkeleton(t,
		Joint{Name: "shoulder", Parent: -1, Pos: Vec{0, 0, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "elbow", Parent: 0, Pos: Vec{1, 0, 0}, Rot: QIdentity, TwistAxis: -1},
	)
	in := &Instance{Sk: s, Rest: s.Rest()}
	loc := in.Locals(nil, 0)
	loc[1].R = QEuler(0, 0, rad(90))
	skin := SkinMatrices(s.World(loc), in.InverseBinds())
	pos := []Vec{{2, 0, 0}, {2, 0, 0}}
	infl := [][]Influence{{{1, 1}}, {{0, 0.5}, {1, 0.5}}}
	got := Skin(pos, infl, skin)
	if !near(got[0], Vec{1, 1, 0}, 1e-12) {
		t.Errorf("rigid: %v, want (1,1,0)", got[0])
	}
	if !near(got[1], Vec{1.5, 0.5, 0}, 1e-12) {
		t.Errorf("blended: %v, want (1.5,0.5,0)", got[1])
	}
}

func TestWorldIgnoresJointOrder(t *testing.T) {
	// Children listed before their parent.
	s := mustSkeleton(t,
		Joint{Name: "tip", Parent: 1, Pos: Vec{0, 2, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "mid", Parent: 2, Pos: Vec{0, 1, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "root", Parent: -1, Pos: Vec{0, 0, 0}, Rot: QIdentity, TwistAxis: -1},
	)
	w := s.World(s.Rest())
	if !near(w[0].T, Vec{0, 2, 0}, 1e-12) || !near(w[1].T, Vec{0, 1, 0}, 1e-12) {
		t.Fatalf("world %v %v", w[0].T, w[1].T)
	}
}

func TestSkeletonRejects(t *testing.T) {
	j := func(n string, p int) Joint { return Joint{Name: n, Parent: p, Rot: QIdentity, TwistAxis: -1} }
	cases := map[string][]Joint{
		"two roots":  {j("a", -1), j("b", -1)},
		"no root":    {j("a", 1), j("b", 0)},
		"duplicate":  {j("a", -1), j("a", 0)},
		"bad parent": {j("a", -1), j("b", 7)},
		"empty":      {},
	}
	for name, js := range cases {
		if _, err := NewSkeleton(js); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The worked fixture of the language reference, section 5.6 (metres).
func TestInstanceRestOffsetsFixture(t *testing.T) {
	s := mustSkeleton(t,
		Joint{Name: "root", Parent: -1, Pos: Vec{0, 1, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "tip", Parent: 0, Pos: Vec{0, 2, 0}, Rot: QIdentity, TwistAxis: -1},
	)
	rest := s.Rest()
	bindVertex := Vec{0, 2.5, 0}
	capLocal := Vec{0, 0.5, 0} // socket `cap` on tip
	long := Offset{Joint: 1, Morph: 0, Delta: Vec{0, 0.2, 0}}
	tall := Offset{Joint: 0, Morph: 1, Delta: Vec{0, 0.1, 0}}

	type row struct {
		name                  string
		w                     []float64
		clip                  *Clip
		scale                 float64
		tip, vertex, capWorld Vec
	}
	rotZ := QEuler(0, 0, rad(90))
	rows := []row{
		{"rest long=0", []float64{0, 0}, nil, 0, Vec{0, 2, 0}, Vec{0, 2.5, 0}, Vec{0, 2.5, 0}},
		{"rest long=1", []float64{1, 0}, nil, 0, Vec{0, 2.2, 0}, Vec{0, 2.7, 0}, Vec{0, 2.7, 0}},
		{"root rotated 90 about z", []float64{1, 0},
			&Clip{Duration: 1, Channels: []Channel{{Target: 0, Path: Rotation, Times: []float64{0, 1}, Values: append(rotZ[:], rotZ[:]...)}}},
			0, Vec{-1.2, 1, 0}, Vec{-1.7, 1, 0}, Vec{-1.7, 1, 0}},
		{"root translation key, scale 1.05", []float64{1, 0},
			&Clip{Duration: 1, Channels: []Channel{{Target: 0, Path: Translation, Times: []float64{0, 1}, Values: []float64{0, 0.9, 0, 0, 0.9, 0}}}},
			1.05, Vec{0, 2.145, 0}, Vec{0, 2.645, 0}, Vec{0, 2.645, 0}},
		{"same key, scale 1.1, tall offsets the root", []float64{1, 1},
			&Clip{Duration: 1, Channels: []Channel{{Target: 0, Path: Translation, Times: []float64{0, 1}, Values: []float64{0, 0.9, 0, 0, 0.9, 0}}}},
			1.1, Vec{0, 2.19, 0}, Vec{0, 2.69, 0}, Vec{0, 2.69, 0}},
	}
	for _, r := range rows {
		in := &Instance{Sk: s, Rest: rest, Offsets: []Offset{long, tall}, Weights: r.w, HasRootScale: r.scale != 0, RootScale: r.scale, Up: 1}
		loc := in.Locals(r.clip, 0.5)
		world := s.World(loc)
		skin := SkinMatrices(world, in.InverseBinds())
		// The morph moved the vertex to its new bind position.
		// `long` moves the vertex by its delta; `tall` moves it with its root offset.
		morphed := mesh.Add(mesh.Add(bindVertex, mesh.Scale(long.Delta, r.w[0])), mesh.Scale(tall.Delta, r.w[1]))
		got := Skin([]Vec{morphed}, [][]Influence{{{1, 1}}}, skin)[0]
		capW := world[1].Apply(capLocal)
		if !near(world[1].T, r.tip, 1e-9) {
			t.Errorf("%s: tip %v, want %v", r.name, world[1].T, r.tip)
		}
		if !near(got, r.vertex, 1e-9) {
			t.Errorf("%s: vertex %v, want %v", r.name, got, r.vertex)
		}
		if !near(capW, r.capWorld, 1e-9) {
			t.Errorf("%s: cap %v, want %v", r.name, capW, r.capWorld)
		}
	}
}

func TestFollowRecordsDifferenceOfLocals(t *testing.T) {
	// A stretch that lifts everything above y=1 by 0.2 moves the tip and the
	// hand, but the hand's local offset is zero: a shared shift is recorded once.
	s := mustSkeleton(t,
		Joint{Name: "root", Parent: -1, Pos: Vec{0, 0, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "tip", Parent: 0, Pos: Vec{0, 2, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "hand", Parent: 1, Pos: Vec{0, 3, 0}, Rot: QIdentity, TwistAxis: -1},
	)
	move := func(p Vec) Vec {
		p[1] += StretchShift(p[1], 1, 2, 0.2)
		return p
	}
	offs := Follow(s, move, 4)
	if len(offs) != 1 || offs[0].Joint != 1 || offs[0].Morph != 4 || !near(offs[0].Delta, Vec{0, 0.2, 0}, 1e-12) {
		t.Fatalf("offsets %+v", offs)
	}
}

func TestStretchNeck(t *testing.T) {
	// A 60 % stretch of a 0.08 m neck lifts what lies above by 0.048 m.
	pos := []Vec{{0, 1.0, 0}, {0, 1.04, 0}, {0, 1.2, 0}}
	if err := Stretch(pos, 1, 1.0, 1.08, 0.6); err != nil {
		t.Fatal(err)
	}
	want := []float64{1.0, 1.04 + 0.024, 1.2 + 0.048}
	for i, w := range want {
		if !nearf(pos[i][1], w, 1e-12) {
			t.Errorf("pos %d: y %v want %v", i, pos[i][1], w)
		}
	}
	if err := Stretch(pos, 1, 2, 1, 0.5); err == nil {
		t.Error("from > to accepted")
	}
}

func TestReshapeOffsetInflate(t *testing.T) {
	pos := []Vec{{2, 0, 0}, {5, 5, 5}}
	shape := []int{0, 1}
	Reshape(pos, shape, []int{0}, Vec{1, 0, 0}, Vec{3, 1, 1})
	if !near(pos[0], Vec{4, 0, 0}, 1e-12) || pos[1] != (Vec{5, 5, 5}) {
		t.Fatalf("reshape: %v", pos)
	}
	Translate(pos, shape, []int{1}, Vec{0, -1, 0})
	if pos[1] != (Vec{5, 4, 5}) {
		t.Fatalf("translate: %v", pos)
	}
	// Inflate a vertex 0.5 from a vertical line by 20 %.
	p := []Vec{{0.5, 3, 0}}
	Inflate(p, []int{0}, []int{0}, [][]Vec{{{0, 0, 0}, {0, 10, 0}}}, 0.2)
	if !near(p[0], Vec{0.6, 3, 0}, 1e-12) {
		t.Fatalf("inflate: %v", p[0])
	}
}

func TestBlendProfile(t *testing.T) {
	// parent 0, child 1, centre at the origin, axis +y, half-width 2.
	mk := func(y float64) *Bound {
		b := &Bound{}
		b.AddShape(0, []Vec{{0, y, 0}}, 0)
		return b
	}
	bl := Blend{Parent: 0, Child: 1, Axis: Vec{0, 1, 0}, Width: 2}
	cases := []struct {
		y, child float64
	}{
		{-2, 0}, {-3, 0}, {0, 0.5}, {2, 1}, {5, 1},
		{1, 0.84375},  // t = 0.75: h = 0.5625*(3-1.5) = 0.84375
		{-1, 0.15625}, // t = 0.25
	}
	for _, c := range cases {
		b := mk(c.y)
		if err := b.Blend(bl); err != nil {
			t.Fatal(err)
		}
		if got := weightOf(b.Infl[0], 1); !nearf(got, c.child, 1e-12) {
			t.Errorf("y=%v child weight %v, want %v", c.y, got, c.child)
		}
		if sum := weightOf(b.Infl[0], 0) + weightOf(b.Infl[0], 1); !nearf(sum, 1, 1e-12) {
			t.Errorf("y=%v weights sum %v", c.y, sum)
		}
	}
	// bias shifts the fraction: bias 0.25 at the centre is t=0.75.
	b := mk(0)
	bl.Bias = 0.25
	_ = b.Blend(bl)
	if got := weightOf(b.Infl[0], 1); !nearf(got, 0.84375, 1e-12) {
		t.Errorf("bias: %v", got)
	}
}

func TestBlendLeavesOthersAndHonoursOnly(t *testing.T) {
	b := &Bound{}
	b.AddShape(0, []Vec{{0, 5, 0}}, 2) // bound to an unrelated joint
	b.AddShape(1, []Vec{{0, 5, 0}}, 0) // parent, shape 1
	b.AddShape(2, []Vec{{0, 5, 0}}, 0) // parent, shape 2
	bl := Blend{Parent: 0, Child: 1, Axis: Vec{0, 1, 0}, Width: 1, Only: []int{1}}
	if err := b.Blend(bl); err != nil {
		t.Fatal(err)
	}
	if got := weightOf(b.Infl[0], 2); got != 1 || len(b.Infl[0]) != 1 {
		t.Errorf("an unrelated joint's vertex was touched: %v", b.Infl[0])
	}
	if got := weightOf(b.Infl[1], 1); got != 1 {
		t.Errorf("shape 1 not blended: %v", b.Infl[1])
	}
	if got := weightOf(b.Infl[2], 1); got != 0 {
		t.Errorf("shape 2 should be outside `only`: %v", b.Infl[2])
	}
	if err := b.Blend(Blend{Parent: 0, Child: 1, Axis: Vec{}, Width: 1}); err == nil {
		t.Error("zero axis accepted")
	}
	if err := b.Blend(Blend{Parent: 0, Child: 1, Axis: Vec{0, 1, 0}, Width: 0}); err == nil {
		t.Error("zero width accepted")
	}
}

func TestBlendFade(t *testing.T) {
	// fade_x = 0..1 on the authored side: at x=0.5 the fade is smoothstep(0.5) = 0.5.
	b := &Bound{}
	b.AddShape(0, []Vec{{0.5, 5, 0}, {0, 5, 0}, {-0.5, 5, 0}}, 0)
	bl := Blend{Parent: 0, Child: 1, Axis: Vec{0, 1, 0}, Width: 1, Fade: FadeX{Set: true, Lo: 0, Hi: 1, Sign: 1}}
	_ = b.Blend(bl)
	if got := weightOf(b.Infl[0], 1); !nearf(got, 0.5, 1e-12) { // old 0, target 1, fade 0.5
		t.Errorf("x=0.5: %v", got)
	}
	if got := weightOf(b.Infl[1], 1); got != 0 {
		t.Errorf("x=0 (fade 0): %v", got)
	}
	if got := weightOf(b.Infl[2], 1); got != 0 {
		t.Errorf("x=-0.5 on the authored side: %v", got)
	}
	// The reflected band (Sign -1) ramps in on the negative side.
	b2 := &Bound{}
	b2.AddShape(0, []Vec{{-0.5, 5, 0}}, 0)
	bl.Fade.Sign = -1
	_ = b2.Blend(bl)
	if got := weightOf(b2.Infl[0], 1); !nearf(got, 0.5, 1e-12) {
		t.Errorf("reflected: %v", got)
	}
}

func TestLengthen(t *testing.T) {
	// Bone from (0,0,0) up to (0,1,0); lengthen to 1.5 with anchor 0.5.
	mk := func() (*Skeleton, *Bound) {
		s := mustSkeleton(t,
			Joint{Name: "j", Parent: -1, Pos: Vec{0, 0, 0}, Rot: QIdentity, TwistAxis: -1},
			Joint{Name: "c", Parent: 0, Pos: Vec{0, 1, 0}, Rot: QIdentity, TwistAxis: -1},
			Joint{Name: "d", Parent: 1, Pos: Vec{0, 2, 0}, Rot: QIdentity, TwistAxis: -1},
		)
		b := &Bound{}
		b.AddShape(0, []Vec{{0.1, -0.2, 0}, {0.1, 0.25, 0}, {0.1, 0.5, 0}, {0.1, 0.75, 0}, {0.1, 1, 0}, {0.1, 1.2, 0}}, 0)
		b.AddShape(1, []Vec{{0, 1.5, 0}}, 1)                  // bound to the child: moves with it
		b.AddShape(2, []Vec{{0, 3, 0}}, 2)                    // bound to the grandchild
		b.AddShape(3, []Vec{{0.3, 0.3, 0}, {0.3, 0.7, 0}}, 0) // rigid: midpoint of 0.3..0.7 = 0.5
		return s, b
	}
	s, b := mk()
	if err := b.Lengthen(s, 0, 1.5, 0.5, []int{3}); err != nil {
		t.Fatal(err)
	}
	// [0.5, 1] -> [0.5, 1.5]: s=0.75 -> 1.0; s=1 -> 1.5; s=1.2 -> 1.2+0.5.
	wantY := []float64{-0.2, 0.25, 0.5, 1.0, 1.5, 1.7}
	for i, w := range wantY {
		if !nearf(b.Pos[i][1], w, 1e-12) {
			t.Errorf("pos %d: y %v want %v", i, b.Pos[i][1], w)
		}
	}
	if !nearf(b.Pos[6][1], 2.0, 1e-12) || !nearf(b.Pos[7][1], 3.5, 1e-12) {
		t.Errorf("child-bound geometry: %v %v", b.Pos[6], b.Pos[7])
	}
	// Rigid shape: the displacement at s = 0.5 is 0, so it does not move at all.
	if b.Pos[8][1] != 0.3 || b.Pos[9][1] != 0.7 {
		t.Errorf("rigid shape moved or stretched: %v %v", b.Pos[8], b.Pos[9])
	}
	if !near(s.Joints[1].Pos, Vec{0, 1.5, 0}, 1e-12) || !near(s.Joints[2].Pos, Vec{0, 2.5, 0}, 1e-12) {
		t.Errorf("joints: %v %v", s.Joints[1].Pos, s.Joints[2].Pos)
	}
	// Rigid with a nonzero midpoint displacement: no stretching, one shift.
	s, b = mk()
	b.Pos[8] = Vec{0.3, 0.8, 0}
	b.Pos[9] = Vec{0.3, 1.0, 0}
	_ = b.Lengthen(s, 0, 1.5, 0.5, []int{3})
	// midpoint s=0.9 -> displacement 0.4 (image 0.5+0.4*(1/0.5)... 0.5+(0.4)*1=1.3: 1.3-0.9=0.4).
	if !nearf(b.Pos[8][1], 1.2, 1e-12) || !nearf(b.Pos[9][1], 1.4, 1e-12) {
		t.Errorf("rigid shift: %v %v", b.Pos[8], b.Pos[9])
	}
}

func TestLengthenRejects(t *testing.T) {
	s := mustSkeleton(t,
		Joint{Name: "j", Parent: -1, Pos: Vec{0, 0, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "c1", Parent: 0, Pos: Vec{0, 1, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "c2", Parent: 0, Pos: Vec{1, 0, 0}, Rot: QIdentity, TwistAxis: -1},
	)
	b := &Bound{}
	if err := b.Lengthen(s, 0, 1, 0, nil); err == nil {
		t.Error("two children accepted")
	}
	s2 := mustSkeleton(t,
		Joint{Name: "j", Parent: -1, Pos: Vec{0, 0, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "c", Parent: 0, Pos: Vec{0, 1, 0}, Rot: QIdentity, TwistAxis: -1},
	)
	for _, c := range []struct{ to, anchor float64 }{{1, 1}, {0, 0}, {1, -0.1}, {0.4, 0.5}} {
		if err := b.Lengthen(s2.Clone(), 0, c.to, c.anchor, nil); err == nil {
			t.Errorf("to=%v anchor=%v accepted", c.to, c.anchor)
		}
	}
	s3 := mustSkeleton(t,
		Joint{Name: "j", Parent: -1, Pos: Vec{0, 0, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "c", Parent: 0, Pos: Vec{0, 0, 0}, Rot: QIdentity, TwistAxis: -1},
	)
	if err := b.Lengthen(s3, 0, 1, 0, nil); err == nil {
		t.Error("zero-length bone accepted")
	}
}

func TestFinishSortsAndChecks(t *testing.T) {
	b := &Bound{Pos: []Vec{{}}, Shape: []int{0}, Infl: [][]Influence{{{3, 0.25}, {1, 0.25}, {2, 0}, {3, 0.25}, {0, 0.25}}}}
	if err := b.Finish(4); err != nil {
		t.Fatal(err)
	}
	// duplicates combined: joint 3 = 0.5; then ties 1 and 0 by joint index.
	want := []Influence{{3, 0.5}, {0, 0.25}, {1, 0.25}}
	if len(b.Infl[0]) != 3 {
		t.Fatalf("got %v", b.Infl[0])
	}
	for i, w := range want {
		if b.Infl[0][i] != w {
			t.Errorf("influence %d: %v want %v", i, b.Infl[0][i], w)
		}
	}
	bad := &Bound{Pos: []Vec{{}}, Shape: []int{0}, Infl: [][]Influence{{{0, 0.4}, {1, 0.4}}}}
	if bad.Finish(4) == nil {
		t.Error("sum 0.8 accepted")
	}
	many := &Bound{Pos: []Vec{{}}, Shape: []int{0}, Infl: [][]Influence{{{0, 0.2}, {1, 0.2}, {2, 0.2}, {3, 0.2}, {4, 0.2}}}}
	if many.Finish(4) == nil {
		t.Error("five influences accepted under max 4")
	}
	neg := &Bound{Pos: []Vec{{}}, Shape: []int{0}, Infl: [][]Influence{{{0, 1.5}, {1, -0.5}}}}
	if neg.Finish(4) == nil {
		t.Error("negative weight accepted")
	}
}

func TestLimitKeepsLargestAndRenormalises(t *testing.T) {
	in := SortInfluences([]Influence{{0, 0.1}, {1, 0.3}, {2, 0.3}, {3, 0.2}, {4, 0.1}})
	got := Limit(in, 4)
	if len(got) != 4 {
		t.Fatal(got)
	}
	// Largest first, ties by joint index (1 before 2); the smallest tie (joint 0) is dropped.
	if got[0].Joint != 1 || got[1].Joint != 2 || got[2].Joint != 3 || got[3].Joint != 0 {
		t.Errorf("order %v", got)
	}
	var sum float64
	for _, x := range got {
		sum += x.W
	}
	if !nearf(sum, 1, 1e-12) || !nearf(got[0].W, 0.3/0.9, 1e-12) {
		t.Errorf("renormalised %v sum %v", got, sum)
	}
}

func TestConditionEndpointsExample(t *testing.T) {
	// Keys (1s, A), (2s, B) with duration 4 s deliver [0 1 2 4] s and [A A B B].
	c := Channel{Path: Translation, Times: []float64{1, 2}, Values: []float64{1, 0, 0, 2, 0, 0}}
	d, err := Condition(c, 4)
	if err != nil {
		t.Fatal(err)
	}
	wantT := []float64{0, 1, 2, 4}
	wantV := []float64{1, 0, 0, 1, 0, 0, 2, 0, 0, 2, 0, 0}
	for i := range wantT {
		if d.Times[i] != wantT[i] {
			t.Fatalf("times %v", d.Times)
		}
	}
	for i := range wantV {
		if d.Values[i] != wantV[i] {
			t.Fatalf("values %v", d.Values)
		}
	}
	// Without a duration larger than the last key: [0 1 2] only when the last key is the duration.
	d, _ = Condition(c, 2)
	if len(d.Times) != 3 || d.Times[0] != 0 || d.Times[2] != 2 {
		t.Fatalf("times %v", d.Times)
	}
	// An endpoint already present is not duplicated.
	e, _ := Condition(Channel{Path: Translation, Times: []float64{0, 2}, Values: make([]float64, 6)}, 2)
	if len(e.Times) != 2 {
		t.Fatalf("times %v", e.Times)
	}
}

func TestConditionRejects(t *testing.T) {
	mk := func(ts ...float64) Channel {
		return Channel{Path: Translation, Times: ts, Values: make([]float64, 3*len(ts))}
	}
	for name, c := range map[string]Channel{
		"reversed":  mk(2, 1),
		"repeated":  mk(1, 1),
		"negative":  mk(-1, 1),
		"late":      mk(1, 5),
		"nan":       mk(math.NaN(), 1),
		"empty":     mk(),
		"short val": {Path: Translation, Times: []float64{0, 1}, Values: make([]float64, 3)},
	} {
		if _, err := Condition(c, 2); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Condition(mk(0, 1), 0); err == nil {
		t.Error("zero duration accepted")
	}
	// Times that float32 rounding makes equal are caught by TimesAre32.
	if i := TimesAre32([]float64{1, 1 + 1e-12}); i != 1 {
		t.Errorf("TimesAre32 = %d", i)
	}
	if i := TimesAre32([]float64{0, 1, 2}); i != -1 {
		t.Errorf("TimesAre32 = %d", i)
	}
}

func TestQuaternionContinuity(t *testing.T) {
	a := QEuler(0, 0, 0)
	b := QEuler(0, 0, rad(10)).Neg() // far sign
	c := Channel{Path: Rotation, Times: []float64{0, 1}, Values: append(a[:], b[:]...)}
	d, err := Condition(c, 1)
	if err != nil {
		t.Fatal(err)
	}
	q0 := Quat{d.Values[0], d.Values[1], d.Values[2], d.Values[3]}
	q1 := Quat{d.Values[4], d.Values[5], d.Values[6], d.Values[7]}
	if q0.Dot(q1) < 0 {
		t.Fatalf("keys not sign aligned: %v %v", q0, q1)
	}
	// Not normalised input is normalised.
	e, _ := Condition(Channel{Path: Rotation, Times: []float64{0}, Values: []float64{0, 0, 0, 2}}, 1)
	if e.Values[3] != 1 {
		t.Fatalf("not normalised: %v", e.Values)
	}
}

func TestSampling(t *testing.T) {
	lin := Channel{Path: Translation, Times: []float64{0, 2}, Values: []float64{0, 0, 0, 2, 4, 6}}
	if got := lin.At(0.5); !near(Vec{got[0], got[1], got[2]}, Vec{0.5, 1, 1.5}, 1e-12) {
		t.Errorf("linear %v", got)
	}
	if got := lin.At(-1); got[0] != 0 {
		t.Errorf("before first key: %v", got)
	}
	if got := lin.At(9); got[2] != 6 {
		t.Errorf("after last key: %v", got)
	}
	st := lin
	st.Interp = Step
	if got := st.At(1.99); got[0] != 0 {
		t.Errorf("step holds the preceding value: %v", got)
	}
	if got := st.At(2); got[0] != 2 {
		t.Errorf("step at the last key: %v", got)
	}
	// Rotation: halfway between 0 and 90 degrees about z is 45 degrees.
	q := QEuler(0, 0, rad(90))
	rc := Channel{Path: Rotation, Times: []float64{0, 1}, Values: append(QIdentity[:], q[:]...)}
	h := rc.At(0.5)
	if (Quat{h[0], h[1], h[2], h[3]}).Dot(QEuler(0, 0, rad(45))) < 1-1e-12 {
		t.Errorf("slerp %v", h)
	}
	// Cubic spline with zero tangents is the smoothstep Hermite curve: the
	// midpoint is the mean, and a quarter of the way is 0.15625 of the span.
	cs := Channel{Path: Translation, Interp: CubicSpline, Times: []float64{0, 1}, Values: []float64{
		0, 0, 0, 0, 0, 0, 0, 0, 0, // in, value, out of key 0
		0, 0, 0, 10, 0, 0, 0, 0, 0, // key 1
	}}
	if got := cs.At(0.5); !nearf(got[0], 5, 1e-12) {
		t.Errorf("cubic midpoint %v", got)
	}
	if got := cs.At(0.25); !nearf(got[0], 1.5625, 1e-12) { // (3*0.0625 - 2*0.015625)*10
		t.Errorf("cubic quarter %v", got)
	}
	// With an out-tangent of 10 on key 0 the curve starts with that slope.
	cs.Values[6] = 10
	if a, b := cs.At(0)[0], cs.At(0.001)[0]; !(b > a) {
		t.Errorf("tangent has no effect: %v %v", a, b)
	}
}

func TestSwingTwist(t *testing.T) {
	// A pure 40 degree twist about y.
	tw, sw, ok := SwingTwist(QEuler(0, rad(40), 0), 1)
	if !ok || !nearf(tw, rad(40), 1e-12) || mesh.Len(sw) > 1e-12 {
		t.Errorf("pure twist: %v %v %v", tw, sw, ok)
	}
	// A pure 30 degree swing about x with the twist axis y.
	tw, sw, ok = SwingTwist(QEuler(rad(30), 0, 0), 1)
	if !ok || !nearf(tw, 0, 1e-12) || !near(sw, Vec{rad(30), 0, 0}, 1e-12) {
		t.Errorf("pure swing: %v %v %v", tw, sw, ok)
	}
	// Negative twist stays signed.
	tw, _, _ = SwingTwist(QEuler(0, rad(-70), 0), 1)
	if !nearf(tw, rad(-70), 1e-12) {
		t.Errorf("negative twist: %v", tw)
	}
	// The same rotation with the opposite quaternion sign gives the same answer.
	tw, sw, _ = SwingTwist(QEuler(rad(30), rad(20), 0).Neg(), 1)
	tw2, sw2, _ := SwingTwist(QEuler(rad(30), rad(20), 0), 1)
	if !nearf(tw, tw2, 1e-12) || !near(sw, sw2, 1e-12) {
		t.Errorf("sign dependence: %v %v vs %v %v", tw, sw, tw2, sw2)
	}
	// A pure 180 degree swing about x has no defined twist.
	if _, _, ok = SwingTwist(QEuler(rad(180), 0, 0), 1); ok {
		t.Error("180 degree transverse swing must be undefined")
	}
}

func TestCheckLimits(t *testing.T) {
	s := mustSkeleton(t,
		Joint{Name: "a", Parent: -1, Pos: Vec{}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "b", Parent: 0, Pos: Vec{0, 1, 0}, Rot: QIdentity, TwistAxis: -1},
	)
	r := Ranges{AnySet: true, Twist: Range{true, rad(-40), rad(40)}}
	r.Swing[0] = Range{true, rad(-60), rad(30)}
	r.Swing[1] = Range{true, rad(-1), rad(1)} // about the twist axis: ignored
	if v := s.Check(0, QEuler(rad(20), rad(30), 0), r); len(v) != 0 {
		t.Errorf("inside the ranges: %v", v)
	}
	v := s.Check(0, QEuler(rad(45), rad(50), 0), r)
	if len(v) != 2 {
		t.Fatalf("outside both: %v", v)
	}
	comps := map[string]bool{v[0].Component: true, v[1].Component: true}
	if !comps["twist"] || !comps["swing_x"] {
		t.Errorf("components %v", comps)
	}
	if v := s.Check(0, QEuler(rad(180), 0, 0), r); len(v) == 0 || !v[0].Undefined {
		t.Errorf("a 180 degree swing must fail as undefined: %v", v)
	}
	if v := s.Check(0, QEuler(rad(179), 0, 0), Ranges{}); v != nil {
		t.Errorf("no ranges, no test: %v", v)
	}
}

func TestTwistAxisChoice(t *testing.T) {
	mk := func(child Vec, rot Quat, explicit int) *Skeleton {
		return mustSkeleton(t,
			Joint{Name: "a", Parent: -1, Pos: Vec{}, Rot: rot, TwistAxis: explicit},
			Joint{Name: "b", Parent: 0, Pos: child, Rot: rot, TwistAxis: -1},
		)
	}
	if a := mk(Vec{0, 2, 0}, QIdentity, -1).TwistAxisOf(0); a != 1 {
		t.Errorf("bone along y: %d", a)
	}
	if a := mk(Vec{2, 0.5, 0}, QIdentity, -1).TwistAxisOf(0); a != 0 {
		t.Errorf("bone along x: %d", a)
	}
	// Seen in the joint's own frame: a joint turned 90 degrees about z has its x axis along world y.
	if a := mk(Vec{0, 2, 0}, QEuler(0, 0, rad(90)), -1).TwistAxisOf(0); a != 0 {
		t.Errorf("rotated frame: %d", a)
	}
	// Ties choose x, then y, then z.
	if a := mk(Vec{1, 1, 0}, QIdentity, -1).TwistAxisOf(0); a != 0 {
		t.Errorf("x/y tie: %d", a)
	}
	if a := mk(Vec{0, 1, 1}, QIdentity, -1).TwistAxisOf(0); a != 1 {
		t.Errorf("y/z tie: %d", a)
	}
	// An explicit axis wins; a zero direction and a lone root give y.
	if a := mk(Vec{0, 2, 0}, QIdentity, 2).TwistAxisOf(0); a != 2 {
		t.Errorf("explicit: %d", a)
	}
	if a := mk(Vec{}, QIdentity, -1).TwistAxisOf(0); a != 1 {
		t.Errorf("zero direction: %d", a)
	}
	lone := mustSkeleton(t, Joint{Name: "a", Parent: -1, Rot: QIdentity, TwistAxis: -1})
	if a := lone.TwistAxisOf(0); a != 1 {
		t.Errorf("lone root: %d", a)
	}
	// A leaf takes the direction from its parent.
	if a := mk(Vec{3, 0, 0}, QIdentity, -1).TwistAxisOf(1); a != 0 {
		t.Errorf("leaf: %d", a)
	}
}

func TestGroundShift(t *testing.T) {
	if got := GroundShift(-0.02, 0.005, 1); !nearf(got, 0.025, 1e-12) {
		t.Errorf("%v", got)
	}
	if got := GroundShift(0.1, 0, 0.5); !nearf(got, -0.05, 1e-12) {
		t.Errorf("negative correction %v", got)
	}
	if got := Lowest([]Vec{{0, 1, 0}, {0, -0.5, 0}}, 1); got != -0.5 {
		t.Errorf("%v", got)
	}
}
