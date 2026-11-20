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
	Skin        *int                       `json:"skin"`
	Children    []int                      `json:"children"`
	Translation []float64                  `json:"translation"`
	Rotation    []float64                  `json:"rotation"`
	Scale       []float64                  `json:"scale"`
	Matrix      []float64                  `json:"matrix"`
	Extras      json.RawMessage            `json:"extras"`
	Extensions  map[string]json.RawMessage `json:"extensions"`
}
type jPrim struct {
	Attributes map[string]int   `json:"attributes"`
	Indices    *int             `json:"indices"`
	Material   *int             `json:"material"`
	Mode       *int             `json:"mode"`
	Targets    []map[string]int `json:"targets"`
	Extras     json.RawMessage  `json:"extras"`
}
type jMesh struct {
	Name       string          `json:"name"`
	Primitives []jPrim         `json:"primitives"`
	Weights    []float64       `json:"weights"`
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
	Emissive   []float64                  `json:"emissiveFactor"`
	AlphaMode  string                     `json:"alphaMode"`
	AlphaCut   *float64                   `json:"alphaCutoff"`
	DoubleSide bool                       `json:"doubleSided"`
	Extras     json.RawMessage            `json:"extras"`
	Extensions map[string]json.RawMessage `json:"extensions"`
}
type jTexture struct {
	Name    string          `json:"name"`
	Sampler *int            `json:"sampler"`
	Source  *int            `json:"source"`
	Extras  json.RawMessage `json:"extras"`
}
type jImage struct {
	Name       string          `json:"name"`
	URI        string          `json:"uri"`
	MimeType   string          `json:"mimeType"`
	BufferView *int            `json:"bufferView"`
	Extras     json.RawMessage `json:"extras"`
}
type jSampler struct {
	MagFilter int `json:"magFilter"`
	MinFilter int `json:"minFilter"`
	WrapS     int `json:"wrapS"`
	WrapT     int `json:"wrapT"`
}
type jSkin struct {
	Name                string `json:"name"`
	Joints              []int  `json:"joints"`
	InverseBindMatrices *int   `json:"inverseBindMatrices"`
	Skeleton            *int   `json:"skeleton"`
}
type jChannel struct {
	Sampler *int `json:"sampler"`
	Target  *struct {
		Node *int   `json:"node"`
		Path string `json:"path"`
	} `json:"target"`
}
type jAnimSampler struct {
	Input         *int   `json:"input"`
	Output        *int   `json:"output"`
	Interpolation string `json:"interpolation"`
}
type jAnimation struct {
	Name     string         `json:"name"`
	Channels []jChannel     `json:"channels"`
	Samplers []jAnimSampler `json:"samplers"`
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
	Skins       []jSkin         `json:"skins"`
	Animations  []jAnimation    `json:"animations"`
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
	return d.accessorN(path, idx, typ, false, comps...)
}

