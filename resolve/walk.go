package resolve

import (
	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// maxCopies is the most copies one array makes.
const maxCopies = 4096

// bodyScope makes a scope for a body and declares its lets. Lets may use one
// another in any order; they are evaluated here so that a fault in an unused
// let is still reported.
func (r *run) bodyScope(parent *scope, items []ast.Item) *scope {
	sc := newScope(parent)
	for _, it := range items {
		l, ok := it.(*ast.Let)
		if !ok {
			continue
		}
		if prev, dup := sc.names[l.Name.Name]; dup {
			r.report(diag.New(diag.CodeDuplicate, l.Name.Span, "the name `%s` is declared twice in one body", l.Name.Name).WithRelated(prev.span, "first declaration"))
			continue
		}
		sc.add(&binding{name: l.Name.Name, span: l.Name.Span, let: l, scope: sc})
	}
	for _, name := range sc.order {
		r.force(sc.names[name])
	}
	return sc
}

// parts evaluates the root part and the definitions.
func (r *run) parts() {
	rootName := ""
	if v, ok := field(r.resolved.Asset, "root"); ok && v.Valid() {
		rootName = v.Str
	}
	for _, p := range r.f.Parts {
		if d := r.syms.lookup(registry.NsGeometry, p.Name.Name); d == nil || d.part != p {
			continue
		}
		n := r.part(p, r.globals)
		if p.Name.Name == rootName {
			r.resolved.Root = n
		} else {
			r.resolved.Definitions = append(r.resolved.Definitions, n)
		}
	}
}

func (r *run) part(p *ast.Part, parent *scope) *Node {
	n := &Node{Kind: "part", Name: p.Name.Name, Pair: p.Pair.Name}
	sc := r.bodyScope(parent, p.Items)
	n.Fields = r.fields(docKind("part"), p.Settings, sc, p.Name.Span)
	n.Children = r.items(p.Items, sc)
	return n
}

func (r *run) items(items []ast.Item, sc *scope) []*Node {
	var out []*Node
	for _, it := range items {
		switch n := it.(type) {
		case *ast.Part:
			out = append(out, r.part(n, sc))
		case *ast.Shape:
			out = append(out, r.shape(n, sc))
		case *ast.Array:
			out = append(out, r.array(n, sc))
		case *ast.Instance:
			out = append(out, r.instance(n, sc))
		case *ast.Mirror:
			out = append(out, r.mirror(n, sc, registry.CtxPart))
		case *ast.Socket:
			k, _ := registry.Lookup(registry.CtxPart, "socket")
			out = append(out, &Node{Kind: "socket", Name: n.Name.Name, Pair: n.Pair.Name, Fields: r.fields(k, n.Settings, sc, n.Name.Span)})
		case *ast.Collider:
			out = append(out, r.collider(n, sc))
		case *ast.When:
			out = append(out, r.when(n, sc)...)
		case *ast.TemplateCall:
			out = append(out, r.templateCall(n, sc)...)
		}
	}
	return out
}

func (r *run) when(w *ast.When, sc *scope) []*Node {
	c := r.eval(w.Cond, sc)
	if c.Valid() && c.Kind != units.KSwitch {
		r.report(diag.New(diag.CodeType, w.Cond.Span(), "a `when` condition must be a switch, found a %s", c.Describe()))
		c = units.Bad()
	}
	branch := func(items []ast.Item, take bool) []*Node {
		if !take {
			r.quiet++
			defer func() { r.quiet-- }()
		}
		bs := r.bodyScope(sc, items)
		return r.items(items, bs)
	}
	switch {
	case !c.Valid():
		branch(w.Then, false)
		branch(w.Else, false)
		return nil
	case c.DependsOnRuntime():
		cond := c
		cd, ok := r.condOf(w.Cond, sc)
		if !ok {
			r.report(diag.New(diag.CodeUnsupported, w.Cond.Span(), "a runtime `when` can test a switch parameter, its negation, or an enum parameter against one of its members; this condition is none of those"))
			branch(w.Then, false)
			branch(w.Else, false)
			return nil
		}
		return []*Node{{Kind: "when", Condition: &cond, Test: cd.String(), Cond: &cd, Children: branch(w.Then, true), Else: branch(w.Else, true)}}
	case c.Bool:
		out := branch(w.Then, true)
		branch(w.Else, false)
		return out
	default:
		branch(w.Then, false)
		return branch(w.Else, true)
	}
}

func (r *run) shape(s *ast.Shape, sc *scope) *Node {
	k, _ := registry.Lookup(registry.CtxPart, s.Kind)
	n := &Node{Kind: s.Kind, Name: s.Name.Name, Pair: s.Pair.Name}
	n.Fields = r.fields(k, s.Settings, sc, s.KindSpan)
	var rowsOK bool
	if k.Name == "loft" {
		r.loft(n, s, k, sc)
	} else {
		n.Rows, rowsOK = r.rows(s.Rows, k, sc)
	}
	if rowsOK {
		for _, spec := range k.Rows {
			if len(n.Rows) < spec.MinRows {
				r.report(diag.New(diag.CodeMissing, s.KindSpan, "`%s` needs at least %d `%s` rows, found %d", k.Name, spec.MinRows, spec.Name, len(n.Rows)))
			}
		}
	}
	if k.Name == "tube" {
		if c, ok := field(n.Fields, "closed"); ok && c.Valid() && c.Bool && rowsOK && len(n.Rows) < 3 {
			r.report(diag.New(diag.CodeMissing, s.KindSpan, "a closed `tube` needs at least 3 points, found %d", len(n.Rows)))
		}
	}
	if k.Name == "ellipsoid" {
		if v, ok := field(n.Fields, "radii"); ok && v.Valid() {
			for _, e := range v.Elems {
				if !(e.Num > 0) {
					r.report(diag.New(diag.CodeFieldRange, settingSpan(s, "radii"), "every radius of an ellipsoid must be greater than zero, found %s", v.Format()))
					break
				}
			}
		}
	}
	for _, b := range s.Blocks {
		bk, _ := registry.Lookup(registry.CtxShape, b.Kind)
		child := &Node{Kind: b.Kind}
		child.Fields = r.fields(bk, b.Settings, sc, b.KindSpan)
		var rowsOK bool
		child.Rows, rowsOK = r.rows(b.Rows, bk, sc)
		if rowsOK {
			for _, spec := range bk.Rows {
				if len(child.Rows) < spec.MinRows {
					r.report(diag.New(diag.CodeMissing, b.KindSpan, "`%s` needs at least %d `%s` rows, found %d", b.Kind, spec.MinRows, spec.Name, len(child.Rows)))
				}
			}
		}
		if b.Kind == "bevel" {
			r.deriveBevel(child, s)
		}
		n.Children = append(n.Children, child)
	}
	if k.Name == "sweep" {
		if len(s.Blocks) == 0 {
			r.report(diag.New(diag.CodeMissing, s.KindSpan, "`sweep` needs a `path` block"))
		}
	}
	if k.Name == "sphere" {
		if _, ok := field(n.Fields, "rings"); !ok {
			if seg, ok := field(n.Fields, "segments"); ok && seg.Valid() {
				rings := float64(int(seg.Num+1) / 2)
				if rings < 2 {
					rings = 2
				}
				n.Fields = append(n.Fields, Field{Name: "rings", Value: units.Num(rings), Default: true})
			}
		}
	}
	return n
}

func (r *run) deriveBevel(b *Node, s *ast.Shape) {
	if _, ok := field(b.Fields, "edges"); ok {
		return
	}
	def := "caps"
	if s.Kind == "box" {
		def = "all"
	}
	b.Fields = append(b.Fields, Field{Name: "edges", Value: units.Label(def), Default: true})
}

func (r *run) array(a *ast.Array, sc *scope) *Node {
	k, _ := registry.Lookup(registry.CtxPart, "array")
	n := &Node{Kind: "array", Name: a.Name.Name, Pair: a.Pair.Name}
	n.Fields = r.fields(k, a.Settings, sc, nonEmpty(a.Name.Span, diag.Span{Start: a.Span.Start, End: a.Span.Start + len("array")}))
	inner := newScope(sc)
	if s := ast.FindSetting(a.Settings, "index"); s != nil {
		if nm, sp, ok := nameOf(s.Value); ok {
			inner.add(&binding{name: nm, span: sp, state: stDone, value: units.Num(0)})
		}
	}
	mode := "linear"
	if v, ok := field(n.Fields, "mode"); ok && v.Valid() {
		mode = v.Str
	}
	count, hasCount := field(n.Fields, "count")
	cs := a.Span
	if s := ast.FindSetting(a.Settings, "count"); s != nil {
		cs = s.Span
	}
	if hasCount && count.Valid() {
		total := 1.0
		isGrid := count.Kind == units.KTuple
		if isGrid {
			for _, e := range count.Elems {
				total = float64(total * e.Num)
			}
		} else {
			total = count.Num
		}
		switch {
		case mode == "grid" && !isGrid:
			r.report(diag.New(diag.CodeType, cs, "a grid array needs three counts, written (nx, ny, nz)"))
		case mode != "grid" && isGrid:
			r.report(diag.New(diag.CodeType, cs, "only a grid array takes three counts"))
		case total > maxCopies:
			r.report(diag.New(diag.CodeFieldRange, cs, "an array makes at most %d copies, found %s", maxCopies, num(total)).
				WithRelated(a.Span, "this array"))
		}
	}
	if mode == "radial" {
		if _, ok := field(n.Fields, "radius"); !ok {
			r.report(diag.New(diag.CodeMissing, a.Span, "a radial array needs `radius`"))
		}
	}
	bs := r.bodyScope(inner, a.Items)
	n.Children = r.items(a.Items, bs)
	return n
}

func (r *run) mirror(m *ast.Mirror, sc *scope, ctx registry.Context) *Node {
	k, _ := registry.Lookup(ctx, "mirror")
	n := &Node{Kind: "mirror"}
	n.Fields = r.fields(k, m.Settings, sc, diag.Span{Start: m.Span.Start, End: m.Span.Start + len("mirror")})
	inner := newScope(sc)
	inner.add(&binding{name: "side", span: m.Span, state: stDone, value: units.Num(1)})
	bs := r.bodyScope(inner, m.Items)
	n.Children = r.items(m.Items, bs)
	return n
}

func (r *run) instance(in *ast.Instance, sc *scope) *Node {
	k, _ := registry.Lookup(registry.CtxPart, "instance")
	n := &Node{Kind: "instance", Name: in.Name.Name, Pair: in.Pair.Name}
	n.Fields = r.fields(k, in.Settings, sc, diag.Span{Start: in.Span.Start, End: in.Span.Start + len("instance")})
	if v, ok := field(n.Fields, "of"); ok && v.Valid() {
		if d := r.syms.lookup(registry.NsGeometry, v.Str); d != nil && (!d.topLevel || d.root) {
			sp := in.Span
			if s := ast.FindSetting(in.Settings, "of"); s != nil {
				sp = s.Value.Span()
			}
			what := "nested"
			if d.root {
				what = "the asset's root"
			}
			r.report(diag.New(diag.CodeType, sp, "`instance` places a top-level part definition, but `%s` is %s", v.Str, what).WithRelated(d.span, "declared here"))
		}
	}
	return n
}

func (r *run) collider(c *ast.Collider, sc *scope) *Node {
	k, _ := registry.Lookup(registry.CtxCollision, c.Kind)
	n := &Node{Kind: c.Kind, Name: c.Name.Name, Pair: c.Pair.Name}
	n.Fields = r.fields(k, c.Settings, sc, c.KindSpan)
	if c.Kind == "capsule" {
		from, fok := field(n.Fields, "from")
		to, tok := field(n.Fields, "to")
		if fok && tok && from.Valid() && to.Valid() && units.Equal(units.Finish(from), units.Finish(to)) {
			r.report(diag.New(diag.CodeFieldRange, c.Span, "the capsule `%s` has the same `from` and `to` point", c.Name.Name))
		}
	}
	return n
}

func (r *run) collision() {
	c := r.f.Collision
	if c == nil {
		return
	}
	sc := r.bodyScope(r.globals, c.Items)
	for _, it := range c.Items {
		switch n := it.(type) {
		case *ast.Collider:
			r.resolved.Collision = append(r.resolved.Collision, r.collider(n, sc))
		case *ast.Mirror:
			r.resolved.Collision = append(r.resolved.Collision, r.mirror(n, sc, registry.CtxCollision))
		case *ast.When:
			r.resolved.Collision = append(r.resolved.Collision, r.when(n, sc)...)
		}
	}
}

func (r *run) lods() {
	k := docKind("lod")
	seen := map[int]diag.Span{}
	prevDist := 0.0
	prevLevel := 0
	for i, l := range r.f.Lods {
		n := &Node{Kind: "lod", Name: itoa(l.Level)}
		n.Fields = r.fields(k, l.Settings, r.globals, l.LevelSpan)
		r.resolved.Lods = append(r.resolved.Lods, n)
		if prev, dup := seen[l.Level]; dup {
			r.report(diag.New(diag.CodeDuplicate, l.LevelSpan, "level %d is declared twice", l.Level).WithRelated(prev, "first declaration"))
			continue
		}
		seen[l.Level] = l.LevelSpan
		if l.Level != prevLevel+1 {
			r.report(diag.New(diag.CodeFieldRange, l.LevelSpan, "levels of detail start at 1 and rise by 1: expected level %d, found %d", prevLevel+1, l.Level))
		}
		prevLevel = l.Level
		if d, ok := field(n.Fields, "distance"); ok && d.Valid() {
			if i > 0 && !(d.Num > prevDist) {
				sp := l.Span
				if s := ast.FindSetting(l.Settings, "distance"); s != nil {
					sp = s.Value.Span()
				}
				r.report(diag.New(diag.CodeFieldRange, sp, "switch distances must increase: level %d switches at %s, not after %s", l.Level, d.Format(), units.Len(prevDist).Format()))
			}
			prevDist = d.Num
		}
		keep, kok := field(n.Fields, "keep")
		drop, dok := field(n.Fields, "drop")
		if kok && dok {
			for _, kv := range keep.Elems {
				for _, dv := range drop.Elems {
					if kv.Str == dv.Str {
						sp := l.Span
						if s := ast.FindSetting(l.Settings, "drop"); s != nil {
							sp = s.Span
						}
						r.report(diag.New(diag.CodeFieldRange, sp, "`%s` is both kept and dropped at level %d", kv.Str, l.Level))
					}
				}
			}
		}
	}
}

func itoa(i int) string { return num(float64(i)) }
