package tools

import (
	"strconv"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
)

// site names the declaration that holds a use: kind "param", "let", "part",
// "shape", "output" and so on.
type site struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// declared is one declaration found in a file.
type declared struct {
	Kind string
	Name string
	// Span is the identifier; Whole is the whole declaration.
	Span  diag.Span
	Whole diag.Span
	// Parent is the part that holds the declaration, "" at the top level.
	Parent string
	// Top is true for a declaration outside any part body.
	Top bool
}

// reference is one place that names something.
type reference struct {
	Name  string
	Span  diag.Span
	In    site
	Field string
}

// index lists every declaration and every name reference of a file, in source
// order within each group. Scope is not tracked: a local let or a sample
// variable that shares a name with a parameter counts as a use of both.
type index struct {
	decls []declared
	refs  []reference
}

type builder struct {
	ix *index
}

func buildIndex(f *ast.File) *index {
	b := &builder{ix: &index{}}
	b.file(f)
	return b.ix
}

func (b *builder) decl(kind string, id ast.Ident, whole diag.Span, parent string, top bool) {
	if id.Name == "" {
		return
	}
	b.ix.decls = append(b.ix.decls, declared{Kind: kind, Name: id.Name, Span: id.Span, Whole: whole, Parent: parent, Top: top})
}

func (b *builder) ref(name string, sp diag.Span, in site, field string) {
	b.ix.refs = append(b.ix.refs, reference{Name: name, Span: sp, In: in, Field: field})
}

func (b *builder) file(f *ast.File) {
	if f.Asset != nil {
		b.decl("asset", f.Asset.Name, f.Asset.Span, "", true)
		b.settings(f.Asset.Settings, site{"asset", f.Asset.Name.Name}, "")
	}
	for _, im := range f.Imports {
		b.decl("import", ast.Ident{Name: im.Qualifier, Span: im.Span}, im.Span, "", true)
	}
	for _, p := range f.Params {
		in := site{"param", p.Name.Name}
		b.decl("param", p.Name, p.Span, "", true)
		b.expr(p.Default, in, "default")
		b.rng(p.Range, in, "range")
		b.settings(p.Settings, in, "")
	}
	for _, l := range f.Lets {
		b.decl("let", l.Name, l.Span, "", true)
		b.expr(l.Value, site{"let", l.Name.Name}, "")
	}
	for _, c := range f.Checks {
		in := site{"check", c.Name.Name}
		for _, r := range c.Requires {
			b.expr(r.Cond, in, "require")
			b.expr(r.Message, in, "require")
		}
		b.settings(c.Settings, in, "")
		for _, cc := range c.Cases {
			b.decl("case", cc.Name, cc.Span, "", true)
			b.settings(cc.Settings, in, cc.Name.Name)
			b.setBlock(cc.Set, in)
		}
	}
	for _, m := range f.Materials {
		b.material(m)
	}
	for _, t := range f.Tables {
		b.decl("table", t.Name, t.Span, "", true)
		b.settings(t.Settings, site{"table", t.Name.Name}, "")
	}
	for _, m := range f.Mounts {
		b.decl("mount", m.Name, m.Span, "", true)
		in := site{"mount", m.Name.Name}
		b.settings(m.Settings, in, "")
		b.settings(m.Fit, in, "fit")
	}
	for _, t := range f.Templates {
		in := site{"template", t.Name.Name}
		b.decl("template", t.Name, t.Span, "", true)
		for _, p := range t.Params {
			b.expr(p.Default, in, p.Name.Name)
		}
		b.items(t.Items, in, "")
	}
	for _, p := range f.Painters {
		in := site{"painter", p.Name.Name}
		b.decl("painter", p.Name, p.Span, "", true)
		for _, tp := range p.Params {
			b.expr(tp.Default, in, tp.Name.Name)
		}
		for _, l := range p.Lets {
			b.expr(l.Value, in, l.Name.Name)
		}
		b.expr(p.Color, in, "color")
	}
	for _, t := range f.Textures {
		in := site{"texture", t.Name.Name}
		b.decl("texture", t.Name, t.Span, "", true)
		b.settings(t.Settings, in, "")
		for _, c := range t.Cells {
			b.decl("cell", c.Name, c.Span, t.Name.Name, true)
			b.settings(c.Settings, in, c.Name.Name)
		}
	}
	for _, p := range f.Plugs {
		b.decl("plug", p.Name, p.Span, "", true)
		b.settings(p.Settings, site{"plug", p.Name.Name}, "")
	}
	for _, p := range f.Parts {
		b.part(p, "", true)
	}
	for _, l := range f.Lods {
		in := site{"lod", strconv.Itoa(l.Level)}
		b.settings(l.Settings, in, "")
	}
	if f.Collision != nil {
		b.items(f.Collision.Items, site{"collision", ""}, "")
	}
	if f.Budget != nil {
		b.settings(f.Budget.Settings, site{"budget", ""}, "")
	}
	for _, o := range f.Outputs {
		in := site{"output", o.Name.Name}
		b.decl("output", o.Name, o.Span, "", true)
		b.settings(o.Settings, in, "")
		b.setBlock(o.Set, in)
		if o.Budget != nil {
			b.settings(o.Budget.Settings, in, "budget")
		}
	}
	for _, r := range f.Rigs {
		in := site{r.Kind, r.Name.Name}
		b.decl(r.Kind, r.Name, r.Span, "", true)
		b.settings(r.Settings, in, "")
		b.rigItems(r.Items, in, r.Name.Name)
	}
}

