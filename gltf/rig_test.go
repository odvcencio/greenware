package gltf

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

// rigGoldenSHA256 is the SHA-256 of testdata/rigged.glb.
const rigGoldenSHA256 = "caf11fb1c7a8b541f94e2c03c6302d98dae42176d49a07aca48ca476f99ab2ce"

// rigDoc is a hand-checked rigged document: a 1 x 2 quad skinned to two
// joints, two morph targets and one animation with every path type.
//
//	node 0 armature (child 1)
//	node 1 joint0 at the origin (child 2)
//	node 2 joint1 one unit above joint0
//	node 3 quad, mesh 0, skin 0
//
// The quad's bottom edge (y = 0) follows joint0 alone. Its top edge (y = 2)
// follows joint0 by 0.25 and joint1 by 0.75. The inverse bind matrix of
// joint1 moves the world up by -1, the inverse of its bind translation.
func rigDoc() *Document {
	return &Document{
		Asset:  Asset{Generator: "gltf codec test"},
		Scene:  Int(0),
		Scenes: []Scene{{Nodes: []int{0, 3}}},
		Nodes: []Node{
			{Name: "armature", Children: []int{1}},
			{Name: "joint0", Children: []int{2}, Translation: &[3]float32{0, 0, 0}},
			{Name: "joint1", Translation: &[3]float32{0, 1, 0}, Rotation: &[4]float32{0, 0, 0, 1}},
			{Name: "quad", Mesh: Int(0), Skin: Int(0), Extras: json.RawMessage(`{"layer":2}`)},
		},
		Meshes: []Mesh{{
			Name: "quad",
			Primitives: []Primitive{{
				Positions: []float32{0, 0, 0, 1, 0, 0, 1, 2, 0, 0, 2, 0},
				Normals:   []float32{0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1},
				UV0:       []float32{0, 0, 1, 0, 1, 1, 0, 1},
				Indices:   []uint32{0, 1, 2, 0, 2, 3},
				Joints0:   []uint16{0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0},
				Weights0:  []float32{1, 0, 0, 0, 1, 0, 0, 0, 0.25, 0.75, 0, 0, 0.25, 0.75, 0, 0},
				Targets: []MorphTarget{
					{Positions: []float32{0, 0, 0, 0, 0, 0, 0, 0.5, 0, 0, 0.5, 0}, Normals: []float32{0, 0, 0, 0, 0, 0, 0, -0.25, 0, 0, -0.25, 0}},
					{Positions: []float32{-0.5, 0, 0, 0.5, 0, 0, 0.5, 0, 0, -0.5, 0, 0}},
				},
				Material: Int(0),
			}},
			Weights:     []float32{0, 0.5},
			TargetNames: []string{"lift", "widen"},
		}},
		Materials: []Material{{
			Name: "skin", BaseColor: [4]float32{0.5, 0.25, 0.125, 1}, Metallic: 0, Roughness: 0.5,
			EmissiveFactor: [3]float32{0.5, 0, 0}, AlphaMode: AlphaMask, AlphaCutoff: 0.25, DoubleSided: true,
		}},
		Skins: []Skin{{
			Name:                "rig",
			Joints:              []int{1, 2},
			InverseBindMatrices: [][16]float32{{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}, {1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, -1, 0, 1}},
			Skeleton:            Int(0),
		}},
		Animations: []Animation{{
			Name: "wave",
			Channels: []Channel{
				{Node: 1, Path: PathTranslation, Sampler: 0},
				{Node: 2, Path: PathRotation, Sampler: 1},
				{Node: 1, Path: PathScale, Sampler: 2},
				{Node: 3, Path: PathWeights, Sampler: 3},
			},
			Samplers: []AnimSampler{
				{Input: []float32{0, 0.5, 1}, Output: []float32{0, 0, 0, 0, 0.5, 0, 0, 1, 0}, Interpolation: InterpLinear},
				{Input: []float32{0, 1}, Output: []float32{0, 0, 0, 1, 0, 0, 0.70710677, 0.70710677}, Interpolation: InterpStep},
				// CUBICSPLINE: in-tangent, value, out-tangent per key.
				{Input: []float32{0, 1}, Output: []float32{0, 0, 0, 1, 1, 1, 0, 0, 0, 0, 0, 0, 2, 2, 2, 0, 0, 0}, Interpolation: InterpCubicSpline},
				{Input: []float32{0, 1}, Output: []float32{0, 0, 1, 0.5}, Interpolation: InterpLinear},
			},
		}},
	}
}

