package resolve

import (
	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// This file evaluates the rig declarations: skeleton, skin, morph and clip.
// Distances come out in metres, angles in radians, times in seconds. A mirror
// is expanded here into the original and one reflected copy. The copy's value
// expressions are evaluated with `side` equal to -1 and read the original
// symbols; the result is marked with the mirror plane and reflected once, by
// slot, in the compiler (language reference 5.3).

// RigRange is an angular range in radians.
type RigRange struct{ Lo, Hi float64 }

// RigRanges are the swing and twist ranges of a joint. A nil range imposes no
// test.
type RigRanges struct {
	Twist, SwingX, SwingY, SwingZ *RigRange
}

// Any reports whether any range is set.
func (r RigRanges) Any() bool {
	return r.Twist != nil || r.SwingX != nil || r.SwingY != nil || r.SwingZ != nil
}

// RigJoint is one joint, in declaration order (depth first, an original before
// its reflection).
type RigJoint struct {
	Name   string
	Parent string // "" for the root
	At     [3]float64
	Orient [3]float64 // rot(x, y, z) in radians
	// Reflect is the mirror plane ("x", "y", "z") when this joint is the
	// reflected copy of an original, else "". At and Orient were evaluated
	// for the original and are reflected by the compiler.
	Reflect   string
	TwistAxis string // "", "x", "y" or "z"
	Band      RigRanges
	Limit     RigRanges
	Span      diag.Span `json:"-"`
}

// RigSocket is a named frame on a joint.
type RigSocket struct {
	Name, Kind, Joint string
	HasAt             bool
	At                [3]float64 // a model-space bind point when HasAt
	Rot               [3]float64
	Reflect           string
	Span              diag.Span `json:"-"`
}

// RigSkeleton is the joint tree.
type RigSkeleton struct {
	Name    string
	Joints  []RigJoint
	Sockets []RigSocket
	// Order is the written joint order when `joint_order` lists it; nil
	// means depth-first declaration order.
	Order []string
	// RootScale names the numeric runtime parameter that scales the up
	// component of the root's translation keys; "" for none.
	RootScale string
	Span      diag.Span `json:"-"`
}

// RigAnchor is a model-space point written as a point, or as joint(name) plus
// a point. Joint is "" for a plain point.
type RigAnchor struct {
	Joint  string
	Offset [3]float64
}

// RigLengthen is a lengthen step.
type RigLengthen struct {
	Joint   string
	To      float64
	Anchor  float64
	Rigid   []string
	Reflect string
	Span    diag.Span `json:"-"`
}

// RigBlend is a blend step.
type RigBlend struct {
	Name          string
	Parent, Child string
	Centre        RigAnchor
	Axis          [3]float64
	Width, Bias   float64
	HasFade       bool
	FadeLo        float64
	FadeHi        float64
	FadeSign      float64 // 1, or -1 in the reflected copy
	Only          []string
	Reflect       string
	Span          diag.Span `json:"-"`
}

// RigSolve is a solve step: the weight solver of milestone M5. MaxInfluences
// is the effective limit (the step's own, else the skin's).
type RigSolve struct {
	Method        string
	MaxInfluences int
	Refine        string // "smooth" or "none"
	Iterations    int
	Tolerance     float64
	Smooth        float64
	Power         int
	Own           float64
	Cutoff        float64
	Soft          float64
	Reach         int
	Rigid         []string
	Span          diag.Span `json:"-"`
}

// RigStep is one skin step: exactly one of its fields is set.
type RigStep struct {
	Lengthen *RigLengthen
	Blend    *RigBlend
	// Solve is omitted from the encoding when nil, so that the encoding of a
	// skin with no solve (and the digest of the file built from it) is what it
	// was before the solver.
	Solve *RigSolve `json:",omitempty"`
}

// RigSkin is the ordered skin steps.
type RigSkin struct {
	MaxInfluences int
	Steps         []RigStep
	Span          diag.Span `json:"-"`
}

// RigBreak is one breakpoint of a piecewise-linear weight.
type RigBreak struct{ X, Y float64 }

// RigMember is the weight of one member of an enum parameter.
type RigMember struct {
	Name string
	W    float64
}

// RigWeight is a morph weight: a constant, or a function of one runtime
// parameter that is a finite table.
type RigWeight struct {
	Param string // "" for a constant
	// Const is the weight of a constant, and the weight at the default of a
	// parameter.
	Const   float64
	Breaks  []RigBreak  // numeric parameter: ordered over its full range
	Members []RigMember // enum parameter: a total lookup in declaration order
	Default float64     // the weight at the parameter's default value
}

// RigMorphOp is one operation of a morph, in source order.
type RigMorphOp struct {
	Kind    string // "inflate", "stretch", "reshape", "offset"
	Parts   []string
	By      float64
	Axis    string // stretch: "x", "y" or "z"
	From    float64
	To      float64
	About   RigAnchor
	Scale   [3]float64
	Vec     [3]float64
	Reflect string
	Span    diag.Span `json:"-"`
}

// RigMorph is a morph target.
type RigMorph struct {
	Name   string // declaration name
	Export string // exported target name
	Weight RigWeight
	Follow bool
	Ops    []RigMorphOp
	Span   diag.Span `json:"-"`
}

// RigRot is the rotation a pose gives one joint.
type RigRot struct {
	Joint   string
	Euler   [3]float64 // rot(x, y, z) in radians
	Reflect string
}

// RigSample is one sampled key of a pose clip.
type RigSample struct {
	Time    float64 // seconds
	Rots    []RigRot
	HasRoot bool
	Root    [3]float64 // the root joint's local translation, metres
}

// RigKey is one `key` row of a clip.
type RigKey struct {
	Time  float64
	Value [3]float64
	Rot   bool // the value is a rotation (radians), else a translation (metres)
}

// RigClip is a clip: sampled from a pose, or keyed on a wrapper node.
type RigClip struct {
	Name        string // declaration name
	Export      string // exported animation name
	Duration    float64
	HasDuration bool
	Keys        int
	Loop        bool
	Airborne    bool
	Interp      string // "linear" or "step"
	Node        string // wrapper node of a keyed clip
	Samples     []RigSample
	Rows        []RigKey
	Span        diag.Span `json:"-"`
}

// Rig is the evaluated rig of an asset. It is nil when the file declares
// none of skeleton, skin, morph or clip.
type Rig struct {
	Skeleton *RigSkeleton
	Skin     *RigSkin
	Morphs   []*RigMorph
	Clips    []*RigClip
	Idle     string
	Wrappers []string
	// JointPairs maps the original name of each mirrored joint to its
	// reflection's name; a reflected shape bound to a joint takes the pair.
	JointPairs map[string]string
}

// rigState holds the name pairs a mirror declares, so that the reflected
// copy's reference slots take the other symbol.
type rigState struct {
	joint, geom, socket map[string]string
	spans               map[string]diag.Span
}

// mirrorCtx says where an item is evaluated: the original, or the reflected
// copy of the mirror with the given plane.
type mirrorCtx struct {
	plane string // "" outside a mirror and in the original
	copy  bool
}

func (m mirrorCtx) side() float64 {
	if m.copy {
		return -1
	}
	return 1
}

func (m mirrorCtx) reflect() string {
	if m.copy {
		return m.plane
	}
	return ""
}

// rigScope makes the scope of a rig body: its lets, in any order, and `side`
// for the side of a mirror.
func (r *run) rigScope(parent *scope, items []ast.RigItem, m *mirrorCtx) *scope {
	sc := newScope(parent)
	if m != nil && m.plane != "" {
		sc.add(&binding{name: "side", span: diag.Span{}, state: stDone, value: units.Num(m.side())})
	}
	for _, it := range items {
		l, ok := it.(*ast.Let)
		if !ok {
			continue
		}
		if prev, dup := sc.names[l.Name.Name]; dup {
			r.report(diag.New(diag.CodeDuplicate, l.Name.Span, "the name `%s` is declared twice in one body", l.Name.Name).WithRelated(prev.span, "first declaration"))
			continue
		}
		sc.add(&binding{name: l.Name.Name, span: l.Name.Span, let: l, scope: sc})
	}
	for _, name := range sc.order {
		if sc.names[name].let != nil {
			r.force(sc.names[name])
		}
	}
	return sc
}

// pair returns the name a reference slot takes in the copy of a mirror.
func (st *rigState) pair(ns registry.Namespace, name string, m mirrorCtx) string {
	if !m.copy {
		return name
	}
	var tab map[string]string
	switch ns {
	case registry.NsJoint:
		tab = st.joint
	case registry.NsGeometry:
		tab = st.geom
	case registry.NsSocket:
		tab = st.socket
	}
	if p, ok := tab[name]; ok {
		return p
	}
	return name
}

func (st *rigState) pairs(ns registry.Namespace, names []string, m mirrorCtx) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = st.pair(ns, n, m)
	}
	return out
}

