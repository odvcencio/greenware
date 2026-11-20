package image

import (
	"errors"
	"fmt"
	"math"
)

// Layout is the grid of an atlas: Cols x Rows square cells of CellSize
// texels, each reserving Inset of its side on every edge (language reference
// 4.8). Cells are numbered left to right, then top to bottom, from zero.
type Layout struct {
	Cols, Rows int
	CellSize   int
	// Inset is a ratio in [0, 0.5).
	Inset float64
}

// Width and Height are the image size in texels.
func (l Layout) Width() int  { return l.Cols * l.CellSize }
func (l Layout) Height() int { return l.Rows * l.CellSize }

// Cells is the number of cells.
func (l Layout) Cells() int { return l.Cols * l.Rows }

// Interior returns the least and greatest texel index of a cell whose centre
// lies inside the inset rectangle: lo = ceil(inset*size - 0.5) and
// hi = floor((1-inset)*size - 0.5). It fails when no texel centre is inside.
func (l Layout) Interior() (lo, hi int, err error) {
	if l.CellSize < 1 || l.Cols < 1 || l.Rows < 1 {
		return 0, 0, errors.New("image: an atlas needs a positive grid and cell size")
	}
	if !(l.Inset >= 0 && l.Inset < 0.5) {
		return 0, 0, fmt.Errorf("image: the inset %g is outside [0, 0.5)", l.Inset)
	}
	s := float64(l.CellSize)
	lo = int(math.Ceil(float64(l.Inset*s) - 0.5))
	hi = int(math.Floor(float64((1-l.Inset)*s) - 0.5))
	if lo < 0 {
		lo = 0
	}
	if hi > l.CellSize-1 {
		hi = l.CellSize - 1
	}
	if lo > hi {
		return 0, 0, fmt.Errorf("image: a %d texel cell with inset %g has no texel centre inside the inset", l.CellSize, l.Inset)
	}
	return lo, hi, nil
}

// TexelUV is the local coordinate of texel p along one axis of a cell:
// ((p + 0.5)/size - inset)/(1 - 2 inset), clamped to [0,1].
func (l Layout) TexelUV(p int) float64 {
	t := (float64(p) + 0.5) / float64(l.CellSize)
	u := (t - l.Inset) / (1 - float64(2*l.Inset))
	switch {
	case u < 0:
		return 0
	case u > 1:
		return 1
	}
	return u
}

// MapUV maps a local coordinate (u, v) of cell index to image coordinates:
//
//	U = (col + d + u (1 - 2d)) / cols
//	V = (row + d + v (1 - 2d)) / rows
//
// with d the inset. (0, 0) is the top left of the image; there is no flip.
func (l Layout) MapUV(index int, u, v float64) (U, V float64) {
	col, row := index%l.Cols, index/l.Cols
	s := 1 - float64(2*l.Inset)
	U = (float64(col) + l.Inset + float64(u*s)) / float64(l.Cols)
	V = (float64(row) + l.Inset + float64(v*s)) / float64(l.Rows)
	return U, V
}

// Rect returns the inset rectangle of a cell in image coordinates.
func (l Layout) Rect(index int) (u0, v0, u1, v1 float64) {
	u0, v0 = l.MapUV(index, 0, 0)
	u1, v1 = l.MapUV(index, 1, 1)
	return
}

// Mode is how an atlas stores its pixels.
type Mode int

const (
	// ModeColour stores the painter's RGBA as sRGB colour.
	ModeColour Mode = iota
	// ModePalette stores linear shading in red (the painter's red channel),
	// the slot byte in green, zero in blue and 255 in alpha.
	ModePalette
)

// Texel is what a painter is asked about: the physical cell, the integer
// texel of the interior (px, py from zero) and the local coordinates at its
// centre.
type Texel struct {
	Seed   int64
	Cell   int
	PX, PY int
	U, V   float64
}

// Painter computes the colour of one texel.
type Painter func(Texel) (Colour, error)

// Cell is one painted cell: its physical index, its palette slot (palette
// mode) and its painter.
type Cell struct {
	Index int
	Slot  int
	Paint Painter
}

