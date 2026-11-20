package measure

import (
	"fmt"
	"math"
	"sort"
)

// weld maps each distinct binary32 position triple to an id. Two corners are
// the same vertex only when all three components are numerically equal (+0
// equals -0). There is no tolerance.
type weld struct {
	ids map[[3]float32]int
	pts [][3]float32
}

func newWeld() *weld { return &weld{ids: map[[3]float32]int{}} }

func (w *weld) id(p [3]float32) int {
	if i, ok := w.ids[p]; ok {
		return i
	}
	i := len(w.pts)
	w.ids[p] = i
	w.pts = append(w.pts, p)
	return i
}

type edgeUse struct{ fwd, bwd int } // uses a->b and b->a, with a<b

// ClosedResult is the incidence census of a selection.
type ClosedResult struct {
	Closed    bool
	Reason    string // why not closed; empty when closed
	Triangles int
	Vertices  int // welded vertices
	Edges     int // distinct undirected edges
	// UseHistogram[k] is the number of edges used by exactly k triangles.
	UseHistogram map[int]int
	OpenEdges    int // edges used once
	// SameDirectionEdges counts edges used twice in the same direction.
	SameDirectionEdges int
	// DegenerateTriangles repeat a welded vertex.
	DegenerateTriangles int
	// SignedVolume is positive when a closed shell is wound outward.
	SignedVolume float64
}

type edgeKey [2]int

func triIDs(w *weld, t Tri) [3]int {
	return [3]int{w.id(t[0]), w.id(t[1]), w.id(t[2])}
}

// Closed tests the selection for a closed surface. It requires at least one
// triangle and, for every undirected edge on exactly welded positions, exactly
// two uses in opposite directions. A triangle that repeats a welded vertex
// makes the result not closed. Four uses of an edge are not closed even when no
// edge is open.
func (m *Model) Closed(s Select) (ClosedResult, error) {
	tris, err := m.Triangles(s)
	if err != nil {
		return ClosedResult{}, err
	}
	return closedOf(tris), nil
}

func closedOf(tris []Tri) ClosedResult {
	r := ClosedResult{Triangles: len(tris), UseHistogram: map[int]int{}}
	w := newWeld()
	edges := map[edgeKey]*edgeUse{}
	for _, t := range tris {
		ids := triIDs(w, t)
		if ids[0] == ids[1] || ids[1] == ids[2] || ids[0] == ids[2] {
			r.DegenerateTriangles++
			continue
		}
		for k := 0; k < 3; k++ {
			a, b := ids[k], ids[(k+1)%3]
			key, fwd := edgeKey{a, b}, true
			if a > b {
				key, fwd = edgeKey{b, a}, false
			}
			e := edges[key]
			if e == nil {
				e = &edgeUse{}
				edges[key] = e
			}
			if fwd {
				e.fwd++
			} else {
				e.bwd++
			}
		}
		p0, p1, p2 := f64(t[0]), f64(t[1]), f64(t[2])
		r.SignedVolume += dot(p0, cross(p1, p2)) / 6
	}
	r.Vertices, r.Edges = len(w.pts), len(edges)
	bad := 0
	for _, e := range edges {
		n := e.fwd + e.bwd
		r.UseHistogram[n]++
		switch {
		case n == 1:
			r.OpenEdges++
			bad++
		case n == 2 && (e.fwd == 0 || e.bwd == 0):
			r.SameDirectionEdges++
			bad++
		case n != 2:
			bad++
		}
	}
	switch {
	case len(tris) == 0:
		r.Reason = "no drawn triangles"
	case r.DegenerateTriangles > 0:
		r.Reason = fmt.Sprintf("%d triangles repeat a welded vertex", r.DegenerateTriangles)
	case bad > 0:
		r.Reason = fmt.Sprintf("%d of %d edges do not have exactly two opposed uses (histogram %v)", bad, r.Edges, r.UseHistogram)
	default:
		r.Closed = true
	}
	return r
}

// ConnectedResult describes shells and their contact graph.
type ConnectedResult struct {
	Connected  bool
	Shells     int // groups of triangles joined by shared welded edges
	Components int // groups of shells joined by contact within the threshold
	Triangles  int
	// ThresholdM is the surface separation limit used.
	ThresholdM float64
}