// setBlock records a `set` block: each name is a use of the parameter it
// sets, and each value is an expression.
func (b *builder) setBlock(ss []*ast.Setting, in site) {
	for _, s := range ss {
		b.ref(s.Name, s.NameSpan, in, "set")
		b.expr(s.Value, in, "set "+s.Name)
		b.rng(s.Range, in, "set "+s.Name)
	}
}

func (b *builder) material(m *ast.Material) {
	if m == nil {
		return
	}
	b.decl("material", m.Name, m.Span, "", true)
	b.settings(m.Settings, site{"material", m.Name.Name}, "")
	if m.Fallback != nil {
		b.settings(m.Fallback.Settings, site{"material", m.Name.Name}, "fallback")
	}
}

func (b *builder) part(p *ast.Part, parent string, top bool) {
	b.decl("part", p.Name, p.Span, parent, top)
	if p.Pair.Name != "" {
		b.decl("part", p.Pair, p.Span, parent, top)
	}
	in := site{"part", p.Name.Name}
	b.settings(p.Settings, in, "")
	b.items(p.Items, in, p.Name.Name)
}

func (b *builder) items(items []ast.Item, in site, parent string) {
	for _, it := range items {
		switch n := it.(type) {
		case *ast.Part:
			b.part(n, parent, false)
		case *ast.Shape:
			si := in
			if n.Name.Name != "" {
				b.decl("shape", n.Name, n.Span, parent, false)
				si = site{"shape", n.Name.Name}
			}
			b.settings(n.Settings, si, n.Kind)
			b.rows(n.Rows, si, n.Kind)
			for _, bl := range n.Blocks {
				b.settings(bl.Settings, si, n.Kind+"."+bl.Kind)
				b.rows(bl.Rows, si, n.Kind+"."+bl.Kind)
			}
		case *ast.Array:
			ai := in
			if n.Name.Name != "" {
				b.decl("array", n.Name, n.Span, parent, false)
				ai = site{"array", n.Name.Name}
			}
			b.settings(n.Settings, ai, "array")
			b.items(n.Items, ai, parent)
		case *ast.Instance:
			b.settings(n.Settings, in, "instance")
		case *ast.Mirror:
			b.settings(n.Settings, in, "mirror")
			b.items(n.Items, in, parent)
		case *ast.Socket:
			b.decl("socket", n.Name, n.Span, parent, false)
			if n.Pair.Name != "" {
				b.decl("socket", n.Pair, n.Span, parent, false)
			}
			b.settings(n.Settings, site{"socket", n.Name.Name}, "")
		case *ast.When:
			b.expr(n.Cond, in, "when")
			b.items(n.Then, in, parent)
			b.items(n.Else, in, parent)
		case *ast.Let:
			b.decl("let", n.Name, n.Span, parent, false)
			b.expr(n.Value, site{"let", n.Name.Name}, "")
		case *ast.TemplateCall:
			b.ref(n.Name, n.NameSpan, in, "call")
			b.args(n.Args, in, n.Name)
		case *ast.Collider:
			b.decl("collider", n.Name, n.Span, parent, false)
			b.settings(n.Settings, site{"collider", n.Name.Name}, n.Kind)
		}
	}
}

