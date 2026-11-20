package image

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// EncodePNG writes the image as an 8-bit RGBA PNG with no metadata chunks:
// the signature, IHDR, one IDAT and IEND. The bytes depend on the pixels
// alone.
//
// Each scanline takes the filter of least cost, where the cost of a filtered
// row is the sum of |v| over its bytes read as signed (the usual heuristic);
// a tie goes to the lower filter number. The filtered scanlines are one zlib
// stream written by this package's own deflate (see deflate.go).
func EncodePNG(m *RGBA8) ([]byte, error) {
	if m == nil || m.W < 1 || m.H < 1 {
		return nil, errors.New("image: a PNG needs at least one pixel")
	}
	if len(m.Pix) != 4*m.W*m.H {
		return nil, errors.New("image: pixel data does not match the image size")
	}
	stride := 4 * m.W
	raw := make([]byte, 0, (stride+1)*m.H)
	zero := make([]byte, stride)
	var cand [5][]byte
	for i := range cand {
		cand[i] = make([]byte, stride)
	}
	for y := 0; y < m.H; y++ {
		row := m.Pix[y*stride : (y+1)*stride]
		up := zero
		if y > 0 {
			up = m.Pix[(y-1)*stride : y*stride]
		}
		best, bestCost := 0, -1
		for f := 0; f < 5; f++ {
			filterRow(f, cand[f], row, up)
			cost := 0
			for _, b := range cand[f] {
				v := int(int8(b))
				if v < 0 {
					v = -v
				}
				cost += v
			}
			if bestCost < 0 || cost < bestCost {
				best, bestCost = f, cost
			}
		}
		raw = append(raw, byte(best))
		raw = append(raw, cand[best]...)
	}
	var out []byte
	out = append(out, 0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n')
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:], uint32(m.W))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(m.H))
	ihdr[8], ihdr[9] = 8, 6 // bit depth 8, colour type RGBA; the rest is zero
	out = chunk(out, "IHDR", ihdr[:])
	out = chunk(out, "IDAT", zlibStream(raw))
	return chunk(out, "IEND", nil), nil
}

func chunk(dst []byte, kind string, data []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(data)))
	start := len(dst)
	dst = append(dst, kind...)
	dst = append(dst, data...)
	return binary.BigEndian.AppendUint32(dst, crc32.ChecksumIEEE(dst[start:]))
}

// filterRow writes the filter f of row (with the row above it, up) into dst.
// Filters 0..4 are None, Sub, Up, Average and Paeth of the PNG specification,
// with the bytes per pixel fixed at 4.
func filterRow(f int, dst, row, up []byte) {
	const bpp = 4
	for i := range row {
		var a, c byte
		if i >= bpp {
			a, c = row[i-bpp], up[i-bpp]
		}
		b := up[i]
		switch f {
		case 0:
			dst[i] = row[i]
		case 1:
			dst[i] = row[i] - a
		case 2:
			dst[i] = row[i] - b
		case 3:
			dst[i] = row[i] - byte((int(a)+int(b))/2)
		default:
			dst[i] = row[i] - paeth(a, b, c)
		}
	}
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
