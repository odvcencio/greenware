package resolve

import (
	"math"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// A clip is sampled from a pose (a rotation for each named joint, and the
// root's translation, at `keys` evenly spaced times from 0 to its duration),
// or keyed on a wrapper node with `key (time, value)` rows.

func (r *run) clip(d *ast.RigDecl, st *rigState, rg *Rig) *RigClip {
	fs := r.fields(docKind("clip"), d.Settings, r.globals, d.KindSpan)
	c := &RigClip{Name: d.Name.Name, Export: d.Name.Name, Keys: 2, Interp: "linear", Span: d.Name.Span}
	if v, ok := field(fs, "name"); ok && v.Valid() {
		c.Export = v.Str
	}
	if v, ok := field(fs, "duration"); ok && v.Valid() {
		c.Duration, c.HasDuration = v.Num, true
	}
	if v, ok := field(fs, "keys"); ok && v.Valid() {
		c.Keys = int(v.Num)
	}
	if v, ok := field(fs, "loop"); ok && v.Valid() {
		c.Loop = v.Bool
	}
	if v, ok := field(fs, "airborne"); ok && v.Valid() {
		c.Airborne = v.Bool
	}
	if v, ok := field(fs, "interpolation"); ok && v.Valid() {
		c.Interp = v.Str
	}
	if v, ok := field(fs, "node"); ok && v.Valid() {
		c.Node = v.Str
	}
	var pose *ast.RigBlock
	var rows []ast.RigItem
	for _, it := range d.Items {
		switch n := it.(type) {
		case *ast.RigBlock:
			if n.Kind != "pose" {
				r.report(diag.New(diag.CodeUnknownField, n.KindSpan, "a clip holds a `pose` block and `key` rows, not `%s`", n.Kind))
				continue
			}
			if pose != nil {
				r.report(diag.New(diag.CodeDuplicate, n.KindSpan, "a clip has one `pose` block").WithRelated(pose.KindSpan, "first pose block"))
				continue
			}
			pose = n
		case *ast.Row, *ast.SampleRow:
			rows = append(rows, it)
		}
	}
	switch {
	case pose != nil && len(rows) > 0:
		r.report(diag.New(diag.CodeType, d.KindSpan, "a clip is sampled from a `pose` or keyed with `key` rows, not both"))
		return nil
	case pose != nil:
		r.sampledClip(c, d, pose, st, rg)
	case len(rows) > 0:
		r.keyedClip(c, d, rows, rg)
	default:
		r.report(diag.New(diag.CodeMissing, d.KindSpan, "a clip needs a `pose` block or `key` rows"))
		return nil
	}
	return c
}

func (r *run) sampledClip(c *RigClip, d *ast.RigDecl, pose *ast.RigBlock, st *rigState, rg *Rig) {
	if rg.Skeleton == nil {
		r.report(diag.New(diag.CodeMissing, d.KindSpan, "a `pose` clip needs a `skeleton`"))
		return
	}
	if c.Node != "" {
		r.report(diag.New(diag.CodeType, valueSpan(d.Settings, "node", d.KindSpan), "a `pose` clip drives the skeleton, not the wrapper node `%s`", c.Node))
		return
	}
	if !c.HasDuration {
		r.report(diag.New(diag.CodeMissing, d.KindSpan, "a sampled clip needs `duration`"))
		return
	}
	joints := map[string]bool{}
	for _, j := range rg.Skeleton.Joints {
		joints[j.Name] = true
	}
	for i := 0; i < c.Keys; i++ {
		tn := float64(i) / float64(c.Keys-1)
		s := RigSample{Time: float64(c.Duration * tn)}
		clipScope := newScope(r.globals)
		clipScope.add(&binding{name: "t", state: stDone, value: units.Num(tn)})
		body := r.rigScope(clipScope, d.Items, nil)
		seen := map[string]bool{}
		r.poseItems(&s, pose, mirrorCtx{}, body, st, joints, seen)
		c.Samples = append(c.Samples, s)
	}
	if len(c.Samples) > 0 && len(c.Samples[0].Rots) == 0 && !c.Samples[0].HasRoot {
		r.report(diag.New(diag.CodeClipKey, pose.KindSpan, "a clip needs at least one channel: the pose names no joint and no `root`"))
	}
}

func valueSpan(ss []*ast.Setting, name string, fallback diag.Span) diag.Span {
	if s := ast.FindSetting(ss, name); s != nil {
		if s.Value != nil {
			return s.Value.Span()
		}
		return s.Span
	}
	return fallback
}

// poseItems evaluates the settings of a pose or of a mirror inside it.
func (r *run) poseItems(s *RigSample, b *ast.RigBlock, m mirrorCtx, sc *scope, st *rigState, joints map[string]bool, seen map[string]bool) {
	sc = r.rigScope(sc, b.Items, &m)
	for _, set := range b.Settings {
		if set.Name == "plane" && b.Kind == "mirror" {
			continue
		}
		if set.Value == nil {
			r.report(diag.New(diag.CodeType, set.Span, "`%s` takes a rotation, not a range", set.Name))
			continue
		}
		if set.Name == "root" && !joints["root"] {
			if m.plane != "" {
				r.report(diag.New(diag.CodeUnsupported, set.NameSpan, "`root` inside a mirror is valid in the language but outside this compiler's subset"))
				continue
			}
			v := r.triple(r.eval(set.Value, sc), units.Length, set.Value.Span(), "a translation (three lengths)")
			if v.Valid() {
				s.HasRoot, s.Root = true, triple3(v)
			}
			continue
		}
		name := st.pair(registry.NsJoint, set.Name, m)
		if !joints[name] {
			d := diag.New(diag.CodeUnknownName, set.NameSpan, "no joint is named `%s`", set.Name)
			var names []string
			for j := range joints {
				names = append(names, j)
			}
			if sug, ok := registry.Suggest(set.Name, sortedStrings(names)); ok {
				d = d.WithEdit("write "+sug, set.NameSpan, sug)
			}
			r.report(d)
			continue
		}
		if seen[name] {
			r.report(diag.New(diag.CodeDuplicate, set.NameSpan, "the pose sets the joint `%s` twice", name))
			continue
		}
		seen[name] = true
		v := r.triple(r.eval(set.Value, sc), units.Angle, set.Value.Span(), "a rotation rot(x, y, z)")
		if !v.Valid() {
			continue
		}
		s.Rots = append(s.Rots, RigRot{Joint: name, Euler: triple3(v), Reflect: m.reflect()})
	}
	for _, it := range b.Items {
		mb, ok := it.(*ast.RigBlock)
		if !ok {
			continue
		}
		if mb.Kind != "mirror" {
			r.report(diag.New(diag.CodeUnknownField, mb.KindSpan, "a pose holds joint rotations, lets and mirrors, not `%s`", mb.Kind))
			continue
		}
		if m.plane != "" {
			r.report(diag.New(diag.CodeUnsupported, mb.KindSpan, "a mirror inside a mirror is valid in the language but outside this compiler's subset"))
			continue
		}
		plane, ok := r.mirrorPlane(mb, sc)
		if !ok {
			continue
		}
		r.poseItems(s, mb, mirrorCtx{plane: plane}, sc, st, joints, seen)
		r.poseItems(s, mb, mirrorCtx{plane: plane, copy: true}, sc, st, joints, seen)
	}
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (r *run) keyedClip(c *RigClip, d *ast.RigDecl, rows []ast.RigItem, rg *Rig) {
	if c.Node == "" {
		r.report(diag.New(diag.CodeMissing, d.KindSpan, "a clip of `key` rows needs `node`, the wrapper it drives"))
		return
	}
	found := false
	for _, w := range rg.Wrappers {
		found = found || w == c.Node
	}
	if !found {
		sp := valueSpan(d.Settings, "node", d.KindSpan)
		dd := diag.New(diag.CodeUnknownName, sp, "no wrapper node is named %q: the asset's `wrappers` lists %v", c.Node, rg.Wrappers)
		r.report(dd)
		return
	}
	sc := r.rigScope(r.globals, d.Items, nil)
	var all []RigKey
	for _, it := range rows {
		switch n := it.(type) {
		case *ast.Row:
			if k, ok := r.keyRow(n, sc); ok {
				all = append(all, k)
			}
		case *ast.SampleRow:
			all = append(all, r.sampleKeys(n, sc)...)
		}
	}
	c.Rows = all
	if len(all) == 0 {
		return
	}
	last := -1.0
	rot := all[0].Rot
	for i, k := range all {
		sp := rowSpan(rows, i, d.KindSpan)
		switch {
		case k.Rot != rot:
			r.report(diag.New(diag.CodeClipKey, sp, "a channel holds translations or rotations, not both"))
		case math.IsNaN(k.Time) || math.IsInf(k.Time, 0):
			r.report(diag.New(diag.CodeClipKey, sp, "key %d has a time that is not finite", i))
		case k.Time < 0:
			r.report(diag.New(diag.CodeClipKey, sp, "key %d has the negative time %s", i, units.Value{Kind: units.KNumber, Dim: units.Duration, Num: k.Time}.Format()))
		case i > 0 && !(k.Time > last):
			r.report(diag.New(diag.CodeClipKey, sp, "key %d at %s does not come after key %d at %s: times increase strictly, and nothing is sorted or merged", i, fmtTime(k.Time), i-1, fmtTime(last)))
		case i > 0 && !(det.Round32(k.Time) > det.Round32(last)):
			r.report(diag.New(diag.CodeClipKey, sp, "key %d at %s is not later than key %d once written as float32", i, fmtTime(k.Time), i-1))
		case c.HasDuration && k.Time > c.Duration:
			r.report(diag.New(diag.CodeClipKey, sp, "key %d at %s is later than the clip's duration %s", i, fmtTime(k.Time), fmtTime(c.Duration)))
		}
		last = k.Time
	}
	if !c.HasDuration {
		c.Duration = all[len(all)-1].Time
	}
	if !(c.Duration > 0) {
		r.report(diag.New(diag.CodeClipKey, d.KindSpan, "a clip needs a positive duration; its keys end at %s", fmtTime(c.Duration)))
	}
}

func fmtTime(t float64) string {
	return units.Value{Kind: units.KNumber, Dim: units.Duration, Num: t}.Format()
}

// rowSpan finds the span of the row that produced key i; sampled rows share
// one span.
func rowSpan(rows []ast.RigItem, i int, fallback diag.Span) diag.Span {
	if i < len(rows) {
		return rows[i].ItemSpan()
	}
	if len(rows) > 0 {
		return rows[len(rows)-1].ItemSpan()
	}
	return fallback
}

// keyRow reads `key (time, value)`.
func (r *run) keyRow(n *ast.Row, sc *scope) (RigKey, bool) {
	if n.Name != "key" || len(n.Args) != 2 || n.Args[0].Value == nil || n.Args[1].Value == nil || n.Args[0].Name != "" || n.Args[1].Name != "" {
		r.report(diag.New(diag.CodeType, n.Span, "`key` takes a time and a value: key (0s, (0, 0, 0))"))
		return RigKey{}, false
	}
	return r.keyOf(n.Args[0].Value, n.Args[1].Value, sc, n.Span)
}

func (r *run) keyOf(te, ve ast.Expr, sc *scope, sp diag.Span) (RigKey, bool) {
	t := r.scalar(r.eval(te, sc), units.Duration, te.Span())
	v := r.eval(ve, sc)
	if !t.Valid() || !v.Valid() {
		return RigKey{}, false
	}
	if v.Kind != units.KTuple || len(v.Elems) != 3 {
		r.report(diag.New(diag.CodeType, ve.Span(), "a key value is a translation (three lengths) or a rotation rot(x, y, z), found a %s", v.Describe()))
		return RigKey{}, false
	}
	k := RigKey{Time: t.Num, Rot: v.Elems[0].Kind == units.KNumber && !v.Elems[0].Pending && v.Elems[0].Dim == units.Angle}
	dim := units.Length
	if k.Rot {
		dim = units.Angle
	}
	out := r.triple(v, dim, ve.Span(), "a translation (three lengths) or a rotation rot(x, y, z)")
	if !out.Valid() {
		return RigKey{}, false
	}
	k.Value = triple3(out)
	return k, true
}

// sampleKeys expands `sample v in lo..hi by n -> key (...)`.
func (r *run) sampleKeys(n *ast.SampleRow, sc *scope) []RigKey {
	if n.Range == nil || n.Row == nil {
		return nil
	}
	if n.Row.Name != "key" || len(n.Row.Args) != 2 || n.Row.Args[0].Value == nil || n.Row.Args[1].Value == nil {
		r.report(diag.New(diag.CodeType, n.Span, "a sampled row of a clip makes `key (time, value)` rows"))
		return nil
	}
	lo, hi := r.eval(n.Range.Lo, sc), r.eval(n.Range.Hi, sc)
	cnt := r.scalar(r.eval(n.Count, sc), units.Number, n.Count.Span())
	if !lo.Valid() || !hi.Valid() || !cnt.Valid() {
		return nil
	}
	lo, hi = units.Finish(lo), units.Finish(hi)
	if lo.Kind != units.KNumber || lo.Dim != hi.Dim {
		r.report(diag.New(diag.CodeUnit, n.Range.Span, "a sampled range has ends of one dimension, found a %s and a %s", lo.Describe(), hi.Describe()))
		return nil
	}
	if !units.IsIntegral(cnt.Num) || cnt.Num < 2 || cnt.Num > 4096 {
		r.report(diag.New(diag.CodeFieldRange, n.Count.Span(), "a sampled row makes between 2 and 4096 samples, found %s", cnt.Format()))
		return nil
	}
	count := int(cnt.Num)
	var out []RigKey
	for i := 0; i < count; i++ {
		x := det.Lerp(lo.Num, hi.Num, float64(i)/float64(count-1))
		inner := newScope(sc)
		inner.add(&binding{name: n.Var.Name, span: n.Var.Span, state: stDone, value: units.Value{Kind: units.KNumber, Dim: lo.Dim, Num: x}})
		if k, ok := r.keyOf(n.Row.Args[0].Value, n.Row.Args[1].Value, inner, n.Span); ok {
			out = append(out, k)
		}
	}
	return out
}

// checkClips checks the clips against each other and the asset.
func (r *run) checkClips(rg *Rig) {
	seen := map[string]*RigClip{}
	byName := map[string]*RigClip{}
	for _, c := range rg.Clips {
		if prev, dup := seen[c.Export]; dup {
			r.report(diag.New(diag.CodeDuplicate, c.Span, "two clips export the animation name %q", c.Export).WithRelated(prev.Span, "first clip"))
			continue
		}
		seen[c.Export] = c
		byName[c.Name] = c
	}
	var asset diag.Span
	if r.f.Asset != nil {
		asset = r.f.Asset.Name.Span
	}
	if rg.Skeleton != nil && rg.Idle == "" {
		r.report(diag.New(diag.CodeMissing, asset, "a skinned asset needs `idle`, the clip a renderer shows before any other: no bind pose is on screen"))
		return
	}
	if rg.Idle == "" {
		return
	}
	sp := asset
	if s := ast.FindSetting(r.f.Asset.Settings, "idle"); s != nil && s.Value != nil {
		sp = s.Value.Span()
	}
	c, ok := byName[rg.Idle]
	switch {
	case !ok:
		d := diag.New(diag.CodeUnknownName, sp, "no clip is named `%s`", rg.Idle)
		var names []string
		for _, x := range rg.Clips {
			names = append(names, x.Name)
		}
		if s, ok := registry.Suggest(rg.Idle, sortedStrings(names)); ok {
			d = d.WithEdit("write "+s, sp, s)
		}
		r.report(d)
	case len(c.Samples) == 0:
		r.report(diag.New(diag.CodeType, sp, "the idle clip must pose the skeleton, and `%s` is keyed on a wrapper node", rg.Idle))
	}
}
