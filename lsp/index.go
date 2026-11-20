package lsp

import (
	"sort"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
)

// decl is one declared name.
type decl struct {
	Kind string // "param", "let", "part", "material", "template", ...
	Name string
	// Span is the identifier; Whole is the whole declaration.
	Span  diag.Span
	Whole diag.Span
	// Scope is where the name is visible: the whole file for a top-level
	// name, the enclosing body for a local let, a template parameter or a
	// sample variable.
	Scope diag.Span
	Top   bool
}

// ref is one use of a name. Target is the index of its declaration in
// index.decls, or -1 when no declaration in scope matches.
type ref struct {
	Name   string
	Span   diag.Span
	Field  string
	Target int
}

// fieldUse is the name of a setting and the kind of block that holds it.
type fieldUse struct {
	Span diag.Span
	Kind string
	Name string
}

// index lists the declarations, name uses and setting names of one file. It
// resolves a use by name within the scope that holds it, and prefers the kind
// of declaration the slot asks for (`material = x` wants a material). It does
// not read library files: a name from an import stays unresolved here.
type index struct {
	decls  []decl
	refs   []ref
	fields []fieldUse
	size   int
	byPos  []int // indices of decls, by identifier position
}

type indexer struct {
	ix    *index
	local diag.Span // the innermost local scope
}

func buildIndex(f *ast.File) *index {
	b := &indexer{ix: &index{size: len(f.Source)}}
	b.local = diag.Span{Start: 0, End: len(f.Source)}
	b.file(f)
	b.resolve()
	return b.ix
}

func (b *indexer) global() diag.Span { return diag.Span{Start: 0, End: b.ix.size} }

func (b *indexer) decl(kind string, id ast.Ident, whole diag.Span, top bool) {
	if id.Name == "" {
		return
	}
	scope := b.global()
	if !top {
		scope = b.local
	}
	b.ix.decls = append(b.ix.decls, decl{Kind: kind, Name: id.Name, Span: id.Span, Whole: whole, Scope: scope, Top: top})
}

func (b *indexer) localDecl(kind string, id ast.Ident, whole, scope diag.Span) {
	if id.Name == "" {
		return
	}
	b.ix.decls = append(b.ix.decls, decl{Kind: kind, Name: id.Name, Span: id.Span, Whole: whole, Scope: scope})
}

func (b *indexer) ref(name string, sp diag.Span, field string) {
	if name == "" {
		return
	}
	b.ix.refs = append(b.ix.refs, ref{Name: name, Span: sp, Field: field, Target: -1})
}

