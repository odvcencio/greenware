package resolve

import (
	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

func (r *run) morph(d *ast.RigDecl, st *rigState) *RigMorph {
	m := &RigMorph{Name: d.Name.Name, Export: d.Name.Name, Span: d.Name.Span}
	fs := r.fields(docKind("morph"), d.Settings, r.globals, d.KindSpan)
	if v, ok := field(fs, "name"); ok && v.Valid() {
		m.Export = v.Str
	}
	if v, ok := field(fs, "joints"); ok && v.Valid() {
		m.Follow = v.Str == "follow"
	}
	if s := ast.FindSetting(d.Settings, "weight"); s != nil && s.Value != nil {
		m.Weight = r.morphWeight(s.Value, r.globals)
	}
	r.morphItems(m, d.Items, mirrorCtx{}, r.globals, st)
	if len(m.Ops) == 0 {
		r.report(diag.New(diag.CodeMissing, d.KindSpan, "a morph needs at least one operation: inflate, stretch, reshape, offset or girth"))
	}
	if m.Follow {
		for _, op := range m.Ops {
			if op.Kind != "stretch" {
				r.report(diag.New(diag.CodeUnsupported, op.Span, "`joints = follow` with %s is valid in the language but outside this compiler's subset: edition 1 permits it for axial stretches only, because only a stretch gives the joints a defined map", op.Kind))
			}
		}
	}
	return m
}

func (r *run) morphItems(m *RigMorph, items []ast.RigItem, mc mirrorCtx, sc *scope, st *rigState) {
	sc = r.rigScope(sc, items, &mc)
	for _, it := range items {
		switch b := it.(type) {
		case *ast.Row, *ast.SampleRow:
			r.report(diag.New(diag.CodeUnknownField, it.ItemSpan(), "a morph holds operations, not rows"))
		case *ast.RigBlock:
			switch b.Kind {
			case "inflate", "stretch", "reshape", "offset", "girth":
				if op, ok := r.morphOp(b, mc, sc, st); ok {
					m.Ops = append(m.Ops, op)
				}
			case "mirror":
				r.openMirrorOnlyPlane(b)
				if mc.plane != "" {
					r.report(diag.New(diag.CodeUnsupported, b.KindSpan, "a mirror inside a mirror is valid in the language but outside this compiler's subset"))
					continue
				}
				plane, ok := r.mirrorPlane(b, sc)
				if !ok {
					continue
				}
				r.morphItems(m, b.Items, mirrorCtx{plane: plane}, sc, st)
				r.morphItems(m, b.Items, mirrorCtx{plane: plane, copy: true}, sc, st)
			default:
				r.report(diag.New(diag.CodeUnknownField, b.KindSpan, "`%s` is not a morph operation: a morph holds `inflate`, `stretch`, `reshape`, `offset`, `girth` and `mirror`", b.Kind))
			}
		}
	}
}

func (r *run) morphOp(b *ast.RigBlock, mc mirrorCtx, sc *scope, st *rigState) (RigMorphOp, bool) {
	body := r.rigScope(sc, b.Items, &mc)
	fs := r.fields(rigKind(b.Kind), b.Settings, body, b.KindSpan)
	op := RigMorphOp{Kind: b.Kind, Reflect: mc.reflect(), Span: b.KindSpan}
	if v, ok := field(fs, "parts"); ok && v.Valid() {
		for _, e := range v.Elems {
			op.Parts = append(op.Parts, st.pair(registry.NsGeometry, e.Str, mc))
		}
	}
	switch b.Kind {
	case "inflate":
		if v, ok := field(fs, "by"); ok && v.Valid() {
			op.By = v.Num
			if !(v.Num > -1) {
				r.report(diag.New(diag.CodeFieldRange, blockSetting(b, "by"), "`by` is a fractional change of the radius and must be above -1 (-100%%), found %s", v.Format()))
			}
		}
	case "stretch":
		if mc.plane != "" {
			r.report(diag.New(diag.CodeUnsupported, b.KindSpan, "a `stretch` inside a mirror is valid in the language but outside this compiler's subset"))
			return op, false
		}
		if v, ok := field(fs, "axis"); ok && v.Valid() {
			op.Axis = v.Str[1:]
			if v.Str[0] == '-' {
				r.report(diag.New(diag.CodeUnsupported, blockSetting(b, "axis"), "a `stretch` along a negative axis is valid in the language but outside this compiler's subset: write the positive axis and a negative `by`"))
				return op, false
			}
		}
		if v, ok := field(fs, "from"); ok && v.Valid() {
			op.From = v.Num
		}
		if v, ok := field(fs, "to"); ok && v.Valid() {
			op.To = v.Num
		}
		if !(op.From < op.To) {
			r.report(diag.New(diag.CodeFieldRange, b.KindSpan, "a `stretch` needs `from` below `to`, found %s and %s", units.Len(op.From).Format(), units.Len(op.To).Format()))
		}
		if v, ok := field(fs, "by"); ok && v.Valid() {
			op.By = v.Num
		}
	case "reshape":
		if v, ok := field(fs, "about"); ok && v.Valid() {
			op.About = anchorOf(v)
		}
		if v, ok := field(fs, "scale"); ok && v.Valid() {
			op.Scale = triple3(v)
		}
	case "offset":
		if v, ok := field(fs, "by"); ok && v.Valid() {
			op.Vec = triple3(v)
		}
	case "girth":
		if op.Girth = r.girth(b, body); op.Girth == nil {
			return op, false
		}
	}
	return op, true
}

func blockSetting(b *ast.RigBlock, name string) diag.Span {
	if s := ast.FindSetting(b.Settings, name); s != nil {
		if s.Value != nil {
			return s.Value.Span()
		}
		return s.Span
	}
	return b.KindSpan
}
