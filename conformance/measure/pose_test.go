package measure_test

import (
	"math"
	"testing"

	"m31labs.dev/greenware/conformance/glb"
	"m31labs.dev/greenware/conformance/internal/fixture"
	"m31labs.dev/greenware/conformance/measure"
)

var (
	idQ      = [4]float32{0, 0, 0, 1}
	negQ90   = [4]float32{0, 0, -fixture.Quat90Z[2], -fixture.Quat90Z[3]}
	vec      = func(v ...float32) []float32 { return v }
	bendTime = []float32{0, 1}
)

// rotationClip adds a LINEAR rotation clip.
func rotationClip(b *fixture.Builder, name string, node int, times []float32, qs ...[4]float32) {
	var flat []float32
	for _, q := range qs {
		flat = append(flat, q[:]...)
	}
	b.Animation(name, []fixture.Sampler{{Input: b.Floats32("SCALAR", times), Output: b.Floats32("VEC4", flat)}},
		[]fixture.Channel{{Sampler: 0, Node: node, Path: "rotation"}})
}

func posedStrip(t *testing.T, m *measure.Model, clip string, time float64) [][3]float64 {
	t.Helper()
	ci := -1
	if clip != "" {
		var ok bool
		if ci, ok = m.ClipByName(clip); !ok {
			t.Fatalf("no clip %q", clip)
		}
	}
	p, err := m.PoseAt(ci, time)
	if err != nil {
		t.Fatal(err)
	}
	prims, err := m.Posed(p, measure.Whole)
	if err != nil || len(prims) != 1 {
		t.Fatalf("posed: %v %d", err, len(prims))
	}
	return prims[0].Pos
}

func wantPos(t *testing.T, label string, got [][3]float64, want [][3]float64, tol float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d vertices, want %d", label, len(got), len(want))
	}
	for i := range want {
		for k := 0; k < 3; k++ {
			if math.Abs(got[i][k]-want[i][k]) > tol {
				t.Errorf("%s: vertex %d = %v, want %v", label, i, got[i], want[i])
				break
			}
		}
	}
}

const r2 = 0.7071067811865476 // sqrt(2)/2

func TestStripBendsToAnalyticPositions(t *testing.T) {
	b, ids := fixture.NewStrip(fixture.Strip{})
	rotationClip(b, "bend", ids.J1, bendTime, idQ, fixture.Quat90Z)
	m := load(t, b.Bytes())

	// Rest and clip start: the stored positions.
	rest := [][3]float64{{-0.5, 0, 0}, {0.5, 0, 0}, {-0.5, 1, 0}, {0.5, 1, 0}, {-0.5, 2, 0}, {0.5, 2, 0}}
	wantPos(t, "rest", posedStrip(t, m, "", 0), rest, 1e-6)
	wantPos(t, "t=0", posedStrip(t, m, "bend", 0), rest, 1e-6)

	// j1 turned 90 degrees about +Z around its own origin (0,1,0): (x,y) -> (-y,x).
	// Row 0 follows j0 and stays. Row 1 is half j0, half j1. Row 2 follows j1.
	//   v5 = (0.5,2) -> rel (0.5,1) -> (-1,0.5) -> world (-1,1.5)
	//   v4 = (-0.5,2) -> rel (-0.5,1) -> (-1,-0.5) -> world (-1,0.5)
	//   v3 = mean of (0.5,1) and rel (0.5,0)->(0,0.5)->world (0,1.5) = (0.25,1.25)
	//   v2 = mean of (-0.5,1) and rel (-0.5,0)->(0,-0.5)->world (0,0.5) = (-0.25,0.75)
	bent := [][3]float64{{-0.5, 0, 0}, {0.5, 0, 0}, {-0.25, 0.75, 0}, {0.25, 1.25, 0}, {-1, 0.5, 0}, {-1, 1.5, 0}}
	wantPos(t, "t=1", posedStrip(t, m, "bend", 1), bent, 1e-6)
	wantPos(t, "after the last key holds", posedStrip(t, m, "bend", 9), bent, 1e-6)

	// Halfway is 45 degrees: rel (x,y) -> (x c - y s, x s + y c) with c = s = sqrt(2)/2.
	//   v5 rel (0.5,1)  -> (-0.5 r2, 1.5 r2)  world y + 1
	//   v4 rel (-0.5,1) -> (-1.5 r2, 0.5 r2)
	//   v3 = mean of (0.5,1) and rel (0.5,0) -> (0.5 r2, 0.5 r2) -> y + 1
	//   v2 = mean of (-0.5,1) and rel (-0.5,0) -> (-0.5 r2, -0.5 r2) -> y + 1
	half := [][3]float64{
		{-0.5, 0, 0}, {0.5, 0, 0},
		{(-0.5 + -0.5*r2) / 2, (1 + 1 - 0.5*r2) / 2, 0},
		{(0.5 + 0.5*r2) / 2, (1 + 1 + 0.5*r2) / 2, 0},
		{-1.5 * r2, 1 + 0.5*r2, 0},
		{-0.5 * r2, 1 + 1.5*r2, 0},
	}
	wantPos(t, "t=0.5", posedStrip(t, m, "bend", 0.5), half, 1e-6)
	// Spelled out as decimals, as computed by hand.
	near(t, "v5.x", posedStrip(t, m, "bend", 0.5)[5][0], -0.3535533906, 1e-6)
	near(t, "v5.y", posedStrip(t, m, "bend", 0.5)[5][1], 2.0606601718, 1e-6)
	near(t, "v3.x", posedStrip(t, m, "bend", 0.5)[3][0], 0.4267766953, 1e-6)
	near(t, "v2.y", posedStrip(t, m, "bend", 0.5)[2][1], 0.8232233047, 1e-6)
}

