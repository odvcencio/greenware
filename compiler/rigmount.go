package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/kernel/rig"
	"m31labs.dev/greenware/resolve"
)

// Mounts (language reference 5.8). A host declares `mount name { asset socket
// mode set fit }` against a project asset it imports. The compiler builds the
// gear output the mount selects with the mount's set values, so the same code
// that writes the gear's own file gives the host the gear's geometry.
//
//   - runtime: the host file records the mount (socket node, plug frame, gear
//     output and file, set values) and nothing of the gear's geometry. The
//     gear stays its own file with its own budget.
//   - bake: the gear's default look (the nodes a viewer draws, no hidden
//     branch) is merged into the host's skinned mesh, rigid on the socket's
//     joint with weight 1. Its materials and images are the host's own.
//
// The gear's frame meets the socket's: gear_world = socket_world *
// inverse(plug). A plug has a position only, so the gear moves by the socket
// position minus the plug position and turns by the socket's rotation.

// mountGear is one mount of the output being built, with the gear built.
type mountGear struct {
	m    resolve.Mount
	mode string
	gear *resolve.GearImport
	// prod is the gear output built with the mount's set; nil when the mount
	// neither bakes nor checks fit.
	prod *OutputProduct
}

// gearFile returns the gear's file for a host level; a host level past the
// gear's last level uses the gear's last.
func (g *mountGear) gearFile(lvl int) *File {
	if g.prod == nil || len(g.prod.Files) == 0 {
		return nil
	}
	if lvl >= len(g.prod.Files) {
		lvl = len(g.prod.Files) - 1
	}
	return g.prod.Files[lvl]
}

// buildMounts builds the gear of every mount that needs it. out carries the
// output's `mounts` override.
func (e *elab) buildMounts(out resolve.Output) bool {
	for _, m := range e.r.Mounts {
		mode := m.Mode
		if out.Mounts != "" {
			mode = out.Mounts
		}
		var gear *resolve.GearImport
		for i := range e.r.Gears {
			if e.r.Gears[i].Qualifier == m.Gear {
				gear = &e.r.Gears[i]
			}
		}
		if gear == nil {
			e.report(diag.CodeUnknownName, m.Span, "the mount `%s` names `%s`, which is not an imported project asset", m.Name, m.Gear)
			return false
		}
		mg := &mountGear{m: m, mode: mode, gear: gear}
		if mode == "bake" || m.HasFit {
			if !e.rigged() {
				e.report(diag.CodeUnsupported, m.Span, "the mount `%s` of an asset without a skeleton is valid in the language but outside this compiler's subset: a mount needs a skinned host", m.Name)
				return false
			}
			gr, ds := resolve.EvaluateGear(gear, m.Output, m.Overrides)
			if gr == nil || diag.HasErrors(ds) {
				msg := "it does not evaluate"
				if len(ds) > 0 {
					msg = ds[0].Message
				}
				e.report(diag.CodeMount, m.Span, "the gear output %s of the mount `%s` does not evaluate: %s", m.Output, m.Name, msg)
				return false
			}
			op, gds, _ := buildOutput(gear.File, gr, m.Output)
			if op == nil || diag.HasErrors(gds) {
				msg := "it does not build"
				if len(gds) > 0 {
					msg = gds[0].Message
				}
				e.report(diag.CodeMount, m.Span, "the gear output %s of the mount `%s` does not build: %s", m.Output, m.Name, msg)
				return false
			}
			if ref := classBoundFiles(op, usesBranchClass(gr), len(atlasesUsed(gr)) > 0); ref != nil {
				e.report(diag.CodeLimit, m.Span, "the `%s` ticket (%s) cannot take the gear of the mount `%s`: %s", TicketMount, TicketMount, m.Name, ref.Bound)
				return false
			}
			mg.prod = op
		}
		e.mounts = append(e.mounts, mg)
	}
	return true
}

// baked lists the mounts that merge into the host mesh.
func (e *elab) baked() []*mountGear {
	var out []*mountGear
	for _, g := range e.mounts {
		if g.mode == "bake" {
			out = append(out, g)
		}
	}
	return out
}

