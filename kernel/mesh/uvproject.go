package mesh

import (
	"fmt"
	"sort"

	"m31labs.dev/greenware/det"
)

// Projection is an explicit UV projection (language reference 4.5). The
// builders write their own chart ("auto"); ProjectUV replaces it by one of
// these, from positions alone, in the untransformed shape frame.
type Projection int

const (
	// ProjPlanar uses the local x and y. Both are normalised over their
	// bounds; a dimension of zero width is 0.5.
	ProjPlanar Projection = iota + 1
	// ProjBox picks, for each triangle, the plane perpendicular to the
	// largest absolute component of its normal (ties x, then y, then z) and
	// uses YZ, ZX or XY in that order, negating the first coordinate on a
	// negative face. Each projected coordinate is normalised over the bounds
	// of the triangles that share its plane and sign.
	ProjBox
	// ProjCylindrical uses the longitude about the axis and the normalised
	// height along it.
	ProjCylindrical
	// ProjSpherical uses the same longitude and the latitude fraction
	// acos(-d.axis)/pi of the unit direction d from the centre: v is 0 at the
	// south pole and 1 at the north pole, with no flip.
	ProjSpherical
)

// ProjectionOf maps the registry spelling of a projection.
func ProjectionOf(name string) (Projection, bool) {
	switch name {
	case "planar":
		return ProjPlanar, true
	case "box":
		return ProjBox, true
	case "cylindrical":
		return ProjCylindrical, true
	case "spherical":
		return ProjSpherical, true
	}
	return 0, false
}

// ProjectOptions are the frame of a periodic projection.
type ProjectOptions struct {
	// Axis is 0, 1 or 2 for x, y or z. Longitude runs about it: for +Y it is
	// atan2(x, z)/(2 pi), so +z is 0 and +x is 1/4. Other axes take the next
	// two axes in cyclic order.
	Axis int
	// Centre is the point the longitude and latitude are measured from. When
	// HasCentre is false the centre of the bounds is used.
	Centre    Vec3
	HasCentre bool
}

// ProjectUV returns a copy of m whose corners carry the projection's UVs. A
// corner used by triangles that want different UVs is duplicated, one copy
// per distinct UV, so a seam changes corners and never triangles: the
// positions, the triangles and their order stay as they were. A periodic
// projection lifts longitudes below the start of a triangle's interval by one,
// so a face may carry u above 1 (repeat wrapping); the values are not taken
// modulo one.
func (m *Mesh) ProjectUV(p Projection, opt ProjectOptions) (*Mesh, error) {
	if len(m.Tris) == 0 {
		return m.Clone(), nil
	}
	if opt.Axis < 0 || opt.Axis > 2 {
		return nil, fmt.Errorf("mesh: projection axis %d is not 0, 1 or 2", opt.Axis)
	}
	lo, hi, _ := m.Bounds()
	var uvs [][3]Vec2
	switch p {
	case ProjPlanar:
		uvs = m.projectPlanar(lo, hi)
	case ProjBox:
		uvs = m.projectBox()
	case ProjCylindrical, ProjSpherical:
		c := opt.Centre
		if !opt.HasCentre {
			c = Vec3{(lo[0] + hi[0]) / 2, (lo[1] + hi[1]) / 2, (lo[2] + hi[2]) / 2}
		}
		uvs = m.projectPeriodic(p, opt.Axis, c, lo, hi)
	default:
		return nil, fmt.Errorf("mesh: unknown projection %d", p)
	}
	out := &Mesh{Positions: append([]Vec3(nil), m.Positions...), Tris: make([]Tri, len(m.Tris)), Shell: append([]int32(nil), m.Shell...)}
	type key struct {
		corner int32
		uv     Vec2
	}
	made := map[key]int32{} // lookup only
	for i, t := range m.Tris {
		for k, ci := range t {
			kk := key{ci, uvs[i][k]}
			idx, ok := made[kk]
			if !ok {
				c := m.Corners[ci]
				c.UV = uvs[i][k]
				idx = int32(len(out.Corners))
				out.Corners = append(out.Corners, c)
				made[kk] = idx
			}
			out.Tris[i][k] = idx
		}
	}
	return out, nil
}

// norm01 is (x - lo)/(hi - lo), or 0.5 when the extent is zero.
func norm01(x, lo, hi float64) float64 {
	if !(hi > lo) {
		return 0.5
	}
	return (x - lo) / (hi - lo)
}

func (m *Mesh) triPos(i int) [3]Vec3 {
	a, b, c := m.TriPositions(i)
	return [3]Vec3{a, b, c}
}

func (m *Mesh) projectPlanar(lo, hi Vec3) [][3]Vec2 {
	uvs := make([][3]Vec2, len(m.Tris))
	for i := range m.Tris {
		for k, p := range m.triPos(i) {
			uvs[i][k] = Vec2{norm01(p[0], lo[0], hi[0]), norm01(p[1], lo[1], hi[1])}
		}
	}
	return uvs
}

