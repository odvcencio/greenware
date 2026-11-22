package accept

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/units"
)

// evalErr is a measurement that could not be taken.
type evalErr struct {
	unavailable bool
	msg         string
	// code is the diagnostic code when the refusal has one of its own: GW-UNIT
	// for an expression that mixes dimensions after reading a data field.
	code string
}

func refuse(format string, a ...any) *evalErr {
	return &evalErr{msg: fmt.Sprintf(format, a...)}
}

func unavailable(format string, a ...any) *evalErr {
	return &evalErr{unavailable: true, msg: fmt.Sprintf(format, a...)}
}

// handle is a node or socket resolved in the scene.
type handle struct {
	idx    int
	name   string
	socket bool
}

type bounds struct{ lo, hi v3 }

// val is an evaluated expression: a units value (numbers, switches, tuples,
// labels, strings), a node or socket handle, or bounds.
type val struct {
	u units.Value
	h *handle
	b *bounds
	m *gltf.Material
}

func uval(u units.Value) val { return val{u: u} }

// evaluator evaluates the expressions of one expect at one output and level.
type evaluator struct {
	sc       *scene
	output   string
	lod      int
	lodErr   float64
	lodKnown bool
	d        units.Defaults
	measures []Measure
	// data holds the acceptance file's data imports; nil when it has none.
	data *Data
	// dataUsed is set once the current expect has read a data field.
	dataUsed bool
	// rd is the rig of the file, prepared on first use.
	rd          *rigDoc
	outlineRefs *outlineRefs
}

func (ev *evaluator) record(name, text string, num *float64) {
	m := Measure{Name: name, Value: text}
	if num != nil {
		m.Number = json.Number(fmtNum(*num))
	}
	ev.measures = append(ev.measures, m)
}

func fmtNum(x float64) string {
	if x == 0 {
		return "0" // folds -0
	}
	return strconv.FormatFloat(x, 'g', 9, 64)
}

// fmtLen writes a length in metres for people: six decimals, trailing zeros
// dropped. The positions are binary32, so digits past the micrometre are noise.
func fmtLen(x float64) string {
	s := strconv.FormatFloat(x, 'f', 6, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s + "m"
}

func (ev *evaluator) recordLen(name string, x float64) {
	ev.record(name, fmtLen(x), &x)
}

func fmtPoint(p v3) string {
	return "(" + fmtLen(p[0]) + ", " + fmtLen(p[1]) + ", " + fmtLen(p[2]) + ")"
}

func (ev *evaluator) recordCount(name string, n int, noun string) {
	x := float64(n)
	ev.record(name, strconv.Itoa(n)+" "+noun, &x)
}

func (ev *evaluator) recordBool(name string, b bool) {
	ev.record(name, strconv.FormatBool(b), nil)
}

// condition evaluates the condition of an expect. When it is a comparison, the
// two sides are measured and recorded separately, so a failure shows both.
func (ev *evaluator) condition(e ast.Expr) (bool, *evalErr) {
	e = unparen(e)
	if b, ok := e.(*ast.Binary); ok && isCompare(b.Op) {
		l, err := ev.eval(b.L)
		if err != nil {
			return false, err
		}
		r, err := ev.eval(b.R)
		if err != nil {
			return false, err
		}
		ev.note(b.L, l)
		ev.note(b.R, r)
		v, err := ev.compare(b.Op, l, r)
		if err != nil {
			return false, err
		}
		return v.u.Bool, nil
	}
	v, err := ev.eval(e)
	if err != nil {
		return false, err
	}
	if v.u.Kind != units.KSwitch {
		return false, refuse("the condition is a %s, not a switch", describe(v))
	}
	return v.u.Bool, nil
}

// note records a comparison side unless it is a bare literal or a call that
// records its own measurement.
func (ev *evaluator) note(e ast.Expr, v val) {
	e = unparen(e)
	switch x := e.(type) {
	case *ast.NumberLit, *ast.StringLit, *ast.BoolLit:
		return
	case *ast.Call:
		switch x.Func {
		case "abs", "min", "max":
		default:
			if _, isMath := units.LookupFunc(x.Func); !isMath {
				return
			}
		}
	}
	if v.u.Kind == units.KNumber {
		n := v.u.Num
		text := v.u.Format()
		if v.u.Dim == units.Length && !v.u.Pending {
			text = fmtLen(n)
		}
		ev.record(exprString(e), text, &n)
	}
}

func isCompare(op string) bool {
	switch op {
	case "==", "!=", "<", "<=", ">", ">=":
		return true
	}
	return false
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.Paren)
		if !ok {
			return e
		}
		e = p.X
	}
}