func TestSlerpTakesTheShortestPath(t *testing.T) {
	// The second key is -(90 degrees about Z): the same rotation, opposite sign.
	// The long way round would pass through -135 degrees at the midpoint.
	b, ids := fixture.NewStrip(fixture.Strip{})
	rotationClip(b, "neg", ids.J1, bendTime, idQ, negQ90)
	rotationClip(b, "pos", ids.J1, bendTime, idQ, fixture.Quat90Z)
	m := load(t, b.Bytes())
	for _, tm := range []float64{0.25, 0.5, 0.75, 1} {
		wantPos(t, "neg vs pos", posedStrip(t, m, "neg", tm), posedStrip(t, m, "pos", tm), 1e-6)
	}
	got := measure.Slerp([4]float64{0, 0, 0, 1}, [4]float64{0, 0, -r2, -r2}, 0.5)
	// 45 degrees about +Z: (0, 0, sin 22.5, cos 22.5)
	want := [4]float64{0, 0, 0.3826834324, 0.9238795325}
	if measure.QuatDistance(got, want) > 1e-9 {
		t.Fatalf("slerp = %v, want %v", got, want)
	}
	if d := measure.QuatDistance([4]float64{0, 0, r2, r2}, [4]float64{0, 0, -r2, -r2}); d != 0 {
		t.Fatalf("sign-aligned distance of q and -q = %v, want 0", d)
	}
}

