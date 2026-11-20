package gltf

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

type jAsset struct {
	Version   string `json:"version"`
	Generator string `json:"generator"`
	Copyright string `json:"copyright"`
}
type jScene struct {
	Name   string          `json:"name"`
	Nodes  []int           `json:"nodes"`
	Extras json.RawMessage `json:"extras"`
}
type jNode struct {
	Name        string                     `json:"name"`
	Mesh        *int                       `json:"mesh"`
	Children    []int                      `json:"children"`
	Translation []float64                  `json:"translation"`
	Rotation    []float64                  `json:"rotation"`
	Scale       []float64                  `json:"scale"`
	Matrix      []float64                  `json:"matrix"`
	Extras      json.RawMessage            `json:"extras"`
	Extensions  map[string]json.RawMessage `json:"extensions"`
}
type jPrim struct {
	Attributes map[string]int  `json:"attributes"`
	Indices    *int            `json:"indices"`
	Material   *int            `json:"material"`
	Mode       *int            `json:"mode"`
	Extras     json.RawMessage `json:"extras"`
}
type jMesh struct {
	Name       string          `json:"name"`
	Primitives []jPrim         `json:"primitives"`
	Extras     json.RawMessage `json:"extras"`
}
type jTexInfo struct {
	Index    *int `json:"index"`
	TexCoord int  `json:"texCoord"`
}
type jPBR struct {
	BaseColorFactor  []float64 `json:"baseColorFactor"`
	BaseColorTexture *jTexInfo `json:"baseColorTexture"`
	MetallicFactor   *float64  `json:"metallicFactor"`
	RoughnessFactor  *float64  `json:"roughnessFactor"`
}
type jMaterial struct {
	Name       string                     `json:"name"`
	PBR        *jPBR                      `json:"pbrMetallicRoughness"`
	Extras     json.RawMessage            `json:"extras"`
	Extensions map[string]json.RawMessage `json:"extensions"`
}
type jTexture struct {
	Name    string `json:"name"`
	Sampler *int   `json:"sampler"`
	Source  *int   `json:"source"`
}
type jImage struct {
	Name       string `json:"name"`
	URI        string `json:"uri"`
	MimeType   string `json:"mimeType"`
	BufferView *int   `json:"bufferView"`
}
type jSampler struct {
	MagFilter int `json:"magFilter"`
	MinFilter int `json:"minFilter"`
	WrapS     int `json:"wrapS"`
	WrapT     int `json:"wrapT"`
}
type jBuffer struct {
	URI        string `json:"uri"`
	ByteLength *int64 `json:"byteLength"`
}
type jView struct {
	Buffer     *int   `json:"buffer"`
	ByteOffset int64  `json:"byteOffset"`
	ByteLength *int64 `json:"byteLength"`
	ByteStride int64  `json:"byteStride"`
}
type jAccessor struct {
	BufferView    *int            `json:"bufferView"`
	ByteOffset    int64           `json:"byteOffset"`
	ComponentType int             `json:"componentType"`
	Normalized    bool            `json:"normalized"`
	Count         int64           `json:"count"`
	Type          string          `json:"type"`
	Sparse        json.RawMessage `json:"sparse"`
}
type jDoc struct {
	Asset       *jAsset         `json:"asset"`
	Scene       *int            `json:"scene"`
	Scenes      []jScene        `json:"scenes"`
	Nodes       []jNode         `json:"nodes"`
	Meshes      []jMesh         `json:"meshes"`
	Materials   []jMaterial     `json:"materials"`
	Textures    []jTexture      `json:"textures"`
	Images      []jImage        `json:"images"`
	Samplers    []jSampler      `json:"samplers"`
	Buffers     []jBuffer       `json:"buffers"`
	BufferViews []jView         `json:"bufferViews"`
	Accessors   []jAccessor     `json:"accessors"`
	Extras      json.RawMessage `json:"extras"`
}

