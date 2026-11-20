package build

import (
	"errors"
	"fmt"

	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/kernel/mesh"
)

// LoftParams describes a loft: rings of points joined by bands.
//
// Each ring holds the points of one row, one per angular column. A full turn
// with n segments has n columns per ring; an arc (Arc) with n intervals has
// n+1. Where the points come from (the default elliptic rule or an author's
// vertex expression) is the caller's business; see LoftRing for the default.
type LoftParams struct {
	Rings [][]mesh.Vec3
	// Arc is true for a partial turn: an open sheet whose last column is not
	// joined to the first. Caps must be off.
	Arc bool
	// V is the texture v of each ring. Nil means row index over (rows - 1).
	// A level that drops rows sets V from the original row indices so the
	// chart does not move (language.md section 6.1 step 4).
	V    []float64
	Caps Caps
}

// Loft builds a loft as one shell. A full turn with k rows and n segments has
// 2n(k-1) side triangles and n-2 per capped end; a pole row (every column the
// same point) contributes n triangles instead of 2n. Rows are kept as
// written. Caps are fans from the first column with no centre vertex; the
// first row is the bottom (CapsStart) and the last the top (CapsEnd). Texture
// u is j/n and v the row share; caps are planar. Side normals are smooth.
func Loft(p LoftParams) (*mesh.Mesh, error) {
	k := len(p.Rings)
	if k < 2 {
		return nil, errors.New("build: a loft needs at least 2 rings")
	}
	v := p.V
	if v == nil {
		v = make([]float64, k)
		for i := range v {
			v[i] = float64(i) / float64(k-1)
		}
	}
	if len(v) != k {
		return nil, fmt.Errorf("build: a loft has %d rings but %d texture rows", k, len(v))
	}
	for i, r := range p.Rings {
		for _, pt := range r {
			for _, c := range pt {
				if c != c || c > 1e300 || c < -1e300 {
					return nil, fmt.Errorf("build: loft ring %d has a point that is not finite", i)
				}
			}
		}
	}
	return ringStack(stackParams{rings: p.Rings, wrap: !p.Arc, v: v, caps: p.Caps})
}

// LoftRing returns the default ring of a loft row (language.md section 3.5):
// column j of a full turn sits at (x + rx*sin(a), y, z + rz*cos(a)) with
// a = 2*pi*j/n, using exact quarter turns. For an arc, columns 0..n run from
// lo to hi radians: a = lo + (hi-lo)*j/n.
func LoftRing(x, y, z, rx, rz float64, n int, arc bool, lo, hi float64) []mesh.Vec3 {
	if !arc {
		out := make([]mesh.Vec3, n)
		for j := range out {
			s, c := mesh.SinCosTurn(j, n)
			out[j] = mesh.Vec3{x + float64(rx*s), y, z + float64(rz*c)}
		}
		return out
	}
	out := make([]mesh.Vec3, n+1)
	for j := range out {
		a := lo + float64(float64(hi-lo)*float64(j))/float64(n)
		out[j] = mesh.Vec3{x + float64(rx*det.Sin(a)), y, z + float64(rz*det.Cos(a))}
	}
	return out
}

// LoftColumnAngle returns the angle of column j of n for a full turn
// (2*pi*j/n) or an arc (lo + (hi-lo)*j/n), as the vertex expression sees it.
func LoftColumnAngle(j, n int, arc bool, lo, hi float64) float64 {
	if !arc {
		return float64(6.283185307179586*float64(j)) / float64(n)
	}
	return lo + float64(float64(hi-lo)*float64(j))/float64(n)
}
