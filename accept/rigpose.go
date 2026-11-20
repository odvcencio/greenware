package accept

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/gltf"
	kgeom "m31labs.dev/greenware/kernel/geom"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/units"
)

// Rig measurements on a decoded file (language reference 5.6, 5.9; conformance
// 7.2 and 8). The evaluator reads only the decoded document: the joints and
// inverse binds of the skin, the morph targets, the animations, and the
// Greenware data in the asset extras (the runtime bindings and `rig_base`).
// It poses the skin at a runtime point by the rules of reference 5.6:
//
//   - a morph weight is the binding's table at the parameter's value;
//   - a joint's adjusted rest is its original rest plus the weighted
//     joint offsets, and the inverse binds are those of the adjusted rest;
//   - a translation key is relative to the original rest, so the applied
//     translation is the key plus the offsets, the root's up component being
//     the key times the root scale and receiving no offset;
//   - a rotation key is applied as written.
//
// It is an evaluator independent of the compiler's: it shares no code with
// package compiler and no kernel rig code, only package geom for distances.
//
// Not measured here: the waist section and seam probes, the hip crossing and
// the swing-and-twist bands. They need the declared joint ranges and the audit
// probes of 5.9, and M5 solves weights on them.

type rtBinding struct {
	Kind  string
	Morph string
	Joint string
	Axis  string
	Delta [3]float64
	Map   struct {
		Table   string
		Points  [][2]float64
		Entries []struct {
			Value  string
			Weight float64
		}
	}
}

type rtParam struct {
	Name     string
	Type     string
	Default  json.RawMessage
	Range    []float64
	Values   []string
	Bindings []rtBinding
}

type baseRest struct {
	Joint       string
	Translation [3]float64
	Rotation    *[4]float64
}

type baseChannel struct {
	Joint    string
	Property string
	Times    []float64
	Values   []float64
}

type rigBase struct {
	Rest  []baseRest
	Clips []struct {
		Clip     string
		Channels []baseChannel
	}
}

type rigDoc struct {
	sc       *scene
	skin     *gltf.Skin
	meshIdx  int
	jointOf  map[int]int // node -> joint index
	names    []string    // joint names
	params   []rtParam
	base     *rigBase
	targets  []string
	defaultW []float64
	root     int // joint index of the root
	prims    []int
	hasBase  bool
}

// extrasOf reads the Greenware asset data of a document.
func extrasOf(doc *gltf.Document) (rt []rtParam, base *rigBase, ok bool) {
	var ex struct {
		Greenware struct {
			Runtime struct{ Params []rtParam }
			RigBase *rigBase `json:"rig_base"`
		}
	}
	if len(doc.Extras) == 0 || json.Unmarshal(doc.Extras, &ex) != nil {
		return nil, nil, false
	}
	return ex.Greenware.Runtime.Params, ex.Greenware.RigBase, true
}

// newRigDoc prepares the rig of a scene; it refuses a file with no skin.
func newRigDoc(sc *scene) (*rigDoc, *evalErr) {
	doc := sc.doc
	if len(doc.Skins) == 0 {
		return nil, refuse("the file has no skin")
	}
	rd := &rigDoc{sc: sc, skin: &doc.Skins[0], jointOf: map[int]int{}, meshIdx: -1}
	for i, n := range doc.Nodes {
		if n.Skin != nil && n.Mesh != nil {
			rd.meshIdx = *n.Mesh
			break
		}
		_ = i
	}
	if rd.meshIdx < 0 {
		return nil, refuse("no node of the file carries the skin and a mesh")
	}
	for ji, n := range rd.skin.Joints {
		rd.jointOf[n] = ji
		rd.names = append(rd.names, doc.Nodes[n].Name)
	}
	for ji, n := range rd.skin.Joints {
		if p := sc.nodes[n].parent; p < 0 || func() bool { _, isJoint := rd.jointOf[p]; return !isJoint }() {
			rd.root = ji
			break
		}
	}
	m := doc.Meshes[rd.meshIdx]
	rd.targets = m.TargetNames
	for _, w := range m.Weights {
		rd.defaultW = append(rd.defaultW, float64(w))
	}
	rd.params, rd.base, rd.hasBase = extrasOf(doc)
	return rd, nil
}

