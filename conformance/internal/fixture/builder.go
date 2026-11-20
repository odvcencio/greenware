// Package fixture hand-assembles small GLB files for the oracle's tests. It
// writes bytes directly from the glTF 2.0 specification and imports nothing
// from Greenware. It is used by tests only.
package fixture

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
)

// Prim is one triangle-list primitive.
type Prim struct {
	Pos      [][3]float32
	Normals  [][3]float32 // optional
	Idx      []uint32
	Material int // -1 for none
	// Wide forces 32-bit indices; otherwise 16-bit when they fit.
	Wide bool
}

// Builder accumulates a glTF document and its binary chunk.
type Builder struct {
	bin        []byte
	accessors  []any
	views      []any
	meshes     []any
	materials  []any
	nodes      []map[string]any
	scenes     []any
	skins      []any
	animations []any
}

// New returns an empty builder.
func New() *Builder { return &Builder{} }

func (b *Builder) pad4() {
	for len(b.bin)%4 != 0 {
		b.bin = append(b.bin, 0)
	}
}

func (b *Builder) view(data []byte, target int) int {
	b.pad4()
	v := map[string]any{"buffer": 0, "byteOffset": len(b.bin), "byteLength": len(data)}
	if target != 0 {
		v["target"] = target
	}
	b.bin = append(b.bin, data...)
	b.views = append(b.views, v)
	return len(b.views) - 1
}

func (b *Builder) acc(a map[string]any) int {
	b.accessors = append(b.accessors, a)
	return len(b.accessors) - 1
}

// Floats adds a FLOAT accessor of the given type ("VEC3" and so on) and
// returns its index. When minmax is true it writes the exact extrema.
func (b *Builder) Floats(typ string, n int, vals []float32, minmax bool) int {
	buf := new(bytes.Buffer)
	for _, v := range vals {
		_ = binary.Write(buf, binary.LittleEndian, v)
	}
	comps := map[string]int{"SCALAR": 1, "VEC2": 2, "VEC3": 3, "VEC4": 4}[typ]
	a := map[string]any{"bufferView": b.view(buf.Bytes(), 34962), "componentType": 5126, "count": n, "type": typ}
	finite := true
	for _, v := range vals {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			finite = false
		}
	}
	if minmax && n > 0 && finite {
		lo, hi := make([]float64, comps), make([]float64, comps)
		for k := 0; k < comps; k++ {
			lo[k], hi[k] = math.Inf(1), math.Inf(-1)
			for e := 0; e < n; e++ {
				x := float64(vals[e*comps+k])
				lo[k], hi[k] = math.Min(lo[k], x), math.Max(hi[k], x)
			}
		}
		a["min"], a["max"] = lo, hi
	}
	return b.acc(a)
}

// Material adds a PBR material.
func (b *Builder) Material(name string, rgba [4]float64) int {
	b.materials = append(b.materials, map[string]any{
		"name": name, "pbrMetallicRoughness": map[string]any{"baseColorFactor": rgba[:]},
	})
	return len(b.materials) - 1
}

// Mesh adds a mesh with the given primitives.
func (b *Builder) Mesh(name string, prims ...Prim) int {
	var ps []any
	for _, p := range prims {
		flat := make([]float32, 0, len(p.Pos)*3)
		for _, v := range p.Pos {
			flat = append(flat, v[:]...)
		}
		attrs := map[string]any{"POSITION": b.Floats("VEC3", len(p.Pos), flat, true)}
		if p.Normals != nil {
			nf := make([]float32, 0, len(p.Normals)*3)
			for _, v := range p.Normals {
				nf = append(nf, v[:]...)
			}
			attrs["NORMAL"] = b.Floats("VEC3", len(p.Normals), nf, false)
		}
		buf := new(bytes.Buffer)
		ct := 5123
		if p.Wide || len(p.Pos) > 65535 {
			ct = 5125
		}
		for _, i := range p.Idx {
			if ct == 5123 {
				_ = binary.Write(buf, binary.LittleEndian, uint16(i))
			} else {
				_ = binary.Write(buf, binary.LittleEndian, i)
			}
		}
		idx := b.acc(map[string]any{"bufferView": b.view(buf.Bytes(), 34963), "componentType": ct, "count": len(p.Idx), "type": "SCALAR"})
		prim := map[string]any{"attributes": attrs, "indices": idx, "mode": 4}
		if p.Material >= 0 {
			prim["material"] = p.Material
		}
		ps = append(ps, prim)
	}
	b.meshes = append(b.meshes, map[string]any{"name": name, "primitives": ps})
	return len(b.meshes) - 1
}

