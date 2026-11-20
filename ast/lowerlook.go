package ast

import (
	"strings"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/registry"
)

// importDecl lowers `import "path" [as name]`.
func (l *lowerer) importDecl(n *lang.Node) *Import {
	im := &Import{Span: span(n)}
	if pn := n.Field("path"); pn != nil {
		v, msg := decodeString(pn.Text())
		if msg != "" {
			l.report(diag.New(diag.CodeSyntax, span(pn), "%s", msg))
		}
		im.Path, im.PathSpan = v, span(pn)
	}
	if an := n.Field("alias"); an != nil {
		im.Qualifier, im.QualSpan, im.Explicit = an.Text(), span(an), true
	} else {
		q := im.Path
		if i := strings.LastIndex(q, "/"); i >= 0 {
			q = q[i+1:]
		}
		im.Qualifier, im.QualSpan = strings.TrimSuffix(q, ".gw"), im.PathSpan
	}
	return im
}

// painter lowers `painter name(formals) { let ... color = ... }`.
func (l *lowerer) painter(n *lang.Node) *Painter {
	p := &Painter{Name: ident(n.Field("name")), Span: span(n)}
	for _, c := range n.Children() {
		if c.Kind() != "typed_param" {
			continue
		}
		tp := TemplateParam{Name: ident(c.Field("name")), Type: paramType(c.Field("type"))}
		if d := c.Field("default"); d != nil {
			tp.Default = l.expr(d)
		}
		p.Params = append(p.Params, tp)
	}
	for _, it := range bodyItems(n.Field("body")) {
		switch {
		case it.Kind() == "let_decl":
			p.Lets = append(p.Lets, l.let(it))
		case it.Kind() == "setting" && it.Field("name").Text() == "color":
			if p.Color != nil {
				l.report(diag.New(diag.CodeDuplicate, span(it.Field("name")), "a painter sets `color` once"))
				continue
			}
			v := it.Field("value")
			if v == nil || v.Kind() == "range" || v.Kind() == "source_value" {
				l.report(diag.New(diag.CodeType, span(it), "`color` takes a colour expression"))
				continue
			}
			p.Color, p.ColorSpan = l.expr(v), span(it)
		default:
			what := "a " + it.Kind()
			if it.Kind() == "setting" {
				what = "the field `" + it.Field("name").Text() + "`"
			}
			l.report(diag.New(diag.CodeUnknownField, span(it), "%s is not allowed in a painter, which holds `let` and `color`", what))
		}
	}
	return p
}

// texture lowers `texture name = atlas { ... }`. Other texture kinds are valid
// in the language and outside this compiler's subset.
func (l *lowerer) texture(n *lang.Node) *Texture {
	name := n.Field("name")
	value := n.Field("value")
	if value == nil {
		return nil
	}
	kn := value.Field("kind")
	t := &Texture{Name: ident(name), Span: span(n), Kind: kn.Text(), KindSpan: span(kn)}
	if t.Kind != "atlas" {
		switch t.Kind {
		case "file", "bake", "selena", "elio":
			l.unsupported(kn, "a `"+t.Kind+"` texture")
		default:
			d := diag.New(diag.CodeUnknownName, t.KindSpan, "unknown texture kind `%s`", t.Kind)
			if s, ok := registry.Suggest(t.Kind, []string{"atlas", "file", "bake", "selena", "elio"}); ok {
				d = d.WithEdit("write "+s, t.KindSpan, s)
			}
			l.report(d)
		}
		return nil
	}
	k, _ := registry.Lookup(registry.CtxDocument, "atlas")
	settings, rest := l.settings(bodyItems(value.Field("body")), k)
	t.Settings = settings
	ck, _ := registry.Lookup(registry.CtxAtlas, "cell")
	for _, it := range rest {
		if it.Kind() != "block" || it.Field("kind").Text() != "cell" {
			l.notAllowed(it, k)
			continue
		}
		cn := it.Field("name")
		c := &Cell{Span: span(it)}
		if cn == nil {
			l.report(diag.New(diag.CodeMissing, span(it.Field("kind")), "`cell` needs a name"))
			continue
		}
		c.Name = ident(cn)
		if cn.Kind() == "name_pair" {
			l.report(diag.New(diag.CodeMirrorName, span(cn), "a name pair is only valid inside a mirror"))
			continue
		}
		c.Settings = l.settingsOnly(it.Field("body"), ck)
		t.Cells = append(t.Cells, c)
	}
	return t
}
