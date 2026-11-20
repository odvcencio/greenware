package glb_test

import (
	"math"
	"testing"

	"m31labs.dev/greenware/conformance/glb"
	"m31labs.dev/greenware/conformance/internal/fixture"
)

func decodeSparse(t *testing.T, o fixture.SparseOpts, edit func(doc map[string]any)) (*glb.File, []glb.Finding) {
	t.Helper()
	b := fixture.New()
	b.Sparse(o)
	doc, bin := b.Parts()
	if edit != nil {
		edit(doc)
	}
	f, fs := glb.Decode(fixture.Pack(doc, bin))
	noInternal(t, fs)
	return f, fs
}

func floatsEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Index widths 5121, 5123 and 5125 all decode, with and without a base view.
func TestSparseIndexWidthsAndBase(t *testing.T) {
	for _, ct := range []int{5121, 5123, 5125} {
		// no base view: the base is zeros
		f, fs := decodeSparse(t, fixture.SparseOpts{Count: 4, Indices: []uint32{1, 3}, Values: []float32{1, 2, 3, 4, 5, 6}, IndexType: ct, MinMax: true}, nil)
		if len(fs) != 0 {
			t.Fatalf("type %d: findings %v", ct, fs)
		}
		v, _, ok := f.Floats(0)
		want := []float64{0, 0, 0, 1, 2, 3, 0, 0, 0, 4, 5, 6}
		if !ok || !floatsEqual(v, want) {
			t.Fatalf("type %d: %v ok=%v, want %v", ct, v, ok, want)
		}
		// with a base view: sparse values replace the base values at their
		// indices (they are not added to them)
		base := []float32{9, 9, 9, 8, 8, 8, 7, 7, 7, 6, 6, 6}
		f, fs = decodeSparse(t, fixture.SparseOpts{Count: 4, Base: base, Indices: []uint32{0, 2}, Values: []float32{1, 1, 1, 2, 2, 2}, IndexType: ct, MinMax: true}, nil)
		if len(fs) != 0 {
			t.Fatalf("type %d with base: findings %v", ct, fs)
		}
		v, _, _ = f.Floats(0)
		want = []float64{1, 1, 1, 8, 8, 8, 2, 2, 2, 6, 6, 6}
		if !floatsEqual(v, want) {
			t.Fatalf("type %d with base: %v, want %v", ct, v, want)
		}
	}
}

// sparse.count equal to the accessor count is allowed and replaces every element.
func TestSparseCountEqualsAccessorCount(t *testing.T) {
	f, fs := decodeSparse(t, fixture.SparseOpts{Type: "SCALAR", Count: 3, Base: []float32{1, 1, 1}, Indices: []uint32{0, 1, 2}, Values: []float32{4, 5, 6}, MinMax: true}, nil)
	if len(fs) != 0 {
		t.Fatalf("findings %v", fs)
	}
	if v, _, _ := f.Floats(0); !floatsEqual(v, []float64{4, 5, 6}) {
		t.Fatalf("got %v", v)
	}
}

