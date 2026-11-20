package build

import (
	"errors"

	"m31labs.dev/greenware/kernel/mesh"
)

// RadialArray repeats m count times around axis through centre. The shape is
// written about its own origin. Copy i sits at distance radius from centre in
// the direction of angle 2*pi*i/count (angle zero is the axis frame's +Z) and
// is turned by that same angle about the axis, so every copy faces outward the
// same way. Copy 0 is not turned. Copies are merged in order; each keeps its
// own shells. Quarter-turn angles are exact.
func RadialArray(m *mesh.Mesh, count int, axis Axis, centre mesh.Vec3, radius float64) (*mesh.Mesh, error) {
	if count < 1 {
		return nil, errors.New("build: an array needs at least 1 copy")
	}
	f := axis.frame()
	dir0 := f.MulVec(mesh.Vec3{0, 0, 1})
	off := mesh.Scale(dir0, radius)
	copies := make([]*mesh.Mesh, count)
	for i := 0; i < count; i++ {
		s, c := mesh.SinCosTurn(i, count)
		ry := mesh.Mat3{{c, 0, s}, {0, 1, 0}, {-s, 0, c}}
		rot := f.Mul(ry).Mul(f.Transpose())
		copies[i] = m.Transform(mesh.TranslateAffine(off).Then(mesh.Affine{M: rot, T: centre}))
	}
	return mesh.Merge(copies...), nil
}
