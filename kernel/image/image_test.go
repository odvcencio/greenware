package image

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"crypto/sha256"
	"encoding/hex"
	stdimage "image"
	"image/color"
	"image/png"
	"io"
	"math"
	"runtime"
	"testing"
)

// ---- the stateless sample ----------------------------------------------------

func TestNoiseZeroVector(t *testing.T) {
	pre := Preimage(FuncNoise, 0, 0, 0, 0, 0)
	if pre != [64]byte{} {
		t.Fatalf("the preimage of seed 0, cell 0, lattice (0,0), lane 0 must be 64 zero bytes, got %x", pre)
	}
	d := Digest(pre)
	const want = "f5a5fd42d16a20302798ef6ed309979b43003d2320d9f0e8ea9831a92759fb4b"
	if got := hex.EncodeToString(d[:]); got != want {
		t.Fatalf("digest %s, want %s", got, want)
	}
	if h := H52(d); h != 0xf5a5fd42d16a2 {
		t.Fatalf("h = %#x, want 0xf5a5fd42d16a2", h)
	}
	u := Uniform(d)
	if exact := float64(2*uint64(0xf5a5fd42d16a2)+1) / 9007199254740992; u != exact { // 2^53
		t.Fatalf("uniform %v, want (2h+1)/2^53 = %v", u, exact)
	}
	n, err := Noise(0, 0, 0.3, 0.9, 0)
	if err != nil || n != u {
		t.Fatalf("noise(0) = %v, %v; want the lattice origin sample %v at every texel", n, err, u)
	}
	// color = #000000 + noise(0): round(255 U) is 245, so f5 f5 f5 ff.
	col := Colour{n, n, n, 1}
	if got := col.Bytes(); got != [4]byte{0xf5, 0xf5, 0xf5, 0xff} {
		t.Fatalf("pixel %x, want f5f5f5ff", got)
	}
}

func TestGrainVectors(t *testing.T) {
	// The preimage of grain (0,0,1,55,2,3,4,lane) is eight LE64 words.
	for lane := uint64(0); lane < 2; lane++ {
		pre := Preimage(FuncGrain, 55, 2, 3, 4, lane)
		for i, w := range [8]uint64{0, 0, 1, 55, 2, 3, 4, lane} {
			got := uint64(0)
			for k := 7; k >= 0; k-- {
				got = got<<8 | uint64(pre[8*i+k])
			}
			if got != w {
				t.Fatalf("lane %d word %d = %d, want %d", lane, i, got, w)
			}
		}
	}
}

func TestGrainPinned(t *testing.T) {
	var got []string
	for lane := uint64(0); lane < 2; lane++ {
		d := Digest(Preimage(FuncGrain, 55, 2, 3, 4, lane))
		got = append(got, hex.EncodeToString(d[:]))
	}
	pinned := []string{grainLane0, grainLane1}
	for i := range pinned {
		if got[i] != pinned[i] {
			t.Errorf("lane %d digest %s, pinned %s", i, got[i], pinned[i])
		}
	}
	// The deviate of the vector texel at sigma 1.2 is pinned as binary64 bits.
	g := Grain(55, 2, 3, 4, 1.2)
	if bits := math.Float64bits(g); bits != grainBits {
		t.Errorf("grain(1.2) = %v (bits %#x), pinned bits %#x", g, bits, uint64(grainBits))
	}
	// Calls at one texel share one unit deviate.
	if a, b := Grain(55, 2, 3, 4, 2.4), Grain(55, 2, 3, 4, 1.2); math.Abs(a-2*b) > 1e-12 {
		t.Errorf("sigma 2.4 gives %v, twice sigma 1.2 is %v", a, 2*b)
	}
}

