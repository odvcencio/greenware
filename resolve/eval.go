package resolve

import (
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// opError reports a failed operation as the diagnostic its kind calls for.
func (r *run) opError(err *units.Error, sp diag.Span) {
	code := diag.CodeType
	switch err.Kind {
	case units.ErrUnit:
		code = diag.CodeUnit
	case units.ErrDomain:
		code = diag.CodeDomain
	case units.ErrUnsupported:
		code = diag.CodeUnsupported
	}
	r.report(diag.New(code, sp, "%s", err.Msg))
}

// unsupportedFuncs are functions of the language that this compiler does not
// evaluate.
var unsupportedFuncs = []string{"joint", "angle", "paint", "jpeg", "grain", "noise", "turntable", "enum", "cloth", "flat"}

// eval evaluates an expression in a scope. A failed evaluation reports its
// diagnostic and returns an invalid value; operations on an invalid value
// stay silent, so one fault gives one diagnostic.
func (r *run) eval(e ast.Expr, sc *scope) units.Value {
	switch n := e.(type) {
	case nil:
		return units.Bad()
	case *ast.Bad:
		return units.Bad()
	case *ast.NumberLit:
		return n.Value
	case *ast.ColorLit:
		return units.Col(n.Value)
	case *ast.StringLit:
		return units.Str(n.Value)
	case *ast.BoolLit:
		return units.Switch(n.Value)
	case *ast.Paren:
		return r.eval(n.X, sc)
	case *ast.Name:
		return r.lookup(n.Name, n.Span(), sc)
	case *ast.Qualified:
		// The token is one run of text with no spaces, so each part's span
		// follows from the lengths of the parts before it.
		at := n.Span().Start
		v := r.lookup(n.Parts[0], diag.Span{Start: at, End: at + len(n.Parts[0])}, sc)
		for i, m := range n.Parts[1:] {
			at += len(n.Parts[i]) + 1 // the previous part and its dot
			v = r.member(v, m, diag.Span{Start: at, End: at + len(m)})
		}
		return v
	case *ast.Tuple:
		return r.elems(n.Elems, sc, units.Tuple)
	case *ast.List:
		return r.elems(n.Elems, sc, units.List)
	case *ast.Member:
		return r.member(r.eval(n.X, sc), n.Name, n.Span())
	case *ast.Index:
		return r.index(n, sc)
	case *ast.Unary:
		return r.unary(n, sc)
	case *ast.Binary:
		return r.binary(n, sc)
	case *ast.If:
		return r.ifExpr(n, sc)
	case *ast.Call:
		return r.call(n, sc)
	}
	return units.Bad()
}

func (r *run) lookup(name string, sp diag.Span, sc *scope) units.Value {
	b := sc.lookup(name)
	if b == nil {
		d := diag.New(diag.CodeUnknownName, sp, "`%s` is not a parameter, let or local value here", name)
		if s, ok := registry.Suggest(name, sc.visible()); ok {
			d = d.WithEdit("write "+s, sp, s)
		}
		r.report(d)
		return units.Bad()
	}
	return r.force(b)
}

func (r *run) elems(es []ast.Expr, sc *scope, mk func(...units.Value) units.Value) units.Value {
	out := make([]units.Value, len(es))
	ok := true
	for i, e := range es {
		out[i] = r.eval(e, sc)
		ok = ok && out[i].Valid()
	}
	if !ok {
		return units.Bad()
	}
	return mk(out...)
}

func (r *run) member(v units.Value, name string, sp diag.Span) units.Value {
	if !v.Valid() {
		return v
	}
	idx := strings.Index("xyz", name)
	if v.Kind != units.KTuple || len(v.Elems) != 3 || len(name) != 1 || idx < 0 {
		r.report(diag.New(diag.CodeType, sp, "a %s has no member `%s`; x, y and z select from a triple", v.Describe(), name))
		return units.Bad()
	}
	out := v.Elems[idx]
	out.Runtime = out.Runtime || v.Runtime
	return out
}

func (r *run) index(n *ast.Index, sc *scope) units.Value {
	x, i := r.eval(n.X, sc), r.eval(n.I, sc)
	if !x.Valid() || !i.Valid() {
		return units.Bad()
	}
	if x.Kind != units.KList && x.Kind != units.KTuple {
		r.report(diag.New(diag.CodeType, n.X.Span(), "a %s cannot be indexed", x.Describe()))
		return units.Bad()
	}
	iv, err := units.Coerce(i, units.Number, r.d)
	if err != nil || !units.IsIntegral(iv.Num) {
		r.report(diag.New(diag.CodeType, n.I.Span(), "an index must be a whole number"))
		return units.Bad()
	}
	k := int(iv.Num)
	if k < 0 || k >= len(x.Elems) {
		r.report(diag.New(diag.CodeDomain, n.Span(), "index %d is outside 0..%d", k, len(x.Elems)-1))
		return units.Bad()
	}
	out := x.Elems[k]
	out.Runtime = out.Runtime || x.Runtime || i.DependsOnRuntime()
	return out
}

func (r *run) unary(n *ast.Unary, sc *scope) units.Value {
	x := r.eval(n.X, sc)
	if !x.Valid() {
		return x
	}
	switch n.Op {
	case "-":
		v, err := units.Neg(x)
		if err != nil {
			r.opError(err, n.Span())
			return units.Bad()
		}
		return v
	case "+":
		if x.Kind != units.KNumber && x.Kind != units.KTuple {
			r.report(diag.New(diag.CodeType, n.Span(), "unary + needs a number, found a %s", x.Describe()))
			return units.Bad()
		}
		return x
	case "!":
		if x.Kind != units.KSwitch {
			r.report(diag.New(diag.CodeType, n.Span(), "! needs a switch, found a %s", x.Describe()))
			return units.Bad()
		}
		x.Bool = !x.Bool
		return x
	}
	return units.Bad()
}

// dry evaluates an expression whose value is not used, to find name and type
// errors in a branch that is not taken. Domain errors do not count there.
func (r *run) dry(e ast.Expr, sc *scope) units.Value {
	r.quiet++
	defer func() { r.quiet-- }()
	return r.eval(e, sc)
}

func (r *run) switchOperand(v units.Value, sp diag.Span, op string) bool {
	if v.Kind != units.KSwitch {
		r.report(diag.New(diag.CodeType, sp, "%s needs switches, found a %s", op, v.Describe()))
		return false
	}
	return true
}

func (r *run) binary(n *ast.Binary, sc *scope) units.Value {
	if n.Op == "&&" || n.Op == "||" {
		l := r.eval(n.L, sc)
		if !l.Valid() {
			r.dry(n.R, sc)
			return l
		}
		if !r.switchOperand(l, n.L.Span(), n.Op) {
			r.dry(n.R, sc)
			return units.Bad()
		}
		short := (n.Op == "&&" && !l.Bool) || (n.Op == "||" && l.Bool)
		var rv units.Value
		if short {
			rv = r.dry(n.R, sc)
		} else {
			rv = r.eval(n.R, sc)
		}
		if !rv.Valid() {
			return rv
		}
		if !r.switchOperand(rv, n.R.Span(), n.Op) {
			return units.Bad()
		}
		out := l
		if !short {
			out = rv
		}
		out.Runtime = l.DependsOnRuntime() || rv.DependsOnRuntime()
		return out
	}
	if n.Op == "==" || n.Op == "!=" {
		if v, ok := r.enumCompare(n, sc); ok {
			return v
		}
	}
	l, rv := r.eval(n.L, sc), r.eval(n.R, sc)
	if !l.Valid() || !rv.Valid() {
		return units.Bad()
	}
	var out units.Value
	var err *units.Error
	switch n.Op {
	case "+":
		out, err = units.Add(l, rv, r.d)
	case "-":
		out, err = units.Sub(l, rv, r.d)
	case "*":
		out, err = units.Mul(l, rv, r.d)
	case "/":
		out, err = units.Div(l, rv, r.d)
	default:
		out, err = units.Compare(n.Op, l, rv, r.d)
	}
	if err != nil {
		r.opError(err, n.Span())
		return units.Bad()
	}
	return out
}

func (r *run) ifExpr(n *ast.If, sc *scope) units.Value {
	c := r.eval(n.Cond, sc)
	if c.Valid() && !r.switchOperand(c, n.Cond.Span(), "if") {
		c = units.Bad()
	}
	if !c.Valid() {
		r.dry(n.Then, sc)
		r.dry(n.Else, sc)
		return units.Bad()
	}
	var taken, other units.Value
	if c.Bool {
		taken, other = r.eval(n.Then, sc), r.dry(n.Else, sc)
	} else {
		taken, other = r.dry(n.Then, sc), r.eval(n.Else, sc)
	}
	if !taken.Valid() || !other.Valid() {
		return units.Bad()
	}
	unified, ok := units.Unify(taken, other, r.d)
	if !ok {
		r.report(diag.New(diag.CodeUnit, n.Span(), "the two branches of `if` have different types: %s and %s", describe(taken), describe(other)))
		return units.Bad()
	}
	// Both branches share one type whichever is taken (spec 2.3 rule 1).
	unified.Runtime = taken.DependsOnRuntime() || c.DependsOnRuntime()
	return unified
}

func describe(v units.Value) string { return v.Describe() }

func (r *run) call(n *ast.Call, sc *scope) units.Value {
	if strings.Contains(n.Func, ".") {
		r.report(diag.New(diag.CodeUnsupported, n.FuncSpan, "the qualified function `%s` is valid in the language but outside this compiler's subset", n.Func))
		return units.Bad()
	}
	if n.Func == "circle" {
		r.report(diag.New(diag.CodeType, n.Span(), "circle(...) makes a profile; it can only fill a `profile` field"))
		return units.Bad()
	}
	if _, ok := units.LookupFunc(n.Func); !ok {
		for _, u := range unsupportedFuncs {
			if u == n.Func {
				r.report(diag.New(diag.CodeUnsupported, n.FuncSpan, "the function `%s` is valid in the language but outside this compiler's subset", n.Func))
				return units.Bad()
			}
		}
		d := diag.New(diag.CodeUnknownName, n.FuncSpan, "unknown function `%s`", n.Func)
		if s, ok := registry.Suggest(n.Func, units.FuncNames()); ok {
			d = d.WithEdit("write "+s, n.FuncSpan, s)
		}
		r.report(d)
		return units.Bad()
	}
	args := make([]units.Value, len(n.Args))
	good := true
	for i, a := range n.Args {
		if a.Name != "" || a.Range != nil || a.Value == nil {
			r.report(diag.New(diag.CodeType, a.Span, "`%s` takes plain positional arguments", n.Func))
			good = false
			continue
		}
		args[i] = r.eval(a.Value, sc)
		good = good && args[i].Valid()
	}
	if !good {
		return units.Bad()
	}
	v, err := units.Call(n.Func, args, r.d)
	if err != nil {
		d := diag.New(map[units.ErrKind]string{units.ErrUnit: diag.CodeUnit, units.ErrType: diag.CodeType,
			units.ErrDomain: diag.CodeDomain, units.ErrUnsupported: diag.CodeUnsupported}[err.Kind], n.Span(), "%s", err.Msg)
		if err.BareAngle && len(n.Args) > 0 {
			if lit, ok := n.Args[0].Value.(*ast.NumberLit); ok {
				end := diag.Span{Start: lit.Span().End, End: lit.Span().End}
				d = d.WithEdit("write "+lit.Text+"rad", end, "rad").WithEdit("write "+lit.Text+"deg", end, "deg")
			}
		}
		r.report(d)
		return units.Bad()
	}
	return v
}

// unboundName returns the identifier an operand is when no value of that name
// is in scope: a candidate enum member.
func unboundName(e ast.Expr, sc *scope) (string, diag.Span, bool) {
	for {
		p, ok := e.(*ast.Paren)
		if !ok {
			break
		}
		e = p.X
	}
	if nm, ok := e.(*ast.Name); ok && sc.lookup(nm.Name) == nil {
		return nm.Name, nm.Span(), true
	}
	return "", diag.Span{}, false
}

// enumCompare reads `param == member`. A member of an enum is a name of the
// enum, not a value in scope, so the side that names no value is the member
// (spec 2.2). When the other side is a parameter, the member must belong to
// its enum. It reports false when the comparison is not of this shape.
func (r *run) enumCompare(n *ast.Binary, sc *scope) (units.Value, bool) {
	ln, lsp, lu := unboundName(n.L, sc)
	rn, rsp, ru := unboundName(n.R, sc)
	if lu == ru {
		return units.Value{}, false
	}
	name, sp, other := ln, lsp, n.R
	if ru {
		name, sp, other = rn, rsp, n.L
	}
	ov := r.eval(other, sc)
	if !ov.Valid() {
		return units.Bad(), true
	}
	if ov.Kind != units.KLabel {
		return units.Value{}, false
	}
	if nm, _, ok := nameOf(unparen(other)); ok {
		if b := sc.lookup(nm); b != nil && b.param != nil && len(b.param.Type.Enum) > 0 {
			members := make([]string, len(b.param.Type.Enum))
			found := false
			for i, m := range b.param.Type.Enum {
				members[i] = m.Name
				found = found || m.Name == name
			}
			if !found {
				d := diag.New(diag.CodeType, sp, "`%s` is not a member of `%s` (%s)", name, nm, strings.Join(members, ", "))
				if s, ok := registry.Suggest(name, members); ok {
					d = d.WithEdit("write "+s, sp, s)
				}
				r.report(d)
				return units.Bad(), true
			}
		}
	}
	out, err := units.Compare(n.Op, ov, units.Label(name), r.d)
	if err != nil {
		r.opError(err, n.Span())
		return units.Bad(), true
	}
	return out, true
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
