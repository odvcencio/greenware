package compiler

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
)

func TestSnapTo(t *testing.T) {
	const n = 20
	step := math.Ldexp(1, -n)
	if z := snapTo(math.Copysign(1e-9, -1), n); z != 0 || math.Signbit(z) {
		t.Errorf("tiny negative snapped to %v (sign bit %v), want +0", z, math.Signbit(z))
	}
	if z := snapTo(math.Copysign(0, -1), n); math.Signbit(z) {
		t.Error("-0 stays -0")
	}
	// Ties round away from zero, as math.Round does.
	if got := snapTo(0.5*step, n); got != step {
		t.Errorf("snap(half step) = %v, want %v", got, step)
	}
	if got := snapTo(-0.5*step, n); got != -step {
		t.Errorf("snap(-half step) = %v, want %v", got, -step)
	}
	if got := snapTo(1.5*step, n); got != 2*step {
		t.Errorf("snap(1.5 steps) = %v, want %v", got, 2*step)
	}
	// Powers of two at or above the step are exact, and snapping twice is a no-op.
	for e := -n; e <= 10; e++ {
		v := math.Ldexp(1, e)
		if got := snapTo(v, n); got != v {
			t.Errorf("snap(2^%d) = %v", e, got)
		}
	}
	for _, v := range []float64{0.123456789, -7.7654321, 1e-6, 123.456} {
		s := snapTo(v, n)
		if snapTo(s, n) != s {
			t.Errorf("snap not idempotent at %v", v)
		}
		if m := s / step; m != math.Trunc(m) {
			t.Errorf("snap(%v) = %v is not a multiple of 2^-%d", v, s, n)
		}
	}
}

func TestSnapWeightsSumToOne(t *testing.T) {
	w := []float32{
		0.3333333, 0.3333333, 0.3333334, 0,
		0.7000001, 0.1999999, 0.05, 0.05,
		1, 0, 0, 0,
		0.25, 0.25, 0.25, 0.25,
	}
	snapWeights(w, 20)
	for i := 0; i < len(w); i += 4 {
		var sum float64
		for k := 0; k < 4; k++ {
			sum += float64(w[i+k])
		}
		if sum != 1 {
			t.Errorf("vertex %d weights %v sum to %v, want exactly 1", i/4, w[i:i+4], sum)
		}
	}
}

// snapTestDoc is one skinned triangle strip with a sparse and a dense morph
// target, clip keys and inverse binds.
func snapTestDoc() *gltf.Document {
	zero := 0
	pos := []float32{0, 0, 0, 1.0000001, 0, 0, 0, 1.3333333, 0.1, 1, 1, 0.7777777}
	nrm := []float32{0.57735027, 0.57735027, 0.57735027, -1e-9, 0, 1, 0, 0.7071068, 0.7071068, 0, 0, -1}
	uv := []float32{0, 0, 0.3333333, 0, 0, 0.6666667, 1, 1}
	w := []float32{0.3333333, 0.3333333, 0.3333334, 0, 0.7000001, 0.1999999, 0.05, 0.05, 1, 0, 0, 0, 0.5, 0.5, 0, 0}
	j := []uint16{0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0}
	t1 := []float32{0, 0, 0, 0, 1e-9, 0, 0, 0, 0, 0.1234567, -0.0000001, 0}
	t2 := []float32{0.3, 0.2, 0.1, 0, 0, 0, 0, 0, 0, 0, 0, 0.9999999}
	var ib [2][16]float32
	for k := range ib {
		ib[k] = [16]float32{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, -0.123456789, 0.987654321, -1e-9, 1}
	}
	return &gltf.Document{
		Scene:  &zero,
		Scenes: []gltf.Scene{{Nodes: []int{0, 1}}},
		Nodes: []gltf.Node{
			{Name: "mesh", Mesh: &zero, Skin: &zero},
			{Name: "j0", Children: []int{2}},
			{Name: "j1", Translation: &[3]float32{0, 0.3333333, 0}},
		},
		Meshes: []gltf.Mesh{{Name: "m", Primitives: []gltf.Primitive{{
			Positions: pos, Normals: nrm, UV0: uv, Indices: []uint32{0, 1, 2, 1, 3, 2},
			Joints0: j, Weights0: w,
			Targets: []gltf.MorphTarget{{Positions: t1, Sparse: true}, {Positions: t2}},
		}}}},
		Skins: []gltf.Skin{{Joints: []int{1, 2}, InverseBindMatrices: ib[:]}},
		Animations: []gltf.Animation{{Name: "a",
			Channels: []gltf.Channel{{Node: 2, Path: gltf.PathTranslation, Sampler: 0}},
			Samplers: []gltf.AnimSampler{{Input: []float32{0, 0.3333333, 1.1}, Output: []float32{0, 0, 0, 0.1111111, 0, 0, 0.2, 0.3333333, 0}}},
		}},
	}
}

