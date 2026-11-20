package gltf

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/triangle.glb")

// goldenSHA256 is the SHA-256 of testdata/triangle.glb.
const goldenSHA256 = "0a08571d065f2ec689667e2b49a2b538eb0320ed963b192d4f655527c228e7ea"

func triangleDoc() *Document {
	return &Document{
		Asset:  Asset{Generator: "gltf codec test"},
		Scene:  Int(0),
		Scenes: []Scene{{Nodes: []int{0}}},
		Nodes:  []Node{{Name: "tri", Mesh: Int(0)}},
		Meshes: []Mesh{{Name: "tri", Primitives: []Primitive{{
			Positions: []float32{0, 0, 0, 1, 0, 0, 0, 1, 0},
			Normals:   []float32{0, 0, 1, 0, 0, 1, 0, 0, 1},
			UV0:       []float32{0, 0, 1, 0, 0, 1},
			Indices:   []uint32{0, 1, 2},
		}}}},
	}
}

func richDoc() *Document {
	return &Document{
		Asset:  Asset{Generator: "g", Copyright: "c <&> é"},
		Scene:  Int(0),
		Scenes: []Scene{{Name: "s", Nodes: []int{0, 3}, Extras: json.RawMessage(`{"a":1}`)}},
		Nodes: []Node{
			{Name: "root", Children: []int{1, 2}, Translation: &[3]float32{1, 2, 3}, Rotation: &[4]float32{0, 0, 0, 1}, Scale: &[3]float32{1, 2, 1},
				Extras: json.RawMessage(`{"b":[1,2.50,"x"],"a":null}`), Extensions: map[string]json.RawMessage{"KHR_z": json.RawMessage(`{"k":1}`), "EXT_a": json.RawMessage(`true`)}},
			{Name: "m0", Mesh: Int(0)},
			{Name: "m1", Mesh: Int(1)},
			{Name: "lone"},
		},
		Meshes: []Mesh{
			{Name: "a", Primitives: []Primitive{{
				Positions: []float32{0, 0, 0, 1, 0, 0, 0, 1, 0, 1, 1, 0},
				Normals:   []float32{0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1},
				UV0:       []float32{0, 0, 1, 0, 0, 1, 1, 1},
				Indices:   []uint32{0, 1, 2, 2, 1, 3},
				Material:  Int(0),
				Extras:    json.RawMessage(`{"p":true}`),
			}}, Extras: json.RawMessage(`[1,2]`)},
			{Name: "b", Primitives: []Primitive{{Positions: []float32{-1.5, 2, 3, 4, -5, 6.25, 7, 8, -9}}}},
		},
		Materials: []Material{{Name: "mat", BaseColor: [4]float32{0.1, 0.2, 0.3, 1}, Metallic: 0, Roughness: 0.5,
			BaseColorTexture: Int(0), Extras: json.RawMessage(`{"m":1}`),
			Extensions: map[string]json.RawMessage{"KHR_materials_unlit": json.RawMessage(`{}`)}}},
		Textures: []Texture{{Name: "t", Sampler: Int(0), Source: Int(0)}},
		Images:   []Image{{Name: "i", MimeType: "image/png", Data: []byte{1, 2, 3, 4, 5}}, {Name: "u", URI: "x.png"}},
		Samplers: []Sampler{{MagFilter: 9729, MinFilter: 9987, WrapS: 10497}},
		Extras:   json.RawMessage(`{"z":1,"y":{"c":2,"b":3}}`),
	}
}