type decoder struct {
	j   jDoc
	bin []byte
}

// Decode checks b as a GLB file and returns its Document. It returns an
// *Error that names the JSON path of the first fault, and it does not panic on
// any input. See the package comment for what is read and what is dropped.
func Decode(b []byte) (doc *Document, err error) {
	defer func() {
		if r := recover(); r != nil { // a bug guard: hostile input must not panic
			doc, err = nil, errf("glb", "internal error: %v", r)
		}
	}()
	jsonChunk, bin, err := splitGLB(b)
	if err != nil {
		return nil, err
	}
	d := &decoder{bin: bin}
	if err := json.Unmarshal(jsonChunk, &d.j); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) {
			path := te.Field
			if path == "" {
				path = "json"
			}
			return nil, errf(path, "wrong JSON type: %s for %s", te.Value, te.Type)
		}
		return nil, errf("json", "%v", err)
	}
	return d.build()
}

func splitGLB(b []byte) (jsonChunk, bin []byte, err error) {
	if len(b) < 12 {
		return nil, nil, errf("glb", "file is %d bytes; the header needs 12", len(b))
	}
	if binary.LittleEndian.Uint32(b) != glbMagic {
		return nil, nil, errf("glb.magic", "not a GLB file (want \"glTF\")")
	}
	if v := binary.LittleEndian.Uint32(b[4:]); v != 2 {
		return nil, nil, errf("glb.version", "version %d is not supported (want 2)", v)
	}
	if l := binary.LittleEndian.Uint32(b[8:]); uint64(l) != uint64(len(b)) {
		return nil, nil, errf("glb.length", "header says %d bytes, file has %d", l, len(b))
	}
	pos := 12
	n := 0
	for pos < len(b) {
		if len(b)-pos < 8 {
			return nil, nil, errf(fmt.Sprintf("glb.chunks[%d]", n), "truncated chunk header")
		}
		clen := uint64(binary.LittleEndian.Uint32(b[pos:]))
		ctype := binary.LittleEndian.Uint32(b[pos+4:])
		pos += 8
		p := fmt.Sprintf("glb.chunks[%d]", n)
		if clen%4 != 0 {
			return nil, nil, errf(p, "length %d is not a multiple of 4", clen)
		}
		if clen > uint64(len(b)-pos) {
			return nil, nil, errf(p, "length %d runs past the end of the file", clen)
		}
		data := b[pos : pos+int(clen)]
		pos += int(clen)
		switch {
		case n == 0:
			if ctype != chunkJSON {
				return nil, nil, errf(p, "first chunk must be JSON")
			}
			jsonChunk = data
		case n == 1 && ctype == chunkBIN:
			bin = data
		case ctype == chunkJSON || ctype == chunkBIN:
			return nil, nil, errf(p, "unexpected extra JSON or BIN chunk")
		}
		n++
	}
	if n == 0 {
		return nil, nil, errf("glb.chunks", "no JSON chunk")
	}
	return jsonChunk, bin, nil
}

var compSize = map[int]int{5120: 1, 5121: 1, 5122: 2, 5123: 2, 5125: 4, 5126: 4}
var typeCount = map[string]int{"SCALAR": 1, "VEC2": 2, "VEC3": 3, "VEC4": 4, "MAT2": 4, "MAT3": 9, "MAT4": 16}

// span is the checked window of bytes behind an accessor.
type span struct {
	data   []byte
	stride int
	esize  int
	count  int
}

