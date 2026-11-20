package ast

import (
	"strings"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/registry"
)

// rigDecl lowers a skeleton, skin, morph or clip declaration.
func (l *lowerer) rigDecl(n *lang.Node) *RigDecl {
	kw := n.Children()[0]
	d := &RigDecl{Kind: strings.TrimSuffix(n.Kind(), "_decl"), KindSpan: span(kw), Name: ident(n.Field("name")), Span: span(n)}
	k, _ := registry.Lookup(registry.CtxDocument, d.Kind)
	if k != nil {
		l.checkName(k, d.KindSpan, n.Field("name"), d.Name, Ident{}, false)
	}
	d.Settings, d.Items = l.rigBody(n.Field("body"), k, false)
	return d
}

// rigBody lowers a rig body against kind k: its settings, and its items in
// source order. inMirror says that names declared here must be pairs.
func (l *lowerer) rigBody(body *lang.Node, k *registry.Kind, inMirror bool) ([]*Setting, []RigItem) {
	settings, rest := l.settings(bodyItems(body), k)
	var items []RigItem
	for _, n := range rest {
		switch n.Kind() {
		case "block":
			if b := l.rigBlock(n, inMirror); b != nil {
				items = append(items, b)
			}
		case "row", "sample_row":
			var ri RowItem
			if k != nil {
				ri = l.rigRow(n, k)
			}
			if ri != nil {
				items = append(items, ri.(RigItem))
			}
		case "let_decl":
			items = append(items, l.let(n))
		case "when_block":
			l.unsupported(n.Children()[0], "`when` inside a rig declaration")
		case "waiver":
			l.unsupported(n, "`waive`")
		default:
			l.report(diag.New(diag.CodeSyntax, span(n), "unexpected %s", n.Kind()))
		}
	}
	return settings, items
}

// rigRow lowers a row of a body whose kind lists the row (the `key` rows of
// a clip).
func (l *lowerer) rigRow(n *lang.Node, k *registry.Kind) RowItem {
	if n.Kind() == "sample_row" {
		inner := n.Field("row")
		if inner == nil {
			return nil
		}
		row := l.row(inner, k)
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

// rigBlock lowers a block of a rig body.
func (l *lowerer) rigBlock(n *lang.Node, inMirror bool) *RigBlock {
	kn := n.Field("kind")
	kind, ksp := kn.Text(), span(kn)
	k, ok := registry.Lookup(registry.CtxRig, kind)
	if !ok {
		d := diag.New(diag.CodeUnknownField, ksp, "unknown block `%s` in a rig declaration", kind)
		if s, ok := registry.Suggest(kind, registry.KindNames(registry.CtxRig)); ok {
			d = d.WithEdit("write "+s, ksp, s)
		}
		l.report(d)
		return nil
	}
	nameNode := n.Field("name")
	name, pair := l.declName(nameNode)
	l.checkName(k, ksp, nameNode, name, pair, inMirror)
	b := &RigBlock{Kind: kind, KindSpan: ksp, Name: name, Pair: pair, Span: span(n)}
	b.Settings, b.Items = l.rigBody(n.Field("body"), k, inMirror || kind == "mirror")
	return b
}
