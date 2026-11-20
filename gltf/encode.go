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

// primRefs holds accessor numbers, -1 when absent. targets has {POSITION,
// NORMAL} per morph target.
type primRefs struct {
	pos, nrm, uv, idx, joints, weights int
	targets                            [][2]int
}

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
			r := primRefs{pos: -1, nrm: -1, uv: -1, idx: -1, joints: -1, weights: -1}
			n := len(p.Positions) / 3
			mn, mx := minMax3(p.Positions)
			var err error
			if r.pos, err = e.addAcc(floatBytes(p.Positions), targetArray,
				accRec{comp: compFloat, count: n, typ: "VEC3", min: mn, max: mx}); err != nil {
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
			if len(p.Joints0) > 0 {
				// The smallest component type that holds every joint index.
				var mxj uint16
				for _, v := range p.Joints0 {
					if v > mxj {
						mxj = v
					}
				}
				var b []byte
				comp := compUbyte
				if mxj <= 255 {
					for _, v := range p.Joints0 {
						b = append(b, byte(v))
					}
				} else {
					comp = compUshort
					for _, v := range p.Joints0 {
						b = binary.LittleEndian.AppendUint16(b, v)
					}
				}
				if r.joints, err = e.addAcc(b, targetArray, accRec{comp: comp, count: n, typ: "VEC4"}); err != nil {
					return nil, err
				}
				if r.weights, err = e.addAcc(floatBytes(p.Weights0), targetArray, accRec{comp: compFloat, count: n, typ: "VEC4"}); err != nil {
					return nil, err
				}
			}
			for _, tg := range p.Targets {
				ref := [2]int{-1, -1}
				if len(tg.Positions) > 0 {
					tmn, tmx := minMax3(tg.Positions)
					if ref[0], err = e.addAcc(floatBytes(tg.Positions), targetArray,
						accRec{comp: compFloat, count: n, typ: "VEC3", min: tmn, max: tmx}); err != nil {
						return nil, err
					}
				}
				if len(tg.Normals) > 0 {
					if ref[1], err = e.addAcc(floatBytes(tg.Normals), targetArray, accRec{comp: compFloat, count: n, typ: "VEC3"}); err != nil {
						return nil, err
					}
				}
				r.targets = append(r.targets, ref)
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

	// Skin and animation data follow the images.
	skinAcc := make([]int, len(doc.Skins)) // inverse bind matrix accessor, -1 when absent
	for i, sk := range doc.Skins {
		skinAcc[i] = -1
		if len(sk.InverseBindMatrices) > 0 {
			flat := make([]float32, 0, 16*len(sk.InverseBindMatrices))
			for _, m := range sk.InverseBindMatrices {
				flat = append(flat, m[:]...)
			}
			a, err := e.addAcc(floatBytes(flat), 0, accRec{comp: compFloat, count: len(sk.InverseBindMatrices), typ: "MAT4"})
			if err != nil {
				return nil, err
			}
			skinAcc[i] = a
		}
	}
	animAcc := make([][][2]int, len(doc.Animations)) // {input, output} accessors per sampler
	for i, an := range doc.Animations {
		animAcc[i] = make([][2]int, len(an.Samplers))
		comps := make([]int, len(an.Samplers))
		for _, c := range an.Channels {
			comps[c.Sampler] = pathComps(c.Path)
		}
		for k, sm := range an.Samplers {
			var tmn, tmx float32
			for q, t := range sm.Input {
				if t == 0 {
					t = 0
				}
				if q == 0 || t < tmn {
					tmn = t
				}
				if q == 0 || t > tmx {
					tmx = t
				}
			}
			in, err := e.addAcc(floatBytes(sm.Input), 0,
				accRec{comp: compFloat, count: len(sm.Input), typ: "SCALAR", min: []float32{tmn}, max: []float32{tmx}})
			if err != nil {
				return nil, err
			}
			typ := map[int]string{1: "SCALAR", 3: "VEC3", 4: "VEC4"}[comps[k]]
			out, err := e.addAcc(floatBytes(sm.Output), 0, accRec{comp: compFloat, count: len(sm.Output) / comps[k], typ: typ})
			if err != nil {
				return nil, err
			}
			animAcc[i][k] = [2]int{in, out}
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
			if n.Skin != nil {
				f.add("skin", strconv.Itoa(*n.Skin))
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
				if r.joints >= 0 {
					attr.add("JOINTS_0", strconv.Itoa(r.joints))
					attr.add("WEIGHTS_0", strconv.Itoa(r.weights))
				}
				pf.add("attributes", attr.String())
				if r.idx >= 0 {
					pf.add("indices", strconv.Itoa(r.idx))
				}
				if p.Material != nil {
					pf.add("material", strconv.Itoa(*p.Material))
				}
				if len(r.targets) > 0 {
					ts := make([]string, len(r.targets))
					for t, ref := range r.targets {
						tf := fields{}
						if ref[0] >= 0 {
							tf.add("POSITION", strconv.Itoa(ref[0]))
						}
						if ref[1] >= 0 {
							tf.add("NORMAL", strconv.Itoa(ref[1]))
						}
						ts[t] = tf.String()
					}
					pf.add("targets", jarr(ts))
				}
				if err := addExtras(&pf, p.Extras, fmt.Sprintf("meshes[%d].primitives[%d].extras", i, j)); err != nil {
					return nil, err
				}
				prims[j] = pf.String()
			}
			f.add("primitives", jarr(prims))
			if len(m.Weights) > 0 {
				f.add("weights", jnums(m.Weights))
			}
			mp := fmt.Sprintf("meshes[%d].extras", i)
			extras, err := mergeTargetNames(m.Extras, m.TargetNames, mp)
			if err != nil {
				return nil, err
			}
			if err := addExtras(&f, extras, mp); err != nil {
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
				if m.BaseColorTexCoord != 0 {
					t.add("texCoord", strconv.Itoa(m.BaseColorTexCoord))
				}
				pbr.add("baseColorTexture", t.String())
			}
			pbr.add("metallicFactor", jnum(m.Metallic))
			pbr.add("roughnessFactor", jnum(m.Roughness))
			f.add("pbrMetallicRoughness", pbr.String())
			if m.EmissiveFactor != [3]float32{} {
				f.add("emissiveFactor", jnums(m.EmissiveFactor[:]))
			}
			if m.AlphaMode == AlphaMask || m.AlphaMode == AlphaBlend {
				f.add("alphaMode", jstr(m.AlphaMode))
			}
			if m.AlphaMode == AlphaMask {
				f.add("alphaCutoff", jnum(m.AlphaCutoff))
			}
			if m.DoubleSided {
				f.add("doubleSided", "true")
			}
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
			if err := addExtras(&f, t.Extras, fmt.Sprintf("textures[%d].extras", i)); err != nil {
				return nil, err
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
			if err := addExtras(&f, im.Extras, fmt.Sprintf("images[%d].extras", i)); err != nil {
				return nil, err
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

	if len(doc.Skins) > 0 {
		items := make([]string, len(doc.Skins))
		for i, sk := range doc.Skins {
			f := fields{}
			if sk.Name != "" {
				f.add("name", jstr(sk.Name))
			}
			if skinAcc[i] >= 0 {
				f.add("inverseBindMatrices", strconv.Itoa(skinAcc[i]))
			}
			if sk.Skeleton != nil {
				f.add("skeleton", strconv.Itoa(*sk.Skeleton))
			}
			f.add("joints", jints(sk.Joints))
			items[i] = f.String()
		}
		root.add("skins", jarr(items))
	}

	if len(doc.Animations) > 0 {
		items := make([]string, len(doc.Animations))
		for i, an := range doc.Animations {
			f := fields{}
			if an.Name != "" {
				f.add("name", jstr(an.Name))
			}
			chs := make([]string, len(an.Channels))
			for k, c := range an.Channels {
				tf := fields{}
				tf.add("node", strconv.Itoa(c.Node))
				tf.add("path", jstr(c.Path))
				cf := fields{}
				cf.add("sampler", strconv.Itoa(c.Sampler))
				cf.add("target", tf.String())
				chs[k] = cf.String()
			}
			f.add("channels", jarr(chs))
			sms := make([]string, len(an.Samplers))
			for k, sm := range an.Samplers {
				interp := sm.Interpolation
				if interp == "" {
					interp = InterpLinear
				}
				sf := fields{}
				sf.add("input", strconv.Itoa(animAcc[i][k][0]))
				sf.add("interpolation", jstr(interp))
				sf.add("output", strconv.Itoa(animAcc[i][k][1]))
				sms[k] = sf.String()
			}
			f.add("samplers", jarr(sms))
			items[i] = f.String()
		}
		root.add("animations", jarr(items))
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

// minMax3 returns the exact per-component minimum and maximum of xyz triples
// (negative zero counts as zero).
func minMax3(fs []float32) (mn, mx []float32) {
	mn, mx = make([]float32, 3), make([]float32, 3)
	n := len(fs) / 3
	for v := 0; v < n; v++ {
		for c := 0; c < 3; c++ {
			x := fs[3*v+c]
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
	return mn, mx
}
