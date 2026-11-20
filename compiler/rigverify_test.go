package compiler

import (
	"math"
	"sort"

	"m31labs.dev/greenware/gltf"
)

// An independent evaluator of a decoded rigged GLB. It reads only the decoded
// document, uses its own matrix and quaternion code (not package kernel/rig)
// and applies linear blend skinning as the glTF specification states it:
//
//	p_posed = sum_j w_j (W_j I_j) (p + sum_t weight_t delta_t)
//
// W_j is the world transform of joint node j under the sampled animation, I_j
// its inverse bind matrix.

type m4 [16]float64 // column-major, as glTF stores it

func ident4() m4 { return m4{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1} }

func (a m4) mul(b m4) m4 {
	var o m4
	for c := 0; c < 4; c++ {
		for r := 0; r < 4; r++ {
			var s float64
			for k := 0; k < 4; k++ {
				s += float64(a[k*4+r] * b[c*4+k])
			}
			o[c*4+r] = s
		}
	}
	return o
}

func (a m4) point(p [3]float64) [3]float64 {
	var o [3]float64
	for r := 0; r < 3; r++ {
		o[r] = float64(a[r]*p[0]) + float64(a[4+r]*p[1]) + float64(a[8+r]*p[2]) + a[12+r]
	}
	return o
}

func quatMat(q [4]float64) m4 {
	l := math.Sqrt(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3])
	x, y, z, w := q[0]/l, q[1]/l, q[2]/l, q[3]/l
	return m4{
		1 - 2*(y*y+z*z), 2 * (x*y + z*w), 2 * (x*z - y*w), 0,
		2 * (x*y - z*w), 1 - 2*(x*x+z*z), 2 * (y*z + x*w), 0,
		2 * (x*z + y*w), 2 * (y*z - x*w), 1 - 2*(x*x+y*y), 0,
		0, 0, 0, 1,
	}
}

func trsMat(t [3]float64, q [4]float64) m4 {
	m := quatMat(q)
	m[12], m[13], m[14] = t[0], t[1], t[2]
	return m
}

// slerpQ interpolates on the shortest arc.
func slerpQ(a, b [4]float64, t float64) [4]float64 {
	d := a[0]*b[0] + a[1]*b[1] + a[2]*b[2] + a[3]*b[3]
	if d < 0 {
		for i := range b {
			b[i] = -b[i]
		}
		d = -d
	}
	var wa, wb float64
	if d > 0.9995 {
		wa, wb = 1-t, t
	} else {
		th := math.Acos(d)
		wa, wb = math.Sin((1-t)*th)/math.Sin(th), math.Sin(t*th)/math.Sin(th)
	}
	var o [4]float64
	for i := range o {
		o[i] = wa*a[i] + wb*b[i]
	}
	return o
}

type rigEval struct {
	doc *gltf.Document
}

func f3(v *[3]float32) [3]float64 {
	if v == nil {
		return [3]float64{}
	}
	return [3]float64{float64(v[0]), float64(v[1]), float64(v[2])}
}

func f4(v *[4]float32) [4]float64 {
	if v == nil {
		return [4]float64{0, 0, 0, 1}
	}
	return [4]float64{float64(v[0]), float64(v[1]), float64(v[2]), float64(v[3])}
}

// sampleChannel evaluates one animation sampler at t, clamped to its keys.
func sampleChannel(s gltf.AnimSampler, comps int, t float64, rot bool) []float64 {
	n := len(s.Input)
	val := func(k int) []float64 {
		v := make([]float64, comps)
		for i := range v {
			v[i] = float64(s.Output[k*comps+i])
		}
		return v
	}
	if t <= float64(s.Input[0]) {
		return val(0)
	}
	if t >= float64(s.Input[n-1]) {
		return val(n - 1)
	}
	k := sort.Search(n, func(i int) bool { return float64(s.Input[i]) > t }) - 1
	t0, t1 := float64(s.Input[k]), float64(s.Input[k+1])
	a, b := val(k), val(k+1)
	if s.Interpolation == gltf.InterpStep {
		return a
	}
	f := (t - t0) / (t1 - t0)
	if rot {
		q := slerpQ([4]float64{a[0], a[1], a[2], a[3]}, [4]float64{b[0], b[1], b[2], b[3]}, f)
		return q[:]
	}
	o := make([]float64, comps)
	for i := range o {
		o[i] = a[i] + f*(b[i]-a[i])
	}
	return o
}

// locals returns the local TRS of every node, with the named animation
// sampled at t ("" for none).
func (r rigEval) locals(anim string, t float64) (T [][3]float64, Q [][4]float64) {
	d := r.doc
	T, Q = make([][3]float64, len(d.Nodes)), make([][4]float64, len(d.Nodes))
	for i, n := range d.Nodes {
		T[i], Q[i] = f3(n.Translation), f4(n.Rotation)
	}
	for _, a := range d.Animations {
		if a.Name != anim {
			continue
		}
		for _, c := range a.Channels {
			s := a.Samplers[c.Sampler]
			switch c.Path {
			case gltf.PathTranslation:
				v := sampleChannel(s, 3, t, false)
				T[c.Node] = [3]float64{v[0], v[1], v[2]}
			case gltf.PathRotation:
				v := sampleChannel(s, 4, t, true)
				Q[c.Node] = [4]float64{v[0], v[1], v[2], v[3]}
			}
		}
	}
	return T, Q
}