func describe(v val) string {
	switch {
	case v.h != nil && v.h.socket:
		return "socket handle"
	case v.h != nil:
		return "node handle"
	case v.b != nil:
		return "bounds"
	case v.m != nil:
		return "material factors"
	}
	return v.u.Describe()
}

func (ev *evaluator) uerr(err *units.Error) *evalErr {
	e := refuse("%s", err.Msg)
	if err.Kind == units.ErrUnit && ev.dataUsed {
		e.code = diag.CodeUnit
	}
	return e
}

func (ev *evaluator) eval(e ast.Expr) (val, *evalErr) {
	switch x := e.(type) {
	case *ast.NumberLit:
		return uval(x.Value), nil
	case *ast.ColorLit:
		return uval(units.Col(x.Value)), nil
	case *ast.StringLit:
		return uval(units.Str(x.Value)), nil
	case *ast.BoolLit:
		return uval(units.Switch(x.Value)), nil
	case *ast.Paren:
		return ev.eval(x.X)
	case *ast.Name:
		return ev.name(x.Name)
	case *ast.Qualified:
		if dv, rest, ok := ev.data.value(x); ok {
			ev.dataUsed = true
			v := uval(dv)
			for _, p := range rest {
				var err *evalErr
				if v, err = ev.member(v, p); err != nil {
					return val{}, err
				}
			}
			return v, nil
		}
		v, err := ev.name(x.Parts[0])
		if err != nil {
			return val{}, err
		}
		for _, p := range x.Parts[1:] {
			if v, err = ev.member(v, p); err != nil {
				return val{}, err
			}
		}
		return v, nil
	case *ast.Tuple:
		elems := make([]units.Value, len(x.Elems))
		for i, el := range x.Elems {
			v, err := ev.eval(el)
			if err != nil {
				return val{}, err
			}
			if v.h != nil || v.b != nil || v.m != nil {
				return val{}, refuse("a tuple holds numbers, not a %s", describe(v))
			}
			elems[i] = v.u
		}
		return uval(units.Tuple(elems...)), nil
	case *ast.Unary:
		v, err := ev.eval(x.X)
		if err != nil {
			return val{}, err
		}
		switch x.Op {
		case "-":
			r, uerr := units.Neg(v.u)
			if uerr != nil {
				return val{}, ev.uerr(uerr)
			}
			return uval(r), nil
		case "+":
			return v, nil
		case "!":
			if v.u.Kind != units.KSwitch {
				return val{}, refuse("`!` needs a switch, found a %s", describe(v))
			}
			return uval(units.Switch(!v.u.Bool)), nil
		}
		return val{}, refuse("unknown prefix operator %q", x.Op)
	case *ast.Binary:
		return ev.binary(x)
	case *ast.If:
		c, err := ev.eval(x.Cond)
		if err != nil {
			return val{}, err
		}
		if c.u.Kind != units.KSwitch {
			return val{}, refuse("`if` needs a switch, found a %s", describe(c))
		}
		if c.u.Bool {
			return ev.eval(x.Then)
		}
		return ev.eval(x.Else)
	case *ast.Member:
		v, err := ev.eval(x.X)
		if err != nil {
			return val{}, err
		}
		return ev.member(v, x.Name)
	case *ast.Call:
		return ev.call(x)
	case *ast.Bad:
		return val{}, refuse("the expression did not parse")
	}
	return val{}, refuse("this expression form is not read in an acceptance file")
}

