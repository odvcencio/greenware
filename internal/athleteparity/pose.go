package athleteparity

import (
	"sort"

	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/gltf"
)

// Linear blend skinning of a decoded GLB, written for this package: it reads
// only the decoded document and shares no code with kernel/rig or with the
// compiler's own test evaluator (the approach is the one glTF states:
//
//	p_posed = sum_j w_j (W_j I_j) (p + sum_t weight_t delta_t)
//
// with W_j the world matrix of joint node j and I_j its inverse bind matrix).
// Products are written as float64(a*b) so that no architecture fuses them, and
// the square root, sine and arc cosine come from det, so the numbers are the
// same everywhere.

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

// trs is the matrix of a translation, a unit-normalised rotation (x y z w) and
// a scale: T R S.
func trs(t [3]float64, q [4]float64, s [3]float64) m4 {
	l := det.Sqrt(float64(q[0]*q[0]) + float64(q[1]*q[1]) + float64(q[2]*q[2]) + float64(q[3]*q[3]))
	x, y, z, w := q[0]/l, q[1]/l, q[2]/l, q[3]/l
	m := m4{
		1 - 2*(float64(y*y)+float64(z*z)), 2 * (float64(x*y) + float64(z*w)), 2 * (float64(x*z) - float64(y*w)), 0,
		2 * (float64(x*y) - float64(z*w)), 1 - 2*(float64(x*x)+float64(z*z)), 2 * (float64(y*z) + float64(x*w)), 0,
		2 * (float64(x*z) + float64(y*w)), 2 * (float64(y*z) - float64(x*w)), 1 - 2*(float64(x*x)+float64(y*y)), 0,
		0, 0, 0, 1,
	}
	for c := 0; c < 3; c++ {
		for r := 0; r < 3; r++ {
			m[c*4+r] = float64(m[c*4+r] * s[c])
		}
	}
	m[12], m[13], m[14] = t[0], t[1], t[2]
	return m
}

func scaleMat(s float64) m4 {
	m := ident4()
	m[0], m[5], m[10] = s, s, s
	return m
}

// slerp interpolates two quaternions on the shorter arc.
func slerp(a, b [4]float64, t float64) [4]float64 {
	d := float64(a[0]*b[0]) + float64(a[1]*b[1]) + float64(a[2]*b[2]) + float64(a[3]*b[3])
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
		th := det.Acos(d)
		s := det.Sin(th)
		wa, wb = det.Sin((1-t)*th)/s, det.Sin(t*th)/s
	}
	var o [4]float64
	for i := range o {
		o[i] = float64(wa*a[i]) + float64(wb*b[i])
	}
	return o
}

// sample evaluates one animation sampler at t, clamped to its keys.
func sample(s gltf.AnimSampler, comps int, t float64, rot bool) []float64 {
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
		q := slerp([4]float64{a[0], a[1], a[2], a[3]}, [4]float64{b[0], b[1], b[2], b[3]}, f)
		return q[:]
	}
	o := make([]float64, comps)
	for i := range o {
		o[i] = det.Lerp(a[i], b[i], f)
	}
	return o
}

func f3(v *[3]float32, def float64) [3]float64 {
	if v == nil {
		return [3]float64{def, def, def}
	}
	return [3]float64{float64(v[0]), float64(v[1]), float64(v[2])}
}

func f4(v *[4]float32) [4]float64 {
	if v == nil {
		return [4]float64{0, 0, 0, 1}
	}
	return [4]float64{float64(v[0]), float64(v[1]), float64(v[2]), float64(v[3])}
}

// HasClip reports whether the file has an animation of that name.
func (m *Model) HasClip(name string) bool {
	for _, a := range m.Doc.Animations {
		if a.Name == name {
			return true
		}
	}
	return false
}

