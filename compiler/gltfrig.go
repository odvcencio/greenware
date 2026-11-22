package compiler

import (
	"encoding/binary"
	"math"
	"sort"

	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/internal/socketmeta"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/kernel/rig"
	"m31labs.dev/greenware/resolve"
)

// rigVertexKey identifies a written vertex of a skinned mesh: its attributes,
// its joints and weights, and the deltas of every target.
type rigVertexKey struct {
	p, n [3]float32
	uv   [2]float32
	j    [4]uint16
	w    [4]float32
	t    string // the target deltas, as bytes
}

// primitiveRigCounted merges the pieces of one material into one skinned
// primitive. Vertices that are equal in every attribute, joint, weight and
// target delta inside one piece share an index. It also returns, for each
// piece, the number of indexed vertices it wrote.
func primitiveRigCounted(ps []piece) (gltf.Primitive, []int) {
	var prim gltf.Primitive
	counts := make([]int, 0, len(ps))
	nt := 0
	if len(ps) > 0 {
		nt = len(ps[0].rig.pos)
	}
	prim.Targets = make([]gltf.MorphTarget, nt)
	for _, p := range ps {
		first := len(prim.Positions) / 3
		seen := map[rigVertexKey]uint32{} // lookup only
		remap := make([]uint32, len(p.m.Corners))
		for i, c := range p.m.Corners {
			k := rigVertexKey{p: p.m.Positions[c.Pos].F32(), n: c.Normal.F32(), uv: c.UV.F32()}
			for l, in := range p.rig.infl[c.Pos] {
				if l == 4 {
					break
				}
				k.j[l], k.w[l] = uint16(in.Joint), det.Round32(in.W)
			}
			var tb []byte
			var dp, dn [][3]float32
			for t := 0; t < nt; t++ {
				a, b := p.rig.pos[t][c.Pos].F32(), p.rig.nrm[t][i].F32()
				dp, dn = append(dp, a), append(dn, b)
				for _, f := range append(a[:], b[:]...) {
					tb = binary.LittleEndian.AppendUint32(tb, math.Float32bits(f))
				}
			}
			k.t = string(tb)
			idx, ok := seen[k]
			if !ok {
				idx = uint32(len(prim.Positions) / 3)
				seen[k] = idx
				prim.Positions = append(prim.Positions, k.p[0], k.p[1], k.p[2])
				prim.Normals = append(prim.Normals, k.n[0], k.n[1], k.n[2])
				prim.UV0 = append(prim.UV0, k.uv[0], k.uv[1])
				prim.Joints0 = append(prim.Joints0, k.j[:]...)
				prim.Weights0 = append(prim.Weights0, k.w[:]...)
				for t := 0; t < nt; t++ {
					prim.Targets[t].Positions = append(prim.Targets[t].Positions, dp[t][0], dp[t][1], dp[t][2])
					prim.Targets[t].Normals = append(prim.Targets[t].Normals, dn[t][0], dn[t][1], dn[t][2])
				}
			}
			remap[i] = idx
		}
		for _, t := range p.m.Tris {
			prim.Indices = append(prim.Indices, remap[t[0]], remap[t[1]], remap[t[2]])
		}
		counts = append(counts, len(prim.Positions)/3-first)
	}
	return prim, counts
}

// rigNodes is what attachRig made.
type rigNodes struct {
	top  int // the scene's root node: the outermost wrapper, or the root
	skin int
}

