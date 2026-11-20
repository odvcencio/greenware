package build

import (
	"errors"

	"m31labs.dev/greenware/kernel/mesh"
)

// SphereParams describes a UV sphere. Rings is the number of latitude
// intervals from pole to pole; zero means half of Segments, at least 2.
type SphereParams struct {
	Centre   mesh.Vec3
	Radius   float64
	Segments int
	Rings    int
}

// DefaultRings returns the ring count a sphere gets when none is given.
func DefaultRings(segments int) int {
	r := segments / 2
	if r < 2 {
		r = 2
	}
	return r
}

// Sphere builds a UV sphere as one closed shell. Latitude rows run from the
// south pole (v = 0) to the north pole (v = 1). Each pole is one position
// joined by one triangle per interval, so n segments and r rings give
// n*(2*(r-2)+2) triangles.
func Sphere(p SphereParams) (*mesh.Mesh, error) {
	if !(p.Radius > 0) {
		return nil, errors.New("build: sphere radius must be positive")
	}
	n := p.Segments
	if n < 3 {
		return nil, errors.New("build: sphere needs at least 3 segments")
	}
	r := p.Rings
	if r == 0 {
		r = DefaultRings(n)
	}
	if r < 2 {
		return nil, errors.New("build: sphere needs at least 2 rings")
	}
	rows := make([]revRow, r+1)
	for i := 0; i <= r; i++ {
		// theta runs from the south pole (0) to the north pole (pi).
		s, c := mesh.SinCosTurn(i, 2*r)
		rows[i] = revRow{
			R: float64(p.Radius * s), Y: -float64(p.Radius * c),
			NR: s, NY: -c, V: float64(i) / float64(r), Share: -1,
		}
	}
	rows[0].R, rows[0].NR = 0, 0
	rows[r].R, rows[r].NR = 0, 0
	bands := make([][2]int, r)
	for i := range bands {
		bands[i] = [2]int{i, i + 1}
	}
	rv, err := revolve(rows, n, bands)
	if err != nil {
		return nil, err
	}
	return place(rv.m, AxisY, p.Centre), nil
}
