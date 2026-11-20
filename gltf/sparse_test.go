package gltf

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

// sparseGoldenSHA256 is the SHA-256 of testdata/rigged-sparse.glb.
const sparseGoldenSHA256 = "7686f6cf23110adb4b560202c37813a702702d58c13198072a8ba1f336e00e94"

// sparseRigDoc is rigDoc with its first morph target (two of four vertices
// move) written sparse.
func sparseRigDoc() *Document {
	d := rigDoc()
	d.Meshes[0].Primitives[0].Targets[0].Sparse = true
	return d
}

func TestSparseGolden(t *testing.T) {
	b := mustEncode(t, sparseRigDoc())
	path := "testdata/rigged-sparse.glb"
	if *update {
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, want) {
		t.Fatalf("encoding differs from %s", path)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != sparseGoldenSHA256 {
		t.Fatalf("sha256 %s, want %s", got, sparseGoldenSHA256)
	}
}

func TestSparseRoundTrip(t *testing.T) {
	sp := sparseRigDoc()
	b1 := mustEncode(t, sp)
	d2 := mustDecode(t, b1)
	if !bytes.Equal(b1, mustEncode(t, d2)) {
		t.Fatal("Encode(Decode(Encode(d))) differs from Encode(d)")
	}
	if !d2.Meshes[0].Primitives[0].Targets[0].Sparse {
		t.Fatal("Sparse flag lost")
	}
	if d2.Meshes[0].Primitives[0].Targets[1].Sparse {
		t.Fatal("Sparse flag appeared on a dense target")
	}
	// Dense and sparse documents decode to the same data, apart from the flag.
	dense := mustDecode(t, mustEncode(t, rigDoc()))
	d2.Meshes[0].Primitives[0].Targets[0].Sparse = false
	d2.Nodes[3].Extras = dense.Nodes[3].Extras
	if !reflect.DeepEqual(dense, d2) {
		t.Fatalf("dense and sparse decode differ\n%+v\n%+v", dense, d2)
	}
	if bytes.Equal(b1, mustEncode(t, rigDoc())) {
		t.Fatal("sparse and dense files are equal")
	}
}

func TestSparseLayout(t *testing.T) {
	js, bin := parts(t, mustEncode(t, sparseRigDoc()))
	// Target 0 POSITION: moved vertices 2 and 3, ushort indices, no bufferView.
	if !strings.Contains(js, `"sparse":{"count":2,"indices":{"bufferView":`) {
		t.Fatalf("no sparse accessor in %s", js)
	}
	m := jsonOf(t, mustEncode(t, sparseRigDoc()))
	acc := m["accessors"].([]any)
	a := acc[6].(map[string]any)
	if _, ok := a["bufferView"]; ok {
		t.Fatalf("sparse accessor has a base bufferView: %v", a)
	}
	s := a["sparse"].(map[string]any)
	ix := s["indices"].(map[string]any)
	views := m["bufferViews"].([]any)
	iview := views[int(ix["bufferView"].(float64))].(map[string]any)
	vview := views[int(s["values"].(map[string]any)["bufferView"].(float64))].(map[string]any)
	for _, v := range []map[string]any{iview, vview} {
		if _, ok := v["target"]; ok {
			t.Fatalf("sparse bufferView has a target: %v", v)
		}
		if int(v["byteOffset"].(float64))%4 != 0 {
			t.Fatalf("sparse bufferView is not 4-aligned: %v", v)
		}
	}
	off := int(iview["byteOffset"].(float64))
	if ix["componentType"].(float64) != compUshort || s["count"].(float64) != 2 ||
		binary.LittleEndian.Uint16(bin[off:]) != 2 || binary.LittleEndian.Uint16(bin[off+2:]) != 3 {
		t.Fatalf("indices wrong: %v", s)
	}
	voff := int(vview["byteOffset"].(float64))
	if got := math.Float32frombits(binary.LittleEndian.Uint32(bin[voff+4:])); got != 0.5 {
		t.Fatalf("first value y = %v, want 0.5", got)
	}
	// min and max still describe the whole dense array.
	if a["max"].([]any)[1].(float64) != 0.5 || a["min"].([]any)[1].(float64) != 0 {
		t.Fatalf("min/max wrong: %v", a)
	}
}

func TestSparseAllZeroWritesDense(t *testing.T) {
	d := rigDoc()
	tg := &d.Meshes[0].Primitives[0].Targets[0]
	tg.Positions = make([]float32, 12)
	tg.Normals = make([]float32, 12)
	tg.Sparse = true
	b := mustEncode(t, d)
	if strings.Contains(string(b), `"sparse"`) {
		t.Fatal("an all-zero target was written sparse")
	}
	d.Meshes[0].Primitives[0].Targets[0].Sparse = false
	if !bytes.Equal(b, mustEncode(t, d)) {
		t.Fatal("all-zero sparse target differs from the dense encoding")
	}
}

// bigDoc has n vertices; the listed vertices move in target 0.
func bigDoc(n int, moved ...int) *Document {
	pos := make([]float32, 3*n)
	for i := range pos {
		pos[i] = float32(i % 7)
	}
	tp := make([]float32, 3*n)
	for _, v := range moved {
		tp[3*v+1] = 0.5
	}
	return &Document{
		Scenes: []Scene{{Nodes: []int{0}}},
		Nodes:  []Node{{Mesh: Int(0)}},
		Meshes: []Mesh{{Primitives: []Primitive{{
			Positions: pos,
			Targets:   []MorphTarget{{Positions: tp, Sparse: true}},
		}}}},
	}
}

func TestSparseIndexWidth(t *testing.T) {
	cases := []struct {
		n     int
		moved []int
		comp  float64
	}{
		{6, []int{0, 5}, compUshort},
		{70002, []int{3, 65535}, compUshort},
		{70002, []int{3, 65536}, compUint},
		{70002, []int{70001}, compUint},
	}
	for _, c := range cases {
		d := bigDoc(c.n, c.moved...)
		b := mustEncode(t, d)
		m := jsonOf(t, b)
		a := m["accessors"].([]any)[1].(map[string]any)
		got := a["sparse"].(map[string]any)["indices"].(map[string]any)["componentType"].(float64)
		if got != c.comp {
			t.Fatalf("n=%d moved=%v: index componentType %v, want %v", c.n, c.moved, got, c.comp)
		}
		d2 := mustDecode(t, b)
		d.Meshes[0].Primitives[0].Targets[0].Sparse = true
		if !reflect.DeepEqual(d.Meshes[0].Primitives[0].Targets, d2.Meshes[0].Primitives[0].Targets) {
			t.Fatalf("n=%d moved=%v: targets differ after a round trip", c.n, c.moved)
		}
	}
}

func TestSparseSavesAndAuto(t *testing.T) {
	d := bigDoc(300, 5)
	tg := d.Meshes[0].Primitives[0].Targets[0]
	dense, sparse := SparseSaves(tg.Positions, nil)
	if dense != 3600 || sparse != 4+12 {
		t.Fatalf("one moved vertex of 300: dense %d sparse %d", dense, sparse)
	}
	if dn, sp := SparseSaves(make([]float32, 30), nil); dn != 120 || sp != 120 {
		t.Fatalf("all zero: %d %d, want both 120", dn, sp)
	}
	if dn, sp := SparseSaves(nil, nil); dn != 0 || sp != 0 {
		t.Fatalf("empty: %d %d", dn, sp)
	}
	// Every vertex moves: sparse is larger.
	all := make([]float32, 30)
	for i := range all {
		all[i] = 1
	}
	if dn, sp := SparseSaves(all, nil); sp <= dn {
		t.Fatalf("all moved: dense %d sparse %d", dn, sp)
	}
	d.Meshes[0].Primitives[0].Targets[0].Sparse = false
	d.AutoSparse()
	if !d.Meshes[0].Primitives[0].Targets[0].Sparse {
		t.Fatal("AutoSparse did not choose sparse for one moved vertex")
	}
	p := &Primitive{Targets: []MorphTarget{{Positions: all, Sparse: true}}}
	p.AutoSparse()
	if p.Targets[0].Sparse {
		t.Fatal("AutoSparse kept sparse when every vertex moves")
	}
	// The estimate matches the bytes written for the target's views.
	b := mustEncode(t, bigDoc(300, 5, 6))
	_, bin := parts(t, b)
	d1 := mustEncode(t, func() *Document {
		x := bigDoc(300, 5, 6)
		x.Meshes[0].Primitives[0].Targets[0].Sparse = false
		return x
	}())
	_, bin1 := parts(t, d1)
	_, est := SparseSaves(bigDoc(300, 5, 6).Meshes[0].Primitives[0].Targets[0].Positions, nil)
	if len(bin1)-len(bin) != 3600-est {
		t.Fatalf("binary shrank by %d, estimate says %d", len(bin1)-len(bin), 3600-est)
	}
}

// sparseTemplate is a hand-built file: a triangle whose target POSITION is a
// sparse accessor over a base bufferView (vertex 1 and 2 replaced) and whose
// NORMAL is sparse over zeros.
//
//	bufferView 0: positions (36 bytes)
//	bufferView 1: base for target POSITION, 3 vertices (36 bytes)
//	bufferView 2: indices uint16 {1,2}, padded to 4
//	bufferView 3: values, 2 vertices (24 bytes)
//	bufferView 4: indices uint8 {0}, padded to 4
//	bufferView 5: values, 1 vertex (12 bytes)
const sparseJSONText = `{"asset":{"version":"2.0"},"meshes":[{"primitives":[{"attributes":{"POSITION":0},` +
	`"targets":[{"POSITION":1,"NORMAL":2}]}]}],"buffers":[{"byteLength":120}],` +
	`"bufferViews":[{"buffer":0,"byteLength":36},{"buffer":0,"byteOffset":36,"byteLength":36},` +
	`{"buffer":0,"byteOffset":72,"byteLength":4},{"buffer":0,"byteOffset":76,"byteLength":24},` +
	`{"buffer":0,"byteOffset":100,"byteLength":1},{"buffer":0,"byteOffset":104,"byteLength":12}],` +
	`"accessors":[{"bufferView":0,"componentType":5126,"count":3,"type":"VEC3"},` +
	`{"bufferView":1,"componentType":5126,"count":3,"type":"VEC3","sparse":{"count":2,"indices":{"bufferView":2,"componentType":5123},"values":{"bufferView":3}}},` +
	`{"componentType":5126,"count":3,"type":"VEC3","sparse":{"count":1,"indices":{"bufferView":4,"componentType":5121},"values":{"bufferView":5}}}]}`

func sparseBin() []byte {
	b := f32bytes(0, 0, 0, 1, 0, 0, 0, 1, 0) // positions
	b = append(b, f32bytes(1, 1, 1, 2, 2, 2, 3, 3, 3)...)
	b = append(b, 1, 0, 2, 0)
	b = append(b, f32bytes(9, 9, 9, 8, 8, 8)...)
	b = append(b, 0, 0, 0, 0)
	b = append(b, f32bytes(0, 0, 7)...)
	b = append(b, 0, 0, 0, 0) // pad to 120 (guard bytes)
	return b[:120]
}

func TestSparseHandBuilt(t *testing.T) {
	d, err := Decode(mkGLB(sparseJSONText, sparseBin()))
	if err != nil {
		t.Fatal(err)
	}
	tg := d.Meshes[0].Primitives[0].Targets[0]
	if !tg.Sparse {
		t.Fatal("Sparse flag not set")
	}
	if want := []float32{1, 1, 1, 9, 9, 9, 8, 8, 8}; !reflect.DeepEqual(tg.Positions, want) {
		t.Fatalf("positions %v, want %v", tg.Positions, want)
	}
	if want := []float32{0, 0, 7, 0, 0, 0, 0, 0, 0}; !reflect.DeepEqual(tg.Normals, want) {
		t.Fatalf("normals %v, want %v", tg.Normals, want)
	}
	// Re-encoding is stable.
	b1 := mustEncode(t, d)
	if !bytes.Equal(b1, mustEncode(t, mustDecode(t, b1))) {
		t.Fatal("re-encoding is not stable")
	}
}

func TestMalformedSparse(t *testing.T) {
	bin := sparseBin()
	withJSON := func(from, to string) []byte {
		if strings.Count(sparseJSONText, from) != 1 {
			t.Fatalf("template has %d copies of %q", strings.Count(sparseJSONText, from), from)
		}
		return mkGLB(strings.Replace(sparseJSONText, from, to, 1), bin)
	}
	withBin := func(off int, data ...byte) []byte {
		b := append([]byte(nil), bin...)
		copy(b[off:], data)
		return mkGLB(sparseJSONText, b)
	}
	const tp = "meshes[0].primitives[0].targets[0]."
	cases := []struct {
		name, path string
		in         []byte
	}{
		{"indices unsorted", tp + "POSITION", withBin(72, 2, 0, 1, 0)},
		{"indices duplicate", tp + "POSITION", withBin(72, 1, 0, 1, 0)},
		{"index out of range", tp + "POSITION", withBin(72, 1, 0, 3, 0)},
		{"index high byte", tp + "POSITION", withBin(72, 1, 0, 2, 1)},
		{"count too large", tp + "POSITION", withJSON(`"sparse":{"count":2,`, `"sparse":{"count":4,`)},
		{"count zero", tp + "POSITION", withJSON(`"sparse":{"count":2,`, `"sparse":{"count":0,`)},
		{"count missing", tp + "POSITION", withJSON(`"sparse":{"count":2,`, `"sparse":{`)},
		{"count not a number", tp + "POSITION", withJSON(`"sparse":{"count":2,`, `"sparse":{"count":"2",`)},
		{"values short", tp + "POSITION", withJSON(`"byteOffset":76,"byteLength":24`, `"byteOffset":76,"byteLength":20`)},
		{"indices short", tp + "POSITION", withJSON(`"byteOffset":72,"byteLength":4`, `"byteOffset":72,"byteLength":2`)},
		{"values past buffer", tp + "POSITION", withJSON(`"values":{"bufferView":3}`, `"values":{"bufferView":3,"byteOffset":20}`)},
		{"index type float", tp + "POSITION", withJSON(`"indices":{"bufferView":2,"componentType":5123}`, `"indices":{"bufferView":2,"componentType":5126}`)},
		{"index type signed", tp + "POSITION", withJSON(`"indices":{"bufferView":2,"componentType":5123}`, `"indices":{"bufferView":2,"componentType":5122}`)},
		{"indices view missing", tp + "POSITION", withJSON(`"indices":{"bufferView":2,"componentType":5123}`, `"indices":{"componentType":5123}`)},
		{"indices view range", tp + "POSITION", withJSON(`"indices":{"bufferView":2,"componentType":5123}`, `"indices":{"bufferView":22,"componentType":5123}`)},
		{"values view missing", tp + "POSITION", withJSON(`"values":{"bufferView":3}`, `"values":{}`)},
		{"values view range", tp + "POSITION", withJSON(`"values":{"bufferView":3}`, `"values":{"bufferView":33}`)},
		{"indices object missing", tp + "POSITION", withJSON(`"indices":{"bufferView":2,"componentType":5123},`, ``)},
		{"values object missing", tp + "POSITION", withJSON(`,"values":{"bufferView":3}`, ``)},
		{"indices misaligned", tp + "POSITION", withJSON(`"indices":{"bufferView":2,"componentType":5123}`, `"indices":{"bufferView":2,"byteOffset":1,"componentType":5123}`)},
		{"value nan", tp + "POSITION", withBin(76, 0, 0, 0xC0, 0x7F)},
		{"normal sparse over nothing: values view", tp + "NORMAL", withJSON(`"values":{"bufferView":5}`, `"values":{"bufferView":55}`)},
		{"normal index type", tp + "NORMAL", withJSON(`"indices":{"bufferView":4,"componentType":5121}`, `"indices":{"bufferView":4,"componentType":5120}`)},
		{"count above base", tp + "POSITION", withJSON(`{"bufferView":1,"componentType":5126,"count":3,`, `{"bufferView":1,"componentType":5126,"count":4,`)},
		{"sparse on base POSITION", "meshes[0].primitives[0].attributes.POSITION", withJSON(`{"bufferView":0,"componentType":5126,"count":3,"type":"VEC3"}`,
			`{"bufferView":0,"componentType":5126,"count":3,"type":"VEC3","sparse":{"count":1,"indices":{"bufferView":2,"componentType":5123},"values":{"bufferView":3}}}`)},
	}
	if len(cases) < 20 {
		t.Fatal("need more malformed sparse files")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := Decode(c.in)
			if err == nil {
				t.Fatalf("accepted; got %+v", d)
			}
			e, ok := err.(*Error)
			if !ok {
				t.Fatalf("error is %T", err)
			}
			if !strings.HasPrefix(e.Path, c.path) {
				t.Fatalf("path %q msg %q, want prefix %q", e.Path, e.Msg, c.path)
			}
		})
	}
	if _, err := Decode(mkGLB(sparseJSONText, bin)); err != nil {
		t.Fatalf("template itself must decode: %v", err)
	}
}