func TestRigGolden(t *testing.T) {
	b := mustEncode(t, rigDoc())
	path := "testdata/rigged.glb"
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
	if got := hex.EncodeToString(sum[:]); got != rigGoldenSHA256 {
		t.Fatalf("sha256 %s, want %s", got, rigGoldenSHA256)
	}
}

func TestRigRoundTrip(t *testing.T) {
	d := rigDoc()
	b1 := mustEncode(t, d)
	d2 := mustDecode(t, b1)
	if !bytes.Equal(b1, mustEncode(t, d2)) {
		t.Fatal("Encode(Decode(Encode(d))) differs from Encode(d)")
	}
	// Extras come back canonical; the rig itself must come back unchanged.
	d.Nodes[3].Extras = d2.Nodes[3].Extras
	if !reflect.DeepEqual(d, d2) {
		t.Fatalf("Decode(Encode(d)) != d\n%+v\n%+v", d, d2)
	}
}

// parts splits a GLB into its JSON text and binary chunk.
func parts(t *testing.T, b []byte) (string, []byte) {
	t.Helper()
	jl := binary.LittleEndian.Uint32(b[12:])
	js := strings.TrimRight(string(b[20:20+jl]), " ")
	return js, b[28+jl:]
}

func jsonOf(t *testing.T, b []byte) map[string]any {
	t.Helper()
	js, _ := parts(t, b)
	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRigLayout(t *testing.T) {
	b := mustEncode(t, rigDoc())
	js, bin := parts(t, b)
	// The accessor order is: POSITION, NORMAL, TEXCOORD_0, indices, JOINTS_0,
	// WEIGHTS_0, two targets (POSITION and NORMAL, then POSITION), the inverse
	// bind matrices, then input and output per animation sampler.
	for _, want := range []string{
		`"nodes":[{"name":"armature","children":[1]},{"name":"joint0","children":[2],"translation":[0,0,0]},` +
			`{"name":"joint1","translation":[0,1,0],"rotation":[0,0,0,1]},{"name":"quad","mesh":0,"skin":0,"extras":{"layer":2}}]`,
		`"attributes":{"POSITION":0,"NORMAL":1,"TEXCOORD_0":2,"JOINTS_0":4,"WEIGHTS_0":5},"indices":3,"material":0,` +
			`"targets":[{"POSITION":6,"NORMAL":7},{"POSITION":8}]}],"weights":[0,0.5],"extras":{"targetNames":["lift","widen"]}`,
		`"materials":[{"name":"skin","pbrMetallicRoughness":{"baseColorFactor":[0.5,0.25,0.125,1],"metallicFactor":0,"roughnessFactor":0.5},` +
			`"emissiveFactor":[0.5,0,0],"alphaMode":"MASK","alphaCutoff":0.25,"doubleSided":true}]`,
		`"skins":[{"name":"rig","inverseBindMatrices":9,"skeleton":0,"joints":[1,2]}]`,
		`"animations":[{"name":"wave","channels":[{"sampler":0,"target":{"node":1,"path":"translation"}},` +
			`{"sampler":1,"target":{"node":2,"path":"rotation"}},{"sampler":2,"target":{"node":1,"path":"scale"}},` +
			`{"sampler":3,"target":{"node":3,"path":"weights"}}],` +
			`"samplers":[{"input":10,"interpolation":"LINEAR","output":11},{"input":12,"interpolation":"STEP","output":13},` +
			`{"input":14,"interpolation":"CUBICSPLINE","output":15},{"input":16,"interpolation":"LINEAR","output":17}]}]`,
		// Joint indices are all below 256, so JOINTS_0 is unsigned byte (5121).
		`{"bufferView":4,"componentType":5121,"count":4,"type":"VEC4"}`,
		`{"bufferView":5,"componentType":5126,"count":4,"type":"VEC4"}`,
		`{"bufferView":8,"componentType":5126,"count":4,"type":"VEC3","min":[-0.5,0,0],"max":[0.5,0,0]}`,
		`{"bufferView":9,"componentType":5126,"count":2,"type":"MAT4"}`,
		`{"bufferView":10,"componentType":5126,"count":3,"type":"SCALAR","min":[0],"max":[1]}`,
		`{"bufferView":11,"componentType":5126,"count":3,"type":"VEC3"}`,
		`{"bufferView":13,"componentType":5126,"count":2,"type":"VEC4"}`,
		`{"bufferView":15,"componentType":5126,"count":6,"type":"VEC3"}`,
		`{"bufferView":17,"componentType":5126,"count":4,"type":"SCALAR"}`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("JSON lacks\n%s\nin\n%s", want, js)
		}
	}
	if len(bin)%4 != 0 {
		t.Fatal("binary chunk is not 4-byte aligned")
	}
	// Every bufferView starts on a multiple of 4.
	m := jsonOf(t, b)
	for i, v := range m["bufferViews"].([]any) {
		if off := v.(map[string]any)["byteOffset"].(float64); int(off)%4 != 0 {
			t.Errorf("bufferViews[%d] offset %v is not aligned", i, off)
		}
	}
	// Skin and animation views carry no target.
	views := m["bufferViews"].([]any)
	for i := 9; i < len(views); i++ {
		if _, ok := views[i].(map[string]any)["target"]; ok {
			t.Errorf("bufferViews[%d] has a target", i)
		}
	}
}