type snapAcc struct {
	Count         int
	ComponentType int
	Min, Max      []float64
	Sparse        *struct{ Count int }
}

func snapAccessors(t *testing.T, glb []byte) []snapAcc {
	t.Helper()
	n := int(binary.LittleEndian.Uint32(glb[12:]))
	var d struct{ Accessors []snapAcc }
	if err := json.Unmarshal(glb[20:20+n], &d); err != nil {
		t.Fatal(err)
	}
	return d.Accessors
}

func buildSnapped(t *testing.T, n int) (*gltf.Document, []byte) {
	t.Helper()
	d := snapTestDoc()
	snapDoc(d, n)
	b, err := gltf.Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	b, err = snapGLB(b, n)
	if err != nil {
		t.Fatal(err)
	}
	return d, b
}

func isGridMultiple(f float32, n int) bool {
	m := float64(f) * math.Ldexp(1, n)
	return m == math.Trunc(m) && !(f == 0 && math.Signbit(float64(f)))
}

func TestSnapGLB(t *testing.T) {
	const n = 20
	_, b1 := buildSnapped(t, n)
	_, b2 := buildSnapped(t, n)
	if !bytes.Equal(b1, b2) {
		t.Fatal("two snapped builds differ")
	}
	got, err := gltf.Decode(b1)
	if err != nil {
		t.Fatalf("snapped file does not decode: %v", err)
	}
	p := got.Meshes[0].Primitives[0]
	for _, s := range [][]float32{p.Positions, p.Normals, p.UV0, p.Weights0, p.Targets[0].Positions, p.Targets[1].Positions,
		got.Animations[0].Samplers[0].Output} {
		for _, f := range s {
			if !isGridMultiple(f, n) {
				t.Errorf("%v is not a multiple of 2^-%d", f, n)
			}
		}
	}
	for _, m := range got.Skins[0].InverseBindMatrices {
		for _, f := range m {
			if !isGridMultiple(f, n) {
				t.Errorf("inverse bind %v is not a multiple of 2^-%d", f, n)
			}
		}
	}
	for i := 0; i+4 <= len(p.Weights0); i += 4 {
		sum := float64(p.Weights0[i]) + float64(p.Weights0[i+1]) + float64(p.Weights0[i+2]) + float64(p.Weights0[i+3])
		if sum != 1 {
			t.Errorf("vertex %d weights sum to %v", i/4, sum)
		}
	}
	// Bounds stay equal to the data they cover.
	var bounded int
	for _, a := range snapAccessors(t, b1) {
		if a.Min == nil {
			continue
		}
		bounded++
	}
	if bounded == 0 {
		t.Fatal("no accessor carries bounds")
	}
	mn, mx := make([]float64, 3), make([]float64, 3)
	for c := 0; c < 3; c++ {
		mn[c], mx[c] = math.Inf(1), math.Inf(-1)
		for v := c; v < len(p.Positions); v += 3 {
			mn[c] = math.Min(mn[c], float64(p.Positions[v]))
			mx[c] = math.Max(mx[c], float64(p.Positions[v]))
		}
	}
	found := false
	for _, a := range snapAccessors(t, b1) {
		if len(a.Min) == 3 && a.Count == 4 && a.Sparse == nil {
			ok := true
			for c := 0; c < 3; c++ {
				if float32(a.Min[c]) != float32(mn[c]) || float32(a.Max[c]) != float32(mx[c]) {
					ok = false
				}
			}
			found = found || ok
		}
	}
	if !found {
		t.Errorf("no position accessor with bounds %v %v", mn, mx)
	}
	// The clip times keep their bounds, which equal the data.
	for _, a := range snapAccessors(t, b1) {
		if a.Count == 3 && len(a.Min) == 1 && (float32(a.Min[0]) != got.Animations[0].Samplers[0].Input[0] || float32(a.Max[0]) != got.Animations[0].Samplers[0].Input[2]) {
			t.Errorf("clip time bounds %v %v differ from the data", a.Min, a.Max)
		}
	}
}

