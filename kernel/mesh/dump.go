package mesh

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
)

// CanonicalBytes returns the byte stream that defines a mesh's identity for
// golden hashes: a header, counts, then positions, corners, triangles and
// shell ids in index order. Floats are written as their float64 bits, little
// endian, with negative zero written as zero.
func (m *Mesh) CanonicalBytes() []byte {
	b := make([]byte, 0, 64+len(m.Positions)*24+len(m.Corners)*44+len(m.Tris)*16)
	b = append(b, "greenware-mesh/1\n"...)
	u32 := func(v uint32) { b = binary.LittleEndian.AppendUint32(b, v) }
	f64 := func(v float64) {
		if v == 0 {
			v = 0
		}
		b = binary.LittleEndian.AppendUint64(b, math.Float64bits(v))
	}
	u32(uint32(len(m.Positions)))
	u32(uint32(len(m.Corners)))
	u32(uint32(len(m.Tris)))
	for _, p := range m.Positions {
		f64(p[0])
		f64(p[1])
		f64(p[2])
	}
	for _, c := range m.Corners {
		u32(uint32(c.Pos))
		f64(c.Normal[0])
		f64(c.Normal[1])
		f64(c.Normal[2])
		f64(c.UV[0])
		f64(c.UV[1])
	}
	for i, t := range m.Tris {
		u32(uint32(t[0]))
		u32(uint32(t[1]))
		u32(uint32(t[2]))
		u32(uint32(m.Shell[i]))
	}
	return b
}

// SHA256 returns the SHA-256 of CanonicalBytes.
func (m *Mesh) SHA256() [32]byte { return sha256.Sum256(m.CanonicalBytes()) }