func TestNoiseInterpolates(t *testing.T) {
	// At an integer lattice point the value is that point's sample.
	got, _ := Noise(7, 3, 2, 5, 1)
	if want := Sample(FuncNoise, 7, 3, 2, 5, 0); got != want {
		t.Errorf("at a lattice point %v, want %v", got, want)
	}
	// In between it stays inside the range of the four corners.
	for i := 0; i < 50; i++ {
		u, v := float64(i)/50, float64(i*7%50)/50
		n, err := Noise(1, 1, u, v, 3.7)
		if err != nil || n < 0 || n > 1 {
			t.Fatalf("noise %v out of [0,1]: %v", n, err)
		}
	}
	if _, err := Noise(0, 0, 1, 1, 1e300); err != ErrLattice {
		t.Errorf("a lattice index beyond int64 must fail, got %v", err)
	}
	// Negative lattice indices encode modulo 2^64.
	pre := Preimage(FuncNoise, 0, 0, -1, -1, 0)
	for i := 40; i < 56; i++ {
		if pre[i] != 0xff {
			t.Fatalf("x and y of -1 must be all ones, preimage %x", pre)
		}
	}
}

// ---- atlas layout and painting ------------------------------------------------

func TestInteriorAndUV(t *testing.T) {
	l := Layout{Cols: 4, Rows: 2, CellSize: 64, Inset: 0.07}
	lo, hi, err := l.Interior()
	if err != nil || lo != 4 || hi != 59 {
		t.Fatalf("interior %d..%d %v, want 4..59", lo, hi, err)
	}
	if l.Width() != 256 || l.Height() != 128 {
		t.Fatalf("size %dx%d", l.Width(), l.Height())
	}
	// U = (col + 0.07 + 0.86 u)/4 at cell 5 (col 1, row 1).
	u0, v0, u1, v1 := l.Rect(5)
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-12 }
	if !near(u0, (1+0.07)/4) || !near(u1, (1+0.93)/4) || !near(v0, (1+0.07)/2) || !near(v1, (1+0.93)/2) {
		t.Errorf("rect %v %v %v %v", u0, v0, u1, v1)
	}
	if got := l.TexelUV(0); got != 0 {
		t.Errorf("a gutter texel clamps to 0, got %v", got)
	}
	if got := l.TexelUV(63); got != 1 {
		t.Errorf("a gutter texel clamps to 1, got %v", got)
	}
	want := ((float64(4)+0.5)/64 - 0.07) / 0.86
	if got := l.TexelUV(4); !near(got, want) {
		t.Errorf("TexelUV(4) = %v, want %v", got, want)
	}
	if _, _, err := (Layout{Cols: 1, Rows: 1, CellSize: 2, Inset: 0.49}).Interior(); err == nil {
		t.Error("a cell with no interior texel centre must be rejected")
	}
	if _, _, err := (Layout{Cols: 1, Rows: 1, CellSize: 8, Inset: 0.5}).Interior(); err == nil {
		t.Error("an inset of 0.5 must be rejected")
	}
}

func TestBakeColourGutterAndFill(t *testing.T) {
	l := Layout{Cols: 2, Rows: 1, CellSize: 8, Inset: 0.25}
	// Interior texels are 2..5. The painter writes px in red and py in green.
	paint := func(t Texel) (Colour, error) {
		return Colour{float64(t.PX) / 255, float64(t.PY) / 255, float64(t.Cell) / 255, 1}, nil
	}
	img, err := Bake(Spec{Layout: l, Cells: []Cell{{Index: 0, Paint: paint}}})
	if err != nil {
		t.Fatal(err)
	}
	if img.W != 16 || img.H != 8 {
		t.Fatalf("size %dx%d", img.W, img.H)
	}
	if got := img.At(0, 0); got != [4]byte{2, 2, 0, 255} {
		t.Errorf("the corner gutter copies the nearest interior texel (2,2): %v", got)
	}
	if got := img.At(7, 7); got != [4]byte{5, 5, 0, 255} {
		t.Errorf("the far corner gutter copies (5,5): %v", got)
	}
	if got := img.At(3, 4); got != [4]byte{3, 4, 0, 255} {
		t.Errorf("an interior texel keeps its own value: %v", got)
	}
	if got := img.At(8, 0); got != [4]byte{} {
		t.Errorf("an unused colour cell is transparent black: %v", got)
	}
}