// Spec describes an atlas to bake.
type Spec struct {
	Layout Layout
	Seed   int64
	Mode   Mode
	Cells  []Cell
	// Fill paints the cells no Cell names, with Slot FillSlot. Nil leaves
	// them transparent black (colour mode) or shade 0 and slot 0 (palette).
	Fill     Painter
	FillSlot int
}

// Bake paints the atlas. A texel whose centre lies outside the inset
// rectangle copies the nearest interior texel (a componentwise clamp of the
// indices), so the gutter holds no new grain or noise. Painting stops at the
// first painter error.
func Bake(s Spec) (*RGBA8, error) {
	lo, hi, err := s.Layout.Interior()
	if err != nil {
		return nil, err
	}
	l := s.Layout
	byIndex := make(map[int]Cell, len(s.Cells)) // lookup only
	for _, c := range s.Cells {
		if c.Index < 0 || c.Index >= l.Cells() {
			return nil, fmt.Errorf("image: cell index %d is outside the %d cells of the grid", c.Index, l.Cells())
		}
		if _, dup := byIndex[c.Index]; dup {
			return nil, fmt.Errorf("image: two cells use index %d", c.Index)
		}
		byIndex[c.Index] = c
	}
	img := New(l.Width(), l.Height())
	n := hi - lo + 1
	interior := make([][4]byte, n*n)
	for idx := 0; idx < l.Cells(); idx++ {
		p, slot := s.Fill, s.FillSlot
		if c, ok := byIndex[idx]; ok {
			p, slot = c.Paint, c.Slot
		}
		if slot < 0 || slot > 255 {
			return nil, fmt.Errorf("image: palette slot %d is outside 0..255", slot)
		}
		for py := lo; py <= hi; py++ {
			for px := lo; px <= hi; px++ {
				var col Colour
				if p != nil {
					var perr error
					col, perr = p(Texel{Seed: s.Seed, Cell: idx, PX: px, PY: py, U: l.TexelUV(px), V: l.TexelUV(py)})
					if perr != nil {
						return nil, fmt.Errorf("cell %d texel (%d, %d): %w", idx, px, py, perr)
					}
				}
				var out [4]byte
				switch {
				case s.Mode == ModePalette:
					out = [4]byte{ToByte(col[0]), byte(slot), 0, 255}
					if p == nil {
						out = [4]byte{0, byte(slot), 0, 255}
					}
				case p == nil:
					out = [4]byte{}
				default:
					out = col.Bytes()
				}
				interior[(py-lo)*n+(px-lo)] = out
			}
		}
		ox, oy := (idx%l.Cols)*l.CellSize, (idx/l.Cols)*l.CellSize
		for y := 0; y < l.CellSize; y++ {
			cy := min(max(y, lo), hi)
			for x := 0; x < l.CellSize; x++ {
				cx := min(max(x, lo), hi)
				img.Set(ox+x, oy+y, interior[(cy-lo)*n+(cx-lo)])
			}
		}
	}
	return img, nil
}

// PaintPalette makes the colour image a stock viewer shows for a palette
// mask: each texel takes its slot's default colour, converted to linear,
// multiplied by the decoded red shading, converted back to sRGB and stored;
// alpha is the slot colour's alpha. colours[slot] is nil for a slot with no
// default colour, which is an error if a texel uses it. The error names the
// first such slot.
func PaintPalette(mask *RGBA8, colours *[256]*[4]byte) (*RGBA8, error) {
	out := New(mask.W, mask.H)
	for i := 0; i < len(mask.Pix); i += 4 {
		slot := mask.Pix[i+1]
		c := colours[slot]
		if c == nil {
			return nil, fmt.Errorf("image: the palette mask uses slot %d, which has no default colour", slot)
		}
		shade := float64(mask.Pix[i]) / 255
		for k := 0; k < 3; k++ {
			l := float64(SRGBToLinear(c[k]) * shade)
			out.Pix[i+k] = ToByte(LinearToSRGB(l))
		}
		out.Pix[i+3] = c[3]
	}
	return out, nil
}

// UsedSlots lists, in increasing order, the slot bytes a palette mask uses.
func UsedSlots(mask *RGBA8) []int {
	var seen [256]bool
	for i := 1; i < len(mask.Pix); i += 4 {
		seen[mask.Pix[i]] = true
	}
	var out []int
	for s, ok := range seen {
		if ok {
			out = append(out, s)
		}
	}
	return out
}