// collectPairs records every name pair a rig declares and every pair the
// geometry declares.
func (r *run) collectPairs() *rigState {
	st := &rigState{joint: map[string]string{}, geom: map[string]string{}, socket: map[string]string{}}
	var rig func(items []ast.RigItem)
	rig = func(items []ast.RigItem) {
		for _, it := range items {
			b, ok := it.(*ast.RigBlock)
			if !ok {
				continue
			}
			if b.Pair.Name != "" {
				switch b.Kind {
				case "joint":
					st.joint[b.Name.Name] = b.Pair.Name
				case "socket":
					st.socket[b.Name.Name] = b.Pair.Name
				}
			}
			rig(b.Items)
		}
	}
	for _, d := range r.f.Rigs {
		rig(d.Items)
	}
	var geom func(items []ast.Item)
	geom = func(items []ast.Item) {
		for _, it := range items {
			switch n := it.(type) {
			case *ast.Part:
				if n.Pair.Name != "" {
					st.geom[n.Name.Name] = n.Pair.Name
				}
				geom(n.Items)
			case *ast.Shape:
				if n.Pair.Name != "" {
					st.geom[n.Name.Name] = n.Pair.Name
				}
			case *ast.Array:
				if n.Pair.Name != "" {
					st.geom[n.Name.Name] = n.Pair.Name
				}
				geom(n.Items)
			case *ast.Instance:
				if n.Pair.Name != "" {
					st.geom[n.Name.Name] = n.Pair.Name
				}
			case *ast.Mirror:
				geom(n.Items)
			case *ast.When:
				geom(n.Then)
				geom(n.Else)
			}
		}
	}
	for _, p := range r.f.Parts {
		geom([]ast.Item{p})
	}
	return st
}

