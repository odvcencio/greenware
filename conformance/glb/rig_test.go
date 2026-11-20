package glb_test

import (
	"math"
	"testing"

	"m31labs.dev/greenware/conformance/glb"
	"m31labs.dev/greenware/conformance/internal/fixture"
)

// bendClip adds a LINEAR rotation clip on j1 and a morph-weights clip to a strip.
func bendClip(b *fixture.Builder, ids fixture.StripIDs, times []float32, quats [][4]float32) {
	var flat []float32
	for _, q := range quats {
		flat = append(flat, q[:]...)
	}
	in := b.Floats32("SCALAR", times)
	out := b.Floats32("VEC4", flat)
	b.Animation("bend", []fixture.Sampler{{Input: in, Output: out}},
		[]fixture.Channel{{Sampler: 0, Node: ids.J1, Path: "rotation"}})
}

var identityQ = [4]float32{0, 0, 0, 1}

func decodeBuilder(b *fixture.Builder) (*glb.File, []glb.Finding) {
	return glb.Decode(b.Bytes())
}

// one asserts exactly one finding with the given code and severity.
func one(t *testing.T, fs []glb.Finding, code string, sev glb.Severity) {
	t.Helper()
	noInternal(t, fs)
	if len(fs) != 1 || fs[0].Code != code || fs[0].Severity != sev {
		t.Fatalf("want exactly one %s %s, got %v", sev, code, fs)
	}
}

func TestValidRiggedStripIsClean(t *testing.T) {
	b, ids := fixture.NewStrip(fixture.Strip{Morph: true, MorphNames: []string{"lift"}})
	bendClip(b, ids, []float32{0, 1}, [][4]float32{identityQ, fixture.Quat90Z})
	// cubic translation and STEP translation and morph weights
	t3 := b.Floats32("SCALAR", []float32{0, 2})
	cub := b.Floats32("VEC3", []float32{0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0})
	ws := b.Floats32("SCALAR", []float32{0, 1})
	wo := b.Floats32("SCALAR", []float32{0, 1})
	b.Animation("rest", []fixture.Sampler{{Input: t3, Output: cub, Interpolation: "CUBICSPLINE"}, {Input: ws, Output: wo, Interpolation: "STEP"}},
		[]fixture.Channel{{Sampler: 0, Node: ids.J0, Path: "translation"}, {Sampler: 1, Node: ids.Body, Path: "weights"}})
	f, fs := decodeBuilder(b)
	if len(fs) != 0 {
		t.Fatalf("findings on a valid rig: %v", fs)
	}
	if names, ok := f.Doc.TargetNames(ids.Mesh); !ok || len(names) != 1 || names[0] != "lift" {
		t.Fatalf("target names: %v %v", names, ok)
	}
}

func TestNormalizedIntegerWeightsAreAcceptedWithinQuantum(t *testing.T) {
	// 85+85+85 = 255: exactly 1. 100+100+50 = 250: off by 5/255, beyond the
	// allowance of 2/255, so it is an error.
	mk := func(w [][4]uint8) []glb.Finding {
		b := fixture.New()
		pos := b.Vec3s([][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}, true)
		idx := b.Indices16([]uint32{0, 1, 2})
		j := b.Joints8([][4]uint8{{0}, {0}, {0}})
		mesh := b.RawMesh("m", map[string]int{"POSITION": pos, "JOINTS_0": j, "WEIGHTS_0": b.Weights8(w)}, idx, nil, nil, nil)
		jn := b.Node("j", -1, [3]float64{})
		body := b.Node("body", mesh, [3]float64{})
		b.Set(body, "skin", b.Skin([]int{jn}, -1, -1))
		b.Scene(jn, body)
		_, fs := decodeBuilder(b)
		return fs
	}
	if fs := mk([][4]uint8{{85, 85, 85}, {255}, {128, 127}}); len(fs) != 0 {
		t.Fatalf("exact sums: %v", fs)
	}
	if fs := mk([][4]uint8{{255}, {255}, {254, 0, 0, 0}}); len(fs) != 0 {
		t.Fatalf("one step off is within the rounding allowance: %v", fs)
	}
	one(t, mk([][4]uint8{{255}, {255}, {100, 100, 50}}), "WEIGHTS-SUM", glb.Error)
}

