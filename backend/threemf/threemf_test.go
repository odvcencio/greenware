package threemf

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"hash/crc32"
	"io"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// cube returns an axis-aligned box from lo to hi with outward winding.
func cube(id int, name string, lo, hi Vec3) Object {
	v := []Vec3{
		{lo[0], lo[1], lo[2]}, {hi[0], lo[1], lo[2]}, {hi[0], hi[1], lo[2]}, {lo[0], hi[1], lo[2]},
		{lo[0], lo[1], hi[2]}, {hi[0], lo[1], hi[2]}, {hi[0], hi[1], hi[2]}, {lo[0], hi[1], hi[2]},
	}
	t := []Tri{
		{0, 2, 1}, {0, 3, 2}, // bottom, facing -z
		{4, 5, 6}, {4, 6, 7}, // top
		{0, 1, 5}, {0, 5, 4}, // front, facing -y
		{1, 2, 6}, {1, 6, 5}, // right
		{2, 3, 7}, {2, 7, 6}, // back
		{3, 0, 4}, {3, 4, 7}, // left
	}
	return Object{ID: id, Name: name, Vertices: v, Triangles: t}
}

func codes(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Code)
	}
	return out
}

func TestFormatMM(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0.000000"},
		{-0.0, "0.000000"},
		{1, "1.000000"},
		{12240, "12240.000000"},
		{-3.5, "-3.500000"},
		{1e-9, "0.000000"},
		{-1e-9, "0.000000"},     // negative zero after rounding
		{0.0078125, "0.007812"}, // exact tie, half to even: 2 is even
		{0.0234375, "0.023438"}, // exact tie, half to even: 8 is even
		{0.1, "0.100000"},
		{1234.5678915, "1234.567892"}, // binary value is above the tie
	}
	for _, c := range cases {
		if got := FormatMM(c.in); got != c.want {
			t.Errorf("FormatMM(%v) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	m := NewModel("Test <&> \"title\"", []Object{
		cube(1, "a \"cube\" & <one>", Vec3{0, 0, 0}, Vec3{10, 20, 30}),
		cube(2, "second", Vec3{-5.25, 1.5, 0}, Vec3{5, 2.5, 1000}),
	})
	b, err := Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, m) {
		t.Errorf("round trip changed the model:\n got %+v\nwant %+v", got, m)
	}
	if fs := Validate(got); len(fs) != 0 {
		t.Errorf("a pair of cubes has findings: %v", fs)
	}
	if v := Volume(&got.Objects[0]); v != 6000 {
		t.Errorf("volume of the 10x20x30 box = %v, want 6000", v)
	}
	lo, hi, ok := Bounds(&got.Objects[1])
	if !ok || lo != (Vec3{-5.25, 1.5, 0}) || hi != (Vec3{5, 2.5, 1000}) {
		t.Errorf("bounds %v %v", lo, hi)
	}
}

func TestEncodeRefusals(t *testing.T) {
	c := cube(1, "c", Vec3{}, Vec3{1, 1, 1})
	if _, err := Encode(&Model{Unit: "meter", Objects: []Object{c}}); err == nil {
		t.Error("a model in metres was written")
	}
	d := c
	d.Vertices = append([]Vec3(nil), c.Vertices...)
	d.Vertices[0][0] = nan()
	if _, err := Encode(NewModel("", []Object{d})); err != ErrNotFinite {
		t.Errorf("NaN coordinate: err = %v", err)
	}
	if _, err := Encode(NewModel("", []Object{cube(2, "b", Vec3{}, Vec3{1, 1, 1}), cube(1, "a", Vec3{}, Vec3{1, 1, 1})})); err == nil {
		t.Error("object ids out of order were written")
	}
}

func nan() float64 { z := 0.0; return z / z }