// name resolves a bare identifier: the context values, else a label such as
// `stadium` or `x`, which compare by text.
func (ev *evaluator) name(n string) (val, *evalErr) {
	switch n {
	case "output":
		return uval(units.Label(ev.output)), nil
	case "lod":
		return uval(units.Num(float64(ev.lod))), nil
	case "lod_error":
		if !ev.lodKnown {
			return val{}, refuse("lod_error is not known for this level: the compiled source did not supply it")
		}
		return uval(units.Len(ev.lodErr)), nil
	}
	return uval(units.Label(n)), nil
}

func (ev *evaluator) binary(x *ast.Binary) (val, *evalErr) {
	switch x.Op {
	case "&&", "||":
		l, err := ev.eval(x.L)
		if err != nil {
			return val{}, err
		}
		if l.u.Kind != units.KSwitch {
			return val{}, refuse("`%s` needs switches, found a %s", x.Op, describe(l))
		}
		if (x.Op == "&&" && !l.u.Bool) || (x.Op == "||" && l.u.Bool) {
			return l, nil
		}
		r, err := ev.eval(x.R)
		if err != nil {
			return val{}, err
		}
		if r.u.Kind != units.KSwitch {
			return val{}, refuse("`%s` needs switches, found a %s", x.Op, describe(r))
		}
		return r, nil
	}
	l, err := ev.eval(x.L)
	if err != nil {
		return val{}, err
	}
	r, err := ev.eval(x.R)
	if err != nil {
		return val{}, err
	}
	if isCompare(x.Op) {
		return ev.compare(x.Op, l, r)
	}
	if l.h != nil || l.b != nil || l.m != nil || r.h != nil || r.b != nil || r.m != nil {
		return val{}, refuse("`%s` does not apply to a %s", x.Op, describe(l))
	}
	var u units.Value
	var uerr *units.Error
	switch x.Op {
	case "+":
		u, uerr = units.Add(l.u, r.u, ev.d)
	case "-":
		u, uerr = units.Sub(l.u, r.u, ev.d)
	case "*":
		u, uerr = units.Mul(l.u, r.u, ev.d)
	case "/":
		u, uerr = units.Div(l.u, r.u, ev.d)
	default:
		return val{}, refuse("unknown operator %q", x.Op)
	}
	if uerr != nil {
		return val{}, ev.uerr(uerr)
	}
	return uval(u), nil
}

func (ev *evaluator) compare(op string, l, r val) (val, *evalErr) {
	if l.h != nil || l.b != nil || l.m != nil || r.h != nil || r.b != nil || r.m != nil {
		return val{}, refuse("`%s` does not apply to a %s", op, describe(l))
	}
	a, b := l.u, r.u
	// `output == "stadium"` and `output == stadium` mean the same.
	if (a.Kind == units.KString && b.Kind == units.KLabel) || (a.Kind == units.KLabel && b.Kind == units.KString) {
		a, b = units.Label(a.Str), units.Label(b.Str)
	}
	v, uerr := units.Compare(op, a, b, ev.d)
	if uerr != nil {
		return val{}, ev.uerr(uerr)
	}
	return uval(v), nil
}

func (ev *evaluator) member(v val, name string) (val, *evalErr) {
	if v.m != nil {
		return ev.materialField(v.m, name)
	}
	if v.b != nil {
		switch name {
		case "min":
			return uval(vecValue(v.b.lo)), nil
		case "max":
			return uval(vecValue(v.b.hi)), nil
		}
		return val{}, refuse("bounds have min and max, not %q", name)
	}
	if v.u.Kind == units.KTuple && len(v.u.Elems) == 3 {
		switch name {
		case "x":
			return uval(v.u.Elems[0]), nil
		case "y":
			return uval(v.u.Elems[1]), nil
		case "z":
			return uval(v.u.Elems[2]), nil
		}
	}
	return val{}, refuse("a %s has no member %q", describe(v), name)
}

func vecValue(p v3) units.Value {
	return units.Tuple(units.Len(p[0]), units.Len(p[1]), units.Len(p[2]))
}

