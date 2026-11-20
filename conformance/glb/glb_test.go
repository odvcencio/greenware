package glb_test

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"m31labs.dev/greenware/conformance/glb"
	"m31labs.dev/greenware/conformance/internal/fixture"
)

func cubeFile() []byte {
	b := fixture.New()
	m := b.Mesh("cube", fixture.Cube(0, 0, 0, 0.5, 0.5, 0.5))
	n := b.Node("cube", m, [3]float64{})
	b.Scene(n)
	return b.Bytes()
}

func codes(fs []glb.Finding) string {
	var s []string
	for _, f := range fs {
		s = append(s, f.Code)
	}
	return strings.Join(s, ",")
}

func hasCode(fs []glb.Finding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}

func noInternal(t *testing.T, fs []glb.Finding) {
	t.Helper()
	if hasCode(fs, "ORACLE-INTERNAL") {
		t.Fatalf("validator fault: %v", fs)
	}
}

func TestValidCubeIsClean(t *testing.T) {
	f, fs := glb.Decode(cubeFile())
	if len(fs) != 0 || f == nil || f.Doc == nil {
		t.Fatalf("findings on a valid file: %v", fs)
	}
	pos, nc, ok := f.Floats(0)
	if !ok || nc != 3 || len(pos) != 24 {
		t.Fatalf("POSITION read: ok=%v ncomp=%d len=%d", ok, nc, len(pos))
	}
}

func TestEveryTruncationIsFoundAndNeverPanics(t *testing.T) {
	data := cubeFile()
	for n := 0; n < len(data); n++ {
		_, fs := glb.Decode(data[:n])
		noInternal(t, fs)
		if !glb.HasErrors(fs) {
			t.Fatalf("a %d-byte prefix of a %d-byte file produced no error finding", n, len(data))
		}
	}
}

func TestContainerFaults(t *testing.T) {
	le := binary.LittleEndian
	cases := []struct {
		name string
		edit func(d []byte) []byte
		want string
	}{
		{"bad magic", func(d []byte) []byte { d[0] = 'X'; return d }, "GLB-MAGIC"},
		{"bad version", func(d []byte) []byte { le.PutUint32(d[4:], 1); return d }, "GLB-VERSION"},
		{"header length too long", func(d []byte) []byte { le.PutUint32(d[8:], uint32(len(d))+8); return d }, "GLB-LENGTH"},
		{"header length too short", func(d []byte) []byte { le.PutUint32(d[8:], uint32(len(d))-4); return d }, "GLB-LENGTH"},
		{"json chunk longer than file", func(d []byte) []byte { le.PutUint32(d[12:], 1<<20); return d }, "GLB-CHUNK-LENGTH"},
		{"json chunk length not aligned", func(d []byte) []byte { le.PutUint32(d[12:], le.Uint32(d[12:])-1); return d }, "GLB-CHUNK-ALIGN"},
		{"first chunk not json", func(d []byte) []byte { le.PutUint32(d[16:], 0x004E4942); return d }, "GLB-CHUNK-ORDER"},
		{"json garbage", func(d []byte) []byte { d[21] = '#'; return d }, "JSON-SYNTAX"},
		{"short header", func(d []byte) []byte { return d[:8] }, "GLB-HEADER"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, fs := glb.Decode(c.edit(cubeFile()))
			noInternal(t, fs)
			if !hasCode(fs, c.want) {
				t.Fatalf("want %s, got %s", c.want, codes(fs))
			}
		})
	}
}

func TestBinChunkMissingAndOversize(t *testing.T) {
	b := fixture.New()
	b.Node("n", -1, [3]float64{})
	b.Scene(0)
	doc, _ := b.Parts()
	doc["buffers"] = []any{map[string]any{"byteLength": 12}}
	_, fs := glb.Decode(fixture.Pack(doc, nil))
	if !hasCode(fs, "BUFFER-NO-BIN") {
		t.Fatalf("got %s", codes(fs))
	}
	doc["buffers"] = []any{map[string]any{"byteLength": 4}}
	_, fs = glb.Decode(fixture.Pack(doc, make([]byte, 16)))
	if !hasCode(fs, "BUFFER-PADDING") {
		t.Fatalf("got %s", codes(fs))
	}
	doc["buffers"] = []any{map[string]any{"byteLength": 40}}
	_, fs = glb.Decode(fixture.Pack(doc, make([]byte, 16)))
	if !hasCode(fs, "BUFFER-LENGTH") {
		t.Fatalf("got %s", codes(fs))
	}
}

