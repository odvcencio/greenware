package accept

import (
	"sort"

	"m31labs.dev/greenware/kernel/mesh"
)

type v3 = mesh.Vec3

// contactTolerance is the shell contact distance of language reference
// section 6.7: two shells join when their surfaces come within 1 mm.
const contactTolerance = 0.001

// rayEps is the distance below which a hit counts as "on the surface": one
// micrometre, a little over the spacing of binary32 positions at 5 m.
const rayEps = 1e-6

// baryEps lets a ray that runs exactly along the shared edge of two triangles
// hit both, so it cannot slip between them.
const baryEps = 1e-9

func axisIndex(a string) int {
	switch a {
	case "x", "+x", "-x":
		return 0
	case "y", "+y", "-y":
		return 1
	case "z", "+z", "-z":
		return 2
	}
	return -1
}

// rayHit is the intersection of a ray with a triangle.
type rayHit struct {
	t     float64
	front bool // the triangle faces the ray origin
}

// rayTri intersects the ray o + t*d with triangle tr (Moeller-Trumbore, edges
// inclusive). It reports every t, positive or not.
func rayTri(o, d v3, tr tri) (rayHit, bool) {
	e1 := mesh.Sub(tr[1], tr[0])
	e2 := mesh.Sub(tr[2], tr[0])
	p := mesh.Cross(d, e2)
	det0 := mesh.Dot(e1, p)
	if det0 > -1e-18 && det0 < 1e-18 {
		return rayHit{}, false // parallel to the plane
	}
	inv := 1 / det0
	s := mesh.Sub(o, tr[0])
	u := float64(mesh.Dot(s, p) * inv)
	if u < -baryEps || u > 1+baryEps {
		return rayHit{}, false
	}
	q := mesh.Cross(s, e1)
	v := float64(mesh.Dot(d, q) * inv)
	if v < -baryEps || u+v > 1+baryEps {
		return rayHit{}, false
	}
	t := float64(mesh.Dot(e2, q) * inv)
	n := mesh.Cross(e1, e2)
	return rayHit{t: t, front: mesh.Dot(n, d) < 0}, true
}

// gapResult is the outcome of a gap measurement.
type gapResult struct {
	neg, pos float64 // nearest hit distance on each side
	refusal  string
}

// gap casts from point at along +axis and -axis over tris and sums the
// nearest strictly positive hit on each side. It refuses when a side has no
// hit, when the point lies on a surface, or when the point lies inside the
// mesh (the nearest hit is a back face).
func gap(tris []tri, axis int, at v3) gapResult {
	if len(tris) == 0 {
		return gapResult{refusal: "the node has no visible triangles of its own"}
	}
	names := [3]string{"x", "y", "z"}
	var dist [2]float64
	for side, sign := range [2]float64{1, -1} {
		var d v3
		d[axis] = sign
		sideName := "+" + names[axis]
		if sign < 0 {
			sideName = "-" + names[axis]
		}
		best := -1.0
		bestFront := false
		have := false
		for _, tr := range tris {
			h, ok := rayTri(at, d, tr)
			if !ok {
				continue
			}
			if h.t >= -rayEps && h.t <= rayEps {
				return gapResult{refusal: "the point lies on a surface"}
			}
			if h.t < 0 {
				continue
			}
			switch {
			case !have || h.t < best-rayEps:
				best, bestFront, have = h.t, h.front, true
			case h.t <= best+rayEps && h.front:
				// A tie at an edge: any front face makes it a front hit.
				if h.t < best {
					best = h.t
				}
				bestFront = true
			}
		}
		if !have {
			return gapResult{refusal: "the ray along " + sideName + " misses the mesh"}
		}
		if !bestFront {
			return gapResult{refusal: "the point lies inside the mesh (the nearest hit along " + sideName + " is a back face)"}
		}
		dist[side] = best
	}
	return gapResult{pos: dist[0], neg: dist[1]}
}