func (d *decoder) view(path string, idx int) (*jView, []byte, error) {
	vp := fmt.Sprintf("bufferViews[%d]", idx)
	if idx < 0 || idx >= len(d.j.BufferViews) {
		return nil, nil, errf(path, "bufferView %d out of range (have %d)", idx, len(d.j.BufferViews))
	}
	v := &d.j.BufferViews[idx]
	if v.Buffer == nil || *v.Buffer != 0 {
		return nil, nil, at(path, vp+".buffer", "only buffer 0 (the GLB binary chunk) is supported")
	}
	if v.ByteLength == nil || *v.ByteLength < 1 {
		return nil, nil, at(path, vp+".byteLength", "missing or not positive")
	}
	if v.ByteOffset < 0 || v.ByteOffset > int64(len(d.bin)) {
		return nil, nil, at(path, vp+".byteOffset", "%d is outside the %d-byte buffer", v.ByteOffset, len(d.bin))
	}
	if *v.ByteLength > int64(len(d.bin))-v.ByteOffset {
		return nil, nil, at(path, vp+".byteLength", "%d bytes from offset %d run past the %d-byte buffer", *v.ByteLength, v.ByteOffset, len(d.bin))
	}
	if v.ByteStride != 0 && (v.ByteStride < 4 || v.ByteStride > 252 || v.ByteStride%4 != 0) {
		return nil, nil, at(path, vp+".byteStride", "%d must be 4..252 and a multiple of 4", v.ByteStride)
	}
	return v, d.bin[v.ByteOffset : v.ByteOffset+*v.ByteLength], nil
}

func (d *decoder) accessor(path string, idx int, typ string, comps ...int) (*span, error) {
	if idx < 0 || idx >= len(d.j.Accessors) {
		return nil, errf(path, "accessor %d out of range (have %d)", idx, len(d.j.Accessors))
	}
	ap := fmt.Sprintf("accessors[%d]", idx)
	a := &d.j.Accessors[idx]
	cs, okc := compSize[a.ComponentType]
	if !okc {
		return nil, at(path, ap+".componentType", "unknown component type %d", a.ComponentType)
	}
	tc, okt := typeCount[a.Type]
	if !okt {
		return nil, at(path, ap+".type", "unknown type %q", a.Type)
	}
	if a.Type != typ {
		return nil, at(path, ap+".type", "%s is used here; need %s", a.Type, typ)
	}
	allowed := false
	for _, c := range comps {
		allowed = allowed || c == a.ComponentType
	}
	if !allowed {
		return nil, at(path, ap+".componentType", "%d is not allowed here (allowed %v)", a.ComponentType, comps)
	}
	if a.Normalized {
		return nil, at(path, ap+".normalized", "normalized data is not supported")
	}
	if len(a.Sparse) > 0 && string(a.Sparse) != "null" {
		return nil, at(path, ap+".sparse", "sparse accessors are not supported")
	}
	if a.BufferView == nil {
		return nil, at(path, ap+".bufferView", "missing; zero-filled accessors are not supported")
	}
	if a.Count < 1 || a.Count > 1<<32 {
		return nil, at(path, ap+".count", "%d is out of range", a.Count)
	}
	v, data, err := d.view(path, *a.BufferView)
	if err != nil {
		return nil, err
	}
	esize := cs * tc
	stride := esize
	if v.ByteStride != 0 {
		stride = int(v.ByteStride)
		if stride < esize {
			return nil, at(path, fmt.Sprintf("bufferViews[%d].byteStride", *a.BufferView), "%d is smaller than the element size %d", stride, esize)
		}
	}
	if a.ByteOffset < 0 || a.ByteOffset > int64(len(data)) {
		return nil, at(path, ap+".byteOffset", "%d is outside the %d-byte bufferView", a.ByteOffset, len(data))
	}
	if (v.ByteOffset+a.ByteOffset)%int64(cs) != 0 {
		return nil, at(path, ap+".byteOffset", "offset %d is not a multiple of the component size %d", v.ByteOffset+a.ByteOffset, cs)
	}
	need := a.ByteOffset + (a.Count-1)*int64(stride) + int64(esize)
	if need > int64(len(data)) {
		return nil, at(path, ap+".count", "%d elements need %d bytes; the bufferView has %d", a.Count, need, len(data))
	}
	return &span{data: data[a.ByteOffset:], stride: stride, esize: esize, count: int(a.Count)}, nil
}