func mustEncode(t *testing.T, d *Document) []byte {
	t.Helper()
	b, err := Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// canon returns d with extras and extensions in canonical form, which is the
// form Decode returns.
func canon(t *testing.T, d *Document) *Document {
	t.Helper()
	return mustDecode(t, mustEncode(t, d))
}

func mustDecode(t *testing.T, b []byte) *Document {
	t.Helper()
	d, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRoundTrip(t *testing.T) {
	for name, d := range map[string]*Document{"triangle": triangleDoc(), "rich": richDoc(), "empty": {}} {
		t.Run(name, func(t *testing.T) {
			b1 := mustEncode(t, d)
			d2 := mustDecode(t, b1)
			b2 := mustEncode(t, d2)
			if !bytes.Equal(b1, b2) {
				t.Fatal("Encode(Decode(Encode(d))) differs from Encode(d)")
			}
			d3 := mustDecode(t, b2)
			if !reflect.DeepEqual(d2, d3) {
				t.Fatal("decoded documents differ")
			}
			if name == "triangle" && !reflect.DeepEqual(d, d2) {
				t.Fatalf("Decode(Encode(d)) != d\n%+v\n%+v", d, d2)
			}
		})
	}
}

func TestRoundTripRichFields(t *testing.T) {
	d := richDoc()
	got := canon(t, d)
	// Extras differ only in form, so compare the rest exactly.
	if string(got.Extras) != `{"y":{"b":3,"c":2},"z":1}` {
		t.Fatalf("document extras: %s", got.Extras)
	}
	if string(got.Nodes[0].Extras) != `{"a":null,"b":[1,2.50,"x"]}` {
		t.Fatalf("node extras: %s", got.Nodes[0].Extras)
	}
	d2 := richDoc()
	d2.Extras, d2.Nodes[0].Extras = got.Extras, got.Nodes[0].Extras
	if !reflect.DeepEqual(got, d2) {
		t.Fatalf("rich document changed in the round trip\n%+v\n%+v", got, d2)
	}
	if !bytes.Contains(mustEncode(t, d), []byte(`"extensionsUsed":["EXT_a","KHR_materials_unlit","KHR_z"]`)) {
		t.Fatal("extensionsUsed missing or unsorted")
	}
}

func TestDeterministic(t *testing.T) {
	a := mustEncode(t, richDoc())
	for i := 0; i < 20; i++ {
		if !bytes.Equal(a, mustEncode(t, richDoc())) {
			t.Fatal("encoding differs between runs")
		}
	}
}

func TestGolden(t *testing.T) {
	b := mustEncode(t, triangleDoc())
	path := "testdata/triangle.glb"
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
	if got := hex.EncodeToString(sum[:]); got != goldenSHA256 {
		t.Fatalf("sha256 %s, want %s", got, goldenSHA256)
	}
	if len(b)%4 != 0 {
		t.Fatal("GLB length not a multiple of 4")
	}
}

func TestLayout(t *testing.T) {
	b := mustEncode(t, triangleDoc())
	jl := binary.LittleEndian.Uint32(b[12:])
	js := b[20 : 20+jl]
	if js[len(js)-1] != ' ' && js[len(js)-1] != '}' {
		t.Fatal("JSON chunk padding must be spaces")
	}
	if !json.Valid(js) {
		t.Fatal("JSON chunk is not valid JSON")
	}
	want := `{"asset":{"generator":"gltf codec test","version":"2.0"},"scene":0,"scenes":[{"nodes":[0]}],"nodes":[{"name":"tri","mesh":0}],` +
		`"meshes":[{"name":"tri","primitives":[{"attributes":{"POSITION":0,"NORMAL":1,"TEXCOORD_0":2},"indices":3}]}],` +
		`"buffers":[{"byteLength":80}],` +
		`"bufferViews":[{"buffer":0,"byteOffset":0,"byteLength":36,"target":34962},{"buffer":0,"byteOffset":36,"byteLength":36,"target":34962},` +
		`{"buffer":0,"byteOffset":72,"byteLength":24,"target":34962},{"buffer":0,"byteOffset":96,"byteLength":6,"target":34963}],` +
		`"accessors":[{"bufferView":0,"componentType":5126,"count":3,"type":"VEC3","min":[0,0,0],"max":[1,1,0]},` +
		`{"bufferView":1,"componentType":5126,"count":3,"type":"VEC3"},{"bufferView":2,"componentType":5126,"count":3,"type":"VEC2"},` +
		`{"bufferView":3,"componentType":5123,"count":3,"type":"SCALAR"}]}`
	_ = want
	got := strings.TrimRight(string(js), " ")
	// buffers length is 104 (6 bytes of indices padded to 8).
	want = strings.Replace(want, `"byteLength":80`, `"byteLength":104`, 1)
	if got != want {
		t.Fatalf("JSON differs\n got %s\nwant %s", got, want)
	}
}

func TestNegativeZeroAndErrors(t *testing.T) {
	d := triangleDoc()
	d.Meshes[0].Primitives[0].Positions[0] = float32(math.Copysign(0, -1))
	d.Nodes[0].Translation = &[3]float32{float32(math.Copysign(0, -1)), 0, 0}
	b1 := mustEncode(t, d)
	d.Meshes[0].Primitives[0].Positions[0] = 0
	d.Nodes[0].Translation = &[3]float32{0, 0, 0}
	if !bytes.Equal(b1, mustEncode(t, d)) {
		t.Fatal("-0 and 0 encode differently")
	}
	for name, mod := range map[string]func(*Document){
		"nan":       func(d *Document) { d.Meshes[0].Primitives[0].Positions[1] = float32(math.NaN()) },
		"inf":       func(d *Document) { d.Meshes[0].Primitives[0].Normals[2] = float32(math.Inf(1)) },
		"scale inf": func(d *Document) { d.Nodes[0].Scale = &[3]float32{float32(math.Inf(-1)), 1, 1} },
		"color nan": func(d *Document) { d.Materials = []Material{{BaseColor: [4]float32{float32(math.NaN())}}} },
		"index":     func(d *Document) { d.Meshes[0].Primitives[0].Indices[2] = 3 },
		"uv length": func(d *Document) { d.Meshes[0].Primitives[0].UV0 = d.Meshes[0].Primitives[0].UV0[:4] },
		"cycle":     func(d *Document) { d.Nodes = append(d.Nodes, Node{Children: []int{0}}); d.Nodes[0].Children = []int{1} },
		"two parents": func(d *Document) {
			d.Nodes = append(d.Nodes, Node{}, Node{Children: []int{1}})
			d.Nodes[0].Children = []int{1}
		},
		"bad mesh":   func(d *Document) { d.Nodes[0].Mesh = Int(4) },
		"bad extras": func(d *Document) { d.Extras = json.RawMessage(`{"a":`) },
		"image":      func(d *Document) { d.Images = []Image{{}} },
	} {
		d := triangleDoc()
		mod(d)
		if _, err := Encode(d); err == nil {
			t.Errorf("%s: Encode accepted the document", name)
		} else if _, ok := err.(*Error); !ok {
			t.Errorf("%s: error is %T, want *Error", name, err)
		}
	}
	if _, err := Encode(nil); err == nil {
		t.Error("Encode(nil) succeeded")
	}
}

func TestIndexWidth(t *testing.T) {
	mk := func(nv int, idx []uint32) *Document {
		return &Document{Meshes: []Mesh{{Primitives: []Primitive{{Positions: make([]float32, 3*nv), Indices: idx}}}}}
	}
	small := mustEncode(t, mk(70000, []uint32{0, 1, 65534}))
	if !bytes.Contains(small, []byte(`"componentType":5123`)) {
		t.Fatal("max index 65534 should use uint16")
	}
	big := mustEncode(t, mk(70000, []uint32{0, 1, 65535}))
	if !bytes.Contains(big, []byte(`"componentType":5125`)) {
		t.Fatal("max index 65535 should use uint32")
	}
	d := mustDecode(t, big)
	if !reflect.DeepEqual(d.Meshes[0].Primitives[0].Indices, []uint32{0, 1, 65535}) {
		t.Fatal("uint32 indices lost")
	}
}

func TestPositionMinMax(t *testing.T) {
	d := &Document{Meshes: []Mesh{{Primitives: []Primitive{{Positions: []float32{1.1, -2.2, 3.3, -4.4, 5.5, float32(math.Copysign(0, -1)), 0.1, 0.2, -6.6}}}}}}
	js := string(mustEncode(t, d))
	if !strings.Contains(js, `"min":[-4.4,-2.2,-6.6],"max":[1.1,5.5,3.3]`) {
		t.Fatalf("min/max wrong: %s", js)
	}
}

// ---- hand-built files ----

func mkGLB(js string, bin []byte) []byte {
	j := []byte(js)
	for len(j)%4 != 0 {
		j = append(j, ' ')
	}
	b := make([]byte, 0, 32+len(j)+len(bin))
	b = binary.LittleEndian.AppendUint32(b, glbMagic)
	b = binary.LittleEndian.AppendUint32(b, 2)
	total := 12 + 8 + len(j)
	if bin != nil {
		total += 8 + len(bin)
	}
	b = binary.LittleEndian.AppendUint32(b, uint32(total))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(j)))
	b = binary.LittleEndian.AppendUint32(b, chunkJSON)
	b = append(b, j...)
	if bin != nil {
		b = binary.LittleEndian.AppendUint32(b, uint32(len(bin)))
		b = binary.LittleEndian.AppendUint32(b, chunkBIN)
		b = append(b, bin...)
	}
	return b
}

