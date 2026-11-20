package glb

import (
	"fmt"
	"math"
	"strconv"

	"encoding/json"
)

// Document is the subset of the glTF 2.0 JSON that the oracle reads.
type Document struct {
	Version            string
	Scene              *int
	Scenes             []Scene
	Nodes              []Node
	Meshes             []Mesh
	Materials          []Material
	Accessors          []Accessor
	BufferViews        []BufferView
	Buffers            []Buffer
	Skins              []Skin
	Animations         []Animation
	Images             []Image
	Textures           []Texture
	Samplers           int
	ExtensionsUsed     []string
	ExtensionsRequired []string
	Extras             any
}

// Scene lists root nodes.
type Scene struct {
	Name  string
	Nodes []int
}

// Node is a glTF node. Translation, Rotation and Scale hold the defaults when
// absent. Matrix is set only when the file gave one.
type Node struct {
	Name        string
	Mesh, Skin  *int
	Children    []int
	Matrix      *[16]float64
	Translation [3]float64
	Rotation    [4]float64
	Scale       [3]float64
	Weights     []float64
	Extras      any
}

// Mesh is a list of primitives.
type Mesh struct {
	Name       string
	Primitives []Primitive
	Weights    []float64
}

// Primitive is one draw.
type Primitive struct {
	Attributes map[string]int
	Indices    *int
	Material   *int
	Mode       int
	Targets    []map[string]int
}

// Material keeps only what reference checks and reports need.
type Material struct {
	Name            string
	BaseColorFactor *[4]float64
}

// Accessor describes a typed view of a buffer view.
type Accessor struct {
	BufferView    *int
	ByteOffset    int
	ComponentType int
	Normalized    bool
	Count         int
	Type          string
	Min, Max      []float64 // nil when absent; values rounded to float32
	HasMinMax     bool
	Sparse        *Sparse
}

// Sparse is an accessor's sparse substitution block.
type Sparse struct {
	Count                int
	IndicesView          int
	IndicesOffset        int
	IndicesComponentType int
	ValuesView           int
	ValuesOffset         int
}

// BufferView is a byte range of a buffer.
type BufferView struct {
	Buffer     int
	ByteOffset int
	ByteLength int
	ByteStride int // 0 when absent
	Target     int // 0 when absent
}

// Buffer is a byte store. URI is empty for the GLB binary chunk.
type Buffer struct {
	ByteLength int
	URI        string
	HasURI     bool
}

// Skin lists joints.
type Skin struct {
	Joints              []int
	InverseBindMatrices *int
	Skeleton            *int
}

// Animation is a set of channels and samplers.
type Animation struct {
	Name     string
	Channels []Channel
	Samplers []AnimSampler
}

// Channel targets a node property.
type Channel struct {
	Sampler int
	Node    *int
	Path    string
}

// AnimSampler maps input times to output values.
type AnimSampler struct {
	Input, Output int
	Interpolation string
}

// Image refers to pixel data.
type Image struct {
	URI        string
	BufferView *int
	MimeType   string
}

// Texture ties an image to a sampler.
type Texture struct {
	Source, Sampler *int
}

type jv = map[string]any

func asObj(v any) (jv, bool) { o, ok := v.(map[string]any); return o, ok }

func numOf(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

func f32Of(v any) (float32, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(n), 32)
	if err != nil || math.IsInf(f, 0) {
		return 0, false
	}
	return float32(f), true
}

func intOf(v any) (int, bool) {
	f, ok := numOf(v)
	if !ok || f != math.Trunc(f) || math.Abs(f) > 1<<40 {
		return 0, false
	}
	return int(f), true
}

type parser struct {
	c *collector
}

func (p *parser) obj(v any, path string) (jv, bool) {
	o, ok := asObj(v)
	if !ok {
		p.c.errf("JSON-TYPE", path, "expected an object")
	}
	return o, ok
}

func (p *parser) arr(o jv, key, path string, required bool) ([]any, bool) {
	v, has := o[key]
	if !has {
		if required {
			p.c.errf("JSON-REQUIRED", path+"/"+key, "missing required array %q", key)
		}
		return nil, false
	}
	a, ok := v.([]any)
	if !ok {
		p.c.errf("JSON-TYPE", path+"/"+key, "expected an array")
		return nil, false
	}
	return a, true
}

func (p *parser) integer(o jv, key, path string, required bool, min int) (int, bool) {
	v, has := o[key]
	if !has {
		if required {
			p.c.errf("JSON-REQUIRED", path+"/"+key, "missing required property %q", key)
		}
		return 0, false
	}
	n, ok := intOf(v)
	if !ok {
		p.c.errf("JSON-TYPE", path+"/"+key, "expected an integer")
		return 0, false
	}
	if n < min {
		p.c.errf("JSON-RANGE", path+"/"+key, "value %d is below the minimum %d", n, min)
		return 0, false
	}
	return n, true
}