// boxPlane returns the plane (0: YZ, 1: ZX, 2: XY) and the sign of triangle
// i's face.
func boxPlane(p [3]Vec3) (plane int, neg bool) {
	n := Cross(Sub(p[1], p[0]), Sub(p[2], p[0]))
	ax, ay, az := absf(n[0]), absf(n[1]), absf(n[2])
	switch {
	case ax >= ay && ax >= az:
		return 0, n[0] < 0
	case ay >= az:
		return 1, n[1] < 0
	}
	return 2, n[2] < 0
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func (m *Mesh) projectBox() [][3]Vec2 {
	type group struct {
		plane int
		neg   bool
	}
	type bnd struct {
		lo, hi Vec2
		set    bool
	}
	coords := func(g group, p Vec3) Vec2 {
		var a, b float64
		switch g.plane {
		case 0:
			a, b = p[1], p[2]
		case 1:
			a, b = p[2], p[0]
		default:
			a, b = p[0], p[1]
		}
		if g.neg {
			a = -a
		}
		return Vec2{a, b}
	}
	groups := make([]group, len(m.Tris))
	bounds := map[group]*bnd{} // lookup only
	for i := range m.Tris {
		pl, neg := boxPlane(m.triPos(i))
		g := group{pl, neg}
		groups[i] = g
		b := bounds[g]
		if b == nil {
			b = &bnd{}
			bounds[g] = b
		}
		for _, p := range m.triPos(i) {
			c := coords(g, p)
			if !b.set {
				b.lo, b.hi, b.set = c, c, true
				continue
			}
			for k := 0; k < 2; k++ {
				b.lo[k] = minf(b.lo[k], c[k])
				b.hi[k] = maxf(b.hi[k], c[k])
			}
		}
	}
	uvs := make([][3]Vec2, len(m.Tris))
	for i := range m.Tris {
		g := groups[i]
		b := bounds[g]
		for k, p := range m.triPos(i) {
			c := coords(g, p)
			uvs[i][k] = Vec2{norm01(c[0], b.lo[0], b.hi[0]), norm01(c[1], b.lo[1], b.hi[1])}
		}
	}
	return uvs
}

func minf(a, b float64) float64 {
	if b < a {
		return b
	}
	return a
}

func maxf(a, b float64) float64 {
	if b > a {
		return b
	}
	return a
}

// poleEps is the distance from the axis below which a corner is a pole. It is
// relative to the extent of the mesh.
const poleEps = 1e-9

func (m *Mesh) projectPeriodic(p Projection, axis int, c, lo, hi Vec3) [][3]Vec2 {
	a1, a2 := (axis+1)%3, (axis+2)%3 // longitude is atan2(a1, a2)
	extent := maxf(hi[0]-lo[0], maxf(hi[1]-lo[1], hi[2]-lo[2]))
	eps := float64(poleEps * extent)
	uvs := make([][3]Vec2, len(m.Tris))
	for i := range m.Tris {
		pos := m.triPos(i)
		var lon [3]float64
		var pole [3]bool
		var v [3]float64
		for k, q := range pos {
			d := Sub(q, c)
			x, z := d[a1], d[a2]
			if absf(x) <= eps && absf(z) <= eps {
				pole[k] = true
			} else {
				l := det.Atan2(x, z) / units2Pi
				if l < 0 {
					l++
				}
				if l >= 1 {
					l = 0
				}
				lon[k] = l
			}
			switch p {
			case ProjCylindrical:
				v[k] = norm01(q[axis], lo[axis], hi[axis])
			default:
				dir := Normalize(d)
				v[k] = det.Acos(-dir[axis]) / pi
			}
		}
		u := unwrap(lon, pole)
		for k := range pos {
			uvs[i][k] = Vec2{u[k], v[k]}
		}
	}
	return uvs
}

const (
	units2Pi = 6.283185307179586
	pi       = 3.141592653589793
)

// unwrap lifts the longitudes of one triangle onto one interval and gives a
// pole corner the midpoint of the other two. The interval is the cyclic one
// that deletes the largest gap between the non-pole longitudes; on a tie it
// starts at the smallest following longitude.
func unwrap(lon [3]float64, pole [3]bool) [3]float64 {
	var idx []int
	for k := range lon {
		if !pole[k] {
			idx = append(idx, k)
		}
	}
	var out [3]float64
	switch len(idx) {
	case 0:
		return out
	case 1:
		for k := range out {
			out[k] = lon[idx[0]]
		}
		return out
	}
	sort.SliceStable(idx, func(a, b int) bool { return lon[idx[a]] < lon[idx[b]] })
	// Gap g lies between idx[g] and the next longitude going up; the last
	// gap crosses the seam.
	n := len(idx)
	bestGap, start := -1.0, 0.0
	for g := 0; g < n; g++ {
		from, to := lon[idx[g]], lon[idx[(g+1)%n]]
		gap := to - from
		if g == n-1 {
			gap = 1 - from + to
		}
		if gap > bestGap || (gap == bestGap && to < start) {
			bestGap, start = gap, to
		}
	}
	for _, k := range idx {
		l := lon[k]
		if l < start {
			l++
		}
		out[k] = l
	}
	if len(idx) == 2 {
		for k := range pole {
			if pole[k] {
				out[k] = (out[idx[0]] + out[idx[1]]) / 2
			}
		}
	}
	return out
}
