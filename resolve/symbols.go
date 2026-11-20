package resolve

import (
	"sort"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
)

// decl is a declared name in one namespace.
type decl struct {
	name     string
	span     diag.Span
	part     *ast.Part // set for a part
	topLevel bool
	root     bool
}

// symbols holds the declared names of each namespace. Names are bound before
// any expansion or evaluation, so a name under a `when` that is not taken
// still resolves.
type symbols struct {
	ns    map[registry.Namespace]map[string]*decl
	order map[registry.Namespace][]string
}

func (s *symbols) names(ns registry.Namespace) []string {
	out := append([]string(nil), s.order[ns]...)
	sort.Strings(out)
	return out
}

func (s *symbols) lookup(ns registry.Namespace, name string) *decl { return s.ns[ns][name] }

func (r *run) declare() *symbols {
	s := &symbols{ns: map[registry.Namespace]map[string]*decl{}, order: map[registry.Namespace][]string{}}
	add := func(ns registry.Namespace, name string, sp diag.Span, d *decl) {
		if name == "" {
			return
		}
		if s.ns[ns] == nil {
			s.ns[ns] = map[string]*decl{}
		}
		if prev, dup := s.ns[ns][name]; dup {
			r.report(diag.New(diag.CodeDuplicate, sp, "the %s name `%s` is declared twice", ns, name).WithRelated(prev.span, "first declaration"))
			return
		}
		d.name, d.span = name, sp
		s.ns[ns][name] = d
		s.order[ns] = append(s.order[ns], name)
	}
	f := r.f

	// Document values: parameters and top-level lets share one namespace.
	vals := map[string]diag.Span{}
	addVal := func(id ast.Ident) {
		if prev, dup := vals[id.Name]; dup {
			r.report(diag.New(diag.CodeDuplicate, id.Span, "the name `%s` is declared twice", id.Name).WithRelated(prev, "first declaration"))
			return
		}
		vals[id.Name] = id.Span
	}
	for _, p := range f.Params {
		addVal(p.Name)
	}
	for _, l := range f.Lets {
		addVal(l.Name)
	}
	for _, m := range f.Materials {
		add(registry.NsMaterial, m.Name.Name, m.Name.Span, &decl{})
	}
	outFiles := map[string]diag.Span{}
	for _, o := range f.Outputs {
		add(registry.NsOutput, o.Name.Name, o.Name.Span, &decl{})
		if fs := ast.FindSetting(o.Settings, "file"); fs != nil {
			if lit, ok := fs.Value.(*ast.StringLit); ok {
				if prev, dup := outFiles[lit.Value]; dup {
					r.report(diag.New(diag.CodeDuplicate, lit.Span(), "two outputs write the file %q", lit.Value).WithRelated(prev, "first output with this file"))
				} else {
					outFiles[lit.Value] = lit.Span()
				}
			}
		}
	}
	rootName := ""
	if f.Asset != nil {
		if rs := ast.FindSetting(f.Asset.Settings, "root"); rs != nil {
			if n, ok := rs.Value.(*ast.Name); ok {
				rootName = n.Name
			}
		}
	}
	for _, p := range f.Parts {
		r.declareItems(s, add, []ast.Item{p}, true, rootName)
	}
	if f.Collision != nil {
		r.declareItems(s, add, f.Collision.Items, false, rootName)
	}
	r.instanceCycles(s)
	return s
}

type adder func(ns registry.Namespace, name string, sp diag.Span, d *decl)

func (r *run) declareItems(s *symbols, add adder, items []ast.Item, top bool, rootName string) {
	pair := func(ns registry.Namespace, a, b ast.Ident, d *decl) {
		add(ns, a.Name, a.Span, d)
		if b.Name != "" {
			add(ns, b.Name, b.Span, &decl{})
		}
	}
	for _, it := range items {
		switch n := it.(type) {
		case *ast.Part:
			d := &decl{part: n, topLevel: top && n.TopLevel, root: top && n.Name.Name == rootName}
			pair(registry.NsGeometry, n.Name, n.Pair, d)
			r.declareItems(s, add, n.Items, false, rootName)
		case *ast.Shape:
			pair(registry.NsGeometry, n.Name, n.Pair, &decl{})
		case *ast.Array:
			pair(registry.NsGeometry, n.Name, n.Pair, &decl{})
			r.declareItems(s, add, n.Items, false, rootName)
		case *ast.Instance:
			pair(registry.NsGeometry, n.Name, n.Pair, &decl{})
		case *ast.Mirror:
			r.declareItems(s, add, n.Items, false, rootName)
		case *ast.Socket:
			pair(registry.NsSocket, n.Name, n.Pair, &decl{})
		case *ast.When:
			r.declareItems(s, add, n.Then, false, rootName)
			r.declareItems(s, add, n.Else, false, rootName)
		case *ast.Collider:
			pair(registry.NsCollider, n.Name, n.Pair, &decl{})
		}
	}
}

// instanceCycles finds top-level parts that place each other through
// `instance { of = ... }`, directly or by a chain.
func (r *run) instanceCycles(s *symbols) {
	type edge struct {
		to   string
		span diag.Span
	}
	edges := map[string][]edge{}
	var names []string
	var collect func(owner string, items []ast.Item)
	collect = func(owner string, items []ast.Item) {
		for _, it := range items {
			switch n := it.(type) {
			case *ast.Part:
				collect(owner, n.Items)
			case *ast.Array:
				collect(owner, n.Items)
			case *ast.Mirror:
				collect(owner, n.Items)
			case *ast.When:
				collect(owner, n.Then)
				collect(owner, n.Else)
			case *ast.Instance:
				if of := ast.FindSetting(n.Settings, "of"); of != nil {
					if nm, ok := of.Value.(*ast.Name); ok {
						edges[owner] = append(edges[owner], edge{nm.Name, of.Span})
					}
				}
			}
		}
	}
	for _, p := range r.f.Parts {
		names = append(names, p.Name.Name)
		collect(p.Name.Name, p.Items)
	}
	state := map[string]int{}
	var path []string
	reported := map[string]bool{}
	var visit func(n string)
	visit = func(n string) {
		state[n] = 1
		path = append(path, n)
		for _, e := range edges[n] {
			if state[e.to] == 1 {
				i := 0
				for path[i] != e.to {
					i++
				}
				chain := append(append([]string(nil), path[i:]...), e.to)
				key := chain[0]
				if reported[key] {
					continue
				}
				reported[key] = true
				msg := ""
				for j, c := range chain {
					if j > 0 {
						msg += " -> "
					}
					msg += c
				}
				d := diag.New(diag.CodeCycle, e.span, "parts place each other in a cycle: %s", msg)
				if dd := s.lookup(registry.NsGeometry, e.to); dd != nil {
					d = d.WithRelated(dd.span, "`"+e.to+"` is part of the cycle")
				}
				r.report(d)
			} else if state[e.to] == 0 {
				visit(e.to)
			}
		}
		path = path[:len(path)-1]
		state[n] = 2
	}
	for _, n := range names {
		if state[n] == 0 {
			visit(n)
		}
	}
}