func f32bytes(fs ...float32) []byte {
	var b []byte
	for _, f := range fs {
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(f))
	}
	return b
}

// tri is a template with one triangle: 36 bytes of positions then 3 uint16
// indices (padded to 8 bytes).
const triJSON = `{"asset":{"version":"2.0"},"meshes":[{"primitives":[{"attributes":{"POSITION":0},"indices":1}]}],` +
	`"buffers":[{"byteLength":44}],"bufferViews":[{"buffer":0,"byteLength":36},{"buffer":0,"byteOffset":36,"byteLength":6}],` +
	`"accessors":[{"bufferView":0,"componentType":5126,"count":3,"type":"VEC3"},{"bufferView":1,"componentType":5123,"count":3,"type":"SCALAR"}]}`

func triBin() []byte {
	b := f32bytes(0, 0, 0, 1, 0, 0, 0, 1, 0)
	b = append(b, 0, 0, 1, 0, 2, 0, 0, 0)
	return b
}

func TestHandBuiltValid(t *testing.T) {
	d := mustDecode(t, mkGLB(triJSON, triBin()))
	p := d.Meshes[0].Primitives[0]
	if !reflect.DeepEqual(p.Indices, []uint32{0, 1, 2}) || len(p.Positions) != 9 || p.Positions[3] != 1 {
		t.Fatalf("decoded %+v", p)
	}
}

