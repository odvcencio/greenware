package compiler

import (
	"errors"
	"fmt"
	"math"

	"m31labs.dev/greenware/kernel/build"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

// lengthScale is the asset's default length unit in metres: what one authored
// unit of a bare length is worth. The ellipsoid's end-ring floor is written in
// that unit (language.md section 3.5).
func (e *elab) lengthScale() float64 {
	for _, f := range e.r.Asset {
		if f.Name == "length" && f.Value.Kind == units.KLabel {
			if sc, ok := units.LengthUnit(f.Value.Str); ok {
				return sc
			}
		}
	}
	return 1
}

// ellipsoidFloor is the least horizontal ring radius of an ellipsoid:
// 0.0002 of the asset's length unit, in metres.
const ellipsoidFloor = 0.0002

func (e *elab) ellipsoid(n *resolve.Node) (*mesh.Mesh, error) {
	r := vec3(n, "radii")
	p := build.EllipsoidParams{
		Centre:   mesh.Add(vec3(n, "centre"), vec3(n, "at")),
		Radii:    r,
		Segments: int(num(n, "segments")),
		Rings:    int(num(n, "rings")),
		Floor:    float64(ellipsoidFloor * e.lengthScale()),
	}
	p = build.RetessellateEllipsoid(p, e.maxErr)
	e.noteChord(max(r[0], r[2]), twoPi, p.Segments)
	e.noteChord(max(r[0], r[1], r[2]), math.Pi, p.Rings-1)
	return build.Ellipsoid(p)
}

func (e *elab) tube(n *resolve.Node) (*mesh.Mesh, error) {
	at := vec3(n, "at")
	var pts []mesh.Vec3
	for _, r := range n.Rows {
		if len(r.Values) != 3 {
			return nil, errors.New("a tube point needs three coordinates")
		}
		pts = append(pts, mesh.Add(mesh.Vec3{r.Values[0].Num, r.Values[1].Num, r.Values[2].Num}, at))
	}
	p := build.TubeParams{Radius: num(n, "radius"), Segments: int(num(n, "segments")), Path: pts, Closed: flag(n, "closed")}
	p = build.RetessellateTube(p, e.maxErr)
	e.noteChord(p.Radius, twoPi, p.Segments)
	return build.Tube(p)
}

// loftColumns returns the column names of a loft's rows.
func loftColumns(n *resolve.Node) []string {
	v, ok := n.Get("columns")
	if !ok || !v.Valid() {
		return []string{"x", "y", "z", "rx", "rz"}
	}
	out := make([]string, len(v.Elems))
	for i, c := range v.Elems {
		out[i] = c.Str
	}
	return out
}

// loftGrid returns the points of every ring at a column count, in metres,
// before `at` is added. A loft with a vertex rule asks the rule; the default
// rule is the elliptic ring of the kernel, written by the column names.
func (e *elab) loftGrid(n *resolve.Node, segs int, arc bool, lo, hi float64) ([][]mesh.Vec3, error) {
	grid := make([][]mesh.Vec3, len(n.Rows))
	if n.Loft != nil {
		pts, err := n.Loft.Points(segs, arc, lo, hi)
		if err != nil {
			return nil, err
		}
		for k, ring := range pts {
			grid[k] = make([]mesh.Vec3, len(ring))
			for j, p := range ring {
				grid[k][j] = mesh.Vec3(p)
			}
		}
		return grid, nil
	}
	idx := map[string]int{} // lookup only
	for i, c := range loftColumns(n) {
		idx[c] = i
	}
	for k, row := range n.Rows {
		v := func(c string) float64 { return row.Values[idx[c]].Num }
		grid[k] = build.LoftRing(v("x"), v("y"), v("z"), v("rx"), v("rz"), segs, arc, lo, hi)
	}
	return grid, nil
}

// loftRadius is the placed radius a loft's angular error is measured with
// (language.md section 6.1 step 2, local maximum radius): the largest distance
// in the ring's plane, across the loft's +Y axis, of any point of a full turn
// from the centre of its ring. A vertex rule may also lift or drop points
// along the axis; that is not an angular construction and does not count.
func (e *elab) loftRadius(n *resolve.Node, source int) (float64, error) {
	cols := max(source, 8)
	full, err := e.loftGrid(n, cols, false, 0, 0)
	if err != nil {
		return 0, err
	}
	r := 0.0
	for _, ring := range full {
		var c mesh.Vec3
		for _, p := range ring {
			c = mesh.Add(c, p)
		}
		c = mesh.Scale(c, 1/float64(len(ring)))
		for _, p := range ring {
			d := mesh.Sub(p, c)
			r = max(r, mesh.Len(mesh.Vec3{d[0], 0, d[2]}))
		}
	}
	return r, nil
}

func (e *elab) loft(n *resolve.Node) (*mesh.Mesh, error) {
	if len(n.Rows) < 2 {
		return nil, errors.New("a loft needs at least 2 rings")
	}
	source := int(num(n, "segments"))
	arcV, _ := n.Get("arc")
	lo, hi := 0.0, units.TwoPi
	if len(arcV.Elems) == 2 {
		lo, hi = arcV.Elems[0].Num, arcV.Elems[1].Num
	}
	partial := !(lo == 0 && hi == units.TwoPi)
	var caps build.Caps
	switch label(n, "caps") {
	case "bottom":
		caps = build.CapsStart
	case "top":
		caps = build.CapsEnd
	case "off":
		caps = build.CapsOff
	default:
		caps = build.CapsBoth
	}
	segs := source
	span := hi - lo
	if e.maxErr > 0 {
		r, err := e.loftRadius(n, source)
		if err != nil {
			return nil, err
		}
		segs = build.LoftSegments(r, partial, span, source, e.maxErr)
		e.noteChord(r, span, segs)
	} else if r, err := e.loftRadius(n, source); err == nil {
		e.noteChord(r, span, segs)
	}
	grid, err := e.loftGrid(n, segs, partial, lo, hi)
	if err != nil {
		return nil, err
	}
	at := vec3(n, "at")
	for _, ring := range grid {
		for j := range ring {
			ring[j] = mesh.Add(ring[j], at)
		}
	}
	// Level rows are chosen on the all-row strip at this level's columns, and
	// keep their original indices for the texture chart.
	keep := build.LoftKeepRows(grid, !partial, e.maxErr)
	k := len(grid)
	rings := make([][]mesh.Vec3, len(keep))
	v := make([]float64, len(keep))
	for i, row := range keep {
		rings[i] = grid[row]
		v[i] = float64(row) / float64(k-1)
	}
	m, err := build.Loft(build.LoftParams{Rings: rings, Arc: partial, V: v, Caps: caps})
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return m, nil
}