// point reads a three-number tuple as a point in metres.
func (ev *evaluator) point(v units.Value) (v3, *evalErr) {
	if v.Kind != units.KTuple || len(v.Elems) != 3 {
		return v3{}, refuse("a point is a tuple of three lengths, found a %s", v.Describe())
	}
	var p v3
	for i, e := range v.Elems {
		c, err := units.Coerce(e, units.Length, ev.d)
		if err != nil {
			return v3{}, ev.uerr(err)
		}
		p[i] = c.Num
	}
	return p, nil
}

// argument binding ------------------------------------------------------

// bind matches a call's arguments to names. Positional arguments fill the
// names in order; a named argument fills its own.
func bind(c *ast.Call, names ...string) ([]ast.Expr, *evalErr) {
	out := make([]ast.Expr, len(names))
	pos := 0
	for _, a := range c.Args {
		if a.Name == "" {
			if pos >= len(names) {
				return nil, refuse("%s takes at most %d argument(s)", c.Func, len(names))
			}
			out[pos] = a.Value
			pos++
			continue
		}
		found := false
		for i, n := range names {
			if n == a.Name {
				if out[i] != nil {
					return nil, refuse("%s: argument %q is given twice", c.Func, a.Name)
				}
				out[i] = a.Value
				found = true
			}
		}
		if !found {
			return nil, refuse("%s has no argument named %q", c.Func, a.Name)
		}
		// A named argument ends the positional run only for ordering; later
		// positional ones are still read in order.
	}
	return out, nil
}

// identifier returns the delivered name an argument spells: a bare name or a
// string.
func identifier(e ast.Expr) (string, bool) {
	switch x := unparen(e).(type) {
	case *ast.Name:
		return x.Name, true
	case *ast.StringLit:
		return x.Value, true
	}
	return "", false
}

// nodeName reads a node slot: a name abbreviates node(name); node(name) is
// also read without being resolved.
func nodeName(e ast.Expr) (string, bool) {
	if n, ok := identifier(e); ok {
		return n, true
	}
	if c, ok := unparen(e).(*ast.Call); ok && c.Func == "node" && len(c.Args) == 1 && c.Args[0].Name == "" {
		return identifier(c.Args[0].Value)
	}
	return "", false
}

func (ev *evaluator) resolve(name string, socket bool) (*handle, *evalErr) {
	full := name
	kind := "node"
	if socket {
		full = "socket." + name
		kind = "socket"
	}
	ids := ev.sc.byName[full]
	switch len(ids) {
	case 0:
		return nil, refuse("no %s named %q at this level", kind, name)
	case 1:
		return &handle{idx: ids[0], name: name, socket: socket}, nil
	}
	return nil, refuse("%d nodes are named %q at this level: the name is ambiguous", len(ids), full)
}

// handleArg evaluates a node or socket slot.
func (ev *evaluator) handleArg(e ast.Expr, what string) (*handle, *evalErr) {
	if e == nil {
		return nil, refuse("%s needs %s", what, "a node")
	}
	if n, ok := identifier(e); ok {
		return ev.resolve(n, false)
	}
	v, err := ev.eval(e)
	if err != nil {
		return nil, err
	}
	if v.h == nil {
		return nil, refuse("%s needs a node, found a %s", what, describe(v))
	}
	return v.h, nil
}

// calls -------------------------------------------------------------------