func TestWeightSumThresholds(t *testing.T) {
	sum := func(extra float32) []glb.Finding {
		b, _ := fixture.NewStrip(fixture.Strip{Weights: [][4]float32{{1}, {1}, {0.5, 0.5}, {0.5, 0.5}, {1}, {1 + extra}}})
		_, fs := decodeBuilder(b)
		return fs
	}
	if fs := sum(0); len(fs) != 0 {
		t.Fatalf("exact: %v", fs)
	}
	if fs := sum(5e-6); len(fs) != 0 {
		t.Fatalf("5e-6 is inside 1e-5: %v", fs)
	}
	one(t, sum(5e-5), "WEIGHTS-SUM", glb.Warning)
	one(t, sum(5e-4), "WEIGHTS-SUM", glb.Error)
	one(t, sum(-0.1), "WEIGHTS-SUM", glb.Error)
}

func TestMalformedRigsGiveOneFinding(t *testing.T) {
	nan := float32(math.NaN())
	var short int
	type edit func(b *fixture.Builder, ids fixture.StripIDs, doc map[string]any)
	cases := []struct {
		name  string
		strip fixture.Strip
		anim  func(b *fixture.Builder, ids fixture.StripIDs)
		edit  edit
		code  string
		sev   glb.Severity
	}{
		{name: "weights not summing", strip: fixture.Strip{Weights: [][4]float32{{1}, {1}, {0.5, 0.4}, {0.5, 0.5}, {1}, {1}}}, code: "WEIGHTS-SUM", sev: glb.Error},
		{name: "negative weight", strip: fixture.Strip{Weights: [][4]float32{{1}, {1}, {1.5, -0.5}, {0.5, 0.5}, {1}, {1}}}, code: "WEIGHTS-NEGATIVE", sev: glb.Error},
		{name: "NaN weight", strip: fixture.Strip{Weights: [][4]float32{{1}, {1}, {nan, 0.5}, {0.5, 0.5}, {1}, {1}}}, code: "ACC-NONFINITE", sev: glb.Error},
		{name: "joint index out of range", strip: fixture.Strip{Joints: [][4]uint8{{0}, {0}, {0, 1}, {0, 1}, {1}, {2}}}, code: "SKIN-JOINT-INDEX", sev: glb.Error},
		{name: "unsorted times", anim: func(b *fixture.Builder, ids fixture.StripIDs) {
			bendClip(b, ids, []float32{0, 2, 1}, [][4]float32{identityQ, fixture.Quat90Z, identityQ})
		}, code: "ANIM-TIMES", sev: glb.Error},
		{name: "duplicate time", anim: func(b *fixture.Builder, ids fixture.StripIDs) {
			bendClip(b, ids, []float32{0, 1, 1}, [][4]float32{identityQ, fixture.Quat90Z, identityQ})
		}, code: "ANIM-TIMES", sev: glb.Error},
		{name: "missing time min and max", anim: func(b *fixture.Builder, ids fixture.StripIDs) {
			var raw []byte
			raw = append(raw, 0, 0, 0, 0, 0, 0, 0x80, 0x3f) // 0.0, 1.0
			in := b.Raw("SCALAR", 5126, 2, false, raw, 0)
			out := b.Floats32("VEC4", []float32{0, 0, 0, 1, 0, 0, 0, 1})
			b.Animation("bend", []fixture.Sampler{{Input: in, Output: out}}, []fixture.Channel{{Sampler: 0, Node: ids.J1, Path: "rotation"}})
		}, code: "ANIM-INPUT", sev: glb.Error},
		{name: "wrong time max", anim: func(b *fixture.Builder, ids fixture.StripIDs) {
			bendClip(b, ids, []float32{0, 1}, [][4]float32{identityQ, fixture.Quat90Z})
		}, edit: func(b *fixture.Builder, ids fixture.StripIDs, doc map[string]any) {
			accs := doc["accessors"].([]any)
			for _, a := range accs {
				m := a.(map[string]any)
				if m["type"] == "SCALAR" && m["componentType"] == 5126 {
					m["max"] = []float64{2}
				}
			}
		}, code: "ACC-MAX", sev: glb.Error},
		{name: "NaN rotation", anim: func(b *fixture.Builder, ids fixture.StripIDs) {
			bendClip(b, ids, []float32{0, 1}, [][4]float32{identityQ, {0, nan, 0, 1}})
		}, code: "ACC-NONFINITE", sev: glb.Error},
		{name: "non-unit rotation", anim: func(b *fixture.Builder, ids fixture.StripIDs) {
			bendClip(b, ids, []float32{0, 1}, [][4]float32{identityQ, {0, 0, 0, 1.01}})
		}, code: "ANIM-QUAT", sev: glb.Warning},
		{name: "cubic output count", anim: func(b *fixture.Builder, ids fixture.StripIDs) {
			in := b.Floats32("SCALAR", []float32{0, 1})
			out := b.Floats32("VEC3", []float32{0, 0, 0, 1, 1, 1}) // needs 6 elements
			b.Animation("c", []fixture.Sampler{{Input: in, Output: out, Interpolation: "CUBICSPLINE"}}, []fixture.Channel{{Sampler: 0, Node: ids.J0, Path: "translation"}})
		}, code: "ANIM-OUTPUT", sev: glb.Error},
		{name: "cubic with one key", anim: func(b *fixture.Builder, ids fixture.StripIDs) {
			in := b.Floats32("SCALAR", []float32{0})
			out := b.Floats32("VEC3", []float32{0, 0, 0, 0, 0, 0, 0, 0, 0})
			b.Animation("c", []fixture.Sampler{{Input: in, Output: out, Interpolation: "CUBICSPLINE"}}, []fixture.Channel{{Sampler: 0, Node: ids.J0, Path: "translation"}})
		}, code: "ANIM-INPUT-COUNT", sev: glb.Error},
		{name: "duplicate node and path", anim: func(b *fixture.Builder, ids fixture.StripIDs) {
			in := b.Floats32("SCALAR", []float32{0, 1})
			out := b.Floats32("VEC3", []float32{0, 0, 0, 1, 1, 1})
			b.Animation("d", []fixture.Sampler{{Input: in, Output: out}}, []fixture.Channel{
				{Sampler: 0, Node: ids.J0, Path: "translation"}, {Sampler: 0, Node: ids.J0, Path: "translation"}})
		}, code: "ANIM-DUP-TARGET", sev: glb.Error},
		{name: "channel node out of range", anim: func(b *fixture.Builder, ids fixture.StripIDs) {
			in := b.Floats32("SCALAR", []float32{0, 1})
			out := b.Floats32("VEC3", []float32{0, 0, 0, 1, 1, 1})
			b.Animation("d", []fixture.Sampler{{Input: in, Output: out}}, []fixture.Channel{{Sampler: 0, Node: 99, Path: "translation"}})
		}, code: "ANIM-CHANNEL", sev: glb.Error},
		{name: "weights channel on a node without targets", anim: func(b *fixture.Builder, ids fixture.StripIDs) {
			in := b.Floats32("SCALAR", []float32{0, 1})
			out := b.Floats32("SCALAR", []float32{0, 1})
			b.Animation("w", []fixture.Sampler{{Input: in, Output: out}}, []fixture.Channel{{Sampler: 0, Node: ids.J0, Path: "weights"}})
		}, code: "ANIM-WEIGHTS-TARGET", sev: glb.Error},
		{name: "morph weights channel count", strip: fixture.Strip{Morph: true}, anim: func(b *fixture.Builder, ids fixture.StripIDs) {
			in := b.Floats32("SCALAR", []float32{0, 1})
			out := b.Floats32("SCALAR", []float32{0, 1, 0.5}) // one target, two keys: needs 2
			b.Animation("w", []fixture.Sampler{{Input: in, Output: out}}, []fixture.Channel{{Sampler: 0, Node: ids.Body, Path: "weights"}})
		}, code: "ANIM-OUTPUT", sev: glb.Error},
		{name: "mesh weights length", strip: fixture.Strip{Morph: true}, edit: func(b *fixture.Builder, ids fixture.StripIDs, doc map[string]any) {
			doc["meshes"].([]any)[0].(map[string]any)["weights"] = []float64{0, 0}
		}, code: "MESH-WEIGHTS", sev: glb.Error},
		{name: "target names length", strip: fixture.Strip{Morph: true, MorphNames: []string{"a", "b"}}, code: "MORPH-NAMES", sev: glb.Error},
		{name: "target names duplicate", strip: fixture.Strip{Morph: true, MorphNames: []string{"a"}}, edit: func(b *fixture.Builder, ids fixture.StripIDs, doc map[string]any) {
			doc["meshes"].([]any)[0].(map[string]any)["extras"] = map[string]any{"targetNames": []string{"a", "a"}}
		}, code: "MORPH-NAMES", sev: glb.Error},
		{name: "morph target element count", strip: fixture.Strip{Morph: true}, edit: func(b *fixture.Builder, ids fixture.StripIDs, doc map[string]any) {
			prim := doc["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)
			prim["targets"] = []any{map[string]any{"POSITION": short}}
		}, anim: func(b *fixture.Builder, ids fixture.StripIDs) { short = b.Vec3s(make([][3]float32, 5), false) },
			code: "TARGET-COUNT", sev: glb.Error},
		{name: "ibm count", strip: fixture.Strip{IBM: [][16]float32{fixture.Translation4(0, 0, 0)}}, code: "SKIN-IBM", sev: glb.Error},
		{name: "missing JOINTS_0", edit: func(b *fixture.Builder, ids fixture.StripIDs, doc map[string]any) {
			attrs := doc["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["attributes"].(map[string]any)
			delete(attrs, "JOINTS_0")
			delete(attrs, "WEIGHTS_0")
		}, code: "SKIN-ATTRS", sev: glb.Error},
		{name: "skeleton is not an ancestor", edit: func(b *fixture.Builder, ids fixture.StripIDs, doc map[string]any) {
			doc["skins"].([]any)[0].(map[string]any)["skeleton"] = ids.Body
		}, code: "SKIN-SKELETON", sev: glb.Error},
		{name: "joints in two trees", edit: func(b *fixture.Builder, ids fixture.StripIDs, doc map[string]any) {
			// detach j1 from j0 and make it a scene root
			doc["nodes"].([]any)[ids.J0].(map[string]any)["children"] = nil
			delete(doc["nodes"].([]any)[ids.J0].(map[string]any), "children")
			doc["scenes"].([]any)[0].(map[string]any)["nodes"] = []int{ids.Root, ids.Body, ids.J1}
		}, code: "SKIN-ROOT", sev: glb.Error},
		{name: "cycle in the skeleton", edit: func(b *fixture.Builder, ids fixture.StripIDs, doc map[string]any) {
			// j0 and j1 become each other's child, and leave the scene
			doc["nodes"].([]any)[ids.J1].(map[string]any)["children"] = []int{ids.J0}
			delete(doc["nodes"].([]any)[ids.Root].(map[string]any), "children")
		}, code: "NODE-CYCLE", sev: glb.Error},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, ids := fixture.NewStrip(c.strip)
			if c.anim != nil {
				c.anim(b, ids)
			}
			doc, bin := b.Parts()
			if c.edit != nil {
				c.edit(b, ids, doc)
			}
			_, fs := glb.Decode(fixture.Pack(doc, bin))
			one(t, fs, c.code, c.sev)
		})
	}
}