// A sparse POSITION with no base view is valid on a primitive attribute. The
// min and max must cover the applied values.
func TestSparseZeroBasePositionOnPrimitive(t *testing.T) {
	build := func(o fixture.SparseOpts) []byte {
		b := fixture.New()
		pos := b.Sparse(o)
		idx := b.Indices16([]uint32{0, 1, 2, 0, 2, 3})
		m := b.RawMesh("quad", map[string]int{"POSITION": pos}, idx, nil, nil, nil)
		b.Scene(b.Node("quad", m, [3]float64{}))
		return b.Bytes()
	}
	o := fixture.SparseOpts{Count: 4, Indices: []uint32{1, 2, 3}, Values: []float32{1, 0, 0, 1, 1, 0, 0, 1, 0}, IndexType: 5121, MinMax: true}
	f, fs := glb.Decode(build(o))
	if len(fs) != 0 {
		t.Fatalf("findings %v", fs)
	}
	v, _, ok := f.Floats(0)
	if !ok || !floatsEqual(v, []float64{0, 0, 0, 1, 0, 0, 1, 1, 0, 0, 1, 0}) {
		t.Fatalf("positions %v", v)
	}
	// min/max written for the base alone (all zeros) do not cover the sparse values
	b := fixture.New()
	pos := b.Sparse(o)
	idx := b.Indices16([]uint32{0, 1, 2, 0, 2, 3})
	m := b.RawMesh("quad", map[string]int{"POSITION": pos}, idx, nil, nil, nil)
	b.Scene(b.Node("quad", m, [3]float64{}))
	doc, bin := b.Parts()
	acc := doc["accessors"].([]any)[pos].(map[string]any)
	acc["max"] = []float64{0, 0, 0}
	_, fs = glb.Decode(fixture.Pack(doc, bin))
	if len(fs) != 2 || fs[0].Code != "ACC-MAX" || fs[1].Code != "ACC-MAX" {
		t.Fatalf("want the x and y maxima reported (z max 0 is right): %v", fs)
	}
	if fs[0].Path != "/accessors/0/max/0" || fs[1].Path != "/accessors/0/max/1" {
		t.Fatalf("paths %s, %s", fs[0].Path, fs[1].Path)
	}
	// no min and max at all
	o.MinMax = false
	if _, fs = glb.Decode(build(o)); len(fs) != 1 || fs[0].Code != "POSITION-MINMAX" {
		t.Fatalf("got %v", fs)
	}
}

// A sparse value that is NaN, and a sparse normal of zero length, are found in
// the applied data.
func TestSparseAppliedValuesAreChecked(t *testing.T) {
	nan := float32(math.NaN())
	_, fs := decodeSparse(t, fixture.SparseOpts{Count: 3, Indices: []uint32{1}, Values: []float32{0, nan, 0}}, nil)
	if len(fs) != 1 || fs[0].Code != "ACC-NONFINITE" {
		t.Fatalf("got %v", fs)
	}
	// NORMAL: base normals of length 1, one sparse normal replaced by zero
	b := fixture.New()
	pos := b.Vec3s([][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}, true)
	nrm := b.Sparse(fixture.SparseOpts{Count: 3, Base: []float32{0, 0, 1, 0, 0, 1, 0, 0, 1}, Indices: []uint32{2}, Values: []float32{0, 0, 0}})
	m := b.RawMesh("tri", map[string]int{"POSITION": pos, "NORMAL": nrm}, b.Indices16([]uint32{0, 1, 2}), nil, nil, nil)
	b.Scene(b.Node("tri", m, [3]float64{}))
	if _, fs = glb.Decode(b.Bytes()); len(fs) != 1 || fs[0].Code != "NORMAL-ZERO" {
		t.Fatalf("got %v", fs)
	}
}