func (ev *evaluator) call(c *ast.Call) (val, *evalErr) {
	if v, handled, err := ev.rigCall(c); handled {
		return v, err
	}
	switch c.Func {
	case "node", "socket":
		args, err := bind(c, "name")
		if err != nil {
			return val{}, err
		}
		if args[0] == nil {
			return val{}, refuse("%s needs a name", c.Func)
		}
		n, ok := identifier(args[0])
		if !ok {
			return val{}, refuse("%s needs a delivered name", c.Func)
		}
		h, err := ev.resolve(n, c.Func == "socket")
		if err != nil {
			return val{}, err
		}
		return val{h: h}, nil

	case "has_node":
		args, err := bind(c, "name")
		if err != nil {
			return val{}, err
		}
		if args[0] == nil {
			return val{}, refuse("has_node needs a name")
		}
		n, ok := nodeName(args[0])
		if !ok {
			return val{}, refuse("has_node needs a delivered name")
		}
		has := len(ev.sc.byName[n]) > 0
		ev.recordBool(exprString(c), has)
		return uval(units.Switch(has)), nil

	case "position":
		args, err := bind(c, "handle")
		if err != nil {
			return val{}, err
		}
		h, err := ev.handleArg(args[0], "position")
		if err != nil {
			return val{}, err
		}
		p := ev.sc.position(h.idx)
		ev.record(exprString(c), fmtPoint(p), nil)
		return uval(vecValue(p)), nil

	case "distance":
		args, err := bind(c, "a", "b")
		if err != nil {
			return val{}, err
		}
		a, err := ev.pointArg(args[0], "distance")
		if err != nil {
			return val{}, err
		}
		b, err := ev.pointArg(args[1], "distance")
		if err != nil {
			return val{}, err
		}
		d := mesh.Len(mesh.Sub(a, b))
		ev.recordLen(exprString(c), d)
		return uval(units.Len(d)), nil

	case "bounds":
		args, err := bind(c, "node")
		if err != nil {
			return val{}, err
		}
		var nodes []int
		what := "the whole scene"
		if args[0] == nil {
			nodes = ev.sc.allVisible()
		} else {
			h, err := ev.handleArg(args[0], "bounds")
			if err != nil {
				return val{}, err
			}
			nodes = ev.sc.subtree(h.idx)
			what = "the subtree of " + h.name
		}
		var tris []tri
		for _, n := range nodes {
			tris = append(tris, ev.sc.ownTris(n)...)
		}
		lo, hi, ok := boundsOf(tris)
		if !ok {
			return val{}, refuse("bounds of %s: no drawn triangles", what)
		}
		ev.record(exprString(c), "min "+fmtPoint(lo)+" max "+fmtPoint(hi), nil)
		return val{b: &bounds{lo, hi}}, nil

	case "gap":
		args, err := bind(c, "node", "axis", "at")
		if err != nil {
			return val{}, err
		}
		h, err := ev.handleArg(args[0], "gap")
		if err != nil {
			return val{}, err
		}
		if args[1] == nil || args[2] == nil {
			return val{}, refuse("gap needs a node, an axis and a point: gap(node, axis = x, at = (x, y, z))")
		}
		axName, ok := identifier(args[1])
		if !ok || axisIndex(axName) < 0 {
			return val{}, refuse("gap: the axis must be x, y or z")
		}
		atv, err := ev.eval(args[2])
		if err != nil {
			return val{}, err
		}
		at, err := ev.point(atv.u)
		if err != nil {
			return val{}, err
		}
		res := gap(ev.sc.ownTris(h.idx), axisIndex(axName), at)
		if res.refusal != "" {
			return val{}, refuse("gap(%s, %s): %s", h.name, axName, res.refusal)
		}
		total := res.pos + res.neg
		ev.recordLen(exprString(c), total)
		return uval(units.Len(total)), nil

	case "open_edges", "closed", "connected":
		args, err := bind(c, "node")
		if err != nil {
			return val{}, err
		}
		h, err := ev.handleArg(args[0], c.Func)
		if err != nil {
			return val{}, err
		}
		tris := ev.sc.ownTris(h.idx)
		tp := analyse(tris)
		name := exprString(c)
		switch c.Func {
		case "open_edges":
			ev.recordCount(name, tp.open, "open edges")
			return uval(units.Num(float64(tp.open))), nil
		case "closed":
			ok := tp.closed()
			if len(tris) == 0 {
				ev.record(name, "false (the node has no visible triangles of its own)", nil)
				return uval(units.Switch(false)), nil
			}
			ev.recordBool(name, ok)
			ev.recordCount(name+" triangles", tp.tris, "triangles")
			ev.recordCount(name+" open edges", tp.open, "edges used once")
			ev.recordCount(name+" overused edges", tp.over, "edges used more than twice")
			ev.recordCount(name+" misoriented edges", tp.misorient, "edges used twice in one direction")
			return uval(units.Switch(ok)), nil
		default:
			if len(tris) == 0 {
				ev.record(name, "false (the node has no visible triangles of its own)", nil)
				return uval(units.Switch(false)), nil
			}
			groups := shellComponents(tris, tp)
			ev.recordBool(name, groups == 1)
			ev.recordCount(name+" shells", tp.shells, "shells")
			ev.recordCount(name+" contact groups", groups, "groups")
			return uval(units.Switch(groups == 1)), nil
		}

	case "triangles", "drawn_triangles":
		var n int
		if c.Func == "triangles" {
			if len(c.Args) != 0 {
				return val{}, refuse("triangles() takes no argument")
			}
			for _, i := range ev.sc.allVisible() {
				n += len(ev.sc.ownTris(i))
			}
		} else {
			if len(c.Args) == 0 {
				return val{}, refuse("drawn_triangles needs a node; use triangles() for the whole scene")
			}
			args, err := bind(c, "node")
			if err != nil {
				return val{}, err
			}
			h, err := ev.handleArg(args[0], "drawn_triangles")
			if err != nil {
				return val{}, err
			}
			n = len(ev.sc.ownTris(h.idx))
		}
		ev.recordCount(exprString(c), n, "triangles")
		return uval(units.Num(float64(n))), nil

	case "draw_calls":
		if len(c.Args) != 0 {
			return val{}, refuse("draw_calls() takes no argument")
		}
		n := 0
		for _, i := range ev.sc.allVisible() {
			n += ev.sc.primitives(i)
		}
		ev.recordCount(exprString(c), n, "draws")
		return uval(units.Num(float64(n))), nil

	case "outline":
		return ev.outline(c)

	case "material":
		return ev.material(c)

	case "joints", "clip":
		return val{}, unavailable("%s is not implemented by this evaluator", c.Func)
	}

	// Plain math: abs, min, max, sqrt and the rest of the units builtins.
	if _, ok := units.LookupFunc(c.Func); ok {
		args := make([]units.Value, len(c.Args))
		for i, a := range c.Args {
			if a.Name != "" {
				return val{}, refuse("%s takes positional arguments", c.Func)
			}
			v, err := ev.eval(a.Value)
			if err != nil {
				return val{}, err
			}
			if v.h != nil || v.b != nil || v.m != nil {
				return val{}, refuse("%s needs numbers, found a %s", c.Func, describe(v))
			}
			args[i] = v.u
		}
		r, uerr := units.Call(c.Func, args, ev.d)
		if uerr != nil {
			return val{}, ev.uerr(uerr)
		}
		return uval(r), nil
	}
	return val{}, refuse("unknown function %q", c.Func)
}

