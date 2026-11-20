package resolve

import (
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// maxTemplateDepth bounds nested template calls; a cycle is reported on its own.
const maxTemplateDepth = 16

// formalType reads the type written after a template formal.
func (r *run) formalType(pt ast.ParamType) (registry.Type, bool) {
	t, ok := registry.ParamType(pt.Name)
	if !ok {
		d := diag.New(diag.CodeUnknownName, pt.Span, "`%s` is not a parameter type", pt.Name)
		if s, ok := registry.Suggest(pt.Name, registry.ParamTypeNames); ok {
			d = d.WithEdit("write "+s, pt.Span, s)
		}
		r.report(d)
		return t, false
	}
	if t.Kind == registry.TEnum {
		for _, m := range pt.Enum {
			t.Enum = append(t.Enum, m.Name)
		}
		if len(t.Enum) == 0 {
			r.report(diag.New(diag.CodeMissing, pt.Span, "`enum` needs its members: enum(a, b)"))
			return t, false
		}
	}
	return t, true
}

// formalValue checks an argument value against a formal's type.
func (r *run) formalValue(e ast.Expr, v units.Value, t registry.Type, sp diag.Span) units.Value {
	if t.Kind == registry.TEnum {
		name, nsp, ok := nameOf(e)
		if ok {
			for _, m := range t.Enum {
				if m == name {
					return units.Label(name)
				}
			}
			r.report(diag.New(diag.CodeType, nsp, "`%s` is not a member of the enum (%s)", name, strings.Join(t.Enum, ", ")))
			return units.Bad()
		}
		r.report(diag.New(diag.CodeType, sp, "expected one of the enum members: %s", strings.Join(t.Enum, ", ")))
		return units.Bad()
	}
	return r.coerce(v, t, sp)
}

// templateCall expands a row that calls a template (language.md section 3.8).
// Arguments bind to the typed formals, positional ones first and then named
// ones, each formal once. The body is read in the definition's scope, so its
// names are the document's and the formals; what it leaves unset is filled
// from where it lands. The call makes no node of its own.
func (r *run) templateCall(c *ast.TemplateCall, sc *scope) []*Node {
	if strings.Contains(c.Name, ".") {
		r.report(diag.New(diag.CodeUnsupported, c.NameSpan, "the qualified template `%s` is valid in the language but outside this compiler's subset", c.Name))
		return nil
	}
	t := r.syms.templates[c.Name]
	if t == nil {
		d := diag.New(diag.CodeUnknownName, c.NameSpan, "no template is named `%s`", c.Name)
		var names []string
		for _, tt := range r.f.Templates {
			names = append(names, tt.Name.Name)
		}
		if s, ok := registry.Suggest(c.Name, names); ok {
			d = d.WithEdit("write "+s, c.NameSpan, s)
		}
		r.report(d)
		return nil
	}
	for i, active := range r.tstack {
		if active == t {
			chain := ""
			for _, a := range r.tstack[i:] {
				chain += a.Name.Name + " -> "
			}
			r.report(diag.New(diag.CodeCycle, c.NameSpan, "templates call each other in a cycle: %s%s", chain, t.Name.Name).
				WithRelated(t.Name.Span, "`"+t.Name.Name+"` is declared here"))
			return nil
		}
	}
	if len(r.tstack) >= maxTemplateDepth {
		r.report(diag.New(diag.CodeLimit, c.NameSpan, "templates are nested more than %d calls deep", maxTemplateDepth))
		return nil
	}
	// Bind the arguments to the formals.
	bound := make([]units.Value, len(t.Params))
	exprs := make([]ast.Expr, len(t.Params))
	have := make([]bool, len(t.Params))
	ok := true
	named := false
	pos := 0
	for _, a := range c.Args {
		if a.Value == nil {
			r.report(diag.New(diag.CodeType, a.Span, "a template argument is a single value, not a range"))
			ok = false
			continue
		}
		idx := -1
		if a.Name == "" {
			if named {
				r.report(diag.New(diag.CodeType, a.Span, "positional arguments come before named ones"))
				ok = false
				continue
			}
			idx = pos
			pos++
			if idx >= len(t.Params) {
				r.report(diag.New(diag.CodeFieldRange, a.Span, "`%s` takes %d arguments, found more", t.Name.Name, len(t.Params)).WithRelated(t.Name.Span, "declared here"))
				ok = false
				continue
			}
		} else {
			named = true
			for i, p := range t.Params {
				if p.Name.Name == a.Name {
					idx = i
				}
			}
			if idx < 0 {
				names := make([]string, len(t.Params))
				for i, p := range t.Params {
					names[i] = p.Name.Name
				}
				d := diag.New(diag.CodeUnknownField, a.NameSpan, "`%s` has no argument `%s`", t.Name.Name, a.Name)
				if s, ok := registry.Suggest(a.Name, names); ok {
					d = d.WithEdit("write "+s, a.NameSpan, s)
				}
				r.report(d)
				ok = false
				continue
			}
		}
		if have[idx] {
			r.report(diag.New(diag.CodeDuplicate, a.Span, "the argument `%s` is given twice", t.Params[idx].Name.Name))
			ok = false
			continue
		}
		have[idx] = true
		exprs[idx] = a.Value
		bound[idx] = r.eval(a.Value, sc)
	}
	// The body's own scope: the document's names and the formals.
	inner := newScope(r.globals)
	for i, p := range t.Params {
		ft, tok := r.formalType(p.Type)
		var v units.Value
		switch {
		case have[i]:
			if tok && bound[i].Valid() {
				v = r.formalValue(exprs[i], bound[i], ft, exprs[i].Span())
			} else if tok && ft.Kind == registry.TEnum {
				v = r.formalValue(exprs[i], bound[i], ft, exprs[i].Span())
			}
		case p.Default != nil:
			if tok {
				dv := units.Bad()
				if ft.Kind != registry.TEnum {
					dv = r.eval(p.Default, inner)
				}
				v = r.formalValue(p.Default, dv, ft, p.Default.Span())
			}
		default:
			r.report(diag.New(diag.CodeMissing, c.Span, "`%s` needs the argument `%s`", t.Name.Name, p.Name.Name).WithRelated(p.Name.Span, "declared here"))
			ok = false
		}
		if !v.Valid() {
			ok = false
		}
		inner.add(&binding{name: p.Name.Name, span: p.Name.Span, state: stDone, value: v})
	}
	if !ok {
		return nil
	}
	r.tstack = append(r.tstack, t)
	defer func() { r.tstack = r.tstack[:len(r.tstack)-1] }()
	bs := r.bodyScope(inner, t.Items)
	return r.items(t.Items, bs)
}