func TestBakePalette(t *testing.T) {
	l := Layout{Cols: 2, Rows: 1, CellSize: 8, Inset: 0.25}
	grey := func(v float64) Painter { return func(Texel) (Colour, error) { return Colour{v, v, v, 1}, nil } }
	img, err := Bake(Spec{Layout: l, Mode: ModePalette, Cells: []Cell{{Index: 1, Slot: 7, Paint: grey(0.5)}}})
	if err != nil {
		t.Fatal(err)
	}
	// Cell 1: red is the shading 0.5 -> 128 (127.5 rounds to even, 128), green is the slot, blue 0, alpha 255.
	if got := img.At(12, 4); got != [4]byte{128, 7, 0, 255} {
		t.Errorf("palette texel %v", got)
	}
	// Cell 0 is unused: zero shade, slot zero.
	if got := img.At(4, 4); got != [4]byte{0, 0, 0, 255} {
		t.Errorf("unused palette cell %v", got)
	}
	if _, err := Bake(Spec{Layout: l, Cells: []Cell{{Index: 2}}}); err == nil {
		t.Error("a cell index beyond the grid must fail")
	}
	if _, err := Bake(Spec{Layout: l, Cells: []Cell{{Index: 0}, {Index: 0}}}); err == nil {
		t.Error("two cells with one index must fail")
	}
}

