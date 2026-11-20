package ast

import (
	"strconv"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

type lowerer struct {
	tree  *lang.Tree
	diags []diag.Diagnostic
	// geom is true for a geometry-only lowering; skipped holds what it left out.
	geom    bool
	lib     bool
	skipped []string
}

// Options choose how Lower reads a file.
type Options struct {
	// GeometryOnly leaves out the look declarations that this compiler does not
	// yet read: `import`, `plug` and `texture` declarations and the `cell`
	// settings of shapes. Every material becomes a plain grey pbr material of
	// the same name. File.Skipped says what was left out. The shapes, the
	// parameters and the outputs are read in full.
	GeometryOnly bool
	// Library lowers a library file: it needs no asset declaration.
	Library bool
	// Project says where project imports and table files are read from. Nil
	// means none: such an import is reported as outside the subset.
	Project *Project
}

// Lower turns a parsed tree into a File and the diagnostics found on the way:
// syntax errors, a bad edition header, unknown or unsupported fields and
// blocks, duplicate settings and declarations of single-use kinds, and name
// pairs that are not allowed. It always returns a File.
func Lower(tree *lang.Tree) (*File, []diag.Diagnostic) {
	return LowerWith(tree, Options{})
}

// LowerWith is Lower with options.
func LowerWith(tree *lang.Tree, opts Options) (*File, []diag.Diagnostic) {
	l := &lowerer{tree: tree, geom: opts.GeometryOnly, lib: opts.Library}
	f := &File{Source: tree.Source(), Broken: tree.HasErrors(), Project: opts.Project}
	for _, d := range tree.Diagnostics() {
		l.report(diag.New(diag.CodeSyntax, diag.Span{Start: d.Span.Start, End: d.Span.End}, "%s", d.Message))
	}
	root := tree.Root()
	l.edition(f, root)
	for _, n := range root.NamedChildren() {
		if n.IsError() || n.IsMissing() {
			continue
		}
		l.declaration(f, n)
	}
	f.Skipped = l.skipped
	if f.Asset == nil && len(f.Accepts) == 0 && !f.Broken && !l.lib {
		sp := diag.Span{}
		if len(f.Source) > 0 {
			sp.End = 1
		}
		l.report(diag.New(diag.CodeMissing, sp, "the file has no asset declaration"))
	}
	if f.Broken {
		// A broken tree makes odd shapes; report only what the parser found.
		kept := l.diags[:0]
		for _, d := range l.diags {
			if d.Code == diag.CodeSyntax || d.Code == diag.CodeEdition {
				kept = append(kept, d)
			}
		}
		l.diags = kept
	}
	return f, diag.Dedup(l.diags)
}

// edition checks the `greenware 1` header.
func (l *lowerer) edition(f *File, root *lang.Node) {
	for _, n := range root.NamedChildren() {
		if n.Kind() == "edition_decl" {
			f.HasEdition = true
			e := n.Field("edition")
			if e == nil {
				return
			}
			f.EditionSpan = span(e)
			v, err := strconv.Atoi(e.Text())
			f.Edition = v
			if err != nil || v != 1 {
				l.report(diag.New(diag.CodeEdition, span(e), "this compiler reads edition 1, not %s", e.Text()).
					WithEdit("write edition 1", span(e), "1"))
			}
			return
		}
	}
	// No header. Find the first token that is not a comment.
	var first *lang.Node
	for _, leaf := range l.tree.Leaves() {
		if leaf.Kind() != "comment" {
			first = leaf
			break
		}
	}
	sp := diag.Span{}
	if first != nil {
		sp = span(first)
	} else if len(f.Source) > 0 {
		sp.End = 1
	}
	d := diag.New(diag.CodeEdition, sp, "the file must start with the edition header `greenware 1`")
	insertAt := 0
	if first != nil {
		insertAt = first.Span().Start
	}
	d = d.WithEdit("insert the header", diag.Span{Start: insertAt, End: insertAt}, "greenware 1\n\n")
	l.report(d)
	// A bad header word produces a syntax error at the same place; the
	// edition diagnostic already says it.
	kept := l.diags[:0]
	for _, x := range l.diags {
		if x.Code == diag.CodeSyntax && x.Span.Start <= sp.End {
			continue
		}
		kept = append(kept, x)
	}
	l.diags = kept
}

// skip records a look declaration that a geometry-only lowering left out.
func (l *lowerer) skip(what string) {
	for _, s := range l.skipped {
		if s == what {
			return
		}
	}
	l.skipped = append(l.skipped, what)
}

// lookField reports whether an unsupported field belongs to the look of a
// shape or material (textures), which a geometry-only lowering drops.
func lookField(why string) bool { return why == "textures" }

func (l *lowerer) unsupported(n *lang.Node, what string) {
	l.report(diag.New(diag.CodeUnsupported, span(n), "%s is valid in the language but outside this compiler's subset", what))
}

func (l *lowerer) once(seen bool, n *lang.Node, what string, first diag.Span) bool {
	if seen {
		l.report(diag.New(diag.CodeDuplicate, span(n), "a file has one %s", what).WithRelated(first, "first "+what))
		return true
	}
	return false
}

func (l *lowerer) declaration(f *File, n *lang.Node) {
	switch n.Kind() {
	case "edition_decl":
		// Read by edition.
	case "asset_decl":
		if f.Asset != nil {
			l.report(diag.New(diag.CodeDuplicate, span(n), "a file describes one asset").WithRelated(f.Asset.Span, "first asset"))
			return
		}
		k, _ := registry.Lookup(registry.CtxDocument, "asset")
		a := &Asset{Name: ident(n.Field("name")), Span: span(n)}
		a.Settings = l.settingsOnly(n.Field("body"), k)
		f.Asset = a
	case "meta_decl":
		if f.Meta != nil {
			l.report(diag.New(diag.CodeDuplicate, span(n), "a file has one meta block").WithRelated(f.Meta.Span, "first meta block"))
			return
		}
		k, _ := registry.Lookup(registry.CtxDocument, "meta")
		f.Meta = &Meta{Span: span(n), Settings: l.settingsOnly(n.Field("body"), k)}
	case "param_decl":
		f.Params = append(f.Params, l.param(n))
	case "let_decl":
		f.Lets = append(f.Lets, l.let(n))
	case "check_decl":
		f.Checks = append(f.Checks, l.check(n))
	case "material_decl":
		if l.geom {
			name := ident(n.Field("name"))
			f.Materials = append(f.Materials, plainGrey(name, span(n)))
			l.skip("material " + name.Name + ": drawn as a plain grey pbr material")
			return
		}
		if m := l.material(n.Field("name"), n.Field("value"), span(n)); m != nil {
			f.Materials = append(f.Materials, m)
		}
	case "template_decl":
		f.Templates = append(f.Templates, l.template(n))
	case "part_decl":
		f.Parts = append(f.Parts, l.part(n, false, true))
	case "lod_decl":
		k, _ := registry.Lookup(registry.CtxDocument, "lod")
		lv := n.Field("level")
		lod := &Lod{Span: span(n), Settings: l.settingsOnly(n.Field("body"), k)}
		if lv != nil {
			lod.LevelSpan = span(lv)
			lod.Level, _ = strconv.Atoi(lv.Text())
		}
		f.Lods = append(f.Lods, lod)
	case "collision_decl":
		if l.once(f.Collision != nil, n, "collision block", spanOf(f.Collision)) {
			return
		}
		f.Collision = &Collision{Span: span(n)}
		ck, _ := registry.Lookup(registry.CtxDocument, "collision")
		_, items := l.container(n.Field("body"), ck, registry.CtxCollision, false)
		f.Collision.Items = items
	case "budget_decl":
		if l.once(f.Budget != nil, n, "budget block", budgetSpan(f.Budget)) {
			return
		}
		f.Budget = l.budget(n.Field("body"), span(n))
	case "output_decl":
		f.Outputs = append(f.Outputs, l.output(n))
	case "accept_decl":
		f.Accepts = append(f.Accepts, l.accept(n))
	case "import_decl", "texture_decl", "plug_decl", "painter_decl":
		if l.geom {
			kw := n.Children()[0]
			what := kw.Text()
			if nm := n.Field("name"); nm != nil {
				what += " " + nm.Text()
			}
			l.skip(what)
			return
		}
		switch n.Kind() {
		case "import_decl":
			f.Imports = append(f.Imports, l.importDecl(n))
		case "texture_decl":
			if t := l.texture(n); t != nil {
				f.Textures = append(f.Textures, t)
			}
		case "plug_decl":
			k, _ := registry.Lookup(registry.CtxDocument, "plug")
			p := &Plug{Name: ident(n.Field("name")), Span: span(n)}
			p.Settings = l.settingsOnly(n.Field("body"), k)
			f.Plugs = append(f.Plugs, p)
		default:
			f.Painters = append(f.Painters, l.painter(n))
		}
	case "skeleton_decl", "skin_decl", "morph_decl", "clip_decl":
		f.Rigs = append(f.Rigs, l.rigDecl(n))
	case "table_decl":
		if l.geom {
			l.skip("table " + n.Field("name").Text())
			return
		}
		f.Tables = append(f.Tables, l.table(n))
	case "mount_decl":
		if l.geom {
			l.skip("mount " + n.Field("name").Text())
			return
		}
		f.Mounts = append(f.Mounts, l.mount(n))
	case "mesh_decl", "view_decl", "variant_decl":
		kw := n.Children()[0]
		l.unsupported(kw, "`"+kw.Text()+"`")
	default:
		l.report(diag.New(diag.CodeSyntax, span(n), "unexpected %s", n.Kind()))
	}
}

func spanOf(c *Collision) diag.Span {
	if c == nil {
		return diag.Span{}
	}
	return c.Span
}

func budgetSpan(b *Budget) diag.Span {
	if b == nil {
		return diag.Span{}
	}
	return b.Span
}

func ident(n *lang.Node) Ident {
	if n == nil {
		return Ident{}
	}
	return Ident{Name: n.Text(), Span: span(n)}
}

// declName reads the name slot of a declaration: an identifier or a pair.
func (l *lowerer) declName(n *lang.Node) (name, pair Ident) {
	if n == nil {
		return
	}
	if n.Kind() == "name_pair" {
		return ident(n.Field("left")), ident(n.Field("right"))
	}
	return ident(n), Ident{}
}

// bodyItems returns the item nodes of a body.
func bodyItems(body *lang.Node) []*lang.Node {
	if body == nil {
		return nil
	}
	var out []*lang.Node
	for _, c := range body.NamedChildren() {
		if c.IsError() || c.IsMissing() {
			continue
		}
		out = append(out, c)
	}
	return out
}

// settingsOnly lowers a body that holds settings and nothing else.
func (l *lowerer) settingsOnly(body *lang.Node, k *registry.Kind) []*Setting {
	settings, rest := l.settings(bodyItems(body), k)
	for _, n := range rest {
		l.notAllowed(n, k)
	}
	return settings
}

// notAllowed reports an item a body of kind k does not accept.
func (l *lowerer) notAllowed(n *lang.Node, k *registry.Kind) {
	what := "a " + n.Kind()
	name := ""
	switch n.Kind() {
	case "block":
		what, name = "a block", n.Field("kind").Text()
	case "row":
		what, name = "a row", n.Field("name").Text()
	}
	if k != nil {
		what += " is not allowed in `" + k.Name + "`"
	} else {
		what += " is not allowed here"
	}
	d := diag.New(diag.CodeUnknownField, span(n), "%s", what)
	if name != "" && k != nil {
		cands := k.FieldNames()
		if n.Kind() == "row" {
			cands = k.RowNames()
		}
		if s, ok := registry.Suggest(name, cands); ok {
			nm := n.Field("name")
			if n.Kind() == "block" {
				nm = n.Field("kind")
			}
			d = d.WithEdit("write "+s, span(nm), s)
		}
	}
	l.report(d)
}

// settings validates the setting nodes of a body against kind k and returns
// the accepted ones and the nodes that are not settings.
func (l *lowerer) settings(items []*lang.Node, k *registry.Kind) ([]*Setting, []*lang.Node) {
	var out []*Setting
	var rest []*lang.Node
	seen := map[string]*Setting{}
	for _, n := range items {
		if n.Kind() != "setting" {
			rest = append(rest, n)
			continue
		}
		nm := n.Field("name")
		s := &Setting{Name: nm.Text(), NameSpan: span(nm), Span: span(n)}
		if v := n.Field("value"); v != nil {
			switch v.Kind() {
			case "range":
				s.Range = l.rng(v)
			case "source_value":
				l.report(diag.New(diag.CodeType, span(v), "`%s` does not take a nested definition", s.Name))
				continue
			default:
				s.Value = l.expr(v)
			}
		}
		if first, dup := seen[s.Name]; dup {
			l.report(diag.New(diag.CodeDuplicate, s.NameSpan, "`%s` is set twice in one body", s.Name).WithRelated(first.NameSpan, "first setting"))
			continue
		}
		seen[s.Name] = s
		if l.geom && k != nil && k.Name == "output" && (s.Name == "for_each" || s.Name == "mounts") {
			l.skip("`" + s.Name + "` settings of outputs")
			continue
		}
		if l.geom && s.Name == "cell" && k != nil && (k.Name == "part" || k.IsShape) {
			l.skip("`cell` settings (atlas cells)")
			continue
		}
		if k != nil && !k.Open {
			f, ok := k.Field(s.Name)
			if !ok {
				d := diag.New(diag.CodeUnknownField, s.NameSpan, "`%s` has no field `%s`", k.Name, s.Name)
				if sug, ok := registry.Suggest(s.Name, k.FieldNames()); ok {
					d = d.WithEdit("write "+sug, s.NameSpan, sug)
				}
				l.report(d)
				continue
			}
			if f.Unsupported != "" {
				if l.geom && lookField(f.Unsupported) {
					l.skip("`" + s.Name + "` settings (" + f.Unsupported + ")")
					continue
				}
				l.report(diag.New(diag.CodeUnsupported, s.NameSpan, "`%s` (%s) is valid in the language but outside this compiler's subset", s.Name, f.Unsupported))
				continue
			}
		} else if k != nil && k.Open {
			if f, ok := k.Field(s.Name); ok && f.Unsupported != "" {
				l.report(diag.New(diag.CodeUnsupported, s.NameSpan, "`%s` (%s) is valid in the language but outside this compiler's subset", s.Name, f.Unsupported))
				continue
			}
		}
		out = append(out, s)
	}
	return out, rest
}

// paramType lowers a param_type node: `length` or `enum(a, b)`.
func paramType(t *lang.Node) ParamType {
	var out ParamType
	if t == nil {
		return out
	}
	out.Span = span(t)
	if nm := t.Field("name"); nm != nil {
		out.Name = nm.Text()
	}
	for _, a := range t.Children() {
		if a.FieldName() == "argument" && a.Kind() == "identifier" {
			out.Enum = append(out.Enum, ident(a))
		}
	}
	return out
}

// template lowers `template name(formals) { items }`.
func (l *lowerer) template(n *lang.Node) *Template {
	t := &Template{Name: ident(n.Field("name")), Span: span(n)}
	for _, c := range n.Children() {
		if c.Kind() != "typed_param" {
			continue
		}
		p := TemplateParam{Name: ident(c.Field("name")), Type: paramType(c.Field("type"))}
		if d := c.Field("default"); d != nil {
			p.Default = l.expr(d)
		}
		t.Params = append(t.Params, p)
	}
	_, t.Items = l.container(n.Field("body"), nil, registry.CtxPart, false)
	return t
}

// plainGrey is the material a geometry-only lowering writes for every material.
func plainGrey(name Ident, sp diag.Span) *Material {
	set := func(field string, v Expr) *Setting {
		return &Setting{Name: field, NameSpan: sp, Span: sp, Value: v}
	}
	return &Material{Name: name, Span: sp, Kind: "pbr", KindSpan: sp, Settings: []*Setting{
		set("base", &ColorLit{base{sp}, units.Color{R: 0x80, G: 0x80, B: 0x80, A: 255}}),
		set("roughness", &NumberLit{base{sp}, "0.5", units.Num(0.5)}),
	}}
}

func (l *lowerer) param(n *lang.Node) *Param {
	p := &Param{Name: ident(n.Field("name")), Span: span(n), Default: l.expr(n.Field("default"))}
	if t := n.Field("type"); t != nil {
		p.Type = paramType(t)
	}
	if r := n.Field("range"); r != nil {
		p.Range = l.rng(r)
	}
	p.Runtime = n.Field("modifier") != nil
	if b := n.Field("body"); b != nil {
		k, _ := registry.Lookup(registry.CtxDocument, "param")
		p.Settings = l.settingsOnly(b, k)
	}
	return p
}

func (l *lowerer) let(n *lang.Node) *Let {
	return &Let{Name: ident(n.Field("name")), Span: span(n), Value: l.expr(n.Field("value"))}
}

func (l *lowerer) check(n *lang.Node) *Check {
	c := &Check{Name: ident(n.Field("name")), Span: span(n)}
	if c.Name.Name != "params" {
		switch c.Name.Name {
		case "rig", "runtime":
			if l.geom {
				l.skip("`check " + c.Name.Name + "`")
				return c
			}
			l.checkBody(c, n.Field("body"), "check "+c.Name.Name)
		default:
			l.report(diag.New(diag.CodeUnknownName, c.Name.Span, "unknown check `%s`", c.Name.Name).
				WithEdit("write params", c.Name.Span, "params"))
		}
		return c
	}
	for _, it := range bodyItems(n.Field("body")) {
		if it.Kind() != "row" || it.Field("name").Text() != "require" {
			if it.Kind() == "row" {
				d := diag.New(diag.CodeUnknownField, span(it.Field("name")), "`check params` holds `require` rows, not `%s`", it.Field("name").Text())
				l.report(d)
			} else {
				l.report(diag.New(diag.CodeUnknownField, span(it), "`check params` holds `require` rows only"))
			}
			continue
		}
		args := l.args(it)
		if len(args) != 2 || args[0].Value == nil || args[1].Value == nil {
			l.report(diag.New(diag.CodeType, span(it), "`require` takes a condition and a message: require (condition, \"message\")"))
			continue
		}
		if _, ok := args[1].Value.(*StringLit); !ok {
			l.report(diag.New(diag.CodeType, args[1].Span, "the message of `require` must be a string"))
			continue
		}
		c.Requires = append(c.Requires, &Require{Span: span(it), Cond: args[0].Value, Message: args[1].Value})
	}
	return c
}

// material lowers `material name = kind { ... }`.
func (l *lowerer) material(name, value *lang.Node, sp diag.Span) *Material {
	if value == nil {
		return nil
	}
	kn := value.Field("kind")
	m := &Material{Name: ident(name), Span: sp, Kind: kn.Text(), KindSpan: span(kn)}
	k, ok := registry.Lookup(registry.CtxDocument, m.Kind)
	if !ok || (m.Kind != "pbr" && m.Kind != "selena") {
		if m.Kind == "pbr" || m.Kind == "selena" {
			return m
		}
		d := diag.New(diag.CodeUnknownName, m.KindSpan, "unknown material kind `%s`", m.Kind)
		if s, ok := registry.Suggest(m.Kind, []string{"pbr", "selena"}); ok {
			d = d.WithEdit("write "+s, m.KindSpan, s)
		}
		l.report(d)
		return nil
	}
	items := bodyItems(value.Field("body"))
	// A selena material's inline fallback is a nested pbr definition.
	var plain []*lang.Node
	for _, it := range items {
		if m.Kind == "selena" && it.Kind() == "setting" && it.Field("name").Text() == "fallback" {
			v := it.Field("value")
			if v != nil && v.Kind() == "source_value" && v.Field("kind").Text() == "pbr" {
				if m.Fallback != nil {
					l.report(diag.New(diag.CodeDuplicate, span(it.Field("name")), "`fallback` is set twice").WithRelated(m.Fallback.Span, "first fallback"))
					continue
				}
				m.Fallback = l.material(it.Field("name"), v, span(it))
				continue
			}
			l.report(diag.New(diag.CodeType, span(it), "`fallback` must be an inline `pbr { ... }`"))
			continue
		}
		plain = append(plain, it)
	}
	settings, rest := l.settings(plain, k)
	for _, n := range rest {
		l.notAllowed(n, k)
	}
	m.Settings = settings
	return m
}

func (l *lowerer) budget(body *lang.Node, sp diag.Span) *Budget {
	k, _ := registry.Lookup(registry.CtxDocument, "budget")
	return &Budget{Span: sp, Settings: l.settingsOnly(body, k)}
}

func (l *lowerer) output(n *lang.Node) *Output {
	k, _ := registry.Lookup(registry.CtxDocument, "output")
	o := &Output{Name: ident(n.Field("name")), Span: span(n)}
	var blocks []*lang.Node
	settings, rest := l.settings(bodyItems(n.Field("body")), k)
	o.Settings = settings
	for _, it := range rest {
		if it.Kind() != "block" {
			l.notAllowed(it, k)
			continue
		}
		blocks = append(blocks, it)
	}
	for _, b := range blocks {
		kn := b.Field("kind")
		switch kn.Text() {
		case "set":
			if o.Set != nil || o.SetSpan.Len() > 0 {
				l.report(diag.New(diag.CodeDuplicate, span(b), "an output has one `set` block").WithRelated(o.SetSpan, "first set block"))
				continue
			}
			sk, _ := registry.Lookup(registry.CtxDocument, "set")
			o.SetSpan = span(b)
			o.Set = l.settingsOnly(b.Field("body"), sk)
		case "budget":
			if o.Budget != nil {
				l.report(diag.New(diag.CodeDuplicate, span(b), "an output has one budget block").WithRelated(o.Budget.Span, "first budget block"))
				continue
			}
			o.Budget = l.budget(b.Field("body"), span(b))
		default:
			l.notAllowed(b, k)
		}
	}
	return o
}

func (l *lowerer) accept(n *lang.Node) *Accept {
	k, _ := registry.Lookup(registry.CtxDocument, "accept")
	a := &Accept{Name: ident(n.Field("name")), Span: span(n)}
	settings, rest := l.settings(bodyItems(n.Field("body")), k)
	a.Settings = settings
	a.Items = l.acceptItems(rest)
	return a
}

func (l *lowerer) acceptItems(nodes []*lang.Node) []AcceptItem {
	var out []AcceptItem
	for _, it := range nodes {
		switch it.Kind() {
		case "row":
			if it.Field("name").Text() != "expect" {
				l.report(diag.New(diag.CodeUnknownField, span(it.Field("name")), "an acceptance file holds `expect` rows, not `%s`", it.Field("name").Text()))
				continue
			}
			args := l.args(it)
			if len(args) != 2 || args[0].Value == nil || args[1].Value == nil {
				l.report(diag.New(diag.CodeType, span(it), "`expect` takes a condition and a message"))
				continue
			}
			out = append(out, &Expect{Span: span(it), Cond: args[0].Value, Message: args[1].Value})
		case "when_block":
			w := &AcceptWhen{Span: span(it), Cond: l.expr(it.Field("condition"))}
			w.Then = l.acceptItems(bodyItems(it.Field("body")))
			if alt := it.Field("alternative"); alt != nil {
				if alt.Kind() == "when_block" {
					w.Else = l.acceptItems([]*lang.Node{alt})
				} else {
					w.Else = l.acceptItems(bodyItems(alt))
				}
			}
			out = append(out, w)
		default:
			l.report(diag.New(diag.CodeUnknownField, span(it), "an acceptance file holds `expect` rows and `when` blocks"))
		}
	}
	return out
}
