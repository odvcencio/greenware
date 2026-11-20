package resolve

import (
	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// cellInh is what a part or shape inherits for atlas cells: the effective
// material and the nearest `cell` setting.
type cellInh struct {
	mat      string
	cell     string
	cellSpan diag.Span
}

func (in cellInh) with(settings []*ast.Setting) cellInh {
	if s := ast.FindSetting(settings, "material"); s != nil {
		if n, ok := s.Value.(*ast.Name); ok {
			in.mat = n.Name
		}
	}
	if s := ast.FindSetting(settings, "cell"); s != nil {
		if n, ok := s.Value.(*ast.Name); ok {
			in.cell, in.cellSpan = n.Name, n.Span()
		}
	}
	return in
}

// checkCells resolves each `cell` against the atlases of the effective
// material (language reference 4.8): the cell must exist in every atlas the
// material samples, at one rectangle. A template's body sees the material and
// cell of its call site.
func (r *run) checkCells() {
	for _, p := range r.f.Parts {
		r.cellWalk([]ast.Item{p}, cellInh{}, 0)
	}
}

const maxCellDepth = 64

func (r *run) cellWalk(items []ast.Item, in cellInh, depth int) {
	if depth > maxCellDepth {
		return
	}
	for _, it := range items {
		switch n := it.(type) {
		case *ast.Part:
			r.cellWalk(n.Items, in.with(n.Settings), depth+1)
		case *ast.Shape:
			if s := in.with(n.Settings); s.cell != "" {
				r.checkCell(s)
			}
		case *ast.Array:
			r.cellWalk(n.Items, in, depth+1)
		case *ast.Mirror:
			r.cellWalk(n.Items, in, depth+1)
		case *ast.When:
			r.cellWalk(n.Then, in, depth+1)
			r.cellWalk(n.Else, in, depth+1)
		case *ast.TemplateCall:
			if t := r.syms.templates[n.Name]; t != nil {
				r.cellWalk(t.Items, in, depth+1)
			}
		case *ast.Instance:
			if of := ast.FindSetting(n.Settings, "of"); of != nil {
				if nm, ok := of.Value.(*ast.Name); ok {
					if d := r.syms.lookup(registry.NsGeometry, nm.Name); d != nil && d.part != nil {
						r.cellWalk([]ast.Item{d.part}, in, depth+1)
					}
				}
			}
		}
	}
}

// materialAtlases lists the atlases a material samples on its UV set: the
// texture arguments of a Selena material first, then the base texture of the
// pbr material that is drawn (the fallback, for a Selena material).
func (r *run) materialAtlases(name string) (atlases []*Atlas, found bool) {
	var mat *Node
	for _, m := range r.resolved.Materials {
		if m.Name == name {
			mat = m
		}
	}
	if mat == nil {
		return nil, false
	}
	seen := map[string]bool{}
	add := func(texture string) {
		if atlas, painted := ParsePaintRef(texture); painted {
			texture = atlas
		}
		if a := r.resolved.Atlas(texture); a != nil && !seen[a.Name] {
			seen[a.Name] = true
			atlases = append(atlases, a)
		}
	}
	pick := func(n *Node, only string) {
		for _, f := range n.Fields {
			if f.Value.Valid() && f.Value.Kind == units.KRef && (only == "" || f.Name == only) {
				add(f.Value.Str)
			}
		}
	}
	if mat.Kind == "selena" {
		pick(mat, "")
		for _, c := range mat.Children {
			pick(c, "base_texture")
		}
	} else {
		pick(mat, "base_texture")
	}
	return atlases, true
}

func (r *run) checkCell(in cellInh) {
	if in.mat == "" {
		r.report(diag.New(diag.CodeUnknownName, in.cellSpan, "`cell = %s` names a cell of an atlas, but no material is set here", in.cell))
		return
	}
	atlases, found := r.materialAtlases(in.mat)
	if !found {
		return // an unknown material is reported where it is named
	}
	if len(atlases) == 0 {
		r.report(diag.New(diag.CodeUnknownName, in.cellSpan, "`cell = %s`: the material `%s` samples no atlas", in.cell, in.mat))
		return
	}
	type rect struct{ u0, v0, u1, v1 float64 }
	var first *rect
	var firstAtlas *Atlas
	for _, a := range atlases {
		idx, ok := a.CellIndex(in.cell)
		if !ok {
			d := diag.New(diag.CodeUnknownName, in.cellSpan, "the atlas `%s` has no cell `%s`", a.Name, in.cell)
			var names []string
			for _, c := range a.Cells {
				names = append(names, c.Name)
			}
			if s, ok := registry.Suggest(in.cell, names); ok {
				d = d.WithEdit("write "+s, in.cellSpan, s)
			}
			r.report(d)
			return
		}
		u0, v0, u1, v1 := a.Layout.Rect(idx)
		rc := &rect{u0, v0, u1, v1}
		if first == nil {
			first, firstAtlas = rc, a
		} else if *first != *rc {
			r.report(diag.New(diag.CodeUVCell, in.cellSpan, "the cell `%s` is not one rectangle in the atlases `%s` and `%s` that material `%s` samples: one UV pair cannot address both layouts", in.cell, firstAtlas.Name, a.Name, in.mat))
			return
		}
	}
}
