package lsp

import (
	"encoding/json"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
)

// targetAt returns the declaration a name at the offset stands for: the one a
// use resolves to, or the declaration itself when the offset is on its name.
func (d *document) targetAt(off int) int {
	ix := d.index()
	if ix == nil {
		return -1
	}
	if i := ix.refAt(off); i >= 0 {
		return ix.refs[i].Target
	}
	return ix.declAt(off)
}

func (s *Server) definition(params json.RawMessage) (any, *respError) {
	var p textDocumentPosition
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	d := s.doc(p.TextDocument.URI)
	if d == nil {
		return nil, nil
	}
	t := d.targetAt(d.offset(p.Position))
	if t < 0 {
		return nil, nil
	}
	return d.location(d.ix.decls[t].Span), nil
}

func (s *Server) references(params json.RawMessage) (any, *respError) {
	var p struct {
		textDocumentPosition
		Context struct {
			IncludeDeclaration bool `json:"includeDeclaration"`
		} `json:"context"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	d := s.doc(p.TextDocument.URI)
	out := []Location{}
	if d == nil {
		return out, nil
	}
	t := d.targetAt(d.offset(p.Position))
	if t < 0 {
		return out, nil
	}
	if p.Context.IncludeDeclaration {
		out = append(out, d.location(d.ix.decls[t].Span))
	}
	for _, r := range d.ix.refs {
		if r.Target == t {
			out = append(out, d.location(r.Span))
		}
	}
	return out, nil
}

// LSP symbol kinds the outline uses.
const (
	symModule    = 2
	symClass     = 5
	symFunction  = 12
	symVariable  = 13
	symConstant  = 14
	symArray     = 18
	symObject    = 19
	symKey       = 20
	symStruct    = 23
	symEvent     = 24
	symNamespace = 3
	symPackage   = 4
)

func (s *Server) documentSymbol(params json.RawMessage) (any, *respError) {
	var p struct {
		TextDocument textDocumentIdentifier `json:"textDocument"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	out := []DocumentSymbol{}
	d := s.doc(p.TextDocument.URI)
	if d == nil || d.file == nil {
		return out, nil
	}
	return d.outline(), nil
}

// symbol builds one outline node. The selection must lie inside the range, so
// both are clamped to the text.
func (d *document) symbol(name, detail string, kind int, whole, sel diag.Span) DocumentSymbol {
	if sel.Start < whole.Start || sel.End > whole.End || sel.Len() == 0 {
		sel = diag.Span{Start: whole.Start, End: whole.Start}
	}
	return DocumentSymbol{Name: name, Detail: detail, Kind: kind, Range: d.rangeOf(whole), SelectionRange: d.rangeOf(sel)}
}

// outline lists the asset, parameters, lets, templates, materials, the part
// tree, outputs and the rig declarations, in source order within each group.
func (d *document) outline() []DocumentSymbol {
	f := d.file
	out := []DocumentSymbol{}
	if f.Asset != nil {
		out = append(out, d.symbol(f.Asset.Name.Name, "asset", symModule, f.Asset.Span, f.Asset.Name.Span))
	}
	for _, p := range f.Params {
		detail := p.Type.Name
		if p.Runtime {
			detail += " runtime"
		}
		out = append(out, d.symbol(p.Name.Name, detail, symVariable, p.Span, p.Name.Span))
	}
	for _, l := range f.Lets {
		out = append(out, d.symbol(l.Name.Name, "let", symConstant, l.Span, l.Name.Span))
	}
	for _, t := range f.Templates {
		out = append(out, d.symbol(t.Name.Name, "template", symFunction, t.Span, t.Name.Span))
	}
	for _, m := range f.Materials {
		out = append(out, d.symbol(m.Name.Name, m.Kind, symStruct, m.Span, m.Name.Span))
	}
	for _, p := range f.Parts {
		out = append(out, d.partSymbol(p))
	}
	for _, o := range f.Outputs {
		out = append(out, d.symbol(o.Name.Name, "output", symPackage, o.Span, o.Name.Span))
	}
	for _, r := range f.Rigs {
		kind := symNamespace
		if r.Kind == "clip" {
			kind = symEvent
		}
		out = append(out, d.symbol(r.Name.Name, r.Kind, kind, r.Span, r.Name.Span))
	}
	return out
}

func pairName(a, b ast.Ident) string {
	if b.Name != "" {
		return a.Name + ", " + b.Name
	}
	return a.Name
}

func (d *document) partSymbol(p *ast.Part) DocumentSymbol {
	s := d.symbol(pairName(p.Name, p.Pair), "part", symClass, p.Span, p.Name.Span)
	s.Children = d.itemSymbols(p.Items)
	return s
}

// itemSymbols lists the children of a part. A mirror or a `when` adds no
// level of its own: its items appear in its place.
func (d *document) itemSymbols(items []ast.Item) []DocumentSymbol {
	var out []DocumentSymbol
	for _, it := range items {
		switch n := it.(type) {
		case *ast.Part:
			out = append(out, d.partSymbol(n))
		case *ast.Shape:
			name, sel := n.Name.Name, n.Name.Span
			if name == "" {
				name, sel = n.Kind, n.KindSpan
			}
			out = append(out, d.symbol(pairName(ast.Ident{Name: name}, n.Pair), n.Kind, symObject, n.Span, sel))
		case *ast.Array:
			name, sel := n.Name.Name, n.Name.Span
			if name == "" {
				name, sel = "array", diag.Span{Start: n.Span.Start, End: n.Span.Start}
			}
			s := d.symbol(name, "array", symArray, n.Span, sel)
			s.Children = d.itemSymbols(n.Items)
			out = append(out, s)
		case *ast.Socket:
			out = append(out, d.symbol(pairName(n.Name, n.Pair), "socket", symKey, n.Span, n.Name.Span))
		case *ast.Mirror:
			out = append(out, d.itemSymbols(n.Items)...)
		case *ast.When:
			out = append(out, d.itemSymbols(n.Then)...)
			out = append(out, d.itemSymbols(n.Else)...)
		case *ast.Collider:
			out = append(out, d.symbol(pairName(n.Name, n.Pair), n.Kind, symObject, n.Span, n.Name.Span))
		}
	}
	return out
}