// Posed returns every vertex in yards: the morph targets folded into the bind
// at the weights (targets by name; a name with no target is ignored), the
// clip sampled at t, the joints named in jointScale scaled as the game scales
// the head (the scale multiplies the joint's local matrix, so it also moves
// the joint's children), and the whole body scaled by bodyScale.
//
// clip may be "" for the bind pose with the node transforms as stored.
func (m *Model) Posed(weights map[string]float64, jointScale map[string]float64, bodyScale float64, clip string, t float64) [][3]float64 {
	d := m.Doc
	n := len(d.Nodes)
	T, Q, S := make([][3]float64, n), make([][4]float64, n), make([][3]float64, n)
	for i, nd := range d.Nodes {
		T[i], Q[i], S[i] = f3(nd.Translation, 0), f4(nd.Rotation), f3(nd.Scale, 1)
	}
	if clip != "" {
		for _, a := range d.Animations {
			if a.Name != clip {
				continue
			}
			for _, c := range a.Channels {
				s := a.Samplers[c.Sampler]
				if len(s.Input) == 0 {
					continue
				}
				switch c.Path {
				case gltf.PathTranslation:
					v := sample(s, 3, t, false)
					T[c.Node] = [3]float64{v[0], v[1], v[2]}
				case gltf.PathRotation:
					v := sample(s, 4, t, true)
					Q[c.Node] = [4]float64{v[0], v[1], v[2], v[3]}
				case gltf.PathScale:
					v := sample(s, 3, t, false)
					S[c.Node] = [3]float64{v[0], v[1], v[2]}
				}
			}
		}
	}
	parent := make([]int, n)
	for i := range parent {
		parent[i] = -1
	}
	for i, nd := range d.Nodes {
		for _, c := range nd.Children {
			parent[c] = i
		}
	}
	world := make([]m4, n)
	done := make([]bool, n)
	var visit func(i int) m4
	visit = func(i int) m4 {
		if done[i] {
			return world[i]
		}
		local := trs(T[i], Q[i], S[i])
		if s, ok := jointScale[d.Nodes[i].Name]; ok {
			local = local.mul(scaleMat(s))
		}
		if parent[i] >= 0 {
			local = visit(parent[i]).mul(local)
		}
		world[i], done[i] = local, true
		return local
	}
	for i := range d.Nodes {
		visit(i)
	}
	skin := d.Skins[*d.Nodes[m.skinNode].Skin]
	jm := make([]m4, len(skin.Joints))
	for j, jn := range skin.Joints {
		ibm := ident4()
		if j < len(skin.InverseBindMatrices) {
			for k, v := range skin.InverseBindMatrices[j] {
				ibm[k] = float64(v)
			}
		}
		jm[j] = world[jn].mul(ibm)
		// The matrix is in metres and the vertices are in yards: a change of
		// unit scales the translation column only.
		for c := 12; c < 15; c++ {
			jm[j][c] = float64(jm[j][c] * (1 / MetresPerYard))
		}
	}
	// Morph deltas, in the file's target order.
	type fold struct {
		w float64
		d [][3]float64
	}
	var folds []fold
	for _, name := range m.TargetNames {
		if w := weights[name]; w != 0 {
			folds = append(folds, fold{w, m.Targets[name]})
		}
	}
	out := make([][3]float64, len(m.Bind))
	for v, b := range m.Bind {
		p := b
		for _, f := range folds {
			for c := 0; c < 3; c++ {
				p[c] = p[c] + float64(f.w*f.d[v][c])
			}
		}
		var acc [3]float64
		for l := 0; l < 4; l++ {
			wt := float64(m.Weight[v][l])
			if wt == 0 {
				continue
			}
			q := jm[m.Joint[v][l]].point(p)
			for c := 0; c < 3; c++ {
				acc[c] += float64(wt * q[c])
			}
		}
		for c := 0; c < 3; c++ {
			acc[c] = float64(acc[c] * bodyScale)
		}
		out[v] = acc
	}
	return out
}
