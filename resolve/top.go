package resolve

import (
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// paramType returns the slot type of a parameter and reports an unknown type.
func (r *run) paramType(p *ast.Param) (registry.Type, bool) {
	t, ok := registry.ParamType(p.Type.Name)
	if !ok {
		d := diag.New(diag.CodeUnknownName, p.Type.Span, "`%s` is not a parameter type", p.Type.Name)
		if s, ok := registry.Suggest(p.Type.Name, registry.ParamTypeNames); ok {
			d = d.WithEdit("write "+s, p.Type.Span, s)
		}
		r.report(d)
		return t, false
	}
	if t.Kind == registry.TEnum {
		seen := map[string]bool{}
		for _, m := range p.Type.Enum {
			if seen[m.Name] {
				r.report(diag.New(diag.CodeDuplicate, m.Span, "the enum member `%s` is listed twice", m.Name))
				continue
			}
			seen[m.Name] = true
			t.Enum = append(t.Enum, m.Name)
		}
		if len(t.Enum) == 0 {
			r.report(diag.New(diag.CodeMissing, p.Type.Span, "`enum` needs its members: enum(a, b)"))
			return t, false
		}
	}
	return t, true
}

// paramValue checks a value against a parameter's type.
func (r *run) paramValue(v units.Value, e ast.Expr, t registry.Type, sc *scope) units.Value {
	if t.Kind == registry.TEnum {
		name, sp, ok := nameOf(e)
		if !ok {
			r.report(diag.New(diag.CodeType, e.Span(), "expected one of the enum members: %s", strings.Join(t.Enum, ", ")))
			return units.Bad()
		}
		for _, m := range t.Enum {
			if m == name {
				return units.Label(name)
			}
		}
		d := diag.New(diag.CodeType, sp, "`%s` is not a member of the enum (%s)", name, strings.Join(t.Enum, ", "))
		if s, ok := registry.Suggest(name, t.Enum); ok {
			d = d.WithEdit("write "+s, sp, s)
		}
		r.report(d)
		return units.Bad()
	}
	return r.coerce(v, t, e.Span())
}

func (r *run) srcText(sp diag.Span) string { return string(r.f.Source[sp.Start:sp.End]) }

// checkRange tests a value against a parameter's bounds. A failure offers the
// nearest bound as an edit when the value is a source expression.
func (r *run) checkRange(p *ast.Param, v, lo, hi units.Value, sp diag.Span, editable bool, what string) {
	if !v.Valid() || v.Kind != units.KNumber || !lo.Valid() || !hi.Valid() {
		return
	}
	if v.Num >= lo.Num && v.Num <= hi.Num {
		return
	}
	d := diag.New(diag.CodeParamRange, sp, "%s %s is outside the range %s..%s of `%s`", what, v.Format(), lo.Format(), hi.Format(), p.Name.Name).
		WithRelated(p.Range.Span, "declared range")
	if editable {
		near, src := p.Range.Lo, "lower"
		if v.Num > hi.Num {
			near, src = p.Range.Hi, "upper"
		}
		d = d.WithEdit("use the "+src+" bound "+r.srcText(near.Span()), sp, r.srcText(near.Span()))
	}
	r.report(d)
}

// evalParam evaluates a parameter: its default, its range, and an override.
func (r *run) evalParam(p *ast.Param) units.Value {
	t, ok := r.paramType(p)
	if !ok {
		return units.Bad()
	}
	var lo, hi units.Value
	hasRange := p.Range != nil && t.Kind != registry.TEnum
	if hasRange {
		lo = r.paramBound(p, p.Range.Lo, t)
		hi = r.paramBound(p, p.Range.Hi, t)
		if lo.Valid() && hi.Valid() && lo.Num > hi.Num {
			r.report(diag.New(diag.CodeParamRange, p.Range.Span, "the range %s..%s of `%s` is empty", lo.Format(), hi.Format(), p.Name.Name))
			hasRange = false
		}
	}
	def := r.paramValue(r.eval(p.Default, r.globals), p.Default, t, r.globals)
	if def.Valid() && def.DependsOnRuntime() {
		r.report(diag.New(diag.CodeRuntime, p.Default.Span(), "the default of `%s` cannot depend on a runtime parameter", p.Name.Name))
		def = units.Bad()
	}
	if hasRange {
		r.checkRange(p, def, lo, hi, p.Default.Span(), true, "the default")
	}
	val := def
	if ov, ok := r.overs[p.Name.Name]; ok {
		var v units.Value
		sp := nonEmpty(ov.NameSpan, ov.Span)
		var e ast.Expr
		switch {
		case ov.Value != nil:
			v = *ov.Value
		case ov.Expr != nil:
			e = ov.Expr
			sp = ov.Expr.Span()
			v = r.eval(ov.Expr, r.globals)
		}
		if e != nil {
			v = r.paramValue(v, e, t, r.globals)
		} else if v.Valid() {
			v = r.coerce(v, t, sp)
			if t.Kind == registry.TEnum && v.Kind == units.KLabel && !contains(t.Enum, v.Str) {
				r.report(diag.New(diag.CodeType, sp, "`%s` is not a member of the enum (%s)", v.Str, strings.Join(t.Enum, ", ")))
				v = units.Bad()
			}
		}
		if hasRange {
			r.checkRange(p, v, lo, hi, sp, e != nil, "the value")
		}
		if v.Valid() {
			val = v
		} else {
			val = v
		}
	}
	if val.Valid() && p.Runtime {
		val.Runtime = true
	}
	return val
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func (r *run) paramBound(p *ast.Param, e ast.Expr, t registry.Type) units.Value {
	v := r.coerce(r.eval(e, r.globals), t, e.Span())
	if v.Valid() && v.DependsOnRuntime() {
		r.report(diag.New(diag.CodeRuntime, e.Span(), "the range of `%s` cannot depend on a runtime parameter", p.Name.Name))
		return units.Bad()
	}
	return v
}

// params evaluates every parameter in declaration order.
func (r *run) params() {
	for _, p := range r.f.Params {
		b := r.globals.names[p.Name.Name]
		if b == nil || b.param != p {
			continue
		}
		v := r.force(b)
		out := Param{Name: p.Name.Name, Type: p.Type.Name, Runtime: p.Runtime, Value: v}
		if t, ok := registry.ParamType(p.Type.Name); ok && t.Kind == registry.TEnum {
			out.Type = "enum"
		}
		if p.Range != nil {
			if t, ok := registry.ParamType(p.Type.Name); ok && t.Kind != registry.TEnum {
				lo := r.quietEval(func() units.Value { return r.coerce(r.eval(p.Range.Lo, r.globals), t, p.Range.Lo.Span()) })
				hi := r.quietEval(func() units.Value { return r.coerce(r.eval(p.Range.Hi, r.globals), t, p.Range.Hi.Span()) })
				if lo.Valid() && hi.Valid() {
					out.Lo, out.Hi = &lo, &hi
				}
			}
		}
		if s := ast.FindSetting(p.Settings, "doc"); s != nil {
			if lit, ok := s.Value.(*ast.StringLit); ok {
				out.Doc = lit.Value
			}
		}
		_, out.Overridden = r.overs[p.Name.Name]
		r.resolved.Params = append(r.resolved.Params, out)
	}
	// An override that names no parameter.
	var names []string
	for _, p := range r.f.Params {
		names = append(names, p.Name.Name)
	}
	for _, o := range r.sortedOverrides() {
		if !contains(names, o.Name) {
			sp := nonEmpty(o.NameSpan, o.Span)
			d := diag.New(diag.CodeUnknownName, sp, "`%s` is not a parameter", o.Name)
			if s, ok := registry.Suggest(o.Name, names); ok && o.NameSpan.Len() > 0 {
				d = d.WithEdit("write "+s, sp, s)
			}
			r.report(d)
		}
	}
}

// quietEval runs fn and drops the diagnostics it makes. It is for values that
// were already checked and reported once.
func (r *run) quietEval(fn func() units.Value) units.Value {
	n := len(r.diags)
	v := fn()
	r.diags = r.diags[:n]
	return v
}

func (r *run) sortedOverrides() []*Override {
	var out []*Override
	for _, v := range r.overs {
		out = append(out, v)
	}
	// Order by name for deterministic reporting.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Name < out[j-1].Name; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// lets evaluates every top-level let. A let may read one declared later.
func (r *run) lets() {
	for _, l := range r.f.Lets {
		b := r.globals.names[l.Name.Name]
		if b == nil || b.let != l {
			continue
		}
		r.resolved.Lets = append(r.resolved.Lets, Let{Name: l.Name.Name, Value: r.force(b)})
	}
}

func (r *run) assetFields() {
	a := r.f.Asset
	if a == nil {
		return
	}
	k := docKind("asset")
	r.resolved.Asset = r.fields(k, a.Settings, r.globals, a.Name.Span)
	if rs := ast.FindSetting(a.Settings, "root"); rs != nil {
		if v, ok := field(r.resolved.Asset, "root"); ok && v.Valid() {
			if d := r.syms.lookup(registry.NsGeometry, v.Str); d != nil && !d.topLevel {
				r.report(diag.New(diag.CodeType, rs.Value.Span(), "the root must be a top-level part, but `%s` is nested", v.Str))
			}
		}
	}
	up, uok := field(r.resolved.Asset, "up")
	fw, fok := field(r.resolved.Asset, "forward")
	if uok && fok && up.Valid() && fw.Valid() && !perpendicularAxes(up.Str, fw.Str) {
		sp := a.Name.Span
		if s := ast.FindSetting(a.Settings, "forward"); s != nil {
			sp = s.Span
		}
		r.report(diag.New(diag.CodeFieldRange, sp, "`up` (%s) and `forward` (%s) must be perpendicular axes", up.Str, fw.Str))
	}
	if m := r.f.Meta; m != nil {
		r.resolved.Meta = r.fields(docKind("meta"), m.Settings, r.globals, m.Span)
	}
}

func (r *run) materials() {
	for _, m := range r.f.Materials {
		r.resolved.Materials = append(r.resolved.Materials, r.material(m))
	}
}

func (r *run) material(m *ast.Material) *Node {
	n := &Node{Kind: m.Kind, Name: m.Name.Name}
	k := docKind(m.Kind)
	if k == nil {
		return n
	}
	n.Fields = r.fields(k, m.Settings, r.globals, m.KindSpan)
	if m.Kind == "selena" {
		if m.Fallback == nil {
			r.report(diag.New(diag.CodeMissing, m.KindSpan, "a selena material needs `fallback = pbr { ... }`, the material every glTF viewer draws"))
		} else {
			fb := r.material(m.Fallback)
			fb.Name = "fallback"
			n.Children = append(n.Children, fb)
		}
	}
	return n
}

// checks evaluates the require rows of `check params`.
func (r *run) checks() {
	for _, c := range r.f.Checks {
		for _, req := range c.Requires {
			v := r.eval(req.Cond, r.globals)
			if !v.Valid() {
				continue
			}
			if v.Kind != units.KSwitch {
				r.report(diag.New(diag.CodeType, req.Cond.Span(), "a requirement must be a switch, found a %s", v.Describe()))
				continue
			}
			if v.DependsOnRuntime() {
				r.report(diag.New(diag.CodeRuntime, req.Cond.Span(), "a requirement cannot depend on a runtime parameter"))
				continue
			}
			if !v.Bool {
				msg := ""
				if lit, ok := req.Message.(*ast.StringLit); ok {
					msg = lit.Value
				}
				r.report(diag.New(diag.CodeRequire, req.Span, "%s%s", msg, r.valuesRead(req.Cond)))
			}
		}
	}
}

// valuesRead lists the named values an expression reads, with their values.
func (r *run) valuesRead(e ast.Expr) string {
	var names []string
	var walk func(e ast.Expr)
	add := func(n string) {
		if !contains(names, n) {
			names = append(names, n)
		}
	}
	walk = func(e ast.Expr) {
		switch n := e.(type) {
		case *ast.Name:
			add(n.Name)
		case *ast.Qualified:
			add(n.Parts[0])
		case *ast.Paren:
			walk(n.X)
		case *ast.Unary:
			walk(n.X)
		case *ast.Binary:
			walk(n.L)
			walk(n.R)
		case *ast.If:
			walk(n.Cond)
			walk(n.Then)
			walk(n.Else)
		case *ast.Member:
			walk(n.X)
		case *ast.Index:
			walk(n.X)
			walk(n.I)
		case *ast.Tuple:
			for _, x := range n.Elems {
				walk(x)
			}
		case *ast.Call:
			for _, a := range n.Args {
				if a.Value != nil {
					walk(a.Value)
				}
			}
		}
	}
	walk(e)
	var parts []string
	for _, n := range names {
		if b := r.globals.lookup(n); b != nil && b.state == stDone && b.value.Valid() {
			parts = append(parts, n+" = "+b.value.Format())
		}
	}
	if len(parts) == 0 {
		return ""
	}
	out := " (" + strings.Join(parts, ", ")
	if r.output != "" {
		out += "; output " + r.output
	}
	return out + ")"
}

// outputs evaluates each output declaration's own fields.
func (r *run) outputs() {
	k := docKind("output")
	for _, o := range r.f.Outputs {
		fs := r.fields(k, o.Settings, r.globals, o.Name.Span)
		out := Output{Name: o.Name.Name}
		if v, ok := field(fs, "file"); ok && v.Valid() {
			out.File = v.Str
		}
		if v, ok := field(fs, "lods"); ok {
			out.LODs = v.Str
		}
		if v, ok := field(fs, "manifest"); ok {
			out.Manifest = v.Bool
		}
		if v, ok := field(fs, "profile"); ok {
			out.Profile = v.Str
		}
		for _, s := range o.Set {
			if s.Value == nil {
				continue
			}
			out.Set = append(out.Set, Field{Name: s.Name, Value: r.quietEval(func() units.Value { return r.eval(s.Value, r.globals) })})
		}
		if o.Budget != nil {
			out.Budget = r.fields(docKind("budget"), o.Budget.Settings, r.globals, o.Budget.Span)
		}
		r.resolved.Outputs = append(r.resolved.Outputs, out)
	}
}

// checkOutputSets validates the `set` block of every output against the
// parameters (name, type, range) without evaluating the output's geometry.
func (r *run) checkOutputSets() {
	for _, o := range r.f.Outputs {
		if len(o.Set) == 0 {
			continue
		}
		sub := &run{f: r.f, syms: r.syms, d: r.d, overs: map[string]*Override{}, output: o.Name.Name, resolved: &Resolved{}}
		ovs := setOverrides(o)
		for i := range ovs {
			if _, dup := sub.overs[ovs[i].Name]; dup {
				continue // already reported by the output's own run
			}
			sub.overs[ovs[i].Name] = &ovs[i]
		}
		sub.bindGlobals()
		sub.params()
		r.diags = append(r.diags, sub.diags...)
	}
}
