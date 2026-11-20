package resolve

import (
	"strconv"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// scalar converts v to the number type of t and returns it, reporting a
// unit or type error at sp.
func (r *run) scalar(v units.Value, dim units.Dim, sp diag.Span) units.Value {
	if !v.Valid() {
		return v
	}
	out, err := units.Coerce(v, dim, r.d)
	if err != nil {
		r.opError(err, sp)
		return units.Bad()
	}
	return out
}

func (r *run) triple(v units.Value, dim units.Dim, sp diag.Span, what string) units.Value {
	if !v.Valid() {
		return v
	}
	if v.Kind != units.KTuple || len(v.Elems) != 3 {
		r.report(diag.New(diag.CodeType, sp, "expected %s, found a %s", what, v.Describe()))
		return units.Bad()
	}
	out := make([]units.Value, 3)
	for i, e := range v.Elems {
		out[i] = r.scalar(e, dim, sp)
		if !out[i].Valid() {
			return units.Bad()
		}
	}
	t := units.Tuple(out...)
	t.Runtime = v.Runtime
	return t
}

// coerce checks an evaluated value against a numeric, switch, colour, string
// or tuple type.
func (r *run) coerce(v units.Value, t registry.Type, sp diag.Span) units.Value {
	if !v.Valid() {
		return v
	}
	switch t.Kind {
	case registry.TLength:
		return r.scalar(v, units.Length, sp)
	case registry.TAngle:
		return r.scalar(v, units.Angle, sp)
	case registry.TDuration:
		return r.scalar(v, units.Duration, sp)
	case registry.TBytes:
		return r.scalar(v, units.Bytes, sp)
	case registry.TNumber:
		return r.scalar(v, units.Number, sp)
	case registry.TInteger:
		out := r.scalar(v, units.Number, sp)
		if out.Valid() && !units.IsIntegral(out.Num) {
			r.report(diag.New(diag.CodeType, sp, "expected a whole number, found %s", out.Format()))
			return units.Bad()
		}
		return out
	case registry.TSwitch:
		if v.Kind != units.KSwitch {
			r.report(diag.New(diag.CodeType, sp, "expected a switch (on or off), found a %s", v.Describe()))
			return units.Bad()
		}
	case registry.TColor:
		if v.Kind != units.KColor {
			r.report(diag.New(diag.CodeType, sp, "expected a colour such as #1d4e89, found a %s", v.Describe()))
			return units.Bad()
		}
	case registry.TString:
		if v.Kind != units.KString {
			r.report(diag.New(diag.CodeType, sp, "expected a string, found a %s", v.Describe()))
			return units.Bad()
		}
	case registry.TStringList:
		ok := v.Kind == units.KList
		for _, e := range v.Elems {
			ok = ok && e.Kind == units.KString
		}
		if !ok {
			r.report(diag.New(diag.CodeType, sp, "expected a list of strings, found a %s", v.Describe()))
			return units.Bad()
		}
	case registry.TPoint, registry.TExtent:
		return r.triple(v, units.Length, sp, "three lengths")
	case registry.TRotation:
		return r.triple(v, units.Angle, sp, "three angles")
	case registry.TScale:
		out := r.triple(v, units.Number, sp, "three numbers")
		for _, e := range out.Elems {
			if e.Num <= 0 {
				r.report(diag.New(diag.CodeFieldRange, sp, "a scale must be greater than zero in every component, found %s", out.Format()))
				return units.Bad()
			}
		}
		return out
	case registry.TCount:
		if v.Kind == units.KTuple {
			out := r.triple(v, units.Number, sp, "three whole numbers")
			for _, e := range out.Elems {
				if !units.IsIntegral(e.Num) || e.Num < 1 {
					r.report(diag.New(diag.CodeFieldRange, sp, "a grid count must be three whole numbers of at least 1, found %s", out.Format()))
					return units.Bad()
				}
			}
			return out
		}
		out := r.coerce(v, registry.Type{Kind: registry.TInteger}, sp)
		if out.Valid() && out.Num < 1 {
			r.report(diag.New(diag.CodeFieldRange, sp, "a count must be at least 1, found %s", out.Format()))
			return units.Bad()
		}
		return out
	}
	return v
}

// bounds checks a value against a field's limits.
func (r *run) bounds(f *registry.Field, v units.Value, sp diag.Span) units.Value {
	if !v.Valid() || v.Kind != units.KNumber {
		return v
	}
	switch {
	case f.Positive && !(v.Num > 0):
		r.report(diag.New(diag.CodeFieldRange, sp, "`%s` must be greater than zero, found %s", f.Name, v.Format()))
		return units.Bad()
	case f.NonNegative && v.Num < 0:
		r.report(diag.New(diag.CodeFieldRange, sp, "`%s` must not be negative, found %s", f.Name, v.Format()))
		return units.Bad()
	case f.Min != nil && v.Num < *f.Min:
		r.report(diag.New(diag.CodeFieldRange, sp, "`%s` must be at least %s, found %s", f.Name, num(*f.Min), v.Format()))
		return units.Bad()
	case f.Max != nil && v.Num > *f.Max:
		r.report(diag.New(diag.CodeFieldRange, sp, "`%s` must be at most %s, found %s", f.Name, num(*f.Max), v.Format()))
		return units.Bad()
	}
	return v
}

func num(x float64) string { return strconv.FormatFloat(x, 'g', 9, 64) }

func perpendicularAxes(a, b string) bool { return a[1] != b[1] }

// nameOf returns the identifier an expression is, if it is one.
func nameOf(e ast.Expr) (string, diag.Span, bool) {
	if n, ok := e.(*ast.Name); ok {
		return n.Name, n.Span(), true
	}
	return "", diag.Span{}, false
}

func (r *run) axis(e ast.Expr) units.Value {
	sign := "+"
	x := e
	if u, ok := e.(*ast.Unary); ok && (u.Op == "-" || u.Op == "+") {
		x, sign = u.X, u.Op
	}
	if name, sp, ok := nameOf(x); ok {
		switch name {
		case "x", "y", "z":
			return units.Axis(sign + name)
		}
		d := diag.New(diag.CodeType, sp, "`%s` is not an axis: use x, y or z, with an optional sign", name)
		if s, ok := registry.Suggest(name, []string{"x", "y", "z"}); ok {
			d = d.WithEdit("write "+s, sp, s)
		}
		r.report(d)
		return units.Bad()
	}
	r.report(diag.New(diag.CodeType, e.Span(), "expected an axis such as y or -z"))
	return units.Bad()
}

func (r *run) refValue(e ast.Expr, ns registry.Namespace) units.Value {
	name, sp, ok := nameOf(e)
	if !ok {
		r.report(diag.New(diag.CodeType, e.Span(), "expected the name of a %s", ns))
		return units.Bad()
	}
	if r.syms.lookup(ns, name) == nil {
		d := diag.New(diag.CodeUnknownName, sp, "no %s is named `%s`", ns, name)
		if s, ok := registry.Suggest(name, r.syms.names(ns)); ok {
			d = d.WithEdit("write "+s, sp, s)
		}
		r.report(d)
		return units.Bad()
	}
	return units.Ref(name)
}

// slot evaluates a setting against its registry field.
func (r *run) slot(sc *scope, f *registry.Field, s *ast.Setting) units.Value {
	t := f.Type
	vsp := s.Span
	if s.Value != nil {
		vsp = s.Value.Span()
	}
	if t.Kind == registry.TAngleRange {
		if s.Range == nil {
			r.report(diag.New(diag.CodeType, vsp, "`%s` takes a range written lo..hi", f.Name))
			return units.Bad()
		}
		lo := r.scalar(r.eval(s.Range.Lo, sc), units.Angle, s.Range.Lo.Span())
		hi := r.scalar(r.eval(s.Range.Hi, sc), units.Angle, s.Range.Hi.Span())
		if !lo.Valid() || !hi.Valid() {
			return units.Bad()
		}
		if lo.Num > hi.Num {
			r.report(diag.New(diag.CodeFieldRange, s.Range.Span, "the range %s..%s is empty", lo.Format(), hi.Format()))
			return units.Bad()
		}
		pair := units.Pair(lo, hi)
		if pair.DependsOnRuntime() && !f.Runtime {
			r.report(diag.New(diag.CodeRuntime, s.Range.Span, "`%s` depends on a runtime parameter, but this field must be known when the file compiles", f.Name))
			return units.Bad()
		}
		return pair
	}
	if s.Value == nil {
		r.report(diag.New(diag.CodeType, vsp, "`%s` takes a %s, not a range", f.Name, t.Name()))
		return units.Bad()
	}
	e := s.Value
	var v units.Value
	switch t.Kind {
	case registry.TAxis:
		v = r.axis(e)
	case registry.TEnum:
		name, sp, ok := nameOf(e)
		if b, isBool := e.(*ast.BoolLit); isBool {
			// `off` and `on` are switch words, and also members of an enum
			// such as `caps = off`.
			name, sp, ok = "off", e.Span(), true
			if b.Value {
				name = "on"
			}
		}
		if !ok {
			r.report(diag.New(diag.CodeType, e.Span(), "`%s` takes %s", f.Name, t.Name()))
			return units.Bad()
		}
		for _, m := range t.Enum {
			if m == name {
				return units.Label(name)
			}
		}
		d := diag.New(diag.CodeType, sp, "`%s` is not a value of `%s`: it takes %s", name, f.Name, t.Name())
		if sug, ok := registry.Suggest(name, t.Enum); ok {
			d = d.WithEdit("write "+sug, sp, sug)
		}
		r.report(d)
		return units.Bad()
	case registry.TLabel:
		name, _, ok := nameOf(e)
		if !ok {
			r.report(diag.New(diag.CodeType, e.Span(), "`%s` takes a plain name", f.Name))
			return units.Bad()
		}
		return units.Label(name)
	case registry.TRef:
		return r.refValue(e, t.Ns)
	case registry.TRefList:
		l, ok := e.(*ast.List)
		if !ok {
			r.report(diag.New(diag.CodeType, e.Span(), "`%s` takes a list of names, such as [a, b]", f.Name))
			return units.Bad()
		}
		out := make([]units.Value, len(l.Elems))
		for i, el := range l.Elems {
			out[i] = r.refValue(el, t.Ns)
			if !out[i].Valid() {
				return units.Bad()
			}
		}
		return units.List(out...)
	case registry.TProfile:
		v = r.profile(e, sc)
	case registry.TNames:
		v = r.names(e)
	case registry.TExpr:
		// Kept as written and evaluated where it applies. The unit scales are
		// part of its meaning, because its bare numbers take them.
		return units.Str(exprText(e) + " @" + strconv.FormatFloat(r.d.LengthScale, 'g', -1, 64) + "," + strconv.FormatFloat(r.d.AngleScale, 'g', -1, 64))
	default:
		v = r.coerce(r.eval(e, sc), t, vsp)
	}
	if !v.Valid() {
		return v
	}
	if v.DependsOnRuntime() && !f.Runtime {
		r.report(diag.New(diag.CodeRuntime, vsp, "`%s` depends on a runtime parameter, but this field must be known when the file compiles", f.Name))
		return units.Bad()
	}
	return r.bounds(f, v, vsp)
}

// profile evaluates circle(r, n).
func (r *run) profile(e ast.Expr, sc *scope) units.Value {
	c, ok := e.(*ast.Call)
	if !ok || c.Func != "circle" {
		r.report(diag.New(diag.CodeType, e.Span(), "a profile is written circle(radius, segments)"))
		return units.Bad()
	}
	if len(c.Args) != 2 || c.Args[0].Value == nil || c.Args[1].Value == nil || c.Args[0].Name != "" || c.Args[1].Name != "" {
		r.report(diag.New(diag.CodeType, c.Span(), "circle takes two arguments: circle(radius, segments)"))
		return units.Bad()
	}
	rad := r.scalar(r.eval(c.Args[0].Value, sc), units.Length, c.Args[0].Value.Span())
	n := r.coerce(r.eval(c.Args[1].Value, sc), registry.Type{Kind: registry.TInteger}, c.Args[1].Value.Span())
	if !rad.Valid() || !n.Valid() {
		return units.Bad()
	}
	if rad.DependsOnRuntime() || n.DependsOnRuntime() {
		r.report(diag.New(diag.CodeRuntime, c.Span(), "a profile cannot depend on a runtime parameter"))
		return units.Bad()
	}
	if !(rad.Num > 0) {
		r.report(diag.New(diag.CodeFieldRange, c.Args[0].Value.Span(), "a circle needs a positive radius, found %s", rad.Format()))
		return units.Bad()
	}
	if n.Num < 3 {
		r.report(diag.New(diag.CodeFieldRange, c.Args[1].Value.Span(), "a circle needs at least 3 segments, found %s", n.Format()))
		return units.Bad()
	}
	return units.Value{Kind: units.KProfile, Str: "circle", Elems: []units.Value{rad, n}}
}

// fields evaluates the settings of a body against kind k, in registry order.
// A missing required field is reported at owner. Fields that are absent and
// have a default appear with Default set.
func (r *run) fields(k *registry.Kind, settings []*ast.Setting, sc *scope, owner diag.Span) []Field {
	var out []Field
	for i := range k.Fields {
		f := &k.Fields[i]
		if f.Unsupported != "" || (k.Name == "selena" && f.Name == "fallback") {
			continue
		}
		s := ast.FindSetting(settings, f.Name)
		switch {
		case s != nil:
			out = append(out, Field{Name: f.Name, Value: r.slot(sc, f, s)})
		case f.Required:
			r.report(diag.New(diag.CodeMissing, owner, "`%s` needs the field `%s`", k.Name, f.Name))
		case f.Default != nil:
			out = append(out, Field{Name: f.Name, Value: *f.Default, Default: true})
		}
	}
	if k.Open {
		for _, s := range settings {
			if _, known := k.Field(s.Name); known || s.Value == nil {
				continue
			}
			out = append(out, Field{Name: s.Name, Value: units.Finish(r.eval(s.Value, sc))})
		}
	}
	return out
}

func field(fs []Field, name string) (units.Value, bool) {
	for _, f := range fs {
		if f.Name == name {
			return f.Value, true
		}
	}
	return units.Bad(), false
}

// rows evaluates the rows of a body, expanding sampled rows.
func (r *run) rows(items []ast.RowItem, k *registry.Kind, sc *scope) ([]Row, bool) {
	var out []Row
	all := true
	for _, it := range items {
		switch n := it.(type) {
		case *ast.Row:
			if row, ok := r.row(n, k, sc); ok {
				out = append(out, row)
			} else {
				all = false
			}
		case *ast.SampleRow:
			rows, ok := r.sample(n, k, sc)
			out = append(out, rows...)
			all = all && ok
		}
	}
	return out, all
}

func (r *run) row(n *ast.Row, k *registry.Kind, sc *scope) (Row, bool) {
	spec, ok := k.Row(n.Name)
	if !ok {
		return Row{}, false
	}
	if len(n.Args) != len(spec.Columns) {
		r.report(diag.New(diag.CodeType, n.Span, "`%s` takes %d values, found %d", n.Name, len(spec.Columns), len(n.Args)))
		return Row{}, false
	}
	row := Row{Name: n.Name}
	good := true
	for i, a := range n.Args {
		col := spec.Columns[i]
		if a.Name != "" || a.Value == nil {
			r.report(diag.New(diag.CodeType, a.Span, "`%s` takes plain positional values", n.Name))
			good = false
			continue
		}
		v := r.coerce(r.eval(a.Value, sc), col.Type, a.Value.Span())
		if v.Valid() && v.DependsOnRuntime() {
			r.report(diag.New(diag.CodeRuntime, a.Value.Span(), "a `%s` value cannot depend on a runtime parameter", n.Name))
			v = units.Bad()
		}
		if v.Valid() && col.NonNegative && v.Num < 0 {
			r.report(diag.New(diag.CodeFieldRange, a.Value.Span(), "`%s` of a `%s` row must not be negative, found %s", col.Name, n.Name, v.Format()))
			v = units.Bad()
		}
		good = good && v.Valid()
		row.Values = append(row.Values, v)
	}
	return row, good
}

// maxSamples is the most rows one sampled row may make.
const maxSamples = 4096

func (r *run) sample(n *ast.SampleRow, k *registry.Kind, sc *scope) ([]Row, bool) {
	if n.Range == nil || n.Row == nil {
		return nil, false
	}
	lo, hi := r.eval(n.Range.Lo, sc), r.eval(n.Range.Hi, sc)
	cnt := r.coerce(r.eval(n.Count, sc), registry.Type{Kind: registry.TInteger}, n.Count.Span())
	if !lo.Valid() || !hi.Valid() || !cnt.Valid() {
		return nil, false
	}
	// The count and the positions of sampled rows are fixed when the file
	// compiles (spec 2.7).
	if lo.DependsOnRuntime() || hi.DependsOnRuntime() {
		r.report(diag.New(diag.CodeRuntime, n.Range.Span, "the range of a sampled row cannot depend on a runtime parameter"))
		return nil, false
	}
	if cnt.DependsOnRuntime() {
		r.report(diag.New(diag.CodeRuntime, n.Count.Span(), "the count of a sampled row cannot depend on a runtime parameter"))
		return nil, false
	}
	if lo.Kind != units.KNumber || hi.Kind != units.KNumber {
		r.report(diag.New(diag.CodeType, n.Range.Span, "a sample range needs numbers"))
		return nil, false
	}
	dim := lo.Dim
	if lo.Pending {
		dim = hi.Dim
		if hi.Pending {
			dim = units.Number
		}
	}
	lo, hi = r.d.Fix(lo, dim), r.d.Fix(hi, dim)
	if lo.Dim != hi.Dim {
		r.report(diag.New(diag.CodeUnit, n.Range.Span, "the ends of a sample range must have the same dimension, found a %s and a %s", lo.Dim, hi.Dim))
		return nil, false
	}
	if lo.Num > hi.Num {
		r.report(diag.New(diag.CodeFieldRange, n.Range.Span, "the range %s..%s is empty", lo.Format(), hi.Format()))
		return nil, false
	}
	if cnt.Num < 2 || cnt.Num > maxSamples {
		r.report(diag.New(diag.CodeFieldRange, n.Count.Span(), "a sampled row makes 2 to %d rows, found %s", maxSamples, cnt.Format()))
		return nil, false
	}
	count := int(cnt.Num)
	var out []Row
	all := true
	span := hi.Num - lo.Num
	for i := 0; i < count; i++ {
		var a float64
		switch i {
		case 0:
			a = lo.Num
		case count - 1:
			a = hi.Num
		default:
			a = lo.Num + float64(float64(span*float64(i))/float64(count-1))
		}
		inner := newScope(sc)
		inner.add(&binding{name: n.Var.Name, span: n.Var.Span, state: stDone, value: units.Value{Kind: units.KNumber, Dim: dim, Num: a}})
		if row, ok := r.row(n.Row, k, inner); ok {
			out = append(out, row)
		} else {
			all = false
		}
	}
	return out, all
}
