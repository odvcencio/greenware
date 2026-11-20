package compiler

import (
	"math"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/kernel/rig"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

// This file builds the rig of a level: the skeleton, the skin weights from the
// ordered steps, the morph targets and the clips, with package kernel/rig.
// gltfrig.go writes them as glTF.

// rigged reports whether the asset has a skeleton.
func (e *elab) rigged() bool { return e.r.Rig != nil && e.r.Rig.Skeleton != nil }

// jointName is the joint a reference names in the current copy of a mirror.
func (e *elab) jointName(name string, refl bool) string {
	if refl && e.r.Rig != nil {
		if p, ok := e.r.Rig.JointPairs[name]; ok {
			return p
		}
	}
	return name
}

// shapeMeta says what a shape is, for the skin and the morphs.
func (e *elab) shapeMeta(n *resolve.Node, c wctx, name string) pieceMeta {
	m := pieceMeta{joint: c.joint, line: shapeLine(n)}
	if n.Kind == "loft" {
		m.line = e.loftLine(n)
	}
	if f, ok := n.Get("joint"); ok && f.Valid() && f.Kind == unitsRef {
		m.joint = e.jointName(f.Str, c.refl)
	}
	m.names = append(m.names, c.chain...)
	if name != "" {
		m.names = append(m.names, name)
	}
	return m
}

// shapeLine is the centre line of a shape in its own frame: the polyline a
// morph's `inflate` grows the shape about. It is nil for a shape without one.
func shapeLine(n *resolve.Node) []mesh.Vec3 {
	switch n.Kind {
	case "sphere":
		return []mesh.Vec3{vec3(n, "at")}
	case "ellipsoid":
		return []mesh.Vec3{mesh.Add(vec3(n, "centre"), vec3(n, "at"))}
	case "cylinder":
		ax, ok := axisOf(unitsField(n, "axis"))
		if !ok {
			return nil
		}
		a := mesh.Vec3{}
		a[map[rune]int{'x': 0, 'y': 1, 'z': 2}[ax]] = num(n, "height") / 2
		at := vec3(n, "at")
		return []mesh.Vec3{mesh.Sub(at, a), mesh.Add(at, a)}
	case "lathe":
		ax, ok := axisOf(unitsField(n, "axis"))
		if !ok || len(n.Rows) == 0 {
			return nil
		}
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, r := range n.Rows {
			if len(r.Values) == 2 {
				lo, hi = math.Min(lo, r.Values[1].Num), math.Max(hi, r.Values[1].Num)
			}
		}
		i := map[rune]int{'x': 0, 'y': 1, 'z': 2}[ax]
		a, b := vec3(n, "at"), vec3(n, "at")
		a[i], b[i] = a[i]+lo, b[i]+hi
		return []mesh.Vec3{a, b}
	case "sweep", "tube":
		path := n.Rows
		if n.Kind == "sweep" {
			p := child(n, "path")
			if p == nil {
				return nil
			}
			path = p.Rows
		}
		at := vec3(n, "at")
		var out []mesh.Vec3
		for _, r := range path {
			if len(r.Values) != 3 {
				return nil
			}
			out = append(out, mesh.Add(mesh.Vec3{r.Values[0].Num, r.Values[1].Num, r.Values[2].Num}, at))
		}
		return out
	}
	return nil
}

// loftLine is the centre line of a loft: the centres of its rings, in the
// loft's frame, from a full turn at no fewer than 8 columns.
func (e *elab) loftLine(n *resolve.Node) []mesh.Vec3 {
	grid, err := e.loftGrid(n, max(int(num(n, "segments")), 8), false, 0, twoPi)
	if err != nil || len(grid) == 0 {
		return nil
	}
	at := vec3(n, "at")
	out := make([]mesh.Vec3, 0, len(grid))
	for _, ring := range grid {
		var c mesh.Vec3
		for _, p := range ring {
			c = mesh.Add(c, p)
		}
		out = append(out, mesh.Add(mesh.Scale(c, 1/float64(len(ring))), at))
	}
	return out
}

// pieceRig is the rig data of one piece: the influences of each position, and
// for each morph target the position delta of each position and the normal
// delta of each corner.
type pieceRig struct {
	infl [][]rig.Influence
	pos  [][]mesh.Vec3 // [target][position]
	nrm  [][]mesh.Vec3 // [target][corner]
}

// upIndex is the index of the asset's up axis.
func (e *elab) upIndex() int {
	if v, ok := fieldOf(e.r.Asset, "up"); ok {
		switch v.Str {
		case "+x", "-x":
			return 0
		case "+z", "-z":
			return 2
		}
	}
	return 1
}

func fieldOf(fs []resolve.Field, name string) (units.Value, bool) {
	for _, f := range fs {
		if f.Name == name {
			return f.Value, true
		}
	}
	return units.Bad(), false
}

func planeIndex(p string) int {
	switch p {
	case "y":
		return 1
	case "z":
		return 2
	}
	return 0
}

// reflectVec reflects a point or direction across the plane of the mirror.
func reflectVec(v mesh.Vec3, plane string) mesh.Vec3 {
	v[planeIndex(plane)] = -v[planeIndex(plane)]
	return v
}

func reflectQuat(q rig.Quat, plane string) rig.Quat {
	return q.Reflect(mesh.Plane(planeIndex(plane)))
}

// reflectRange reverses and negates a signed range.
func reflectRange(r rig.Range) rig.Range {
	if !r.Set {
		return r
	}
	return rig.Range{Set: true, Lo: -r.Hi, Hi: -r.Lo}
}

func kernelRange(r *resolve.RigRange) rig.Range {
	if r == nil {
		return rig.Range{}
	}
	return rig.Range{Set: true, Lo: r.Lo, Hi: r.Hi}
}

// ranges converts the ranges of a joint; for a reflected joint a component
// that changes sign under the mirror is reversed and negated.
func ranges(rr resolve.RigRanges, plane string, twistAxis int) rig.Ranges {
	out := rig.Ranges{Twist: kernelRange(rr.Twist), AnySet: rr.Any()}
	out.Swing = [3]rig.Range{kernelRange(rr.SwingX), kernelRange(rr.SwingY), kernelRange(rr.SwingZ)}
	if plane == "" {
		return out
	}
	pa := planeIndex(plane)
	if twistAxis != pa {
		out.Twist = reflectRange(out.Twist)
	}
	for c := 0; c < 3; c++ {
		if c != pa {
			out.Swing[c] = reflectRange(out.Swing[c])
		}
	}
	return out
}

// skeletonData is the built skeleton and what is attached to its joints.
type skeletonData struct {
	sk      *rig.Skeleton // joint order; binds before the skin steps
	src     []resolve.RigJoint
	band    []rig.Ranges
	limit   []rig.Ranges
	index   map[string]int // lookup only
	sockets []resolve.RigSocket
}

// buildSkeleton makes the kernel skeleton from the resolved joints, in joint
// order.
func (e *elab) buildSkeleton() (*skeletonData, bool) {
	rs := e.r.Rig.Skeleton
	order := rs.Order
	if order == nil {
		for _, j := range rs.Joints {
			order = append(order, j.Name)
		}
	}
	by := map[string]resolve.RigJoint{} // lookup only
	for _, j := range rs.Joints {
		by[j.Name] = j
	}
	idx := map[string]int{} // lookup only
	for i, n := range order {
		idx[n] = i
	}
	var js []rig.Joint
	var src []resolve.RigJoint
	for _, n := range order {
		j, ok := by[n]
		if !ok {
			e.report(diag.CodeRigStructure, rs.Span, "`joint_order` names `%s`, which is not a joint", n)
			return nil, false
		}
		q := rig.QEuler(j.Orient[0], j.Orient[1], j.Orient[2])
		pos := mesh.Vec3(j.At)
		if j.Reflect != "" {
			q, pos = reflectQuat(q, j.Reflect), reflectVec(pos, j.Reflect)
		}
		kj := rig.Joint{Name: j.Name, Parent: -1, Pos: pos, Rot: q.Normalize(), TwistAxis: -1}
		if j.Parent != "" {
			p, ok := idx[j.Parent]
			if !ok {
				e.report(diag.CodeRigStructure, j.Span, "the parent `%s` of `%s` is not a joint", j.Parent, j.Name)
				return nil, false
			}
			kj.Parent = p
		}
		switch j.TwistAxis {
		case "x":
			kj.TwistAxis = 0
		case "y":
			kj.TwistAxis = 1
		case "z":
			kj.TwistAxis = 2
		}
		js = append(js, kj)
		src = append(src, j)
	}
	sk, err := rig.NewSkeleton(js)
	if err != nil {
		e.report(diag.CodeRigStructure, rs.Span, "%v", err)
		return nil, false
	}
	d := &skeletonData{sk: sk, src: src, index: idx, sockets: rs.Sockets}
	for i, j := range src {
		ax := sk.TwistAxisOf(i)
		d.band = append(d.band, ranges(j.Band, j.Reflect, ax))
		d.limit = append(d.limit, ranges(j.Limit, j.Reflect, ax))
	}
	return d, true
}

// selectIDs lists the pieces that answer to any of the names.
func selectIDs(pieces []piece, names []string) []int {
	var out []int
	for i, p := range pieces {
		for _, n := range names {
			hit := false
			for _, pn := range p.meta.names {
				if pn == n {
					hit = true
					break
				}
			}
			if hit {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// anchorPoint is a model-space point written as a point or joint(name) plus
// a point, read at the binds sk has now. A reflected copy reflects the
// result once.
func anchorPoint(a resolve.RigAnchor, sk *rig.Skeleton, plane string) mesh.Vec3 {
	p := mesh.Vec3(a.Offset)
	if a.Joint != "" {
		if i := sk.Index(a.Joint); i >= 0 {
			p = mesh.Add(sk.Joints[i].Pos, p)
		}
	}
	if plane != "" {
		p = reflectVec(p, plane)
	}
	return p
}

// rigLevel is the rig of one level.
type rigLevel struct {
	sd      *skeletonData
	sk      *rig.Skeleton // binds after the skin steps
	rest    []rig.TRS     // original local rest
	pieces  []piece       // the level's pieces with rig data
	morphs  []morphOut
	offsets []rig.Offset
	weights []float64 // default morph weights
	inst    *rig.Instance
	maxInfl int
	verts   int // bound positions
}

type morphOut struct {
	name   string
	weight resolve.RigWeight
	follow bool
}

// buildRigLevel runs the skin steps and the morphs on the level's pieces.
func (e *elab) buildRigLevel(root *snode) (*rigLevel, bool) {
	rg := e.r.Rig
	sd, ok := e.buildSkeleton()
	if !ok {
		return nil, false
	}
	if len(root.kids) > 0 {
		for _, k := range root.kids {
			if len(k.pieces) > 0 || len(k.kids) > 0 {
				e.report(diag.CodeUnsupported, e.spanOf(k.name), "an exported part (`%s`) inside a skinned asset is valid in the language but outside this compiler's subset: a skinned asset writes one skinned mesh", k.name)
				return nil, false
			}
		}
	}
	// The model frame is the frame of the root part's origin; a skinned mesh
	// node's own transform is ignored by a reader, so the root's offset moves
	// into the positions.
	shift := root.t
	root.t = mesh.Vec3{}
	pieces := make([]piece, len(root.pieces))
	for i, p := range root.pieces {
		pieces[i] = p
		if shift != (mesh.Vec3{}) {
			pieces[i].m = p.m.Translate(shift)
			if p.meta.line != nil {
				ln := make([]mesh.Vec3, len(p.meta.line))
				for k, q := range p.meta.line {
					ln[k] = mesh.Add(q, shift)
				}
				pieces[i].meta.line = ln
			}
		}
	}
	for i := range root.sockets {
		root.sockets[i].pos = mesh.Add(root.sockets[i].pos, shift)
	}
	sk := sd.sk.Clone()
	b := &rig.Bound{}
	first := make([]int, len(pieces))
	for i, p := range pieces {
		ji := sk.Index(p.meta.joint)
		if ji < 0 {
			e.report(diag.CodeRigUnbound, e.spanOf(firstName(p)), "the shape `%s` has no joint", firstName(p))
			return nil, false
		}
		first[i] = b.AddShape(i, p.m.Positions, ji)
	}
	real := len(b.Pos)
	// The centre line of each shape rides the steps as extra positions bound
	// the way the shape is, so that a morph that grows the shape works about
	// the finished line, not the authored one. Their shape ids are negative:
	// no part list, `rigid` or `only` selects them.
	lineFirst := make([]int, len(pieces))
	for i, p := range pieces {
		lineFirst[i] = -1
		if len(p.meta.line) > 0 {
			lineFirst[i] = b.AddShape(-1-i, p.meta.line, sk.Index(p.meta.joint))
		}
	}
	maxInfl := 4
	if rg.Skin != nil {
		maxInfl = rg.Skin.MaxInfluences
		for _, st := range rg.Skin.Steps {
			if !e.runStep(st, sk, b, pieces) {
				return nil, false
			}
		}
	}
	if err := b.Finish(maxInfl); err != nil {
		sp := rg.Skeleton.Span
		if rg.Skin != nil {
			sp = rg.Skin.Span
		}
		e.report(diag.CodeRigStructure, sp, "the skin weights are not valid: %v", err)
		return nil, false
	}
	rl := &rigLevel{sd: sd, sk: sk, rest: sk.Rest(), maxInfl: 0, verts: real}
	for _, in := range b.Infl[:real] {
		rl.maxInfl = max(rl.maxInfl, len(in))
	}
	// Pieces with the finished bind positions, and their normals.
	rl.pieces = make([]piece, len(pieces))
	for i, p := range pieces {
		np := p
		n := len(p.m.Positions)
		moved := false
		for k := 0; k < n; k++ {
			if b.Pos[first[i]+k] != p.m.Positions[k] {
				moved = true
				break
			}
		}
		if moved {
			m := p.m.Clone()
			copy(m.Positions, b.Pos[first[i]:first[i]+n])
			m.ComputeNormals()
			np.m = m
		}
		np.rig = &pieceRig{infl: b.Infl[first[i] : first[i]+n]}
		if lineFirst[i] >= 0 {
			np.meta.line = append([]mesh.Vec3(nil), b.Pos[lineFirst[i]:lineFirst[i]+len(p.meta.line)]...)
		}
		rl.pieces[i] = np
	}
	// Morphs.
	defaults := make([]float64, len(rg.Morphs))
	for mi, m := range rg.Morphs {
		tpos, ok := e.buildMorph(m, mi, rl, b)
		if !ok {
			return nil, false
		}
		defaults[mi] = m.Weight.Default
		rl.morphs = append(rl.morphs, morphOut{name: m.Export, weight: m.Weight, follow: m.Follow})
		for i := range rl.pieces {
			p := &rl.pieces[i]
			n := len(p.m.Positions)
			dpos := rig.Deltas(b.Pos[first[i]:first[i]+n], tpos[first[i]:first[i]+n])
			// Normals: recompute both surfaces and take the difference.
			base := p.m.Clone()
			base.ComputeNormals()
			tm := p.m.Clone()
			copy(tm.Positions, tpos[first[i]:first[i]+n])
			tm.ComputeNormals()
			dn := make([]mesh.Vec3, len(p.m.Corners))
			for ci := range dn {
				dn[ci] = mesh.Sub(tm.Corners[ci].Normal, base.Corners[ci].Normal)
			}
			p.rig.pos = append(p.rig.pos, dpos)
			p.rig.nrm = append(p.rig.nrm, dn)
		}
		rl.offsets = append(rl.offsets, e.followOffsets(m, mi, rl)...)
	}
	rl.weights = defaults
	rl.inst = &rig.Instance{Sk: rl.sk, Rest: rl.rest, Offsets: rl.offsets, Weights: defaults, Up: e.upIndex()}
	if rs := rg.Skeleton.RootScale; rs != "" {
		for _, p := range e.r.Params {
			if p.Name == rs {
				rl.inst.HasRootScale, rl.inst.RootScale = true, p.Value.Num
			}
		}
	}
	return rl, true
}

func firstName(p piece) string {
	if n := len(p.meta.names); n > 0 {
		return p.meta.names[n-1]
	}
	return ""
}

// runStep runs one skin step.
func (e *elab) runStep(st resolve.RigStep, sk *rig.Skeleton, b *rig.Bound, pieces []piece) bool {
	if l := st.Lengthen; l != nil {
		ji := sk.Index(l.Joint)
		if ji < 0 {
			e.report(diag.CodeRigStructure, l.Span, "`lengthen` names `%s`, which is not a joint", l.Joint)
			return false
		}
		if err := b.Lengthen(sk, ji, l.To, l.Anchor, selectIDs(pieces, l.Rigid)); err != nil {
			e.report(diag.CodeRigStructure, l.Span, "%v", err)
			return false
		}
		return true
	}
	bl := st.Blend
	pi, ci := sk.Index(bl.Parent), sk.Index(bl.Child)
	if pi < 0 || ci < 0 {
		e.report(diag.CodeRigStructure, bl.Span, "the blend `%s` names a joint that is not in the skeleton", bl.Name)
		return false
	}
	axis := mesh.Vec3(bl.Axis)
	if bl.Reflect != "" {
		axis = reflectVec(axis, bl.Reflect)
	}
	k := rig.Blend{
		Parent: pi, Child: ci, Centre: anchorPoint(bl.Centre, sk, bl.Reflect), Axis: axis,
		Width: bl.Width, Bias: bl.Bias, Only: selectIDs(pieces, bl.Only),
	}
	if bl.HasFade {
		k.Fade = rig.FadeX{Set: true, Lo: bl.FadeLo, Hi: bl.FadeHi, Sign: bl.FadeSign}
	}
	if len(bl.Only) > 0 && len(k.Only) == 0 {
		// A band that names shapes the level dropped has nothing to work on.
		return true
	}
	if err := b.Blend(k); err != nil {
		e.report(diag.CodeRigStructure, bl.Span, "%v", err)
		return false
	}
	return true
}

// buildMorph applies a morph's operations to a copy of the finished bind
// positions at weight 1 and returns the result.
func (e *elab) buildMorph(m *resolve.RigMorph, mi int, rl *rigLevel, b *rig.Bound) ([]mesh.Vec3, bool) {
	tpos := append([]mesh.Vec3(nil), b.Pos...)
	lines := make([][]mesh.Vec3, len(rl.pieces))
	for i, p := range rl.pieces {
		lines[i] = p.meta.line
	}
	for _, op := range m.Ops {
		ids := selectIDs(rl.pieces, op.Parts)
		switch op.Kind {
		case "inflate":
			for _, id := range ids {
				if len(lines[id]) == 0 {
					e.report(diag.CodeUnsupported, op.Span, "`inflate` of the shape `%s` is valid in the language but outside this compiler's subset: it grows a sphere, ellipsoid, cylinder, lathe, loft, sweep or tube about its centre line", firstName(rl.pieces[id]))
					return nil, false
				}
			}
			rig.Inflate(tpos, b.Shape, ids, lines, op.By)
		case "stretch":
			if err := rig.Stretch(tpos, map[string]int{"x": 0, "y": 1, "z": 2}[op.Axis], op.From, op.To, op.By); err != nil {
				e.report(diag.CodeFieldRange, op.Span, "%v", err)
				return nil, false
			}
		case "reshape":
			rig.Reshape(tpos, b.Shape, ids, anchorPoint(op.About, rl.sk, op.Reflect), mesh.Vec3(op.Scale))
		case "offset":
			by := mesh.Vec3(op.Vec)
			if op.Reflect != "" {
				by = reflectVec(by, op.Reflect)
			}
			rig.Translate(tpos, b.Shape, ids, by)
		}
	}
	return tpos, true
}

// followOffsets returns the joint offsets of a morph with `joints = follow`:
// the stretches of the morph, in order, applied to every bind point.
func (e *elab) followOffsets(m *resolve.RigMorph, mi int, rl *rigLevel) []rig.Offset {
	if !m.Follow {
		return nil
	}
	move := func(p mesh.Vec3) mesh.Vec3 {
		for _, op := range m.Ops {
			if op.Kind != "stretch" {
				continue
			}
			ax := map[string]int{"x": 0, "y": 1, "z": 2}[op.Axis]
			p[ax] += rig.StretchShift(p[ax], op.From, op.To, op.By)
		}
		return p
	}
	return rig.Follow(rl.sk, move, mi)
}
