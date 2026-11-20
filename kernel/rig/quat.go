package rig

import (
	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/kernel/mesh"
)

// Vec is the vector type of the rig kernel; it is the mesh kernel's.
type Vec = mesh.Vec3

// Quat is a rotation as x, y, z, w (the glTF order).
type Quat [4]float64

// QIdentity is the rotation that does nothing.
var QIdentity = Quat{0, 0, 0, 1}

// Mul returns the product q*o: o first, then q.
func (q Quat) Mul(o Quat) Quat {
	return Quat{
		float64(q[3]*o[0]) + float64(q[0]*o[3]) + float64(q[1]*o[2]) - float64(q[2]*o[1]),
		float64(q[3]*o[1]) - float64(q[0]*o[2]) + float64(q[1]*o[3]) + float64(q[2]*o[0]),
		float64(q[3]*o[2]) + float64(q[0]*o[1]) - float64(q[1]*o[0]) + float64(q[2]*o[3]),
		float64(q[3]*o[3]) - float64(q[0]*o[0]) - float64(q[1]*o[1]) - float64(q[2]*o[2]),
	}
}

// Conj returns the conjugate, which is the inverse of a unit quaternion.
func (q Quat) Conj() Quat { return Quat{-q[0], -q[1], -q[2], q[3]} }

// Dot returns the 4D dot product.
func (q Quat) Dot(o Quat) float64 {
	return float64(q[0]*o[0]) + float64(q[1]*o[1]) + float64(q[2]*o[2]) + float64(q[3]*o[3])
}

// Neg returns -q, the same rotation.
func (q Quat) Neg() Quat { return Quat{-q[0], -q[1], -q[2], -q[3]} }

// Normalize returns q at unit length. A zero quaternion comes back as the
// identity.
func (q Quat) Normalize() Quat {
	l := det.Sqrt(q.Dot(q))
	if l == 0 {
		return QIdentity
	}
	return Quat{q[0] / l, q[1] / l, q[2] / l, q[3] / l}
}

// Rotate turns v by the unit quaternion q.
func (q Quat) Rotate(v Vec) Vec {
	u := Vec{q[0], q[1], q[2]}
	t := mesh.Scale(mesh.Cross(u, v), 2)
	return mesh.Add(mesh.Add(v, mesh.Scale(t, q[3])), mesh.Cross(u, t))
}

// QAxis returns the rotation of radians about the unit axis.
func QAxis(axis Vec, radians float64) Quat {
	h := radians / 2
	s := det.Sin(h)
	return Quat{float64(axis[0] * s), float64(axis[1] * s), float64(axis[2] * s), det.Cos(h)}
}

// QEuler returns the rotation rot(x, y, z): the product q_x q_y q_z, so z is
// applied first. Angles are in radians.
func QEuler(x, y, z float64) Quat {
	qx := QAxis(Vec{1, 0, 0}, x)
	qy := QAxis(Vec{0, 1, 0}, y)
	qz := QAxis(Vec{0, 0, 1}, z)
	return qx.Mul(qy).Mul(qz)
}

// Mat3 returns the rotation matrix of the unit quaternion q.
func (q Quat) Mat3() mesh.Mat3 {
	x, y, z, w := q[0], q[1], q[2], q[3]
	xx, yy, zz := float64(x*x), float64(y*y), float64(z*z)
	xy, xz, yz := float64(x*y), float64(x*z), float64(y*z)
	wx, wy, wz := float64(w*x), float64(w*y), float64(w*z)
	return mesh.Mat3{
		{1 - float64(2*(yy+zz)), float64(2 * (xy - wz)), float64(2 * (xz + wy))},
		{float64(2 * (xy + wz)), 1 - float64(2*(xx+zz)), float64(2 * (yz - wx))},
		{float64(2 * (xz - wy)), float64(2 * (yz + wx)), 1 - float64(2*(xx+yy))},
	}
}

// Reflect returns the rotation M q M for the reflection M across the
// coordinate plane. A rotation is an axial vector: reflecting across the
// plane whose normal is axis a keeps the component along a and negates the
// other two.
func (q Quat) Reflect(p mesh.Plane) Quat {
	switch p {
	case mesh.PlaneX:
		return Quat{q[0], -q[1], -q[2], q[3]}
	case mesh.PlaneY:
		return Quat{-q[0], q[1], -q[2], q[3]}
	}
	return Quat{-q[0], -q[1], q[2], q[3]}
}

// Slerp interpolates from a to b on the shortest arc. Both must be unit
// length. It returns exactly a at t=0 and exactly b at t=1 (b taken with the
// sign that makes the dot product nonnegative).
func Slerp(a, b Quat, t float64) Quat {
	d := a.Dot(b)
	if d < 0 {
		b, d = b.Neg(), -d
	}
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	if d > 0.9995 {
		var r Quat
		for i := range r {
			r[i] = det.Lerp(a[i], b[i], t)
		}
		return r.Normalize()
	}
	th := det.Acos(d)
	s := det.Sin(th)
	wa := det.Sin(float64((1-t)*th)) / s
	wb := det.Sin(float64(t*th)) / s
	var r Quat
	for i := range r {
		r[i] = float64(a[i]*wa) + float64(b[i]*wb)
	}
	return r.Normalize()
}

// Rigid is a rotation followed by a translation: p -> R p + T.
type Rigid struct {
	R Quat
	T Vec
}

// RigidIdentity does nothing.
var RigidIdentity = Rigid{R: QIdentity}

// Compose returns a∘b: b first, then a.
func (a Rigid) Compose(b Rigid) Rigid {
	return Rigid{R: a.R.Mul(b.R).Normalize(), T: mesh.Add(a.T, a.R.Rotate(b.T))}
}

// Inverse returns the inverse transform.
func (a Rigid) Inverse() Rigid {
	r := a.R.Conj()
	return Rigid{R: r, T: mesh.Neg(r.Rotate(a.T))}
}

// Apply moves a point.
func (a Rigid) Apply(p Vec) Vec { return mesh.Add(a.R.Rotate(p), a.T) }

// Dir turns a direction (no translation).
func (a Rigid) Dir(v Vec) Vec { return a.R.Rotate(v) }

// Mat4 returns the column-major 4x4 matrix glTF stores.
func (a Rigid) Mat4() [16]float64 {
	r := a.R.Mat3()
	return [16]float64{
		r[0][0], r[1][0], r[2][0], 0,
		r[0][1], r[1][1], r[2][1], 0,
		r[0][2], r[1][2], r[2][2], 0,
		a.T[0], a.T[1], a.T[2], 1,
	}
}