// rig evaluates the rig declarations of the file.
func (r *run) rig() {
	if len(r.f.Rigs) == 0 {
		// An idle clip or wrapper list without declarations.
		r.rigAssetOnly()
		return
	}
	st := r.collectPairs()
	rg := &Rig{JointPairs: st.joint}
	r.resolved.Rig = rg
	var clips []*ast.RigDecl
	var morphs []*ast.RigDecl
	var skins []*ast.RigDecl
	for _, d := range r.f.Rigs {
		switch d.Kind {
		case "skeleton":
			if rg.Skeleton != nil {
				r.report(diag.New(diag.CodeUnsupported, d.KindSpan, "a second skeleton is valid in the language but outside this compiler's subset: it writes one joint tree"))
				continue
			}
			rg.Skeleton = r.skeleton(d, st)
		case "skin":
			skins = append(skins, d)
		case "morph":
			morphs = append(morphs, d)
		case "clip":
			clips = append(clips, d)
		}
	}
	if len(skins) > 1 {
		r.report(diag.New(diag.CodeDuplicate, skins[1].KindSpan, "a file has one skin block").WithRelated(skins[0].KindSpan, "first skin block"))
	}
	if len(skins) > 0 {
		if rg.Skeleton == nil {
			r.report(diag.New(diag.CodeMissing, skins[0].KindSpan, "`skin` needs a `skeleton`"))
		} else {
			rg.Skin = r.skin(skins[0], st)
			r.checkSkin(rg)
		}
	}
	if rg.Skeleton != nil {
		r.checkBound(rg)
	}
	r.rigAsset(rg)
	for _, d := range morphs {
		if rg.Skeleton == nil {
			r.report(diag.New(diag.CodeMissing, d.KindSpan, "`morph` needs a `skeleton`: this compiler writes morph targets on a skinned mesh"))
			break
		}
		rg.Morphs = append(rg.Morphs, r.morph(d, st))
	}
	r.checkMorphNames(rg)
	for _, d := range clips {
		if c := r.clip(d, st, rg); c != nil {
			rg.Clips = append(rg.Clips, c)
		}
	}
	r.checkClips(rg)
}

