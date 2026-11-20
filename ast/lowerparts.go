package ast

import (
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/registry"
)

// checkName applies a kind's naming rule and the mirror pair rule.
func (l *lowerer) checkName(k *registry.Kind, kindSpan diag.Span, nameNode *lang.Node, name, pair Ident, inMirror bool) {
	hasName := name.Name != ""
	switch {
	case k.Names == registry.NameNone && hasName:
		l.report(diag.New(diag.CodeType, name.Span, "`%s` does not take a name", k.Name))
		return
	case k.Names == registry.NameRequired && !hasName:
		l.report(diag.New(diag.CodeMissing, kindSpan, "`%s` needs a name", k.Name))
		return
	}
	if !hasName {
		return
	}
	hasPair := pair.Name != ""
	switch {
	case inMirror && !hasPair:
		l.report(diag.New(diag.CodeMirrorName, name.Span,
			"`%s` is declared inside a mirror, so its name must be a pair: (%s, %s_2) with the reflected name second", name.Name, name.Name, name.Name))
	case !inMirror && hasPair:
		l.report(diag.New(diag.CodeMirrorName, span(nameNode), "a name pair is only valid inside a mirror"))
	}
}

// part lowers a part_decl. top is true for a declaration at file level.
func (l *lowerer) part(n *lang.Node, inMirror, top bool) *Part {
	k, _ := registry.Lookup(registry.CtxDocument, "part")
	p := &Part{Span: span(n), TopLevel: top}
	nameNode := n.Field("name")
	p.Name, p.Pair = l.declName(nameNode)
	kw := n.Children()[0]
	l.checkName(k, span(kw), nameNode, p.Name, p.Pair, inMirror)
	p.Settings, p.Items = l.container(n.Field("body"), k, registry.CtxPart, inMirror)
	return p
}

// container lowers a body that holds part items (or collision items). k is
// the kind of the container, nil for a `when` body, which has no settings.
func (l *lowerer) container(body *lang.Node, k *registry.Kind, ctx registry.Context, inMirror bool) ([]*Setting, []Item) {
	settings, rest := l.settings(bodyItems(body), k)
	if k == nil {
		for _, s := range settings {
			l.report(diag.New(diag.CodeUnknownField, s.NameSpan, "a `when` body holds parts and shapes, not settings"))
		}
		settings = nil
	}
	var items []Item
	for _, n := range rest {
		if it := l.item(n, ctx, inMirror); it != nil {
			items = append(items, it)
		}
	}
	return settings, items
}

func (l *lowerer) item(n *lang.Node, ctx registry.Context, inMirror bool) Item {
	switch n.Kind() {
	case "part_decl":
		if ctx == registry.CtxCollision {
			l.report(diag.New(diag.CodeUnknownField, span(n), "collision holds colliders and mirrors, not parts"))
			return nil
		}
		return l.part(n, inMirror, false)
	case "let_decl":
		return l.let(n)
	case "when_block":
		return l.when(n, ctx, inMirror)
	case "block":
		return l.block(n, ctx, inMirror)
	case "row":
		if ctx == registry.CtxCollision {
			l.report(diag.New(diag.CodeUnknownField, span(n.Field("name")), "collision holds colliders and mirrors, not rows"))
			return nil
		}
		nm := n.Field("name")
		return &TemplateCall{Name: nm.Text(), NameSpan: span(nm), Span: span(n), Args: l.args(n)}
	case "sample_row":
		l.report(diag.New(diag.CodeType, span(n), "a sampled row belongs in a lathe or path, not here"))
	case "waiver":
		l.unsupported(n, "`waive`")
	default:
		l.report(diag.New(diag.CodeSyntax, span(n), "unexpected %s", n.Kind()))
	}
	return nil
}

func (l *lowerer) when(n *lang.Node, ctx registry.Context, inMirror bool) Item {
	w := &When{Span: span(n), Cond: l.expr(n.Field("condition"))}
	_, w.Then = l.container(n.Field("body"), nil, ctx, inMirror)
	if alt := n.Field("alternative"); alt != nil {
		if alt.Kind() == "when_block" {
			if it := l.when(alt, ctx, inMirror); it != nil {
				w.Else = []Item{it}
			}
		} else {
			_, w.Else = l.container(alt, nil, ctx, inMirror)
		}
	}
	return w
}