func TestToByteRounding(t *testing.T) {
	cases := []struct {
		in   float64
		want uint8
	}{
		{0, 0}, {1, 255}, {-3, 0}, {7, 255}, {math.NaN(), 0},
		{0.5 / 255, 0},     // 0.5 ties to even: 0
		{1.5 / 255, 2},     // 1.5 ties to even: 2
		{2.5 / 255, 2},     // 2.5 ties to even: 2
		{127.5 / 255, 128}, // 127.5 ties to even: 128
		{0.5, 128},
	}
	for _, c := range cases {
		if got := ToByte(c.in); got != c.want {
			t.Errorf("ToByte(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPaintPaletteRoundTrip(t *testing.T) {
	// At shade 1 a default colour comes back exactly, for every byte value.
	for v := 0; v < 256; v++ {
		mask := New(1, 1)
		mask.Set(0, 0, [4]byte{255, 3, 0, 255})
		var cols [256]*[4]byte
		c := [4]byte{byte(v), byte(255 - v), byte(v / 2), 200}
		cols[3] = &c
		out, err := PaintPalette(mask, &cols)
		if err != nil {
			t.Fatal(err)
		}
		if got := out.At(0, 0); got != c {
			t.Fatalf("round trip of %v gave %v", c, got)
		}
	}
	// A shade scales the linear value: half shading of white is the sRGB of 0.5 linear.
	mask := New(1, 1)
	mask.Set(0, 0, [4]byte{128, 1, 0, 255})
	var cols [256]*[4]byte
	cols[1] = &[4]byte{255, 255, 255, 255}
	out, _ := PaintPalette(mask, &cols)
	want := ToByte(LinearToSRGB(128.0 / 255))
	if got := out.At(0, 0); got[0] != want || got[3] != 255 {
		t.Errorf("half shading gave %v, want red %d", got, want)
	}
	// A used slot with no colour is an error.
	cols[1] = nil
	if _, err := PaintPalette(mask, &cols); err == nil {
		t.Error("a missing slot colour must fail")
	}
	if got := UsedSlots(mask); len(got) != 1 || got[0] != 1 {
		t.Errorf("used slots %v", got)
	}
}

// ---- PNG ---------------------------------------------------------------------

func decode(t *testing.T, b []byte) *RGBA8 {
	t.Helper()
	im, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("image/png cannot decode the bytes: %v", err)
	}
	bnd := im.Bounds()
	out := New(bnd.Dx(), bnd.Dy())
	for y := 0; y < out.H; y++ {
		for x := 0; x < out.W; x++ {
			c := color.NRGBAModel.Convert(im.At(bnd.Min.X+x, bnd.Min.Y+y)).(color.NRGBA)
			out.Set(x, y, [4]byte{c.R, c.G, c.B, c.A})
		}
	}
	return out
}

func lcg(seed uint32) func() byte {
	s := seed
	return func() byte {
		s = s*1664525 + 1013904223
		return byte(s >> 24)
	}
}

func testImages() map[string]*RGBA8 {
	out := map[string]*RGBA8{}
	flat := New(33, 17)
	for i := 0; i < len(flat.Pix); i += 4 {
		copy(flat.Pix[i:], []byte{10, 200, 30, 255})
	}
	out["flat"] = flat
	grad := New(256, 128)
	for y := 0; y < grad.H; y++ {
		for x := 0; x < grad.W; x++ {
			grad.Set(x, y, [4]byte{byte(x), byte(y * 2), byte(x + y), 255})
		}
	}
	out["gradient"] = grad
	noise := New(64, 48)
	r := lcg(1)
	for i := range noise.Pix {
		noise.Pix[i] = r()
	}
	out["noise"] = noise
	out["one"] = &RGBA8{W: 1, H: 1, Pix: []byte{1, 2, 3, 4}}
	wide := New(300, 3) // rows longer than a distance code's smallest buckets
	r = lcg(9)
	for i := range wide.Pix {
		wide.Pix[i] = r() & 0x03
	}
	out["narrow-noise"] = wide
	return out
}

func TestEncodePNGDecodesExactly(t *testing.T) {
	for name, im := range testImages() {
		b, err := EncodePNG(im)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := decode(t, b)
		if got.W != im.W || got.H != im.H || !bytes.Equal(got.Pix, im.Pix) {
			t.Errorf("%s: decoded pixels differ", name)
		}
		// Straight alpha: image/png returns NRGBA for colour type 6.
		cfg, err := png.DecodeConfig(bytes.NewReader(b))
		if err != nil || cfg.Width != im.W || cfg.Height != im.H {
			t.Errorf("%s: config %v %v", name, cfg, err)
		}
	}
}

func TestDeflateInflatesWithStdlib(t *testing.T) {
	for name, im := range testImages() {
		for kind, enc := range map[string]func([]byte) []byte{"fixed": deflateFixed, "stored": deflateStored, "chosen": deflate} {
			z := enc(im.Pix)
			got, err := io.ReadAll(flate.NewReader(bytes.NewReader(z)))
			if err != nil || !bytes.Equal(got, im.Pix) {
				t.Errorf("%s/%s: inflate: %v", name, kind, err)
			}
		}
		z := zlibStream(im.Pix)
		zr, err := zlib.NewReader(bytes.NewReader(z))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, err := io.ReadAll(zr); err != nil || !bytes.Equal(got, im.Pix) {
			t.Errorf("%s: zlib stream: %v", name, err)
		}
	}
	// Edge sizes of the stored path: empty and over one block.
	for _, n := range []int{0, 1, 65535, 65536, 131072} {
		data := make([]byte, n)
		r := lcg(uint32(n))
		for i := range data {
			data[i] = r()
		}
		got, err := io.ReadAll(flate.NewReader(bytes.NewReader(deflateStored(data))))
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("stored %d bytes: %v", n, err)
		}
	}
}

func TestIncompressibleDataIsStored(t *testing.T) {
	im := testImages()["noise"]
	if z := deflate(im.Pix); len(z) > len(im.Pix)+5*(len(im.Pix)/65535+1) {
		t.Errorf("incompressible data grew to %d bytes", len(z))
	}
}

func TestAdler32AndChunks(t *testing.T) {
	// Adler-32 of "Wikipedia" is 0x11E60398 (the RFC 1950 worked example).
	if got := adler32([]byte("Wikipedia")); got != 0x11E60398 {
		t.Errorf("adler32 = %#x", got)
	}
	if got := adler32(nil); got != 1 {
		t.Errorf("adler32 of nothing = %#x", got)
	}
	b, _ := EncodePNG(New(1, 1))
	if !bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) || !bytes.HasSuffix(b, []byte{0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}) {
		t.Errorf("signature or IEND chunk wrong: %x", b)
	}
	if _, err := EncodePNG(nil); err == nil {
		t.Error("a nil image must fail")
	}
	if _, err := EncodePNG(&RGBA8{W: 2, H: 2, Pix: make([]byte, 3)}); err == nil {
		t.Error("short pixel data must fail")
	}
}