// rigAssetOnly reports an `idle` or `wrappers` field of an asset with no rig
// declarations.
func (r *run) rigAssetOnly() {
	if v, ok := field(r.resolved.Asset, "idle"); ok && v.Valid() {
		if s := ast.FindSetting(r.f.Asset.Settings, "idle"); s != nil {
			r.report(diag.New(diag.CodeUnknownName, s.Value.Span(), "no clip is named `%s`", v.Str))
		}
	}
}

// rigAsset reads the asset's idle clip and wrapper names.
func (r *run) rigAsset(rg *Rig) {
	if v, ok := field(r.resolved.Asset, "wrappers"); ok && v.Valid() {
		seen := map[string]bool{}
		for _, e := range v.Elems {
			if seen[e.Str] {
				if s := ast.FindSetting(r.f.Asset.Settings, "wrappers"); s != nil {
					r.report(diag.New(diag.CodeDuplicate, s.Value.Span(), "the wrapper name %q is listed twice", e.Str))
				}
			}
			seen[e.Str] = true
			rg.Wrappers = append(rg.Wrappers, e.Str)
		}
	}
	if v, ok := field(r.resolved.Asset, "idle"); ok && v.Valid() {
		rg.Idle = v.Str
	}
}

// checkMorphNames reports exported target names that repeat.
func (r *run) checkMorphNames(rg *Rig) {
	seen := map[string]*RigMorph{}
	for _, m := range rg.Morphs {
		if prev, dup := seen[m.Export]; dup {
			r.report(diag.New(diag.CodeDuplicate, m.Span, "two morphs export the target name %q", m.Export).WithRelated(prev.Span, "first morph"))
			continue
		}
		seen[m.Export] = m
	}
}

// triple3 returns the three numbers of a tuple value.
func triple3(v units.Value) [3]float64 {
	a, _ := v.Vec3()
	return a
}

func rangeOf(v units.Value, ok bool) *RigRange {
	if !ok || !v.Valid() || v.Kind != units.KRange || len(v.Elems) != 2 {
		return nil
	}
	return &RigRange{Lo: v.Elems[0].Num, Hi: v.Elems[1].Num}
}

func rigRanges(fs []Field) RigRanges {
	get := func(name string) *RigRange {
		v, ok := field(fs, name)
		return rangeOf(v, ok)
	}
	return RigRanges{Twist: get("twist"), SwingX: get("swing_x"), SwingY: get("swing_y"), SwingZ: get("swing_z")}
}

func rigKind(name string) *registry.Kind {
	k, _ := registry.Lookup(registry.CtxRig, name)
	return k
}

// rigStructure reports a malformed rig tree.
func (r *run) rigStructure(sp diag.Span, format string, args ...any) {
	r.report(diag.New(diag.CodeRigStructure, sp, format, args...))
}

