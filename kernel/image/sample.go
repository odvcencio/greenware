package image

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"

	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/units"
)

// The edition 1 random sample (language reference 4.9). One sample hashes
// exactly 64 bytes with SHA-256: eight consecutive unsigned little-endian
// 64-bit words
//
//	(domain, revision, function, seed, cell, x, y, lane)
//
// with no string prefix, length word or terminator. Signed integers are
// encoded modulo 2^64. The first 13 hexadecimal digits of the digest, in
// conventional display order, make a 52-bit integer h, and the uniform
// deviate is U = (h + 0.5) / 2^52.
const (
	// DomainPainter is the binary domain of painter samples.
	DomainPainter = 0
	// Revision is the sampler revision of edition 1.
	Revision = 0
	// FuncNoise and FuncGrain are the `function` words.
	FuncNoise = 0
	FuncGrain = 1
)

// Preimage returns the 64 bytes that one sample hashes.
func Preimage(function uint64, seed, cell, x, y int64, lane uint64) [64]byte {
	var b [64]byte
	words := [8]uint64{DomainPainter, Revision, function, uint64(seed), uint64(cell), uint64(x), uint64(y), lane}
	for i, w := range words {
		binary.LittleEndian.PutUint64(b[8*i:], w)
	}
	return b
}

// Digest hashes a preimage.
func Digest(pre [64]byte) [32]byte { return sha256.Sum256(pre[:]) }

// H52 is the 52-bit integer of a digest: its first 13 hexadecimal digits.
func H52(d [32]byte) uint64 {
	v := uint64(d[0])<<48 | uint64(d[1])<<40 | uint64(d[2])<<32 | uint64(d[3])<<24 | uint64(d[4])<<16 | uint64(d[5])<<8 | uint64(d[6])
	return v >> 4
}

// Uniform returns U = (h + 0.5) / 2^52 for a digest. Both h and the sum are
// exact in binary64, so U is exact.
func Uniform(d [32]byte) float64 {
	return (float64(H52(d)) + 0.5) / 4503599627370496 // 2^52
}

// Sample returns the uniform deviate of one lane at one lattice point or texel.
func Sample(function uint64, seed, cell, x, y int64, lane uint64) float64 {
	return Uniform(Digest(Preimage(function, seed, cell, x, y, lane)))
}

// Grain returns sigma * z for the unit normal deviate z of a texel:
// z = sqrt(-2 log a) cos(2 pi b), where a and b are the lane 0 and lane 1
// uniforms of function 1 at the interior texel (px, py). sigma is in 8-bit
// levels, so a painter adds Grain(...)/255 to a channel.
func Grain(seed, cell int64, px, py int, sigma float64) float64 {
	a := Sample(FuncGrain, seed, cell, int64(px), int64(py), 0)
	b := Sample(FuncGrain, seed, cell, int64(px), int64(py), 1)
	r := det.Sqrt(float64(-2 * units.Log(a)))
	return float64(sigma*r) * det.Cos(float64(units.TwoPi*b))
}

// ErrLattice reports a noise coordinate whose lattice index does not fit a
// signed 64-bit integer.
var ErrLattice = errors.New("image: noise lattice index does not fit a signed 64-bit integer")

func smooth(f float64) float64 { return float64(float64(f*f) * (3 - float64(2*f))) }

func lerp(a, b, t float64) float64 { return a + float64((b-a)*t) }

// Noise returns lattice value noise in [0,1] at (u*scale, v*scale): the lane 0
// uniforms of function 0 at the four surrounding integer lattice points,
// interpolated in x first and then in y, each with the weight f*f*(3 - 2f) of
// its fractional part f and the form a + (b-a)*t.
func Noise(seed, cell int64, u, v, scale float64) (float64, error) {
	x, y := float64(u*scale), float64(v*scale)
	fx, fy := math.Floor(x), math.Floor(y)
	const lim = 9.2e18 // below 2^63 with room for the +1 corner
	if !(math.Abs(fx) < lim) || !(math.Abs(fy) < lim) {
		return 0, ErrLattice
	}
	ix, iy := int64(fx), int64(fy)
	tx, ty := smooth(x-fx), smooth(y-fy)
	at := func(i, j int64) float64 { return Sample(FuncNoise, seed, cell, i, j, 0) }
	top := lerp(at(ix, iy), at(ix+1, iy), tx)
	bot := lerp(at(ix, iy+1), at(ix+1, iy+1), tx)
	return lerp(top, bot, ty), nil
}
