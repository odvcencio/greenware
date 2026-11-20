// Package geom measures triangle soups: the distance between two triangles and
// the least distance between two sets of them, found with a bounding-volume
// tree. It is the kernel of the fit check (language reference 5.8): the gap
// between a mounted gear and the host's posed skin.
//
// Every floating-point multiply is wrapped in float64(...), which forbids a
// fused multiply-add on arm64, so amd64 and arm64 give the same bits.
package geom

import (
	"math"
	"sort"

	"m31labs.dev/greenware/kernel/mesh"
)

// Vec is a 3D point.
type Vec = mesh.Vec3

// Tri is a triangle by its three corners.
type Tri [3]Vec

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// segmentHitsTri reports whether the segment p-q pierces the triangle
// (Moeller-Trumbore, edges inclusive).
func segmentHitsTri(p, q Vec, tr Tri) bool {
	d := mesh.Sub(q, p)
	e1, e2 := mesh.Sub(tr[1], tr[0]), mesh.Sub(tr[2], tr[0])
	pv := mesh.Cross(d, e2)
	det := mesh.Dot(e1, pv)
	if det > -1e-18 && det < 1e-18 {
		return false
	}
	inv := 1 / det
	s := mesh.Sub(p, tr[0])
	u := float64(mesh.Dot(s, pv) * inv)
	if u < -1e-9 || u > 1+1e-9 {
		return false
	}
	qv := mesh.Cross(s, e1)
	v := float64(mesh.Dot(d, qv) * inv)
	if v < -1e-9 || u+v > 1+1e-9 {
		return false
	}
	t := float64(mesh.Dot(e2, qv) * inv)
	return t >= 0 && t <= 1
}

// closestOnTri is the point of triangle abc nearest p (Ericson, Real-Time
// Collision Detection, 5.1.5).
func closestOnTri(p, a, b, c Vec) Vec {
	ab, ac, ap := mesh.Sub(b, a), mesh.Sub(c, a), mesh.Sub(p, a)
	d1, d2 := mesh.Dot(ab, ap), mesh.Dot(ac, ap)
	if d1 <= 0 && d2 <= 0 {
		return a
	}
	bp := mesh.Sub(p, b)
	d3, d4 := mesh.Dot(ab, bp), mesh.Dot(ac, bp)
	if d3 >= 0 && d4 <= d3 {
		return b
	}
	vc := float64(d1*d4) - float64(d3*d2)
	if vc <= 0 && d1 >= 0 && d3 <= 0 {
		return mesh.Add(a, mesh.Scale(ab, d1/(d1-d3)))
	}
	cp := mesh.Sub(p, c)
	d5, d6 := mesh.Dot(ab, cp), mesh.Dot(ac, cp)
	if d6 >= 0 && d5 <= d6 {
		return c
	}
	vb := float64(d5*d2) - float64(d1*d6)
	if vb <= 0 && d2 >= 0 && d6 <= 0 {
		return mesh.Add(a, mesh.Scale(ac, d2/(d2-d6)))
	}
	va := float64(d3*d6) - float64(d5*d4)
	if va <= 0 && (d4-d3) >= 0 && (d5-d6) >= 0 {
		w := (d4 - d3) / ((d4 - d3) + (d5 - d6))
		return mesh.Add(b, mesh.Scale(mesh.Sub(c, b), w))
	}
	den := 1 / (va + vb + vc)
	return mesh.Add(a, mesh.Add(mesh.Scale(ab, float64(vb*den)), mesh.Scale(ac, float64(vc*den))))
}

// PointTriDistance is the distance from p to the triangle.
func PointTriDistance(p Vec, tr Tri) float64 {
	return mesh.Len(mesh.Sub(p, closestOnTri(p, tr[0], tr[1], tr[2])))
}

// segSegDistance is the distance between segments p1-q1 and p2-q2 (Ericson,
// 5.1.9).
func segSegDistance(p1, q1, p2, q2 Vec) float64 {
	d1, d2, r := mesh.Sub(q1, p1), mesh.Sub(q2, p2), mesh.Sub(p1, p2)
	a, e, f := mesh.Dot(d1, d1), mesh.Dot(d2, d2), mesh.Dot(d2, r)
	const eps = 1e-18
	var s, t float64
	switch {
	case a <= eps && e <= eps:
		return mesh.Len(r)
	case a <= eps:
		s, t = 0, clamp01(f/e)
	default:
		c := mesh.Dot(d1, r)
		if e <= eps {
			t, s = 0, clamp01(-c/a)
		} else {
			b := mesh.Dot(d1, d2)
			den := float64(a*e) - float64(b*b)
			if den > 0 {
				s = clamp01((float64(b*f) - float64(c*e)) / den)
			}
			t = (float64(b*s) + f) / e
			switch {
			case t < 0:
				t, s = 0, clamp01(-c/a)
			case t > 1:
				t, s = 1, clamp01((b-c)/a)
			}
		}
	}
	c1 := mesh.Add(p1, mesh.Scale(d1, s))
	c2 := mesh.Add(p2, mesh.Scale(d2, t))
	return mesh.Len(mesh.Sub(c1, c2))
}