func TestStepLinearAndCubicSamplers(t *testing.T) {
	b, ids := fixture.NewStrip(fixture.Strip{})
	// STEP on j0.translation: keys at 0,1,2 with y = 0,1,2
	b.Animation("step", []fixture.Sampler{{Input: b.Floats32("SCALAR", vec(0, 1, 2)), Output: b.Floats32("VEC3", vec(0, 0, 0, 0, 1, 0, 0, 2, 0)), Interpolation: "STEP"}},
		[]fixture.Channel{{Sampler: 0, Node: ids.J0, Path: "translation"}})
	// LINEAR: y from 0 to 4 over 2 s
	b.Animation("lin", []fixture.Sampler{{Input: b.Floats32("SCALAR", vec(0, 2)), Output: b.Floats32("VEC3", vec(0, 0, 0, 1, 4, 2))}},
		[]fixture.Channel{{Sampler: 0, Node: ids.J0, Path: "translation"}})
	// CUBICSPLINE: keys at 0 and 2; (in, value, out) per key.
	//   key 0: in (0,0,0) value (0,0,0) out (0,2,0)   key 1: in (0,0,0) value (0,2,0) out (0,0,0)
	b.Animation("cub", []fixture.Sampler{{Input: b.Floats32("SCALAR", vec(0, 2)),
		Output: b.Floats32("VEC3", vec(0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0)), Interpolation: "CUBICSPLINE"}},
		[]fixture.Channel{{Sampler: 0, Node: ids.J0, Path: "translation"}})
	m := load(t, b.Bytes())
	y := func(clip string, tm float64) float64 {
		ci, ok := m.ClipByName(clip)
		if !ok {
			t.Fatalf("no clip %s", clip)
		}
		tr, _, _, err := m.SampleTRS(ci, tm, ids.J0)
		if err != nil {
			t.Fatal(err)
		}
		return tr[1]
	}
	for _, c := range []struct {
		clip    string
		time, y float64
	}{
		{"step", -1, 0}, {"step", 0, 0}, {"step", 0.999, 0}, {"step", 1, 1}, {"step", 1.5, 1},
		{"step", 2, 2}, {"step", 7, 2}, // the last key holds
		{"lin", 0, 0}, {"lin", 0.5, 1}, {"lin", 1, 2}, {"lin", 1.5, 3}, {"lin", 2, 4}, {"lin", 9, 4},
		// Hermite with dt=2: p(s) = h10*dt*b0 + h01*p1, s = t/2
		//   t=0.5: s=0.25, h10=0.140625, h01=0.15625 -> 0.140625*2*2 + 0.15625*2 = 0.875
		//   t=1:   s=0.5,  h10=0.125,    h01=0.5     -> 0.125*2*2 + 0.5*2       = 1.5
		{"cub", 0, 0}, {"cub", 0.5, 0.875}, {"cub", 1, 1.5}, {"cub", 2, 2}, {"cub", 3, 2},
	} {
		near(t, c.clip+" at "+ftoa(c.time), y(c.clip, c.time), c.y, 1e-6)
	}
}

func ftoa(f float64) string {
	return string(rune('0'+int(math.Abs(f)))) + "." + string(rune('0'+int(math.Abs(f)*10)%10))
}

func TestCubicRotationIsNormalised(t *testing.T) {
	b, ids := fixture.NewStrip(fixture.Strip{})
	z := []float32{0, 0, 0, 0}
	var out []float32
	out = append(out, z...)
	out = append(out, idQ[:]...)
	out = append(out, z...)
	out = append(out, z...)
	out = append(out, fixture.Quat90Z[:]...)
	out = append(out, z...)
	b.Animation("cr", []fixture.Sampler{{Input: b.Floats32("SCALAR", vec(0, 1)), Output: b.Floats32("VEC4", out), Interpolation: "CUBICSPLINE"}},
		[]fixture.Channel{{Sampler: 0, Node: ids.J1, Path: "rotation"}})
	m := load(t, b.Bytes())
	ci, _ := m.ClipByName("cr")
	_, q, _, err := m.SampleTRS(ci, 0.5, ids.J1)
	if err != nil {
		t.Fatal(err)
	}
	// zero tangents: the blend is (id + q90)/2, normalised; that is 45 degrees.
	if d := measure.QuatDistance(q, [4]float64{0, 0, 0.3826834324, 0.9238795325}); d > 1e-6 {
		t.Fatalf("q = %v", q)
	}
}

func TestMorphWeightsAndSkinTogether(t *testing.T) {
	b, ids := fixture.NewStrip(fixture.Strip{Morph: true, MorphNames: []string{"lift"}})
	rot := b.Floats32("VEC4", []float32{0, 0, 0, 1, fixture.Quat90Z[0], fixture.Quat90Z[1], fixture.Quat90Z[2], fixture.Quat90Z[3]})
	w := b.Floats32("SCALAR", vec(0, 1))
	tm := b.Floats32("SCALAR", vec(0, 1))
	b.Animation("both", []fixture.Sampler{{Input: tm, Output: rot}, {Input: tm, Output: w}},
		[]fixture.Channel{{Sampler: 0, Node: ids.J1, Path: "rotation"}, {Sampler: 1, Node: ids.Body, Path: "weights"}})
	m := load(t, b.Bytes())
	// Weight 0.5 moves vertex 5 by (0,0,0.5) before skinning: (0.5,2,0.5) at rest.
	p, _ := m.PoseAt(0, 0.5)
	if got := p.Weights(ids.Body); len(got) != 1 || math.Abs(got[0]-0.5) > 1e-9 {
		t.Fatalf("weights at 0.5: %v", got)
	}
	// At t=1: weight 1, rotation 90 degrees. Vertex 5 morphs to (0.5,2,1); relative to j1
	// that is (0.5,1,1); the rotation about Z gives (-1,0.5,1); world (-1,1.5,1).
	got := posedStrip(t, m, "both", 1)
	wantPos(t, "v5", got[5:6], [][3]float64{{-1, 1.5, 1}}, 1e-6)
	// Vertex 4 has no delta: unchanged from the bend-only case.
	wantPos(t, "v4", got[4:5], [][3]float64{{-1, 0.5, 0}}, 1e-6)
	// Default weights are 0, so the stored mesh is unmorphed at rest.
	wantPos(t, "rest", posedStrip(t, m, "", 0)[5:6], [][3]float64{{0.5, 2, 0}}, 1e-9)
}