// ---- skeleton ----------------------------------------------------------------

func (r *run) skeleton(d *ast.RigDecl, st *rigState) *RigSkeleton {
	sk := &RigSkeleton{Name: d.Name.Name, Span: d.Span}
	fs := r.fields(docKind("skeleton"), d.Settings, r.globals, d.KindSpan)
	r.jointItems(sk, d.Items, "", mirrorCtx{}, r.globals, st, nil)
	roots := 0
	for _, j := range sk.Joints {
		if j.Parent == "" {
			roots++
		}
	}
	switch {
	case len(sk.Joints) == 0:
		r.rigStructure(d.KindSpan, "a skeleton needs a root joint")
	case roots != 1:
		r.rigStructure(d.KindSpan, "a skeleton has exactly one joint with no parent, found %d: nest the other joints under it", roots)
	}
	if v, ok := field(fs, "joint_order"); ok && v.Valid() {
		for _, e := range v.Elems {
			sk.Order = append(sk.Order, e.Str)
		}
		r.checkOrder(sk, d)
	}
	if v, ok := field(fs, "root_scale"); ok && v.Valid() {
		sk.RootScale = v.Str
		r.checkRootScale(sk, d)
	}
	return sk
}

// checkOrder requires `joint_order` to list every joint exactly once.
func (r *run) checkOrder(sk *RigSkeleton, d *ast.RigDecl) {
	sp := d.KindSpan
	if s := ast.FindSetting(d.Settings, "joint_order"); s != nil && s.Value != nil {
		sp = s.Value.Span()
	}
	have := map[string]bool{}
	for _, j := range sk.Joints {
		have[j.Name] = true
	}
	seen := map[string]bool{}
	for _, n := range sk.Order {
		if !have[n] {
			r.rigStructure(sp, "`joint_order` names `%s`, which is not a joint of the skeleton", n)
		}
		if seen[n] {
			r.rigStructure(sp, "`joint_order` lists `%s` twice", n)
		}
		seen[n] = true
	}
	for _, j := range sk.Joints {
		if !seen[j.Name] {
			r.rigStructure(sp, "`joint_order` leaves out the joint `%s`: it lists every joint exactly once", j.Name)
		}
	}
}

// checkRootScale requires a numeric runtime parameter.
func (r *run) checkRootScale(sk *RigSkeleton, d *ast.RigDecl) {
	sp := d.KindSpan
	if s := ast.FindSetting(d.Settings, "root_scale"); s != nil && s.Value != nil {
		sp = s.Value.Span()
	}
	b := r.globals.lookup(sk.RootScale)
	if b == nil || b.param == nil {
		r.report(diag.New(diag.CodeUnknownName, sp, "`root_scale` names a runtime parameter, and `%s` is not one", sk.RootScale))
		return
	}
	t, ok := registry.ParamType(b.param.Type.Name)
	if !b.param.Runtime || !ok || (t.Kind != registry.TNumber) {
		r.report(diag.New(diag.CodeType, sp, "`root_scale` needs a numeric runtime parameter (number or ratio), and `%s` is not one", sk.RootScale))
	}
}

// jointItems walks the items of a skeleton or joint body. parent is the
// effective name of the joint the items are children of.
func (r *run) jointItems(sk *RigSkeleton, items []ast.RigItem, parent string, m mirrorCtx, sc *scope, st *rigState, cur *RigJoint) {
	sc = r.rigScope(sc, items, &m)
	for _, it := range items {
		switch b := it.(type) {
		case *ast.Row, *ast.SampleRow:
			r.report(diag.New(diag.CodeUnknownField, it.ItemSpan(), "a skeleton holds joints, not rows"))
		case *ast.RigBlock:
			switch b.Kind {
			case "joint":
				r.joint(sk, b, parent, m, sc, st)
			case "socket":
				if parent == "" {
					r.rigStructure(b.KindSpan, "a socket belongs inside a joint")
					continue
				}
				r.socket(sk, b, parent, m, sc)
			case "limit":
				// Handled with its joint.
			case "mirror":
				r.jointMirror(sk, b, parent, m, sc, st)
			default:
				r.report(diag.New(diag.CodeUnknownField, b.KindSpan, "`%s` is not allowed in a skeleton", b.Kind))
			}
		}
	}
}