func TestOpenMeshIsFound(t *testing.T) {
	c := cube(1, "box", Vec3{}, Vec3{1, 1, 1})
	c.Triangles = c.Triangles[:10] // drop the left face
	fs := Validate(NewModel("", []Object{c}))
	if got := codes(fs); !reflect.DeepEqual(got, []string{CodeOpenEdge}) {
		t.Fatalf("findings %v", fs)
	}
	if fs[0].Count != 4 {
		t.Errorf("open edges = %d, want 4", fs[0].Count)
	}
}

func TestInvertedWindingIsCaughtByVolume(t *testing.T) {
	c := cube(1, "inside out", Vec3{}, Vec3{2, 2, 2})
	for i, tr := range c.Triangles {
		c.Triangles[i] = Tri{tr[0], tr[2], tr[1]}
	}
	fs := Validate(NewModel("", []Object{c}))
	if got := codes(fs); !reflect.DeepEqual(got, []string{CodeVolume}) {
		t.Fatalf("findings %v", fs)
	}
	if v := Volume(&c); v != -8 {
		t.Errorf("volume = %v, want -8", v)
	}
}

func TestOneFlippedTriangleIsInconsistentWinding(t *testing.T) {
	c := cube(1, "one flipped", Vec3{}, Vec3{1, 1, 1})
	c.Triangles[0] = Tri{c.Triangles[0][0], c.Triangles[0][2], c.Triangles[0][1]}
	fs := Validate(NewModel("", []Object{c}))
	found := false
	for _, f := range fs {
		if f.Code == CodeWinding {
			found = true
		}
	}
	if !found {
		t.Errorf("findings %v", fs)
	}
}

func TestNonManifoldEdge(t *testing.T) {
	// Two cubes that share one edge by position, merged into one object: the
	// shared edge is used four times.
	a := cube(1, "a", Vec3{0, 0, 0}, Vec3{1, 1, 1})
	b := cube(1, "b", Vec3{1, 1, 0}, Vec3{2, 2, 1})
	base := len(a.Vertices)
	a.Vertices = append(a.Vertices, b.Vertices...)
	for _, tr := range b.Triangles {
		a.Triangles = append(a.Triangles, Tri{tr[0] + base, tr[1] + base, tr[2] + base})
	}
	fs := Validate(NewModel("", []Object{a}))
	found := false
	for _, f := range fs {
		if f.Code == CodeNonManifold {
			found = true
		}
	}
	if !found {
		t.Errorf("findings %v", fs)
	}
}

func TestDegenerateAndRange(t *testing.T) {
	c := cube(1, "c", Vec3{}, Vec3{1, 1, 1})
	c.Triangles = append(c.Triangles, Tri{0, 0, 1})
	if got := codes(Validate(NewModel("", []Object{c}))); !reflect.DeepEqual(got, []string{CodeDegenerate}) {
		t.Errorf("degenerate: %v", got)
	}
	d := cube(1, "d", Vec3{}, Vec3{1, 1, 1})
	d.Triangles = append(d.Triangles, Tri{0, 1, 99})
	if got := codes(Validate(NewModel("", []Object{d}))); !reflect.DeepEqual(got, []string{CodeIndexRange}) {
		t.Errorf("range: %v", got)
	}
	// A collinear triangle has no repeated vertex and still no area.
	e := cube(1, "e", Vec3{}, Vec3{1, 1, 1})
	e.Vertices = append(e.Vertices, Vec3{0.5, 0, 0})
	e.Triangles = append(e.Triangles, Tri{0, 8, 1})
	got := codes(Validate(NewModel("", []Object{e})))
	if len(got) == 0 || got[0] != CodeDegenerate {
		t.Errorf("collinear: %v", got)
	}
}