// topology is the welded edge structure of a set of triangles.
type topology struct {
	tris      int
	shells    int
	shellOf   []int32 // shell id of each triangle
	open      int     // edges used once
	over      int     // edges used more than twice
	maxUses   int
	misorient int // edges used twice in the same direction
	// minorUses is the number of edges used a number of times other than 2.
	badEdges int
}

// meshOf copies the triangles into a mesh.Mesh with one corner per triangle
// corner, so the kernel's exact weld and edge counting apply.
func meshOf(tris []tri) *mesh.Mesh {
	m := &mesh.Mesh{}
	for i, tr := range tris {
		var t mesh.Tri
		for k := 0; k < 3; k++ {
			m.Positions = append(m.Positions, tr[k])
			m.Corners = append(m.Corners, mesh.Corner{Pos: int32(3*i + k)})
			t[k] = int32(3*i + k)
		}
		m.Tris = append(m.Tris, t)
		m.Shell = append(m.Shell, 0)
	}
	return m
}

// analyse welds by exact equality, counts edge uses, checks that the two uses
// of an edge run in opposite directions, and groups triangles into shells by
// shared edges (language reference, section 6.7).
func analyse(tris []tri) topology {
	tp := topology{tris: len(tris)}
	if len(tris) == 0 {
		return tp
	}
	m := meshOf(tris)
	w := m.Weld()
	for _, e := range m.EdgeUses() {
		switch {
		case e.Count == 1:
			tp.open++
			tp.badEdges++
		case e.Count > 2:
			tp.over++
			tp.badEdges++
		}
		if e.Count > tp.maxUses {
			tp.maxUses = e.Count
		}
	}
	type key struct{ a, b int32 }
	type use struct {
		tri int
		dir bool // true when the triangle runs a -> b with a < b
	}
	edges := map[key][]use{} // lookup only; keys are visited in sorted order below
	for i := range m.Tris {
		var v [3]int32
		for k, ci := range m.Tris[i] {
			v[k] = w.ID[m.Corners[ci].Pos]
		}
		for k := 0; k < 3; k++ {
			a, b := v[k], v[(k+1)%3]
			if a == b {
				continue
			}
			fwd := a < b
			if !fwd {
				a, b = b, a
			}
			edges[key{a, b}] = append(edges[key{a, b}], use{i, fwd})
		}
	}
	keys := make([]key, 0, len(edges))
	for k := range edges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].b < keys[j].b
	})
	parent := make([]int, len(tris))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for _, k := range keys {
		us := edges[k]
		if len(us) == 2 && us[0].dir == us[1].dir {
			tp.misorient++
		}
		for _, u := range us[1:] {
			a, b := find(us[0].tri), find(u.tri)
			if a != b {
				if b < a {
					a, b = b, a
				}
				parent[b] = a
			}
		}
	}
	tp.shellOf = make([]int32, len(tris))
	ids := map[int]int32{} // lookup only
	for i := range tris {
		r := find(i)
		id, ok := ids[r]
		if !ok {
			id = int32(len(ids))
			ids[r] = id
		}
		tp.shellOf[i] = id
	}
	tp.shells = len(ids)
	return tp
}

// closed is true when the mesh has triangles and every welded edge has
// exactly two uses in opposite directions.
func (tp topology) closed() bool {
	return tp.tris > 0 && tp.badEdges == 0 && tp.misorient == 0
}

