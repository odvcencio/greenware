package gltf

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
)

const (
	glbMagic     = 0x46546C67
	chunkJSON    = 0x4E4F534A
	chunkBIN     = 0x004E4942
	targetArray  = 34962
	targetElem   = 34963
	compUbyte    = 5121
	compUshort   = 5123
	compUint     = 5125
	compFloat    = 5126
	maxBinLength = math.MaxUint32 - 1<<20
)

type viewRec struct {
	offset, length, target int
}

type accRec struct {
	view, comp, count int
	typ               string
	min, max          []float32
}

type primRefs struct{ pos, nrm, uv, idx int } // accessor numbers, -1 when absent

type encoder struct {
	bin   []byte
	views []viewRec
	accs  []accRec
}

func (e *encoder) addView(data []byte, target int) (int, error) {
	if len(e.bin)+len(data)+4 > maxBinLength {
		return 0, errf("buffers[0]", "binary data exceeds the 4 GiB GLB limit")
	}
	e.views = append(e.views, viewRec{offset: len(e.bin), length: len(data), target: target})
	e.bin = append(e.bin, data...)
	for len(e.bin)%4 != 0 {
		e.bin = append(e.bin, 0)
	}
	return len(e.views) - 1, nil
}

func (e *encoder) addAcc(data []byte, target int, a accRec) (int, error) {
	v, err := e.addView(data, target)
	if err != nil {
		return 0, err
	}
	a.view = v
	e.accs = append(e.accs, a)
	return len(e.accs) - 1, nil
}

func floatBytes(fs []float32) []byte {
	b := make([]byte, 0, 4*len(fs))
	for _, f := range fs {
		if f == 0 {
			f = 0 // drops the sign of -0
		}
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(f))
	}
	return b
}

