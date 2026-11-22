package pbrcolor

import "m31labs.dev/greenware/det"

// Linear returns the linear factor for an encoded 8-bit sRGB channel.
func Linear(channel uint8) float64 {
	return toLinear(float64(channel) / 255)
}

func toLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	x := (c + 0.055) / 1.055
	// x^2.4 = x^2 * x^(2/5). Use the same fixed Newton steps on every CPU.
	a := float64(x * x)
	r := 1.0
	for range 80 {
		r = (float64(4*r) + a/det.Pow(r, 4)) / 5
	}
	return float64(a * r)
}

// SRGB converts a linear factor in [0, 1] to an sRGB byte. Search in linear
// space using the half-step thresholds of the encoded byte domain.
func SRGB(linear float64) uint8 {
	lo, hi := 0, 255
	for lo < hi {
		mid := (lo + hi) / 2
		cut := toLinear((float64(mid) + 0.5) / 255)
		if linear < cut || (linear == cut && mid%2 == 0) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return uint8(lo)
}