func TestPNGCompressesFlatAreas(t *testing.T) {
	im := testImages()["flat"]
	b, _ := EncodePNG(im)
	if len(b) > 200 {
		t.Errorf("a flat image of %d bytes encoded to %d bytes", len(im.Pix), len(b))
	}
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// TestPNGBytesAreFixed pins the encoder's output. The same hashes must come
// out on every architecture and Go release, with any GOMAXPROCS.
func TestPNGBytesAreFixed(t *testing.T) {
	imgs := testImages()
	names := []string{"flat", "gradient", "noise", "one", "narrow-noise"}
	for _, name := range names {
		var first string
		for _, procs := range []int{1, 8, 3} {
			old := runtime.GOMAXPROCS(procs)
			b, err := EncodePNG(imgs[name])
			runtime.GOMAXPROCS(old)
			if err != nil {
				t.Fatal(err)
			}
			s := sum(b)
			if first == "" {
				first = s
			} else if s != first {
				t.Errorf("%s: GOMAXPROCS %d gave a different file", name, procs)
			}
		}
		if want := pngPins[name]; first != want {
			t.Errorf("%s: sha256 %s, pinned %s", name, first, want)
		}
	}
}

// A decoded stdlib image must agree when the encoder is given what it decoded.
func TestStdlibRoundTripOfOwnOutput(t *testing.T) {
	im := testImages()["gradient"]
	b, _ := EncodePNG(im)
	dec, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	nr, ok := dec.(*stdimage.NRGBA)
	if !ok {
		t.Fatalf("decoded as %T, want *image.NRGBA (colour type 6)", dec)
	}
	if !bytes.Equal(nr.Pix, im.Pix) {
		t.Error("pixels differ")
	}
}

// Structured data with matches at every distance up to the window and lengths
// up to the maximum must inflate to the input.
func TestDeflateRoundTripsStructuredData(t *testing.T) {
	for seed := uint32(1); seed <= 12; seed++ {
		r := lcg(seed)
		var data []byte
		for len(data) < 200000 {
			switch r() % 4 {
			case 0: // a run
				b, n := r(), int(r())+1
				for i := 0; i < n*2; i++ {
					data = append(data, b)
				}
			case 1: // a copy from far back, up to the window
				if len(data) > 8 {
					d := 1 + (int(r())<<8|int(r()))%min(len(data), 40000)
					n := 3 + int(r())
					for i := 0; i < n; i++ {
						data = append(data, data[len(data)-d])
					}
				}
			case 2: // noise in a small alphabet
				for i := 0; i < int(r()); i++ {
					data = append(data, r()&7)
				}
			default: // a copy from nearby
				if len(data) > 4 {
					d := 1 + int(r())%min(len(data), 300)
					n := 3 + int(r())%300
					for i := 0; i < n; i++ {
						data = append(data, data[len(data)-d])
					}
				}
			}
		}
		z := deflateFixed(data)
		got, err := io.ReadAll(flate.NewReader(bytes.NewReader(z)))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("seed %d: inflate: %v", seed, err)
		}
		if len(z) >= len(data) {
			t.Errorf("seed %d: structured data did not shrink: %d -> %d", seed, len(data), len(z))
		}
	}
}