func (p *parser) optInt(o jv, key, path string) *int {
	n, ok := p.integer(o, key, path, false, 0)
	if !ok {
		return nil
	}
	return &n
}

func (p *parser) str(o jv, key, path string) (string, bool) {
	v, has := o[key]
	if !has {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		p.c.errf("JSON-TYPE", path+"/"+key, "expected a string")
	}
	return s, ok
}

func (p *parser) ints(o jv, key, path string, minLen int) []int {
	a, ok := p.arr(o, key, path, false)
	if !ok {
		return nil
	}
	if len(a) < minLen {
		p.c.errf("JSON-RANGE", path+"/"+key, "array has %d items; at least %d required", len(a), minLen)
	}
	out := make([]int, 0, len(a))
	for i, v := range a {
		n, ok := intOf(v)
		if !ok || n < 0 {
			p.c.errf("JSON-TYPE", fmt.Sprintf("%s/%s/%d", path, key, i), "expected a non-negative integer")
			n = -1
		}
		out = append(out, n)
	}
	return out
}

// floats reads a numeric array. want>0 demands that exact length.
func (p *parser) floats(o jv, key, path string, want int) ([]float64, bool) {
	a, ok := p.arr(o, key, path, false)
	if !ok {
		return nil, false
	}
	if want > 0 && len(a) != want {
		p.c.errf("JSON-RANGE", path+"/"+key, "array has %d items; %d required", len(a), want)
		return nil, false
	}
	out := make([]float64, len(a))
	for i, v := range a {
		f, ok := numOf(v)
		if !ok {
			p.c.errf("JSON-TYPE", fmt.Sprintf("%s/%s/%d", path, key, i), "expected a finite number")
			return nil, false
		}
		out[i] = f
	}
	return out, true
}

func (p *parser) strs(o jv, key, path string) []string {
	a, ok := p.arr(o, key, path, false)
	if !ok {
		return nil
	}
	var out []string
	for i, v := range a {
		s, ok := v.(string)
		if !ok {
			p.c.errf("JSON-TYPE", fmt.Sprintf("%s/%s/%d", path, key, i), "expected a string")
			continue
		}
		out = append(out, s)
	}
	return out
}

