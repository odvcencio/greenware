package image

import (
	"math"
	"testing"
)

// An explicit layout maps a cell's unit square into its rectangle inside the
// margin, in layout units, whatever the baked image's size.
func TestExplicitLayoutMapsIntoTheRectangle(t *testing.T) {
	l := Layout{Size: 1024, Texels: 512, Rects: []Rect{{X: 0, Y: 0, W: 512, H: 512, Margin: 6}, {X: 768, Y: 832, W: 256, H: 96, Margin: 6}}}
	if l.Width() != 512 || l.Height() != 512 || l.Cells() != 2 {
		t.Fatalf("size %d x %d, %d cells", l.Width(), l.Height(), l.Cells())
	}
	u, v := l.MapUV(1, 0.5, 1)
	if math.Abs(u-(768+6+0.5*244)/1024) > 1e-12 || math.Abs(v-(832+6+84)/1024.0) > 1e-12 {
		t.Errorf("MapUV(1, .5, 1) = %v, %v", u, v)
	}
	img, err := Bake(Spec{Layout: l, Cells: []Cell{{Index: 0, Paint: func(Texel) (Colour, error) { return Colour{1, 0, 0, 1}, nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	if img.W != 512 || img.H != 512 {
		t.Fatalf("image %d x %d", img.W, img.H)
	}
	// the first cell is painted red out to its edge (the margin copies the interior)
	if px := img.Pix[4*(5*512+5):][:4]; px[0] != 255 || px[3] != 255 {
		t.Errorf("pixel (5, 5) is %v, want red", px)
	}
	// a rectangle that is not a whole multiple of the scale is refused
	bad := Layout{Size: 1024, Texels: 512, Rects: []Rect{{X: 1, Y: 0, W: 64, H: 64, Margin: 2}}}
	if _, err := Bake(Spec{Layout: bad}); err == nil {
		t.Error("an odd rectangle was baked")
	}
}