// worlds composes the node hierarchy.
func (r rigEval) worlds(T [][3]float64, Q [][4]float64) []m4 {
	d := r.doc
	parent := make([]int, len(d.Nodes))
	for i := range parent {
		parent[i] = -1
	}
	for i, n := range d.Nodes {
		for _, c := range n.Children {
			parent[c] = i
		}
	}
	w := make([]m4, len(d.Nodes))
	done := make([]bool, len(d.Nodes))
	var visit func(i int) m4
	visit = func(i int) m4 {
		if done[i] {
			return w[i]
		}
		m := trsMat(T[i], Q[i])
		if parent[i] >= 0 {
			m = visit(parent[i]).mul(m)
		}
		w[i], done[i] = m, true
		return m
	}
	for i := range d.Nodes {
		visit(i)
	}
	return w
}

// posed returns the skinned positions of every vertex of every primitive of
// the first skinned node, with the animation at t and the morph weights given
// (nil for the mesh defaults).
func (r rigEval) posed(anim string, t float64, weights []float64) [][3]float64 {
	d := r.doc
	T, Q := r.locals(anim, t)
	w := r.worlds(T, Q)
	var node int
	for i, n := range d.Nodes {
		if n.Skin != nil {
			node = i
			break
		}
	}
	mesh := d.Meshes[*d.Nodes[node].Mesh]
	skin := d.Skins[*d.Nodes[node].Skin]
	if weights == nil {
		for _, x := range mesh.Weights {
			weights = append(weights, float64(x))
		}
	}
	jm := make([]m4, len(skin.Joints))
	for j, jn := range skin.Joints {
		var ibm m4
		for k, v := range skin.InverseBindMatrices[j] {
			ibm[k] = float64(v)
		}
		jm[j] = w[jn].mul(ibm)
	}
	var out [][3]float64
	for _, p := range mesh.Primitives {
		for v := 0; v < len(p.Positions)/3; v++ {
			pos := [3]float64{float64(p.Positions[3*v]), float64(p.Positions[3*v+1]), float64(p.Positions[3*v+2])}
			for ti, tg := range p.Targets {
				if ti < len(weights) {
					for c := 0; c < 3; c++ {
						pos[c] += weights[ti] * float64(tg.Positions[3*v+c])
					}
				}
			}
			var acc [3]float64
			for l := 0; l < 4; l++ {
				wt := float64(p.Weights0[4*v+l])
				if wt == 0 {
					continue
				}
				q := jm[p.Joints0[4*v+l]].point(pos)
				for c := 0; c < 3; c++ {
					acc[c] += wt * q[c]
				}
			}
			out = append(out, acc)
		}
	}
	return out
}

// bindPositions returns the POSITION of every vertex in primitive order.
func bindPositions(d *gltf.Document) [][3]float64 {
	var out [][3]float64
	for _, n := range d.Nodes {
		if n.Skin == nil {
			continue
		}
		for _, p := range d.Meshes[*n.Mesh].Primitives {
			for v := 0; v < len(p.Positions)/3; v++ {
				out = append(out, [3]float64{float64(p.Positions[3*v]), float64(p.Positions[3*v+1]), float64(p.Positions[3*v+2])})
			}
		}
		break
	}
	return out
}

// jointsOf returns the joint indices and weights of every vertex.
func skinAttrs(d *gltf.Document) (js [][4]uint16, ws [][4]float32) {
	for _, n := range d.Nodes {
		if n.Skin == nil {
			continue
		}
		for _, p := range d.Meshes[*n.Mesh].Primitives {
			for v := 0; v < len(p.Positions)/3; v++ {
				var j [4]uint16
				var w [4]float32
				copy(j[:], p.Joints0[4*v:4*v+4])
				copy(w[:], p.Weights0[4*v:4*v+4])
				js, ws = append(js, j), append(ws, w)
			}
		}
		break
	}
	return js, ws
}

func dist3(a, b [3]float64) float64 {
	return math.Sqrt((a[0]-b[0])*(a[0]-b[0]) + (a[1]-b[1])*(a[1]-b[1]) + (a[2]-b[2])*(a[2]-b[2]))
}

// rotMat builds Rx(x) Ry(y) Rz(z) from angles in degrees, as plain matrices.
func rotMat(xd, yd, zd float64) m4 {
	r := func(d float64) (s, c float64) { return math.Sin(d * math.Pi / 180), math.Cos(d * math.Pi / 180) }
	sx, cx := r(xd)
	sy, cy := r(yd)
	sz, cz := r(zd)
	rx := m4{1, 0, 0, 0, 0, cx, sx, 0, 0, -sx, cx, 0, 0, 0, 0, 1}
	ry := m4{cy, 0, -sy, 0, 0, 1, 0, 0, sy, 0, cy, 0, 0, 0, 0, 1}
	rz := m4{cz, sz, 0, 0, -sz, cz, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	return rx.mul(ry).mul(rz)
}

func transMat(x, y, z float64) m4 {
	m := ident4()
	m[12], m[13], m[14] = x, y, z
	return m
}