// parseDocument reads the root object into a Document, reporting schema
// problems with JSON paths. It never panics on arbitrary JSON.
func parseDocument(root any, c *collector) *Document {
	p := &parser{c}
	top, ok := asObj(root)
	if !ok {
		c.errf("JSON-TYPE", "", "the glTF JSON root must be an object")
		return nil
	}
	d := &Document{}
	if a, ok := p.obj(top["asset"], "/asset"); ok {
		if v, ok := p.str(a, "version", "/asset"); ok {
			d.Version = v
			if v != "2.0" {
				c.errf("ASSET-VERSION", "/asset/version", "version is %q; only 2.0 is supported", v)
			}
		} else if _, has := a["version"]; !has {
			c.errf("JSON-REQUIRED", "/asset/version", "missing required property \"version\"")
		}
	} else if _, has := top["asset"]; !has {
		// obj already reported a type error for nil; replace by a clearer one.
		c.errf("JSON-REQUIRED", "/asset", "missing required property \"asset\"")
	}
	d.Scene = p.optInt(top, "scene", "")
	d.ExtensionsUsed = p.strs(top, "extensionsUsed", "")
	d.ExtensionsRequired = p.strs(top, "extensionsRequired", "")
	d.Extras = top["extras"]

	each := func(key string, fn func(i int, o jv, path string)) {
		a, ok := p.arr(top, key, "", false)
		if !ok {
			return
		}
		if len(a) == 0 {
			c.errf("JSON-RANGE", "/"+key, "array must not be empty when present")
		}
		for i, v := range a {
			path := fmt.Sprintf("/%s/%d", key, i)
			o, ok := p.obj(v, path)
			if !ok {
				o = jv{}
			}
			fn(i, o, path)
		}
	}

	each("scenes", func(i int, o jv, path string) {
		s := Scene{Nodes: p.ints(o, "nodes", path, 1)}
		s.Name, _ = p.str(o, "name", path)
		d.Scenes = append(d.Scenes, s)
	})
	each("nodes", func(i int, o jv, path string) {
		n := Node{Rotation: [4]float64{0, 0, 0, 1}, Scale: [3]float64{1, 1, 1}}
		n.Name, _ = p.str(o, "name", path)
		n.Mesh = p.optInt(o, "mesh", path)
		n.Skin = p.optInt(o, "skin", path)
		n.Children = p.ints(o, "children", path, 1)
		n.Extras = o["extras"]
		_, hasM := o["matrix"]
		if m, ok := p.floats(o, "matrix", path, 16); ok {
			var mm [16]float64
			copy(mm[:], m)
			n.Matrix = &mm
		}
		if t, ok := p.floats(o, "translation", path, 3); ok {
			copy(n.Translation[:], t)
		}
		if r, ok := p.floats(o, "rotation", path, 4); ok {
			copy(n.Rotation[:], r)
		}
		if s, ok := p.floats(o, "scale", path, 3); ok {
			copy(n.Scale[:], s)
		}
		if hasM {
			for _, k := range []string{"translation", "rotation", "scale"} {
				if _, has := o[k]; has {
					c.errf("NODE-TRANSFORM", path+"/"+k, "a node with a matrix must not also have %s", k)
				}
			}
		}
		n.Weights, _ = p.floats(o, "weights", path, 0)
		d.Nodes = append(d.Nodes, n)
	})
	each("meshes", func(i int, o jv, path string) {
		m := Mesh{}
		m.Name, _ = p.str(o, "name", path)
		m.Weights, _ = p.floats(o, "weights", path, 0)
		prims, _ := p.arr(o, "primitives", path, true)
		if len(prims) == 0 && o["primitives"] != nil {
			c.errf("JSON-RANGE", path+"/primitives", "a mesh needs at least one primitive")
		}
		for j, pv := range prims {
			pp := fmt.Sprintf("%s/primitives/%d", path, j)
			po, ok := p.obj(pv, pp)
			if !ok {
				po = jv{}
			}
			pr := Primitive{Mode: 4, Attributes: map[string]int{}}
			if m, ok := p.integer(po, "mode", pp, false, 0); ok {
				pr.Mode = m
			}
			pr.Indices = p.optInt(po, "indices", pp)
			pr.Material = p.optInt(po, "material", pp)
			if ao, ok := p.obj(po["attributes"], pp+"/attributes"); ok {
				for k := range ao {
					if n, ok := p.integer(ao, k, pp+"/attributes", true, 0); ok {
						pr.Attributes[k] = n
					}
				}
			} else if _, has := po["attributes"]; !has {
				c.errf("JSON-REQUIRED", pp+"/attributes", "missing required property \"attributes\"")
			}
			if ta, ok := p.arr(po, "targets", pp, false); ok {
				for k, tv := range ta {
					tp := fmt.Sprintf("%s/targets/%d", pp, k)
					to, ok := p.obj(tv, tp)
					t := map[string]int{}
					if ok {
						for key := range to {
							if n, ok := p.integer(to, key, tp, true, 0); ok {
								t[key] = n
							}
						}
					}
					pr.Targets = append(pr.Targets, t)
				}
			}
			m.Primitives = append(m.Primitives, pr)
		}
		d.Meshes = append(d.Meshes, m)
	})
	each("materials", func(i int, o jv, path string) {
		m := Material{}
		m.Name, _ = p.str(o, "name", path)
		if pbr, ok := asObj(o["pbrMetallicRoughness"]); ok {
			if f, ok := p.floats(pbr, "baseColorFactor", path+"/pbrMetallicRoughness", 4); ok {
				var b [4]float64
				copy(b[:], f)
				m.BaseColorFactor = &b
			}
		}
		d.Materials = append(d.Materials, m)
	})
	each("accessors", func(i int, o jv, path string) {
		a := Accessor{}
		a.BufferView = p.optInt(o, "bufferView", path)
		if n, ok := p.integer(o, "byteOffset", path, false, 0); ok {
			a.ByteOffset = n
		}
		a.ComponentType, _ = p.integer(o, "componentType", path, true, 0)
		if v, ok := o["normalized"].(bool); ok {
			a.Normalized = v
		}
		a.Count, _ = p.integer(o, "count", path, true, 1)
		a.Type, _ = p.str(o, "type", path)
		if _, has := o["type"]; !has {
			c.errf("JSON-REQUIRED", path+"/type", "missing required property \"type\"")
		}
		readF32 := func(key string) []float64 {
			arr, ok := p.arr(o, key, path, false)
			if !ok {
				return nil
			}
			out := make([]float64, len(arr))
			for k, v := range arr {
				f, ok := f32Of(v)
				if !ok {
					c.errf("JSON-TYPE", fmt.Sprintf("%s/%s/%d", path, key, k), "expected a finite number")
					return nil
				}
				out[k] = float64(f)
			}
			return out
		}
		a.Min, a.Max = readF32("min"), readF32("max")
		a.HasMinMax = a.Min != nil || a.Max != nil
		if so, ok := asObj(o["sparse"]); ok {
			s := &Sparse{}
			s.Count, _ = p.integer(so, "count", path+"/sparse", true, 1)
			if io, ok := p.obj(so["indices"], path+"/sparse/indices"); ok {
				s.IndicesView, _ = p.integer(io, "bufferView", path+"/sparse/indices", true, 0)
				s.IndicesOffset, _ = p.integer(io, "byteOffset", path+"/sparse/indices", false, 0)
				s.IndicesComponentType, _ = p.integer(io, "componentType", path+"/sparse/indices", true, 0)
			}
			if vo, ok := p.obj(so["values"], path+"/sparse/values"); ok {
				s.ValuesView, _ = p.integer(vo, "bufferView", path+"/sparse/values", true, 0)
				s.ValuesOffset, _ = p.integer(vo, "byteOffset", path+"/sparse/values", false, 0)
			}
			a.Sparse = s
		} else if _, has := o["sparse"]; has {
			c.errf("JSON-TYPE", path+"/sparse", "expected an object")
		}
		d.Accessors = append(d.Accessors, a)
	})
	each("bufferViews", func(i int, o jv, path string) {
		b := BufferView{}
		b.Buffer, _ = p.integer(o, "buffer", path, true, 0)
		b.ByteOffset, _ = p.integer(o, "byteOffset", path, false, 0)
		b.ByteLength, _ = p.integer(o, "byteLength", path, true, 1)
		b.ByteStride, _ = p.integer(o, "byteStride", path, false, 0)
		b.Target, _ = p.integer(o, "target", path, false, 0)
		d.BufferViews = append(d.BufferViews, b)
	})
	each("buffers", func(i int, o jv, path string) {
		b := Buffer{}
		b.ByteLength, _ = p.integer(o, "byteLength", path, true, 1)
		b.URI, b.HasURI = p.str(o, "uri", path)
		d.Buffers = append(d.Buffers, b)
	})
	each("skins", func(i int, o jv, path string) {
		s := Skin{Joints: p.ints(o, "joints", path, 1)}
		if _, has := o["joints"]; !has {
			c.errf("JSON-REQUIRED", path+"/joints", "missing required property \"joints\"")
		}
		s.InverseBindMatrices = p.optInt(o, "inverseBindMatrices", path)
		s.Skeleton = p.optInt(o, "skeleton", path)
		d.Skins = append(d.Skins, s)
	})
	each("animations", func(i int, o jv, path string) {
		an := Animation{}
		an.Name, _ = p.str(o, "name", path)
		if sa, ok := p.arr(o, "samplers", path, true); ok {
			for j, sv := range sa {
				sp := fmt.Sprintf("%s/samplers/%d", path, j)
				so, ok := p.obj(sv, sp)
				if !ok {
					so = jv{}
				}
				s := AnimSampler{Interpolation: "LINEAR"}
				s.Input, _ = p.integer(so, "input", sp, true, 0)
				s.Output, _ = p.integer(so, "output", sp, true, 0)
				if v, ok := p.str(so, "interpolation", sp); ok {
					s.Interpolation = v
				}
				an.Samplers = append(an.Samplers, s)
			}
		}
		if ca, ok := p.arr(o, "channels", path, true); ok {
			for j, cv := range ca {
				cp := fmt.Sprintf("%s/channels/%d", path, j)
				co, ok := p.obj(cv, cp)
				if !ok {
					co = jv{}
				}
				ch := Channel{}
				ch.Sampler, _ = p.integer(co, "sampler", cp, true, 0)
				if to, ok := p.obj(co["target"], cp+"/target"); ok {
					ch.Node = p.optInt(to, "node", cp+"/target")
					ch.Path, _ = p.str(to, "path", cp+"/target")
				}
				an.Channels = append(an.Channels, ch)
			}
		}
		d.Animations = append(d.Animations, an)
	})
	each("images", func(i int, o jv, path string) {
		im := Image{}
		im.URI, _ = p.str(o, "uri", path)
		im.BufferView = p.optInt(o, "bufferView", path)
		im.MimeType, _ = p.str(o, "mimeType", path)
		d.Images = append(d.Images, im)
	})
	each("textures", func(i int, o jv, path string) {
		d.Textures = append(d.Textures, Texture{Source: p.optInt(o, "source", path), Sampler: p.optInt(o, "sampler", path)})
	})
	if a, ok := top["samplers"].([]any); ok {
		d.Samplers = len(a)
	}
	return d
}
