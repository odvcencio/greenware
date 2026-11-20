package gltf

import (
	"fmt"
	"math"
)

// weightSumTolerance is how far a vertex's skin weights may sum from 1.
const weightSumTolerance = 1e-5

// validateSkinAttrs checks JOINTS_0 and WEIGHTS_0 of one primitive (path p).
// The joint indices are checked against a skin in validateRig.
func validateSkinAttrs(p string, pr Primitive, nv int) error {
	if len(pr.Joints0) == 0 && len(pr.Weights0) == 0 {
		return nil
	}
	if len(pr.Joints0) != 4*nv {
		return errf(p+".attributes.JOINTS_0", "have %d values, need %d (4 per vertex; JOINTS_0 and WEIGHTS_0 go together)", len(pr.Joints0), 4*nv)
	}
	if len(pr.Weights0) != 4*nv {
		return errf(p+".attributes.WEIGHTS_0", "have %d values, need %d (4 per vertex; JOINTS_0 and WEIGHTS_0 go together)", len(pr.Weights0), 4*nv)
	}
	if err := finite(p+".attributes.WEIGHTS_0", pr.Weights0); err != nil {
		return err
	}
	for v := 0; v < nv; v++ {
		var sum float64
		nonzero := false
		for c := 0; c < 4; c++ {
			w := pr.Weights0[4*v+c]
			if w < 0 {
				return errf(fmt.Sprintf("%s.attributes.WEIGHTS_0[%d]", p, 4*v+c), "weight %v is negative", w)
			}
			if w != 0 {
				nonzero = true
			}
			sum += float64(w)
		}
		if nonzero && math.Abs(sum-1) > weightSumTolerance {
			return errf(fmt.Sprintf("%s.attributes.WEIGHTS_0[%d]", p, 4*v), "weights of vertex %d sum to %v; they must sum to 1 within %g", v, sum, weightSumTolerance)
		}
	}
	return nil
}