func (b *indexer) file(f *ast.File) {
	if f.Asset != nil {
		b.decl("asset", f.Asset.Name, f.Asset.Span, true)
		b.settings(f.Asset.Settings, "asset", "asset")
	}
	if f.Meta != nil {
		b.settings(f.Meta.Settings, "meta", "meta")
	}
	for _, im := range f.Imports {
		sp := im.QualSpan
		if sp.Len() == 0 {
			sp = im.Span
		}
		b.decl("import", ast.Ident{Name: im.Qualifier, Span: sp}, im.Span, true)
	}
	for _, p := range f.Params {
		b.decl("param", p.Name, p.Span, true)
		b.expr(p.Default, "default")
		b.rng(p.Range, "range")
		b.settings(p.Settings, "param", "doc")
	}
	for _, l := range f.Lets {
		b.decl("let", l.Name, l.Span, true)
		b.expr(l.Value, "let")
	}
	for _, c := range f.Checks {
		for _, r := range c.Requires {
			b.expr(r.Cond, "require")
			b.expr(r.Message, "require")
		}
		b.settings(c.Settings, "", "check")
		for _, cc := range c.Cases {
			b.decl("case", cc.Name, cc.Span, true)
			b.settings(cc.Settings, "", "case")
			b.setBlock(cc.Set)
		}
	}
	for _, m := range f.Materials {
		b.decl("material", m.Name, m.Span, true)
		b.settings(m.Settings, m.Kind, "material")
		if m.Fallback != nil {
			b.settings(m.Fallback.Settings, "pbr", "fallback")
		}
	}
	for _, t := range f.Tables {
		b.decl("table", t.Name, t.Span, true)
		b.settings(t.Settings, "", "table")
	}
	for _, m := range f.Mounts {
		b.decl("mount", m.Name, m.Span, true)
		b.settings(m.Settings, "mount", "mount")
		b.settings(m.Fit, "", "fit")
		b.setBlock(m.Set)
	}
	for _, t := range f.Templates {
		b.decl("template", t.Name, t.Span, true)
		saved := b.local
		b.local = t.Span
		for _, p := range t.Params {
			b.localDecl("tparam", p.Name, p.Name.Span, t.Span)
			b.expr(p.Default, p.Name.Name)
		}
		b.items(t.Items)
		b.local = saved
	}
	for _, p := range f.Painters {
		b.decl("painter", p.Name, p.Span, true)
		saved := b.local
		b.local = p.Span
		for _, tp := range p.Params {
			b.localDecl("tparam", tp.Name, tp.Name.Span, p.Span)
			b.expr(tp.Default, tp.Name.Name)
		}
		for _, l := range p.Lets {
			b.localDecl("let", l.Name, l.Span, p.Span)
			b.expr(l.Value, l.Name.Name)
		}
		b.expr(p.Color, "color")
		b.local = saved
	}
	for _, t := range f.Textures {
		b.decl("texture", t.Name, t.Span, true)
		b.settings(t.Settings, t.Kind, "texture")
		for _, c := range t.Cells {
			b.decl("cell", c.Name, c.Span, true)
			b.settings(c.Settings, "cell", "cell")
		}
	}
	for _, p := range f.Plugs {
		b.decl("plug", p.Name, p.Span, true)
		b.settings(p.Settings, "plug", "plug")
	}
	for _, p := range f.Parts {
		b.part(p, true)
	}
	for _, l := range f.Lods {
		b.settings(l.Settings, "lod", "lod")
	}
	if f.Collision != nil {
		b.items(f.Collision.Items)
	}
	if f.Budget != nil {
		b.settings(f.Budget.Settings, "budget", "budget")
	}
	for _, o := range f.Outputs {
		b.decl("output", o.Name, o.Span, true)
		b.settings(o.Settings, "output", "output")
		b.setBlock(o.Set)
		if o.Budget != nil {
			b.settings(o.Budget.Settings, "budget", "budget")
		}
	}
	for _, r := range f.Rigs {
		b.decl(r.Kind, r.Name, r.Span, true)
		b.settings(r.Settings, r.Kind, r.Kind)
		b.rigItems(r.Items)
	}
}

// setBlock records a `set` block: each name is a use of the parameter it
// sets.
func (b *indexer) setBlock(ss []*ast.Setting) {
	for _, s := range ss {
		b.ref(s.Name, s.NameSpan, "set")
		b.expr(s.Value, "set")
		b.rng(s.Range, "set")
	}
}

func (b *indexer) part(p *ast.Part, top bool) {
	b.decl("part", p.Name, p.Span, true)
	if p.Pair.Name != "" {
		b.decl("part", p.Pair, p.Span, true)
	}
	saved := b.local
	b.local = p.Span
	b.settings(p.Settings, "part", "part")
	b.items(p.Items)
	b.local = saved
}

func (b *indexer) items(items []ast.Item) {
	for _, it := range items {
		switch n := it.(type) {
		case *ast.Part:
			b.part(n, false)
		case *ast.Shape:
			b.decl("shape", n.Name, n.Span, true)
			b.settings(n.Settings, n.Kind, n.Kind)
			b.rows(n.Rows)
			for _, bl := range n.Blocks {
				b.settings(bl.Settings, bl.Kind, bl.Kind)
				b.rows(bl.Rows)
			}
		case *ast.Array:
			b.decl("array", n.Name, n.Span, true)
			saved := b.local
			b.local = n.Span
			for _, s := range n.Settings {
				if nm, ok := s.Value.(*ast.Name); ok && s.Name == "index" {
					b.localDecl("index", ast.Ident{Name: nm.Name, Span: nm.Span()}, nm.Span(), n.Span)
					b.field("array", s)
					continue
				}
				b.setting("array", s, "array")
			}
			b.items(n.Items)
			b.local = saved
		case *ast.Instance:
			b.settings(n.Settings, "instance", "instance")
		case *ast.Mirror:
			b.settings(n.Settings, "mirror", "mirror")
			b.items(n.Items)
		case *ast.Socket:
			b.decl("socket", n.Name, n.Span, true)
			if n.Pair.Name != "" {
				b.decl("socket", n.Pair, n.Span, true)
			}
			b.settings(n.Settings, "socket", "socket")
		case *ast.When:
			b.expr(n.Cond, "when")
			b.items(n.Then)
			b.items(n.Else)
		case *ast.Let:
			b.decl("let", n.Name, n.Span, false)
			b.expr(n.Value, "let")
		case *ast.TemplateCall:
			b.ref(n.Name, n.NameSpan, "call")
			b.args(n.Args, n.Name)
		case *ast.Collider:
			b.decl("collider", n.Name, n.Span, true)
			b.settings(n.Settings, n.Kind, n.Kind)
		}
	}
}

