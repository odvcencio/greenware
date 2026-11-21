package build

import (
	"errors"
	"fmt"

	"m31labs.dev/greenware/kernel/mesh"
)

// A ring stack is k rings of columns joined by bands of quads. Loft and
// Ellipsoid are both ring stacks; they differ in how they place the rings.
//
// Columns are indexed from zero. A full turn with n segments has n columns
// per ring and the last quad of each band wraps from column n-1 back to
// column 0; the first position is reused, never recomputed at 2*pi. A partial
// arc with n intervals has n+1 columns and does not wrap (language.md
// section 3.5). A ring whose columns all hold one position is a pole: it has
// one position and one corner per interval, and its band is one triangle per
// interval instead of a collapsed quad and a degenerate triangle.

// stackParams is the input of ringStack.
type stackParams struct {
	rings [][]mesh.Vec3
	wrap  bool      // full turn: columns wrap
	v     []float64 // texture v of each ring
	caps  Caps
	// noSeam drops the repeated column of a full turn: every ring has n
	// corners and the last band's texture u runs from (n-1)/n back to 0, as
	// a plain ring stack's does. Ignored on an arc, which has n+1 real columns.
	noSeam bool
	// shareCaps makes each cap a fan over the end ring's own corners, so it
	// takes the side's smooth normals and adds no vertices.
	shareCaps bool
}

// stackCols returns the column count per ring and the number of cells in a
// band (the intervals around the ring).
func (p *stackParams) cells() (cols, n int) {
	cols = len(p.rings[0])
	n = cols
	if !p.wrap {
		n = cols - 1
	}
	return cols, n
}

func isPole(r []mesh.Vec3) bool {
	for _, p := range r[1:] {
		if p != r[0] {
			return false
		}
	}
	return true
}