// weightTable evaluates a morph binding at a parameter value.
func (b rtBinding) weight(v units.Value) (float64, bool) {
	switch b.Map.Table {
	case "piecewise_linear":
		pts := b.Map.Points
		if len(pts) == 0 || v.Kind != units.KNumber {
			return 0, false
		}
		x := v.Num
		if x <= pts[0][0] {
			return pts[0][1], true
		}
		for i := 1; i < len(pts); i++ {
			if x <= pts[i][0] {
				t := (x - pts[i-1][0]) / (pts[i][0] - pts[i-1][0])
				return pts[i-1][1] + float64(t*(pts[i][1]-pts[i-1][1])), true
			}
		}
		return pts[len(pts)-1][1], true
	case "lookup":
		for _, e := range b.Map.Entries {
			if e.Value == v.Str {
				return e.Weight, true
			}
		}
	}
	return 0, false
}

// defaultValue reads a parameter's default from the runtime table.
func (p rtParam) defaultValue() units.Value {
	var f float64
	if json.Unmarshal(p.Default, &f) == nil {
		return units.Num(f)
	}
	var s string
	if json.Unmarshal(p.Default, &s) == nil {
		return units.Label(s)
	}
	var b bool
	if json.Unmarshal(p.Default, &b) == nil {
		return units.Switch(b)
	}
	return units.Bad()
}

type quat [4]float64

func (q quat) mul(o quat) quat {
	return quat{
		float64(q[3]*o[0]) + float64(q[0]*o[3]) + float64(q[1]*o[2]) - float64(q[2]*o[1]),
		float64(q[3]*o[1]) - float64(q[0]*o[2]) + float64(q[1]*o[3]) + float64(q[2]*o[0]),
		float64(q[3]*o[2]) + float64(q[0]*o[1]) - float64(q[1]*o[0]) + float64(q[2]*o[3]),
		float64(q[3]*o[3]) - float64(q[0]*o[0]) - float64(q[1]*o[1]) - float64(q[2]*o[2]),
	}
}

func slerp(a, b quat, t float64) quat {
	d := float64(a[0]*b[0]) + float64(a[1]*b[1]) + float64(a[2]*b[2]) + float64(a[3]*b[3])
	if d < 0 {
		for i := range b {
			b[i] = -b[i]
		}
		d = -d
	}
	var wa, wb float64
	if d > 0.9995 {
		wa, wb = 1-t, t
	} else {
		th := math.Acos(d)
		s := math.Sin(th)
		wa, wb = math.Sin(float64((1-t)*th))/s, math.Sin(float64(t*th))/s
	}
	var o quat
	for i := range o {
		o[i] = float64(wa*a[i]) + float64(wb*b[i])
	}
	return o
}

func (q quat) affine(t [3]float64) affine {
	l := det.Sqrt(float64(q[0]*q[0]) + float64(q[1]*q[1]) + float64(q[2]*q[2]) + float64(q[3]*q[3]))
	x, y, z, w := q[0]/l, q[1]/l, q[2]/l, q[3]/l
	xx, yy, zz := float64(x*x), float64(y*y), float64(z*z)
	xy, xz, yz := float64(x*y), float64(x*z), float64(y*z)
	wx, wy, wz := float64(w*x), float64(w*y), float64(w*z)
	return affine{m: [3][3]float64{
		{1 - float64(2*(yy+zz)), float64(2 * (xy - wz)), float64(2 * (xz + wy))},
		{float64(2 * (xy + wz)), 1 - float64(2*(xx+zz)), float64(2 * (yz - wx))},
		{float64(2 * (xz - wy)), float64(2 * (yz + wx)), 1 - float64(2*(xx+yy))},
	}, t: t}
}

func (a affine) inverse() affine {
	var o affine
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			o.m[i][j] = a.m[j][i] // a rigid transform
		}
	}
	for i := 0; i < 3; i++ {
		o.t[i] = -(float64(o.m[i][0]*a.t[0]) + float64(o.m[i][1]*a.t[1]) + float64(o.m[i][2]*a.t[2]))
	}
	return o
}

// sample1 interpolates flat key values linearly, clamped to the keys.
func sample1(times, vals []float64, comps int, t float64) []float64 {
	n := len(times)
	out := make([]float64, comps)
	if n == 0 {
		return out
	}
	val := func(k int) []float64 { return vals[k*comps : (k+1)*comps] }
	if t <= times[0] {
		copy(out, val(0))
		return out
	}
	if t >= times[n-1] {
		copy(out, val(n-1))
		return out
	}
	k := sort.Search(n, func(i int) bool { return times[i] > t }) - 1
	f := (t - times[k]) / (times[k+1] - times[k])
	a, b := val(k), val(k+1)
	for i := range out {
		out[i] = a[i] + float64(f*(b[i]-a[i]))
	}
	return out
}

// poseState is the skinned mesh at one pose.
type poseState struct {
	world []affine
	// prims holds the skinned positions of each primitive of the skinned mesh.
	prims [][]mesh.Vec3
}