func TestNaNMorphDeltaIsOneFinding(t *testing.T) {
	b, _ := fixture.NewStrip(fixture.Strip{})
	pts := make([][3]float32, 6)
	pts[3][1] = float32(math.NaN())
	t0 := b.Vec3s(pts, false)
	doc, bin := b.Parts()
	prim := doc["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)
	prim["targets"] = []any{map[string]any{"POSITION": t0}}
	_, fs := glb.Decode(fixture.Pack(doc, bin))
	one(t, fs, "ACC-NONFINITE", glb.Error)
}

func TestTargetNeedsBaseAttribute(t *testing.T) {
	b, _ := fixture.NewStrip(fixture.Strip{})
	nrm := b.Vec3s(make([][3]float32, 6), false)
	doc, bin := b.Parts()
	prim := doc["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)
	prim["targets"] = []any{map[string]any{"NORMAL": nrm}}
	_, fs := glb.Decode(fixture.Pack(doc, bin))
	one(t, fs, "TARGET-BASE", glb.Error)
}

func TestSkinAttributesWithoutSkinWarn(t *testing.T) {
	b, ids := fixture.NewStrip(fixture.Strip{})
	doc, bin := b.Parts()
	delete(doc["nodes"].([]any)[ids.Body].(map[string]any), "skin")
	_, fs := glb.Decode(fixture.Pack(doc, bin))
	one(t, fs, "SKIN-ATTRS-UNUSED", glb.Warning)
}

func TestJointsWithoutWeightsAreOneFinding(t *testing.T) {
	// WEIGHTS_0 removed: one SKIN-ATTRS finding names it; the unpaired JOINTS_0 is not reported twice.
	b, _ := fixture.NewStrip(fixture.Strip{})
	doc, bin := b.Parts()
	attrs := doc["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["attributes"].(map[string]any)
	delete(attrs, "WEIGHTS_0")
	_, fs := glb.Decode(fixture.Pack(doc, bin))
	one(t, fs, "SKIN-ATTRS", glb.Error)
}

func TestMeshNodeMatrixIsNotAnimatable(t *testing.T) {
	b, ids := fixture.NewStrip(fixture.Strip{})
	in := b.Floats32("SCALAR", []float32{0, 1})
	out := b.Floats32("VEC3", []float32{0, 0, 0, 1, 1, 1})
	b.Animation("m", []fixture.Sampler{{Input: in, Output: out}}, []fixture.Channel{{Sampler: 0, Node: ids.J1, Path: "translation"}})
	doc, bin := b.Parts()
	n := doc["nodes"].([]any)[ids.J1].(map[string]any)
	delete(n, "translation")
	n["matrix"] = []float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 1, 0, 1}
	_, fs := glb.Decode(fixture.Pack(doc, bin))
	one(t, fs, "ANIM-TARGET-MATRIX", glb.Error)
}

func TestRotationOutputNormalizedShortAccepted(t *testing.T) {
	b, ids := fixture.NewStrip(fixture.Strip{})
	in := b.Floats32("SCALAR", []float32{0, 1})
	// two unit quaternions as normalized shorts: (0,0,0,1) and (0,0,0,1)
	var raw []byte
	for k := 0; k < 2; k++ {
		raw = append(raw, 0, 0, 0, 0, 0, 0, 0xff, 0x7f)
	}
	out := b.Raw("VEC4", 5122, 2, true, raw, 0)
	b.Animation("n", []fixture.Sampler{{Input: in, Output: out}}, []fixture.Channel{{Sampler: 0, Node: ids.J1, Path: "rotation"}})
	if _, fs := decodeBuilder(b); len(fs) != 0 {
		t.Fatalf("got %v", fs)
	}
}

func TestRotationOutputUnnormalizedShortRejected(t *testing.T) {
	b, ids := fixture.NewStrip(fixture.Strip{})
	in := b.Floats32("SCALAR", []float32{0, 1})
	out := b.Raw("VEC4", 5122, 2, false, make([]byte, 16), 0)
	b.Animation("n", []fixture.Sampler{{Input: in, Output: out}}, []fixture.Channel{{Sampler: 0, Node: ids.J1, Path: "rotation"}})
	_, fs := decodeBuilder(b)
	one(t, fs, "ANIM-OUTPUT-TYPE", glb.Error)
}

func TestWeightsSumAcrossSets(t *testing.T) {
	// Eight influences over two sets: each set sums to 0.5, the vertex sums to 1.
	// Checking one set alone would call this a fault.
	b := fixture.New()
	pos := b.Vec3s([][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}, true)
	idx := b.Indices16([]uint32{0, 1, 2})
	j0 := b.Joints8([][4]uint8{{0, 1, 2, 3}, {0, 1, 2, 3}, {0, 1, 2, 3}})
	j1 := b.Joints8([][4]uint8{{4, 5, 6, 7}, {4, 5, 6, 7}, {4, 5, 6, 7}})
	half := []float32{0.125, 0.125, 0.125, 0.125, 0.125, 0.125, 0.125, 0.125, 0.125, 0.125, 0.125, 0.125}
	w0 := b.Floats("VEC4", 3, half, false)
	w1 := b.Floats("VEC4", 3, half, false)
	mesh := b.RawMesh("m", map[string]int{"POSITION": pos, "JOINTS_0": j0, "WEIGHTS_0": w0, "JOINTS_1": j1, "WEIGHTS_1": w1}, idx, nil, nil, nil)
	root := b.Node("rig", -1, [3]float64{})
	var joints []int
	for k := 0; k < 8; k++ {
		j := b.Node(string(rune('a'+k)), -1, [3]float64{})
		b.Child(root, j)
		joints = append(joints, j)
	}
	body := b.Node("body", mesh, [3]float64{})
	b.Set(body, "skin", b.Skin(joints, -1, -1))
	b.Scene(root, body)
	if _, fs := decodeBuilder(b); len(fs) != 0 {
		t.Fatalf("got %v", fs)
	}
}

func TestSixteenBitWeightsAndJoints(t *testing.T) {
	b := fixture.New()
	pos := b.Vec3s([][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}, true)
	idx := b.Indices16([]uint32{0, 1, 2})
	j := b.Joints16([][4]uint16{{0}, {0}, {0}})
	// 32768 + 32767 = 65535: exactly 1
	w := b.Weights16([][4]uint16{{65535}, {32768, 32767}, {1, 65534}})
	mesh := b.RawMesh("m", map[string]int{"POSITION": pos, "JOINTS_0": j, "WEIGHTS_0": w}, idx, nil, nil, nil)
	jn := b.Node("j", -1, [3]float64{})
	body := b.Node("body", mesh, [3]float64{})
	b.Set(body, "skin", b.Skin([]int{jn}, -1, -1))
	b.Scene(jn, body)
	if _, fs := decodeBuilder(b); len(fs) != 0 {
		t.Fatalf("got %v", fs)
	}
}

func TestIdentityInverseBindMatricesMayBeOmitted(t *testing.T) {
	b, _ := fixture.NewStrip(fixture.Strip{})
	doc, bin := b.Parts()
	delete(doc["skins"].([]any)[0].(map[string]any), "inverseBindMatrices")
	if _, fs := glb.Decode(fixture.Pack(doc, bin)); len(fs) != 0 {
		t.Fatalf("got %v", fs)
	}
}