// socketFrame is a host socket's frame in the model's bind pose: its position
// and rotation, and the joint it is rigid on.
func (e *elab) socketFrame(rl *rigLevel, m resolve.Mount) (joint int, pos mesh.Vec3, q rig.Quat, ok bool) {
	if m.SocketJoint == "" {
		e.report(diag.CodeUnsupported, m.Span, "the mount `%s` meets the socket `%s`, which is on a part, not a joint: a skinned host mounts at a joint socket", m.Name, m.Socket)
		return 0, pos, q, false
	}
	joint = rl.sk.Index(m.SocketJoint)
	if joint < 0 {
		e.report(diag.CodeMount, m.Span, "the socket `%s` is on `%s`, which is not a joint", m.Socket, m.SocketJoint)
		return 0, pos, q, false
	}
	pos, q = rl.sk.Joints[joint].Pos, rl.sk.Joints[joint].Rot
	for _, s := range rl.sd.sockets {
		if s.Name != m.Socket {
			continue
		}
		if s.HasAt {
			pos = mesh.Vec3(s.At)
			if s.Reflect != "" {
				pos = reflectVec(pos, s.Reflect)
			}
		}
		sq := rig.QEuler(s.Rot[0], s.Rot[1], s.Rot[2])
		if s.Reflect != "" {
			sq = reflectQuat(sq, s.Reflect)
		}
		q = q.Mul(sq.Normalize()).Normalize()
	}
	return joint, pos, q, true
}

// nodeWorld is a gear node's transform: scale, then rotation, then
// translation, composed down the node tree.
type nodeWorld struct {
	t mesh.Vec3
	q rig.Quat
	s mesh.Vec3
}

func (w nodeWorld) point(p mesh.Vec3) mesh.Vec3 {
	sp := mesh.Vec3{float64(p[0] * w.s[0]), float64(p[1] * w.s[1]), float64(p[2] * w.s[2])}
	return mesh.Add(w.q.Rotate(sp), w.t)
}

func (w nodeWorld) dir(n mesh.Vec3) mesh.Vec3 {
	// Scales of the gear's nodes are uniform or one; a rotation is enough.
	return w.q.Rotate(n)
}

func (w nodeWorld) child(n gltf.Node) nodeWorld {
	l := nodeWorld{q: rig.QIdentity, s: mesh.Vec3{1, 1, 1}}
	if n.Translation != nil {
		l.t = mesh.Vec3{float64(n.Translation[0]), float64(n.Translation[1]), float64(n.Translation[2])}
	}
	if n.Rotation != nil {
		l.q = rig.Quat{float64(n.Rotation[0]), float64(n.Rotation[1]), float64(n.Rotation[2]), float64(n.Rotation[3])}
	}
	if n.Scale != nil {
		l.s = mesh.Vec3{float64(n.Scale[0]), float64(n.Scale[1]), float64(n.Scale[2])}
	}
	out := nodeWorld{q: w.q.Mul(l.q).Normalize()}
	out.s = mesh.Vec3{float64(w.s[0] * l.s[0]), float64(w.s[1] * l.s[1]), float64(w.s[2] * l.s[2])}
	out.t = w.point(l.t)
	return out
}

// gearDrawn is one primitive the gear draws in its default look, with the
// transform of its node.
type gearDrawn struct {
	prim     gltf.Primitive
	world    nodeWorld
	nodeName string
}

// defaultLook walks the gear's default scene and returns the primitives a
// viewer draws: a node whose visibility extension says false hides its subtree.
func defaultLook(d *gltf.Document) []gearDrawn {
	var out []gearDrawn
	var walk func(i int, w nodeWorld)
	walk = func(i int, w nodeWorld) {
		n := d.Nodes[i]
		if raw, ok := n.Extensions["KHR_node_visibility"]; ok && bytes.Contains(raw, []byte(`"visible":false`)) {
			return
		}
		w = w.child(n)
		if n.Mesh != nil {
			for _, p := range d.Meshes[*n.Mesh].Primitives {
				out = append(out, gearDrawn{prim: p, world: w, nodeName: n.Name})
			}
		}
		for _, c := range n.Children {
			walk(c, w)
		}
	}
	sc := 0
	if d.Scene != nil {
		sc = *d.Scene
	}
	if sc < len(d.Scenes) {
		for _, r := range d.Scenes[sc].Nodes {
			walk(r, nodeWorld{q: rig.QIdentity, s: mesh.Vec3{1, 1, 1}})
		}
	}
	return out
}

// bakeStats is what a baked gear adds to the host's counts.
type bakeStats struct {
	tris, verts, draws, mats int
	texBytes                 int
	images                   []ImageInfo
}

