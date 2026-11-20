package gltf

import (
	"m31labs.dev/greenware/det"
)

// decomposeMatrix splits a column-major 4x4 matrix into translation, rotation
// (x y z w) and scale. It returns false when the matrix has shear, a zero
// scale, a perspective row, or non-finite values. Every product is wrapped in
// float64(...) so the result is the same bits on every architecture.
func decomposeMatrix(m []float64) (t [3]float32, r [4]float32, s [3]float32, ok bool) {
	if len(m) != 16 {
		return t, r, s, false
	}
	for _, v := range m {
		if v != v || v > 1e300 || v < -1e300 {
			return t, r, s, false
		}
	}
	if m[3] != 0 || m[7] != 0 || m[11] != 0 || m[15] != 1 {
		return t, r, s, false
	}
	col := [3][3]float64{{m[0], m[1], m[2]}, {m[4], m[5], m[6]}, {m[8], m[9], m[10]}}
	var sc [3]float64
	for i := 0; i < 3; i++ {
		c := col[i]
		sc[i] = det.Sqrt(float64(c[0]*c[0]) + float64(c[1]*c[1]) + float64(c[2]*c[2]))
		if !(sc[i] > 1e-12) {
			return t, r, s, false
		}
	}
	// Right-handed check: a negative determinant is folded into the x scale.
	c01 := [3]float64{
		float64(col[0][1]*col[1][2]) - float64(col[0][2]*col[1][1]),
		float64(col[0][2]*col[1][0]) - float64(col[0][0]*col[1][2]),
		float64(col[0][0]*col[1][1]) - float64(col[0][1]*col[1][0]),
	}
	d := float64(c01[0]*col[2][0]) + float64(c01[1]*col[2][1]) + float64(c01[2]*col[2][2])
	if d < 0 {
		sc[0] = -sc[0]
	}
	var rot [3][3]float64 // rot[i] is column i, normalised
	for i := 0; i < 3; i++ {
		for k := 0; k < 3; k++ {
			rot[i][k] = col[i][k] / sc[i]
		}
	}
	// Columns must be orthonormal; otherwise the matrix has shear.
	for i := 0; i < 3; i++ {
		for j := i + 1; j < 3; j++ {
			dot := float64(rot[i][0]*rot[j][0]) + float64(rot[i][1]*rot[j][1]) + float64(rot[i][2]*rot[j][2])
			if dot > 1e-4 || dot < -1e-4 {
				return t, r, s, false
			}
		}
	}
	// rotation matrix R[row][col] = rot[col][row]
	r00, r10, r20 := rot[0][0], rot[0][1], rot[0][2]
	r01, r11, r21 := rot[1][0], rot[1][1], rot[1][2]
	r02, r12, r22 := rot[2][0], rot[2][1], rot[2][2]
	var x, y, z, w float64
	tr := r00 + r11 + r22
	switch {
	case tr > 0:
		q := 2 * det.Sqrt(1+tr)
		w = q / 4
		x = (r21 - r12) / q
		y = (r02 - r20) / q
		z = (r10 - r01) / q
	case r00 > r11 && r00 > r22:
		q := 2 * det.Sqrt(1+r00-r11-r22)
		w = (r21 - r12) / q
		x = q / 4
		y = (r01 + r10) / q
		z = (r02 + r20) / q
	case r11 > r22:
		q := 2 * det.Sqrt(1+r11-r00-r22)
		w = (r02 - r20) / q
		x = (r01 + r10) / q
		y = q / 4
		z = (r12 + r21) / q
	default:
		q := 2 * det.Sqrt(1+r22-r00-r11)
		w = (r10 - r01) / q
		x = (r02 + r20) / q
		y = (r12 + r21) / q
		z = q / 4
	}
	n := det.Sqrt(float64(x*x) + float64(y*y) + float64(z*z) + float64(w*w))
	if !(n > 0) {
		return t, r, s, false
	}
	x, y, z, w = x/n, y/n, z/n, w/n
	t = [3]float32{float32(m[12]), float32(m[13]), float32(m[14])}
	r = [4]float32{float32(x), float32(y), float32(z), float32(w)}
	s = [3]float32{float32(sc[0]), float32(sc[1]), float32(sc[2])}
	for _, v := range append(append(t[:], r[:]...), s[:]...) {
		if !finite32(v) {
			return t, r, s, false
		}
	}
	return t, r, s, true
}