// The sparse target keeps the entries the snapped deltas choose: a delta that
// rounds to zero is no longer an entry.
func TestSnapSparseEntries(t *testing.T) {
	sparseCount := func(d *gltf.Document) int {
		b, err := gltf.Encode(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range snapAccessors(t, b) {
			if a.Sparse != nil {
				return a.Sparse.Count
			}
		}
		return 0
	}
	before := sparseCount(snapTestDoc())
	d := snapTestDoc()
	snapDoc(d, 20)
	after := sparseCount(d)
	if before != 2 || after != 1 {
		t.Errorf("sparse entries %d before and %d after snapping, want 2 and 1", before, after)
	}
}

func TestGridRange(t *testing.T) {
	for _, g := range []string{"0", "31", "-1"} {
		src := string(goalpostSource(t, "output stadium {", "output stadium {\n  grid = "+g))
		_, ds := Build(lowerSource(t, []byte(src)), "")
		var found bool
		for _, d := range ds {
			if d.Code == diag.CodeFieldRange {
				found = true
			}
		}
		if !found {
			t.Errorf("grid = %s: no %s in %v", g, diag.CodeFieldRange, ds)
		}
	}
	src := string(goalpostSource(t, "output stadium {", "output stadium {\n  grid = 2.5"))
	if _, ds := Build(lowerSource(t, []byte(src)), ""); !diag.HasErrors(ds) {
		t.Error("grid = 2.5 is accepted")
	}
}

// With `grid = 20` the athlete's every dense float without bounds is a
// multiple of 2^-20, and two builds write the same bytes.
func TestAthleteGrid(t *testing.T) {
	requireFullSuite(t)
	const old = "file = \"athlete.glb\"\n  mounts = runtime"
	build := func() *Product {
		t.Helper()
		f := loadProjectWith(t, athletePath, old, old+"\n  grid = 20")
		p, _, ds := BuildWith(f, "match", Options{})
		if p == nil {
			t.Fatalf("build: %v", ds)
		}
		return p
	}
	p, q := build(), build()
	files := p.Outputs[0].Files
	for i, f := range files {
		if !bytes.Equal(f.GLB, q.Outputs[0].Files[i].GLB) {
			t.Fatalf("level %d differs between two builds", f.Level)
		}
		d, bin := f.GLB[20:20+int(binary.LittleEndian.Uint32(f.GLB[12:]))], 20+int(binary.LittleEndian.Uint32(f.GLB[12:]))+8
		var j struct {
			Accessors []struct {
				BufferView    *int
				ByteOffset    int
				ComponentType int
				Count         int
				Type          string
				Min, Max      json.RawMessage
				Sparse        json.RawMessage
			}
			BufferViews []struct{ ByteOffset, ByteStride int }
		}
		if err := json.Unmarshal(d, &j); err != nil {
			t.Fatal(err)
		}
		comps := map[string]int{"SCALAR": 1, "VEC2": 2, "VEC3": 3, "VEC4": 4, "MAT4": 16}
		checked := 0
		for _, a := range j.Accessors {
			if a.ComponentType != 5126 || a.BufferView == nil || a.Min != nil || a.Sparse != nil {
				continue
			}
			v := j.BufferViews[*a.BufferView]
			nc := comps[a.Type]
			stride := v.ByteStride
			if stride == 0 {
				stride = 4 * nc
			}
			for e := 0; e < a.Count; e++ {
				for c := 0; c < nc; c++ {
					at := bin + v.ByteOffset + a.ByteOffset + e*stride + 4*c
					x := math.Float32frombits(binary.LittleEndian.Uint32(f.GLB[at:]))
					if !isGridMultiple(x, 20) {
						t.Fatalf("level %d: %v is not a multiple of 2^-20", f.Level, x)
					}
					checked++
				}
			}
		}
		if checked == 0 {
			t.Errorf("level %d: no dense float accessor checked", f.Level)
		}
	}
	if !strings.Contains(string(files[0].GLB), "athlete") && len(files) == 0 {
		t.Fatal("no files")
	}
}
