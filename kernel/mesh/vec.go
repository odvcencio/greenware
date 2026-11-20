package mesh

import "m31labs.dev/det"

// Every floating-point multiply in this package is wrapped in float64(...).
// The wrapper forbids fused multiply-add on arm64, so amd64 and arm64 produce
// the same bits. TestNoUnwrappedMultiply enforces the add/subtract case.

// Vec2 is a 2D float64 vector, used for texture coordinates.
type Vec2 [2]float64

// Vec3 is a 3D float64 vector.
type Vec3 [3]float64

// Add returns a+b.
func Add(a, b Vec3) Vec3 { return Vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }

// Sub returns a-b.
func Sub(a, b Vec3) Vec3 { return Vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }

// Scale returns a*s.
func Scale(a Vec3, s float64) Vec3 {
	return Vec3{float64(a[0] * s), float64(a[1] * s), float64(a[2] * s)}
}

// Dot returns the dot product, summed left to right with each product rounded.
func Dot(a, b Vec3) float64 {
	return float64(a[0]*b[0]) + float64(a[1]*b[1]) + float64(a[2]*b[2])
}

// Cross returns a x b.
func Cross(a, b Vec3) Vec3 {
	return Vec3{
		float64(a[1]*b[2]) - float64(a[2]*b[1]),
		float64(a[2]*b[0]) - float64(a[0]*b[2]),
		float64(a[0]*b[1]) - float64(a[1]*b[0]),
	}
}

// Len returns the Euclidean length of a.
func Len(a Vec3) float64 { return det.Sqrt(Dot(a, a)) }

// Normalize returns a scaled to unit length. A zero vector comes back as zero.
func Normalize(a Vec3) Vec3 {
	l := Len(a)
	if l == 0 {
		return Vec3{}
	}
	return Vec3{a[0] / l, a[1] / l, a[2] / l}
}

// Neg returns -a.
func Neg(a Vec3) Vec3 { return Vec3{-a[0], -a[1], -a[2]} }

// F32 rounds each component to float32.
func (v Vec3) F32() [3]float32 {
	return [3]float32{det.Round32(v[0]), det.Round32(v[1]), det.Round32(v[2])}
}

// F32 rounds each component to float32.
func (v Vec2) F32() [2]float32 { return [2]float32{det.Round32(v[0]), det.Round32(v[1])} }

// Mat3 is a row-major 3x3 matrix.
type Mat3 [3][3]float64

// Identity3 is the identity matrix.
var Identity3 = Mat3{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}

// MulVec returns m*v.
func (m Mat3) MulVec(v Vec3) Vec3 {
	return Vec3{
		float64(m[0][0]*v[0]) + float64(m[0][1]*v[1]) + float64(m[0][2]*v[2]),
		float64(m[1][0]*v[0]) + float64(m[1][1]*v[1]) + float64(m[1][2]*v[2]),
		float64(m[2][0]*v[0]) + float64(m[2][1]*v[1]) + float64(m[2][2]*v[2]),
	}
}

// Mul returns m*o.
func (m Mat3) Mul(o Mat3) Mat3 {
	var r Mat3
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			r[i][j] = float64(m[i][0]*o[0][j]) + float64(m[i][1]*o[1][j]) + float64(m[i][2]*o[2][j])
		}
	}
	return r
}

// Transpose returns the transpose of m.
func (m Mat3) Transpose() Mat3 {
	var r Mat3
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			r[i][j] = m[j][i]
		}
	}
	return r
}

// Det returns the determinant of m.
func (m Mat3) Det() float64 {
	return float64(m[0][0]*(float64(m[1][1]*m[2][2])-float64(m[1][2]*m[2][1]))) -
		float64(m[0][1]*(float64(m[1][0]*m[2][2])-float64(m[1][2]*m[2][0]))) +
		float64(m[0][2]*(float64(m[1][0]*m[2][1])-float64(m[1][1]*m[2][0])))
}

// InverseTranspose returns (m^-1)^T, the matrix that carries normals. ok is
// false when m is singular.
func (m Mat3) InverseTranspose() (r Mat3, ok bool) {
	d := m.Det()
	if d == 0 {
		return Mat3{}, false
	}
	// The cofactor matrix divided by the determinant is the inverse transpose.
	r[0][0] = (float64(m[1][1]*m[2][2]) - float64(m[1][2]*m[2][1])) / d
	r[0][1] = (float64(m[1][2]*m[2][0]) - float64(m[1][0]*m[2][2])) / d
	r[0][2] = (float64(m[1][0]*m[2][1]) - float64(m[1][1]*m[2][0])) / d
	r[1][0] = (float64(m[0][2]*m[2][1]) - float64(m[0][1]*m[2][2])) / d
	r[1][1] = (float64(m[0][0]*m[2][2]) - float64(m[0][2]*m[2][0])) / d
	r[1][2] = (float64(m[0][1]*m[2][0]) - float64(m[0][0]*m[2][1])) / d
	r[2][0] = (float64(m[0][1]*m[1][2]) - float64(m[0][2]*m[1][1])) / d
	r[2][1] = (float64(m[0][2]*m[1][0]) - float64(m[0][0]*m[1][2])) / d
	r[2][2] = (float64(m[0][0]*m[1][1]) - float64(m[0][1]*m[1][0])) / d
	return r, true
}

// Rotation returns the matrix that turns vectors by angle radians about axis
// (Rodrigues' formula). The axis need not be unit length. Quarter turns about
// a coordinate axis come out exact only when built with SinCosTurn; callers
// who need that build the matrix themselves.
func Rotation(axis Vec3, radians float64) Mat3 {
	s, c := det.Sin(radians), det.Cos(radians)
	return RotationSinCos(axis, s, c)
}

// RotationSinCos is Rotation with the sine and cosine supplied by the caller.
func RotationSinCos(axis Vec3, s, c float64) Mat3 {
	a := Normalize(axis)
	x, y, z := a[0], a[1], a[2]
	t := 1 - c
	return Mat3{
		{c + float64(float64(t*x)*x), float64(float64(t*x)*y) - float64(s*z), float64(float64(t*x)*z) + float64(s*y)},
		{float64(float64(t*x)*y) + float64(s*z), c + float64(float64(t*y)*y), float64(float64(t*y)*z) - float64(s*x)},
		{float64(float64(t*x)*z) - float64(s*y), float64(float64(t*y)*z) + float64(s*x), c + float64(float64(t*z)*z)},
	}
}

// halfPi is pi/2 rounded to float64.
const halfPi = 1.5707963267948966

// SinCosTurn returns sin and cos of the angle 2*pi*j/n. Angles that are
// multiples of a quarter turn come out exact (0, 1, -1), and the four
// quadrants and the two halves of each quadrant are mirror images of one
// another bit for bit, so a ring of n points is exactly symmetric.
func SinCosTurn(j, n int) (s, c float64) {
	j %= n
	if j < 0 {
		j += n
	}
	q := (4 * j) / n
	rem := 4*j - q*n
	var bs, bc float64
	switch {
	case rem == 0:
		bs, bc = 0, 1
	case 2*rem <= n:
		phi := float64(halfPi*float64(rem)) / float64(n)
		bs, bc = det.Sin(phi), det.Cos(phi)
	default:
		psi := float64(halfPi*float64(n-rem)) / float64(n)
		bs, bc = det.Cos(psi), det.Sin(psi)
	}
	switch q {
	case 0:
		return bs, bc
	case 1:
		return bc, -bs
	case 2:
		return -bs, -bc
	default:
		return -bc, bs
	}
}