// accessorN is accessor with a choice: when normOK is true a normalized
// accessor is accepted (the caller scales the integers); when it is false a
// normalized accessor is an error.
func (d *decoder) accessorN(path string, idx int, typ string, normOK bool, comps ...int) (*span, error) {
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
	if a.Normalized && !normOK {
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

// uints reads VEC4 ubyte or ushort data as flat uint16 values.
func (s *span) uints(comp int) []uint16 {
	out := make([]uint16, 0, 4*s.count)
	for i := 0; i < s.count; i++ {
		el := s.data[i*s.stride:]
		for c := 0; c < 4; c++ {
			if comp == compUbyte {
				out = append(out, uint16(el[c]))
			} else {
				out = append(out, binary.LittleEndian.Uint16(el[2*c:]))
			}
		}
	}
	return out
}

// normWeights reads VEC4 normalized ubyte or ushort weights. Each vertex is
// scaled by its integer sum, so the weights add to 1 (up to float rounding)
// even though the integer sum is allowed to miss the full range by a step.
func (s *span) normWeights(comp int) []float32 {
	maxv := float64(255)
	if comp == compUshort {
		maxv = 65535
	}
	raw := s.uints(comp)
	out := make([]float32, len(raw))
	for v := 0; v < s.count; v++ {
		var sum float64
		for c := 0; c < 4; c++ {
			sum += float64(raw[4*v+c])
		}
		for c := 0; c < 4; c++ {
			w := float64(raw[4*v+c])
			if sum != 0 {
				out[4*v+c] = float32(w / sum)
			} else {
				out[4*v+c] = float32(w / maxv)
			}
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
		nd := Node{Name: n.Name, Mesh: n.Mesh, Skin: n.Skin, Children: nilInts(n.Children)}
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
			if err := d.skinAttrs(pp, pr, nv, &out); err != nil {
				return nil, err
			}
			for t, tg := range pr.Targets {
				tp := fmt.Sprintf("%s.targets[%d]", pp, t)
				var mt MorphTarget
				if a, ok := tg["POSITION"]; ok {
					sp, err := d.accessor(tp+".POSITION", a, "VEC3", compFloat)
					if err != nil {
						return nil, err
					}
					if sp.count != nv {
						return nil, errf(tp+".POSITION", "count %d differs from POSITION count %d", sp.count, nv)
					}
					if mt.Positions, err = sp.floats(tp+".POSITION", 3); err != nil {
						return nil, err
					}
				}
				if a, ok := tg["NORMAL"]; ok {
					sp, err := d.accessor(tp+".NORMAL", a, "VEC3", compFloat)
					if err != nil {
						return nil, err
					}
					if sp.count != nv {
						return nil, errf(tp+".NORMAL", "count %d differs from POSITION count %d", sp.count, nv)
					}
					if mt.Normals, err = sp.floats(tp+".NORMAL", 3); err != nil {
						return nil, err
					}
				}
				out.Targets = append(out.Targets, mt)
			}
			me.Primitives = append(me.Primitives, out)
		}
		if len(m.Weights) > 0 {
			if me.Weights, err = f32s(mp+".weights", m.Weights, len(m.Weights)); err != nil {
				return nil, err
			}
		}
		if len(me.Primitives) > 0 {
			me.Extras, me.TargetNames = splitTargetNames(me.Extras, len(me.Primitives[0].Targets))
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
		mt.DoubleSided = m.DoubleSide
		if m.Emissive != nil {
			v, err := f32s(p+".emissiveFactor", m.Emissive, 3)
			if err != nil {
				return nil, err
			}
			copy(mt.EmissiveFactor[:], v)
		}
		switch m.AlphaMode {
		case "", AlphaOpaque:
		case AlphaMask:
			mt.AlphaMode, mt.AlphaCutoff = AlphaMask, 0.5
			if m.AlphaCut != nil {
				v, err := f32s(p+".alphaCutoff", []float64{*m.AlphaCut}, 1)
				if err != nil {
					return nil, err
				}
				mt.AlphaCutoff = v[0]
			}
		default:
			mt.AlphaMode = m.AlphaMode // Validate reports it (BLEND is valid)
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
				mt.BaseColorTexture, mt.BaseColorTexCoord = t.Index, t.TexCoord
			}
		}
		doc.Materials = append(doc.Materials, mt)
	}
	for i, t := range j.Textures {
		tx := Texture{Name: t.Name, Sampler: t.Sampler, Source: t.Source}
		if tx.Extras, err = canonicalJSON(t.Extras); err != nil {
			return nil, errf(fmt.Sprintf("textures[%d].extras", i), "invalid JSON: %v", err)
		}
		doc.Textures = append(doc.Textures, tx)
	}
	for i, im := range j.Images {
		p := fmt.Sprintf("images[%d]", i)
		out := Image{Name: im.Name, MimeType: im.MimeType, URI: im.URI}
		if out.Extras, err = canonicalJSON(im.Extras); err != nil {
			return nil, errf(p+".extras", "invalid JSON: %v", err)
		}
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
	if err := d.rig(doc); err != nil {
		return nil, err
	}
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	return doc, nil
}

// skinAttrs reads JOINTS_0 and WEIGHTS_0 of one primitive into out.
func (d *decoder) skinAttrs(pp string, pr jPrim, nv int, out *Primitive) error {
	ja, hasJ := pr.Attributes["JOINTS_0"]
	wa, hasW := pr.Attributes["WEIGHTS_0"]
	if !hasJ && !hasW {
		return nil
	}
	if !hasJ || !hasW {
		return errf(pp+".attributes", "JOINTS_0 and WEIGHTS_0 must both be present")
	}
	jp, wp := pp+".attributes.JOINTS_0", pp+".attributes.WEIGHTS_0"
	sp, err := d.accessor(jp, ja, "VEC4", compUbyte, compUshort)
	if err != nil {
		return err
	}
	if sp.count != nv {
		return errf(jp, "count %d differs from POSITION count %d", sp.count, nv)
	}
	out.Joints0 = sp.uints(d.j.Accessors[ja].ComponentType)
	sp, err = d.accessorN(wp, wa, "VEC4", true, compFloat, compUbyte, compUshort)
	if err != nil {
		return err
	}
	if sp.count != nv {
		return errf(wp, "count %d differs from POSITION count %d", sp.count, nv)
	}
	comp := d.j.Accessors[wa].ComponentType
	if (comp == compFloat) == d.j.Accessors[wa].Normalized {
		return errf(wp, "accessors[%d].normalized must be true for integer weights and absent for float weights", wa)
	}
	if comp == compFloat {
		out.Weights0, err = sp.floats(wp, 4)
		return err
	}
	out.Weights0 = sp.normWeights(comp)
	return nil
}

// rig reads skins and animations into doc.
func (d *decoder) rig(doc *Document) error {
	j := &d.j
	for i, s := range j.Skins {
		p := fmt.Sprintf("skins[%d]", i)
		sk := Skin{Name: s.Name, Joints: nilInts(s.Joints), Skeleton: s.Skeleton}
		if s.InverseBindMatrices != nil {
			ip := p + ".inverseBindMatrices"
			sp, err := d.accessor(ip, *s.InverseBindMatrices, "MAT4", compFloat)
			if err != nil {
				return err
			}
			if sp.count < len(s.Joints) {
				return errf(ip, "count %d is below the %d joints", sp.count, len(s.Joints))
			}
			sp.count = len(s.Joints) // extra matrices are ignored, as glTF allows
			flat, err := sp.floats(ip, 16)
			if err != nil {
				return err
			}
			for k := 0; k < len(s.Joints); k++ {
				var m [16]float32
				copy(m[:], flat[16*k:])
				sk.InverseBindMatrices = append(sk.InverseBindMatrices, m)
			}
		}
		doc.Skins = append(doc.Skins, sk)
	}
	for i, a := range j.Animations {
		p := fmt.Sprintf("animations[%d]", i)
		an := Animation{Name: a.Name}
		sampPath := make([]string, len(a.Samplers)) // path of the first channel using each sampler
		for k, c := range a.Channels {
			cp := fmt.Sprintf("%s.channels[%d]", p, k)
			if c.Sampler == nil {
				return errf(cp+".sampler", "missing")
			}
			if c.Target == nil || c.Target.Node == nil {
				return errf(cp+".target.node", "missing (animation pointer targets are not supported)")
			}
			if pathComps(c.Target.Path) == 0 {
				return errf(cp+".target.path", "%q is not translation, rotation, scale or weights", c.Target.Path)
			}
			if *c.Sampler < 0 || *c.Sampler >= len(a.Samplers) {
				return errf(cp+".sampler", "index %d out of range (have %d)", *c.Sampler, len(a.Samplers))
			}
			if sampPath[*c.Sampler] == "" {
				sampPath[*c.Sampler] = c.Target.Path
			}
			an.Channels = append(an.Channels, Channel{Node: *c.Target.Node, Path: c.Target.Path, Sampler: *c.Sampler})
		}
		for k, s := range a.Samplers {
			sp := fmt.Sprintf("%s.samplers[%d]", p, k)
			if s.Input == nil || s.Output == nil {
				return errf(sp, "input and output are required")
			}
			if sampPath[k] == "" {
				return errf(sp, "no channel uses this sampler")
			}
			as := AnimSampler{}
			switch s.Interpolation {
			case "", InterpLinear:
				as.Interpolation = InterpLinear
			default:
				as.Interpolation = s.Interpolation // Validate reports an unknown name
			}
			in, err := d.accessor(sp+".input", *s.Input, "SCALAR", compFloat)
			if err != nil {
				return err
			}
			if as.Input, err = in.floats(sp+".input", 1); err != nil {
				return err
			}
			comps := pathComps(sampPath[k])
			typ := map[int]string{1: "SCALAR", 3: "VEC3", 4: "VEC4"}[comps]
			out, err := d.accessor(sp+".output", *s.Output, typ, compFloat)
			if err != nil {
				return err
			}
			if as.Output, err = out.floats(sp+".output", comps); err != nil {
				return err
			}
			an.Samplers = append(an.Samplers, as)
		}
		doc.Animations = append(doc.Animations, an)
	}
	return nil
}

func nilInts(v []int) []int {
	if len(v) == 0 {
		return nil
	}
	return v
}