func (b *indexer) rigItems(items []ast.RigItem) {
	for _, it := range items {
		switch n := it.(type) {
		case *ast.RigBlock:
			b.decl(n.Kind, n.Name, n.Span, true)
			if n.Pair.Name != "" {
				b.decl(n.Kind, n.Pair, n.Span, true)
			}
			b.settings(n.Settings, n.Kind, n.Kind)
			b.rigItems(n.Items)
		case *ast.Row:
			b.args(n.Args, n.Name)
		case *ast.SampleRow:
			b.sample(n)
		}
	}
}

func (b *indexer) rows(rows []ast.RowItem) {
	for _, r := range rows {
		switch n := r.(type) {
		case *ast.Row:
			b.args(n.Args, n.Name)
		case *ast.SampleRow:
			b.sample(n)
		}
	}
}

// sample declares the sample variable for the row it drives.
func (b *indexer) sample(n *ast.SampleRow) {
	b.localDecl("variable", n.Var, n.Span, n.Span)
	b.rng(n.Range, "sample")
	b.expr(n.Count, "sample")
	if n.Row != nil {
		b.args(n.Row.Args, n.Row.Name)
	}
}

func (b *indexer) field(kind string, s *ast.Setting) {
	if kind != "" && s.NameSpan.Len() > 0 {
		b.ix.fields = append(b.ix.fields, fieldUse{Span: s.NameSpan, Kind: kind, Name: s.Name})
	}
}

func (b *indexer) setting(kind string, s *ast.Setting, prefix string) {
	b.field(kind, s)
	field := s.Name
	if prefix != "" {
		field = prefix + "." + s.Name
	}
	b.expr(s.Value, field)
	b.rng(s.Range, field)
}

func (b *indexer) settings(ss []*ast.Setting, kind, prefix string) {
	for _, s := range ss {
		b.setting(kind, s, prefix)
	}
}

func (b *indexer) args(args []ast.Arg, field string) {
	for _, a := range args {
		b.expr(a.Value, field)
		b.rng(a.Range, field)
	}
}

func (b *indexer) rng(r *ast.Range, field string) {
	if r == nil {
		return
	}
	b.expr(r.Lo, field)
	b.expr(r.Hi, field)
}

func (b *indexer) expr(e ast.Expr, field string) {
	switch n := e.(type) {
	case nil:
	case *ast.Name:
		b.ref(n.Name, n.Span(), field)
	case *ast.Qualified:
		if len(n.Parts) > 0 {
			sp := n.Span()
			b.ref(n.Parts[0], diag.Span{Start: sp.Start, End: sp.Start + len(n.Parts[0])}, field)
		}
	case *ast.Tuple:
		for _, x := range n.Elems {
			b.expr(x, field)
		}
	case *ast.List:
		for _, x := range n.Elems {
			b.expr(x, field)
		}
	case *ast.Unary:
		b.expr(n.X, field)
	case *ast.Binary:
		b.expr(n.L, field)
		b.expr(n.R, field)
	case *ast.If:
		b.expr(n.Cond, field)
		b.expr(n.Then, field)
		b.expr(n.Else, field)
	case *ast.Call:
		b.ref(n.Func, n.FuncSpan, "func")
		b.args(n.Args, field)
	case *ast.Index:
		b.expr(n.X, field)
		b.expr(n.I, field)
	case *ast.Member:
		b.expr(n.X, field)
	case *ast.Paren:
		b.expr(n.X, field)
	}
}