func (rd *rigDoc) upIndex() int {
	for _, p := range rd.params {
		for _, b := range p.Bindings {
			if b.Kind == "root_scale" {
				return map[string]int{"x": 0, "y": 1, "z": 2}[b.Axis]
			}
		}
	}
	return 1
}

// pose poses the skin. clip "" is the adjusted rest.
func (rd *rigDoc) pose(clip string, t float64, set map[string]units.Value) (*poseState, *evalErr) {
	doc := rd.sc.doc
	if !rd.hasBase || rd.base == nil {
		return nil, unavailable("the file records no `rig_base`, which a pose at another runtime point is built from")
	}
	vals := map[string]units.Value{}
	for _, p := range rd.params {
		vals[p.Name] = p.defaultValue()
	}
	for k, v := range set {
		if _, ok := vals[k]; !ok {
			return nil, refuse("`%s` is not a runtime parameter of the file", k)
		}
		vals[k] = v
	}
	// Morph weights and joint offsets.
	w := append([]float64(nil), rd.defaultW...)
	idx := map[string]int{}
	for i, n := range rd.targets {
		idx[n] = i
	}
	for _, p := range rd.params {
		for _, b := range p.Bindings {
			if b.Kind != "morph" {
				continue
			}
			ti, ok := idx[b.Morph]
			if !ok {
				continue
			}
			if x, ok := b.weight(vals[p.Name]); ok {
				w[ti] = x
			}
		}
	}
	offset := make([][3]float64, len(rd.names))
	var rootScale *float64
	for _, p := range rd.params {
		for _, b := range p.Bindings {
			switch b.Kind {
			case "joint_offset":
				ji := indexOf(rd.names, b.Joint)
				ti, ok := idx[b.Morph]
				if ji < 0 || !ok {
					continue
				}
				for c := 0; c < 3; c++ {
					offset[ji][c] += float64(b.Delta[c] * w[ti])
				}
			case "root_scale":
				s := vals[p.Name].Num
				rootScale = &s
			}
		}
	}
	up := rd.upIndex()
	rest := make([]baseRest, len(rd.names))
	for i, n := range rd.names {
		rest[i] = baseRest{Joint: n}
		for _, r := range rd.base.Rest {
			if r.Joint == n {
				rest[i] = r
			}
		}
	}
	restQ := func(r baseRest) quat {
		if r.Rotation != nil {
			return quat{r.Rotation[0], r.Rotation[1], r.Rotation[2], r.Rotation[3]}
		}
		return quat{0, 0, 0, 1}
	}
	// The posed local TRS of every joint.
	type trs struct {
		t [3]float64
		q quat
	}
	adj := make([]trs, len(rd.names))
	loc := make([]trs, len(rd.names))
	for i, r := range rest {
		var tt [3]float64
		for c := 0; c < 3; c++ {
			tt[c] = r.Translation[c] + offset[i][c]
		}
		adj[i] = trs{tt, restQ(r)}
		loc[i] = adj[i]
	}
	if clip != "" {
		found := false
		for _, c := range rd.base.Clips {
			if c.Clip != clip {
				continue
			}
			found = true
			for _, ch := range c.Channels {
				ji := indexOf(rd.names, ch.Joint)
				if ji < 0 || ch.Property != "translation" {
					continue
				}
				k := sample1(ch.Times, ch.Values, 3, t)
				off := offset[ji]
				if ji == rd.root && rootScale != nil {
					k[up] = float64(k[up] * *rootScale)
					off[up] = 0
				}
				for c := 0; c < 3; c++ {
					loc[ji].t[c] = k[c] + off[c]
				}
			}
		}
		for _, a := range doc.Animations {
			if a.Name != clip {
				continue
			}
			found = true
			for _, ch := range a.Channels {
				ji, ok := rd.jointOf[ch.Node]
				if !ok || ch.Path != gltf.PathRotation {
					continue
				}
				s := a.Samplers[ch.Sampler]
				times := make([]float64, len(s.Input))
				for i, x := range s.Input {
					times[i] = float64(x)
				}
				vals := make([]float64, len(s.Output))
				for i, x := range s.Output {
					vals[i] = float64(x)
				}
				loc[ji].q = quatAt(times, vals, t)
			}
		}
		if !found {
			return nil, refuse("the file has no animation named %q", clip)
		}
	}
	worlds := func(joints []trs) []affine {
		out := make([]affine, len(doc.Nodes))
		done := make([]bool, len(doc.Nodes))
		var rec func(i int) affine
		rec = func(i int) affine {
			if done[i] {
				return out[i]
			}
			local := localTransform(&doc.Nodes[i])
			if ji, ok := rd.jointOf[i]; ok {
				local = joints[ji].q.affine(joints[ji].t)
			}
			wd := local
			if p := rd.sc.nodes[i].parent; p >= 0 {
				wd = rec(p).then(local)
			}
			out[i], done[i] = wd, true
			return wd
		}
		for i := range doc.Nodes {
			rec(i)
		}
		return out
	}
	posedW := worlds(loc)
	restW := worlds(adj)
	skinM := make([]affine, len(rd.names))
	for ji, n := range rd.skin.Joints {
		skinM[ji] = posedW[n].then(restW[n].inverse())
	}
	st := &poseState{world: posedW}
	for _, p := range doc.Meshes[rd.meshIdx].Primitives {
		pos := make([]mesh.Vec3, len(p.Positions)/3)
		for v := range pos {
			pt := [3]float64{float64(p.Positions[3*v]), float64(p.Positions[3*v+1]), float64(p.Positions[3*v+2])}
			for ti, tg := range p.Targets {
				if ti < len(w) && w[ti] != 0 && len(tg.Positions) > 0 {
					for c := 0; c < 3; c++ {
						pt[c] += float64(w[ti] * float64(tg.Positions[3*v+c]))
					}
				}
			}
			var acc [3]float64
			for l := 0; l < 4 && len(p.Weights0) > 0; l++ {
				wt := float64(p.Weights0[4*v+l])
				if wt == 0 {
					continue
				}
				q := skinM[p.Joints0[4*v+l]].apply(pt)
				for c := 0; c < 3; c++ {
					acc[c] += float64(wt * q[c])
				}
			}
			pos[v] = acc
		}
		st.prims = append(st.prims, pos)
	}
	return st, nil
}