func TestIndexComponentTypesAndStride(t *testing.T) {
	// Positions interleaved with 4 bytes of padding (stride 16); ubyte indices.
	bin := []byte{}
	for _, v := range [][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}} {
		bin = append(bin, f32bytes(v[0], v[1], v[2])...)
		bin = append(bin, 9, 9, 9, 9)
	}
	bin = append(bin, 0, 1, 2, 0) // 48..52 ubyte indices
	js := `{"asset":{"version":"2.0"},"meshes":[{"primitives":[{"attributes":{"POSITION":0},"indices":1}]}],` +
		`"buffers":[{"byteLength":52}],"bufferViews":[{"buffer":0,"byteLength":48,"byteStride":16},{"buffer":0,"byteOffset":48,"byteLength":4}],` +
		`"accessors":[{"bufferView":0,"componentType":5126,"count":3,"type":"VEC3"},{"bufferView":1,"componentType":5121,"count":3,"type":"SCALAR"}]}`
	d := mustDecode(t, mkGLB(js, bin))
	p := d.Meshes[0].Primitives[0]
	if !reflect.DeepEqual(p.Positions, []float32{0, 0, 0, 1, 0, 0, 0, 1, 0}) || !reflect.DeepEqual(p.Indices, []uint32{0, 1, 2}) {
		t.Fatalf("decoded %+v", p)
	}
	// uint32 indices
	bin32 := append(f32bytes(0, 0, 0, 1, 0, 0, 0, 1, 0), 0, 0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0)
	js32 := strings.NewReplacer(`"byteLength":44`, `"byteLength":48`, `"byteLength":6`, `"byteLength":12`, "5123", "5125").Replace(triJSON)
	d = mustDecode(t, mkGLB(js32, bin32))
	if !reflect.DeepEqual(d.Meshes[0].Primitives[0].Indices, []uint32{0, 1, 2}) {
		t.Fatal("uint32 indices")
	}
}

