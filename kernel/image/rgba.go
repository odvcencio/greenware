// Package image is the CPU image kernel of Greenware: stateless texel noise,
// atlas layout and painting, the palette-to-colour fallback, and a PNG encoder
// whose bytes depend on nothing but the pixels.
//
// Rules that every byte of output follows:
//
//   - Pixels are RGBA, 8 bits per channel, row-major, first row at the top.
//   - A painter computes in binary64. Every float multiply that feeds an add
//     or a subtract is wrapped in float64(...), so no architecture fuses it
//     (the package test TestNoUnwrappedMultiply scans for the rest).
//   - A float channel becomes a byte by ToByte: clip to [0,1], multiply by
//     255, round to the nearest integer with ties to even.
//   - The PNG encoder uses its own deflate (fixed Huffman codes and a
//     bounded-chain LZ77, or stored blocks when those would be larger). It
//     does not call compress/flate or compress/zlib, whose output may change
//     between Go releases.
package image

import "math"

// RGBA8 is an image of 8-bit straight RGBA pixels.
type RGBA8 struct {
	W, H int
	// Pix holds 4*W*H bytes: R, G, B, A of each pixel, left to right, top to
	// bottom.
	Pix []byte
}

// New returns a transparent black image. It panics on a negative size, which
// is a programming error: callers check dimensions against their class first.
func New(w, h int) *RGBA8 {
	if w < 0 || h < 0 {
		panic("image: negative size")
	}
	return &RGBA8{W: w, H: h, Pix: make([]byte, 4*w*h)}
}

// Set writes the pixel at (x, y).
func (m *RGBA8) Set(x, y int, p [4]byte) {
	i := 4 * (y*m.W + x)
	copy(m.Pix[i:i+4], p[:])
}

// At returns the pixel at (x, y).
func (m *RGBA8) At(x, y int) [4]byte {
	i := 4 * (y*m.W + x)
	return [4]byte{m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3]}
}

// DecodedBytes is the size of the image once decoded: 4 bytes per pixel.
func (m *RGBA8) DecodedBytes() int { return 4 * m.W * m.H }

// ToByte converts a float channel to a byte: clip to [0,1], then round 255c
// to the nearest integer, ties to even. NaN becomes 0.
func ToByte(c float64) uint8 {
	if !(c > 0) {
		return 0
	}
	if c >= 1 {
		return 255
	}
	return uint8(math.RoundToEven(float64(255 * c)))
}