// validateTargets checks the morph targets of one primitive (path p).
func validateTargets(p string, pr Primitive, nv int) error {
	for t, tg := range pr.Targets {
		tp := fmt.Sprintf("%s.targets[%d]", p, t)
		if len(tg.Positions) != 0 {
			if len(tg.Positions) != 3*nv {
				return errf(tp+".POSITION", "have %d floats, need %d", len(tg.Positions), 3*nv)
			}
			if err := finite(tp+".POSITION", tg.Positions); err != nil {
				return err
			}
		}
		if len(tg.Normals) != 0 {
			if len(tg.Normals) != 3*nv {
				return errf(tp+".NORMAL", "have %d floats, need %d", len(tg.Normals), 3*nv)
			}
			if err := finite(tp+".NORMAL", tg.Normals); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateMeshWeights checks Mesh.Weights and Mesh.TargetNames against the
// number of morph targets. The caller has checked that all primitives agree.
func validateMeshWeights(p string, m Mesh) error {
	nt := 0
	if len(m.Primitives) > 0 {
		nt = len(m.Primitives[0].Targets)
	}
	if len(m.Weights) != 0 {
		if len(m.Weights) != nt {
			return errf(p+".weights", "have %d weights; the mesh has %d morph targets", len(m.Weights), nt)
		}
		if err := finite(p+".weights", m.Weights); err != nil {
			return err
		}
	}
	if len(m.TargetNames) != 0 {
		if len(m.TargetNames) != nt {
			return errf(p+".extras.targetNames", "have %d names; the mesh has %d morph targets", len(m.TargetNames), nt)
		}
		if _, err := mergeTargetNames(m.Extras, m.TargetNames, p+".extras"); err != nil {
			return err
		}
	}
	return nil
}

// meshTargets is the number of morph targets of mesh i (0 when out of range).
func (d *Document) meshTargets(i int) int {
	if i < 0 || i >= len(d.Meshes) || len(d.Meshes[i].Primitives) == 0 {
		return 0
	}
	return len(d.Meshes[i].Primitives[0].Targets)
}

// pathComps is the number of floats per value for a channel path, or 0 for an
// unknown path. For "weights" the value count is the mesh's target count, so
// the second result says the accessor is SCALAR with that many per key.
func pathComps(path string) int {
	switch path {
	case PathTranslation, PathScale:
		return 3
	case PathRotation:
		return 4
	case PathWeights:
		return 1
	}
	return 0
}

// validateRig checks skins, joint indices against skins, and animations.
func (d *Document) validateRig() error {
	for i, s := range d.Skins {
		p := fmt.Sprintf("skins[%d]", i)
		if len(s.Joints) == 0 {
			return errf(p+".joints", "a skin needs at least one joint")
		}
		seen := make(map[int]bool, len(s.Joints))
		for j, n := range s.Joints {
			jp := fmt.Sprintf("%s.joints[%d]", p, j)
			if n < 0 || n >= len(d.Nodes) {
				return errf(jp, "index %d out of range (have %d nodes)", n, len(d.Nodes))
			}
			if seen[n] {
				return errf(jp, "node %d is listed twice", n)
			}
			seen[n] = true
		}
		if s.Skeleton != nil && (*s.Skeleton < 0 || *s.Skeleton >= len(d.Nodes)) {
			return errf(p+".skeleton", "index %d out of range (have %d nodes)", *s.Skeleton, len(d.Nodes))
		}
		if len(s.InverseBindMatrices) != 0 {
			if len(s.InverseBindMatrices) != len(s.Joints) {
				return errf(p+".inverseBindMatrices", "have %d matrices; the skin has %d joints", len(s.InverseBindMatrices), len(s.Joints))
			}
			for k, m := range s.InverseBindMatrices {
				if err := finite(fmt.Sprintf("%s.inverseBindMatrices[%d]", p, k), m[:]); err != nil {
					return err
				}
			}
		}
	}
	// The largest joint index of each primitive, found once per mesh so that
	// many nodes sharing a mesh do not rescan its vertices.
	topJoint := map[[2]int]int{}
	for i, n := range d.Nodes {
		if n.Skin == nil || n.Mesh == nil {
			continue
		}
		nj := len(d.Skins[*n.Skin].Joints)
		for j, pr := range d.Meshes[*n.Mesh].Primitives {
			k := [2]int{*n.Mesh, j}
			top, ok := topJoint[k]
			if !ok {
				top = -1
				for _, v := range pr.Joints0 {
					if int(v) > top {
						top = int(v)
					}
				}
				topJoint[k] = top
			}
			if top >= nj {
				for q, v := range pr.Joints0 {
					if int(v) >= nj {
						return errf(fmt.Sprintf("meshes[%d].primitives[%d].attributes.JOINTS_0[%d]", *n.Mesh, j, q),
							"joint index %d is not below the %d joints of skins[%d] (used by nodes[%d])", v, nj, *n.Skin, i)
					}
				}
			}
		}
	}
	for i, a := range d.Animations {
		if err := d.validateAnimation(fmt.Sprintf("animations[%d]", i), a); err != nil {
			return err
		}
	}
	return nil
}

func (d *Document) validateAnimation(p string, a Animation) error {
	if len(a.Channels) == 0 {
		return errf(p+".channels", "an animation needs at least one channel")
	}
	if len(a.Samplers) == 0 {
		return errf(p+".samplers", "an animation needs at least one sampler")
	}
	for i, s := range a.Samplers {
		sp := fmt.Sprintf("%s.samplers[%d]", p, i)
		switch s.Interpolation {
		case "", InterpLinear, InterpStep, InterpCubicSpline:
		default:
			return errf(sp+".interpolation", "%q is not LINEAR, STEP or CUBICSPLINE", s.Interpolation)
		}
		if len(s.Input) == 0 {
			return errf(sp+".input", "a sampler needs at least one keyframe time")
		}
		if s.Interpolation == InterpCubicSpline && len(s.Input) < 2 {
			return errf(sp+".input", "CUBICSPLINE needs at least two keyframes")
		}
		for k, t := range s.Input {
			if !finite32(t) {
				return errf(fmt.Sprintf("%s.input[%d]", sp, k), "time is %v; NaN and infinity are not allowed", t)
			}
			if t < 0 {
				return errf(fmt.Sprintf("%s.input[%d]", sp, k), "time %v is negative", t)
			}
			if k > 0 && !(t > s.Input[k-1]) {
				return errf(fmt.Sprintf("%s.input[%d]", sp, k), "time %v does not exceed the previous time %v; times must increase strictly", t, s.Input[k-1])
			}
		}
		if err := finite(sp+".output", s.Output); err != nil {
			return err
		}
	}
	used := make([]int, len(a.Samplers)) // floats per value of the first channel that uses the sampler
	type key struct {
		node int
		path string
	}
	seen := map[key]bool{}
	for i, c := range a.Channels {
		cp := fmt.Sprintf("%s.channels[%d]", p, i)
		if c.Node < 0 || c.Node >= len(d.Nodes) {
			return errf(cp+".target.node", "index %d out of range (have %d nodes)", c.Node, len(d.Nodes))
		}
		comps := pathComps(c.Path)
		if comps == 0 {
			return errf(cp+".target.path", "%q is not translation, rotation, scale or weights", c.Path)
		}
		if c.Sampler < 0 || c.Sampler >= len(a.Samplers) {
			return errf(cp+".sampler", "index %d out of range (have %d)", c.Sampler, len(a.Samplers))
		}
		k := key{c.Node, c.Path}
		if seen[k] {
			return errf(cp+".target", "node %d path %q is driven by another channel of this animation", c.Node, c.Path)
		}
		seen[k] = true
		per := comps // floats per key, before CUBICSPLINE
		if c.Path == PathWeights {
			nm := d.Nodes[c.Node].Mesh
			if nm == nil || d.meshTargets(*nm) == 0 {
				return errf(cp+".target.path", "weights need a node whose mesh has morph targets")
			}
			per = d.meshTargets(*nm)
		}
		s := a.Samplers[c.Sampler]
		groups := 1
		if s.Interpolation == InterpCubicSpline {
			groups = 3
		}
		if want := len(s.Input) * per * groups; len(s.Output) != want {
			return errf(fmt.Sprintf("%s.samplers[%d].output", p, c.Sampler), "have %d floats; %s on node %d with %d keys needs %d",
				len(s.Output), c.Path, c.Node, len(s.Input), want)
		}
		if used[c.Sampler] != 0 && used[c.Sampler] != comps {
			return errf(cp+".sampler", "sampler %d is shared by channels with different value types", c.Sampler)
		}
		used[c.Sampler] = comps
	}
	for i, u := range used {
		if u == 0 {
			return errf(fmt.Sprintf("%s.samplers[%d]", p, i), "no channel uses this sampler")
		}
	}
	return nil
}
