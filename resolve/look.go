package resolve

import (
	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

const noMaterial = "(default)"

type lookState struct {
	defs   map[string]*Node
	draws  map[[2]string]bool
	mats   map[string]bool
	nodeID int
	worst  bool
	depth  int
}

func isShapeKind(kind string) bool {
	k, ok := registry.Lookup(registry.CtxPart, kind)
	return ok && k.IsShape
}

// walk counts draws. A draw is one material on one node: the root and every
// exported part are nodes, and a part that is not exported merges its shapes
// into its nearest exported ancestor.
func (ls *lookState) walk(n *Node, node int, inherited string) {
	if ls.depth > 64 {
		return
	}
	ls.depth++
	defer func() { ls.depth-- }()
	switch {
	case n.Kind == "part":
		mat := inherited
		if f, ok := n.Get("material"); ok && f.Valid() {
			mat = f.Str
		}
		if e, ok := n.Get("export"); ok && e.Bool {
			ls.nodeID++
			node = ls.nodeID
		}
		for _, c := range n.Children {
			ls.walk(c, node, mat)
		}
	case isShapeKind(n.Kind):
		mat := inherited
		if f, ok := n.Get("material"); ok && f.Valid() {
			mat = f.Str
		}
		if mat == "" {
			mat = noMaterial
		}
		ls.draws[[2]string{itoa(node), mat}] = true
		if mat != noMaterial {
			ls.mats[mat] = true
		}
	case n.Kind == "array" || n.Kind == "mirror":
		for _, c := range n.Children {
			ls.walk(c, node, inherited)
		}
	case n.Kind == "when":
		take := n.Children
		if n.Condition != nil && !n.Condition.Bool {
			take = n.Else
		}
		if ls.worst {
			take = append(append([]*Node(nil), n.Children...), n.Else...)
		}
		for _, c := range take {
			ls.walk(c, node, inherited)
		}
	case n.Kind == "instance":
		if of, ok := n.Get("of"); ok && of.Valid() {
			if def := ls.defs[of.Str]; def != nil {
				ls.walk(def, node, inherited)
			}
		}
	}
}

func (r *run) look() Look {
	defs := map[string]*Node{}
	for _, d := range r.resolved.Definitions {
		defs[d.Name] = d
	}
	count := func(worst bool) (*lookState, int) {
		ls := &lookState{defs: defs, draws: map[[2]string]bool{}, mats: map[string]bool{}, worst: worst}
		if r.resolved.Root != nil {
			ls.walk(r.resolved.Root, 0, "")
		}
		return ls, len(ls.draws)
	}
	_, def := count(false)
	ls, worst := count(true)
	out := Look{DrawCalls: def, DrawCallsWorst: worst, Materials: []string{}}
	for _, m := range r.f.Materials {
		if ls.mats[m.Name.Name] {
			out.Materials = append(out.Materials, m.Name.Name)
		}
	}
	return out
}

// budgetLimit is one effective limit and where it was written.
type budgetLimit struct {
	name string
	val  units.Value
	span diag.Span
}

func (r *run) budgetAndLook() {
	r.resolved.Look = r.look()
	var docSet, outSet []*ast.Setting
	var docSpan diag.Span
	if r.f.Budget != nil {
		docSet = r.f.Budget.Settings
		docSpan = r.f.Budget.Span
	}
	var outBudget *ast.Budget
	for _, o := range r.f.Outputs {
		if o.Name.Name == r.output {
			outBudget = o.Budget
		}
	}
	if outBudget != nil {
		outSet = outBudget.Settings
	}
	k := docKind("budget")
	var limits []budgetLimit
	var fields []Field
	evalAll := func(settings []*ast.Setting, owner diag.Span) map[string]budgetLimit {
		m := map[string]budgetLimit{}
		for _, f := range r.fields(k, settings, r.globals, owner) {
			if s := ast.FindSetting(settings, f.Name); s != nil && f.Value.Valid() {
				m[f.Name] = budgetLimit{f.Name, f.Value, s.Value.Span()}
			}
		}
		return m
	}
	docM := evalAll(docSet, docSpan)
	outM := map[string]budgetLimit{}
	if outBudget != nil {
		outM = evalAll(outSet, outBudget.Span)
	}
	for _, f := range k.Fields {
		l, ok := outM[f.Name]
		if !ok {
			l, ok = docM[f.Name]
		}
		if ok {
			limits = append(limits, l)
			fields = append(fields, Field{Name: l.name, Value: l.val})
		}
	}
	r.resolved.Budget = fields
	get := func(name string) (budgetLimit, bool) {
		for _, l := range limits {
			if l.name == name {
				return l, true
			}
		}
		return budgetLimit{}, false
	}
	look := r.resolved.Look
	if l, ok := get("materials"); ok && float64(len(look.Materials)) > l.val.Num {
		r.report(diag.New(diag.CodeBudget, l.span, "the asset uses %d materials (%s) but the budget allows %s", len(look.Materials), joinNames(look.Materials), l.val.Format()))
	}
	if l, ok := get("draw_calls"); ok && float64(look.DrawCalls) > l.val.Num {
		r.report(diag.New(diag.CodeBudget, l.span, "the default look makes %d draws (one per exported node and material) but the budget allows %s", look.DrawCalls, l.val.Format()))
	}
	if l, ok := get("draw_calls_worst"); ok && float64(look.DrawCallsWorst) > l.val.Num {
		r.report(diag.New(diag.CodeBudget, l.span, "the worst look makes %d draws but the budget allows %s", look.DrawCallsWorst, l.val.Format()))
	}
	for _, pair := range [][2]string{{"draw_calls", "draw_calls_worst"}, {"triangles", "triangles_worst"}} {
		a, aok := get(pair[0])
		b, bok := get(pair[1])
		if aok && bok && b.val.Num < a.val.Num {
			r.report(diag.New(diag.CodeBudget, b.span, "`%s` (%s) is below `%s` (%s), but the worst look cannot be smaller than the default look", pair[1], b.val.Format(), pair[0], a.val.Format()).
				WithRelated(a.span, "the default-look limit"))
		}
	}
}

func joinNames(ns []string) string {
	out := ""
	for i, n := range ns {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