// bakeGear merges the gear's default look into the host's skinned mesh.
func (e *elab) bakeGear(doc *gltf.Document, rl *rigLevel, rootIdx int, g *mountGear, lvl int) (bakeStats, bool) {
	var st bakeStats
	gf := g.gearFile(lvl)
	if gf == nil {
		return st, false
	}
	joint, spos, sq, ok := e.socketFrame(rl, g.m)
	if !ok {
		return st, false
	}
	plug := mesh.Vec3(g.m.PlugAt)
	// The frame change: v -> socket + R_socket (v - plug).
	frame := nodeWorld{q: sq, s: mesh.Vec3{1, 1, 1}}
	frame.t = mesh.Sub(spos, sq.Rotate(plug))

	host := &doc.Meshes[*doc.Nodes[rootIdx].Mesh]
	nt := len(host.Primitives[0].Targets)
	drawn := defaultLook(gf.Doc)
	// Group the primitives by the gear's material, in order of first use.
	var order []int
	groups := map[int][]gearDrawn{} // lookup only
	for _, d := range drawn {
		mi := -1
		if d.prim.Material != nil {
			mi = *d.prim.Material
		}
		if _, seen := groups[mi]; !seen {
			order = append(order, mi)
		}
		groups[mi] = append(groups[mi], d)
	}
	gd := gf.Doc
	texMap := map[int]int{} // gear texture -> host texture
	addTexture := func(gt int) int {
		if h, ok := texMap[gt]; ok {
			return h
		}
		t := gd.Textures[gt]
		tex := gltf.Texture{Name: t.Name}
		if t.Source != nil {
			img := gd.Images[*t.Source]
			ii := -1
			for k, x := range doc.Images {
				if bytes.Equal(x.Data, img.Data) && x.MimeType == img.MimeType {
					ii = k
				}
			}
			if ii < 0 {
				ii = len(doc.Images)
				doc.Images = append(doc.Images, img)
				for _, info := range gf.Images {
					if info.Name == img.Name {
						st.images = append(st.images, info)
						st.texBytes += info.Width * info.Height * 4
					}
				}
			}
			tex.Source = gltf.Int(ii)
		}
		if t.Sampler != nil {
			sm := gd.Samplers[*t.Sampler]
			si := -1
			for k, x := range doc.Samplers {
				if x == sm {
					si = k
				}
			}
			if si < 0 {
				si = len(doc.Samplers)
				doc.Samplers = append(doc.Samplers, sm)
			}
			tex.Sampler = gltf.Int(si)
		}
		h := len(doc.Textures)
		doc.Textures = append(doc.Textures, tex)
		texMap[gt] = h
		return h
	}
	for _, mi := range order {
		var prim gltf.Primitive
		prim.Targets = make([]gltf.MorphTarget, nt)
		for _, d := range groups[mi] {
			base := uint32(len(prim.Positions) / 3)
			p := d.prim
			// gear node frame, then the mount frame.
			w := nodeWorld{q: frame.q, s: frame.s}
			w.t = frame.t
			for v := 0; v < len(p.Positions)/3; v++ {
				pt := d.world.point(mesh.Vec3{float64(p.Positions[3*v]), float64(p.Positions[3*v+1]), float64(p.Positions[3*v+2])})
				pt = w.point(pt)
				f := pt.F32()
				prim.Positions = append(prim.Positions, f[0], f[1], f[2])
				if len(p.Normals) > 0 {
					n := mesh.Vec3{float64(p.Normals[3*v]), float64(p.Normals[3*v+1]), float64(p.Normals[3*v+2])}
					n = w.dir(d.world.dir(n))
					fn := n.F32()
					prim.Normals = append(prim.Normals, fn[0], fn[1], fn[2])
				}
				if len(p.UV0) > 0 {
					prim.UV0 = append(prim.UV0, p.UV0[2*v], p.UV0[2*v+1])
				}
				prim.Joints0 = append(prim.Joints0, uint16(joint), 0, 0, 0)
				prim.Weights0 = append(prim.Weights0, 1, 0, 0, 0)
			}
			for _, ix := range p.Indices {
				prim.Indices = append(prim.Indices, ix+base)
			}
		}
		if mi >= 0 {
			m := gd.Materials[mi]
			hm := m
			if m.BaseColorTexture != nil {
				hm.BaseColorTexture = gltf.Int(addTexture(*m.BaseColorTexture))
			}
			hm.Extras = remapSelenaExtras(m.Extras, addTexture)
			// The runtime binding of the baked colours is gone: they are fixed.
			prim.Material = gltf.Int(len(doc.Materials))
			doc.Materials = append(doc.Materials, hm)
			st.mats++
		}
		st.tris += len(prim.Indices) / 3
		st.verts += len(prim.Positions) / 3
		st.draws++
		host.Primitives = append(host.Primitives, prim)
	}
	return st, true
}

// remapSelenaExtras rewrites the texture indices a Selena descriptor records
// (the palette mask and its fallback) with the host's indices. Other bytes of
// the descriptor are kept as written.
func remapSelenaExtras(raw json.RawMessage, add func(int) int) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return raw
	}
	gw, _ := root["greenware"].(map[string]any)
	pal, _ := gw["palette"].(map[string]any)
	if pal == nil {
		return raw
	}
	for _, k := range []string{"mask_texture", "fallback_texture"} {
		if n, ok := pal[k].(json.Number); ok {
			var i int
			fmt.Sscan(string(n), &i)
			pal[k] = json.Number(fmt.Sprint(add(i)))
		}
	}
	b, err := json.Marshal(root)
	if err != nil {
		return raw
	}
	return b
}