func TestMatrixDecomposed(t *testing.T) {
	// Rotation of 90 degrees about z, scale (2,3,4), translation (5,6,7).
	js := `{"asset":{"version":"2.0"},"nodes":[{"matrix":[0,2,0,0,-3,0,0,0,0,0,4,0,5,6,7,1]}]}`
	d := mustDecode(t, mkGLB(js, nil))
	n := d.Nodes[0]
	if *n.Translation != [3]float32{5, 6, 7} {
		t.Fatalf("translation %v", *n.Translation)
	}
	if math.Abs(float64(n.Scale[0])-2) > 1e-6 || math.Abs(float64(n.Scale[1])-3) > 1e-6 || math.Abs(float64(n.Scale[2])-4) > 1e-6 {
		t.Fatalf("scale %v", *n.Scale)
	}
	h := math.Sqrt2 / 2
	if math.Abs(float64(n.Rotation[2])-h) > 1e-6 || math.Abs(float64(n.Rotation[3])-h) > 1e-6 || math.Abs(float64(n.Rotation[0])) > 1e-6 {
		t.Fatalf("rotation %v", *n.Rotation)
	}
	for name, m := range map[string]string{
		"shear":    `[1,0,0,0, 1,1,0,0, 0,0,1,0, 0,0,0,1]`,
		"zero":     `[0,0,0,0, 0,1,0,0, 0,0,1,0, 0,0,0,1]`,
		"last row": `[1,0,0,0, 0,1,0,0, 0,0,1,0, 0,0,0,2]`,
	} {
		_, err := Decode(mkGLB(`{"asset":{"version":"2.0"},"nodes":[{"matrix":`+m+`}]}`, nil))
		if err == nil || !strings.Contains(err.Error(), "nodes[0].matrix") {
			t.Errorf("%s: err %v", name, err)
		}
	}
	_, err := Decode(mkGLB(`{"asset":{"version":"2.0"},"nodes":[{"matrix":[1,0,0,0,0,1,0,0,0,0,1,0,0,0,0,1],"scale":[1,1,1]}]}`, nil))
	if err == nil {
		t.Error("matrix with scale accepted")
	}
}