// Each malformed block gives exactly one finding with the expected code and
// path, and no fault.
func TestSparseMalformedGivesOneFinding(t *testing.T) {
	zero, three, minusOne := 0, 3, -1
	good := fixture.SparseOpts{Count: 4, Indices: []uint32{1, 3}, Values: []float32{1, 2, 3, 4, 5, 6}}
	with := func(f func(o *fixture.SparseOpts)) fixture.SparseOpts {
		o := good
		f(&o)
		return o
	}
	cases := []struct {
		name string
		o    fixture.SparseOpts
		edit func(doc map[string]any)
		code string
		path string
	}{
		{"unsorted", with(func(o *fixture.SparseOpts) { o.Indices = []uint32{3, 1} }), nil, "SPARSE-INDEX", "/accessors/0/sparse/indices/1"},
		{"duplicate", with(func(o *fixture.SparseOpts) { o.Indices = []uint32{2, 2} }), nil, "SPARSE-INDEX", "/accessors/0/sparse/indices/1"},
		{"out of range", with(func(o *fixture.SparseOpts) { o.Indices = []uint32{1, 4} }), nil, "SPARSE-INDEX", "/accessors/0/sparse/indices/1"},
		{"out of range, first", with(func(o *fixture.SparseOpts) { o.Indices = []uint32{200, 201}; o.IndexType = 5121 }), nil, "SPARSE-INDEX", "/accessors/0/sparse/indices/0"},
		{"count 0", with(func(o *fixture.SparseOpts) { o.SparseCount = &zero }), nil, "SPARSE-COUNT", "/accessors/0/sparse/count"},
		{"count negative", with(func(o *fixture.SparseOpts) { o.SparseCount = &minusOne }), nil, "SPARSE-COUNT", "/accessors/0/sparse/count"},
		{"count above accessor count", with(func(o *fixture.SparseOpts) {
			o.Count = 2
			o.Indices = []uint32{0, 1, 2}
			o.Values = make([]float32, 9)
			o.SparseCount = &three
		}), nil, "SPARSE-COUNT", "/accessors/0/sparse/count"},
		{"signed short indices", with(func(o *fixture.SparseOpts) { o.IndexType = 5122 }), nil, "SPARSE-INDEX-TYPE", "/accessors/0/sparse/indices/componentType"},
		{"float indices", with(func(o *fixture.SparseOpts) { o.IndexType = 5126 }), nil, "SPARSE-INDEX-TYPE", "/accessors/0/sparse/indices/componentType"},
		{"values too short", with(func(o *fixture.SparseOpts) { o.DropValueBytes = 4 }), nil, "SPARSE-BOUNDS", "/accessors/0/sparse/values"},
		{"indices too short", with(func(o *fixture.SparseOpts) { o.IndexBytes = []byte{1, 0, 3} }), nil, "SPARSE-BOUNDS", "/accessors/0/sparse/indices"},
		{"index offset misaligned", with(func(o *fixture.SparseOpts) { o.IndexOffset = 1 }), nil, "SPARSE-ALIGN", "/accessors/0/sparse/indices"},
		{"values offset misaligned", with(func(o *fixture.SparseOpts) { o.ValuesOffset = 2 }), nil, "SPARSE-ALIGN", "/accessors/0/sparse/values"},
		{"index view has a target", with(func(o *fixture.SparseOpts) { o.ViewTarget = 34962 }), nil, "SPARSE-VIEW-USE", "/accessors/0/sparse/indices/bufferView"},
		{"indices view out of range", good, func(doc map[string]any) {
			doc["accessors"].([]any)[0].(map[string]any)["sparse"].(map[string]any)["indices"].(map[string]any)["bufferView"] = 99
		}, "SPARSE-VIEW", "/accessors/0/sparse/indices/bufferView"},
		{"values view out of range", good, func(doc map[string]any) {
			doc["accessors"].([]any)[0].(map[string]any)["sparse"].(map[string]any)["values"].(map[string]any)["bufferView"] = 99
		}, "SPARSE-VIEW", "/accessors/0/sparse/values/bufferView"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, fs := decodeSparse(t, c.o, c.edit)
			if len(fs) != 1 || fs[0].Code != c.code || fs[0].Path != c.path {
				t.Fatalf("want one %s at %s; got %v", c.code, c.path, fs)
			}
			if f != nil {
				if _, _, ok := f.Floats(0); ok {
					t.Fatalf("a malformed sparse accessor must not read")
				}
			}
		})
	}
}

// The well-formed baseline of the table above is clean.
func TestSparseBaselineIsClean(t *testing.T) {
	_, fs := decodeSparse(t, fixture.SparseOpts{Count: 4, Indices: []uint32{1, 3}, Values: []float32{1, 2, 3, 4, 5, 6}}, nil)
	if len(fs) != 0 {
		t.Fatalf("%v", fs)
	}
}

// Offsets that are aligned and nonzero decode.
func TestSparseAlignedOffsetsDecode(t *testing.T) {
	f, fs := decodeSparse(t, fixture.SparseOpts{Count: 4, Indices: []uint32{1, 3}, Values: []float32{1, 2, 3, 4, 5, 6}, IndexOffset: 2, ValuesOffset: 4}, nil)
	if len(fs) != 0 {
		t.Fatalf("%v", fs)
	}
	v, _, _ := f.Floats(0)
	if !floatsEqual(v, []float64{0, 0, 0, 1, 2, 3, 0, 0, 0, 4, 5, 6}) {
		t.Fatalf("%v", v)
	}
}