// mirrorPlane reads the plane of a mirror block.
func (r *run) mirrorPlane(b *ast.RigBlock, sc *scope) (string, bool) {
	// Only `plane` is read here: inside a pose the other settings are joint
	// rotations, which are evaluated in the mirror's own scope.
	var only []*ast.Setting
	if s := ast.FindSetting(b.Settings, "plane"); s != nil {
		only = append(only, s)
	}
	fs := r.fields(rigKind("mirror"), only, sc, b.KindSpan)
	v, ok := field(fs, "plane")
	if !ok || !v.Valid() {
		return "", false
	}
	return v.Str, true
}

// openMirrorOnlyPlane reports settings other than `plane` in a mirror outside
// a pose.
func (r *run) openMirrorOnlyPlane(b *ast.RigBlock) {
	for _, s := range b.Settings {
		if s.Name != "plane" {
			r.report(diag.New(diag.CodeUnknownField, s.NameSpan, "`mirror` has no field `%s` here: only a pose holds joint rotations in a mirror", s.Name))
		}
	}
}

func (r *run) jointMirror(sk *RigSkeleton, b *ast.RigBlock, parent string, m mirrorCtx, sc *scope, st *rigState) {
	r.openMirrorOnlyPlane(b)
	if m.plane != "" {
		r.report(diag.New(diag.CodeUnsupported, b.KindSpan, "a mirror inside a mirror is valid in the language but outside this compiler's subset"))
		return
	}
	plane, ok := r.mirrorPlane(b, sc)
	if !ok {
		return
	}
	r.jointItems(sk, b.Items, parent, mirrorCtx{plane: plane}, sc, st, nil)
	r.jointItems(sk, b.Items, parent, mirrorCtx{plane: plane, copy: true}, sc, st, nil)
}

// declared returns the name a declaration has in the current copy.
func declared(b *ast.RigBlock, m mirrorCtx) string {
	if m.copy && b.Pair.Name != "" {
		return b.Pair.Name
	}
	return b.Name.Name
}

func (r *run) joint(sk *RigSkeleton, b *ast.RigBlock, parent string, m mirrorCtx, sc *scope, st *rigState) {
	name := declared(b, m)
	body := r.rigScope(sc, b.Items, &m)
	fs := r.fields(rigKind("joint"), b.Settings, body, b.KindSpan)
	j := RigJoint{Name: name, Parent: parent, Reflect: m.reflect(), Span: b.Name.Span}
	if v, ok := field(fs, "at"); ok && v.Valid() {
		j.At = triple3(v)
	}
	if v, ok := field(fs, "orient"); ok && v.Valid() {
		j.Orient = triple3(v)
	}
	if v, ok := field(fs, "twist_axis"); ok && v.Valid() {
		j.TwistAxis = v.Str
	}
	j.Band = rigRanges(fs)
	for _, it := range b.Items {
		lb, ok := it.(*ast.RigBlock)
		if !ok || lb.Kind != "limit" {
			continue
		}
		if j.Limit.Any() {
			r.report(diag.New(diag.CodeDuplicate, lb.KindSpan, "a joint has one `limit` block"))
			continue
		}
		j.Limit = rigRanges(r.fields(rigKind("limit"), lb.Settings, body, lb.KindSpan))
	}
	r.checkRanges(b, j)
	sk.Joints = append(sk.Joints, j)
	// Children and sockets.
	r.jointItems(sk, b.Items, name, m, body, st, &j)
}

