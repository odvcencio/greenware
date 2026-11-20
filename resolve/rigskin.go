package resolve

import (
	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// ---- anchors -----------------------------------------------------------------

// anchor evaluates a TAnchor slot: a point, or joint(name) with a point added
// or subtracted. A joint is checked against the skeleton's names here; its
// position is read where the step runs, so that it sees the binds the
// earlier steps leave.
func (r *run) anchor(e ast.Expr, sc *scope) units.Value {
	joint, offE, neg, ok := splitAnchor(e)
	if !ok {
		v := r.triple(r.eval(e, sc), units.Length, e.Span(), "a point, or joint(name) plus a point")
		if !v.Valid() {
			return v
		}
		return units.Tuple(units.Ref(""), v)
	}
	call := joint
	if len(call.Args) != 1 || call.Args[0].Value == nil || call.Args[0].Name != "" {
		r.report(diag.New(diag.CodeType, call.Span(), "joint(...) takes one joint name"))
		return units.Bad()
	}
	nm, sp, isName := nameOf(call.Args[0].Value)
	if !isName {
		r.report(diag.New(diag.CodeType, call.Args[0].Value.Span(), "joint(...) takes the name of a joint"))
		return units.Bad()
	}
	if r.syms.lookup(registry.NsJoint, nm) == nil {
		d := diag.New(diag.CodeUnknownName, sp, "no joint is named `%s`", nm)
		if s, ok := registry.Suggest(nm, r.syms.names(registry.NsJoint)); ok {
			d = d.WithEdit("write "+s, sp, s)
		}
		r.report(d)
		return units.Bad()
	}
	off := units.Tuple(units.Len(0), units.Len(0), units.Len(0))
	if offE != nil {
		v := r.triple(r.eval(offE, sc), units.Length, offE.Span(), "a point to add to joint(...)")
		if !v.Valid() {
			return v
		}
		if neg {
			a := triple3(v)
			v = units.Tuple(units.Len(-a[0]), units.Len(-a[1]), units.Len(-a[2]))
		}
		off = v
	}
	return units.Tuple(units.Ref(nm), off)
}

// splitAnchor recognises joint(name), joint(name) + point and
// joint(name) - point.
func splitAnchor(e ast.Expr) (call *ast.Call, off ast.Expr, neg, ok bool) {
	e = unparen(e)
	if c, isCall := e.(*ast.Call); isCall && c.Func == "joint" {
		return c, nil, false, true
	}
	if b, isBin := e.(*ast.Binary); isBin && (b.Op == "+" || b.Op == "-") {
		if c, isCall := unparen(b.L).(*ast.Call); isCall && c.Func == "joint" {
			return c, b.R, b.Op == "-", true
		}
	}
	return nil, nil, false, false
}

func anchorOf(v units.Value) RigAnchor {
	if v.Kind != units.KTuple || len(v.Elems) != 2 {
		return RigAnchor{}
	}
	return RigAnchor{Joint: v.Elems[0].Str, Offset: triple3(v.Elems[1])}
}

// ---- skin --------------------------------------------------------------------

func (r *run) skin(d *ast.RigDecl, st *rigState) *RigSkin {
	sk := &RigSkin{Span: d.Span}
	fs := r.fields(docKind("skin"), d.Settings, r.globals, d.KindSpan)
	sk.MaxInfluences = 4
	if v, ok := field(fs, "max_influences"); ok && v.Valid() {
		sk.MaxInfluences = int(v.Num)
	}
	r.skinItems(sk, d.Items, mirrorCtx{}, r.globals, st)
	return sk
}

func (r *run) skinItems(sk *RigSkin, items []ast.RigItem, m mirrorCtx, sc *scope, st *rigState) {
	sc = r.rigScope(sc, items, &m)
	for _, it := range items {
		switch b := it.(type) {
		case *ast.Row, *ast.SampleRow:
			r.report(diag.New(diag.CodeUnknownField, it.ItemSpan(), "a skin holds steps, not rows"))
		case *ast.RigBlock:
			switch b.Kind {
			case "lengthen":
				r.lengthen(sk, b, m, sc, st)
			case "blend":
				r.blend(sk, b, m, sc, st)
			case "solve":
				r.solve(sk, b, m, sc, st)
			case "mirror":
				r.openMirrorOnlyPlane(b)
				if m.plane != "" {
					r.report(diag.New(diag.CodeUnsupported, b.KindSpan, "a mirror inside a mirror is valid in the language but outside this compiler's subset"))
					continue
				}
				plane, ok := r.mirrorPlane(b, sc)
				if !ok {
					continue
				}
				r.skinItems(sk, b.Items, mirrorCtx{plane: plane}, sc, st)
				r.skinItems(sk, b.Items, mirrorCtx{plane: plane, copy: true}, sc, st)
			default:
				r.report(diag.New(diag.CodeUnknownField, b.KindSpan, "`%s` is not a skin step: a skin holds `lengthen`, `blend`, `solve` and `mirror`", b.Kind))
			}
		}
	}
}

func (r *run) lengthen(sk *RigSkin, b *ast.RigBlock, m mirrorCtx, sc *scope, st *rigState) {
	body := r.rigScope(sc, b.Items, &m)
	fs := r.fields(rigKind("lengthen"), b.Settings, body, b.KindSpan)
	l := &RigLengthen{Reflect: m.reflect(), Span: b.KindSpan}
	if v, ok := field(fs, "joint"); ok && v.Valid() {
		l.Joint = st.pair(registry.NsJoint, v.Str, m)
	}
	if v, ok := field(fs, "to"); ok && v.Valid() {
		l.To = v.Num
	}
	if v, ok := field(fs, "anchor"); ok && v.Valid() {
		l.Anchor = v.Num
		if !(v.Num >= 0 && v.Num < 1) {
			sp := b.KindSpan
			if s := ast.FindSetting(b.Settings, "anchor"); s != nil && s.Value != nil {
				sp = s.Value.Span()
			}
			r.report(diag.New(diag.CodeFieldRange, sp, "`anchor` is a share of the original bone: at least 0 and below 1, found %s", v.Format()))
		}
	}
	if v, ok := field(fs, "rigid"); ok && v.Valid() {
		for _, e := range v.Elems {
			l.Rigid = append(l.Rigid, st.pair(registry.NsGeometry, e.Str, m))
		}
	}
	sk.Steps = append(sk.Steps, RigStep{Lengthen: l})
}

func (r *run) blend(sk *RigSkin, b *ast.RigBlock, m mirrorCtx, sc *scope, st *rigState) {
	body := r.rigScope(sc, b.Items, &m)
	fs := r.fields(rigKind("blend"), b.Settings, body, b.KindSpan)
	bl := &RigBlend{Name: declared(b, m), FadeSign: 1, Reflect: m.reflect(), Span: b.KindSpan}
	if v, ok := field(fs, "parent"); ok && v.Valid() {
		bl.Parent = st.pair(registry.NsJoint, v.Str, m)
	}
	if v, ok := field(fs, "child"); ok && v.Valid() {
		bl.Child = st.pair(registry.NsJoint, v.Str, m)
	}
	if v, ok := field(fs, "centre"); ok && v.Valid() {
		bl.Centre = anchorOf(v)
	}
	if v, ok := field(fs, "axis"); ok && v.Valid() {
		bl.Axis = triple3(v)
		if bl.Axis == ([3]float64{}) {
			sp := b.KindSpan
			if s := ast.FindSetting(b.Settings, "axis"); s != nil && s.Value != nil {
				sp = s.Value.Span()
			}
			r.rigStructure(sp, "a blend `axis` must have a nonzero direction")
		}
	}
	if v, ok := field(fs, "width"); ok && v.Valid() {
		bl.Width = v.Num
	}
	if v, ok := field(fs, "bias"); ok && v.Valid() {
		bl.Bias = v.Num
	}
	if v, ok := field(fs, "fade_x"); ok && v.Valid() && v.Kind == units.KRange && len(v.Elems) == 2 {
		bl.HasFade, bl.FadeLo, bl.FadeHi = true, v.Elems[0].Num, v.Elems[1].Num
		if !(bl.FadeHi > bl.FadeLo) {
			sp := b.KindSpan
			if s := ast.FindSetting(b.Settings, "fade_x"); s != nil {
				sp = s.Span
			}
			r.report(diag.New(diag.CodeFieldRange, sp, "`fade_x` needs lo below hi, found %s..%s", v.Elems[0].Format(), v.Elems[1].Format()))
		}
		if m.copy {
			bl.FadeSign = -1
		}
	}
	if v, ok := field(fs, "only"); ok && v.Valid() {
		for _, e := range v.Elems {
			bl.Only = append(bl.Only, st.pair(registry.NsGeometry, e.Str, m))
		}
	}
	sk.Steps = append(sk.Steps, RigStep{Blend: bl})
}

// checkSkin checks the steps against the skeleton: the joints they name and
// the structure each step needs.
func (r *run) checkSkin(rg *Rig) {
	if rg.Skin == nil || rg.Skeleton == nil {
		return
	}
	children := map[string][]string{}
	for _, j := range rg.Skeleton.Joints {
		children[j.Parent] = append(children[j.Parent], j.Name)
	}
	parent := map[string]string{}
	for _, j := range rg.Skeleton.Joints {
		parent[j.Name] = j.Parent
	}
	for _, s := range rg.Skin.Steps {
		if l := s.Lengthen; l != nil {
			if n := len(children[l.Joint]); n != 1 {
				r.rigStructure(l.Span, "`lengthen` needs a joint with exactly one child, and `%s` has %d", l.Joint, n)
			}
		}
		if b := s.Blend; b != nil {
			if parent[b.Child] != b.Parent {
				r.rigStructure(b.Span, "a blend needs a parent joint and its immediate child: `%s` is not a child of `%s`", b.Child, b.Parent)
			}
		}
	}
}

// solve reads a `solve` step. It is valid once in a skin and not inside a
// mirror: a solve works on the whole skin, and a reflected copy would solve
// it twice. `refine = audit` is valid in the language and outside this
// compiler's subset.
func (r *run) solve(sk *RigSkin, b *ast.RigBlock, m mirrorCtx, sc *scope, st *rigState) {
	if m.plane != "" {
		r.report(diag.New(diag.CodeUnsupported, b.KindSpan, "a `solve` inside a mirror is valid in the language but outside this compiler's subset: write one `solve` in the skin"))
		return
	}
	for _, s := range sk.Steps {
		if s.Solve != nil {
			r.report(diag.New(diag.CodeUnsupported, b.KindSpan, "a second `solve` in one skin is valid in the language but outside this compiler's subset"))
			return
		}
	}
	body := r.rigScope(sc, b.Items, &m)
	fs := r.fields(rigKind("solve"), b.Settings, body, b.KindSpan)
	sv := &RigSolve{MaxInfluences: sk.MaxInfluences, Span: b.KindSpan}
	if v, ok := field(fs, "method"); ok && v.Valid() {
		sv.Method = v.Str
	}
	if s := ast.FindSetting(b.Settings, "max_influences"); s != nil {
		if v, ok := field(fs, "max_influences"); ok && v.Valid() {
			sv.MaxInfluences = int(v.Num)
			if sv.MaxInfluences > sk.MaxInfluences {
				sp := b.KindSpan
				if s.Value != nil {
					sp = s.Value.Span()
				}
				r.report(diag.New(diag.CodeFieldRange, sp, "the solve keeps %d influences, above the skin's `max_influences` of %d", sv.MaxInfluences, sk.MaxInfluences))
			}
		}
	}
	if v, ok := field(fs, "refine"); ok && v.Valid() {
		sv.Refine = v.Str
		if v.Str == "audit" {
			sp := b.KindSpan
			if s := ast.FindSetting(b.Settings, "refine"); s != nil && s.Value != nil {
				sp = s.Value.Span()
			}
			r.report(diag.New(diag.CodeUnsupported, sp, "`refine = audit` is valid in the language but outside this compiler's subset: the pinned deformation audit is not a refiner here; use `smooth` or `none`"))
		}
	}
	if v, ok := field(fs, "iterations"); ok && v.Valid() {
		sv.Iterations = int(v.Num)
	}
	if v, ok := field(fs, "tolerance"); ok && v.Valid() {
		sv.Tolerance = v.Num
	}
	if v, ok := field(fs, "smooth"); ok && v.Valid() {
		sv.Smooth = v.Num
	}
	if v, ok := field(fs, "power"); ok && v.Valid() {
		sv.Power = int(v.Num)
	}
	if v, ok := field(fs, "cutoff"); ok && v.Valid() {
		sv.Cutoff = v.Num
	}
	if v, ok := field(fs, "own"); ok && v.Valid() {
		sv.Own = v.Num
	}
	if v, ok := field(fs, "soft"); ok && v.Valid() {
		sv.Soft = v.Num
	}
	if v, ok := field(fs, "reach"); ok && v.Valid() {
		sv.Reach = int(v.Num)
	}
	if v, ok := field(fs, "rigid"); ok && v.Valid() {
		for _, e := range v.Elems {
			sv.Rigid = append(sv.Rigid, st.pair(registry.NsGeometry, e.Str, m))
		}
	}
	sk.Steps = append(sk.Steps, RigStep{Solve: sv})
}