// pointArg reads a point slot: a socket or node handle (its origin) or a
// tuple of three lengths.
func (ev *evaluator) pointArg(e ast.Expr, what string) (v3, *evalErr) {
	if e == nil {
		return v3{}, refuse("%s needs two points", what)
	}
	v, err := ev.eval(e)
	if err != nil {
		return v3{}, err
	}
	if v.h != nil {
		return ev.sc.position(v.h.idx), nil
	}
	return ev.point(v.u)
}

// exprString writes an expression as short source text for a measurement name.
func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.NumberLit:
		return x.Text
	case *ast.ColorLit:
		return colorText(x.Value)
	case *ast.StringLit:
		return strconv.Quote(x.Value)
	case *ast.BoolLit:
		if x.Value {
			return "on"
		}
		return "off"
	case *ast.Name:
		return x.Name
	case *ast.Qualified:
		return strings.Join(x.Parts, ".")
	case *ast.Paren:
		return "(" + exprString(x.X) + ")"
	case *ast.Tuple:
		parts := make([]string, len(x.Elems))
		for i, el := range x.Elems {
			parts[i] = exprString(el)
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case *ast.Unary:
		return x.Op + exprString(x.X)
	case *ast.Binary:
		return exprString(x.L) + " " + x.Op + " " + exprString(x.R)
	case *ast.Member:
		return exprString(x.X) + "." + x.Name
	case *ast.Call:
		parts := make([]string, len(x.Args))
		for i, a := range x.Args {
			s := exprString(a.Value)
			if a.Name != "" {
				s = a.Name + " = " + s
			}
			parts[i] = s
		}
		return x.Func + "(" + strings.Join(parts, ", ") + ")"
	}
	return "?"
}