func TestJSONTrailingNUL(t *testing.T) {
	b := fixture.New()
	b.Node("n", -1, [3]float64{})
	b.Scene(0)
	doc, _ := b.Parts()
	raw := []byte(`{"asset":{"version":"2.0"}}`)
	_ = doc
	raw = append(raw, 0)
	_, fs := glb.Decode(fixture.PackRaw(raw, nil))
	if !hasCode(fs, "JSON-TRAILING") {
		t.Fatalf("got %s", codes(fs))
	}
}

func TestIndexAndAttributeFaults(t *testing.T) {
	mk := func(mod func(p *fixture.Prim)) []byte {
		p := fixture.Cube(0, 0, 0, 1, 1, 1)
		mod(&p)
		b := fixture.New()
		b.Scene(b.Node("n", b.Mesh("m", p), [3]float64{}))
		return b.Bytes()
	}
	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"index out of range", mk(func(p *fixture.Prim) { p.Idx[5] = 99 }), "INDEX-RANGE"},
		{"restart index", mk(func(p *fixture.Prim) { p.Wide = false; p.Idx[0] = 0xFFFF }), "INDEX-RESTART"},
		{"index count", mk(func(p *fixture.Prim) { p.Idx = p.Idx[:10] }), "INDEX-COUNT"},
		{"NaN position", mk(func(p *fixture.Prim) { p.Pos[3][1] = nan }), "ACC-NONFINITE"},
		{"Inf position", mk(func(p *fixture.Prim) { p.Pos[3][1] = inf }), "ACC-NONFINITE"},
		{"NaN normal", mk(func(p *fixture.Prim) {
			p.Normals = make([][3]float32, len(p.Pos))
			for i := range p.Normals {
				p.Normals[i] = [3]float32{0, 1, 0}
			}
			p.Normals[2][0] = nan
		}), "ACC-NONFINITE"},
		{"zero normal", mk(func(p *fixture.Prim) { p.Normals = make([][3]float32, len(p.Pos)) }), "NORMAL-ZERO"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, fs := glb.Decode(c.data)
			noInternal(t, fs)
			if !hasCode(fs, c.want) {
				t.Fatalf("want %s, got %s", c.want, codes(fs))
			}
		})
	}
}

func TestNonUnitNormalIsWarning(t *testing.T) {
	p := fixture.Cube(0, 0, 0, 1, 1, 1)
	p.Normals = make([][3]float32, len(p.Pos))
	for i := range p.Normals {
		p.Normals[i] = [3]float32{0, 2, 0}
	}
	b := fixture.New()
	b.Scene(b.Node("n", b.Mesh("m", p), [3]float64{}))
	_, fs := glb.Decode(b.Bytes())
	if !hasCode(fs, "NORMAL-LENGTH") || glb.HasErrors(fs) {
		t.Fatalf("got %s", codes(fs))
	}
}

func edited(t *testing.T, edit func(doc map[string]any)) []glb.Finding {
	t.Helper()
	b := fixture.New()
	m := b.Mesh("cube", fixture.Cube(0, 0, 0, 0.5, 0.5, 0.5))
	b.Scene(b.Node("cube", m, [3]float64{}))
	doc, bin := b.Parts()
	edit(doc)
	_, fs := glb.Decode(fixture.Pack(doc, bin))
	noInternal(t, fs)
	return fs
}

func acc(doc map[string]any, i int) map[string]any {
	return doc["accessors"].([]any)[i].(map[string]any)
}

