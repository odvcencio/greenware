package image

// A deflate encoder (RFC 1951) whose output is defined by this file alone.
//
// The stream is one block. It is a fixed-Huffman block (BTYPE 01) of LZ77
// symbols found by a bounded hash-chain search, or, when that is not smaller,
// stored blocks (BTYPE 00). The search, the tie-breaks and the choice between
// the two are fixed rules with no dependence on the machine, so the same input
// gives the same bytes on every Go release and architecture.
//
// The LZ77 rules:
//
//   - The window is 32768 bytes and a match is 3 to 258 bytes.
//   - Positions are hashed on their next 3 bytes with (a<<16 ^ b<<8 ^ c)
//     times 0x9E3779B1, keeping the top 15 bits of the 32-bit product.
//   - A search walks the chain from the newest position back, at most 48
//     links, and keeps the first longest match, so a tie goes to the nearer.
//   - Matching is lazy by one position: if the match at the next position is
//     longer and the current match is shorter than 32, the current byte is a
//     literal.
//   - Every position, inside a match as well, is added to the chains.

const (
	windowSize = 1 << 15
	minMatch   = 3
	maxMatch   = 258
	hashBits   = 15
	maxChain   = 48
	lazyLimit  = 32
)

type bitWriter struct {
	out  []byte
	acc  uint64
	nacc uint
}

// bits writes the low n bits of v, least significant first.
func (w *bitWriter) bits(v uint32, n uint) {
	w.acc |= uint64(v) << w.nacc
	w.nacc += n
	for w.nacc >= 8 {
		w.out = append(w.out, byte(w.acc))
		w.acc >>= 8
		w.nacc -= 8
	}
}

// huff writes a Huffman code of n bits, most significant bit first.
func (w *bitWriter) huff(code uint32, n uint) {
	var r uint32
	for i := uint(0); i < n; i++ {
		r = r<<1 | code>>i&1
	}
	w.bits(r, n)
}

func (w *bitWriter) flush() {
	if w.nacc > 0 {
		w.out = append(w.out, byte(w.acc))
		w.acc, w.nacc = 0, 0
	}
}

// writeSym writes a literal/length symbol with the fixed code of RFC 1951 3.2.6.
func (w *bitWriter) writeSym(s int) {
	switch {
	case s < 144:
		w.huff(uint32(0x30+s), 8)
	case s < 256:
		w.huff(uint32(0x190+s-144), 9)
	case s < 280:
		w.huff(uint32(s-256), 7)
	default:
		w.huff(uint32(0xC0+s-280), 8)
	}
}

var lengthBase = [29]int{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258}
var lengthExtra = [29]uint{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}
var distBase = [30]int{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577}
var distExtra = [30]uint{0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13}

func lengthCode(n int) int {
	for i := 28; i >= 0; i-- {
		if n >= lengthBase[i] {
			return i
		}
	}
	return 0
}

func distCode(d int) int {
	for i := 29; i >= 0; i-- {
		if d >= distBase[i] {
			return i
		}
	}
	return 0
}

func hash3(b []byte, i int) uint32 {
	v := uint32(b[i])<<16 ^ uint32(b[i+1])<<8 ^ uint32(b[i+2])
	return (v * 0x9E3779B1) >> (32 - hashBits)
}

type matcher struct {
	data []byte
	head []int32 // newest position per hash, or -1
	prev []int32 // previous position with the same hash, by position
}

func newMatcher(data []byte) *matcher {
	m := &matcher{data: data, head: make([]int32, 1<<hashBits), prev: make([]int32, len(data))}
	for i := range m.head {
		m.head[i] = -1
	}
	return m
}

// insert adds position i to its chain when 3 bytes remain.
func (m *matcher) insert(i int) {
	if i+minMatch > len(m.data) {
		return
	}
	h := hash3(m.data, i)
	m.prev[i] = m.head[h]
	m.head[h] = int32(i)
}