// Encode returns the canonical GLB bytes for doc. See the package comment for
// the layout. It returns an *Error when doc is not valid.
func Encode(doc *Document) ([]byte, error) {
	if doc == nil {
		return nil, errf("document", "nil document")
	}
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	e := &encoder{}
	refs := make([][]primRefs, len(doc.Meshes))
	for i, m := range doc.Meshes {
		refs[i] = make([]primRefs, len(m.Primitives))
		for j, p := range m.Primitives {
			r := primRefs{-1, -1, -1, -1}
			n := len(p.Positions) / 3
			mn, mx := [3]float32{}, [3]float32{}
			for v := 0; v < n; v++ {
				for c := 0; c < 3; c++ {
					x := p.Positions[3*v+c]
					if x == 0 {
						x = 0
					}
					if v == 0 || x < mn[c] {
						mn[c] = x
					}
					if v == 0 || x > mx[c] {
						mx[c] = x
					}
				}
			}
			var err error
			if r.pos, err = e.addAcc(floatBytes(p.Positions), targetArray,
				accRec{comp: compFloat, count: n, typ: "VEC3", min: mn[:], max: mx[:]}); err != nil {
				return nil, err
			}
			if len(p.Normals) > 0 {
				if r.nrm, err = e.addAcc(floatBytes(p.Normals), targetArray, accRec{comp: compFloat, count: n, typ: "VEC3"}); err != nil {
					return nil, err
				}
			}
			if len(p.UV0) > 0 {
				if r.uv, err = e.addAcc(floatBytes(p.UV0), targetArray, accRec{comp: compFloat, count: n, typ: "VEC2"}); err != nil {
					return nil, err
				}
			}
			if len(p.Indices) > 0 {
				var mxi uint32
				for _, v := range p.Indices {
					if v > mxi {
						mxi = v
					}
				}
				var b []byte
				comp := compUint
				if mxi < 65535 {
					comp = compUshort
					for _, v := range p.Indices {
						b = binary.LittleEndian.AppendUint16(b, uint16(v))
					}
				} else {
					for _, v := range p.Indices {
						b = binary.LittleEndian.AppendUint32(b, v)
					}
				}
				if r.idx, err = e.addAcc(b, targetElem, accRec{comp: comp, count: len(p.Indices), typ: "SCALAR"}); err != nil {
					return nil, err
				}
			}
			refs[i][j] = r
		}
	}
	imageView := make([]int, len(doc.Images))
	for i, im := range doc.Images {
		imageView[i] = -1
		if len(im.Data) > 0 {
			v, err := e.addView(im.Data, 0)
			if err != nil {
				return nil, err
			}
			imageView[i] = v
		}
	}

	root := fields{}
	af := fields{}
	if doc.Asset.Copyright != "" {
		af.add("copyright", jstr(doc.Asset.Copyright))
	}
	if doc.Asset.Generator != "" {
		af.add("generator", jstr(doc.Asset.Generator))
	}
	af.add("version", jstr("2.0"))
	root.add("asset", af.String())

	used := map[string]bool{}
	for _, n := range doc.Nodes {
		for k := range n.Extensions {
			used[k] = true
		}
	}
	for _, m := range doc.Materials {
		for k := range m.Extensions {
			used[k] = true
		}
	}
	if len(used) > 0 {
		names := make([]string, 0, len(used))
		for k := range used {
			names = append(names, k)
		}
		sort.Strings(names)
		q := make([]string, len(names))
		for i, k := range names {
			q[i] = jstr(k)
		}
		root.add("extensionsUsed", jarr(q))
	}
	if doc.Scene != nil {
		root.add("scene", strconv.Itoa(*doc.Scene))
	}

	if len(doc.Scenes) > 0 {
		items := make([]string, len(doc.Scenes))
		for i, s := range doc.Scenes {
			f := fields{}
			if s.Name != "" {
				f.add("name", jstr(s.Name))
			}
			if len(s.Nodes) > 0 {
				f.add("nodes", jints(s.Nodes))
			}
			if err := addExtras(&f, s.Extras, fmt.Sprintf("scenes[%d].extras", i)); err != nil {
				return nil, err
			}
			items[i] = f.String()
		}
		root.add("scenes", jarr(items))
	}

	if len(doc.Nodes) > 0 {
		items := make([]string, len(doc.Nodes))
		for i, n := range doc.Nodes {
			p := fmt.Sprintf("nodes[%d]", i)
			f := fields{}
			if n.Name != "" {
				f.add("name", jstr(n.Name))
			}
			if len(n.Children) > 0 {
				f.add("children", jints(n.Children))
			}
			if n.Mesh != nil {
				f.add("mesh", strconv.Itoa(*n.Mesh))
			}
			if n.Translation != nil {
				f.add("translation", jnums(n.Translation[:]))
			}
			if n.Rotation != nil {
				f.add("rotation", jnums(n.Rotation[:]))
			}
			if n.Scale != nil {
				f.add("scale", jnums(n.Scale[:]))
			}
			if err := addExtensions(&f, n.Extensions, p+".extensions"); err != nil {
				return nil, err
			}
			if err := addExtras(&f, n.Extras, p+".extras"); err != nil {
				return nil, err
			}
			items[i] = f.String()
		}
		root.add("nodes", jarr(items))
	}

	if len(doc.Meshes) > 0 {
		items := make([]string, len(doc.Meshes))
		for i, m := range doc.Meshes {
			f := fields{}
			if m.Name != "" {
				f.add("name", jstr(m.Name))
			}
			prims := make([]string, len(m.Primitives))
			for j, p := range m.Primitives {
				r := refs[i][j]
				pf := fields{}
				attr := fields{}
				attr.add("POSITION", strconv.Itoa(r.pos))
				if r.nrm >= 0 {
					attr.add("NORMAL", strconv.Itoa(r.nrm))
				}
				if r.uv >= 0 {
					attr.add("TEXCOORD_0", strconv.Itoa(r.uv))
				}
				pf.add("attributes", attr.String())
				if r.idx >= 0 {
					pf.add("indices", strconv.Itoa(r.idx))
				}
				if p.Material != nil {
					pf.add("material", strconv.Itoa(*p.Material))
				}
				if err := addExtras(&pf, p.Extras, fmt.Sprintf("meshes[%d].primitives[%d].extras", i, j)); err != nil {
					return nil, err
				}
				prims[j] = pf.String()
			}
			f.add("primitives", jarr(prims))
			if err := addExtras(&f, m.Extras, fmt.Sprintf("meshes[%d].extras", i)); err != nil {
				return nil, err
			}
			items[i] = f.String()
		}
		root.add("meshes", jarr(items))
	}

	if len(doc.Materials) > 0 {
		items := make([]string, len(doc.Materials))
		for i, m := range doc.Materials {
			p := fmt.Sprintf("materials[%d]", i)
			f := fields{}
			if m.Name != "" {
				f.add("name", jstr(m.Name))
			}
			pbr := fields{}
			pbr.add("baseColorFactor", jnums(m.BaseColor[:]))
			if m.BaseColorTexture != nil {
				t := fields{}
				t.add("index", strconv.Itoa(*m.BaseColorTexture))
				pbr.add("baseColorTexture", t.String())
			}
			pbr.add("metallicFactor", jnum(m.Metallic))
			pbr.add("roughnessFactor", jnum(m.Roughness))
			f.add("pbrMetallicRoughness", pbr.String())
			if err := addExtensions(&f, m.Extensions, p+".extensions"); err != nil {
				return nil, err
			}
			if err := addExtras(&f, m.Extras, p+".extras"); err != nil {
				return nil, err
			}
			items[i] = f.String()
		}
		root.add("materials", jarr(items))
	}

	if len(doc.Textures) > 0 {
		items := make([]string, len(doc.Textures))
		for i, t := range doc.Textures {
			f := fields{}
			if t.Name != "" {
				f.add("name", jstr(t.Name))
			}
			if t.Sampler != nil {
				f.add("sampler", strconv.Itoa(*t.Sampler))
			}
			if t.Source != nil {
				f.add("source", strconv.Itoa(*t.Source))
			}
			items[i] = f.String()
		}
		root.add("textures", jarr(items))
	}

	if len(doc.Images) > 0 {
		items := make([]string, len(doc.Images))
		for i, im := range doc.Images {
			f := fields{}
			if im.Name != "" {
				f.add("name", jstr(im.Name))
			}
			if im.URI != "" {
				f.add("uri", jstr(im.URI))
			}
			if imageView[i] >= 0 {
				f.add("bufferView", strconv.Itoa(imageView[i]))
			}
			if im.MimeType != "" {
				f.add("mimeType", jstr(im.MimeType))
			}
			items[i] = f.String()
		}
		root.add("images", jarr(items))
	}

	if len(doc.Samplers) > 0 {
		items := make([]string, len(doc.Samplers))
		for i, s := range doc.Samplers {
			f := fields{}
			if s.MagFilter != 0 {
				f.add("magFilter", strconv.Itoa(s.MagFilter))
			}
			if s.MinFilter != 0 {
				f.add("minFilter", strconv.Itoa(s.MinFilter))
			}
			if s.WrapS != 0 {
				f.add("wrapS", strconv.Itoa(s.WrapS))
			}
			if s.WrapT != 0 {
				f.add("wrapT", strconv.Itoa(s.WrapT))
			}
			items[i] = f.String()
		}
		root.add("samplers", jarr(items))
	}

	if len(e.bin) > 0 {
		bf := fields{}
		bf.add("byteLength", strconv.Itoa(len(e.bin)))
		root.add("buffers", jarr([]string{bf.String()}))
		items := make([]string, len(e.views))
		for i, v := range e.views {
			f := fields{}
			f.add("buffer", "0")
			f.add("byteOffset", strconv.Itoa(v.offset))
			f.add("byteLength", strconv.Itoa(v.length))
			if v.target != 0 {
				f.add("target", strconv.Itoa(v.target))
			}
			items[i] = f.String()
		}
		root.add("bufferViews", jarr(items))
	}
	if len(e.accs) > 0 {
		items := make([]string, len(e.accs))
		for i, a := range e.accs {
			f := fields{}
			f.add("bufferView", strconv.Itoa(a.view))
			f.add("componentType", strconv.Itoa(a.comp))
			f.add("count", strconv.Itoa(a.count))
			f.add("type", jstr(a.typ))
			if a.min != nil {
				f.add("min", jnums(a.min))
				f.add("max", jnums(a.max))
			}
			items[i] = f.String()
		}
		root.add("accessors", jarr(items))
	}
	if err := addExtras(&root, doc.Extras, "extras"); err != nil {
		return nil, err
	}

	js := []byte(root.String())
	for len(js)%4 != 0 {
		js = append(js, ' ')
	}
	total := 12 + 8 + len(js)
	if len(e.bin) > 0 {
		total += 8 + len(e.bin)
	}
	if total > math.MaxUint32 {
		return nil, errf("glb", "file exceeds the 4 GiB GLB limit")
	}
	out := make([]byte, 0, total)
	out = binary.LittleEndian.AppendUint32(out, glbMagic)
	out = binary.LittleEndian.AppendUint32(out, 2)
	out = binary.LittleEndian.AppendUint32(out, uint32(total))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(js)))
	out = binary.LittleEndian.AppendUint32(out, chunkJSON)
	out = append(out, js...)
	if len(e.bin) > 0 {
		out = binary.LittleEndian.AppendUint32(out, uint32(len(e.bin)))
		out = binary.LittleEndian.AppendUint32(out, chunkBIN)
		out = append(out, e.bin...)
	}
	return out, nil
}

func addExtras(f *fields, raw []byte, path string) error {
	c, err := canonicalJSON(raw)
	if err != nil {
		return errf(path, "invalid JSON: %v", err)
	}
	if c != nil {
		f.add("extras", string(c))
	}
	return nil
}

func addExtensions(f *fields, m map[string]json.RawMessage, path string) error {
	c, err := canonicalExt(m, path)
	if err != nil {
		return err
	}
	if len(c) == 0 {
		return nil
	}
	ef := fields{}
	for _, k := range sortedKeys(c) {
		ef.add(k, string(c[k]))
	}
	f.add("extensions", ef.String())
	return nil
}