// clipByName returns the resolved clip with the name.
func clipByName(rg *resolve.Rig, name string) *resolve.RigClip {
	for _, c := range rg.Clips {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func quatNonIdentity(q rig.Quat) bool { return q != rig.QIdentity }

// attachRig adds the joint nodes, the skin, the wrapper nodes and the
// animations of a rigged level to the document. The skinned mesh is the root
// node, already written.
func (e *elab) attachRig(doc *gltf.Document, rl *rigLevel, rootIdx int) (rigNodes, []*clipData, bool) {
	rg := e.r.Rig
	sk := rl.sk
	// Clips first: the idle clip's first key is the display pose.
	var cds []*clipData
	var idle *clipData
	for _, c := range rg.Clips {
		if len(c.Samples) == 0 {
			continue
		}
		cd, ok := e.kernelClip(c, rl)
		if !ok {
			return rigNodes{}, nil, false
		}
		e.checkLimits(c, rl)
		cds = append(cds, cd)
		if c.Name == rg.Idle {
			idle = cd
		}
	}
	var display []rig.TRS
	if idle != nil {
		display = rl.inst.Locals(idle.clip, 0)
	} else {
		display = rl.inst.Adjusted()
	}
	// Joint nodes, depth first from the root, children in joint order.
	jointNode := make([]int, len(sk.Joints))
	var add func(j int) int
	add = func(j int) int {
		n := gltf.Node{Name: sk.Joints[j].Name}
		if t := display[j].T; t != (mesh.Vec3{}) {
			f := t.F32()
			n.Translation = &f
		}
		if q := display[j].R; quatNonIdentity(q) {
			f := [4]float32{det.Round32(q[0]), det.Round32(q[1]), det.Round32(q[2]), det.Round32(q[3])}
			n.Rotation = &f
		}
		idx := len(doc.Nodes)
		jointNode[j] = idx
		doc.Nodes = append(doc.Nodes, n)
		var kids []int
		for _, c := range sk.Children(j) {
			kids = append(kids, add(c))
		}
		for _, s := range rl.sd.sockets {
			if sk.Index(s.Joint) != j {
				continue
			}
			sn := gltf.Node{Name: socketmeta.Prefix + s.Name}
			at := mesh.Vec3{}
			if s.HasAt {
				at = mesh.Vec3(s.At)
				if s.Reflect != "" {
					at = reflectVec(at, s.Reflect)
				}
				at = sk.LocalPoint(j, at)
			}
			if at != (mesh.Vec3{}) {
				f := at.F32()
				sn.Translation = &f
			}
			q := rig.QEuler(s.Rot[0], s.Rot[1], s.Rot[2])
			if s.Reflect != "" {
				q = reflectQuat(q, s.Reflect)
			}
			if q = q.Normalize(); quatNonIdentity(q) {
				f := [4]float32{det.Round32(q[0]), det.Round32(q[1]), det.Round32(q[2]), det.Round32(q[3])}
				sn.Rotation = &f
			}
			sn.Extras = socketmeta.Encode(socketmeta.Value{Name: s.Name, Kind: s.Kind, Joint: s.Joint})
			doc.Nodes = append(doc.Nodes, sn)
			kids = append(kids, len(doc.Nodes)-1)
		}
		doc.Nodes[idx].Children = kids
		return idx
	}
	rootJoint := add(sk.Root())
	doc.Nodes[rootIdx].Children = append(doc.Nodes[rootIdx].Children, rootJoint)
	// The skin: joints in joint order, inverse binds of the adjusted rest.
	ibm := rl.inst.InverseBinds()
	skin := gltf.Skin{Name: e.r.Rig.Skeleton.Name, Joints: jointNode, Skeleton: gltf.Int(rootJoint)}
	for _, r := range ibm {
		m := r.Mat4()
		var f [16]float32
		for i, v := range m {
			f[i] = det.Round32(v)
		}
		skin.InverseBindMatrices = append(skin.InverseBindMatrices, f)
	}
	doc.Skins = append(doc.Skins, skin)
	doc.Nodes[rootIdx].Skin = gltf.Int(0)
	// Wrapper nodes, outermost first.
	wrapper := map[string]int{} // lookup only
	top := rootIdx
	for i := len(rg.Wrappers) - 1; i >= 0; i-- {
		doc.Nodes = append(doc.Nodes, gltf.Node{Name: rg.Wrappers[i], Children: []int{top}})
		top = len(doc.Nodes) - 1
		wrapper[rg.Wrappers[i]] = top
	}
	// Animations, by exported name.
	nn := animNodes{joint: jointNode, wrapper: wrapper}
	byName := map[string]*clipData{} // lookup only
	for _, cd := range cds {
		byName[cd.src.Name] = cd
	}
	var anims []gltf.Animation
	for _, c := range rg.Clips {
		var an gltf.Animation
		var ok bool
		if len(c.Samples) > 0 {
			an, ok = e.skeletal(byName[c.Name], rl, nn, idle)
		} else {
			an, ok = e.keyed(c, nn)
		}
		if !ok {
			return rigNodes{}, nil, false
		}
		anims = append(anims, an)
	}
	sort.SliceStable(anims, func(i, j int) bool { return anims[i].Name < anims[j].Name })
	doc.Animations = anims
	return rigNodes{top: top, skin: 0}, cds, true
}

// rigExtras adds the rig's Greenware data: the joints with their ranges, the
// runtime bindings of the morph weights, the joint offsets and the root
// scale, and the original rest and keys a reader rebuilds from.
func (e *elab) rigExtras(asset map[string]any, rl *rigLevel, cds []*clipData, bindings *[]binding) {
	rg := e.r.Rig
	sk := rl.sk
	rangeJSON := func(r rig.Ranges) map[string]any {
		out := map[string]any{}
		put := func(name string, x rig.Range) {
			if x.Set {
				out[name] = []f32{f32(det.Round32(x.Lo)), f32(det.Round32(x.Hi))}
			}
		}
		put("twist", r.Twist)
		put("swing_x", r.Swing[0])
		put("swing_y", r.Swing[1])
		put("swing_z", r.Swing[2])
		return out
	}
	var joints []any
	for i, j := range sk.Joints {
		ent := map[string]any{"name": j.Name, "parent": j.Parent, "twist_axis": []string{"x", "y", "z"}[sk.TwistAxisOf(i)]}
		if rl.sd.band[i].AnySet {
			ent["band"] = rangeJSON(rl.sd.band[i])
		}
		if rl.sd.limit[i].AnySet {
			ent["limit"] = rangeJSON(rl.sd.limit[i])
		}
		joints = append(joints, ent)
	}
	rigData := map[string]any{"format": "greenware.rig/1", "skeleton": rg.Skeleton.Name, "joints": joints, "idle": rg.Idle}
	if len(rg.Wrappers) > 0 {
		rigData["wrappers"] = rg.Wrappers
	}
	asset["rig"] = rigData

	// Runtime bindings.
	for _, m := range rl.morphs {
		if m.weight.Param == "" {
			continue
		}
		ent := map[string]any{"kind": "morph", "morph": m.name}
		if len(m.weight.Breaks) > 0 {
			var br []any
			for _, b := range m.weight.Breaks {
				br = append(br, []f32{f32(det.Round32(b.X)), f32(det.Round32(b.Y))})
			}
			ent["map"] = map[string]any{"table": "piecewise_linear", "points": br}
		} else {
			var mem []any
			for _, x := range m.weight.Members {
				mem = append(mem, map[string]any{"value": x.Name, "weight": f32(det.Round32(x.W))})
			}
			ent["map"] = map[string]any{"table": "lookup", "entries": mem}
		}
		*bindings = append(*bindings, binding{param: m.weight.Param, kind: "morph", node: m.name, extra: ent})
	}
	for _, o := range rl.offsets {
		name := rl.morphs[o.Morph].name
		param := rl.morphs[o.Morph].weight.Param
		if param == "" {
			continue
		}
		ent := map[string]any{"kind": "joint_offset", "morph": name, "joint": sk.Joints[o.Joint].Name, "delta": vecJSON(o.Delta)}
		*bindings = append(*bindings, binding{param: param, kind: "joint_offset", node: name + "/" + sk.Joints[o.Joint].Name, extra: ent})
	}
	if rl.inst.HasRootScale {
		ent := map[string]any{"kind": "root_scale", "joint": sk.Joints[sk.Root()].Name, "axis": []string{"x", "y", "z"}[rl.inst.Up]}
		*bindings = append(*bindings, binding{param: rg.Skeleton.RootScale, kind: "root_scale", node: sk.Joints[sk.Root()].Name, extra: ent})
	}
	// A file with only morph weights bound to parameters still needs the base:
	// a pose at another runtime point (the acceptance measures) is built from it.
	if len(*bindings) == 0 {
		return
	}
	// rig_base: the original local rest, the original translation keys, which
	// channels were authored, and the idle clip.
	var rest []any
	for i, r := range rl.rest {
		ent := map[string]any{"joint": sk.Joints[i].Name, "translation": vecJSON(r.T)}
		if quatNonIdentity(r.R) {
			ent["rotation"] = []f32{f32(det.Round32(r.R[0])), f32(det.Round32(r.R[1])), f32(det.Round32(r.R[2])), f32(det.Round32(r.R[3]))}
		}
		rest = append(rest, ent)
	}
	var keys []any
	for _, cd := range cds {
		var chans []any
		for _, ch := range cd.clip.Channels {
			ent := map[string]any{"joint": sk.Joints[ch.Target].Name}
			if ch.Path == rig.Translation {
				ent["property"] = "translation"
				var ts []f32
				var vs []f32
				for _, t := range ch.Times {
					ts = append(ts, f32(det.Round32(t)))
				}
				for _, v := range ch.Values {
					vs = append(vs, f32(det.Round32(v)))
				}
				ent["times"], ent["values"] = ts, vs
			} else {
				ent["property"] = "rotation"
			}
			chans = append(chans, ent)
		}
		keys = append(keys, map[string]any{"clip": cd.src.Export, "channels": chans})
	}
	asset["rig_base"] = map[string]any{
		"format": "greenware.rig_base/1", "rest": rest, "clips": keys,
		"idle": map[string]any{"clip": rg.Idle, "time": f32(0)},
	}
}
