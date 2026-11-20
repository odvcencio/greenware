package build

import (
	"errors"
	"fmt"

	"m31labs.dev/greenware/kernel/mesh"
)

// LatheParams turns a profile of (radius, height) rows about an axis. The
// rows are kept exactly as written, in order.
type LatheParams struct {
	Centre   mesh.Vec3
	Axis     Axis
	Rows     []mesh.Vec2 // (radius, axial height); a radius of 0 is a pole
	Segments int
	Closed   bool // join the last row back to the first with one more band
}

// Lathe builds a surface of revolution. Adjacent rows are joined by bands of
// n quads; Closed adds a band from the last row to the first, not two disk
// caps. A closed profile of k positive-radius rows has 2*k*n triangles. The
// texture u is the angle fraction and v the profile-row share (i/(k-1) open,
// i/k closed, where the closing row repeats the first at v = 1, so the
// closed profile splits at its seam). Normals are the shell's smooth normals.
// The profile should wind so that its outward side is on the right when read
// in order from the axis outward (rows going up the outside).
func Lathe(p LatheParams) (*mesh.Mesh, error) {
	k := len(p.Rows)
	if k < 2 {
		return nil, errors.New("build: a lathe needs at least 2 profile rows")
	}
	rows := make([]revRow, 0, k+1)
	for i, r := range p.Rows {
		if r[0] < 0 {
			return nil, fmt.Errorf("build: lathe row %d has a negative radius", i)
		}
		if i > 0 && r == p.Rows[i-1] {
			return nil, fmt.Errorf("build: lathe rows %d and %d coincide", i-1, i)
		}
		rows = append(rows, revRow{R: r[0], Y: r[1], Share: -1})
	}
	denom := float64(k - 1)
	if p.Closed {
		if p.Rows[0][0] == 0 || p.Rows[k-1][0] == 0 {
			return nil, errors.New("build: a closed lathe profile cannot start or end on the axis")
		}
		if p.Rows[0] == p.Rows[k-1] {
			return nil, errors.New("build: a closed lathe profile must not repeat its first row")
		}
		denom = float64(k)
		rows = append(rows, revRow{R: p.Rows[0][0], Y: p.Rows[0][1], Share: 0})
	}
	for i := range rows {
		rows[i].V = float64(i) / denom
	}
	bands := make([][2]int, 0, len(rows)-1)
	for i := 0; i+1 < len(rows); i++ {
		bands = append(bands, [2]int{i, i + 1})
	}
	rv, err := revolve(rows, p.Segments, bands)
	if err != nil {
		return nil, err
	}
	rv.m.ComputeNormals()
	return place(rv.m, p.Axis, p.Centre), nil
}
