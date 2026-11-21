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
		NoSeam:   !seamOn(n), ShareCaps: flag(n, "weld_caps"),
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
	p := build.TubeParams{Radius: num(n, "radius"), Segments: int(num(n, "segments")), Path: pts, Closed: flag(n, "closed"), NoSeam: !seamOn(n), PointV: label(n, "v_by") == "point", FlipDiagonal: label(n, "diagonal") == "flip"}
	p = build.RetessellateTube(p, e.maxErr)
	e.noteChord(p.Radius, twoPi, p.Segments)
	return build.Tube(p)
}

// seamOn reports whether a ring shape keeps its repeated column (the default).
// `seam = off` takes it away.
func seamOn(n *resolve.Node) bool {
	v, ok := n.Get("seam")
	return !ok || !v.Valid() || v.Bool
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
	var along []float64
	if label(n, "v_by") == "length" {
		var ok bool
		if along, ok = profileV(n); !ok {
			return nil, errors.New("`v_by = length` needs ring columns named rx and rz (x, y and z are 0 when a loft does not name them)")
		}
	}
	for i, row := range keep {
		rings[i] = grid[row]
		v[i] = float64(row) / float64(k-1)
		if along != nil {
			v[i] = along[row]
		}
	}
	m, err := build.Loft(build.LoftParams{Rings: rings, Arc: partial, V: v, Caps: caps, NoSeam: !seamOn(n), ShareCaps: flag(n, "weld_caps")})
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	return m, nil
}

// profileV is each ring's texture v by length: its distance along the profile
// through the ring centres (the columns x, y and z, 0 when absent) and their
// mean radius (rx and rz), from 0 at the first ring to 1 at the last. Sums and
// roots only, so the figures are the same on every platform. It reports false
// when the rows have no rx or rz column.
func profileV(n *resolve.Node) ([]float64, bool) {
	idx := map[string]int{} // lookup only
	for i, c := range loftColumns(n) {
		idx[c] = i
	}
	rxi, ok1 := idx["rx"]
	rzi, ok2 := idx["rz"]
	if !ok1 || !ok2 {
		return nil, false
	}
	val := func(k int, c string) float64 {
		if i, ok := idx[c]; ok {
			return n.Rows[k].Values[i].Num
		}
		return 0
	}
	out := make([]float64, len(n.Rows))
	total := 0.0
	for k := 1; k < len(n.Rows); k++ {
		dx, dy, dz := val(k, "x")-val(k-1, "x"), val(k, "y")-val(k-1, "y"), val(k, "z")-val(k-1, "z")
		dr := float64((n.Rows[k].Values[rxi].Num+n.Rows[k].Values[rzi].Num)/2) - float64((n.Rows[k-1].Values[rxi].Num+n.Rows[k-1].Values[rzi].Num)/2)
		l := math.Sqrt(float64(float64(dx*dx)+float64(dy*dy)) + float64(dz*dz))
		total += math.Sqrt(float64(l*l) + float64(dr*dr))
		out[k] = total
	}
	if total > 0 {
		for k := range out {
			out[k] /= total
		}
	}
	return out, true
}