// mountExtras writes the mounts of a level into the asset's Greenware data:
// where each gear meets the host, which gear output and values it has, and
// whether it is merged into this file.
func (e *elab) mountExtras(asset map[string]any, rl *rigLevel, stats map[string]bakeStats) {
	if len(e.mounts) == 0 {
		return
	}
	var out []any
	for _, g := range e.mounts {
		m := g.m
		ent := map[string]any{
			"name": m.Name, "mode": g.mode, "socket": m.Socket, "socket_node": "socket." + m.Socket,
			"kind": m.SocketKind, "joint": m.SocketJoint,
			"plug": map[string]any{"name": m.Plug, "at": vecJSON(mesh.Vec3(m.PlugAt))},
			"gear": map[string]any{"import": g.gear.Path, "output": m.Output, "file": m.OutputFile},
		}
		if g.prod != nil {
			ent["gear"].(map[string]any)["fir_sha256"] = g.prod.FirSHA256
		}
		var set []any
		for _, s := range m.Set {
			set = append(set, map[string]any{"name": s.Name, "value": s.Text})
		}
		sort.SliceStable(set, func(i, j int) bool {
			return set[i].(map[string]any)["name"].(string) < set[j].(map[string]any)["name"].(string)
		})
		if len(set) > 0 {
			ent["set"] = set
		}
		if m.HasFit {
			ent["fit"] = map[string]any{"clearance": f32(det.Round32(m.Clearance))}
		}
		if st, ok := stats[m.Name]; ok {
			ent["baked"] = map[string]any{"triangles": st.tris, "vertices": st.verts, "draws": st.draws, "materials": st.mats}
		}
		out = append(out, ent)
	}
	asset["mounts"] = out
}

// mountIdent is what a mount contributes to the meaning hash of the host: the
// mount's own fields, its mode in this output and, for a gear built into the
// host, the meaning hash of the gear output.
type mountIdent struct {
	Name, Mode, Socket, Plug, Output string
	Set                              []string
	Clearance                        *float64 `json:",omitempty"`
	GearFir                          string   `json:",omitempty"`
}

func (e *elab) mountIdentity() []mountIdent {
	var out []mountIdent
	for _, g := range e.mounts {
		m := g.m
		id := mountIdent{Name: m.Name, Mode: g.mode, Socket: m.Socket, Plug: m.Plug, Output: m.Output}
		for _, s := range m.Set {
			id.Set = append(id.Set, s.Name+"="+s.Text)
		}
		sort.Strings(id.Set)
		if m.HasFit {
			c := m.Clearance
			id.Clearance = &c
		}
		if g.prod != nil {
			id.GearFir = g.prod.FirSHA256
		}
		out = append(out, id)
	}
	return out
}

// prodFile returns the file of a gear product for a host level; a level past
// the gear's last uses the gear's last.
func prodFile(p *OutputProduct, lvl int) *File {
	if lvl >= len(p.Files) {
		lvl = len(p.Files) - 1
	}
	return p.Files[lvl]
}

// gearProduct returns the gear of a mount built at a gear output. The mount's
// own output is built with the mount; another output (a runtime point that
// selects the aero shell) is built on first use with the mount's runtime
// values, because a bake value is what the output itself fixes.
func (e *elab) gearProduct(g *mountGear, output string) (*OutputProduct, bool) {
	if output == g.m.Output && g.prod != nil {
		return g.prod, true
	}
	key := g.m.Name + "/" + output
	if p, ok := e.gearProds[key]; ok {
		return p, true
	}
	var extra []resolve.Override
	for _, o := range g.m.Overrides {
		if p := paramNamed(g.gear.Resolved.Params, o.Name); p != nil && p.Runtime {
			extra = append(extra, o)
		}
	}
	gr, ds := resolve.EvaluateGear(g.gear, output, extra)
	if gr == nil || diag.HasErrors(ds) {
		e.report(diag.CodeMount, g.m.Span, "the gear output %s of the mount `%s` does not evaluate", output, g.m.Name)
		return nil, false
	}
	op, gds, _ := buildOutput(g.gear.File, gr, output)
	if op == nil || diag.HasErrors(gds) {
		msg := "it does not build"
		if len(gds) > 0 {
			msg = gds[0].Message
		}
		e.report(diag.CodeMount, g.m.Span, "the gear output %s of the mount `%s` does not build: %s", output, g.m.Name, msg)
		return nil, false
	}
	if e.gearProds == nil {
		e.gearProds = map[string]*OutputProduct{}
	}
	e.gearProds[key] = op
	return op, true
}

func paramNamed(ps []resolve.Param, name string) *resolve.Param {
	for i := range ps {
		if ps[i].Name == name {
			return &ps[i]
		}
	}
	return nil
}