// ContactThreshold is the largest surface separation, in metres, at which two
// shells are in contact (1 mm, inclusive).
const ContactThreshold = 1e-3

type dsu []int

func newDSU(n int) dsu {
	d := make(dsu, n)
	for i := range d {
		d[i] = i
	}
	return d
}

func (d dsu) find(x int) int {
	for d[x] != x {
		d[x] = d[d[x]]
		x = d[x]
	}
	return x
}

func (d dsu) union(a, b int) bool {
	a, b = d.find(a), d.find(b)
	if a == b {
		return false
	}
	d[a] = b
	return true
}

// Connected tests whether the selection forms one component. Shells are
// formed by triangles that share an exactly welded edge. Two shells touch when
// some pair of their triangles has surface separation of at most
// ContactThreshold (separation 0 when they intersect). The selection is
// connected when it has at least one triangle and a single contact component.
func (m *Model) Connected(s Select) (ConnectedResult, error) {
	tris, err := m.Triangles(s)
	if err != nil {
		return ConnectedResult{}, err
	}
	return connectedOf(tris, ContactThreshold), nil
}

func connectedOf(tris []Tri, tol float64) ConnectedResult {
	r := ConnectedResult{Triangles: len(tris), ThresholdM: tol}
	if len(tris) == 0 {
		return r
	}
	w := newWeld()
	tri := newDSU(len(tris))
	first := map[edgeKey]int{}
	for i, t := range tris {
		ids := triIDs(w, t)
		for k := 0; k < 3; k++ {
			a, b := ids[k], ids[(k+1)%3]
			if a == b {
				continue
			}
			if a > b {
				a, b = b, a
			}
			if j, ok := first[edgeKey{a, b}]; ok {
				tri.union(i, j)
			} else {
				first[edgeKey{a, b}] = i
			}
		}
	}
	shellOf := make([]int, len(tris))
	shellIDs := map[int]int{}
	for i := range tris {
		root := tri.find(i)
		if _, ok := shellIDs[root]; !ok {
			shellIDs[root] = len(shellIDs)
		}
		shellOf[i] = shellIDs[root]
	}
	r.Shells = len(shellIDs)
	comp := newDSU(r.Shells)
	r.Components = r.Shells
	if r.Shells > 1 {
		type bb struct{ lo, hi [3]float64 }
		boxes := make([]bb, len(tris))
		for i, t := range tris {
			b := bb{[3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}, [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}}
			for _, v := range t {
				for k := 0; k < 3; k++ {
					b.lo[k] = math.Min(b.lo[k], float64(v[k]))
					b.hi[k] = math.Max(b.hi[k], float64(v[k]))
				}
			}
			boxes[i] = b
		}
		order := make([]int, len(tris))
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(a, b int) bool { return boxes[order[a]].lo[0] < boxes[order[b]].lo[0] })
		for oi, i := range order {
			for _, j := range order[oi+1:] {
				if boxes[j].lo[0] > boxes[i].hi[0]+tol {
					break
				}
				if shellOf[i] == shellOf[j] || comp.find(shellOf[i]) == comp.find(shellOf[j]) {
					continue
				}
				if boxes[j].lo[1] > boxes[i].hi[1]+tol || boxes[i].lo[1] > boxes[j].hi[1]+tol ||
					boxes[j].lo[2] > boxes[i].hi[2]+tol || boxes[i].lo[2] > boxes[j].hi[2]+tol {
					continue
				}
				if triTriDistance(tris[i], tris[j]) <= tol {
					if comp.union(shellOf[i], shellOf[j]) {
						r.Components--
					}
				}
			}
		}
	}
	r.Connected = r.Components == 1
	return r
}

// ---- small vector helpers and triangle distance ----

type v3 = [3]float64

