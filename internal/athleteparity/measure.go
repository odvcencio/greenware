package athleteparity

import (
	"fmt"
	"math"
	"sort"
)

// Widths are one figure's measures in yards, as the game's silhouette tool
// takes them: X across the front, Z front to back (the "Deep" widths).
type Widths struct {
	Height, Helmet, Shoulder, Ribs, RibsDeep, Waist, WaistDeep, Seat, SeatDeep, Thigh, ThighDeep float64
}

// Band is a height band of the bind pose, in yards, with the shell it is cut
// from. The game's bands are fixed numbers, so they are data here.
type Band struct {
	Shell  string
	Lo, Hi float64
	// Side is 0 for both sides, 1 for the vertices with bind x >= 0 and -1
	// for those with bind x <= 0.
	Side int
	// Axis is 0 for the span across the front and 2 for front to back.
	Axis int
}

// The bands of the game's `measure` (cmd/bakeathletes/silhouette.go).
var (
	bandHelmet    = Band{"helmet", -9, 9, 0, 0}
	bandShoulder  = Band{"shoulderpad", -9, 9, 0, 0}
	bandRibs      = Band{"torso", 1.30, 1.36, 0, 0}
	bandRibsDeep  = Band{"torso", 1.30, 1.36, 0, 2}
	bandWaist     = Band{"torso", 1.13, 1.19, 0, 0}
	bandWaistDeep = Band{"torso", 1.13, 1.19, 0, 2}
	bandSeat      = Band{"hips", .92, .98, 0, 0}
	bandSeatDeep  = Band{"hips", .92, .98, 0, 2}
	bandThigh     = Band{"thigh", .76, .84, 1, 0}
	bandThighDeep = Band{"thigh", .76, .84, 1, 2}
)

// Span is the extent along the band's axis of the posed vertices of the
// band's shell whose bind height lies in [Lo, Hi] (and whose bind side
// matches), or 0 when there are none. The pose does not move a vertex out of
// its band: the band is cut on the bind position, as the game does.
func (m *Model) Span(posed [][3]float64, b Band) float64 {
	mn, mx := math.Inf(1), math.Inf(-1)
	for i, v := range posed {
		bp := m.Bind[i]
		if m.VertexShell(i) != b.Shell || bp[1] < b.Lo || bp[1] > b.Hi || (b.Side > 0 && bp[0] < 0) || (b.Side < 0 && bp[0] > 0) {
			continue
		}
		mn, mx = math.Min(mn, v[b.Axis]), math.Max(mx, v[b.Axis])
	}
	if mx < mn {
		return 0
	}
	return mx - mn
}

// Measure takes the widths of a posed figure.
func (m *Model) Measure(posed [][3]float64) Widths {
	top, bottom := math.Inf(-1), math.Inf(1)
	for _, v := range posed {
		top, bottom = math.Max(top, v[1]), math.Min(bottom, v[1])
	}
	return Widths{
		Height:    top - bottom,
		Helmet:    m.Span(posed, bandHelmet),
		Shoulder:  m.Span(posed, bandShoulder),
		Ribs:      m.Span(posed, bandRibs),
		RibsDeep:  m.Span(posed, bandRibsDeep),
		Waist:     m.Span(posed, bandWaist),
		WaistDeep: m.Span(posed, bandWaistDeep),
		Seat:      m.Span(posed, bandSeat),
		SeatDeep:  m.Span(posed, bandSeatDeep),
		Thigh:     m.Span(posed, bandThigh),
		ThighDeep: m.Span(posed, bandThighDeep),
	}
}

// MedianWidths is the game's median: each column sorted, the element at
// len/2.
func MedianWidths(all []Widths) Widths {
	med := func(f func(Widths) float64) float64 {
		v := make([]float64, len(all))
		for i, w := range all {
			v[i] = f(w)
		}
		sort.Float64s(v)
		return v[len(v)/2]
	}
	return Widths{
		Height: med(func(w Widths) float64 { return w.Height }), Helmet: med(func(w Widths) float64 { return w.Helmet }),
		Shoulder: med(func(w Widths) float64 { return w.Shoulder }), Ribs: med(func(w Widths) float64 { return w.Ribs }),
		RibsDeep: med(func(w Widths) float64 { return w.RibsDeep }), Waist: med(func(w Widths) float64 { return w.Waist }),
		WaistDeep: med(func(w Widths) float64 { return w.WaistDeep }), Seat: med(func(w Widths) float64 { return w.Seat }),
		SeatDeep: med(func(w Widths) float64 { return w.SeatDeep }), Thigh: med(func(w Widths) float64 { return w.Thigh }),
		ThighDeep: med(func(w Widths) float64 { return w.ThighDeep }),
	}
}

// SilColumns is the number of numeric columns of a silhouette row.
const SilColumns = 14

// SilColumnNames names the columns of a silhouette row, in print order: the
// height and helmet width in yards, the front widths in helmet widths, the
// side depths in helmet widths, and two ratios.
var SilColumnNames = [SilColumns]string{
	"height", "helmet", "shoulder", "ribs", "waist", "seat", "thigh", "height/H",
	"ribs-d", "waist-d", "seat-d", "thigh-d", "helmet/shoulder", "shoulder/waist",
}

// Columns is the 14 numbers of a row. unit is the helmet width the other
// columns are divided by: the figure's own helmet, or a fallback when the
// model has none. A column that divides by zero is NaN.
func (w Widths) Columns(unit float64) [SilColumns]float64 {
	d := func(a, b float64) float64 {
		if b == 0 {
			return math.NaN()
		}
		return a / b
	}
	return [SilColumns]float64{
		w.Height, w.Helmet,
		d(w.Shoulder, unit), d(w.Ribs, unit), d(w.Waist, unit), d(w.Seat, unit), d(w.Thigh, unit), d(w.Height, unit),
		d(w.RibsDeep, unit), d(w.WaistDeep, unit), d(w.SeatDeep, unit), d(w.ThighDeep, unit),
		d(unit, w.Shoulder), d(w.Shoulder, w.Waist),
	}
}

// SilHeader is the game's header line of the silhouette table.
const SilHeader = "wearer                     height helmet | shoulder ribs waist seat thigh height (front, helmet widths) | ribs waist seat thigh (side depth) | helmet/shoulder shoulder/waist\n"

// FormatRow prints a row in the game's layout. A NaN prints as "n/a" in the
// same width.
func FormatRow(name string, c [SilColumns]float64) string {
	f := func(format string, width int, x float64) string {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Sprintf("%*s", width, "n/a")
		}
		return fmt.Sprintf(format, x)
	}
	f3 := func(x float64) string { return f("%5.3f", 5, x) }
	f2 := func(x float64) string { return f("%4.2f", 4, x) }
	return fmt.Sprintf("%-26s %s %s | %s %s %s %s %s %s | %s %s %s %s | %s %s\n", name,
		f3(c[0]), f3(c[1]), f2(c[2]), f2(c[3]), f2(c[4]), f2(c[5]), f2(c[6]), f2(c[7]),
		f2(c[8]), f2(c[9]), f2(c[10]), f2(c[11]), f2(c[12]), f2(c[13]))
}
