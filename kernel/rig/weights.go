package rig

import (
	"fmt"
	"math"
	"sort"

	"m31labs.dev/greenware/kernel/mesh"
)

// Influence is one joint's share of a vertex.
type Influence struct {
	Joint int
	W     float64
}

// Bound is the geometry a skin is built on: every position of the body, the
// shape each came from, and the influences of each. Position i of every
// piece is one entry; a UV or normal split is not an entry of its own, so a
// written copy of a vertex takes its position's influences.
type Bound struct {
	Pos   []Vec
	Shape []int         // shape id of each position
	Infl  [][]Influence // influences of each position
}

// AddShape appends the positions of one shape, rigid on joint with weight 1,
// and returns the index of the first. Shape ids name the shapes that `only`,
// `rigid` and the morph part lists select.
func (b *Bound) AddShape(shape int, pos []Vec, joint int) int {
	first := len(b.Pos)
	for _, p := range pos {
		b.Pos = append(b.Pos, p)
		b.Shape = append(b.Shape, shape)
		b.Infl = append(b.Infl, []Influence{{Joint: joint, W: 1}})
	}
	return first
}

// weightOf returns the weight of joint j in infl.
func weightOf(infl []Influence, j int) float64 {
	var w float64
	for _, in := range infl {
		if in.Joint == j {
			w += in.W
		}
	}
	return w
}

// Lengthen sets the bone from joint to its single child to length `to`.
//
// With l the original bone length and s the axial coordinate of a vertex from
// the joint along the bone, a vertex bound to the joint maps [0, a*l]
// identically and [a*l, l] affinely onto [a*l, to]; before 0 it does not move
// and past l it moves by the constant to-l. Vertices of the shapes in rigid
// move by the displacement at the midpoint of their axial bounds. Vertices
// bound to the child's subtree move with the child. The child joint and its
// subtree move by (to-l) along the original bone direction. A vertex shared
// between joints takes the weighted sum of the displacements. It needs
// exactly one child, a nonzero length, and 0 <= anchor < 1 with anchor*l < to.
func (b *Bound) Lengthen(sk *Skeleton, joint int, to, anchor float64, rigid []int) error {
	kids := sk.Children(joint)
	if len(kids) != 1 {
		return fmt.Errorf("lengthen needs a joint with exactly one child, %q has %d", sk.Joints[joint].Name, len(kids))
	}
	child := kids[0]
	bone := mesh.Sub(sk.Joints[child].Pos, sk.Joints[joint].Pos)
	l := mesh.Len(bone)
	if !(l > 0) {
		return fmt.Errorf("lengthen needs a bone of nonzero length, %q has none", sk.Joints[joint].Name)
	}
	if !(to > 0) {
		return fmt.Errorf("lengthen `to` must be positive")
	}
	if !(anchor >= 0 && anchor < 1) {
		return fmt.Errorf("lengthen `anchor` must be at least 0 and below 1, found %v", anchor)
	}
	al := float64(anchor * l)
	if !(al < to) {
		return fmt.Errorf("lengthen needs anchor*length (%v) below `to` (%v)", al, to)
	}
	dir := Vec{bone[0] / l, bone[1] / l, bone[2] / l}
	origin := sk.Joints[joint].Pos
	shift := to - l
	// disp is the axial displacement of a vertex bound to the joint.
	disp := func(s float64) float64 {
		switch {
		case s <= al:
			return 0
		case s >= l:
			return shift
		}
		img := al + float64((s-al)*((to-al)/(l-al)))
		return img - s
	}
	isRigid := map[int]bool{} // lookup only
	for _, r := range rigid {
		isRigid[r] = true
	}
	// The midpoint displacement of each rigid shape, from the axial bounds of
	// the positions that are bound to the joint.
	type span struct{ lo, hi float64 }
	spans := map[int]*span{} // lookup only; read by key
	for i, p := range b.Pos {
		if !isRigid[b.Shape[i]] || weightOf(b.Infl[i], joint) == 0 {
			continue
		}
		s := mesh.Dot(mesh.Sub(p, origin), dir)
		sp := spans[b.Shape[i]]
		if sp == nil {
			spans[b.Shape[i]] = &span{lo: s, hi: s}
			continue
		}
		sp.lo, sp.hi = math.Min(sp.lo, s), math.Max(sp.hi, s)
	}
	for i, p := range b.Pos {
		var d float64
		for _, in := range b.Infl[i] {
			switch {
			case in.Joint == joint:
				var dd float64
				if isRigid[b.Shape[i]] {
					sp := spans[b.Shape[i]]
					dd = disp(float64((sp.lo + sp.hi) / 2))
				} else {
					dd = disp(mesh.Dot(mesh.Sub(p, origin), dir))
				}
				d += float64(in.W * dd)
			case sk.InSubtree(child, in.Joint):
				d += float64(in.W * shift)
			}
		}
		if d != 0 {
			b.Pos[i] = mesh.Add(p, mesh.Scale(dir, d))
		}
	}
	move := mesh.Scale(dir, shift)
	for k := range sk.Joints {
		if sk.InSubtree(child, k) {
			sk.Joints[k].Pos = mesh.Add(sk.Joints[k].Pos, move)
		}
	}
	return nil
}

