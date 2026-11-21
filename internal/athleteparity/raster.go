package athleteparity

import "math"

// Mask is a silhouette: which pixels a figure covers.
type Mask struct {
	W, H  int
	Scale float64 // pixels per yard
	Pad   float64 // border in pixels, as the game's picture has
	Cover []bool
}

// renderPad is the border of the game's grey picture, in pixels.
const renderPad = 12.

// Render draws the posed triangles orthographically, front view looking along
// +z (the player faces -z) or side view looking along -x, at scale pixels per
// yard. It is the coverage of the game's greyRender (cmd/bakeathletes/
// silhouette.go): the same picture size (the width is the figure's extent
// across the view, the height is the top of the figure, both plus the border
// twice), the same pixel-centre test and the same bounding loop. The game's
// depth test and grey shading choose a pixel's colour, not whether it is
// covered, so they are left out.
func Render(tri [][3]int, p [][3]float64, scale float64, side bool) *Mask {
	proj := func(v [3]float64) (float64, float64) {
		if side {
			return v[2], v[1]
		}
		return v[0], v[1]
	}
	minU, maxU, maxV := math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, v := range p {
		u, vv := proj(v)
		minU, maxU, maxV = math.Min(minU, u), math.Max(maxU, u), math.Max(maxV, vv)
	}
	pad := renderPad
	W := int((maxU-minU)*scale + 2*pad)
	H := int(maxV*scale + 2*pad)
	mk := &Mask{W: W, H: H, Scale: scale, Pad: pad, Cover: make([]bool, W*H)}
	for _, t := range tri {
		var xs, ys [3]float64
		for k, vi := range t {
			u, vv := proj(p[vi])
			xs[k], ys[k] = (u-minU)*scale+pad, float64(H)-pad-vv*scale
		}
		area := (xs[1]-xs[0])*(ys[2]-ys[0]) - (xs[2]-xs[0])*(ys[1]-ys[0])
		if area == 0 {
			continue
		}
		x0, x1 := int(math.Floor(min3(xs))), int(math.Ceil(max3(xs)))
		y0, y1 := int(math.Floor(min3(ys))), int(math.Ceil(max3(ys)))
		for y := max(y0, 0); y <= min(y1, H-1); y++ {
			for x := max(x0, 0); x <= min(x1, W-1); x++ {
				px, py := float64(x)+.5, float64(y)+.5
				w0 := ((xs[1]-px)*(ys[2]-py) - (xs[2]-px)*(ys[1]-py)) / area
				w1 := ((xs[2]-px)*(ys[0]-py) - (xs[0]-px)*(ys[2]-py)) / area
				w2 := 1 - w0 - w1
				if w0 < 0 || w1 < 0 || w2 < 0 {
					continue
				}
				mk.Cover[y*W+x] = true
			}
		}
	}
	return mk
}

func min3(v [3]float64) float64 { return math.Min(v[0], math.Min(v[1], v[2])) }
func max3(v [3]float64) float64 { return math.Max(v[0], math.Max(v[1], v[2])) }

// RowExtent is the distance in pixels from the first to the last covered
// pixel of picture row y, counting both ends; 0 for an empty row.
func (m *Mask) RowExtent(y int) int {
	if y < 0 || y >= m.H {
		return 0
	}
	first, last := -1, -1
	for x := 0; x < m.W; x++ {
		if m.Cover[y*m.W+x] {
			if first < 0 {
				first = x
			}
			last = x
		}
	}
	if first < 0 {
		return 0
	}
	return last - first + 1
}

// BandExtent is the mean of RowExtent over the picture rows whose centres lie
// between heights lo and hi (yards above the figure's feet at y = 0). A band
// thinner than one row uses the row at its centre.
func (m *Mask) BandExtent(lo, hi float64) float64 {
	row := func(y float64) float64 { return float64(m.H) - m.Pad - y*m.Scale } // picture row coordinate of height y
	top, bot := row(hi), row(lo)
	var sum float64
	n := 0
	for y := int(math.Ceil(top - .5)); float64(y)+.5 <= bot; y++ {
		sum += float64(m.RowExtent(y))
		n++
	}
	if n == 0 {
		return float64(m.RowExtent(int(math.Floor(row((lo + hi) / 2)))))
	}
	return sum / float64(n)
}