func f64(p [3]float32) v3 { return v3{float64(p[0]), float64(p[1]), float64(p[2])} }
func sub(a, b v3) v3      { return v3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func add(a, b v3) v3      { return v3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func scale(a v3, s float64) v3 {
	return v3{a[0] * s, a[1] * s, a[2] * s}
}
func dot(a, b v3) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func cross(a, b v3) v3 {
	return v3{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
func norm(a v3) float64 { return math.Sqrt(dot(a, a)) }

// closestOnTriangle is Ericson's closest point on triangle abc to p.
func closestOnTriangle(p, a, b, c v3) v3 {
	ab, ac, ap := sub(b, a), sub(c, a), sub(p, a)
	d1, d2 := dot(ab, ap), dot(ac, ap)
	if d1 <= 0 && d2 <= 0 {
		return a
	}
	bp := sub(p, b)
	d3, d4 := dot(ab, bp), dot(ac, bp)
	if d3 >= 0 && d4 <= d3 {
		return b
	}
	if vc := d1*d4 - d3*d2; vc <= 0 && d1 >= 0 && d3 <= 0 {
		return add(a, scale(ab, d1/(d1-d3)))
	}
	cp := sub(p, c)
	d5, d6 := dot(ab, cp), dot(ac, cp)
	if d6 >= 0 && d5 <= d6 {
		return c
	}
	if vb := d5*d2 - d1*d6; vb <= 0 && d2 >= 0 && d6 <= 0 {
		return add(a, scale(ac, d2/(d2-d6)))
	}
	if va := d3*d6 - d5*d4; va <= 0 && d4-d3 >= 0 && d5-d6 >= 0 {
		return add(b, scale(sub(c, b), (d4-d3)/((d4-d3)+(d5-d6))))
	}
	vc, vb, va := d1*d4-d3*d2, d5*d2-d1*d6, d3*d6-d5*d4
	den := 1 / (va + vb + vc)
	return add(a, add(scale(ab, vb*den), scale(ac, vc*den)))
}

// segSegDistance is the minimum distance between segments p1q1 and p2q2.
func segSegDistance(p1, q1, p2, q2 v3) float64 {
	d1, d2, r := sub(q1, p1), sub(q2, p2), sub(p1, p2)
	a, e, f := dot(d1, d1), dot(d2, d2), dot(d2, r)
	const eps = 1e-30
	var s, t float64
	switch {
	case a <= eps && e <= eps:
		return norm(r)
	case a <= eps:
		t = clamp01(f / e)
	default:
		c := dot(d1, r)
		if e <= eps {
			s = clamp01(-c / a)
		} else {
			b := dot(d1, d2)
			den := a*e - b*b
			if den > eps {
				s = clamp01((b*f - c*e) / den)
			}
			t = (b*s + f) / e
			if t < 0 {
				t, s = 0, clamp01(-c/a)
			} else if t > 1 {
				t, s = 1, clamp01((b-c)/a)
			}
		}
	}
	return norm(sub(add(p1, scale(d1, s)), add(p2, scale(d2, t))))
}

func clamp01(x float64) float64 { return math.Max(0, math.Min(1, x)) }

// segHitsTriangle reports whether segment pq crosses triangle abc.
func segHitsTriangle(p, q, a, b, c v3) bool {
	d := sub(q, p)
	e1, e2 := sub(b, a), sub(c, a)
	h := cross(d, e2)
	det := dot(e1, h)
	if math.Abs(det) < 1e-18 {
		return false
	}
	inv := 1 / det
	s := sub(p, a)
	u := inv * dot(s, h)
	if u < 0 || u > 1 {
		return false
	}
	qv := cross(s, e1)
	v := inv * dot(d, qv)
	if v < 0 || u+v > 1 {
		return false
	}
	t := inv * dot(e2, qv)
	return t >= 0 && t <= 1
}

// triTriDistance is the minimum distance between the surfaces of two triangles.
func triTriDistance(x, y Tri) float64 {
	xp := [3]v3{f64(x[0]), f64(x[1]), f64(x[2])}
	yp := [3]v3{f64(y[0]), f64(y[1]), f64(y[2])}
	for i := 0; i < 3; i++ {
		if segHitsTriangle(xp[i], xp[(i+1)%3], yp[0], yp[1], yp[2]) ||
			segHitsTriangle(yp[i], yp[(i+1)%3], xp[0], xp[1], xp[2]) {
			return 0
		}
	}
	best := math.Inf(1)
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			best = math.Min(best, segSegDistance(xp[i], xp[(i+1)%3], yp[j], yp[(j+1)%3]))
		}
		best = math.Min(best, norm(sub(xp[i], closestOnTriangle(xp[i], yp[0], yp[1], yp[2]))))
		best = math.Min(best, norm(sub(yp[i], closestOnTriangle(yp[i], xp[0], xp[1], xp[2]))))
	}
	return best
}
