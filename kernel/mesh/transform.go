package mesh

// Affine is the map p -> M*p + T.
type Affine struct {
	M Mat3
	T Vec3
}

// IdentityAffine leaves a mesh unchanged.
var IdentityAffine = Affine{M: Identity3}

// Then returns the map that applies a first and b second.
func (a Affine) Then(b Affine) Affine {
	return Affine{M: b.M.Mul(a.M), T: Add(b.M.MulVec(a.T), b.T)}
}

// TranslateAffine moves by t.
func TranslateAffine(t Vec3) Affine { return Affine{M: Identity3, T: t} }

// ScaleAffine scales each axis by s, which may differ per axis or be negative.
func ScaleAffine(s Vec3) Affine {
	return Affine{M: Mat3{{s[0], 0, 0}, {0, s[1], 0}, {0, 0, s[2]}}}
}

// RotateAffine turns by radians about axis through the origin.
func RotateAffine(axis Vec3, radians float64) Affine {
	return Affine{M: Rotation(axis, radians)}
}

// Plane names a coordinate plane for Mirror: the plane whose normal is the
// named axis (PlaneX is x = 0).
type Plane int

// Mirror planes.
const (
	PlaneX Plane = iota
	PlaneY
	PlaneZ
)

// MirrorAffine reflects across a coordinate plane through the origin.
func MirrorAffine(p Plane) Affine {
	s := Vec3{1, 1, 1}
	s[p] = -1
	return ScaleAffine(s)
}

// Transform returns a transformed copy. Positions use the map as written.
// Normals use the inverse transpose of M and are renormalised, so a
// nonuniform scale keeps them perpendicular to their faces. A map with a
// negative determinant (a reflection, or a scale with an odd number of
// negative factors) reverses the winding of every triangle once, so the
// faces still point outward. Texture coordinates stay on their corners.
func (m *Mesh) Transform(a Affine) *Mesh {
	out := m.Clone()
	for i, p := range out.Positions {
		out.Positions[i] = Add(a.M.MulVec(p), a.T)
	}
	if a.M != Identity3 {
		nm, ok := a.M.InverseTranspose()
		if !ok {
			nm = a.M
		}
		for i := range out.Corners {
			out.Corners[i].Normal = Normalize(nm.MulVec(out.Corners[i].Normal))
		}
	}
	if a.M.Det() < 0 {
		for i, t := range out.Tris {
			out.Tris[i] = Tri{t[0], t[2], t[1]}
		}
	}
	return out
}

// Translate returns a copy moved by t.
func (m *Mesh) Translate(t Vec3) *Mesh { return m.Transform(TranslateAffine(t)) }

// Scaled returns a copy scaled per axis about the origin.
func (m *Mesh) Scaled(s Vec3) *Mesh { return m.Transform(ScaleAffine(s)) }

// Rotated returns a copy turned by radians about axis through the origin.
func (m *Mesh) Rotated(axis Vec3, radians float64) *Mesh {
	return m.Transform(RotateAffine(axis, radians))
}

// Mirror returns the reflection of m across a coordinate plane through the
// origin, with the winding flipped. To keep the original as well, Merge the
// two.
func (m *Mesh) Mirror(p Plane) *Mesh { return m.Transform(MirrorAffine(p)) }