// TestRigDefaultsAreNotWritten shows documents that skip the new fields keep
// the old output (the triangle golden test proves the bytes).
func TestRigDefaultsAreNotWritten(t *testing.T) {
	js, _ := parts(t, mustEncode(t, richDoc()))
	for _, k := range []string{"skins", "animations", "emissiveFactor", "alphaMode", "alphaCutoff", "doubleSided", "targets", "JOINTS_0", "texCoord"} {
		if strings.Contains(js, `"`+k+`"`) {
			t.Errorf("default document writes %q", k)
		}
	}
}

func TestJointWidth(t *testing.T) {
	for _, tc := range []struct {
		joints, top int
		comp        float64
	}{{256, 255, 5121}, {257, 256, 5123}, {2, 0, 5121}} {
		d := &Document{Scenes: []Scene{{Nodes: []int{tc.joints}}}}
		sk := Skin{}
		for i := 0; i < tc.joints; i++ {
			d.Nodes = append(d.Nodes, Node{})
			sk.Joints = append(sk.Joints, i)
		}
		d.Nodes = append(d.Nodes, Node{Mesh: Int(0), Skin: Int(0)})
		d.Skins = []Skin{sk}
		d.Meshes = []Mesh{{Primitives: []Primitive{{
			Positions: []float32{0, 0, 0, 1, 0, 0, 0, 1, 0},
			Joints0:   []uint16{0, 0, 0, 0, 0, 0, 0, 0, uint16(tc.top), 0, 0, 0},
			Weights0:  []float32{1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0},
		}}}}
		b := mustEncode(t, d)
		acc := jsonOf(t, b)["accessors"].([]any)
		if got := acc[1].(map[string]any)["componentType"].(float64); got != tc.comp {
			t.Errorf("%d joints, top index %d: componentType %v, want %v", tc.joints, tc.top, got, tc.comp)
		}
		if got := mustDecode(t, b).Meshes[0].Primitives[0].Joints0; got[8] != uint16(tc.top) {
			t.Errorf("joint index %d came back as %d", tc.top, got[8])
		}
	}
}

