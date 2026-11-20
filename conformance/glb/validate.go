package glb

import (
	"fmt"
	"math"
	"sort"
)

// knownExtensions are the extensions the oracle can read without losing data.
var knownExtensions = map[string]bool{
	"KHR_materials_clearcoat":         true,
	"KHR_materials_emissive_strength": true,
	"KHR_materials_unlit":             true,
	"KHR_texture_transform":           true,
	"KHR_materials_variants":          true,
}

func (f *File) validate(c *collector) {
	d := f.Doc
	f.validateExtensions(c)
	viewOK := f.validateBufferViews(c)
	f.validateAccessors(c, viewOK)
	f.checkAccessorData(c)
	f.parent = f.validateNodes(c)
	f.validateMeshes(c)
	f.validateSkins(c)
	f.validateAnimations(c)
	f.validateMaterialsAndImages(c)
	if len(d.Scenes) > 0 && d.Scene == nil {
		c.warnf("SCENE-DEFAULT", "/scene", "no default scene is named; the oracle uses scene 0")
	}
}

func (f *File) validateExtensions(c *collector) {
	d := f.Doc
	used := map[string]bool{}
	for _, e := range d.ExtensionsUsed {
		used[e] = true
	}
	for i, e := range d.ExtensionsRequired {
		if !used[e] {
			c.errf("EXT-NOT-USED", fmt.Sprintf("/extensionsRequired/%d", i), "%q is required but not listed in extensionsUsed", e)
		}
		if !knownExtensions[e] {
			c.errf("EXT-UNSUPPORTED", fmt.Sprintf("/extensionsRequired/%d", i), "required extension %q cannot be decoded by the oracle", e)
		}
	}
	for i, e := range d.ExtensionsUsed {
		if !knownExtensions[e] && !contains(d.ExtensionsRequired, e) {
			c.warnf("EXT-UNKNOWN", fmt.Sprintf("/extensionsUsed/%d", i), "extension %q is not checked by the oracle", e)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// validateNodes checks references, transforms, parents, cycles and scenes.
// It returns the parent index of each node (-1 for roots).
func (f *File) validateNodes(c *collector) []int {
	d := f.Doc
	n := len(d.Nodes)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = -1
	}
	for i, nd := range d.Nodes {
		path := fmt.Sprintf("/nodes/%d", i)
		if nd.Mesh != nil && *nd.Mesh >= len(d.Meshes) {
			c.errf("NODE-MESH", path+"/mesh", "mesh %d is out of range (%d meshes)", *nd.Mesh, len(d.Meshes))
		}
		if nd.Skin != nil {
			if *nd.Skin >= len(d.Skins) {
				c.errf("NODE-SKIN", path+"/skin", "skin %d is out of range (%d skins)", *nd.Skin, len(d.Skins))
			}
			if nd.Mesh == nil {
				c.errf("NODE-SKIN", path+"/skin", "a node with a skin must also have a mesh")
			}
		}
		for k, ch := range nd.Children {
			cp := fmt.Sprintf("%s/children/%d", path, k)
			switch {
			case ch < 0:
			case ch >= n:
				c.errf("NODE-CHILD", cp, "child %d is out of range (%d nodes)", ch, n)
			case ch == i:
				c.errf("NODE-CYCLE", cp, "node %d is its own child", i)
			case parent[ch] != -1:
				c.errf("NODE-PARENTS", cp, "node %d already has parent %d", ch, parent[ch])
			default:
				parent[ch] = i
			}
		}
		if nd.Matrix != nil {
			for k, x := range nd.Matrix {
				if math.IsInf(x, 0) || math.IsNaN(x) {
					c.errf("NODE-MATRIX", fmt.Sprintf("%s/matrix/%d", path, k), "non-finite value")
				}
			}
			m := nd.Matrix
			if m[3] != 0 || m[7] != 0 || m[11] != 0 || m[15] != 1 {
				c.errf("NODE-MATRIX", path+"/matrix", "the bottom row must be (0,0,0,1): the node matrix must be affine")
			}
		}
		q := nd.Rotation
		if l := math.Sqrt(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3]); math.Abs(l-1) > 1e-3 {
			c.errf("NODE-ROTATION", path+"/rotation", "quaternion length is %v; it must be 1", l)
		}
		if nd.Mesh != nil && *nd.Mesh < len(d.Meshes) {
			want := 0
			if p := d.Meshes[*nd.Mesh].Primitives; len(p) > 0 {
				want = len(p[0].Targets)
			}
			if nd.Weights != nil && len(nd.Weights) != want {
				c.errf("NODE-WEIGHTS", path+"/weights", "%d weights for a mesh with %d morph targets", len(nd.Weights), want)
			}
		}
		if nd.Name != "" {
			// duplicate names are legal in glTF but make by-name measurement ambiguous
			for j := 0; j < i; j++ {
				if d.Nodes[j].Name == nd.Name {
					c.warnf("NODE-NAME-DUP", path+"/name", "name %q is also used by node %d; by-name lookups take the first", nd.Name, j)
					break
				}
			}
		}
	}
	// cycles: walk parent chains with three-colour marking
	state := make([]int8, n)
	for i := 0; i < n; i++ {
		if state[i] != 0 {
			continue
		}
		var path []int
		j := i
		for j != -1 && state[j] == 0 {
			state[j] = 1
			path = append(path, j)
			j = parent[j]
		}
		if j != -1 && state[j] == 1 {
			c.errf("NODE-CYCLE", fmt.Sprintf("/nodes/%d", j), "the node graph has a cycle through node %d", j)
		}
		for _, p := range path {
			state[p] = 2
		}
	}
	for si, s := range d.Scenes {
		seen := map[int]bool{}
		for k, r := range s.Nodes {
			sp := fmt.Sprintf("/scenes/%d/nodes/%d", si, k)
			switch {
			case r < 0:
			case r >= n:
				c.errf("SCENE-NODE", sp, "node %d is out of range (%d nodes)", r, n)
			case seen[r]:
				c.errf("SCENE-NODE", sp, "node %d is listed twice", r)
			case parent[r] != -1:
				c.errf("SCENE-NODE", sp, "node %d is a child of node %d; scenes list root nodes only", r, parent[r])
			}
			if r >= 0 {
				seen[r] = true
			}
		}
	}
	if d.Scene != nil && *d.Scene >= len(d.Scenes) {
		c.errf("SCENE-INDEX", "/scene", "scene %d is out of range (%d scenes)", *d.Scene, len(d.Scenes))
	}
	return parent
}

var attrSemantics = map[string]bool{"POSITION": true, "NORMAL": true, "TANGENT": true}

func semanticBase(name string) (base string, set int, ok bool) {
	for _, b := range []string{"TEXCOORD_", "COLOR_", "JOINTS_", "WEIGHTS_"} {
		if len(name) > len(b) && name[:len(b)] == b {
			n := 0
			for _, ch := range name[len(b):] {
				if ch < '0' || ch > '9' {
					return "", 0, false
				}
				n = n*10 + int(ch-'0')
				if n > 1000 {
					return "", 0, false
				}
			}
			return b[:len(b)-1], n, true
		}
	}
	return "", 0, false
}

func (f *File) accessorOK(i int) bool {
	return i >= 0 && i < len(f.accs) && f.accs[i].structOK
}

func (f *File) validateMeshes(c *collector) {
	d := f.Doc
	skinned := map[int]bool{} // meshes drawn by a node with a skin; validateSkinnedNodes covers their set 0
	for _, nd := range d.Nodes {
		if nd.Mesh != nil && nd.Skin != nil {
			skinned[*nd.Mesh] = true
		}
	}
	for mi, m := range d.Meshes {
		for pi, p := range m.Primitives {
			path := fmt.Sprintf("/meshes/%d/primitives/%d", mi, pi)
			f.validatePrimitive(c, path, p)
			checkSetPairs(c, path, p, skinned[mi])
		}
		if len(m.Primitives) > 0 {
			n0 := len(m.Primitives[0].Targets)
			for pi, p := range m.Primitives {
				if len(p.Targets) != n0 {
					c.errf("MESH-TARGETS", fmt.Sprintf("/meshes/%d/primitives/%d/targets", mi, pi), "primitive has %d targets; the first has %d", len(p.Targets), n0)
				}
			}
			if m.Weights != nil && len(m.Weights) != n0 {
				c.errf("MESH-WEIGHTS", fmt.Sprintf("/meshes/%d/weights", mi), "%d weights for %d morph targets", len(m.Weights), n0)
			}
			f.checkTargetNames(c, mi, m, n0)
		}
	}
	f.validateSkinnedNodes(c)
}

func (f *File) validatePrimitive(c *collector, path string, p Primitive) {
	d := f.Doc
	if p.Mode < 0 || p.Mode > 6 {
		c.errf("PRIM-MODE", path+"/mode", "mode %d is not in 0..6", p.Mode)
	} else if p.Mode != 4 {
		c.warnf("PRIM-MODE-UNMEASURED", path+"/mode", "mode %d is valid but the oracle measures triangle lists (mode 4) only", p.Mode)
	}
	if p.Material != nil && *p.Material >= len(d.Materials) {
		c.errf("PRIM-MATERIAL", path+"/material", "material %d is out of range (%d materials)", *p.Material, len(d.Materials))
	}
	pos, hasPos := p.Attributes["POSITION"]
	if !hasPos {
		c.errf("PRIM-POSITION", path+"/attributes", "the primitive has no POSITION attribute")
	}
	vcount := -1
	names := make([]string, 0, len(p.Attributes))
	for k := range p.Attributes {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		ai := p.Attributes[name]
		ap := path + "/attributes/" + name
		if ai >= len(d.Accessors) {
			c.errf("ATTR-ACCESSOR", ap, "accessor %d is out of range (%d accessors)", ai, len(d.Accessors))
			continue
		}
		a := d.Accessors[ai]
		if !f.accessorOK(ai) {
			continue
		}
		if vcount < 0 {
			vcount = a.Count
		} else if a.Count != vcount {
			c.errf("ATTR-COUNT", ap, "accessor %d has %d elements; the other attributes have %d", ai, a.Count, vcount)
		}
		if a.BufferView != nil && *a.BufferView < len(d.BufferViews) && d.BufferViews[*a.BufferView].Target == 34963 {
			c.errf("ATTR-VIEW-TARGET", ap, "attribute data is in a bufferView with target ELEMENT_ARRAY_BUFFER")
		}
		f.checkAttribute(c, ap, name, a, ai)
	}
	_ = pos
	f.checkPrimitiveWeights(c, path, p)
	// indices
	if p.Indices != nil {
		ip := path + "/indices"
		ii := *p.Indices
		if ii >= len(d.Accessors) {
			c.errf("INDEX-ACCESSOR", ip, "accessor %d is out of range (%d accessors)", ii, len(d.Accessors))
		} else if f.accessorOK(ii) {
			a := d.Accessors[ii]
			if a.Type != "SCALAR" || (a.ComponentType != 5121 && a.ComponentType != 5123 && a.ComponentType != 5125) {
				c.errf("INDEX-TYPE", ip, "indices must be SCALAR unsigned byte, short or int; got %s with component type %d", a.Type, a.ComponentType)
			} else {
				if a.BufferView != nil && *a.BufferView < len(d.BufferViews) {
					bv := d.BufferViews[*a.BufferView]
					if bv.Target == 34962 {
						c.errf("INDEX-VIEW-TARGET", ip, "index data is in a bufferView with target ARRAY_BUFFER")
					}
					if bv.ByteStride != 0 {
						c.errf("INDEX-VIEW-STRIDE", ip, "a bufferView used for indices must not set byteStride")
					}
				}
				if p.Mode == 4 && a.Count%3 != 0 {
					c.errf("INDEX-COUNT", ip, "%d indices is not a multiple of 3 for a triangle list", a.Count)
				}
				if idx, ok := f.Indices(ii); ok && vcount >= 0 {
					f.checkIndices(c, ip, idx, a.ComponentType, vcount, p.Mode)
				}
			}
		}
	} else if vcount >= 0 && p.Mode == 4 && vcount%3 != 0 {
		c.errf("INDEX-COUNT", path+"/attributes/POSITION", "%d vertices without indices is not a multiple of 3", vcount)
	}
	// morph targets
	for ti, t := range p.Targets {
		tp := fmt.Sprintf("%s/targets/%d", path, ti)
		for name, ai := range t {
			ap := tp + "/" + name
			if !attrSemantics[name] {
				c.errf("TARGET-SEMANTIC", ap, "morph targets may carry POSITION, NORMAL and TANGENT only")
				continue
			}
			if _, inBase := p.Attributes[name]; !inBase {
				c.errf("TARGET-BASE", ap, "the target changes %s but the base primitive has no %s", name, name)
			}
			if ai >= len(d.Accessors) {
				c.errf("TARGET-ACCESSOR", ap, "accessor %d is out of range", ai)
				continue
			}
			if !f.accessorOK(ai) {
				continue
			}
			a := d.Accessors[ai]
			if vcount >= 0 && a.Count != vcount {
				c.errf("TARGET-COUNT", ap, "target has %d elements; the base has %d", a.Count, vcount)
			}
			want := "VEC3"
			if a.Type != want || a.ComponentType != 5126 {
				c.errf("TARGET-TYPE", ap, "morph target data must be VEC3 FLOAT; got %s with component type %d", a.Type, a.ComponentType)
			}
		}
	}
}

func (f *File) checkIndices(c *collector, path string, idx []uint32, ct, vcount, mode int) {
	restart := uint32(0xFFFFFFFF)
	switch ct {
	case 5121:
		restart = 0xFF
	case 5123:
		restart = 0xFFFF
	}
	for k, v := range idx {
		if v == restart {
			c.errf("INDEX-RESTART", path, "index %d is the primitive restart value %#x, which glTF forbids", k, v)
			return
		}
		if int(v) >= vcount {
			c.errf("INDEX-RANGE", path, "index %d is %d but the primitive has %d vertices", k, v, vcount)
			return
		}
	}
	if mode == 4 {
		degenerate := 0
		for k := 0; k+2 < len(idx); k += 3 {
			a, b, cc := idx[k], idx[k+1], idx[k+2]
			if a == b || b == cc || a == cc {
				degenerate++
			}
		}
		if degenerate > 0 {
			c.warnf("INDEX-DEGENERATE", path, "%d triangles repeat a vertex index", degenerate)
		}
	}
}

func (f *File) checkAttribute(c *collector, ap, name string, a Accessor, ai int) {
	vals, ncomp, ok := f.Floats(ai)
	typeErr := func(want string, cts ...int) bool {
		good := a.Type == want
		if good {
			good = false
			for _, ct := range cts {
				if a.ComponentType == ct {
					good = true
				}
			}
		}
		if !good {
			c.errf("ATTR-TYPE", ap, "%s must be %s with a permitted component type; got %s with component type %d", name, want, a.Type, a.ComponentType)
		}
		return good
	}
	switch name {
	case "POSITION":
		if !typeErr("VEC3", 5126) {
			return
		}
		if !a.HasMinMax {
			c.errf("POSITION-MINMAX", ap, "POSITION accessors must carry min and max")
		}
	case "NORMAL":
		if !typeErr("VEC3", 5126) || !ok {
			return
		}
		bad, zero := 0, -1
		for e := 0; e < a.Count; e++ {
			x, y, z := vals[e*3], vals[e*3+1], vals[e*3+2]
			l := math.Sqrt(x*x + y*y + z*z)
			if l < 1e-9 && zero < 0 {
				zero = e
			}
			if math.Abs(l-1) > 0.005 {
				bad++
			}
		}
		if zero >= 0 {
			c.errf("NORMAL-ZERO", ap, "normal %d has zero length", zero)
		} else if bad > 0 {
			c.warnf("NORMAL-LENGTH", ap, "%d normals are not unit length (tolerance 0.005)", bad)
		}
	case "TANGENT":
		if !typeErr("VEC4", 5126) || !ok {
			return
		}
		for e := 0; e < a.Count; e++ {
			if w := vals[e*4+3]; w != 1 && w != -1 {
				c.errf("TANGENT-W", ap, "tangent %d has w=%v; it must be 1 or -1", e, w)
				break
			}
		}
	default:
		base, _, okb := semanticBase(name)
		if !okb {
			if len(name) == 0 || name[0] != '_' {
				c.warnf("ATTR-UNKNOWN", ap, "attribute %q is not a glTF semantic", name)
			}
			return
		}
		switch base {
		case "TEXCOORD":
			typeErr("VEC2", 5126, 5121, 5123)
		case "COLOR":
			if a.Type != "VEC3" && a.Type != "VEC4" {
				c.errf("ATTR-TYPE", ap, "COLOR must be VEC3 or VEC4; got %s", a.Type)
			}
		case "JOINTS":
			typeErr("VEC4", 5121, 5123)
		case "WEIGHTS":
			typeErr("VEC4", 5126, 5121, 5123) // the values are checked per primitive
		}
	}
	_ = ncomp
}

func (f *File) validateSkins(c *collector) {
	d := f.Doc
	for si, s := range d.Skins {
		path := fmt.Sprintf("/skins/%d", si)
		seen := map[int]bool{}
		for k, j := range s.Joints {
			jp := fmt.Sprintf("%s/joints/%d", path, k)
			if j >= len(d.Nodes) {
				c.errf("SKIN-JOINT", jp, "node %d is out of range (%d nodes)", j, len(d.Nodes))
			} else if seen[j] {
				c.errf("SKIN-JOINT", jp, "node %d is listed twice", j)
			}
			seen[j] = true
		}
		if s.Skeleton != nil && *s.Skeleton >= len(d.Nodes) {
			c.errf("SKIN-SKELETON", path+"/skeleton", "node %d is out of range", *s.Skeleton)
		}
		f.checkSkeleton(c, path, s)
		if s.InverseBindMatrices != nil {
			ai := *s.InverseBindMatrices
			ip := path + "/inverseBindMatrices"
			if ai >= len(d.Accessors) {
				c.errf("SKIN-IBM", ip, "accessor %d is out of range", ai)
			} else if f.accessorOK(ai) {
				a := d.Accessors[ai]
				if a.Type != "MAT4" || a.ComponentType != 5126 {
					c.errf("SKIN-IBM", ip, "inverse bind matrices must be MAT4 FLOAT; got %s with component type %d", a.Type, a.ComponentType)
				} else if a.Count != len(s.Joints) {
					c.errf("SKIN-IBM", ip, "%d matrices for %d joints", a.Count, len(s.Joints))
				}
			}
		}
	}
}

func (f *File) validateAnimations(c *collector) {
	d := f.Doc
	for ai, an := range d.Animations {
		path := fmt.Sprintf("/animations/%d", ai)
		for si, s := range an.Samplers {
			f.checkSamplerInput(c, fmt.Sprintf("%s/samplers/%d", path, si), s)
		}
		type target struct {
			node int
			path string
		}
		seen := map[target]int{}
		for ci, ch := range an.Channels {
			cp := fmt.Sprintf("%s/channels/%d", path, ci)
			if ch.Sampler >= len(an.Samplers) {
				c.errf("ANIM-CHANNEL", cp+"/sampler", "sampler %d is out of range (%d samplers)", ch.Sampler, len(an.Samplers))
				continue
			}
			s := an.Samplers[ch.Sampler]
			if ch.Node == nil {
				c.warnf("ANIM-TARGET-NONE", cp+"/target", "the channel has no target node; it animates nothing")
				continue
			}
			if *ch.Node >= len(d.Nodes) {
				c.errf("ANIM-CHANNEL", cp+"/target/node", "node %d is out of range (%d nodes)", *ch.Node, len(d.Nodes))
				continue
			}
			var want string
			switch ch.Path {
			case "translation", "scale":
				want = "VEC3"
			case "rotation":
				want = "VEC4"
			case "weights":
				want = "SCALAR"
			default:
				c.errf("ANIM-PATH", cp+"/target/path", "path %q is not translation, rotation, scale or weights", ch.Path)
				continue
			}
			key := target{*ch.Node, ch.Path}
			if first, dup := seen[key]; dup {
				c.errf("ANIM-DUP-TARGET", cp+"/target", "channel %d already animates %s of node %d", first, ch.Path, *ch.Node)
			} else {
				seen[key] = ci
			}
			node := d.Nodes[*ch.Node]
			if ch.Path != "weights" && node.Matrix != nil {
				c.errf("ANIM-TARGET-MATRIX", cp+"/target", "node %d has a matrix; its %s cannot be animated", *ch.Node, ch.Path)
			}
			nt := 0
			if ch.Path == "weights" {
				if node.Mesh != nil && *node.Mesh < len(d.Meshes) {
					if p := d.Meshes[*node.Mesh].Primitives; len(p) > 0 {
						nt = len(p[0].Targets)
					}
				}
				if nt == 0 {
					c.errf("ANIM-WEIGHTS-TARGET", cp+"/target", "node %d has no mesh with morph targets to animate", *ch.Node)
					continue
				}
			}
			if s.Input >= len(d.Accessors) || s.Output >= len(d.Accessors) || !f.accessorOK(s.Output) || !f.accessorOK(s.Input) {
				continue
			}
			out, in := d.Accessors[s.Output], d.Accessors[s.Input]
			if out.Type != want {
				c.errf("ANIM-OUTPUT", cp, "path %s needs %s output; accessor has %s", ch.Path, want, out.Type)
				continue
			}
			if !outputComponentOK(ch.Path, out) {
				c.errf("ANIM-OUTPUT-TYPE", cp, "%s output has component type %d (normalized %v), which glTF does not allow for this path", ch.Path, out.ComponentType, out.Normalized)
				continue
			}
			mult := 1
			if s.Interpolation == "CUBICSPLINE" {
				mult = 3
			}
			expect := in.Count * mult
			if ch.Path == "weights" {
				expect *= nt
			}
			if out.Count != expect {
				c.errf("ANIM-OUTPUT", cp, "output has %d elements; %s interpolation over %d times needs %d", out.Count, s.Interpolation, in.Count, expect)
				continue
			}
			if ch.Path == "rotation" {
				f.checkQuats(c, cp, s, out)
			}
		}
	}
}

// outputComponentOK applies the glTF rule for each animation path: translation
// and scale are FLOAT; rotation and weights are FLOAT or a normalized integer.
func outputComponentOK(path string, a Accessor) bool {
	if a.ComponentType == 5126 {
		return true
	}
	if !a.Normalized {
		return false
	}
	switch path {
	case "rotation":
		return a.ComponentType == 5120 || a.ComponentType == 5122
	case "weights":
		return a.ComponentType == 5120 || a.ComponentType == 5121 || a.ComponentType == 5122 || a.ComponentType == 5123
	}
	return false
}

func (f *File) checkSamplerInput(c *collector, sp string, s AnimSampler) {
	d := f.Doc
	switch s.Interpolation {
	case "LINEAR", "STEP", "CUBICSPLINE":
	default:
		c.errf("ANIM-INTERP", sp+"/interpolation", "interpolation %q is not LINEAR, STEP or CUBICSPLINE", s.Interpolation)
	}
	if s.Input >= len(d.Accessors) || s.Output >= len(d.Accessors) {
		c.errf("ANIM-ACCESSOR", sp, "input %d or output %d is out of range (%d accessors)", s.Input, s.Output, len(d.Accessors))
		return
	}
	if !f.accessorOK(s.Input) {
		return
	}
	in := d.Accessors[s.Input]
	if in.Type != "SCALAR" || in.ComponentType != 5126 {
		c.errf("ANIM-INPUT", sp+"/input", "sampler input must be SCALAR FLOAT times")
		return
	}
	if !in.HasMinMax {
		c.errf("ANIM-INPUT", sp+"/input", "sampler input accessors must carry min and max")
	}
	if s.Interpolation == "CUBICSPLINE" && in.Count < 2 {
		c.errf("ANIM-INPUT-COUNT", sp+"/input", "CUBICSPLINE needs at least two keys; the input has %d", in.Count)
	}
	if v, _, ok := f.Floats(s.Input); ok {
		for k, x := range v {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return // ACC-NONFINITE already reports it
			}
			if x < 0 || (k > 0 && x <= v[k-1]) {
				c.errf("ANIM-TIMES", sp+"/input", "time %d (%v) is negative or not greater than the one before", k, x)
				return
			}
		}
	}
}

