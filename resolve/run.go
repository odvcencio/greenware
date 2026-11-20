package resolve

import (
	"sort"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

const (
	stNew = iota
	stBusy
	stDone
)

// binding is a name that holds a value: a parameter, a let, a loop variable.
type binding struct {
	name  string
	span  diag.Span
	state int
	value units.Value
	param *ast.Param
	let   *ast.Let
	scope *scope // where the let's expression is evaluated
}

// scope is a lexical scope of values.
type scope struct {
	parent *scope
	names  map[string]*binding
	order  []string
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent, names: map[string]*binding{}}
}

func (s *scope) add(b *binding) { s.names[b.name] = b; s.order = append(s.order, b.name) }

func (s *scope) lookup(name string) *binding {
	for c := s; c != nil; c = c.parent {
		if b, ok := c.names[name]; ok {
			return b
		}
	}
	return nil
}

// visible lists the names a lookup can find, sorted, for suggestions.
func (s *scope) visible() []string {
	seen := map[string]bool{}
	var out []string
	for c := s; c != nil; c = c.parent {
		for _, n := range c.order {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

// run is one evaluation of a file.
type run struct {
	f        *ast.File
	syms     *symbols
	d        units.Defaults
	diags    []diag.Diagnostic
	quiet    int // > 0: evaluating a branch that is not taken; no domain errors
	globals  *scope
	stack    []*binding
	overs    map[string]*Override
	output   string
	resolved *Resolved
	// tstack is the chain of template calls being expanded.
	tstack []*ast.Template
	// libs are the imported libraries by qualifier; painters are the compiled
	// painters by key.
	libs     map[string]*library
	painters map[string]*pentry
}

func (r *run) report(d diag.Diagnostic) {
	if r.quiet > 0 && d.Code == diag.CodeDomain {
		return
	}
	r.diags = append(r.diags, d)
}

func evaluate(f *ast.File, output string, overrides []Override, shallowOutputs bool) (*Resolved, []diag.Diagnostic) {
	if f.Broken {
		return nil, nil
	}
	r := &run{f: f, d: units.DefaultUnits(), output: output, overs: map[string]*Override{}, libs: map[string]*library{}, painters: map[string]*pentry{}}
	r.resolved = &Resolved{Output: output}
	for i := range overrides {
		o := &overrides[i]
		if prev, dup := r.overs[o.Name]; dup {
			r.report(diag.New(diag.CodeDuplicate, nonEmpty(o.NameSpan, o.Span), "`%s` is overridden twice", o.Name).WithRelated(nonEmpty(prev.NameSpan, prev.Span), "first override"))
			continue
		}
		r.overs[o.Name] = o
	}
	r.syms = r.declare()
	r.assetUnits()
	r.bindGlobals()
	r.params()
	r.lets()
	r.assetFields()
	r.imports()
	r.checkPainters()
	r.textures()
	r.materials()
	r.plugs()
	r.parts()
	r.checkCells()
	r.lods()
	r.collision()
	r.checks()
	r.outputs()
	r.budgetAndLook()
	if shallowOutputs {
		r.checkOutputSets()
	}
	return r.resolved, diag.Dedup(r.diags)
}

func nonEmpty(a, b diag.Span) diag.Span {
	if a.Len() > 0 {
		return a
	}
	return b
}

// bindGlobals creates the document-level bindings for parameters and lets.
// Duplicates were reported by declare; the first declaration wins.
func (r *run) bindGlobals() {
	r.globals = newScope(nil)
	for _, p := range r.f.Params {
		if r.globals.names[p.Name.Name] != nil {
			continue
		}
		r.globals.add(&binding{name: p.Name.Name, span: p.Name.Span, param: p})
	}
	for _, l := range r.f.Lets {
		if r.globals.names[l.Name.Name] != nil {
			continue
		}
		r.globals.add(&binding{name: l.Name.Name, span: l.Name.Span, let: l, scope: r.globals})
	}
}

// force evaluates a binding on first use. A binding that is used while it is
// being evaluated closes a cycle.
func (r *run) force(b *binding) units.Value {
	switch b.state {
	case stDone:
		return b.value
	case stBusy:
		r.cycle(b)
		return units.Bad()
	}
	b.state = stBusy
	r.stack = append(r.stack, b)
	// A binding's own errors do not depend on the branch that reached it
	// first, so it is evaluated with domain errors on even when the caller is
	// checking a branch that is not taken.
	quiet := r.quiet
	r.quiet = 0
	defer func() { r.quiet = quiet }()
	switch {
	case b.param != nil:
		b.value = r.evalParam(b.param)
	case b.let != nil:
		v := r.eval(b.let.Value, b.scope)
		b.value = units.Finish(v)
	}
	r.stack = r.stack[:len(r.stack)-1]
	if b.state == stBusy {
		b.state = stDone
	}
	return b.value
}

// cycle reports a closed chain of dependencies that ends at b.
func (r *run) cycle(b *binding) {
	i := 0
	for i < len(r.stack) && r.stack[i] != b {
		i++
	}
	chain := r.stack[i:]
	names := ""
	for _, c := range chain {
		names += c.name + " -> "
	}
	names += b.name
	d := diag.New(diag.CodeCycle, b.span, "`%s` depends on itself: %s", b.name, names)
	for _, c := range chain {
		d = d.WithRelated(c.span, "`"+c.name+"` is part of the cycle")
	}
	r.report(d)
	for _, c := range chain {
		c.state = stDone
		c.value = units.Bad()
	}
}

// assetUnits reads the asset's default length and angle units first, because
// every bare literal depends on them.
func (r *run) assetUnits() {
	a := r.f.Asset
	if a == nil {
		return
	}
	if s := ast.FindSetting(a.Settings, "length"); s != nil {
		if n, ok := s.Value.(*ast.Name); ok {
			if sc, ok := units.LengthUnit(n.Name); ok {
				r.d.LengthScale = sc
			}
		}
	}
	if s := ast.FindSetting(a.Settings, "angle"); s != nil {
		if n, ok := s.Value.(*ast.Name); ok {
			if sc, ok := units.AngleUnit(n.Name); ok {
				r.d.AngleScale = sc
			}
		}
	}
}

// setKind finds a document-level kind.
func docKind(name string) *registry.Kind {
	k, _ := registry.Lookup(registry.CtxDocument, name)
	return k
}