func TestMorphOnAnUnskinnedMesh(t *testing.T) {
	b := fixture.New()
	pos := b.Vec3s([][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}, true)
	idx := b.Indices16([]uint32{0, 1, 2})
	tgt := b.Vec3s([][3]float32{{0, -0.25, 0}, {0, 0, 0}, {0, 0, 0}}, true)
	mesh := b.RawMesh("m", map[string]int{"POSITION": pos}, idx, []map[string]int{{"POSITION": tgt}}, []float64{0.5}, nil)
	n := b.Node("n", mesh, [3]float64{2, 0, 0})
	b.Scene(n)
	m := load(t, b.Bytes())
	box, ok, err := m.PosedBounds(m.RestPose(), measure.Whole)
	if err != nil || !ok {
		t.Fatal(err)
	}
	// vertex 0 drops 0.5*0.25 = 0.125 and the node moves everything by x=2.
	near(t, "min y", box.Min[1], -0.125, 1e-9)
	near(t, "min x", box.Min[0], 2, 1e-9)
	near(t, "max x", box.Max[0], 3, 1e-9)
}

func TestBindPoseReproduction(t *testing.T) {
	b, _ := fixture.NewStrip(fixture.Strip{})
	m := load(t, b.Bytes())
	devs, err := m.BindPoseDeviation()
	if err != nil || len(devs) != 1 {
		t.Fatalf("%v %d", err, len(devs))
	}
	if devs[0].Max > 1e-5 {
		t.Fatalf("bind pose deviation %g", devs[0].Max)
	}
	// A wrong inverse bind matrix for j1 (identity, not translate(0,-1,0)) lifts row 2 by 1
	// and row 1 by 0.5, so the worst vertex is off by exactly 1.
	b, _ = fixture.NewStrip(fixture.Strip{IBM: [][16]float32{fixture.Translation4(0, 0, 0), fixture.Translation4(0, 0, 0)}})
	devs, err = load(t, b.Bytes()).BindPoseDeviation()
	if err != nil {
		t.Fatal(err)
	}
	near(t, "wrong ibm", devs[0].Max, 1, 1e-6)
	if devs[0].Vertex != 4 {
		t.Errorf("worst vertex %d, want 4 (first of the two row-2 vertices)", devs[0].Vertex)
	}
}

func TestSkinnedMeshNodeTransformIsIgnored(t *testing.T) {
	b, _ := fixture.NewStrip(fixture.Strip{MeshMove: [3]float64{5, 3, -2}})
	m := load(t, b.Bytes())
	wantPos(t, "moved mesh node", posedStrip(t, m, "", 0),
		[][3]float64{{-0.5, 0, 0}, {0.5, 0, 0}, {-0.5, 1, 0}, {0.5, 1, 0}, {-0.5, 2, 0}, {0.5, 2, 0}}, 1e-6)
}

func TestRestBoundsOfASkinnedMeshIgnoreItsNodeTransform(t *testing.T) {
	b, _ := fixture.NewStrip(fixture.Strip{MeshMove: [3]float64{5, 3, -2}})
	m := load(t, b.Bytes())
	box, ok, err := m.Bounds(measure.Whole)
	if err != nil || !ok {
		t.Fatal(err)
	}
	near(t, "min x", box.Min[0], -0.5, 1e-6)
	near(t, "max y", box.Max[1], 2, 1e-6)
	near(t, "max z", box.Max[2], 0, 1e-6)
}

