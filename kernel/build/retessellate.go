package build

import (
	"m31labs.dev/greenware/det"
)

// chordTie is the relative slack on the error bound, so a shape whose error
// equals max_error exactly (to rounding) is accepted. language.md section 6.1.
const chordTie = 1e-9

// ChordError returns the largest gap between an arc of the given angular span
// and its n straight chords, for a circle of the given radius:
//
//	radius * (1 - cos(span / (2n)))
//
// span is 2*pi for a full circle and pi for a sphere's pole-to-pole latitude.
func ChordError(radius, span float64, n int) float64 {
	return radius * (1 - det.Cos(span/float64(2*n)))
}

// Spans used by the rule.
const (
	spanCircle = 6.283185307179586 // full turn, floor 3
	spanLat    = 3.141592653589793 // pole to pole, floor 2
)

// Fewest returns the fewest intervals n, floor <= n <= source, whose chord
// error is within maxError (with the 1e-9 relative tie slack). It never
// returns more than source: a shape cannot gain detail past its authored
// count. If no n up to source qualifies, or maxError is not positive, it
// returns source. A source below floor is returned as is.
func Fewest(radius, span float64, source, floor int, maxError float64) int {
	if maxError <= 0 || source <= floor {
		return source
	}
	limit := maxError * (1 + chordTie)
	for n := floor; n < source; n++ {
		if ChordError(radius, span, n) <= limit {
			return n
		}
	}
	return source
}

// CircleSegments is Fewest for a full circle, with the floor of 3. radius is
// the placed radius, after any ancestor scale.
func CircleSegments(radius float64, source int, maxError float64) int {
	return Fewest(radius, spanCircle, source, 3, maxError)
}

// LatitudeRings is Fewest for a sphere's latitude intervals, with the floor
// of 2.
func LatitudeRings(radius float64, source int, maxError float64) int {
	return Fewest(radius, spanLat, source, 2, maxError)
}

// RetessellateCylinder returns p with the fewest segments that keep the chord
// error within maxError. The bevel's own segment count is kept.
func RetessellateCylinder(p CylinderParams, maxError float64) CylinderParams {
	p.Segments = CircleSegments(p.Radius, p.Segments, maxError)
	return p
}

// RetessellateSphere returns p with segments and rings reduced the same way.
// An unset Rings is first resolved from the source segments.
func RetessellateSphere(p SphereParams, maxError float64) SphereParams {
	if p.Rings == 0 {
		p.Rings = DefaultRings(p.Segments)
	}
	p.Segments = CircleSegments(p.Radius, p.Segments, maxError)
	p.Rings = LatitudeRings(p.Radius, p.Rings, maxError)
	return p
}

// RetessellateSweep returns p with the profile's segments reduced. The path
// rows are author rows and are kept.
func RetessellateSweep(p SweepParams, maxError float64) SweepParams {
	p.Segments = CircleSegments(p.Radius, p.Segments, maxError)
	return p
}

// RetessellateLathe returns p with the turn's segments reduced, using the
// largest profile radius. The profile rows are author rows and are kept.
func RetessellateLathe(p LatheParams, maxError float64) LatheParams {
	r := 0.0
	for _, row := range p.Rows {
		if row[0] > r {
			r = row[0]
		}
	}
	p.Segments = CircleSegments(r, p.Segments, maxError)
	return p
}

// RetessellateEllipsoid returns p with segments and latitude rows reduced.
// Segments use the larger horizontal radius; the latitude rows, which are
// Rings-1 intervals, use the largest radius. The least a level keeps is 3
// segments and 3 rows (two latitude intervals, as for a sphere), unless the
// source already has fewer.
func RetessellateEllipsoid(p EllipsoidParams, maxError float64) EllipsoidParams {
	p.Segments = CircleSegments(max(p.Radii[0], p.Radii[2]), p.Segments, maxError)
	p.Rings = LatitudeRings(max(p.Radii[0], p.Radii[1], p.Radii[2]), p.Rings-1, maxError) + 1
	return p
}

// RetessellateTube returns p with the ring's segments reduced. The path rows
// are author rows and are kept.
func RetessellateTube(p TubeParams, maxError float64) TubeParams {
	p.Segments = CircleSegments(p.Radius, p.Segments, maxError)
	return p
}

// LoftSegments returns the fewest segments (or arc intervals) of a loft whose
// chord error, for the placed radius r, stays within maxError. A full turn
// has the floor of 3; an arc of angular span span has the floor of 1.
func LoftSegments(r float64, arc bool, span float64, source int, maxError float64) int {
	if arc {
		return Fewest(r, span, source, 1, maxError)
	}
	return CircleSegments(r, source, maxError)
}
