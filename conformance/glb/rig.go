package glb

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Weight-sum thresholds. A vertex whose weights sum to 1 within weightOK is
// clean; within weightWarn it is a warning; beyond that it is an error. For
// normalized integer weights the stored value has a rounding step, so the
// allowed deviation grows by the largest error that rounding four components
// can cause (four half-steps).
const (
	weightOK   = 1e-5
	weightWarn = 1e-4
)

// weightQuantum is the extra allowed sum deviation for a stored component type.
func weightQuantum(ct int) float64 {
	switch ct {
	case 5121:
		return 2.0 / 255
	case 5123:
		return 2.0 / 65535
	}
	return 0
}

// checkPrimitiveWeights checks the WEIGHTS_n accessors of one primitive and
// reports at most one finding for it: a negative weight, or the worst vertex
// whose weights, added over every WEIGHTS_n set, do not sum to 1. (Sets beyond
// the first carry the influences past the fourth, so a set alone need not sum
// to 1.) Non-finite values are left to ACC-NONFINITE so that a NaN produces one
// finding, not two. Accessors of the wrong type are left to ATTR-TYPE.
func (f *File) checkPrimitiveWeights(c *collector, path string, p Primitive) {
	d := f.Doc
	var names []string
	for k := range p.Attributes {
		if b, _, ok := semanticBase(k); ok && b == "WEIGHTS" {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	var sets [][]float64
	var first string
	quantum, count := 0.0, -1
	for _, name := range names {
		ai := p.Attributes[name]
		if ai >= len(d.Accessors) || !f.accessorOK(ai) {
			return
		}
		a := d.Accessors[ai]
		if a.Type != "VEC4" || (a.ComponentType != 5126 && a.ComponentType != 5121 && a.ComponentType != 5123) {
			return
		}
		if a.ComponentType != 5126 && !a.Normalized {
			c.errf("WEIGHTS-NORMALIZED", path+"/attributes/"+name, "unsigned byte and unsigned short WEIGHTS must set normalized: true")
			return
		}
		vals, _, ok := f.Floats(ai)
		if !ok || (count >= 0 && a.Count != count) {
			return
		}
		count = a.Count
		for _, x := range vals {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return
			}
		}
		for e := 0; e < a.Count; e++ {
			for k := 0; k < 4; k++ {
				if vals[e*4+k] < 0 {
					c.errf("WEIGHTS-NEGATIVE", path+"/attributes/"+name, "vertex %d has a negative weight %v", e, vals[e*4+k])
					return
				}
			}
		}
		if first == "" {
			first = name
		}
		quantum += weightQuantum(a.ComponentType)
		sets = append(sets, vals)
	}
	if len(sets) == 0 {
		return
	}
	worst, at, sum := 0.0, -1, 0.0
	for e := 0; e < count; e++ {
		s := 0.0
		for _, v := range sets {
			s += v[e*4] + v[e*4+1] + v[e*4+2] + v[e*4+3]
		}
		if dev := math.Abs(s - 1); dev > worst {
			worst, at, sum = dev, e, s
		}
	}
	ap := path + "/attributes/" + first
	switch {
	case worst > weightWarn+quantum:
		c.errf("WEIGHTS-SUM", ap, "weights of vertex %d sum to %v; they must sum to 1 within %g", at, sum, weightWarn+quantum)
	case worst > weightOK+quantum:
		c.warnf("WEIGHTS-SUM", ap, "weights of vertex %d sum to %v; the tolerance is %g (a warning up to %g)", at, sum, weightOK+quantum, weightWarn+quantum)
	}
}

// checkSetPairs requires every JOINTS_n to have a WEIGHTS_n and the reverse.
// Set 0 is skipped when skip0 is true because the skinned-node check names the
// missing half itself.
func checkSetPairs(c *collector, path string, p Primitive, skip0 bool) {
	names := make([]string, 0, len(p.Attributes))
	for k := range p.Attributes {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		base, set, ok := semanticBase(name)
		if !ok || (base != "JOINTS" && base != "WEIGHTS") || (skip0 && set == 0) {
			continue
		}
		other := "WEIGHTS"
		if base == "WEIGHTS" {
			other = "JOINTS"
		}
		if _, has := p.Attributes[fmt.Sprintf("%s_%d", other, set)]; !has {
			c.errf("SKIN-ATTRS-PAIR", path+"/attributes/"+name, "%s has no matching %s_%d", name, other, set)
		}
	}
}

// validateSkinnedNodes checks, for every node that has both a mesh and a skin,
// that each primitive carries JOINTS_0 and WEIGHTS_0 and that no joint index
// reaches past the skin's joint list. A skin object alone proves nothing: a
// mesh without the two attributes is not skinned at all.
func (f *File) validateSkinnedNodes(c *collector) {
	d := f.Doc
	type key struct {
		mesh, prim int
		attr       string
	}
	done := map[key]bool{}
	for ni, nd := range d.Nodes {
		if nd.Mesh == nil || *nd.Mesh >= len(d.Meshes) {
			continue
		}
		mesh := d.Meshes[*nd.Mesh]
		if nd.Skin == nil || *nd.Skin >= len(d.Skins) {
			if nd.Skin == nil {
				for pi, p := range mesh.Primitives {
					if _, has := p.Attributes["JOINTS_0"]; has && !done[key{*nd.Mesh, pi, "unused"}] {
						done[key{*nd.Mesh, pi, "unused"}] = true
						c.warnf("SKIN-ATTRS-UNUSED", fmt.Sprintf("/nodes/%d", ni), "the mesh has JOINTS_0 but the node has no skin; the mesh is drawn unskinned")
					}
				}
			}
			continue
		}
		nj := len(d.Skins[*nd.Skin].Joints)
		for pi, p := range mesh.Primitives {
			pp := fmt.Sprintf("/meshes/%d/primitives/%d", *nd.Mesh, pi)
			var missing []string
			for _, need := range []string{"JOINTS_0", "WEIGHTS_0"} {
				if _, has := p.Attributes[need]; !has {
					missing = append(missing, need)
				}
			}
			if len(missing) > 0 && !done[key{*nd.Mesh, pi, "missing"}] {
				done[key{*nd.Mesh, pi, "missing"}] = true
				c.errf("SKIN-ATTRS", pp+"/attributes", "node %d has skin %d but the primitive has no %s", ni, *nd.Skin, strings.Join(missing, " or "))
			}
			names := make([]string, 0, len(p.Attributes))
			for k := range p.Attributes {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, name := range names {
				if b, _, ok := semanticBase(name); !ok || b != "JOINTS" || done[key{*nd.Mesh, pi, name}] {
					continue
				}
				done[key{*nd.Mesh, pi, name}] = true
				ai := p.Attributes[name]
				if ai >= len(d.Accessors) || !f.accessorOK(ai) {
					continue
				}
				if a := d.Accessors[ai]; a.Type != "VEC4" || (a.ComponentType != 5121 && a.ComponentType != 5123) {
					continue // ATTR-TYPE already reports it
				}
				v, _, ok := f.Floats(ai)
				if !ok {
					continue
				}
				for k, x := range v {
					if int(x) >= nj {
						c.errf("SKIN-JOINT-INDEX", pp+"/attributes/"+name,
							"joint index %d (vertex %d) is not below the %d joints of skin %d used by node %d", int(x), k/4, nj, *nd.Skin, ni)
						break
					}
				}
			}
		}
	}
}

// chainToRoot lists node i and its ancestors, nearest first. ok is false when
// the walk meets a cycle (NODE-CYCLE reports those).
func (f *File) chainToRoot(i int) (chain []int, ok bool) {
	n := len(f.parent)
	for steps := 0; i >= 0 && i < n; steps++ {
		if steps > n {
			return nil, false
		}
		chain = append(chain, i)
		i = f.parent[i]
	}
	return chain, true
}

// checkSkeleton requires the joints to hang from one root and, when the skin
// names a skeleton node, that node to be the closest common ancestor of the
// joints or one of its ancestors.
func (f *File) checkSkeleton(c *collector, path string, s Skin) {
	if len(s.Joints) == 0 {
		return
	}
	var chains [][]int
	for _, j := range s.Joints {
		if j < 0 || j >= len(f.parent) {
			return // SKIN-JOINT already reports it
		}
		ch, ok := f.chainToRoot(j)
		if !ok {
			return // NODE-CYCLE already reports it
		}
		chains = append(chains, ch)
	}
	root := chains[0][len(chains[0])-1]
	for _, ch := range chains {
		if ch[len(ch)-1] != root {
			c.errf("SKIN-ROOT", path+"/joints", "the joints do not share a common root: node %d and node %d are in different trees", chains[0][0], ch[0])
			return
		}
	}
	// closest common ancestor: the deepest node in every chain
	lca := root
	for depth := 1; ; depth++ {
		var cand int
		for k, ch := range chains {
			if depth > len(ch) {
				cand = -1
				break
			}
			v := ch[len(ch)-depth]
			if k == 0 {
				cand = v
			} else if v != cand {
				cand = -1
				break
			}
		}
		if cand < 0 {
			break
		}
		lca = cand
	}
	if s.Skeleton == nil || *s.Skeleton < 0 || *s.Skeleton >= len(f.parent) {
		return
	}
	ch, ok := f.chainToRoot(lca)
	if !ok {
		return
	}
	for _, n := range ch {
		if n == *s.Skeleton {
			return
		}
	}
	c.errf("SKIN-SKELETON", path+"/skeleton", "node %d is not the closest common ancestor of the joints (node %d) or one of its ancestors", *s.Skeleton, lca)
}

// TargetNames returns the morph target names a mesh declares in
// extras.targetNames (on the mesh, else on its first primitive). ok is false
// when none are declared or they are not an array of strings.
func (d *Document) TargetNames(mesh int) (names []string, ok bool) {
	if mesh < 0 || mesh >= len(d.Meshes) {
		return nil, false
	}
	m := d.Meshes[mesh]
	if n, ok, _ := namesIn(m.Extras); ok {
		return n, true
	}
	if len(m.Primitives) > 0 {
		if n, ok, _ := namesIn(m.Primitives[0].Extras); ok {
			return n, true
		}
	}
	return nil, false
}

// namesIn reads extras.targetNames. present reports whether the key exists.
func namesIn(extras any) (names []string, ok bool, present bool) {
	o, isObj := asObj(extras)
	if !isObj {
		return nil, false, false
	}
	v, has := o["targetNames"]
	if !has {
		return nil, false, false
	}
	a, isArr := v.([]any)
	if !isArr {
		return nil, false, true
	}
	for _, e := range a {
		s, isStr := e.(string)
		if !isStr {
			return nil, false, true
		}
		names = append(names, s)
	}
	return names, true, true
}

// checkTargetNames requires extras.targetNames, when present, to name every
// morph target once.
func (f *File) checkTargetNames(c *collector, mi int, m Mesh, targets int) {
	check := func(path string, extras any) {
		names, ok, present := namesIn(extras)
		if !present {
			return
		}
		if !ok {
			c.errf("MORPH-NAMES", path, "targetNames must be an array of strings")
			return
		}
		if len(names) != targets {
			c.errf("MORPH-NAMES", path, "%d target names for %d morph targets", len(names), targets)
			return
		}
		seen := map[string]bool{}
		for _, n := range names {
			if seen[n] {
				c.warnf("MORPH-NAMES-DUP", path, "target name %q is used more than once", n)
				return
			}
			seen[n] = true
		}
	}
	check(fmt.Sprintf("/meshes/%d/extras/targetNames", mi), m.Extras)
	for pi, p := range m.Primitives {
		check(fmt.Sprintf("/meshes/%d/primitives/%d/extras/targetNames", mi, pi), p.Extras)
	}
}