// checkQuats warns about rotation keys that are not unit length. For
// CUBICSPLINE only the value element of each key (the middle of three) is a
// rotation; the tangents are free.
func (f *File) checkQuats(c *collector, cp string, s AnimSampler, out Accessor) {
	v, _, ok := f.Floats(s.Output)
	if !ok {
		return
	}
	step, first := 1, 0
	if s.Interpolation == "CUBICSPLINE" {
		step, first = 3, 1
	}
	for e := first; (e*4)+3 < len(v); e += step {
		q := v[e*4 : e*4+4]
		l := math.Sqrt(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3])
		if math.IsNaN(l) || math.IsInf(l, 0) {
			return // ACC-NONFINITE already reports it
		}
		if math.Abs(l-1) > 1e-3 {
			c.warnf("ANIM-QUAT", cp, "rotation key %d has length %v; it should be 1 within 1e-3", e/step, l)
			return
		}
	}
}

func (f *File) validateMaterialsAndImages(c *collector) {
	d := f.Doc
	for i, m := range d.Materials {
		if m.BaseColorFactor != nil {
			for k, x := range m.BaseColorFactor {
				if x < 0 || x > 1 {
					c.errf("MATERIAL-COLOR", fmt.Sprintf("/materials/%d/pbrMetallicRoughness/baseColorFactor/%d", i, k), "value %v is outside 0..1", x)
				}
			}
		}
	}
	for i, im := range d.Images {
		path := fmt.Sprintf("/images/%d", i)
		if (im.URI == "") == (im.BufferView == nil) {
			c.errf("IMAGE-SOURCE", path, "an image needs exactly one of uri and bufferView")
		}
		if im.BufferView != nil {
			if *im.BufferView >= len(d.BufferViews) {
				c.errf("IMAGE-VIEW", path+"/bufferView", "bufferView %d is out of range", *im.BufferView)
			}
			if im.MimeType == "" {
				c.errf("IMAGE-MIME", path+"/mimeType", "mimeType is required with bufferView")
			}
		}
	}
	for i, t := range d.Textures {
		path := fmt.Sprintf("/textures/%d", i)
		if t.Source != nil && *t.Source >= len(d.Images) {
			c.errf("TEXTURE-SOURCE", path+"/source", "image %d is out of range (%d images)", *t.Source, len(d.Images))
		}
		if t.Sampler != nil && *t.Sampler >= d.Samplers {
			c.errf("TEXTURE-SAMPLER", path+"/sampler", "sampler %d is out of range (%d samplers)", *t.Sampler, d.Samplers)
		}
	}
}