func TestLowestVertexAndBoundsFollowThePose(t *testing.T) {
	b, ids := fixture.NewStrip(fixture.Strip{})
	// j0 rises from y=0 to y=4 over 2 s; the whole strip rises with it.
	b.Animation("lift", []fixture.Sampler{{Input: b.Floats32("SCALAR", vec(0, 2)), Output: b.Floats32("VEC3", vec(0, 0, 0, 0, 4, 0))}},
		[]fixture.Channel{{Sampler: 0, Node: ids.J0, Path: "translation"}})
	rotationClip(b, "bend", ids.J1, bendTime, idQ, fixture.Quat90Z)
	m := load(t, b.Bytes())
	lift, _ := m.ClipByName("lift")
	p, _ := m.PoseAt(lift, 0.5)
	lo, ok, err := m.LowestVertex(p, measure.Whole)
	if err != nil || !ok {
		t.Fatal(err)
	}
	near(t, "lowest y at 0.5", lo.Y, 1, 1e-6)
	if lo.Vertex != 0 || lo.Node != "body" {
		t.Errorf("lowest = %+v; the first of the two bottom vertices is vertex 0 of body", lo)
	}
	box, _, _ := m.PosedBounds(p, measure.Whole)
	near(t, "bounds min y", box.Min[1], 1, 1e-6)
	near(t, "bounds max y", box.Max[1], 3, 1e-6)
	// bent at 90 degrees: x reaches -1 (vertices 4 and 5); y spans 0 to 1.5
	bend, _ := m.ClipByName("bend")
	p, _ = m.PoseAt(bend, 1)
	box, _, _ = m.PosedBounds(p, measure.Whole)
	near(t, "bent min x", box.Min[0], -1, 1e-6)
	near(t, "bent max x", box.Max[0], 0.5, 1e-6)
	near(t, "bent max y", box.Max[1], 1.5, 1e-6)
	near(t, "bent min y", box.Min[1], 0, 1e-6)
}

func TestJointDistanceAndPositions(t *testing.T) {
	b, ids := fixture.NewStrip(fixture.Strip{})
	b.Animation("reach", []fixture.Sampler{{Input: b.Floats32("SCALAR", vec(0, 1)), Output: b.Floats32("VEC3", vec(0, 1, 0, 0, 3, 0))}},
		[]fixture.Channel{{Sampler: 0, Node: ids.J1, Path: "translation"}})
	rotationClip(b, "swing", ids.J0, bendTime, idQ, fixture.Quat90Z)
	m := load(t, b.Bytes())
	reach, _ := m.ClipByName("reach")
	p, _ := m.PoseAt(reach, 0.5)
	d, err := m.JointDistance(p, "j0", "j1")
	if err != nil {
		t.Fatal(err)
	}
	near(t, "distance at 0.5", d, 2, 1e-6)
	swing, _ := m.ClipByName("swing")
	p, _ = m.PoseAt(swing, 1)
	pos, _ := m.NodePosition(p, "j1")
	// j0 turned 90 degrees about Z: j1 at (0,1,0) goes to (-1,0,0)
	wantPos(t, "j1", [][3]float64{pos}, [][3]float64{{-1, 0, 0}}, 1e-6)
	d, _ = m.JointDistance(p, "j0", "j1")
	near(t, "bone length is kept", d, 1, 1e-6)
	if _, err := m.JointDistance(p, "j0", "nope"); err == nil {
		t.Fatal("an unknown joint must be an error")
	}
	// the strip is rigid under j0: vertex 5 (0.5,2) -> (-2,0.5)
	wantPos(t, "v5", posedStrip(t, m, "swing", 1)[5:6], [][3]float64{{-2, 0.5, 0}}, 1e-6)
}

