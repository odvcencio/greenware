package compiler

import (
	"errors"
	"math"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/kernel/build"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

const twoPi = 2 * math.Pi

// radial repeats m around an axis; a kernel error becomes a diagnostic.
func radial(m *mesh.Mesh, count int, axis rune, centre mesh.Vec3, radius float64, e *elab, n *resolve.Node) *mesh.Mesh {
	out, err := build.RadialArray(m, count, buildAxis(axis), centre, radius)
	if err != nil {
		e.fail(n.Name, err)
		return nil
	}
	return out
}

func buildAxis(a rune) build.Axis {
	switch a {
	case 'x':
		return build.AxisX
	case 'z':
		return build.AxisZ
	}
	return build.AxisY
}

// shape builds one shape at the current level and hands it to the walk.
func (e *elab) shape(n *resolve.Node, c wctx) {
	name := effName(n, c.refl)
	if c.dropped || (name != "" && e.drop[name]) || (n.Name != "" && e.drop[n.Name]) {
		return
	}
	if !e.noFrameFields(n, "a shape "+n.Kind) {
		return
	}
	mat := c.mat
	if f, ok := n.Get("material"); ok && f.Valid() {
		mat = f.Str
	}
	if !e.uvOK(n) {
		return
	}
	var m *mesh.Mesh
	var ok bool
	var err error
	switch n.Kind {
	case "cylinder":
		m, err = e.cylinder(n)
	case "sphere":
		m, err = e.sphere(n)
	case "sweep":
		m, err = e.sweep(n)
	case "lathe":
		m, err = e.lathe(n)
	case "loft":
		m, err = e.loft(n)
	case "ellipsoid":
		m, err = e.ellipsoid(n)
	case "tube":
		m, err = e.tube(n)
	default:
		e.report(diag.CodeUnsupported, e.spanOfShape(n), "the shape `%s` is not supported yet by the compiler (cylinder, sphere, sweep, lathe, loft, ellipsoid and tube are)", n.Kind)
		return
	}
	if err != nil {
		e.fail(n.Name, err)
		return
	}
	cell := c.cell
	if f, ok := n.Get("cell"); ok && f.Valid() {
		cell = f.Str
	}
	if m, ok = e.finishUV(n, m, mat, cell); !ok {
		return
	}
	e.emit(c, mat, m)
}

func (e *elab) spanOfShape(n *resolve.Node) diag.Span { return e.spanOf(n.Name) }

// uvOK accepts the projections the kernel builds: its own chart ("auto"),
// and the explicit planar, box, cylindrical and spherical projections of
// language reference 4.5. A cylindrical projection on a lathe is the chart the
// lathe writes.
func (e *elab) uvOK(n *resolve.Node) bool {
	v := label(n, "uv")
	if v == "" || v == "auto" {
		return true
	}
	if _, ok := mesh.ProjectionOf(v); ok {
		return true
	}
	e.unsupported(n.Name, "`uv = %s` on a %s is not supported yet", v, n.Kind)
	return false
}

// uvEps is the slack in the test that a chart lies inside the unit square.
const uvEps = 1e-9

// finishUV applies the shape's explicit projection and then its atlas cell.
// A projection replaces the builder's chart; a cell maps the unit square of
// the chart into the cell's inset rectangle (language reference 4.5, 4.8) and
// refuses a chart that leaves the unit square rather than add triangles.
func (e *elab) finishUV(n *resolve.Node, m *mesh.Mesh, mat, cell string) (*mesh.Mesh, bool) {
	if v := label(n, "uv"); v != "" && v != "auto" && !(n.Kind == "lathe" && v == "cylindrical") {
		proj, _ := mesh.ProjectionOf(v)
		opt := mesh.ProjectOptions{Axis: 1}
		if ax, has := n.Get("axis"); has && ax.Valid() {
			a, ok := axisOf(ax)
			if !ok {
				e.unsupported(n.Name, "`uv = %s` about a negative axis is not supported yet", v)
				return nil, false
			}
			opt.Axis = map[rune]int{'x': 0, 'y': 1, 'z': 2}[a]
		}
		switch n.Kind {
		case "sphere":
			opt.Centre, opt.HasCentre = vec3(n, "at"), true
		case "ellipsoid":
			opt.Centre, opt.HasCentre = mesh.Add(vec3(n, "centre"), vec3(n, "at")), true
		}
		out, err := m.ProjectUV(proj, opt)
		if err != nil {
			e.fail(n.Name, err)
			return nil, false
		}
		m = out
	}
	if cell == "" {
		return m, true
	}
	a := e.primaryAtlas(mat)
	if a == nil {
		e.unsupported(n.Name, "`cell = %s` needs an atlas in the material `%s`", cell, mat)
		return nil, false
	}
	idx, ok := a.CellIndex(cell)
	if !ok {
		e.unsupported(n.Name, "the atlas `%s` has no cell `%s`", a.Name, cell)
		return nil, false
	}
	out := m.Clone()
	for i, c := range out.Corners {
		u, v := c.UV[0], c.UV[1]
		if !(u >= -uvEps && u <= 1+uvEps && v >= -uvEps && v <= 1+uvEps) {
			u0, v0, u1, v1 := a.Layout.Rect(idx)
			e.report(diag.CodeUVCell, e.spanOf(n.Name), "the chart of `%s` has the coordinate (%.6g, %.6g), outside the unit square, so it cannot lie in the inset rectangle [%.6g, %.6g] x [%.6g, %.6g] of cell `%s`: use an aligned chart or another projection", nameOr(n), u, v, u0, u1, v0, v1, cell)
			return nil, false
		}
		u, v = min(max(u, 0), 1), min(max(v, 0), 1)
		U, V := a.Layout.MapUV(idx, u, v)
		out.Corners[i].UV = mesh.Vec2{U, V}
	}
	return out, true
}

func nameOr(n *resolve.Node) string {
	if n.Name != "" {
		return n.Name
	}
	return n.Kind
}

func child(n *resolve.Node, kind string) *resolve.Node {
	for _, c := range n.Children {
		if c.Kind == kind {
			return c
		}
	}
	return nil
}

func (e *elab) cylinder(n *resolve.Node) (*mesh.Mesh, error) {
	ax, ok := axisOf(unitsField(n, "axis"))
	if !ok {
		return nil, errors.New("a cylinder along a negative axis is not supported yet")
	}
	p := build.CylinderParams{
		Centre: vec3(n, "at"), Axis: buildAxis(ax),
		Radius: num(n, "radius"), Height: num(n, "height"),
		Segments: int(num(n, "segments")), Caps: flag(n, "caps"),
	}
	if b := child(n, "bevel"); b != nil {
		bv := &build.Bevel{Width: num(b, "width"), Segments: int(num(b, "segments"))}
		switch label(b, "edges") {
		case "top":
			bv.Edges = build.BevelTop
		case "bottom":
			bv.Edges = build.BevelBottom
		default: // "caps" and "all"
			bv.Edges = build.BevelCaps
		}
		p.Bevel = bv
	}
	p = build.RetessellateCylinder(p, e.maxErr)
	e.noteChord(p.Radius, twoPi, p.Segments)
	return build.Cylinder(p)
}

func (e *elab) sphere(n *resolve.Node) (*mesh.Mesh, error) {
	p := build.SphereParams{
		Centre: vec3(n, "at"), Radius: num(n, "radius"),
		Segments: int(num(n, "segments")), Rings: int(num(n, "rings")),
	}
	p = build.RetessellateSphere(p, e.maxErr)
	e.noteChord(p.Radius, twoPi, p.Segments)
	e.noteChord(p.Radius, math.Pi, p.Rings)
	return build.Sphere(p)
}

func (e *elab) sweep(n *resolve.Node) (*mesh.Mesh, error) {
	if !isTuple1(n, "end_scale", 1) || num(n, "twist") != 0 {
		return nil, errors.New("`twist` and `end_scale` on a sweep are not supported yet")
	}
	prof, _ := n.Get("profile")
	if prof.Kind != units.KProfile || prof.Str != "circle" || len(prof.Elems) != 2 {
		return nil, errors.New("a sweep needs a circle profile")
	}
	path := child(n, "path")
	if path == nil {
		return nil, errors.New("a sweep needs a path")
	}
	at := vec3(n, "at")
	var pts []mesh.Vec3
	for _, r := range path.Rows {
		if len(r.Values) != 3 {
			return nil, errors.New("a path point needs three coordinates")
		}
		pt := mesh.Vec3{r.Values[0].Num, r.Values[1].Num, r.Values[2].Num}
		pts = append(pts, mesh.Add(pt, at))
	}
	caps := build.CapsOff
	switch label(n, "caps") {
	case "both":
		caps = build.CapsBoth
	case "start":
		caps = build.CapsStart
	case "end":
		caps = build.CapsEnd
	}
	frame := build.FrameMinimal
	if label(n, "frame") == "tube" {
		frame = build.FrameTube
	}
	p := build.SweepParams{
		Radius: prof.Elems[0].Num, Segments: int(prof.Elems[1].Num),
		Path: pts, Caps: caps, Frame: frame,
	}
	p = build.RetessellateSweep(p, e.maxErr)
	e.noteChord(p.Radius, twoPi, p.Segments)
	return build.Sweep(p)
}

func isTuple1(n *resolve.Node, name string, want float64) bool {
	v, ok := n.Get(name)
	return !ok || v.Num == want
}

func (e *elab) lathe(n *resolve.Node) (*mesh.Mesh, error) {
	ax, ok := axisOf(unitsField(n, "axis"))
	if !ok {
		return nil, errors.New("a lathe about a negative axis is not supported yet")
	}
	if arc, ok := n.Get("arc"); ok && arc.Kind == units.KRange && len(arc.Elems) == 2 {
		if arc.Elems[0].Num != 0 || arc.Elems[1].Num != units.TwoPi {
			return nil, errors.New("a partial `arc` on a lathe is not supported yet")
		}
	}
	var rows []mesh.Vec2
	for _, r := range n.Rows {
		if len(r.Values) != 2 {
			return nil, errors.New("a lathe point needs a radius and a height")
		}
		rows = append(rows, mesh.Vec2{r.Values[0].Num, r.Values[1].Num})
	}
	p := build.LatheParams{
		Centre: vec3(n, "at"), Axis: buildAxis(ax), Rows: rows,
		Segments: int(num(n, "segments")), Closed: flag(n, "closed"),
	}
	p = build.RetessellateLathe(p, e.maxErr)
	widest := 0.0 // the radius RetessellateLathe measured
	for _, r := range p.Rows {
		widest = max(widest, r[0])
	}
	e.noteChord(widest, twoPi, p.Segments)
	return build.Lathe(p)
}