// FadeX ramps a blend in across model X: smoothstep of (sign*x - lo)/(hi-lo).
// Sign is 1 for the authored side and -1 for its reflection, so the ramp
// increases away from the midline on each side.
type FadeX struct {
	Set    bool
	Lo, Hi float64
	Sign   float64
}

// Blend is a smooth weight band between a parent joint and its child.
type Blend struct {
	Parent, Child int
	Centre        Vec
	Axis          Vec // points toward increasing child weight; nonzero
	Width         float64
	Bias          float64
	Fade          FadeX
	// Only restricts the band to these shapes; empty means every shape.
	Only []int
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func smoothstep(t float64) float64 { return float64(float64(t*t) * (3 - float64(2*t))) }

// Blend runs one band. With d the signed distance of a position from the
// centre along the unit axis, t = clamp((1 + d/width)/2 + bias, 0, 1) and
// h = t^2 (3 - 2t); the pair's weight mass m = w_parent + w_child is split as
// m(1-h) and m h, and each weight moves from its old value toward that target
// by the fade. Other joints' weights are untouched. A zero pair mass is left
// alone.
func (b *Bound) Blend(bl Blend) error {
	if !(bl.Width > 0) {
		return fmt.Errorf("a blend `width` must be positive")
	}
	n := mesh.Len(bl.Axis)
	if !(n > 0) {
		return fmt.Errorf("a blend `axis` must have a nonzero direction")
	}
	axis := Vec{bl.Axis[0] / n, bl.Axis[1] / n, bl.Axis[2] / n}
	only := map[int]bool{} // lookup only
	for _, o := range bl.Only {
		only[o] = true
	}
	for i, p := range b.Pos {
		if len(bl.Only) > 0 && !only[b.Shape[i]] {
			continue
		}
		wp, wc := weightOf(b.Infl[i], bl.Parent), weightOf(b.Infl[i], bl.Child)
		m := wp + wc
		if m == 0 {
			continue
		}
		d := mesh.Dot(mesh.Sub(p, bl.Centre), axis)
		t := clamp01(float64((1+d/bl.Width)/2) + bl.Bias)
		h := smoothstep(t)
		f := 1.0
		if bl.Fade.Set {
			f = smoothstep(clamp01((float64(bl.Fade.Sign*p[0]) - bl.Fade.Lo) / (bl.Fade.Hi - bl.Fade.Lo)))
		}
		tp, tc := float64(m*(1-h)), float64(m*h)
		np := wp + float64(f*(tp-wp))
		nc := wc + float64(f*(tc-wc))
		out := make([]Influence, 0, len(b.Infl[i])+1)
		for _, in := range b.Infl[i] {
			if in.Joint != bl.Parent && in.Joint != bl.Child {
				out = append(out, in)
			}
		}
		out = append(out, Influence{bl.Parent, np}, Influence{bl.Child, nc})
		b.Infl[i] = out
	}
	return nil
}

// SortInfluences removes zero weights, combines duplicate joints and sorts by
// descending weight, then by joint index.
func SortInfluences(in []Influence) []Influence {
	byJoint := map[int]float64{} // lookup only
	var joints []int
	for _, x := range in {
		if _, ok := byJoint[x.Joint]; !ok {
			joints = append(joints, x.Joint)
		}
		byJoint[x.Joint] += x.W
	}
	var out []Influence
	for _, j := range joints {
		if w := byJoint[j]; w != 0 {
			out = append(out, Influence{j, w})
		}
	}
	sort.SliceStable(out, func(a, c int) bool {
		if out[a].W != out[c].W {
			return out[a].W > out[c].W
		}
		return out[a].Joint < out[c].Joint
	})
	return out
}

// weightSumTol is how far active weights may sum from 1.
const weightSumTol = 1e-6

// Finish sorts every position's influences and checks them: finite,
// nonnegative, summing to 1 within 1e-6, at most maxInfluences of them. It
// fails rather than drop a joint. The sums are then made exactly 1 within
// rounding by dividing by the sum.
func (b *Bound) Finish(maxInfluences int) error {
	for i := range b.Infl {
		in := SortInfluences(b.Infl[i])
		if len(in) == 0 {
			return fmt.Errorf("position %d has no influence", i)
		}
		var sum float64
		for _, x := range in {
			if math.IsNaN(x.W) || math.IsInf(x.W, 0) || x.W < 0 {
				return fmt.Errorf("position %d has the invalid weight %v on joint %d", i, x.W, x.Joint)
			}
			sum += x.W
		}
		if math.Abs(sum-1) > weightSumTol {
			return fmt.Errorf("the weights of position %d sum to %v, not 1", i, sum)
		}
		if len(in) > maxInfluences {
			return fmt.Errorf("position %d has %d influences; `max_influences` is %d", i, len(in), maxInfluences)
		}
		for k := range in {
			in[k].W /= sum
		}
		b.Infl[i] = in
	}
	return nil
}

// Limit keeps the n largest influences of in (already sorted by SortInfluences)
// and renormalises them to sum to 1. Ties were already ordered by joint index.
func Limit(in []Influence, n int) []Influence {
	if len(in) <= n {
		return in
	}
	out := append([]Influence(nil), in[:n]...)
	var sum float64
	for _, x := range out {
		sum += x.W
	}
	for k := range out {
		out[k].W /= sum
	}
	return out
}
