// Package build makes meshes from parametric shapes and picks the number of
// segments a level of detail needs.
//
// Every shape is built around the +Y axis at the origin, then turned onto the
// requested axis by an exact coordinate permutation and moved to its centre.
// A radial direction at angle a is (sin a, 0, cos a): angle zero is +Z and
// increasing angle goes toward +X (language.md section 3.5).
package build

import (
	"errors"

	"m31labs.dev/greenware/kernel/mesh"
)

// Axis names the axis a shape is built along.
type Axis int

// Axes. The shape's own axis is turned onto the named one by the shortest
// proper rotation taking +Y to it.
const (
	AxisY Axis = iota
	AxisX
	AxisZ
)

// frame returns the exact rotation taking the +Y construction onto the axis.
func (a Axis) frame() mesh.Mat3 {
	switch a {
	case AxisX:
		// Rotation about Z by -90 degrees: +Y goes to +X, +Z stays.
		return mesh.Mat3{{0, 1, 0}, {-1, 0, 0}, {0, 0, 1}}
	case AxisZ:
		// Rotation about X by +90 degrees: +Y goes to +Z, +X stays.
		return mesh.Mat3{{1, 0, 0}, {0, 0, -1}, {0, 1, 0}}
	}
	return mesh.Identity3
}

// place turns a +Y construction onto its axis and moves it to centre.
func place(m *mesh.Mesh, axis Axis, centre mesh.Vec3) *mesh.Mesh {
	return m.Transform(mesh.Affine{M: axis.frame(), T: centre})
}

// revRow is one ring of a surface of revolution.
type revRow struct {
	R, Y   float64 // radius and height of the ring
	NR, NY float64 // outward normal in the (radius, height) plane
	V      float64 // texture v of the ring
	Share  int     // index of an earlier row whose positions this row reuses, or -1
}

// revolved is a surface of revolution built from rows.
type revolved struct {
	m          *mesh.Mesh
	n          int
	sin, cos   []float64 // length n+1; entry n repeats entry 0 exactly
	posBase    []int32   // first position of each row (the pole's only position)
	cornerBase []int32   // first corner of each row
	pole       []bool
}

// pos returns the position index of column j of row r.
func (v *revolved) pos(r, j int) int32 {
	if v.pole[r] {
		return v.posBase[r]
	}
	return v.posBase[r] + int32(j%v.n)
}

// corner returns the corner index of column j of row r. A pole row has one
// corner per interval, so j is the interval.
func (v *revolved) corner(r, j int) int32 { return v.cornerBase[r] + int32(j) }

// revolve lays out positions and corners for the rows and joins each pair of
// consecutive rows in bands. A row with R == 0 is a pole: it has one position
// and one corner per interval, whose u is the interval's midpoint longitude.
// Other rows have n positions and n+1 corners; the last corner repeats the
// first position at u = 1 (the texture seam).
func revolve(rows []revRow, n int, bands [][2]int) (*revolved, error) {
	if n < 3 {
		return nil, errors.New("build: a ring needs at least 3 segments")
	}
	v := &revolved{m: &mesh.Mesh{}, n: n}
	v.sin = make([]float64, n+1)
	v.cos = make([]float64, n+1)
	for j := 0; j < n; j++ {
		v.sin[j], v.cos[j] = mesh.SinCosTurn(j, n)
	}
	v.sin[n], v.cos[n] = v.sin[0], v.cos[0]
	v.posBase = make([]int32, len(rows))
	v.cornerBase = make([]int32, len(rows))
	v.pole = make([]bool, len(rows))
	m := v.m
	for i, r := range rows {
		v.pole[i] = r.R == 0
		if r.Share >= 0 {
			v.posBase[i] = v.posBase[r.Share]
			v.pole[i] = v.pole[r.Share]
		} else if v.pole[i] {
			v.posBase[i] = int32(len(m.Positions))
			m.Positions = append(m.Positions, mesh.Vec3{0, r.Y, 0})
		} else {
			v.posBase[i] = int32(len(m.Positions))
			for j := 0; j < n; j++ {
				m.Positions = append(m.Positions, mesh.Vec3{
					float64(r.R * v.sin[j]), r.Y, float64(r.R * v.cos[j]),
				})
			}
		}
		v.cornerBase[i] = int32(len(m.Corners))
		if v.pole[i] {
			for j := 0; j < n; j++ {
				m.Corners = append(m.Corners, mesh.Corner{
					Pos:    v.posBase[i],
					Normal: mesh.Vec3{0, r.NY, 0},
					UV:     mesh.Vec2{(float64(j) + 0.5) / float64(n), r.V},
				})
			}
			continue
		}
		for j := 0; j <= n; j++ {
			m.Corners = append(m.Corners, mesh.Corner{
				Pos:    v.posBase[i] + int32(j%n),
				Normal: mesh.Vec3{float64(r.NR * v.sin[j]), r.NY, float64(r.NR * v.cos[j])},
				UV:     mesh.Vec2{float64(j) / float64(n), r.V},
			})
		}
	}
	for _, b := range bands {
		r0, r1 := b[0], b[1]
		if v.pole[r0] && v.pole[r1] {
			return nil, errors.New("build: two adjacent rows both lie on the axis")
		}
		for j := 0; j < n; j++ {
			switch {
			case v.pole[r0]:
				// The first row collapses: keep (a, c, d).
				v.tri(v.corner(r0, j), v.corner(r1, j+1), v.corner(r1, j))
			case v.pole[r1]:
				// The second row collapses: keep (a, b, c).
				v.tri(v.corner(r0, j), v.corner(r0, j+1), v.corner(r1, j))
			default:
				a, b2, c, d := v.corner(r0, j), v.corner(r0, j+1), v.corner(r1, j+1), v.corner(r1, j)
				v.tri(a, b2, c)
				v.tri(a, c, d)
			}
		}
	}
	return v, nil
}

func (v *revolved) tri(a, b, c int32) {
	v.m.Tris = append(v.m.Tris, mesh.Tri{a, b, c})
	v.m.Shell = append(v.m.Shell, 0)
}
