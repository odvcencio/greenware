package athleteparity

import (
	"fmt"
	"io"
	"sort"
)

// The silhouette gate of deep consult 1, section C: in upright, equal-height,
// equal-yaw grey renders at 96 px, the median OL/DL against the median WR is
// at least 3 px wider at the waist, 2 px thicker at each thigh in side view
// and 2 px deeper at the lower ribs. The gate was proposed with "collect
// baseline first", so this prints the pixels and the verdict.
//
// A figure's pixel width is the mean extent of its silhouette over the
// picture rows of the band the game measures (waist 1.13-1.19 yd, lower ribs
// 1.30-1.36 yd, thigh .76-.84 yd; each times the figure's body scale, because
// the band is cut on the bind and the figure is scaled afterwards). The waist
// is read in the front view and the thigh and lower ribs in the side view.
// The arms hang inside the silhouette, so a pixel width includes them.

// GateMetric is one line of the gate.
type GateMetric struct {
	Name           string
	Need           float64 // pixels
	Heavy, Light   float64 // median pixel width of the OL/DL group and the WR group
	Diff           float64
	Pass           bool
	View, BandName string
}

// GateResult is the gate at one scale convention.
type GateResult struct {
	Convention string
	PxPerYard  float64 // 0 when every figure is scaled to its own height
	Metrics    []GateMetric
	// HeightHeavy and HeightLight are the median standing heights in yards.
	HeightHeavy, HeightLight float64
}

// Figure is a posed look.
type Figure struct {
	Look   Look
	Posed  [][3]float64
	Widths Widths
}

// PoseLook poses a look in the clip at time t.
func (m *Model) PoseLook(l Look, clip string, t float64) Figure {
	p := m.Posed(l.ShapeWeights, l.JointScales, l.BodyScale, clip, t)
	return Figure{Look: l, Posed: p, Widths: m.Measure(p)}
}

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// Gate computes the gate for the heavy group (OL/DL) and the light group
// (WR) at height px: with "equal scale" every figure is drawn at the pixel
// scale that makes the median WR stand px pixels tall, so physical heights
// show in the picture; with "equal height" each figure is drawn px tall.
func (m *Model) Gate(heavy, light []Figure, px float64) []GateResult {
	if len(heavy) == 0 || len(light) == 0 {
		return nil
	}
	hl := make([]float64, len(light))
	for i, f := range light {
		hl[i] = f.Widths.Height
	}
	hh := make([]float64, len(heavy))
	for i, f := range heavy {
		hh[i] = f.Widths.Height
	}
	equalScale := px / median(hl)
	type spec struct {
		name, view, band string
		lo, hi           float64
		side             bool
		need             float64
	}
	specs := []spec{
		{"waist width (front)", "front", "torso 1.13-1.19", 1.13, 1.19, false, 3},
		{"thigh thickness (side)", "side", "thigh .76-.84", .76, .84, true, 2},
		{"lower rib depth (side)", "side", "torso 1.30-1.36", 1.30, 1.36, true, 2},
	}
	var out []GateResult
	for _, conv := range []string{"equal scale", "equal height"} {
		g := GateResult{Convention: conv, HeightHeavy: median(hh), HeightLight: median(hl)}
		if conv == "equal scale" {
			g.PxPerYard = equalScale
		}
		width := func(f Figure, s spec) float64 {
			scale := equalScale
			if conv == "equal height" {
				scale = px / f.Widths.Height
			}
			mask := Render(m.Tri, f.Posed, scale, s.side)
			return mask.BandExtent(s.lo*f.Look.BodyScale, s.hi*f.Look.BodyScale)
		}
		for _, s := range specs {
			var a, b []float64
			for _, f := range heavy {
				a = append(a, width(f, s))
			}
			for _, f := range light {
				b = append(b, width(f, s))
			}
			mt := GateMetric{Name: s.name, Need: s.need, Heavy: median(a), Light: median(b), View: s.view, BandName: s.band}
			mt.Diff = mt.Heavy - mt.Light
			mt.Pass = mt.Diff >= mt.Need
			g.Metrics = append(g.Metrics, mt)
		}
		out = append(out, g)
	}
	return out
}

// WriteGate prints the gate results.
func WriteGate(w io.Writer, gs []GateResult, px float64) {
	fmt.Fprintf(w, "silhouette gate (deep consult 1, section C): median OL/DL against median WR, %g px renders\n", px)
	for _, g := range gs {
		scale := "each figure drawn at its own height"
		if g.PxPerYard > 0 {
			scale = fmt.Sprintf("%.2f px per yard for every figure", g.PxPerYard)
		}
		fmt.Fprintf(w, "\n%s (%s); standing height OL/DL %.3f yd, WR %.3f yd\n", g.Convention, scale, g.HeightHeavy, g.HeightLight)
		fmt.Fprintf(w, "%-24s %-6s %8s %8s %8s %6s  %s\n", "metric", "view", "OL/DL px", "WR px", "diff px", "need", "verdict")
		for _, m := range g.Metrics {
			v := "FAIL"
			if m.Pass {
				v = "PASS"
			}
			fmt.Fprintf(w, "%-24s %-6s %8.2f %8.2f %8.2f %6.0f  %s\n", m.Name, m.View, m.Heavy, m.Light, m.Diff, m.Need, v)
		}
	}
}