func (s *span) floats(path string, comps int) ([]float32, error) {
	out := make([]float32, 0, s.count*comps)
	for i := 0; i < s.count; i++ {
		el := s.data[i*s.stride:]
		for c := 0; c < comps; c++ {
			f := math.Float32frombits(binary.LittleEndian.Uint32(el[4*c:]))
			if !finite32(f) {
				return nil, errf(fmt.Sprintf("%s[%d]", path, i*comps+c), "value is %v; NaN and infinity are not allowed", f)
			}
			if f == 0 {
				f = 0
			}
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *span) indices(comp int) []uint32 {
	out := make([]uint32, s.count)
	for i := range out {
		el := s.data[i*s.stride:]
		switch comp {
		case compUbyte:
			out[i] = uint32(el[0])
		case compUshort:
			out[i] = uint32(binary.LittleEndian.Uint16(el))
		default:
			out[i] = binary.LittleEndian.Uint32(el)
		}
	}
	return out
}

// at reports a fault found at loc (an accessor or bufferView field) while
// reading the JSON path where it was referenced.
func at(path, loc, format string, args ...any) *Error {
	return errf(path, loc+": "+format, args...)
}

func f32s(path string, in []float64, n int) ([]float32, error) {
	if len(in) != n {
		return nil, errf(path, "need %d numbers, have %d", n, len(in))
	}
	out := make([]float32, n)
	for i, v := range in {
		f := float32(v)
		if !finite32(f) {
			return nil, errf(fmt.Sprintf("%s[%d]", path, i), "number %v does not fit a float32", v)
		}
		if f == 0 {
			f = 0
		}
		out[i] = f
	}
	return out, nil
}

func (d *decoder) build() (*Document, error) {
	j := &d.j
	if j.Asset == nil {
		return nil, errf("asset", "missing")
	}
	if !strings.HasPrefix(j.Asset.Version, "2.") && j.Asset.Version != "2" {
		return nil, errf("asset.version", "%q is not glTF 2.x", j.Asset.Version)
	}
	if len(j.Buffers) > 1 {
		return nil, errf("buffers", "only one buffer is supported, have %d", len(j.Buffers))
	}
	if len(j.Buffers) == 1 {
		b := j.Buffers[0]
		if b.URI != "" {
			return nil, errf("buffers[0].uri", "external and data-URI buffers are not supported")
		}
		if b.ByteLength == nil || *b.ByteLength < 1 {
			return nil, errf("buffers[0].byteLength", "missing or not positive")
		}
		if *b.ByteLength > int64(len(d.bin)) || *b.ByteLength < int64(len(d.bin))-3 {
			return nil, errf("buffers[0].byteLength", "%d does not match the %d-byte binary chunk", *b.ByteLength, len(d.bin))
		}
		d.bin = d.bin[:*b.ByteLength]
	} else {
		d.bin = nil
	}

	doc := &Document{Asset: Asset{Generator: j.Asset.Generator, Copyright: j.Asset.Copyright}, Scene: j.Scene}
	var err error
	if doc.Extras, err = canonicalJSON(j.Extras); err != nil {
		return nil, errf("extras", "invalid JSON: %v", err)
	}
	for i, s := range j.Scenes {
		sc := Scene{Name: s.Name, Nodes: nilInts(s.Nodes)}
		if sc.Extras, err = canonicalJSON(s.Extras); err != nil {
			return nil, errf(fmt.Sprintf("scenes[%d].extras", i), "invalid JSON: %v", err)
		}
		doc.Scenes = append(doc.Scenes, sc)
	}
	for i, n := range j.Nodes {
		p := fmt.Sprintf("nodes[%d]", i)
		nd := Node{Name: n.Name, Mesh: n.Mesh, Children: nilInts(n.Children)}
		if nd.Extras, err = canonicalJSON(n.Extras); err != nil {
			return nil, errf(p+".extras", "invalid JSON: %v", err)
		}
		if nd.Extensions, err = canonicalExt(n.Extensions, p+".extensions"); err != nil {
			return nil, err
		}
		hasTRS := n.Translation != nil || n.Rotation != nil || n.Scale != nil
		if n.Matrix != nil && hasTRS {
			return nil, errf(p+".matrix", "a node may not have both matrix and translation, rotation or scale")
		}
		if n.Matrix != nil {
			if len(n.Matrix) != 16 {
				return nil, errf(p+".matrix", "need 16 numbers, have %d", len(n.Matrix))
			}
			t, r, s, ok := decomposeMatrix(n.Matrix)
			if !ok {
				return nil, errf(p+".matrix", "not a translation-rotation-scale matrix (shear, zero scale, bad last row or non-finite)")
			}
			nd.Translation, nd.Rotation, nd.Scale = &t, &r, &s
		}
		if n.Translation != nil {
			v, err := f32s(p+".translation", n.Translation, 3)
			if err != nil {
				return nil, err
			}
			nd.Translation = &[3]float32{v[0], v[1], v[2]}
		}
		if n.Rotation != nil {
			v, err := f32s(p+".rotation", n.Rotation, 4)
			if err != nil {
				return nil, err
			}
			nd.Rotation = &[4]float32{v[0], v[1], v[2], v[3]}
		}
		if n.Scale != nil {
			v, err := f32s(p+".scale", n.Scale, 3)
			if err != nil {
				return nil, err
			}
			nd.Scale = &[3]float32{v[0], v[1], v[2]}
		}
		doc.Nodes = append(doc.Nodes, nd)
	}
	for i, m := range j.Meshes {
		mp := fmt.Sprintf("meshes[%d]", i)
		me := Mesh{Name: m.Name}
		if me.Extras, err = canonicalJSON(m.Extras); err != nil {
			return nil, errf(mp+".extras", "invalid JSON: %v", err)
		}
		if len(m.Primitives) == 0 {
			return nil, errf(mp+".primitives", "a mesh needs at least one primitive")
		}
		for k, pr := range m.Primitives {
			pp := fmt.Sprintf("%s.primitives[%d]", mp, k)
			out := Primitive{Material: pr.Material}
			if pr.Mode != nil && *pr.Mode != 4 {
				return nil, errf(pp+".mode", "mode %d is not supported (only 4, triangles)", *pr.Mode)
			}
			if out.Extras, err = canonicalJSON(pr.Extras); err != nil {
				return nil, errf(pp+".extras", "invalid JSON: %v", err)
			}
			pos, ok := pr.Attributes["POSITION"]
			if !ok {
				return nil, errf(pp+".attributes.POSITION", "missing")
			}
			sp, err := d.accessor(pp+".attributes.POSITION", pos, "VEC3", compFloat)
			if err != nil {
				return nil, err
			}
			nv := sp.count
			if out.Positions, err = sp.floats(pp+".attributes.POSITION", 3); err != nil {
				return nil, err
			}
			if n, ok := pr.Attributes["NORMAL"]; ok {
				sp, err := d.accessor(pp+".attributes.NORMAL", n, "VEC3", compFloat)
				if err != nil {
					return nil, err
				}
				if sp.count != nv {
					return nil, errf(pp+".attributes.NORMAL", "count %d differs from POSITION count %d", sp.count, nv)
				}
				if out.Normals, err = sp.floats(pp+".attributes.NORMAL", 3); err != nil {
					return nil, err
				}
			}
			if n, ok := pr.Attributes["TEXCOORD_0"]; ok {
				sp, err := d.accessor(pp+".attributes.TEXCOORD_0", n, "VEC2", compFloat)
				if err != nil {
					return nil, err
				}
				if sp.count != nv {
					return nil, errf(pp+".attributes.TEXCOORD_0", "count %d differs from POSITION count %d", sp.count, nv)
				}
				if out.UV0, err = sp.floats(pp+".attributes.TEXCOORD_0", 2); err != nil {
					return nil, err
				}
			}
			if pr.Indices != nil {
				sp, err := d.accessor(pp+".indices", *pr.Indices, "SCALAR", compUbyte, compUshort, compUint)
				if err != nil {
					return nil, err
				}
				out.Indices = sp.indices(d.j.Accessors[*pr.Indices].ComponentType)
				for q, v := range out.Indices {
					if int64(v) >= int64(nv) {
						return nil, errf(pp+".indices", "index %d (value %d) is not below vertex count %d", q, v, nv)
					}
				}
				if len(out.Indices)%3 != 0 {
					return nil, errf(pp+".indices", "triangle list needs a multiple of 3 indices, have %d", len(out.Indices))
				}
			} else if nv%3 != 0 {
				return nil, errf(pp+".attributes.POSITION", "non-indexed triangle list needs a multiple of 3 vertices, have %d", nv)
			}
			me.Primitives = append(me.Primitives, out)
		}
		doc.Meshes = append(doc.Meshes, me)
	}
	for i, m := range j.Materials {
		p := fmt.Sprintf("materials[%d]", i)
		mt := Material{Name: m.Name, BaseColor: [4]float32{1, 1, 1, 1}, Metallic: 1, Roughness: 1}
		if mt.Extras, err = canonicalJSON(m.Extras); err != nil {
			return nil, errf(p+".extras", "invalid JSON: %v", err)
		}
		if mt.Extensions, err = canonicalExt(m.Extensions, p+".extensions"); err != nil {
			return nil, err
		}
		if m.PBR != nil {
			pp := p + ".pbrMetallicRoughness"
			if m.PBR.BaseColorFactor != nil {
				v, err := f32s(pp+".baseColorFactor", m.PBR.BaseColorFactor, 4)
				if err != nil {
					return nil, err
				}
				copy(mt.BaseColor[:], v)
			}
			if m.PBR.MetallicFactor != nil {
				v, err := f32s(pp+".metallicFactor", []float64{*m.PBR.MetallicFactor}, 1)
				if err != nil {
					return nil, err
				}
				mt.Metallic = v[0]
			}
			if m.PBR.RoughnessFactor != nil {
				v, err := f32s(pp+".roughnessFactor", []float64{*m.PBR.RoughnessFactor}, 1)
				if err != nil {
					return nil, err
				}
				mt.Roughness = v[0]
			}
			if t := m.PBR.BaseColorTexture; t != nil {
				if t.Index == nil {
					return nil, errf(pp+".baseColorTexture.index", "missing")
				}
				if t.TexCoord != 0 {
					return nil, errf(pp+".baseColorTexture.texCoord", "only texCoord 0 is supported")
				}
				mt.BaseColorTexture = t.Index
			}
		}
		doc.Materials = append(doc.Materials, mt)
	}
	for _, t := range j.Textures {
		doc.Textures = append(doc.Textures, Texture{Name: t.Name, Sampler: t.Sampler, Source: t.Source})
	}
	for i, im := range j.Images {
		p := fmt.Sprintf("images[%d]", i)
		out := Image{Name: im.Name, MimeType: im.MimeType, URI: im.URI}
		if im.BufferView != nil {
			if im.URI != "" {
				return nil, errf(p, "has both uri and bufferView")
			}
			_, data, err := d.view(p+".bufferView", *im.BufferView)
			if err != nil {
				return nil, err
			}
			out.Data = append([]byte(nil), data...)
		}
		doc.Images = append(doc.Images, out)
	}
	for _, s := range j.Samplers {
		doc.Samplers = append(doc.Samplers, Sampler{MagFilter: s.MagFilter, MinFilter: s.MinFilter, WrapS: s.WrapS, WrapT: s.WrapT})
	}
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	return doc, nil
}

func nilInts(v []int) []int {
	if len(v) == 0 {
		return nil
	}
	return v
}
