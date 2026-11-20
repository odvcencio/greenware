package mesh

import "m31labs.dev/greenware/det"

// snapEps is how far, in eighths of a turn, an angle may lie from a multiple
// of an eighth turn and still be taken as that multiple. It absorbs the
// rounding of `90deg`, which is 90 times a rounded degree.
const snapEps = 1e-12

// SinCos returns sin and cos of an angle in radians. An angle at a multiple
// of an eighth turn (45 degrees) gets the exact values of SinCosTurn: 0, 1 and
// -1 at the quarter turns, and the one value sqrt(1/2) for both |sin| and
// |cos| at the odd eighths, so a 45 degree turn is bit for bit symmetric. Other angles use the deterministic det.Sin and det.Cos.
func SinCos(a float64) (s, c float64) {
	const eighth = 0.7853981633974483 // pi/4 rounded to float64
	k := a / eighth
	if k > -1e9 && k < 1e9 {
		r := float64(int64(k + 0.5))
		if k < 0 {
			r = float64(int64(k - 0.5))
		}
		if d := k - r; d < snapEps && d > -snapEps {
			j := int(r) % 8
			if j < 0 {
				j += 8
			}
			if j%2 == 0 {
				return SinCosTurn(j, 8)
			}
			// An odd eighth: sin and cos have one magnitude, correctly rounded.
			m := det.Sqrt(0.5)
			switch j {
			case 1:
				return m, m
			case 3:
				return m, -m
			case 5:
				return -m, -m
			}
			return -m, m
		}
	}
	return det.Sin(a), det.Cos(a)
}

// EulerXYZ returns the rotation of the language reference (2.4 and 3.4): the
// intrinsic turns about X, then Y, then Z, angles in radians. For column
// vectors the matrix is Rx * Ry * Rz, the product of the quaternions
// qx * qy * qz. A positive turn about +X carries +Z toward -Y. Right-angle
// turns give exact zeros and ones.
func EulerXYZ(x, y, z float64) Mat3 {
	sx, cx := SinCos(x)
	sy, cy := SinCos(y)
	sz, cz := SinCos(z)
	rx := Mat3{{1, 0, 0}, {0, cx, -sx}, {0, sx, cx}}
	ry := Mat3{{cy, 0, sy}, {0, 1, 0}, {-sy, 0, cy}}
	rz := Mat3{{cz, -sz, 0}, {sz, cz, 0}, {0, 0, 1}}
	return rx.Mul(ry).Mul(rz)
}
