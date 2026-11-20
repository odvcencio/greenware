package compiler

import (
	"errors"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/kernel/build"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

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
	default:
		e.report(diag.CodeUnsupported, e.spanOfShape(n), "the shape `%s` is not supported yet by the first compiler (cylinder, sphere, sweep and lathe are)", n.Kind)
		return
	}
	if err != nil {
		e.fail(n.Name, err)
		return
	}
	e.emit(c, mat, m)
}

func (e *elab) spanOfShape(n *resolve.Node) diag.Span { return e.spanOf(n.Name) }

// uvOK accepts the projections the kernel builds: its own parametrisation
// ("auto"), and "cylindrical" on a lathe, which is what the lathe writes.
func (e *elab) uvOK(n *resolve.Node) bool {
	v := label(n, "uv")
	if v == "" || v == "auto" || (n.Kind == "lathe" && v == "cylindrical") {
		return true
	}
	e.unsupported(n.Name, "`uv = %s` on a %s is not supported yet", v, n.Kind)
	return false
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
	return build.Cylinder(build.RetessellateCylinder(p, e.maxErr))
}

func (e *elab) sphere(n *resolve.Node) (*mesh.Mesh, error) {
	p := build.SphereParams{
		Centre: vec3(n, "at"), Radius: num(n, "radius"),
		Segments: int(num(n, "segments")), Rings: int(num(n, "rings")),
	}
	return build.Sphere(build.RetessellateSphere(p, e.maxErr))
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
	return build.Sweep(build.RetessellateSweep(p, e.maxErr))
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
	return build.Lathe(build.RetessellateLathe(p, e.maxErr))
}