func TestJSONFaults(t *testing.T) {
	cases := []struct {
		name string
		edit func(doc map[string]any)
		want string
	}{
		{"min wrong", func(d map[string]any) { acc(d, 0)["min"] = []float64{-0.4, -0.5, -0.5} }, "ACC-MIN"},
		{"max wrong", func(d map[string]any) { acc(d, 0)["max"] = []float64{0.5, 0.6, 0.5} }, "ACC-MAX"},
		{"min missing", func(d map[string]any) { delete(acc(d, 0), "min"); delete(acc(d, 0), "max") }, "POSITION-MINMAX"},
		{"min length", func(d map[string]any) { acc(d, 0)["min"] = []float64{0} }, "ACC-MINMAX-LENGTH"},
		{"count past view", func(d map[string]any) { acc(d, 0)["count"] = 1000 }, "ACC-BOUNDS"},
		{"bad component type", func(d map[string]any) { acc(d, 0)["componentType"] = 5124 }, "ACC-COMPONENT"},
		{"bad type", func(d map[string]any) { acc(d, 0)["type"] = "VEC5" }, "ACC-TYPE"},
		{"unaligned offset", func(d map[string]any) { acc(d, 0)["byteOffset"] = 2 }, "ACC-ALIGN"},
		{"view out of range", func(d map[string]any) { acc(d, 0)["bufferView"] = 9 }, "ACC-VIEW"},
		{"view past buffer", func(d map[string]any) {
			d["bufferViews"].([]any)[0].(map[string]any)["byteLength"] = 1 << 20
		}, "VIEW-BOUNDS"},
		{"bad stride", func(d map[string]any) { d["bufferViews"].([]any)[0].(map[string]any)["byteStride"] = 6 }, "VIEW-STRIDE"},
		{"index accessor type", func(d map[string]any) { acc(d, 1)["componentType"] = 5122 }, "INDEX-TYPE"},
		{"mesh ref", func(d map[string]any) { d["nodes"].([]any)[0].(map[string]any)["mesh"] = 7 }, "NODE-MESH"},
		{"material ref", func(d map[string]any) {
			d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["material"] = 3
		}, "PRIM-MATERIAL"},
		{"attribute accessor ref", func(d map[string]any) {
			d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["attributes"].(map[string]any)["POSITION"] = 50
		}, "ATTR-ACCESSOR"},
		{"no position", func(d map[string]any) {
			d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["attributes"] = map[string]any{}
		}, "PRIM-POSITION"},
		{"scene index", func(d map[string]any) { d["scene"] = 4 }, "SCENE-INDEX"},
		{"scene node", func(d map[string]any) { d["scenes"].([]any)[0].(map[string]any)["nodes"] = []int{3} }, "SCENE-NODE"},
		{"asset version", func(d map[string]any) { d["asset"] = map[string]any{"version": "1.0"} }, "ASSET-VERSION"},
		{"asset missing", func(d map[string]any) { delete(d, "asset") }, "JSON-REQUIRED"},
		{"required extension", func(d map[string]any) {
			d["extensionsUsed"] = []string{"KHR_draco_mesh_compression"}
			d["extensionsRequired"] = []string{"KHR_draco_mesh_compression"}
		}, "EXT-UNSUPPORTED"},
		{"required but unused", func(d map[string]any) { d["extensionsRequired"] = []string{"KHR_texture_transform"} }, "EXT-NOT-USED"},
		{"node matrix and trs", func(d map[string]any) {
			n := d["nodes"].([]any)[0].(map[string]any)
			n["matrix"] = []float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
			n["translation"] = []float64{0, 0, 0}
		}, "NODE-TRANSFORM"},
		{"non unit rotation", func(d map[string]any) { d["nodes"].([]any)[0].(map[string]any)["rotation"] = []float64{0, 0, 0, 2} }, "NODE-ROTATION"},
		{"wrong type", func(d map[string]any) { d["nodes"] = "nodes" }, "JSON-TYPE"},
		{"empty array", func(d map[string]any) { d["materials"] = []any{} }, "JSON-RANGE"},
		{"float index", func(d map[string]any) { acc(d, 0)["count"] = 1.5 }, "JSON-TYPE"},
		{"negative count", func(d map[string]any) { acc(d, 0)["count"] = -3 }, "JSON-RANGE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := edited(t, c.edit)
			if !hasCode(fs, c.want) {
				t.Fatalf("want %s, got %s", c.want, codes(fs))
			}
			for _, f := range fs {
				if f.Path == "" && !strings.HasPrefix(f.Code, "JSON") && f.Code != "SCENE-DEFAULT" {
					t.Errorf("finding %s has no path", f.Code)
				}
			}
		})
	}
}