func TestRigEncodeErrors(t *testing.T) {
	mut := func(f func(d *Document)) *Document {
		d := rigDoc()
		f(d)
		return d
	}
	cases := []struct {
		name string
		doc  *Document
		path string
	}{
		{"weights sum", mut(func(d *Document) { d.Meshes[0].Primitives[0].Weights0[9] = 0.5 }), "meshes[0].primitives[0].attributes.WEIGHTS_0[8]"},
		{"negative weight", mut(func(d *Document) {
			d.Meshes[0].Primitives[0].Weights0[1] = -0.5
			d.Meshes[0].Primitives[0].Weights0[0] = 1.5
		}), "meshes[0].primitives[0].attributes.WEIGHTS_0[1]"},
		{"nan weight", mut(func(d *Document) { d.Meshes[0].Primitives[0].Weights0[2] = float32(math.NaN()) }), "meshes[0].primitives[0].attributes.WEIGHTS_0[2]"},
		{"joint out of skin", mut(func(d *Document) { d.Meshes[0].Primitives[0].Joints0[4] = 2 }), "meshes[0].primitives[0].attributes.JOINTS_0[4]"},
		{"joint count", mut(func(d *Document) { d.Meshes[0].Primitives[0].Joints0 = d.Meshes[0].Primitives[0].Joints0[:12] }), "meshes[0].primitives[0].attributes.JOINTS_0"},
		{"weights without joints", mut(func(d *Document) { d.Meshes[0].Primitives[0].Joints0 = nil }), "meshes[0].primitives[0].attributes.JOINTS_0"},
		{"skin without mesh", mut(func(d *Document) { d.Nodes[3].Mesh = nil }), "nodes[3].skin"},
		{"skin index", mut(func(d *Document) { d.Nodes[3].Skin = Int(1) }), "nodes[3].skin"},
		{"joint node", mut(func(d *Document) { d.Skins[0].Joints[1] = 9 }), "skins[0].joints[1]"},
		{"joint twice", mut(func(d *Document) { d.Skins[0].Joints[1] = 1 }), "skins[0].joints[1]"},
		{"ibm count", mut(func(d *Document) { d.Skins[0].InverseBindMatrices = d.Skins[0].InverseBindMatrices[:1] }), "skins[0].inverseBindMatrices"},
		{"target length", mut(func(d *Document) { d.Meshes[0].Primitives[0].Targets[1].Positions = []float32{1, 2, 3} }), "meshes[0].primitives[0].targets[1].POSITION"},
		{"target normals length", mut(func(d *Document) { d.Meshes[0].Primitives[0].Targets[0].Normals = []float32{1, 2, 3} }), "meshes[0].primitives[0].targets[0].NORMAL"},
		{"mesh weights length", mut(func(d *Document) { d.Meshes[0].Weights = []float32{1} }), "meshes[0].weights"},
		{"target names length", mut(func(d *Document) { d.Meshes[0].TargetNames = []string{"a"} }), "meshes[0].extras.targetNames"},
		{"target names and extras", mut(func(d *Document) { d.Meshes[0].Extras = json.RawMessage(`{"targetNames":["x","y"]}`) }), "meshes[0].extras.targetNames"},
		{"target names with array extras", mut(func(d *Document) { d.Meshes[0].Extras = json.RawMessage(`[1]`) }), "meshes[0].extras"},
		{"time order", mut(func(d *Document) { d.Animations[0].Samplers[0].Input[1] = 0 }), "animations[0].samplers[0].input[1]"},
		{"time nan", mut(func(d *Document) { d.Animations[0].Samplers[0].Input[1] = float32(math.Inf(1)) }), "animations[0].samplers[0].input[1]"},
		{"time negative", mut(func(d *Document) { d.Animations[0].Samplers[0].Input[0] = -1 }), "animations[0].samplers[0].input[0]"},
		{"output length", mut(func(d *Document) { d.Animations[0].Samplers[0].Output = d.Animations[0].Samplers[0].Output[:6] }), "animations[0].samplers[0].output"},
		{"cubic output length", mut(func(d *Document) { d.Animations[0].Samplers[2].Output = d.Animations[0].Samplers[2].Output[:6] }), "animations[0].samplers[2].output"},
		{"quaternion nan", mut(func(d *Document) { d.Animations[0].Samplers[1].Output[3] = float32(math.NaN()) }), "animations[0].samplers[1].output[3]"},
		{"path", mut(func(d *Document) { d.Animations[0].Channels[0].Path = "pointer" }), "animations[0].channels[0].target.path"},
		{"channel node", mut(func(d *Document) { d.Animations[0].Channels[0].Node = 9 }), "animations[0].channels[0].target.node"},
		{"channel sampler", mut(func(d *Document) { d.Animations[0].Channels[0].Sampler = 9 }), "animations[0].channels[0].sampler"},
		{"duplicate target", mut(func(d *Document) { d.Animations[0].Channels[2] = Channel{Node: 1, Path: PathTranslation, Sampler: 0} }), "animations[0].channels[2].target"},
		{"unused sampler", mut(func(d *Document) { d.Animations[0].Channels = d.Animations[0].Channels[:3] }), "animations[0].samplers[3]"},
		{"weights on meshless node", mut(func(d *Document) { d.Animations[0].Channels[3].Node = 0 }), "animations[0].channels[3].target.path"},
		{"interpolation", mut(func(d *Document) { d.Animations[0].Samplers[0].Interpolation = "SMOOTH" }), "animations[0].samplers[0].interpolation"},
		{"alpha mode", mut(func(d *Document) { d.Materials[0].AlphaMode = "CLIP" }), "materials[0].alphaMode"},
		{"cutoff without mask", mut(func(d *Document) { d.Materials[0].AlphaMode = "" }), "materials[0].alphaCutoff"},
		{"emissive nan", mut(func(d *Document) { d.Materials[0].EmissiveFactor[1] = float32(math.NaN()) }), "materials[0].emissiveFactor[1]"},
		{"target count differs", mut(func(d *Document) {
			p := d.Meshes[0].Primitives[0]
			p.Targets = p.Targets[:1]
			d.Meshes[0].Primitives = append(d.Meshes[0].Primitives, p)
		}), "meshes[0].primitives[1].targets"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Encode(c.doc)
			if err == nil {
				t.Fatal("accepted")
			}
			e, ok := err.(*Error)
			if !ok || !strings.HasPrefix(e.Path, c.path) {
				t.Fatalf("error %v, want path %q", err, c.path)
			}
		})
	}
	// A vertex whose weights are all zero is allowed.
	d := rigDoc()
	copy(d.Meshes[0].Primitives[0].Weights0[12:], []float32{0, 0, 0, 0})
	mustEncode(t, d)
}