// TriDistance is the least distance between two triangle surfaces: zero when
// they intersect, otherwise reached between two edges or between a corner and
// the other triangle.
func TriDistance(a, b Tri) float64 {
	for i := 0; i < 3; i++ {
		if segmentHitsTri(a[i], a[(i+1)%3], b) || segmentHitsTri(b[i], b[(i+1)%3], a) {
			return 0
		}
	}
	best := math.Inf(1)
	for i := 0; i < 3; i++ {
		best = math.Min(best, math.Min(PointTriDistance(a[i], b), PointTriDistance(b[i], a)))
		for j := 0; j < 3; j++ {
			best = math.Min(best, segSegDistance(a[i], a[(i+1)%3], b[j], b[(j+1)%3]))
		}
	}
	return best
}

// box is an axis-aligned bounding box.
type box struct{ lo, hi Vec }

func (b box) union(o box) box {
	for k := 0; k < 3; k++ {
		b.lo[k] = math.Min(b.lo[k], o.lo[k])
		b.hi[k] = math.Max(b.hi[k], o.hi[k])
	}
	return b
}

// boxDistance is the least distance between two boxes (zero when they touch).
func boxDistance(a, b box) float64 {
	var s float64
	for k := 0; k < 3; k++ {
		var d float64
		switch {
		case a.hi[k] < b.lo[k]:
			d = b.lo[k] - a.hi[k]
		case b.hi[k] < a.lo[k]:
			d = a.lo[k] - b.hi[k]
		}
		s += float64(d * d)
	}
	return math.Sqrt(s)
}

func triBox(t Tri) box {
	b := box{lo: t[0], hi: t[0]}
	for _, p := range t[1:] {
		for k := 0; k < 3; k++ {
			b.lo[k] = math.Min(b.lo[k], p[k])
			b.hi[k] = math.Max(b.hi[k], p[k])
		}
	}
	return b
}

const leafSize = 4

type node struct {
	box         box
	left, right int32 // children, or -1 for a leaf
	first, n    int32 // a leaf's range in the BVH's order
}

// BVH is a bounding-volume tree over a set of triangles.
type BVH struct {
	tris  []Tri
	order []int32 // triangle indices, leaf ranges are contiguous
	nodes []node
}

// NewBVH builds a tree by median split on the longest axis of the triangle
// centroids. The tree depends only on the triangles and their order.
func NewBVH(tris []Tri) *BVH {
	b := &BVH{tris: tris, order: make([]int32, len(tris))}
	for i := range b.order {
		b.order[i] = int32(i)
	}
	if len(tris) > 0 {
		b.build(0, len(tris))
	}
	return b
}

func (b *BVH) build(first, end int) int32 {
	bx := triBox(b.tris[b.order[first]])
	cl, ch := centroid(b.tris[b.order[first]]), centroid(b.tris[b.order[first]])
	for _, i := range b.order[first+1 : end] {
		bx = bx.union(triBox(b.tris[i]))
		c := centroid(b.tris[i])
		for k := 0; k < 3; k++ {
			cl[k], ch[k] = math.Min(cl[k], c[k]), math.Max(ch[k], c[k])
		}
	}
	idx := int32(len(b.nodes))
	b.nodes = append(b.nodes, node{box: bx, left: -1, right: -1, first: int32(first), n: int32(end - first)})
	if end-first <= leafSize {
		return idx
	}
	axis := 0
	for k := 1; k < 3; k++ {
		if ch[k]-cl[k] > ch[axis]-cl[axis] {
			axis = k
		}
	}
	sub := b.order[first:end]
	sort.SliceStable(sub, func(i, j int) bool { return centroid(b.tris[sub[i]])[axis] < centroid(b.tris[sub[j]])[axis] })
	mid := first + (end-first)/2
	l := b.build(first, mid)
	r := b.build(mid, end)
	b.nodes[idx].left, b.nodes[idx].right, b.nodes[idx].n = l, r, 0
	return idx
}

func centroid(t Tri) Vec {
	return Vec{(t[0][0] + t[1][0] + t[2][0]) / 3, (t[0][1] + t[1][1] + t[2][1]) / 3, (t[0][2] + t[1][2] + t[2][2]) / 3}
}

// Result is the closest pair found by MinDistance.
type Result struct {
	Distance float64
	// A and B are the indices of the triangles in the two sets.
	A, B int
	// Found is false when no pair is closer than the limit.
	Found bool
}

// MinDistance returns the closest pair of triangles, one from each tree,
// among pairs closer than limit. A pair at exactly limit is not found. Ties
// keep the first pair met, which is the same on every machine.
func MinDistance(x, y *BVH, limit float64) Result {
	r := Result{Distance: limit}
	if len(x.nodes) == 0 || len(y.nodes) == 0 {
		return r
	}
	var rec func(a, b int32)
	rec = func(a, b int32) {
		na, nb := &x.nodes[a], &y.nodes[b]
		if boxDistance(na.box, nb.box) >= r.Distance {
			return
		}
		switch {
		case na.left < 0 && nb.left < 0:
			for _, i := range x.order[na.first : na.first+na.n] {
				for _, j := range y.order[nb.first : nb.first+nb.n] {
					if r.Distance == 0 {
						return
					}
					if d := TriDistance(x.tris[i], y.tris[j]); d < r.Distance {
						r = Result{Distance: d, A: int(i), B: int(j), Found: true}
					}
				}
			}
		case nb.left < 0 || (na.left >= 0 && boxVolume(na.box) >= boxVolume(nb.box)):
			rec(na.left, b)
			rec(na.right, b)
		default:
			rec(a, nb.left)
			rec(a, nb.right)
		}
	}
	rec(0, 0)
	return r
}

func boxVolume(b box) float64 {
	return float64((b.hi[0]-b.lo[0])*(b.hi[1]-b.lo[1])) + float64((b.hi[2]-b.lo[2])*0.001)
}
