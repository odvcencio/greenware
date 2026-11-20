package measure

import (
	"fmt"
	"math"
)

// GapStatus is the outcome of a gap measurement. Only GapOK can pass.
type GapStatus string

// Gap statuses.
const (
	GapOK           GapStatus = "ok"
	GapMissingPos   GapStatus = "missing_hit_positive"
	GapMissingNeg   GapStatus = "missing_hit_negative"
	GapOnSurface    GapStatus = "origin_on_surface"
	GapOriginInside GapStatus = "origin_inside"
	GapBadAxis      GapStatus = "bad_axis"
	GapNoTriangles  GapStatus = "no_triangles"
)

// OnSurfaceTolerance is the distance, in metres, under which a hit counts as
// the ray origin lying on the surface.
const OnSurfaceTolerance = 1e-6

// GapResult is the distance between the nearest surfaces on both sides of a point.
type GapResult struct {
	Status   GapStatus
	Distance float64 // metres; valid when Status is GapOK
	Pos, Neg float64 // distance to the nearest hit along +axis and -axis
	Message  string
}

// OK reports whether the gap was measured cleanly.
func (g GapResult) OK() bool { return g.Status == GapOK }

// Gap casts a ray from the point `at` along +axis and another along -axis
// against the drawn triangles of the selection (the whole subtree unless Own
// is set). It returns the sum of the distances to the nearest hit on each side.
//
// The measurement refuses, with a status other than GapOK, when either side has
// no hit, when a hit lies within OnSurfaceTolerance of the origin, and when the
// nearest hit on either side is a back face (the origin is inside a solid wound
// outward, or the winding is inverted). Hits count on both faces of a triangle;
// a ray that passes exactly along a shared edge or through a shared vertex still
// hits, because edge tests are inclusive. Triangles parallel to the ray are skipped.
func (m *Model) Gap(s Select, axis string, at [3]float64) (GapResult, error) {
	tris, err := m.Triangles(s)
	if err != nil {
		return GapResult{}, err
	}
	return gapOf(tris, axis, at), nil
}

func gapOf(tris []Tri, axis string, at [3]float64) GapResult {
	var ax int
	switch axis {
	case "x":
		ax = 0
	case "y":
		ax = 1
	case "z":
		ax = 2
	default:
		return GapResult{Status: GapBadAxis, Message: fmt.Sprintf("axis %q is not x, y or z", axis)}
	}
	if len(tris) == 0 {
		return GapResult{Status: GapNoTriangles, Message: "the selection draws no triangles"}
	}
	var dir v3
	dir[ax] = 1
	type hit struct {
		t     float64
		front bool
		ok    bool
	}
	var pos, neg hit
	onSurface := false
	origin := v3(at)
	for _, tr := range tris {
		a, b, c := f64(tr[0]), f64(tr[1]), f64(tr[2])
		t, ok := rayLine(origin, dir, a, b, c)
		if !ok {
			continue
		}
		n := cross(sub(b, a), sub(c, a))
		if math.Abs(t) <= OnSurfaceTolerance {
			onSurface = true
			continue
		}
		if t > 0 {
			if !pos.ok || t < pos.t {
				pos = hit{t, n[ax] < 0, true}
			}
		} else {
			if !neg.ok || -t < neg.t {
				neg = hit{-t, n[ax] > 0, true}
			}
		}
	}
	res := GapResult{Pos: pos.t, Neg: neg.t}
	switch {
	case onSurface:
		res.Status, res.Message = GapOnSurface, "the origin lies on a surface"
	case !pos.ok:
		res.Status, res.Message = GapMissingPos, "no hit along +"+axis
	case !neg.ok:
		res.Status, res.Message = GapMissingNeg, "no hit along -"+axis
	case !pos.front || !neg.front:
		res.Status, res.Message = GapOriginInside, "the nearest hit is a back face: the origin is inside a solid"
	default:
		res.Status, res.Distance = GapOK, pos.t+neg.t
	}
	return res
}

// rayLine intersects the infinite line origin + t*dir with triangle abc and
// returns signed t. Edge tests are inclusive within a small barycentric slack.
func rayLine(origin, dir, a, b, c v3) (float64, bool) {
	e1, e2 := sub(b, a), sub(c, a)
	h := cross(dir, e2)
	det := dot(e1, h)
	if math.Abs(det) < 1e-18 {
		return 0, false
	}
	inv := 1 / det
	s := sub(origin, a)
	u := inv * dot(s, h)
	const slack = 1e-9
	if u < -slack || u > 1+slack {
		return 0, false
	}
	q := cross(s, e1)
	v := inv * dot(dir, q)
	if v < -slack || u+v > 1+slack {
		return 0, false
	}
	return inv * dot(e2, q), true
}