func TestWeightsWithinTolerance(t *testing.T) {
	d := rigDoc()
	d.Meshes[0].Primitives[0].Weights0[8], d.Meshes[0].Primitives[0].Weights0[9] = 0.25, 0.750005
	mustEncode(t, d)
	d.Meshes[0].Primitives[0].Weights0[9] = 0.75002
	if _, err := Encode(d); err == nil {
		t.Fatal("sum 1.00002 accepted")
	}
}

func TestTargetNamesRoundTrip(t *testing.T) {
	d := rigDoc()
	d.Meshes[0].Extras = json.RawMessage(`{"z":1.50,"a":[true]}`)
	b := mustEncode(t, d)
	if !strings.Contains(string(b), `"extras":{"a":[true],"targetNames":["lift","widen"],"z":1.50}`) {
		t.Fatal("targetNames not merged into sorted extras")
	}
	got := mustDecode(t, b).Meshes[0]
	if !reflect.DeepEqual(got.TargetNames, []string{"lift", "widen"}) || string(got.Extras) != `{"a":[true],"z":1.50}` {
		t.Fatalf("names %v extras %s", got.TargetNames, got.Extras)
	}
	// Names the mesh cannot use stay in the extras and still round trip.
	d = rigDoc()
	d.Meshes[0].TargetNames = nil
	d.Meshes[0].Extras = json.RawMessage(`{"targetNames":["only one"]}`)
	b = mustEncode(t, d)
	got = mustDecode(t, b).Meshes[0]
	if got.TargetNames != nil || string(got.Extras) != `{"targetNames":["only one"]}` {
		t.Fatalf("names %v extras %s", got.TargetNames, got.Extras)
	}
	if !bytes.Equal(b, mustEncode(t, mustDecode(t, b))) {
		t.Fatal("unstable")
	}
}