// Node adds a node. mesh<0 means no mesh.
func (b *Builder) Node(name string, mesh int, t [3]float64) int {
	n := map[string]any{"name": name}
	if mesh >= 0 {
		n["mesh"] = mesh
	}
	if t != [3]float64{} {
		n["translation"] = t[:]
	}
	b.nodes = append(b.nodes, n)
	return len(b.nodes) - 1
}

// Child links child under parent.
func (b *Builder) Child(parent, child int) {
	ch, _ := b.nodes[parent]["children"].([]int)
	b.nodes[parent]["children"] = append(ch, child)
}

// Set changes a node property directly (for rotations, extras, and so on).
func (b *Builder) Set(node int, key string, v any) { b.nodes[node][key] = v }

// Scene adds a scene listing roots.
func (b *Builder) Scene(roots ...int) {
	b.scenes = append(b.scenes, map[string]any{"nodes": roots})
}

// Parts returns the glTF JSON object and binary chunk for tests to edit.
func (b *Builder) Parts() (map[string]any, []byte) {
	b.pad4()
	doc := map[string]any{"asset": map[string]any{"version": "2.0"}}
	if len(b.nodes) > 0 {
		ns := make([]any, len(b.nodes))
		for i, n := range b.nodes {
			ns[i] = n
		}
		doc["nodes"] = ns
	}
	if len(b.scenes) > 0 {
		doc["scenes"], doc["scene"] = b.scenes, 0
	}
	if len(b.meshes) > 0 {
		doc["meshes"] = b.meshes
	}
	if len(b.materials) > 0 {
		doc["materials"] = b.materials
	}
	if len(b.skins) > 0 {
		doc["skins"] = b.skins
	}
	if len(b.animations) > 0 {
		doc["animations"] = b.animations
	}
	if len(b.accessors) > 0 {
		doc["accessors"] = b.accessors
		doc["bufferViews"] = b.views
		doc["buffers"] = []any{map[string]any{"byteLength": len(b.bin)}}
	}
	return doc, b.bin
}

// Bytes returns the finished GLB.
func (b *Builder) Bytes() []byte {
	doc, bin := b.Parts()
	return Pack(doc, bin)
}

// Pack serialises a JSON object and binary chunk into a GLB.
func Pack(doc map[string]any, bin []byte) []byte {
	js, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return PackRaw(js, bin)
}

// PackRaw writes a GLB around raw JSON text, padding it with spaces.
func PackRaw(js, bin []byte) []byte {
	for len(js)%4 != 0 {
		js = append(js, ' ')
	}
	for len(bin)%4 != 0 {
		bin = append(bin, 0)
	}
	total := 12 + 8 + len(js)
	if len(bin) > 0 {
		total += 8 + len(bin)
	}
	out := make([]byte, 0, total)
	le := binary.LittleEndian
	out = le.AppendUint32(out, 0x46546C67)
	out = le.AppendUint32(out, 2)
	out = le.AppendUint32(out, uint32(total))
	out = le.AppendUint32(out, uint32(len(js)))
	out = le.AppendUint32(out, 0x4E4F534A)
	out = append(out, js...)
	if len(bin) > 0 {
		out = le.AppendUint32(out, uint32(len(bin)))
		out = le.AppendUint32(out, 0x004E4942)
		out = append(out, bin...)
	}
	return out
}