func quatAt(times, vals []float64, t float64) quat {
	n := len(times)
	get := func(k int) quat { return quat{vals[4*k], vals[4*k+1], vals[4*k+2], vals[4*k+3]} }
	switch {
	case n == 0:
		return quat{0, 0, 0, 1}
	case t <= times[0]:
		return get(0)
	case t >= times[n-1]:
		return get(n - 1)
	}
	k := sort.Search(n, func(i int) bool { return times[i] > t }) - 1
	return slerp(get(k), get(k+1), (t-times[k])/(times[k+1]-times[k]))
}

func indexOf(ss []string, s string) int {
	for i, x := range ss {
		if x == s {
			return i
		}
	}
	return -1
}

// ---- calls ------------------------------------------------------------------------

// rigArgs reads the named arguments of a pose call: `clip` and `time`, and
// every other name is a runtime parameter's value.
func (ev *evaluator) rigArgs(c *ast.Call, positional ...string) (pos []ast.Expr, clip string, t float64, set map[string]units.Value, err *evalErr) {
	set = map[string]units.Value{}
	pos = make([]ast.Expr, len(positional))
	n := 0
	for _, a := range c.Args {
		if a.Name == "" {
			if n >= len(positional) {
				return nil, "", 0, nil, refuse("%s takes at most %d positional argument(s)", c.Func, len(positional))
			}
			pos[n] = a.Value
			n++
			continue
		}
		switch a.Name {
		case "clip":
			s, ok := identifier(a.Value)
			if !ok {
				return nil, "", 0, nil, refuse("%s: clip is a name", c.Func)
			}
			clip = s
		case "time":
			v, e := ev.eval(a.Value)
			if e != nil {
				return nil, "", 0, nil, e
			}
			x, uerr := units.Coerce(v.u, units.Duration, ev.d)
			if uerr != nil {
				return nil, "", 0, nil, ev.uerr(uerr)
			}
			t = x.Num
		default:
			v, e := ev.eval(a.Value)
			if e != nil {
				return nil, "", 0, nil, e
			}
			set[a.Name] = v.u
		}
	}
	return pos, clip, t, set, nil
}

func (ev *evaluator) rig() (*rigDoc, *evalErr) {
	if ev.rd == nil {
		rd, err := newRigDoc(ev.sc)
		if err != nil {
			return nil, err
		}
		ev.rd = rd
	}
	return ev.rd, nil
}

