package measure_test

import (
	"testing"

	"m31labs.dev/greenware/conformance/internal/fixture"
	"m31labs.dev/greenware/conformance/measure"
)

// sparseQuad is a unit quad in the XY plane. The morph target "bulge" is
// stored sparse (no base view) and moves vertices 2 and 3 only: vertex 2 by
// (0,0,1) and vertex 3 by (0.5,0,2). The mesh default weight is w.
func sparseQuad(w float64) []byte {
	b := fixture.New()
	pos := b.Vec3s([][3]float32{{0, 0, 0}, {1, 0, 0}, {1, 1, 0}, {0, 1, 0}}, true)
	idx := b.Indices16([]uint32{0, 1, 2, 0, 2, 3})
	delta := b.Sparse(fixture.SparseOpts{Count: 4, Indices: []uint32{2, 3}, Values: []float32{0, 0, 1, 0.5, 0, 2}, IndexType: 5121, MinMax: true})
	m := b.RawMesh("quad", map[string]int{"POSITION": pos}, idx,
		[]map[string]int{{"POSITION": delta}}, []float64{w}, map[string]any{"targetNames": []string{"bulge"}})
	b.Scene(b.Node("quad", m, [3]float64{}))
	return b.Bytes()
}

func TestSparseMorphAppliedAtWeights(t *testing.T) {
	cases := []struct {
		w    float64
		want [][3]float64
	}{
		{0, [][3]float64{{0, 0, 0}, {1, 0, 0}, {1, 1, 0}, {0, 1, 0}}},
		{0.5, [][3]float64{{0, 0, 0}, {1, 0, 0}, {1, 1, 0.5}, {0.25, 1, 1}}},
		{1, [][3]float64{{0, 0, 0}, {1, 0, 0}, {1, 1, 1}, {0.5, 1, 2}}},
	}
	for _, c := range cases {
		m := load(t, sparseQuad(c.w))
		p, err := m.PoseAt(-1, 0)
		if err != nil {
			t.Fatal(err)
		}
		prims, err := m.Posed(p, measure.Whole)
		if err != nil || len(prims) != 1 {
			t.Fatalf("posed: %v %d", err, len(prims))
		}
		wantPos(t, "weight "+string(rune('0'+int(c.w*2))), prims[0].Pos, c.want, 1e-12)
		// the static measures use the default weights as well
		box, ok, err := m.Bounds(measure.Whole)
		if err != nil || !ok {
			t.Fatalf("bounds: %v %v", ok, err)
		}
		near(t, "max z", box.Max[2], c.want[3][2], 1e-6)
		near(t, "max x", box.Max[0], 1, 1e-6)
	}
}

// A sparse POSITION with no base view draws: its stored positions are the
// sparse values over zeros.
func TestSparseZeroBasePositionMeasures(t *testing.T) {
	b := fixture.New()
	pos := b.Sparse(fixture.SparseOpts{Count: 3, Indices: []uint32{1, 2}, Values: []float32{2, 0, 0, 0, 3, 0}, MinMax: true})
	m := b.RawMesh("tri", map[string]int{"POSITION": pos}, b.Indices16([]uint32{0, 1, 2}), nil, nil, nil)
	b.Scene(b.Node("tri", m, [3]float64{}))
	model := load(t, b.Bytes())
	box, ok, err := model.Bounds(measure.Whole)
	if err != nil || !ok {
		t.Fatalf("bounds %v %v", ok, err)
	}
	if box.Min != [3]float64{0, 0, 0} || box.Max != [3]float64{2, 3, 0} {
		t.Fatalf("box %+v", box)
	}
}

// The rig report says which targets are sparse and how many vertices move.
func TestRigReportsSparseMorphs(t *testing.T) {
	r, err := load(t, sparseQuad(0)).Rig()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Morphs) != 1 {
		t.Fatalf("morphs %+v", r.Morphs)
	}
	mi := r.Morphs[0]
	if !mi.Sparse || mi.Vertices != 4 || len(mi.Targets) != 1 {
		t.Fatalf("morph info %+v", mi)
	}
	if tg := mi.Targets[0]; !tg.Sparse || tg.MovedVertices != 2 || tg.Name != "bulge" || tg.Index != 0 {
		t.Fatalf("target %+v", tg)
	}
	// the same target stored dense: not sparse, same count of moved vertices
	b, _ := fixture.NewStrip(fixture.Strip{Morph: true})
	d, err := load(t, b.Bytes()).Rig()
	if err != nil {
		t.Fatal(err)
	}
	if d.Morphs[0].Sparse || d.Morphs[0].Targets[0].Sparse || d.Morphs[0].Targets[0].MovedVertices != 1 || d.Morphs[0].Vertices != 6 {
		t.Fatalf("dense %+v", d.Morphs[0])
	}
	bs, _ := fixture.NewStrip(fixture.Strip{Morph: true, MorphSparse: true})
	s, err := load(t, bs.Bytes()).Rig()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Morphs[0].Sparse || !s.Morphs[0].Targets[0].Sparse || s.Morphs[0].Targets[0].MovedVertices != 1 {
		t.Fatalf("sparse strip %+v", s.Morphs[0])
	}
}

// A sparse target and the same target stored dense pose the skinned strip alike.
func TestSparseMorphEqualsDenseOnStrip(t *testing.T) {
	pose := func(sparse bool) [][3]float64 {
		b, ids := fixture.NewStrip(fixture.Strip{Morph: true, MorphSparse: sparse})
		doc, bin := b.Parts()
		doc["meshes"].([]any)[ids.Mesh].(map[string]any)["weights"] = []float64{0.5}
		return posedStrip(t, load(t, fixture.Pack(doc, bin)), "", 0)
	}
	dense, sparse := pose(false), pose(true)
	wantPos(t, "sparse vs dense", sparse, dense, 1e-12)
	near(t, "vertex 5 z", sparse[5][2], 0.5, 1e-12)
}