// valueKinds are the declarations an expression can read as a value.
var valueKinds = []string{"variable", "index", "tparam", "let", "param"}

// wantedKinds returns the declaration kinds a slot reads, best first. An empty
// result means any kind.
func wantedKinds(field string) []string {
	last := field
	if i := strings.LastIndexByte(field, '.'); i >= 0 {
		last = field[i+1:]
	}
	switch last {
	case "material", "fallback":
		return []string{"material"}
	case "of", "root", "drop":
		return []string{"part", "shape", "socket"}
	case "call":
		return []string{"template", "painter"}
	case "set":
		return []string{"param"}
	case "func":
		return []string{"template", "painter"}
	}
	return valueKinds
}

// resolve points each use at the declaration it names. Among the declarations
// of that name whose scope holds the use, the kind the slot wants comes first
// (earlier in its list is better), then the narrower scope. A declaration of
// another kind is a last resort, except for kinds that live in a namespace of
// their own.
func (b *indexer) resolve() {
	byName := map[string][]int{}
	for i, d := range b.ix.decls {
		byName[d.Name] = append(byName[d.Name], i)
	}
	for i := range b.ix.refs {
		r := &b.ix.refs[i]
		want := wantedKinds(r.Field)
		best, bestRank := -1, 0
		for _, di := range byName[r.Name] {
			d := b.ix.decls[di]
			if r.Span.Start < d.Scope.Start || r.Span.End > d.Scope.End {
				continue
			}
			rank := len(want)
			for k, kind := range want {
				if d.Kind == kind {
					rank = k
					break
				}
			}
			if rank == len(want) && !anyKind(d.Kind) {
				continue
			}
			if best < 0 || rank < bestRank || rank == bestRank && d.Scope.Len() < b.ix.decls[best].Scope.Len() {
				best, bestRank = di, rank
			}
		}
		r.Target = best
	}
	sort.SliceStable(b.ix.refs, func(i, j int) bool { return b.ix.refs[i].Span.Start < b.ix.refs[j].Span.Start })
	sort.SliceStable(b.ix.fields, func(i, j int) bool { return b.ix.fields[i].Span.Start < b.ix.fields[j].Span.Start })
	b.ix.byPos = make([]int, len(b.ix.decls))
	for i := range b.ix.byPos {
		b.ix.byPos[i] = i
	}
	sort.SliceStable(b.ix.byPos, func(i, j int) bool {
		return b.ix.decls[b.ix.byPos[i]].Span.Start < b.ix.decls[b.ix.byPos[j]].Span.Start
	})
}

// anyKind reports whether a declaration of this kind may be the target of a
// use whose slot named other kinds.
func anyKind(kind string) bool {
	switch kind {
	case "asset", "output", "case", "import":
		return false
	}
	return true
}

// refAt returns the index of the use that holds the offset, or -1.
func (ix *index) refAt(off int) int {
	i := sort.Search(len(ix.refs), func(i int) bool { return ix.refs[i].Span.End >= off })
	for ; i < len(ix.refs) && ix.refs[i].Span.Start <= off; i++ {
		if off >= ix.refs[i].Span.Start && off <= ix.refs[i].Span.End {
			return i
		}
	}
	return -1
}

// declAt returns the index of the declaration whose name holds the offset, or -1.
func (ix *index) declAt(off int) int {
	i := sort.Search(len(ix.byPos), func(i int) bool { return ix.decls[ix.byPos[i]].Span.End >= off })
	for ; i < len(ix.byPos) && ix.decls[ix.byPos[i]].Span.Start <= off; i++ {
		d := ix.decls[ix.byPos[i]]
		if off >= d.Span.Start && off <= d.Span.End {
			return ix.byPos[i]
		}
	}
	return -1
}

// fieldAt returns the setting name that holds the offset.
func (ix *index) fieldAt(off int) (fieldUse, bool) {
	i := sort.Search(len(ix.fields), func(i int) bool { return ix.fields[i].Span.End >= off })
	if i < len(ix.fields) && ix.fields[i].Span.Start <= off {
		return ix.fields[i], true
	}
	return fieldUse{}, false
}