// ringStack builds the shell. Corners of the side carry smooth normals; the
// two caps, when asked for, get their own corners with flat normals and share
// ring positions with the side, so a capped stack is closed.
func ringStack(p stackParams) (*mesh.Mesh, error) {
	k := len(p.rings)
	if k < 2 {
		return nil, errors.New("build: a ring stack needs at least 2 rings")
	}
	cols, n := p.cells()
	if (p.wrap && n < 3) || (!p.wrap && n < 1) {
		return nil, errors.New("build: a ring needs at least 3 segments around a full turn, or 1 interval on an arc")
	}
	for i, r := range p.rings {
		if len(r) != cols {
			return nil, fmt.Errorf("build: ring %d has %d columns, want %d", i, len(r), cols)
		}
		if i > 0 && sameRing(r, p.rings[i-1]) {
			return nil, fmt.Errorf("build: rings %d and %d coincide", i-1, i)
		}
		if i > 0 && isPole(r) && isPole(p.rings[i-1]) {
			return nil, errors.New("build: two adjacent rings both collapse to a point")
		}
	}
	if p.caps != CapsOff && !p.wrap {
		return nil, errors.New("build: caps need a full turn; an open arc is a sheet, so use caps = off")
	}
	pole := make([]bool, k)
	posBase := make([]int32, k)
	cornerBase := make([]int32, k)
	m := &mesh.Mesh{}
	seamless := p.noSeam && p.wrap
	for i, r := range p.rings {
		pole[i] = isPole(r)
		posBase[i] = int32(len(m.Positions))
		if pole[i] {
			m.Positions = append(m.Positions, r[0])
		} else {
			m.Positions = append(m.Positions, r...)
		}
		cornerBase[i] = int32(len(m.Corners))
		v := p.v[i]
		if pole[i] {
			for j := 0; j < n; j++ {
				m.Corners = append(m.Corners, mesh.Corner{Pos: posBase[i], UV: mesh.Vec2{(float64(j) + 0.5) / float64(n), v}})
			}
			continue
		}
		last := n
		if seamless {
			last = n - 1
		}
		for j := 0; j <= last; j++ {
			m.Corners = append(m.Corners, mesh.Corner{Pos: posBase[i] + int32(j%cols), UV: mesh.Vec2{float64(j) / float64(n), v}})
		}
	}
	corner := func(r, j int) int32 {
		if seamless && !pole[r] {
			j %= n
		}
		return cornerBase[r] + int32(j)
	}
	tri := func(a, b, c int32) {
		m.Tris = append(m.Tris, mesh.Tri{a, b, c})
		m.Shell = append(m.Shell, 0)
	}
	for r := 0; r+1 < k; r++ {
		for j := 0; j < n; j++ {
			switch {
			case pole[r]:
				tri(corner(r, j), corner(r+1, j+1), corner(r+1, j))
			case pole[r+1]:
				tri(corner(r, j), corner(r, j+1), corner(r+1, j))
			default:
				a, b, c, d := corner(r, j), corner(r, j+1), corner(r+1, j+1), corner(r+1, j)
				tri(a, b, c)
				tri(a, c, d)
			}
		}
	}
	m.ComputeNormals()
	if p.caps == CapsBoth || p.caps == CapsStart {
		if p.shareCaps {
			err := sharedCap(m, p.rings[0], pole[0], func(j int) int32 { return corner(0, j) }, false)
			if err != nil {
				return nil, err
			}
		} else if err := stackCap(m, p.rings[0], posBase[0], pole[0], false); err != nil {
			return nil, err
		}
	}
	if p.caps == CapsBoth || p.caps == CapsEnd {
		if p.shareCaps {
			err := sharedCap(m, p.rings[k-1], pole[k-1], func(j int) int32 { return corner(k-1, j) }, true)
			if err != nil {
				return nil, err
			}
		} else if err := stackCap(m, p.rings[k-1], posBase[k-1], pole[k-1], true); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func sameRing(a, b []mesh.Vec3) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ringNormal is the unit normal of the polygon through the ring's columns, in
// column order (Newell's method). A convex ring that increases in angle about
// +Y gives +Y.
func ringNormal(r []mesh.Vec3) mesh.Vec3 {
	var s mesh.Vec3
	for i := range r {
		a, b := r[i], r[(i+1)%len(r)]
		s[0] += float64(float64(a[1]-b[1]) * float64(a[2]+b[2]))
		s[1] += float64(float64(a[2]-b[2]) * float64(a[0]+b[0]))
		s[2] += float64(float64(a[0]-b[0]) * float64(a[1]+b[1]))
	}
	return mesh.Normalize(s)
}

// stackCap adds a fan over a ring from its first column: n-2 triangles and no
// centre vertex. The end cap (the last ring) winds with the ring's column
// order; the start cap winds the other way. The cap's corners are its own, so
// its flat normal does not blend into the side.
func stackCap(m *mesh.Mesh, ring []mesh.Vec3, posBase int32, pole, end bool) error {
	if pole {
		return errors.New("build: a cap needs an end ring that is not a point")
	}
	n := len(ring)
	nrm := ringNormal(ring)
	if nrm == (mesh.Vec3{}) {
		return errors.New("build: a cap ring has no area")
	}
	if !end {
		nrm = mesh.Neg(nrm)
	}
	// Planar projection perpendicular to the largest component of the normal,
	// normalised over the ring's bounds; 0.5 on a zero-width side.
	ax := 0
	for i := 1; i < 3; i++ {
		if abs(nrm[i]) > abs(nrm[ax]) {
			ax = i
		}
	}
	iu, iv := (ax+1)%3, (ax+2)%3
	lo, hi := ring[0], ring[0]
	for _, p := range ring {
		for c := 0; c < 3; c++ {
			if p[c] < lo[c] {
				lo[c] = p[c]
			}
			if p[c] > hi[c] {
				hi[c] = p[c]
			}
		}
	}
	norm := func(c int, x float64) float64 {
		if hi[c] == lo[c] {
			return 0.5
		}
		return (x - lo[c]) / (hi[c] - lo[c])
	}
	base := int32(len(m.Corners))
	for j := 0; j < n; j++ {
		u, v := norm(iu, ring[j][iu]), norm(iv, ring[j][iv])
		if nrm[ax] < 0 {
			u = 1 - u
		}
		m.Corners = append(m.Corners, mesh.Corner{Pos: posBase + int32(j), Normal: nrm, UV: mesh.Vec2{u, v}})
	}
	for j := 1; j+1 < n; j++ {
		if end {
			m.Tris = append(m.Tris, mesh.Tri{base, base + int32(j), base + int32(j+1)})
		} else {
			m.Tris = append(m.Tris, mesh.Tri{base, base + int32(j+1), base + int32(j)})
		}
		m.Shell = append(m.Shell, 0)
	}
	return nil
}

// sharedCap adds the same fan as stackCap, over the corners the end ring
// already has: n-2 triangles, the same winding, and no corner of its own.
func sharedCap(m *mesh.Mesh, ring []mesh.Vec3, pole bool, at func(j int) int32, end bool) error {
	if pole {
		return errors.New("build: a cap needs an end ring that is not a point")
	}
	if ringNormal(ring) == (mesh.Vec3{}) {
		return errors.New("build: a cap ring has no area")
	}
	n := len(ring)
	for j := 1; j+1 < n; j++ {
		if end {
			m.Tris = append(m.Tris, mesh.Tri{at(0), at(j), at(j + 1)})
		} else {
			m.Tris = append(m.Tris, mesh.Tri{at(0), at(j + 1), at(j)})
		}
		m.Shell = append(m.Shell, 0)
	}
	return nil
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
