package build

import (
	"errors"

	"m31labs.dev/greenware/kernel/mesh"
)

// EllipsoidParams describes an ellipsoid centred on Centre with half-extents
// Radii along x, y and z. Rings counts latitude rows including both ends.
type EllipsoidParams struct {
	Centre   mesh.Vec3
	Radii    mesh.Vec3
	Segments int
	Rings    int
	// Floor is the least horizontal ring radius, in the same unit as Radii.
	// The end rows of an ellipsoid shrink to nearly a point; the floor keeps
	// them a small polygon (language.md section 3.5 puts it at 0.0002 of the
	// asset's length unit, so the caller converts it to the output unit).
	Floor float64
	// NoSeam and ShareCaps are the ring stack's options of the same names:
	// no repeated column, and caps that reuse the end rings' corners.
	NoSeam, ShareCaps bool
}

// Ellipsoid builds an ellipsoid as one closed shell. Row i of R rows sits at
// latitude -pi/2 + pi*i/(R-1); its horizontal radii are Radii x and z times
// the cosine of the latitude, each floored at Floor, and its height is
// Radii y times the sine. Adjacent rows are joined by bands and each end
// polygon is capped, so n segments and R rows give 2n(R-1) + 2(n-2)
// triangles. Texture u is j/n and v the row share; caps are planar.
func Ellipsoid(p EllipsoidParams) (*mesh.Mesh, error) {
	for _, r := range p.Radii {
		if !(r > 0) {
			return nil, errors.New("build: ellipsoid radii must be positive")
		}
	}
	n, rows := p.Segments, p.Rings
	if n < 3 {
		return nil, errors.New("build: ellipsoid needs at least 3 segments")
	}
	if rows < 2 {
		return nil, errors.New("build: ellipsoid needs at least 2 rings")
	}
	if p.Floor < 0 {
		return nil, errors.New("build: ellipsoid floor must not be negative")
	}
	sn := make([]float64, n)
	cs := make([]float64, n)
	for j := range sn {
		sn[j], cs[j] = mesh.SinCosTurn(j, n)
	}
	rings := make([][]mesh.Vec3, rows)
	v := make([]float64, rows)
	for i := range rings {
		// theta runs 0..pi from the south end; sin(latitude) = -cos(theta).
		s, c := mesh.SinCosTurn(i, 2*(rows-1))
		rx := max(float64(p.Radii[0]*s), p.Floor)
		rz := max(float64(p.Radii[2]*s), p.Floor)
		y := -float64(p.Radii[1] * c)
		ring := make([]mesh.Vec3, n)
		for j := range ring {
			ring[j] = mesh.Vec3{
				p.Centre[0] + float64(rx*sn[j]),
				p.Centre[1] + y,
				p.Centre[2] + float64(rz*cs[j]),
			}
		}
		rings[i] = ring
		v[i] = float64(i) / float64(rows-1)
	}
	caps := CapsBoth
	if p.Floor == 0 {
		caps = CapsOff // both end rows are single points
	}
	return ringStack(stackParams{rings: rings, wrap: true, v: v, caps: caps, noSeam: p.NoSeam, shareCaps: p.ShareCaps})
}