func TestModelFindings(t *testing.T) {
	m := NewModel("", []Object{cube(1, "c", Vec3{}, Vec3{1, 1, 1})})
	m.Unit = "inch"
	m.Items = append(m.Items, Item{ObjectID: 7})
	got := codes(Validate(m))
	if !reflect.DeepEqual(got, []string{CodeUnit, CodeBuildItem}) {
		t.Errorf("findings %v", got)
	}
	if got := codes(Validate(&Model{Unit: Millimeter, Objects: []Object{{ID: 1, Name: "empty"}}, Items: []Item{{1}}})); !reflect.DeepEqual(got, []string{CodeEmpty}) {
		t.Errorf("empty: %v", got)
	}
}

// pack builds a model the determinism tests encode.
func pack() []byte {
	m := NewModel("determinism", []Object{
		cube(1, "a", Vec3{0, 0, 0}, Vec3{10.123456789, 20, 30}),
		cube(2, "b", Vec3{-1, -2, -3}, Vec3{1.0000005, 2, 3}),
	})
	b, err := Encode(m)
	if err != nil {
		panic(err)
	}
	return b
}

func TestByteDeterminism(t *testing.T) {
	want := sha256.Sum256(pack())
	prev := runtime.GOMAXPROCS(1)
	one := sha256.Sum256(pack())
	runtime.GOMAXPROCS(8)
	eight := sha256.Sum256(pack())
	runtime.GOMAXPROCS(prev)
	if one != want || eight != want {
		t.Error("the bytes depend on GOMAXPROCS")
	}
	// A fixed zip header: version 20, flags 0, method 0, 1980-01-01 00:00.
	b := pack()
	if !bytes.HasPrefix(b, []byte("PK\x03\x04\x14\x00\x00\x00\x00\x00\x00\x00\x21\x00")) {
		t.Errorf("unexpected local header % x", b[:14])
	}
}

// TestZipWithStandardReader reads the package with archive/zip, which this
// package's own reader and writer do not share any code with.
func TestZipWithStandardReader(t *testing.T) {
	b := pack()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		if f.Method != zip.Store {
			t.Errorf("%s: method %d, want stored", f.Name, f.Method)
		}
		if !f.Modified.IsZero() && (f.Modified.Year() != 1980 || f.Modified.Month() != 1 || f.Modified.Day() != 1 || f.Modified.Hour() != 0) {
			t.Errorf("%s: modified %v", f.Name, f.Modified)
		}
		if len(f.Extra) != 0 {
			t.Errorf("%s: extra field of %d bytes", f.Name, len(f.Extra))
		}
		rc, err := f.Open() // archive/zip checks the CRC on read
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Errorf("%s: %v", f.Name, err)
		}
		if crc32.ChecksumIEEE(data) != f.CRC32 || uint64(len(data)) != f.UncompressedSize64 {
			t.Errorf("%s: crc or size disagrees with the directory", f.Name)
		}
	}
	want := []string{"3D/3dmodel.model", "[Content_Types].xml", "_rels/.rels"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("entries %v, want %v", names, want)
	}
}

func TestDecodeRejectsDamage(t *testing.T) {
	good := pack()
	if _, err := Decode(good); err != nil {
		t.Fatal(err)
	}
	// A flipped byte inside the model part fails the CRC.
	bad := append([]byte(nil), good...)
	i := bytes.Index(bad, []byte("<vertex"))
	bad[i+1] ^= 0x20
	if _, err := Decode(bad); err == nil || !strings.Contains(err.Error(), "CRC") {
		t.Errorf("flipped byte: err = %v", err)
	}
	if _, err := Decode(good[:len(good)-5]); err == nil {
		t.Error("truncated package decoded")
	}
	// Well-formedness: a model part that is not XML, with a correct CRC.
	broken, err := writeZip([]zipEntry{
		{Name: "[Content_Types].xml", Data: []byte(contentTypes)},
		{Name: "_rels/.rels", Data: []byte(rootRels)},
		{Name: ModelPath, Data: []byte(`<model xmlns="` + CoreNamespace + `"><resources></model>`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(broken); err == nil {
		t.Error("ill-formed XML decoded")
	}
}

func TestNoUnwrappedMultiply(t *testing.T) {
	noFusion(t, ".")
}