func TestClipSummaries(t *testing.T) {
	b, ids := fixture.NewStrip(fixture.Strip{})
	rot := b.Floats32("VEC4", []float32{0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1})
	tr := b.Floats32("VEC3", []float32{0, 0, 0, 0, 1, 0})
	b.Animation("two", []fixture.Sampler{{Input: b.Floats32("SCALAR", vec(0, 0.5, 2)), Output: rot}, {Input: b.Floats32("SCALAR", vec(0.5, 3)), Output: tr}},
		[]fixture.Channel{{Sampler: 0, Node: ids.J1, Path: "rotation"}, {Sampler: 1, Node: ids.J0, Path: "translation"}})
	m := load(t, b.Bytes())
	cs := m.Clips()
	if len(cs) != 1 {
		t.Fatalf("%v", cs)
	}
	c := cs[0]
	if c.Name != "two" || c.Duration != 3 || c.Channels != 2 || c.Keys != 5 || c.UniqueTimes != 4 {
		t.Fatalf("clip = %+v; want duration 3, 2 channels, 5 keys, 4 unique times (0, 0.5, 2, 3)", c)
	}
	kt, err := m.KeyTimes(0)
	if err != nil || len(kt) != 4 || kt[0] != 0 || kt[1] != 0.5 || kt[2] != 2 || kt[3] != 3 {
		t.Fatalf("key times %v %v", kt, err)
	}
	// before the first key of the translation (t=0.5) it holds its first value
	tr0, _, _, _ := m.SampleTRS(0, 0.25, ids.J0)
	if tr0 != [3]float64{0, 0, 0} {
		t.Fatalf("held first value: %v", tr0)
	}
}

func TestWeightStatistics(t *testing.T) {
	b, _ := fixture.NewStrip(fixture.Strip{})
	m := load(t, b.Bytes())
	ws, err := m.WeightStats()
	if err != nil || len(ws) != 1 {
		t.Fatalf("%v %v", ws, err)
	}
	if w := ws[0]; w.Vertices != 6 || w.MaxInfluences != 2 || w.OverFour != 0 || w.ZeroSum != 0 || w.MaxSumError > 1e-9 {
		t.Fatalf("strip stats = %+v", w)
	}

	// Five joints; vertex 0 has five equal influences over two sets, vertex 1 has
	// none (zero sum), vertex 2 has one.
	b = fixture.New()
	pos := b.Vec3s([][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}, true)
	idx := b.Indices16([]uint32{0, 1, 2})
	j0 := b.Joints8([][4]uint8{{0, 1, 2, 3}, {0}, {4}})
	w0 := b.Floats("VEC4", 3, []float32{0.2, 0.2, 0.2, 0.2, 0, 0, 0, 0, 1, 0, 0, 0}, false)
	j1 := b.Joints8([][4]uint8{{4}, {0}, {0}})
	w1 := b.Floats("VEC4", 3, []float32{0.2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, false)
	mesh := b.RawMesh("m", map[string]int{"POSITION": pos, "JOINTS_0": j0, "WEIGHTS_0": w0, "JOINTS_1": j1, "WEIGHTS_1": w1}, idx, nil, nil, nil)
	var joints []int
	root := b.Node("rig", -1, [3]float64{})
	for k := 0; k < 5; k++ {
		j := b.Node(string(rune('a'+k)), -1, [3]float64{})
		b.Child(root, j)
		joints = append(joints, j)
	}
	body := b.Node("body", mesh, [3]float64{})
	b.Set(body, "skin", b.Skin(joints, -1, -1))
	b.Scene(root, body)
	f, fs := glb.Decode(b.Bytes())
	if len(fs) == 0 || fs[0].Code != "WEIGHTS-SUM" {
		t.Fatalf("expected the zero-sum vertex to be a finding: %v", fs)
	}
	m2, err := measure.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	ws, err = m2.WeightStats()
	if err != nil {
		t.Fatal(err)
	}
	w := ws[0]
	if w.MaxInfluences != 5 || w.OverFour != 1 || w.ZeroSum != 1 {
		t.Fatalf("stats = %+v; want 5 influences, 1 vertex over four, 1 zero sum", w)
	}
	near(t, "max sum error", w.MaxSumError, 1, 1e-9)
}

func TestRigSummary(t *testing.T) {
	b, ids := fixture.NewStrip(fixture.Strip{Morph: true, MorphNames: []string{"lift"}})
	rotationClip(b, "bend", ids.J1, bendTime, idQ, fixture.Quat90Z)
	m := load(t, b.Bytes())
	r, err := m.Rig()
	if err != nil {
		t.Fatal(err)
	}
	if r.JointCount != 2 || len(r.Skins) != 1 || r.Skins[0].JointNames[1] != "j1" || len(r.Animations) != 1 ||
		len(r.Morphs) != 1 || r.Morphs[0].Names[0] != "lift" || r.BindPoseMaxError > 1e-5 {
		t.Fatalf("rig = %+v", r)
	}
}