// checkRanges rejects a swing range about the twist axis when the axis is
// explicit: there is no such component.
func (r *run) checkRanges(b *ast.RigBlock, j RigJoint) {
	if j.TwistAxis == "" {
		return
	}
	bad := map[string]*RigRange{"x": j.Band.SwingX, "y": j.Band.SwingY, "z": j.Band.SwingZ}
	if bad[j.TwistAxis] != nil {
		field := "swing_" + j.TwistAxis
		sp := b.KindSpan
		if s := ast.FindSetting(b.Settings, field); s != nil {
			sp = s.NameSpan
		}
		r.report(diag.New(diag.CodeFieldRange, sp, "`%s` is the swing about the twist axis of `%s`; a twist about %s admits the other two swings", field, j.Name, j.TwistAxis))
	}
}

func (r *run) socket(sk *RigSkeleton, b *ast.RigBlock, parent string, m mirrorCtx, sc *scope) {
	fs := r.fields(rigKind("socket"), b.Settings, sc, b.KindSpan)
	s := RigSocket{Name: declared(b, m), Joint: parent, Reflect: m.reflect(), Span: b.Name.Span}
	if v, ok := field(fs, "kind"); ok && v.Valid() {
		s.Kind = v.Str
	}
	if v, ok := field(fs, "at"); ok && v.Valid() {
		s.At, s.HasAt = triple3(v), true
	}
	if v, ok := field(fs, "rot"); ok && v.Valid() {
		s.Rot = triple3(v)
	}
	sk.Sockets = append(sk.Sockets, s)
}

// checkBound reports a rendered shape that has no joint (language reference
// 5.4: a missing binding is GW-RIG-UNBOUND).
func (r *run) checkBound(rg *Rig) {
	var walk func(n *Node, bound bool)
	walk = func(n *Node, bound bool) {
		if n == nil {
			return
		}
		switch n.Kind {
		case "part":
			if v, ok := n.Get("joint"); ok && v.Valid() && v.Kind == units.KRef {
				bound = true
			}
		case "mirror", "array", "when", "instance", "socket":
		default:
			if v, ok := n.Get("joint"); ok && v.Valid() && v.Kind == units.KRef {
				return
			}
			if !bound && n.Name != "" || !bound {
				name := n.Name
				if name == "" {
					name = n.Kind
				}
				r.report(diag.New(diag.CodeRigUnbound, r.shapeSpan(n), "the %s `%s` has no joint: bind it with `joint = name` on the shape or on a part that holds it", n.Kind, name))
			}
			return
		}
		for _, c := range n.Children {
			walk(c, bound)
		}
		for _, c := range n.Else {
			walk(c, bound)
		}
	}
	walk(r.resolved.Root, false)
	for _, d := range r.resolved.Definitions {
		walk(d, false)
	}
}

// shapeSpan finds the span of the first geometry declaration with n's name.
func (r *run) shapeSpan(n *Node) diag.Span {
	var find func(items []ast.Item) (diag.Span, bool)
	find = func(items []ast.Item) (diag.Span, bool) {
		for _, it := range items {
			switch x := it.(type) {
			case *ast.Part:
				if x.Name.Name == n.Name && n.Kind == "part" {
					return x.Name.Span, true
				}
				if sp, ok := find(x.Items); ok {
					return sp, true
				}
			case *ast.Shape:
				if n.Name != "" && (x.Name.Name == n.Name || x.Pair.Name == n.Name) && x.Kind == n.Kind {
					return x.KindSpan, true
				}
			case *ast.Array:
				if sp, ok := find(x.Items); ok {
					return sp, true
				}
			case *ast.Mirror:
				if sp, ok := find(x.Items); ok {
					return sp, true
				}
			case *ast.When:
				if sp, ok := find(x.Then); ok {
					return sp, true
				}
				if sp, ok := find(x.Else); ok {
					return sp, true
				}
			}
		}
		return diag.Span{}, false
	}
	for _, p := range r.f.Parts {
		if sp, ok := find([]ast.Item{p}); ok {
			return sp
		}
	}
	return diag.Span{}
}