// find returns the longest match for position i among earlier positions, as
// length and distance, or length 0.
func (m *matcher) find(i int) (length, dist int) {
	if i+minMatch > len(m.data) {
		return 0, 0
	}
	maxLen := min(maxMatch, len(m.data)-i)
	cand := m.head[hash3(m.data, i)]
	for chain := 0; cand >= 0 && chain < maxChain; chain++ {
		c := int(cand)
		if i-c > windowSize {
			break
		}
		if m.data[c+length] == m.data[i+length] || length == 0 {
			n := 0
			for n < maxLen && m.data[c+n] == m.data[i+n] {
				n++
			}
			if n > length {
				length, dist = n, i-c
				if n == maxLen {
					break
				}
			}
		}
		cand = m.prev[c]
	}
	if length < minMatch {
		return 0, 0
	}
	return length, dist
}

// deflateFixed encodes data as one final fixed-Huffman block.
func deflateFixed(data []byte) []byte {
	w := &bitWriter{out: make([]byte, 0, len(data)/2+16)}
	w.bits(1, 1) // BFINAL
	w.bits(1, 2) // BTYPE = 01
	m := newMatcher(data)
	n := len(data)
	i := 0
	for i < n {
		l, d := m.find(i)
		inserted := false
		if l >= minMatch && l < lazyLimit && i+1 < n {
			// Lazy by one: position i joins the chains first, so the search at
			// i+1 may match it.
			m.insert(i)
			inserted = true
			if l2, _ := m.find(i + 1); l2 > l {
				w.writeSym(int(data[i]))
				i++
				continue
			}
		}
		if l < minMatch {
			w.writeSym(int(data[i]))
			if !inserted {
				m.insert(i)
			}
			i++
			continue
		}
		lc := lengthCode(l)
		w.writeSym(257 + lc)
		w.bits(uint32(l-lengthBase[lc]), lengthExtra[lc])
		dc := distCode(d)
		w.huff(uint32(dc), 5)
		w.bits(uint32(d-distBase[dc]), distExtra[dc])
		from := i
		if inserted {
			from = i + 1
		}
		for k := from; k < i+l; k++ {
			m.insert(k)
		}
		i += l
	}
	w.writeSym(256)
	w.flush()
	return w.out
}

// deflateStored encodes data as stored blocks of at most 65535 bytes.
func deflateStored(data []byte) []byte {
	out := make([]byte, 0, len(data)+5*(len(data)/65535+1))
	if len(data) == 0 {
		return append(out, 1, 0, 0, 0xff, 0xff)
	}
	for len(data) > 0 {
		n := min(len(data), 65535)
		final := byte(0)
		if n == len(data) {
			final = 1
		}
		out = append(out, final, byte(n), byte(n>>8), byte(^n), byte(^n>>8))
		out = append(out, data[:n]...)
		data = data[n:]
	}
	return out
}

// deflate returns the fixed-Huffman stream, or the stored blocks when those
// are not larger.
func deflate(data []byte) []byte {
	f := deflateFixed(data)
	if s := deflateStored(data); len(s) <= len(f) {
		return s
	}
	return f
}

// adler32 is the Adler-32 checksum of RFC 1950: two sums modulo 65521, the
// second over the running first.
func adler32(data []byte) uint32 {
	const mod = 65521
	a, b := uint32(1), uint32(0)
	for _, c := range data {
		a = (a + uint32(c)) % mod
		b = (b + a) % mod
	}
	return b<<16 | a
}

// zlibStream wraps a deflate stream: header 0x78 0x01 (32 KiB window, no
// dictionary, fastest-algorithm flag, check bits valid) and the big-endian
// Adler-32 of the uncompressed data.
func zlibStream(raw []byte) []byte {
	out := []byte{0x78, 0x01}
	out = append(out, deflate(raw)...)
	a := adler32(raw)
	return append(out, byte(a>>24), byte(a>>16), byte(a>>8), byte(a))
}
