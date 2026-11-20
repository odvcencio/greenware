package image

import (
	"sync"

	"m31labs.dev/greenware/units"
)

// Colour is a painter's colour: sRGB-encoded red, green, blue and linear
// alpha as real components. Painters do not clip until storage.
type Colour [4]float64

// FromBytes decodes a stored colour: each byte divided by 255.
func FromBytes(b [4]byte) Colour {
	return Colour{float64(b[0]) / 255, float64(b[1]) / 255, float64(b[2]) / 255, float64(b[3]) / 255}
}

// Bytes stores a colour with ToByte on every channel.
func (c Colour) Bytes() [4]byte {
	return [4]byte{ToByte(c[0]), ToByte(c[1]), ToByte(c[2]), ToByte(c[3])}
}

var (
	linOnce sync.Once
	linTab  [256]float64
)

// SRGBToLinear converts a stored sRGB byte to a linear component: c/12.92
// up to 0.04045, else ((c+0.055)/1.055)^2.4, with the power taken as
// exp(2.4 log x) by the package's own deterministic Exp and Log.
func SRGBToLinear(b uint8) float64 {
	linOnce.Do(func() {
		for i := range linTab {
			c := float64(i) / 255
			if c <= 0.04045 {
				linTab[i] = c / 12.92
				continue
			}
			x := (c + 0.055) / 1.055
			linTab[i] = units.Exp(float64(2.4 * units.Log(x)))
		}
	})
	return linTab[b]
}

// LinearToSRGB converts a linear component to stored sRGB, as a real number:
// 12.92 l up to 0.0031308, else 1.055 l^(1/2.4) - 0.055.
func LinearToSRGB(l float64) float64 {
	if l <= 0.0031308 {
		return float64(12.92 * l)
	}
	p := units.Exp(units.Log(l) / 2.4)
	return float64(1.055*p) - 0.055
}