func TestMaterialAndExtras(t *testing.T) {
	d := &Document{
		Materials: []Material{
			{Name: "blend", BaseColor: [4]float32{1, 1, 1, 1}, Metallic: 1, Roughness: 1, AlphaMode: AlphaBlend, BaseColorTexture: Int(0), BaseColorTexCoord: 1},
			{Name: "opaque", BaseColor: [4]float32{1, 1, 1, 1}, Metallic: 1, Roughness: 1, AlphaMode: AlphaOpaque},
		},
		Textures: []Texture{{Source: Int(0), Extras: json.RawMessage(`{"t":1}`)}},
		Images:   []Image{{URI: "a.png", Extras: json.RawMessage(`{"i":[2]}`)}},
	}
	b := mustEncode(t, d)
	got := mustDecode(t, b)
	if got.Materials[0].AlphaMode != AlphaBlend || got.Materials[0].BaseColorTexCoord != 1 || got.Materials[1].AlphaMode != "" {
		t.Fatalf("materials %+v", got.Materials)
	}
	if string(got.Textures[0].Extras) != `{"t":1}` || string(got.Images[0].Extras) != `{"i":[2]}` {
		t.Fatalf("extras %s %s", got.Textures[0].Extras, got.Images[0].Extras)
	}
	if !bytes.Equal(b, mustEncode(t, got)) {
		t.Fatal("unstable")
	}
	// An absent alphaCutoff on a MASK material is 0.5 and a decoded cutoff
	// is written back.
	m := mustDecode(t, mkGLB(`{"asset":{"version":"2.0"},"materials":[{"alphaMode":"MASK"}]}`, nil)).Materials[0]
	if m.AlphaMode != AlphaMask || m.AlphaCutoff != 0.5 {
		t.Fatalf("%+v", m)
	}
}