// block lowers a kind-introduced block inside a part or collision.
func (l *lowerer) block(n *lang.Node, ctx registry.Context, inMirror bool) Item {
	kn := n.Field("kind")
	kind, ksp := kn.Text(), span(kn)
	k, ok := registry.Lookup(ctx, kind)
	if !ok {
		if registry.IsKnownUnsupported(ctx, kind) {
			l.unsupported(kn, "`"+kind+"`")
			return nil
		}
		d := diag.New(diag.CodeUnknownField, ksp, "unknown block `%s`", kind)
		if s, ok := registry.Suggest(kind, registry.KindNames(ctx)); ok {
			d = d.WithEdit("write "+s, ksp, s)
		}
		l.report(d)
		return nil
	}
	nameNode := n.Field("name")
	name, pair := l.declName(nameNode)
	l.checkName(k, ksp, nameNode, name, pair, inMirror)
	body := n.Field("body")
	sp := span(n)
	switch {
	case kind == "mirror":
		m := &Mirror{Span: sp}
		m.Settings, m.Items = l.container(body, k, ctx, true)
		return m
	case kind == "array":
		a := &Array{Name: name, Pair: pair, Span: sp}
		a.Settings, a.Items = l.container(body, k, ctx, inMirror)
		return a
	case kind == "instance":
		return &Instance{Name: name, Pair: pair, Span: sp, Settings: l.settingsOnly(body, k)}
	case kind == "socket":
		return &Socket{Name: name, Pair: pair, Span: sp, Settings: l.settingsOnly(body, k)}
	case ctx == registry.CtxCollision:
		return &Collider{Kind: kind, KindSpan: ksp, Name: name, Pair: pair, Span: sp, Settings: l.settingsOnly(body, k)}
	case k.IsShape:
		return l.shape(n, k, name, pair)
	}
	l.report(diag.New(diag.CodeUnknownField, ksp, "block `%s` is not allowed here", kind))
	return nil
}

func (l *lowerer) shape(n *lang.Node, k *registry.Kind, name, pair Ident) Item {
	kn := n.Field("kind")
	s := &Shape{Kind: k.Name, KindSpan: span(kn), Name: name, Pair: pair, Span: span(n)}
	settings, rest := l.settings(bodyItems(n.Field("body")), k)
	s.Settings = settings
	for _, it := range rest {
		switch it.Kind() {
		case "row", "sample_row":
			if r := l.rowItem(it, k); r != nil {
				s.Rows = append(s.Rows, r)
			}
		case "block":
			bk := it.Field("kind")
			allowed := false
			for _, b := range k.Blocks {
				if b == bk.Text() {
					allowed = true
				}
			}
			if !allowed {
				if registry.IsKnownUnsupported(registry.CtxShape, bk.Text()) {
					l.unsupported(bk, "`"+bk.Text()+"`")
					continue
				}
				d := diag.New(diag.CodeUnknownField, span(bk), "`%s` does not take a `%s` block", k.Name, bk.Text())
				if sug, ok := registry.Suggest(bk.Text(), k.Blocks); ok {
					d = d.WithEdit("write "+sug, span(bk), sug)
				}
				l.report(d)
				continue
			}
			bkind, _ := registry.Lookup(registry.CtxShape, bk.Text())
			if nm := it.Field("name"); nm != nil {
				l.report(diag.New(diag.CodeType, span(nm), "`%s` does not take a name", bk.Text()))
			}
			for _, prev := range s.Blocks {
				if prev.Kind == bk.Text() {
					l.report(diag.New(diag.CodeDuplicate, span(bk), "a shape has one `%s` block", bk.Text()).WithRelated(prev.KindSpan, "first block"))
				}
			}
			blk := &Block{Kind: bk.Text(), KindSpan: span(bk), Span: span(it)}
			bs, brest := l.settings(bodyItems(it.Field("body")), bkind)
			blk.Settings = bs
			for _, r := range brest {
				if r.Kind() == "row" || r.Kind() == "sample_row" {
					if ri := l.rowItem(r, bkind); ri != nil {
						blk.Rows = append(blk.Rows, ri)
					}
				} else {
					l.notAllowed(r, bkind)
				}
			}
			s.Blocks = append(s.Blocks, blk)
		default:
			l.notAllowed(it, k)
		}
	}
	return s
}

// rowItem lowers a row or sampled row and checks the row name against k.
func (l *lowerer) rowItem(n *lang.Node, k *registry.Kind) RowItem {
	if n.Kind() == "sample_row" {
		row := l.row(n.Field("row"), k)
		if row == nil {
			return nil
		}
		s := &SampleRow{Span: span(n), Var: ident(n.Field("variable")), Count: l.expr(n.Field("count")), Row: row}
		if r := n.Field("range"); r != nil {
			s.Range = l.rng(r)
		}
		return s
	}
	if r := l.row(n, k); r != nil {
		return r
	}
	return nil
}

func (l *lowerer) row(n *lang.Node, k *registry.Kind) *Row {
	nm := n.Field("name")
	name := nm.Text()
	if _, ok := k.Row(name); !ok {
		if len(k.Rows) == 0 {
			l.report(diag.New(diag.CodeUnknownField, span(nm), "`%s` takes no rows", k.Name))
		} else {
			d := diag.New(diag.CodeUnknownField, span(nm), "`%s` has no row `%s`", k.Name, name)
			if s, ok := registry.Suggest(name, k.RowNames()); ok {
				d = d.WithEdit("write "+s, span(nm), s)
			}
			l.report(d)
		}
		return nil
	}
	return &Row{Name: name, NameSpan: span(nm), Span: span(n), Args: l.args(n)}
}