func TestMalformed(t *testing.T) {
	good := mkGLB(triJSON, triBin())
	with := func(from, to string) []byte {
		if !strings.Contains(triJSON, from) {
			t.Fatalf("template lacks %q", from)
		}
		return mkGLB(strings.Replace(triJSON, from, to, 1), triBin())
	}
	patch := func(off int, v uint32) []byte {
		b := append([]byte(nil), good...)
		binary.LittleEndian.PutUint32(b[off:], v)
		return b
	}
	nanBin := triBin()
	copy(nanBin[4:], f32bytes(float32(math.NaN())))
	badIdx := triBin()
	badIdx[38] = 7
	cases := []struct {
		name string
		in   []byte
		path string
	}{
		{"empty", nil, "glb"},
		{"short header", good[:11], "glb"},
		{"bad magic", patch(0, 0x12345678), "glb.magic"},
		{"bad version", patch(4, 1), "glb.version"},
		{"length mismatch", patch(8, uint32(len(good)+4)), "glb.length"},
		{"unaligned chunk", patch(12, uint32(binary.LittleEndian.Uint32(good[12:])-1)), "glb.chunks[0]"},
		{"chunk past end", patch(12, uint32(len(good))), "glb.chunks[0]"},
		{"first chunk not json", patch(16, chunkBIN), "glb.chunks[0]"},
		{"truncated second header", good[:len(good)-len(triBin())-4], "glb"},
		{"json garbage", mkGLB(`{"asset":`, nil), "json"},
		{"json wrong type", mkGLB(`{"asset":{"version":"2.0"},"nodes":[{"mesh":"x"}]}`, nil), "nodes"},
		{"no asset", mkGLB(`{}`, nil), "asset"},
		{"accessor count too big", with(`"count":3,"type":"VEC3"`, `"count":4,"type":"VEC3"`), "accessors[0].count"},
		{"view past buffer", with(`"byteOffset":36,"byteLength":6`, `"byteOffset":40,"byteLength":6`), "bufferViews[1].byteLength"},
		{"unknown component", with(`"componentType":5123`, `"componentType":5000`), "accessors[1].componentType"},
		{"position not vec3", with(`"componentType":5126,"count":3,"type":"VEC3"`, `"componentType":5126,"count":3,"type":"SCALAR"`), "meshes[0].primitives[0].attributes.POSITION"},
		{"float indices", with(`"componentType":5123`, `"componentType":5126`), "meshes[0].primitives[0].indices"},
		{"index out of range", mkGLB(triJSON, badIdx), "meshes[0].primitives[0].indices"},
		{"missing position", with(`"POSITION":0`, `"NORMAL":0`), "meshes[0].primitives[0].attributes.POSITION"},
		{"nan position", mkGLB(triJSON, nanBin), "meshes[0].primitives[0].attributes.POSITION[1]"},
		{"mesh index", mkGLB(`{"asset":{"version":"2.0"},"nodes":[{"mesh":3}]}`, nil), "nodes[0].mesh"},
		{"child cycle", mkGLB(`{"asset":{"version":"2.0"},"nodes":[{"children":[1]},{"children":[0]}]}`, nil), "nodes"},
		{"sparse", with(`"count":3,"type":"VEC3"`, `"count":3,"type":"VEC3","sparse":{}`), "accessors[0].sparse"},
		{"external buffer", with(`"buffers":[{"byteLength":44}`, `"buffers":[{"uri":"a.bin","byteLength":44}`), "buffers[0].uri"},
		{"line mode", with(`"indices":1`, `"indices":1,"mode":1`), "meshes[0].primitives[0].mode"},
		{"buffer length", with(`"byteLength":44}]`, `"byteLength":99}]`), "buffers[0].byteLength"},
		{"negative offset", with(`"byteOffset":36`, `"byteOffset":-4`), "bufferViews[1].byteOffset"},
		{"bad stride", with(`{"buffer":0,"byteLength":36}`, `{"buffer":0,"byteLength":36,"byteStride":6}`), "bufferViews[0].byteStride"},
		{"misaligned accessor", with(`"bufferView":0,"componentType":5126,"count":3`, `"bufferView":0,"byteOffset":2,"componentType":5126,"count":3`), "accessors[0].byteOffset"},
		{"huge count", with(`"count":3,"type":"VEC3"`, `"count":4294967296,"type":"VEC3"`), "accessors[0].count"},
		{"bad version string", mkGLB(`{"asset":{"version":"1.0"}}`, nil), "asset.version"},
		{"no buffer but view", mkGLB(`{"asset":{"version":"2.0"},"bufferViews":[{"buffer":0,"byteLength":4}],"images":[{"bufferView":0,"mimeType":"image/png"}]}`, nil), "images[0].bufferView"},
	}
	if len(cases) < 15 {
		t.Fatal("need at least 15 malformed files")
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
			// The path is where the fault was referenced; the message may
			// name the accessor or bufferView at fault. Either must match.
			if !strings.HasPrefix(e.Path, c.path) && !strings.HasPrefix(e.Msg, c.path) {
				t.Fatalf("path %q msg %q, want %q", e.Path, e.Msg, c.path)
			}
		})
	}
	if _, err := Decode(good); err != nil {
		t.Fatalf("template itself must decode: %v", err)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(mustEncodeF(triangleDoc()))
	f.Add(mustEncodeF(richDoc()))
	f.Add(mustEncodeF(rigDoc()))
	f.Add(mkGLB(triJSON, triBin()))
	f.Add(mkGLB(`{"asset":{"version":"2.0"},"nodes":[{"matrix":[0,2,0,0,-3,0,0,0,0,0,4,0,5,6,7,1]}]}`, nil))
	f.Add([]byte("glTF"))
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := Decode(b)
		if err != nil {
			if _, ok := err.(*Error); !ok {
				t.Fatalf("error is %T", err)
			}
			return
		}
		e1, err := Encode(d)
		if err != nil {
			t.Fatalf("decoded document does not encode: %v", err)
		}
		d2, err := Decode(e1)
		if err != nil {
			t.Fatalf("own output does not decode: %v", err)
		}
		e2, err := Encode(d2)
		if err != nil || !bytes.Equal(e1, e2) {
			t.Fatalf("encoding is not stable (%v)", err)
		}
	})
}

func mustEncodeF(d *Document) []byte {
	b, err := Encode(d)
	if err != nil {
		panic(err)
	}
	return b
}

// TestNoUnwrappedMultiply applies the det no-fusion rule to matrix.go.
func TestNoUnwrappedMultiply(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "matrix.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	isMul := func(e ast.Expr) bool {
		for {
			p, ok := e.(*ast.ParenExpr)
			if !ok {
				break
			}
			e = p.X
		}
		b, ok := e.(*ast.BinaryExpr)
		return ok && b.Op == token.MUL
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if b, ok := n.(*ast.BinaryExpr); ok && (b.Op == token.ADD || b.Op == token.SUB) && (isMul(b.X) || isMul(b.Y)) {
			t.Errorf("%s: multiply feeds add or subtract without float64(...)", fset.Position(b.Pos()))
		}
		return true
	})
}

var _ = fmt.Sprint