func (b *builder) rigItems(items []ast.RigItem, in site, parent string) {
	for _, it := range items {
		switch n := it.(type) {
		case *ast.RigBlock:
			if n.Name.Name != "" {
				b.decl(n.Kind, n.Name, n.Span, parent, false)
			}
			if n.Pair.Name != "" {
				b.decl(n.Kind, n.Pair, n.Span, parent, false)
			}
			b.settings(n.Settings, in, n.Kind)
			b.rigItems(n.Items, in, parent)
		case *ast.Row:
			b.args(n.Args, in, n.Name)
		case *ast.SampleRow:
			b.sample(n, in, "sample")
		}
	}
}

func (b *builder) rows(rows []ast.RowItem, in site, field string) {
	for _, r := range rows {
		switch n := r.(type) {
		case *ast.Row:
			b.args(n.Args, in, field+"."+n.Name)
		case *ast.SampleRow:
			b.sample(n, in, field)
		}
	}
}

func (b *builder) sample(n *ast.SampleRow, in site, field string) {
	b.rng(n.Range, in, field)
	b.expr(n.Count, in, field)
	if n.Row != nil {
		b.args(n.Row.Args, in, field+"."+n.Row.Name)
	}
}

func (b *builder) settings(ss []*ast.Setting, in site, prefix string) {
	for _, s := range ss {
		field := s.Name
		if prefix != "" {
			field = prefix + "." + s.Name
		}
		b.expr(s.Value, in, field)
		b.rng(s.Range, in, field)
	}
}

func (b *builder) args(args []ast.Arg, in site, field string) {
	for _, a := range args {
		f := field
		if a.Name != "" {
			f = field + "." + a.Name
		}
		b.expr(a.Value, in, f)
		b.rng(a.Range, in, f)
	}
}

func (b *builder) rng(r *ast.Range, in site, field string) {
	if r == nil {
		return
	}
	b.expr(r.Lo, in, field)
	b.expr(r.Hi, in, field)
}

func (b *builder) expr(e ast.Expr, in site, field string) {
	switch n := e.(type) {
	case nil:
	case *ast.Name:
		b.ref(n.Name, n.Span(), in, field)
	case *ast.Qualified:
		if len(n.Parts) > 0 {
			sp := n.Span()
			b.ref(n.Parts[0], diag.Span{Start: sp.Start, End: sp.Start + len(n.Parts[0])}, in, field)
		}
	case *ast.Tuple:
		for _, x := range n.Elems {
			b.expr(x, in, field)
		}
	case *ast.List:
		for _, x := range n.Elems {
			b.expr(x, in, field)
		}
	case *ast.Unary:
		b.expr(n.X, in, field)
	case *ast.Binary:
		b.expr(n.L, in, field)
		b.expr(n.R, in, field)
	case *ast.If:
		b.expr(n.Cond, in, field)
		b.expr(n.Then, in, field)
		b.expr(n.Else, in, field)
	case *ast.Call:
		b.ref(n.Func, n.FuncSpan, in, field)
		b.args(n.Args, in, field)
	case *ast.Index:
		b.expr(n.X, in, field)
		b.expr(n.I, in, field)
	case *ast.Member:
		b.expr(n.X, in, field)
	case *ast.Paren:
		b.expr(n.X, in, field)
	}
}