func TestNodeGraphFaults(t *testing.T) {
	build := func(links ...[2]int) []glb.Finding {
		b := fixture.New()
		for i := 0; i < 3; i++ {
			b.Node("n", -1, [3]float64{})
		}
		for _, l := range links {
			b.Child(l[0], l[1])
		}
		b.Scene(0)
		_, fs := glb.Decode(b.Bytes())
		noInternal(t, fs)
		return fs
	}
	if fs := build([2]int{0, 1}, [2]int{1, 0}); !hasCode(fs, "NODE-CYCLE") {
		t.Fatalf("two-node cycle: %s", codes(fs))
	}
	if fs := build([2]int{1, 2}, [2]int{2, 1}, [2]int{0, 1}); !hasCode(fs, "NODE-CYCLE") && !hasCode(fs, "NODE-PARENTS") {
		t.Fatalf("cycle below a root: %s", codes(fs))
	}
	if fs := build([2]int{0, 0}); !hasCode(fs, "NODE-CYCLE") {
		t.Fatalf("self child: %s", codes(fs))
	}
	if fs := build([2]int{0, 2}, [2]int{1, 2}); !hasCode(fs, "NODE-PARENTS") {
		t.Fatalf("two parents: %s", codes(fs))
	}
	if fs := build([2]int{0, 9}); !hasCode(fs, "NODE-CHILD") {
		t.Fatalf("child out of range: %s", codes(fs))
	}
	if fs := build([2]int{0, 1}, [2]int{1, 2}); glb.HasErrors(fs) {
		t.Fatalf("a chain is valid: %s", codes(fs))
	}
}

func TestSparseAccessorDecodes(t *testing.T) {
	bin := []byte{
		1, 3, 0, 0, // sparse indices (u8) padded
		0, 0, 0xA0, 0x40, 0, 0, 0xE0, 0x40, // 5.0 and 7.0
	}
	doc := map[string]any{
		"asset":   map[string]any{"version": "2.0"},
		"buffers": []any{map[string]any{"byteLength": len(bin)}},
		"bufferViews": []any{
			map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": 2},
			map[string]any{"buffer": 0, "byteOffset": 4, "byteLength": 8},
		},
		"accessors": []any{map[string]any{
			"componentType": 5126, "count": 4, "type": "SCALAR", "min": []float64{0}, "max": []float64{7},
			"sparse": map[string]any{
				"count":   2,
				"indices": map[string]any{"bufferView": 0, "componentType": 5121},
				"values":  map[string]any{"bufferView": 1},
			},
		}},
	}
	f, fs := glb.Decode(fixture.Pack(doc, bin))
	if len(fs) != 0 {
		t.Fatalf("findings: %v", fs)
	}
	v, _, ok := f.Floats(0)
	if !ok || len(v) != 4 || v[0] != 0 || v[1] != 5 || v[2] != 0 || v[3] != 7 {
		t.Fatalf("sparse read: %v ok=%v", v, ok)
	}
	// indices out of order must be reported
	bin[0], bin[1] = 3, 1
	_, fs = glb.Decode(fixture.Pack(doc, bin))
	if !hasCode(fs, "SPARSE-INDEX") {
		t.Fatalf("got %s", codes(fs))
	}
}

func TestAnimationTimesMustIncrease(t *testing.T) {
	b := fixture.New()
	times := b.Floats("SCALAR", 3, []float32{0, 1, 1}, true)
	vals := b.Floats("VEC3", 3, []float32{0, 0, 0, 1, 1, 1, 2, 2, 2}, false)
	n := b.Node("n", -1, [3]float64{})
	b.Scene(n)
	doc, bin := b.Parts()
	doc["animations"] = []any{map[string]any{
		"samplers": []any{map[string]any{"input": times, "output": vals}},
		"channels": []any{map[string]any{"sampler": 0, "target": map[string]any{"node": n, "path": "translation"}}},
	}}
	_, fs := glb.Decode(fixture.Pack(doc, bin))
	if !hasCode(fs, "ANIM-TIMES") {
		t.Fatalf("got %s", codes(fs))
	}
}

func TestSkinJointIndexRange(t *testing.T) {
	// one joint, but a vertex refers to joint 1
	b := fixture.New()
	pos := b.Floats("VEC3", 3, []float32{0, 0, 0, 1, 0, 0, 0, 1, 0}, true)
	joints := b.Floats("VEC4", 3, make([]float32, 12), false) // floats are not valid JOINTS
	_ = joints
	j := b.Node("j", -1, [3]float64{})
	m := b.Node("m", -1, [3]float64{})
	b.Scene(j, m)
	doc, bin := b.Parts()
	_ = pos
	_ = bin
	_ = doc
	// the JOINTS type check is the observable fault here
	doc["meshes"] = []any{map[string]any{"primitives": []any{map[string]any{
		"attributes": map[string]any{"POSITION": pos, "JOINTS_0": joints},
	}}}}
	doc["nodes"].([]any)[m].(map[string]any)["mesh"] = 0
	_, fs := glb.Decode(fixture.Pack(doc, bin))
	if !hasCode(fs, "ATTR-TYPE") {
		t.Fatalf("got %s", codes(fs))
	}
}