// normalized builds a one-triangle file whose WEIGHTS_0 is normalized ubyte.
func TestNormalizedWeights(t *testing.T) {
	js := `{"asset":{"version":"2.0"},"nodes":[{"mesh":0,"skin":0},{}],"skins":[{"joints":[1]}],` +
		`"meshes":[{"primitives":[{"attributes":{"POSITION":0,"JOINTS_0":1,"WEIGHTS_0":2}}]}],` +
		`"buffers":[{"byteLength":60}],"bufferViews":[{"buffer":0,"byteLength":36},{"buffer":0,"byteOffset":36,"byteLength":12},{"buffer":0,"byteOffset":48,"byteLength":12}],` +
		`"accessors":[{"bufferView":0,"componentType":5126,"count":3,"type":"VEC3"},{"bufferView":1,"componentType":5121,"count":3,"type":"VEC4"},` +
		`{"bufferView":2,"componentType":5121,"normalized":true,"count":3,"type":"VEC4"}]}`
	bin := f32bytes(0, 0, 0, 1, 0, 0, 0, 1, 0)
	bin = append(bin, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	// 255 total, 254 total (a rounding step short) and all zero.
	bin = append(bin, 255, 0, 0, 0, 127, 127, 0, 0, 0, 0, 0, 0)
	p := mustDecode(t, mkGLB(js, bin)).Meshes[0].Primitives[0]
	if p.Weights0[0] != 1 {
		t.Errorf("weights %v", p.Weights0)
	}
	if got := float64(p.Weights0[4]) + float64(p.Weights0[5]); math.Abs(got-1) > 1e-6 {
		t.Errorf("rescaled weights %v sum to %v", p.Weights0[4:8], got)
	}
	for _, w := range p.Weights0[8:] {
		if w != 0 {
			t.Errorf("zero vertex weights %v", p.Weights0[8:])
		}
	}
	// A non-normalized integer weight accessor is a fault.
	bad := strings.Replace(js, `"normalized":true,`, ``, 1)
	if _, err := Decode(mkGLB(bad, bin)); err == nil {
		t.Error("integer weights without normalized accepted")
	}
}

// ---- hostile skin, animation and morph files ----

// accOffset returns the byte offset in the binary chunk of accessor i.
func accOffset(t *testing.T, b []byte, i int) int {
	t.Helper()
	m := jsonOf(t, b)
	a := m["accessors"].([]any)[i].(map[string]any)
	v := m["bufferViews"].([]any)[int(a["bufferView"].(float64))].(map[string]any)
	return int(v["byteOffset"].(float64))
}

func TestMalformedRig(t *testing.T) {
	good := mustEncode(t, rigDoc())
	js, bin := parts(t, good)
	withJSON := func(from, to string) []byte {
		if strings.Count(js, from) != 1 {
			t.Fatalf("template has %d copies of %q", strings.Count(js, from), from)
		}
		return mkGLB(strings.Replace(js, from, to, 1), bin)
	}
	withBin := func(acc, floatIndex int, v float32) []byte {
		b := append([]byte(nil), bin...)
		binary.LittleEndian.PutUint32(b[accOffset(t, good, acc)+4*floatIndex:], math.Float32bits(v))
		return mkGLB(js, b)
	}
	// A joint index at or above the skin's joint count: patch the ubyte data.
	jb := append([]byte(nil), bin...)
	jb[accOffset(t, good, 4)+4] = 2
	jointBin := mkGLB(js, jb)
	cases := []struct {
		name string
		in   []byte
		path string
	}{
		{"skin joint node", withJSON(`"joints":[1,2]`, `"joints":[1,7]`), "skins[0].joints[1]"},
		{"skin no joints", withJSON(`"joints":[1,2]`, `"joints":[]`), "skins[0].joints"},
		{"skin ibm not mat4", withJSON(`"inverseBindMatrices":9`, `"inverseBindMatrices":8`), "skins[0].inverseBindMatrices"},
		{"skin ibm count", withJSON(`"count":2,"type":"MAT4"`, `"count":1,"type":"MAT4"`), "skins[0].inverseBindMatrices"},
		{"skin ibm accessor", withJSON(`"inverseBindMatrices":9`, `"inverseBindMatrices":99`), "skins[0].inverseBindMatrices"},
		{"node skin index", withJSON(`"skin":0`, `"skin":4`), "nodes[3].skin"},
		{"joints without weights", withJSON(`,"WEIGHTS_0":5`, ``), "meshes[0].primitives[0].attributes"},
		{"joints type", withJSON(`"componentType":5121,"count":4,"type":"VEC4"`, `"componentType":5126,"count":4,"type":"VEC4"`), "meshes[0].primitives[0].attributes.JOINTS_0"},
		{"joint index over skin", jointBin, "meshes[0].primitives[0].attributes.JOINTS_0[4]"},
		{"weights sum", withBin(5, 0, 0.5), "meshes[0].primitives[0].attributes.WEIGHTS_0"},
		{"weights nan", withBin(5, 1, float32(math.NaN())), "meshes[0].primitives[0].attributes.WEIGHTS_0"},
		{"target count", withJSON(`"count":4,"type":"VEC3","min":[-0.5,0,0]`, `"count":3,"type":"VEC3","min":[-0.5,0,0]`), "meshes[0].primitives[0].targets[1].POSITION"},
		{"target missing accessor", withJSON(`"targets":[{"POSITION":6,"NORMAL":7}`, `"targets":[{"POSITION":60,"NORMAL":7}`), "meshes[0].primitives[0].targets[0].POSITION"},
		{"target type", withJSON(`"targets":[{"POSITION":6,"NORMAL":7}`, `"targets":[{"POSITION":4,"NORMAL":7}`), "meshes[0].primitives[0].targets[0].POSITION"},
		{"target sparse", withJSON(`{"bufferView":8,"componentType":5126,"count":4,"type":"VEC3",`, `{"bufferView":8,"sparse":{},"componentType":5126,"count":4,"type":"VEC3",`), "meshes[0].primitives[0].targets[1].POSITION"},
		{"mesh weights count", withJSON(`"weights":[0,0.5]`, `"weights":[0,0.5,1]`), "meshes[0].weights"},
		{"anim time order", withBin(10, 1, 2), "animations[0].samplers[0].input"},
		{"anim time nan", withBin(10, 2, float32(math.NaN())), "animations[0].samplers[0].input"},
		{"anim time negative", withBin(12, 0, -1), "animations[0].samplers[1].input"},
		{"anim quaternion nan", withBin(13, 5, float32(math.NaN())), "animations[0].samplers[1].output"},
		{"anim output length", withJSON(`"count":6,"type":"VEC3"}`, `"count":5,"type":"VEC3"}`), "animations[0].samplers[2].output"},
		{"anim output type", withJSON(`"input":12,"interpolation":"STEP","output":13`, `"input":12,"interpolation":"STEP","output":11`), "animations[0].samplers[1].output"},
		{"anim bad path", withJSON(`"path":"scale"`, `"path":"pointer"`), "animations[0].channels[2].target.path"},
		{"anim channel node", withJSON(`"node":2,"path":"rotation"`, `"node":22,"path":"rotation"`), "animations[0].channels[1].target.node"},
		{"anim channel sampler", withJSON(`{"sampler":3,"target"`, `{"sampler":9,"target"`), "animations[0].channels[3].sampler"},
		{"anim no target node", withJSON(`"target":{"node":3,"path":"weights"}`, `"target":{"path":"weights"}`), "animations[0].channels[3].target.node"},
		{"anim sampler unused", withJSON(`{"sampler":3,"target":{"node":3,"path":"weights"}}`, `{"sampler":2,"target":{"node":3,"path":"weights"}}`), "animations[0].samplers[3]"},
		{"anim interpolation", withJSON(`"interpolation":"STEP"`, `"interpolation":"SMOOTH"`), "animations[0].samplers[1].interpolation"},
		{"anim missing input", withJSON(`{"input":10,"interpolation":"LINEAR","output":11}`, `{"interpolation":"LINEAR","output":11}`), "animations[0].samplers[0]"},
		{"anim input type", withJSON(`"input":10,"interpolation":"LINEAR","output":11`, `"input":11,"interpolation":"LINEAR","output":11`), "animations[0].samplers[0].input"},
		{"material alpha", withJSON(`"alphaMode":"MASK"`, `"alphaMode":"CLIP"`), "materials[0].alphaMode"},
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
			if !strings.HasPrefix(e.Path, c.path) && !strings.HasPrefix(e.Msg, c.path) {
				t.Fatalf("path %q msg %q, want %q", e.Path, e.Msg, c.path)
			}
		})
	}
	if _, err := Decode(good); err != nil {
		t.Fatalf("template itself must decode: %v", err)
	}
}