// shellComponents groups shells that touch (minimum triangle separation of at
// most contactTolerance) and returns the number of groups.
func shellComponents(tris []tri, tp topology) int {
	n := tp.shells
	if n <= 1 {
		return n
	}
	byShell := make([][]int, n)
	for i, s := range tp.shellOf {
		byShell[s] = append(byShell[s], i)
	}
	triBox := func(tr tri) aabb {
		b := aabb{tr[0], tr[0]}
		for _, p := range tr[1:] {
			for k := 0; k < 3; k++ {
				if p[k] < b.lo[k] {
					b.lo[k] = p[k]
				}
				if p[k] > b.hi[k] {
					b.hi[k] = p[k]
				}
			}
		}
		for k := 0; k < 3; k++ {
			b.lo[k] -= contactTolerance
			b.hi[k] += contactTolerance
		}
		return b
	}
	boxes := make([]aabb, len(tris))
	shellBox := make([]aabb, n)
	for s, list := range byShell {
		for j, ti := range list {
			b := triBox(tris[ti])
			boxes[ti] = b
			if j == 0 {
				shellBox[s] = b
				continue
			}
			for k := 0; k < 3; k++ {
				if b.lo[k] < shellBox[s].lo[k] {
					shellBox[s].lo[k] = b.lo[k]
				}
				if b.hi[k] > shellBox[s].hi[k] {
					shellBox[s].hi[k] = b.hi[k]
				}
			}
		}
	}
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	groups := n
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if find(i) == find(j) || !shellBox[i].overlaps(shellBox[j]) {
				continue
			}
			if shellsTouch(tris, byShell[i], byShell[j], boxes) {
				parent[find(j)] = find(i)
				groups--
			}
		}
	}
	return groups
}

type aabb struct{ lo, hi v3 }

func (a aabb) overlaps(b aabb) bool {
	for k := 0; k < 3; k++ {
		if a.hi[k] < b.lo[k] || b.hi[k] < a.lo[k] {
			return false
		}
	}
	return true
}

func shellsTouch(tris []tri, a, b []int, boxes []aabb) bool {
	for _, i := range a {
		for _, j := range b {
			if !boxes[i].overlaps(boxes[j]) {
				continue
			}
			if triDistance(tris[i], tris[j]) <= contactTolerance {
				return true
			}
		}
	}
	return false
}

// triDistance is the minimum distance between two triangle surfaces. It is
// zero when they intersect. The minimum of two disjoint triangles is reached
// between two edges or between a corner and the other triangle.
func triDistance(a, b tri) float64 {
	for i := 0; i < 3; i++ {
		if segmentHitsTri(a[i], a[(i+1)%3], b) || segmentHitsTri(b[i], b[(i+1)%3], a) {
			return 0
		}
	}
	best := -1.0
	take := func(d float64) {
		if best < 0 || d < best {
			best = d
		}
	}
	for i := 0; i < 3; i++ {
		take(pointTriDistance(a[i], b))
		take(pointTriDistance(b[i], a))
		for j := 0; j < 3; j++ {
			take(segSegDistance(a[i], a[(i+1)%3], b[j], b[(j+1)%3]))
		}
	}
	return best
}

// segmentHitsTri reports whether the segment p-q pierces the triangle.
func segmentHitsTri(p, q v3, tr tri) bool {
	d := mesh.Sub(q, p)
	h, ok := rayTri(p, d, tr)
	return ok && h.t >= 0 && h.t <= 1
}

func pointTriDistance(p v3, tr tri) float64 {
	return mesh.Len(mesh.Sub(p, closestOnTri(p, tr[0], tr[1], tr[2])))
}

// closestOnTri is the point of triangle abc nearest p (Ericson, Real-Time
// Collision Detection, section 5.1.5).
func closestOnTri(p, a, b, c v3) v3 {
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

// segSegDistance is the distance between segments p1-q1 and p2-q2 (Ericson,
// section 5.1.9).
func segSegDistance(p1, q1, p2, q2 v3) float64 {
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

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// boundsOf returns the tight bounds of the triangles' corners.
func boundsOf(tris []tri) (lo, hi v3, ok bool) {
	for _, tr := range tris {
		for _, p := range tr {
			if !ok {
				lo, hi, ok = p, p, true
				continue
			}
			for k := 0; k < 3; k++ {
				if p[k] < lo[k] {
					lo[k] = p[k]
				}
				if p[k] > hi[k] {
					hi[k] = p[k]
				}
			}
		}
	}
	return lo, hi, ok
}