// rigCall evaluates the rig functions; handled reports whether c is one.
func (ev *evaluator) rigCall(c *ast.Call) (v val, handled bool, err *evalErr) {
	switch c.Func {
	case "animation_count", "has_animation", "animations_present":
		doc := ev.sc.doc
		have := map[string]bool{}
		for _, a := range doc.Animations {
			have[a.Name] = true
		}
		switch c.Func {
		case "animation_count":
			ev.recordCount(exprString(c), len(doc.Animations), "animations")
			return uval(units.Num(float64(len(doc.Animations)))), true, nil
		case "has_animation":
			args, e := bind(c, "name")
			if e != nil {
				return val{}, true, e
			}
			n, ok := identifier(args[0])
			if !ok {
				return val{}, true, refuse("has_animation needs a name")
			}
			ev.recordBool(exprString(c), have[n])
			return uval(units.Switch(have[n])), true, nil
		default:
			got := 0
			var missing []string
			for _, a := range c.Args {
				n, ok := identifier(a.Value)
				if !ok || a.Name != "" {
					return val{}, true, refuse("animations_present takes names")
				}
				if have[n] {
					got++
				} else {
					missing = append(missing, n)
				}
			}
			ev.recordCount(exprString(c), got, "of "+fmt.Sprint(len(c.Args))+" present")
			if len(missing) > 0 {
				ev.record("missing animations", strings.Join(missing, ", "), nil)
			}
			return uval(units.Num(float64(got))), true, nil
		}
	case "joint_count", "morph_count":
		rd, e := ev.rig()
		if e != nil {
			return val{}, true, e
		}
		n := len(rd.names)
		noun := "joints"
		if c.Func == "morph_count" {
			n, noun = len(rd.targets), "morph targets"
		}
		ev.recordCount(exprString(c), n, noun)
		return uval(units.Num(float64(n))), true, nil
	case "runtime_points":
		rd, e := ev.rig()
		if e != nil {
			return val{}, true, e
		}
		n := 1
		for _, p := range rd.params {
			switch {
			case len(p.Values) > 0:
				n *= len(p.Values)
			case p.Type == "switch":
				n *= 2
			case len(p.Range) == 2 && p.Range[0] != p.Range[1]:
				n *= 2
			}
		}
		ev.recordCount(exprString(c), n, "points of the file's own runtime parameters")
		return uval(units.Num(float64(n))), true, nil
	case "posed_lowest", "posed_position", "mounted_clearance":
		rd, e := ev.rig()
		if e != nil {
			return val{}, true, e
		}
		var names []string
		if c.Func == "posed_position" {
			names = []string{"node"}
		}
		pos, clip, t, set, e := ev.rigArgs(c, names...)
		if e != nil {
			return val{}, true, e
		}
		st, e := rd.pose(clip, t, set)
		if e != nil {
			return val{}, true, e
		}
		switch c.Func {
		case "posed_lowest":
			up := rd.upIndex()
			lo := math.Inf(1)
			for _, p := range st.prims {
				for _, q := range p {
					lo = math.Min(lo, q[up])
				}
			}
			ev.recordLen(exprString(c), lo)
			return uval(units.Len(lo)), true, nil
		case "posed_position":
			if pos[0] == nil {
				return val{}, true, refuse("posed_position needs a node or socket name")
			}
			n, ok := identifier(pos[0])
			if !ok {
				return val{}, true, refuse("posed_position needs a delivered name")
			}
			ids := ev.sc.byName[n]
			if len(ids) == 0 {
				ids = ev.sc.byName["socket."+n]
			}
			if len(ids) != 1 {
				return val{}, true, refuse("%d nodes are named %q", len(ids), n)
			}
			p := st.world[ids[0]].t
			ev.record(exprString(c), fmtPoint(p), nil)
			return uval(vecValue(p)), true, nil
		default:
			prims := ev.sc.doc.Meshes[rd.meshIdx].Primitives
			if len(prims) < 2 {
				return val{}, true, unavailable("this output has no baked gear: its gear is its own file, which is not an input of this acceptance")
			}
			mk := func(pi int) []kgeom.Tri {
				var out []kgeom.Tri
				p := prims[pi]
				for i := 0; i+2 < len(p.Indices); i += 3 {
					out = append(out, kgeom.Tri{st.prims[pi][p.Indices[i]], st.prims[pi][p.Indices[i+1]], st.prims[pi][p.Indices[i+2]]})
				}
				return out
			}
			host := kgeom.NewBVH(mk(0))
			best := math.Inf(1)
			for pi := 1; pi < len(prims); pi++ {
				if r := kgeom.MinDistance(host, kgeom.NewBVH(mk(pi)), 1); r.Found && r.Distance < best {
					best = r.Distance
				}
			}
			if math.IsInf(best, 1) {
				best = 1 // no pair within one metre
			}
			ev.recordLen(exprString(c), best)
			return uval(units.Len(best)), true, nil
		}
	}
	return val{}, false, nil
}
