package compiler

import (
	"fmt"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/kernel/build"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

// piece is one shape's mesh, in the frame of the node it will merge into,
// with the name of the material it draws with ("" for none).
type piece struct {
	mat string
	m   *mesh.Mesh
}

// socketOut is a socket placed in its node's frame.
type socketOut struct {
	name string
	kind string
	pos  mesh.Vec3
}

// snode is one glTF node of a level: the root and every exported part that
// the level keeps. Parts that are not exported merge their shapes into the
// nearest exported ancestor's pieces.
type snode struct {
	name    string
	t       mesh.Vec3 // translation relative to the parent node
	pieces  []piece
	kids    []*snode
	sockets []socketOut
}

// wctx is the walk state.
type wctx struct {
	node    *snode      // nearest exported ancestor; nil while collecting
	collect *[]piece    // array contents are gathered here, in the array's frame
	a       mesh.Affine // the current frame, relative to node's frame (or the array's)
	mat     string      // inherited material
	dropped bool        // inside geometry this level drops
	refl    bool        // in the reflected copy of a mirror
	mirror  bool        // inside a mirror
	depth   int
}

// elab holds the state of building one output.
type elab struct {
	file  *ast.File
	r     *resolve.Resolved
	defs  map[string]*resolve.Node // lookup only
	diags []diag.Diagnostic

	// per level
	maxErr float64
	// lodErr is the largest chord error over the shapes this level draws, in
	// metres: the kernel's ChordError of each shape's retessellated counts.
	lodErr float64
	drop   map[string]bool // lookup only
}

func (e *elab) report(code string, sp diag.Span, format string, args ...any) {
	e.diags = append(e.diags, diag.New(code, sp, format, args...))
}

func (e *elab) unsupported(name string, format string, args ...any) {
	e.report(diag.CodeUnsupported, e.spanOf(name), format, args...)
}

// failed reports whether the walk has found an error.
func (e *elab) failed() bool { return diag.HasErrors(e.diags) }

// spanOf finds the source span of the first declaration with the name.
func (e *elab) spanOf(name string) diag.Span {
	if name != "" {
		for _, p := range e.file.Parts {
			if sp, ok := findName(p, name); ok {
				return sp
			}
		}
		if e.file.Collision != nil {
			if sp, ok := findItems(e.file.Collision.Items, name); ok {
				return sp
			}
		}
		for _, m := range e.file.Materials {
			if m.Name.Name == name {
				return m.Name.Span
			}
		}
	}
	if e.r.Root != nil {
		for _, p := range e.file.Parts {
			if p.Name.Name == e.r.Root.Name {
				return p.Name.Span
			}
		}
	}
	return diag.Span{}
}

func findName(p *ast.Part, name string) (diag.Span, bool) {
	if p.Name.Name == name {
		return p.Name.Span, true
	}
	if p.Pair.Name == name {
		return p.Pair.Span, true
	}
	return findItems(p.Items, name)
}

func findItems(items []ast.Item, name string) (diag.Span, bool) {
	pick := func(a, b ast.Ident) (diag.Span, bool) {
		if a.Name == name {
			return a.Span, true
		}
		if b.Name == name {
			return b.Span, true
		}
		return diag.Span{}, false
	}
	for _, it := range items {
		switch n := it.(type) {
		case *ast.Part:
			if sp, ok := findName(n, name); ok {
				return sp, true
			}
		case *ast.Shape:
			if sp, ok := pick(n.Name, n.Pair); ok {
				return sp, true
			}
		case *ast.Array:
			if sp, ok := pick(n.Name, n.Pair); ok {
				return sp, true
			}
			if sp, ok := findItems(n.Items, name); ok {
				return sp, true
			}
		case *ast.Instance:
			if sp, ok := pick(n.Name, n.Pair); ok {
				return sp, true
			}
		case *ast.Socket:
			if sp, ok := pick(n.Name, n.Pair); ok {
				return sp, true
			}
		case *ast.Collider:
			if sp, ok := pick(n.Name, n.Pair); ok {
				return sp, true
			}
		case *ast.Mirror:
			if sp, ok := findItems(n.Items, name); ok {
				return sp, true
			}
		case *ast.When:
			if sp, ok := findItems(n.Then, name); ok {
				return sp, true
			}
			if sp, ok := findItems(n.Else, name); ok {
				return sp, true
			}
		}
	}
	return diag.Span{}, false
}

// ---- field access ----------------------------------------------------------

func num(n *resolve.Node, name string) float64 {
	v, _ := n.Get(name)
	return v.Num
}

func label(n *resolve.Node, name string) string {
	v, _ := n.Get(name)
	return v.Str
}

func flag(n *resolve.Node, name string) bool {
	v, _ := n.Get(name)
	return v.Bool
}

func vec3(n *resolve.Node, name string) mesh.Vec3 {
	v, _ := n.Get(name)
	a, _ := v.Vec3()
	return mesh.Vec3(a)
}

// isIdentityField reports whether the field is absent or equals want in every
// component.
func isTuple(n *resolve.Node, name string, want float64) bool {
	v, ok := n.Get(name)
	if !ok {
		return true
	}
	a, ok := v.Vec3()
	if !ok {
		return false
	}
	return a[0] == want && a[1] == want && a[2] == want
}

// noFrameFields reports GW-UNSUPPORTED for a rotation, scale or pivot that is
// not the identity. It returns false when it reported.
func (e *elab) noFrameFields(n *resolve.Node, what string) bool {
	ok := true
	for _, f := range []struct {
		name string
		want float64
	}{{"rot", 0}, {"scale", 1}, {"pivot", 0}} {
		if !isTuple(n, f.name, f.want) {
			e.unsupported(n.Name, "`%s` on %s is not supported yet: the first compiler places parts by `at` only", f.name, what)
			ok = false
		}
	}
	return ok
}

// effName is the name a declaration has in the current copy of a mirror.
func effName(n *resolve.Node, refl bool) string {
	if refl && n.Pair != "" {
		return n.Pair
	}
	return n.Name
}

// ---- frames ----------------------------------------------------------------

var identityAffine = mesh.IdentityAffine

func (a wctx) identity() bool { return a.a.M == mesh.Identity3 && a.a.T == (mesh.Vec3{}) }

// placeAt returns the frame at offset at inside frame a.
func placeAt(a mesh.Affine, at mesh.Vec3) mesh.Affine {
	return mesh.Affine{M: a.M, T: mesh.Add(a.M.MulVec(at), a.T)}
}

func planeOf(label string) mesh.Plane {
	switch label {
	case "y":
		return mesh.PlaneY
	case "z":
		return mesh.PlaneZ
	}
	return mesh.PlaneX
}

// ---- the walk --------------------------------------------------------------

const maxDepth = 64

// emit places a mesh in the current frame and hands it to its collector.
func (e *elab) emit(c wctx, mat string, m *mesh.Mesh) {
	if c.dropped || m == nil || m.TriangleCount() == 0 {
		return
	}
	if !c.identity() {
		m = m.Transform(c.a)
	}
	p := piece{mat: mat, m: m}
	if c.collect != nil {
		*c.collect = append(*c.collect, p)
		return
	}
	c.node.pieces = append(c.node.pieces, p)
}

func (e *elab) walk(n *resolve.Node, c wctx) {
	if c.depth > maxDepth {
		e.unsupported(n.Name, "the part tree is nested more than %d levels deep", maxDepth)
		return
	}
	c.depth++
	switch n.Kind {
	case "part":
		e.part(n, c, false)
	case "mirror":
		e.mirrorNode(n, c)
	case "array":
		e.array(n, c)
	case "instance":
		e.instance(n, c)
	case "socket":
		e.socket(n, c)
	case "when":
		e.unsupported(n.Name, "a `when` on a runtime parameter is not supported yet: it needs a hidden-branch scene")
	default:
		e.shape(n, c)
	}
}

// part walks a part. The root is always a node of its own.
func (e *elab) part(n *resolve.Node, c wctx, isRoot bool) {
	name := effName(n, c.refl)
	if !e.noFrameFields(n, "a part") {
		return
	}
	at := vec3(n, "at")
	if f, ok := n.Get("material"); ok && f.Valid() {
		c.mat = f.Str
	}
	if e.drop[name] || (n.Name != name && e.drop[n.Name]) {
		c.dropped = true
	}
	c.a = placeAt(c.a, at)
	export := isRoot || flag(n, "export")
	if export {
		if c.collect != nil {
			e.unsupported(n.Name, "`export` inside an array is not supported yet")
			return
		}
		if !c.dropped {
			if isRoot {
				// The root node is made by the caller; its frame starts here.
				c.node.t = c.a.T
				c.a = mesh.Affine{M: c.a.M}
			} else {
				kid := &snode{name: name, t: c.a.T}
				c.node.kids = append(c.node.kids, kid)
				c.node = kid
				c.a = mesh.Affine{M: c.a.M}
			}
		}
	}
	for _, ch := range n.Children {
		e.walk(ch, c)
	}
}

func (e *elab) mirrorNode(n *resolve.Node, c wctx) {
	if c.mirror {
		e.unsupported("", "a mirror inside a mirror is not supported yet")
		return
	}
	c.mirror = true
	plane := planeOf(label(n, "plane"))
	for _, ch := range n.Children {
		e.walk(ch, c)
	}
	r := c
	r.refl = true
	r.a = mesh.Affine{M: c.a.M.Mul(mesh.MirrorAffine(plane).M), T: c.a.T}
	for _, ch := range n.Children {
		e.walk(ch, r)
	}
}

func (e *elab) instance(n *resolve.Node, c wctx) {
	of, _ := n.Get("of")
	def := e.defs[of.Str]
	if def == nil {
		e.unsupported(of.Str, "the part `%s` that an instance places was not found", of.Str)
		return
	}
	if !e.noFrameFields(n, "an instance") {
		return
	}
	if e.drop[n.Name] && n.Name != "" {
		c.dropped = true
	}
	c.a = placeAt(c.a, vec3(n, "at"))
	e.part(def, c, false)
}

func (e *elab) socket(n *resolve.Node, c wctx) {
	if c.collect != nil {
		e.unsupported(n.Name, "a socket inside an array is not supported yet")
		return
	}
	if !e.noFrameFields(n, "a socket") {
		return
	}
	pos := mesh.Add(c.a.M.MulVec(vec3(n, "at")), c.a.T)
	c.node.sockets = append(c.node.sockets, socketOut{name: effName(n, c.refl), kind: label(n, "kind"), pos: pos})
}

func axisOf(v units.Value) (rune, bool) {
	switch v.Str {
	case "+x", "x":
		return 'x', true
	case "+y", "y":
		return 'y', true
	case "+z", "z":
		return 'z', true
	}
	return 0, false
}

func (e *elab) array(n *resolve.Node, c wctx) {
	var coll []piece
	sub := c
	sub.collect = &coll
	sub.a = identityAffine
	for _, ch := range n.Children {
		e.walk(ch, sub)
	}
	if len(coll) == 0 || e.failed() {
		return
	}
	mode := label(n, "mode")
	cv, _ := n.Get("count")
	for _, p := range coll {
		var out *mesh.Mesh
		switch mode {
		case "radial":
			ax, ok := axisOf(unitsField(n, "axis"))
			if !ok {
				e.unsupported(n.Name, "a radial array about a negative axis is not supported yet")
				return
			}
			out = radial(p.m, int(cv.Num), ax, vec3(n, "centre"), num(n, "radius"), e, n)
		case "linear":
			step := vec3(n, "step")
			var cs []*mesh.Mesh
			for i := 0; i < int(cv.Num); i++ {
				cs = append(cs, p.m.Translate(mesh.Scale(step, float64(i))))
			}
			out = mesh.Merge(cs...)
		case "grid":
			step := vec3(n, "step")
			var cs []*mesh.Mesh
			cnt := cv.Elems
			if len(cnt) == 3 {
				for ix := 0; ix < int(cnt[0].Num); ix++ {
					for iy := 0; iy < int(cnt[1].Num); iy++ {
						for iz := 0; iz < int(cnt[2].Num); iz++ {
							d := mesh.Vec3{
								float64(step[0] * float64(ix)),
								float64(step[1] * float64(iy)),
								float64(step[2] * float64(iz)),
							}
							cs = append(cs, p.m.Translate(d))
						}
					}
				}
			}
			out = mesh.Merge(cs...)
		}
		if out == nil {
			return
		}
		// The collected mesh is already in the array's frame, which is the
		// current frame, so it goes through the current placement once.
		e.emit(c, p.mat, out)
	}
}

func unitsField(n *resolve.Node, name string) units.Value {
	v, _ := n.Get(name)
	return v
}

// fail formats a kernel error as a diagnostic on a named declaration.
func (e *elab) fail(name string, err error) {
	e.report(diag.CodeFieldRange, e.spanOf(name), "%s", fmt.Sprint(err))
}

// noteChord records the chord error of one drawn shape, after retessellation.
// Bevel segments and authored sweep or lathe rows are not tessellation and do
// not count (language.md section 6.1).
func (e *elab) noteChord(radius, span float64, n int) {
	if n < 1 {
		return
	}
	if c := build.ChordError(radius, span, n); c > e.lodErr {
		e.lodErr = c
	}
}
