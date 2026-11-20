package compiler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/resolve"
)

// f32 is a float written the way a float32 reads back: the shortest digits
// that round-trip at 32 bits, never negative zero.
type f32 float32

func (f f32) MarshalJSON() ([]byte, error) {
	v := float32(f)
	if v == 0 {
		v = 0
	}
	return strconv.AppendFloat(nil, float64(v), 'g', -1, 32), nil
}

func vecJSON(v mesh.Vec3) []f32 {
	return []f32{f32(det.Round32(v[0])), f32(det.Round32(v[1])), f32(det.Round32(v[2]))}
}

// collider is one collision shape in model space.
type collider struct {
	name  string
	shape string
	data  map[string]any
}

// level is the result of building one level of detail.
type level struct {
	n     int
	doc   *gltf.Document
	tris  int // triangles drawn in the default look
	draws int // draws in the default look
	verts int // written vertices, every branch
	mats  int
	nodes int
	// The worst look is the largest count over every reachable assignment of
	// the runtime parameters; stored counts every branch.
	trisWorst  int
	drawsWorst int
	trisStored int
	alternates int // roots of the alternates scene
	// texBytes is the decoded RGBA size of the distinct images the file
	// carries; images describes them.
	texBytes int
	images   []ImageInfo
	// The rig: joints, most influences on one vertex, morph targets,
	// animations, animation keys and bound positions. Zero without a rig.
	joints, influences, morphs, animations, keys, rigVerts int
	audit                                                  *AuditReport
}

// vertexKey identifies a written vertex.
type vertexKey struct {
	p  [3]float32
	n  [3]float32
	uv [2]float32
}

// primitiveOf merges the pieces of one material into one primitive. Vertices
// that are equal in position, normal and uv inside one piece share an index.
func primitiveOf(ps []piece) gltf.Primitive {
	var prim gltf.Primitive
	for _, p := range ps {
		seen := map[vertexKey]uint32{} // lookup only
		remap := make([]uint32, len(p.m.Corners))
		for i, c := range p.m.Corners {
			k := vertexKey{
				p:  p.m.Positions[c.Pos].F32(),
				n:  c.Normal.F32(),
				uv: c.UV.F32(),
			}
			idx, ok := seen[k]
			if !ok {
				idx = uint32(len(prim.Positions) / 3)
				seen[k] = idx
				prim.Positions = append(prim.Positions, k.p[0], k.p[1], k.p[2])
				prim.Normals = append(prim.Normals, k.n[0], k.n[1], k.n[2])
				prim.UV0 = append(prim.UV0, k.uv[0], k.uv[1])
			}
			remap[i] = idx
		}
		for _, t := range p.m.Tris {
			prim.Indices = append(prim.Indices, remap[t[0]], remap[t[1]], remap[t[2]])
		}
	}
	return prim
}

// groupPieces splits a node's pieces by material, in the order the materials
// first appear.
func groupPieces(ps []piece) (order []string, groups map[string][]piece) {
	groups = map[string][]piece{} // lookup only
	for _, p := range ps {
		if _, ok := groups[p.mat]; !ok {
			order = append(order, p.mat)
		}
		groups[p.mat] = append(groups[p.mat], p)
	}
	return order, groups
}

func piecesTris(ps []piece) int {
	n := 0
	for _, p := range ps {
		n += p.m.TriangleCount()
	}
	return n
}

// condNames lists the parameters the node tree's conditions test.
func condNames(n *snode, set map[string]bool) {
	if n.cond != nil {
		set[n.cond.Param] = true
	}
	for _, k := range n.kids {
		condNames(k, set)
	}
}

// look counts the triangles and draws the tree shows under an assignment of
// the runtime parameters. A node whose condition fails hides its whole subtree.
func look(n *snode, assign map[string]string) (tris, draws int) {
	if n.cond != nil && !n.cond.Holds(assign) {
		return 0, 0
	}
	order, groups := groupPieces(n.pieces)
	for _, mat := range order {
		tris += piecesTris(groups[mat])
		draws++
	}
	for _, k := range n.kids {
		if len(k.pieces) == 0 && len(k.kids) == 0 && len(k.sockets) == 0 {
			continue
		}
		t, d := look(k, assign)
		tris, draws = tris+t, draws+d
	}
	return tris, draws
}

// maxBranches is the most nodes one level may let a runtime `when` control.
const maxBranches = 16

// assemble builds the glTF document of one level from its node tree.
func (e *elab) assemble(root *snode, cols []collider, lvl int, fir string) (*level, bool) {
	// A skinned asset gets its weights, targets and joints first: the skin
	// steps move the geometry the pieces hold.
	var rl *rigLevel
	if e.rigged() {
		var ok bool
		if rl, ok = e.buildRigLevel(root); !ok {
			return nil, false
		}
		root.pieces = rl.pieces
	}
	// The materials this level draws, by name.
	var names []string
	seen := map[string]bool{} // lookup only
	var collectMats func(n *snode)
	collectMats = func(n *snode) {
		for _, p := range n.pieces {
			if p.mat != "" && !seen[p.mat] {
				seen[p.mat] = true
				names = append(names, p.mat)
			}
		}
		for _, k := range n.kids {
			collectMats(k)
		}
	}
	collectMats(root)
	sort.Strings(names) // materials are keyed by name: their order is not meaning
	doc := &gltf.Document{Asset: gltf.Asset{Generator: Generator}}
	if v := metaString(e.r, "author"); v != "" {
		doc.Asset.Copyright = v
	}
	matIndex := map[string]int{} // lookup only
	var bindings []binding
	tt := newTexTables(doc)
	for i, nm := range names {
		m, ok := e.material(nm)
		if !ok {
			return nil, false
		}
		matIndex[nm] = i
		// The base texture first, then the palette mask, so the tables fill
		// in the order of the sorted materials.
		baseTex, maskTex := -1, -1
		if m.look != nil && m.look.base != nil {
			baseTex = tt.use(m.look.base, samplerOf(m.filter, m.wrap))
			m.m.BaseColorTexture = gltf.Int(baseTex)
		}
		if m.look != nil && m.look.mask != nil {
			maskTex = tt.use(m.look.mask, maskSampler(m.wrap))
		}
		// A Selena material with a palette mask records its descriptor for a
		// palette-aware renderer. One without a palette (the goal post's) is
		// written as its fallback alone, as before.
		if m.selena != nil && m.look != nil && m.look.maskAt != nil {
			m.m.Extras = e.selenaExtras(m, baseTex, maskTex)
		}
		doc.Materials = append(doc.Materials, m.m)
		bindings = append(bindings, m.bindings...)
	}

	defaults := resolve.Defaults(e.r.Params)
	lv := &level{n: lvl}
	var alts []int // roots of the alternates scene
	var addNode func(n *snode, parent string, hiddenAbove bool) (idx int, alternate bool)
	addNode = func(n *snode, parent string, hiddenAbove bool) (int, bool) {
		idx := len(doc.Nodes)
		node := gltf.Node{Name: n.name}
		if n.t != (mesh.Vec3{}) {
			t := n.t.F32()
			node.Translation = &t
		}
		// A node a runtime `when` controls carries its own visibility state, and
		// the outermost hidden one is a root of the alternates scene: a stock
		// viewer draws the default scene and never sees it.
		alternate := false
		if n.cond != nil {
			visible := n.cond.Holds(defaults)
			node.Extensions = map[string]json.RawMessage{"KHR_node_visibility": json.RawMessage(fmt.Sprintf(`{"visible":%t}`, visible))}
			scene := "default"
			if !visible || hiddenAbove {
				scene = "alternates"
			}
			alternate = !visible && !hiddenAbove
			bindings = append(bindings, binding{kind: "visibility", param: n.cond.Param, node: n.name, parent: parent,
				translation: n.t, scene: scene, visible: visible, cond: *n.cond})
		}
		hidden := hiddenAbove || alternate
		doc.Nodes = append(doc.Nodes, node)
		if len(n.pieces) > 0 {
			// One primitive per material, in the order the materials first appear.
			order, groups := groupPieces(n.pieces)
			m := gltf.Mesh{Name: n.name}
			skinned := rl != nil && n == root
			if skinned {
				if v, ok := fieldOf(e.r.Asset, "mesh_name"); ok && v.Valid() && v.Str != "" {
					m.Name = v.Str
				}
				for _, mo := range rl.morphs {
					m.TargetNames = append(m.TargetNames, mo.name)
				}
				for _, w := range rl.weights {
					m.Weights = append(m.Weights, det.Round32(w))
				}
			}
			for _, mat := range order {
				var prim gltf.Primitive
				if skinned {
					prim = primitiveRig(groups[mat])
				} else {
					prim = primitiveOf(groups[mat])
				}
				if mat != "" {
					prim.Material = gltf.Int(matIndex[mat])
				}
				lv.trisStored += len(prim.Indices) / 3
				lv.verts += len(prim.Positions) / 3
				m.Primitives = append(m.Primitives, prim)
			}
			doc.Meshes = append(doc.Meshes, m)
			doc.Nodes[idx].Mesh = gltf.Int(len(doc.Meshes) - 1)
		}
		var kids []int
		for _, k := range n.kids {
			if len(k.pieces) == 0 && len(k.kids) == 0 && len(k.sockets) == 0 {
				continue // a part that the level emptied adds no node
			}
			ki, alt := addNode(k, n.name, hidden)
			if alt {
				alts = append(alts, ki)
				continue
			}
			kids = append(kids, ki)
		}
		for _, s := range n.sockets {
			sn := gltf.Node{Name: "socket." + s.name}
			if s.pos != (mesh.Vec3{}) {
				t := s.pos.F32()
				sn.Translation = &t
			}
			sn.Extras = mustJSON(map[string]any{"greenware": map[string]any{
				"format": "greenware.socket/1", "name": s.name, "kind": s.kind,
			}})
			doc.Nodes = append(doc.Nodes, sn)
			kids = append(kids, len(doc.Nodes)-1)
		}
		doc.Nodes[idx].Children = kids
		return idx, alternate
	}
	rootIdx, _ := addNode(root, "", false)
	top := rootIdx
	// A baked mount merges its gear into the host's skinned mesh.
	bakes := map[string]bakeStats{}
	var bakedAdd bakeStats
	if rl != nil {
		for _, g := range e.baked() {
			st, ok := e.bakeGear(doc, rl, rootIdx, g, lvl)
			if !ok {
				return nil, false
			}
			bakes[g.m.Name] = st
			bakedAdd.tris += st.tris
			bakedAdd.verts += st.verts
			bakedAdd.draws += st.draws
			bakedAdd.mats += st.mats
			bakedAdd.texBytes += st.texBytes
			bakedAdd.images = append(bakedAdd.images, st.images...)
		}
	}
	lv.trisStored += bakedAdd.tris
	lv.verts += bakedAdd.verts
	var cds []*clipData
	if rl != nil {
		rn, c, ok := e.attachRig(doc, rl, rootIdx)
		if !ok {
			return nil, false
		}
		top, cds = rn.top, c
		lv.joints, lv.influences, lv.morphs = len(rl.sk.Joints), rl.maxInfl, len(rl.morphs)
		for _, an := range doc.Animations {
			for _, sm := range an.Samplers {
				lv.keys += len(sm.Input)
			}
		}
		lv.animations = len(doc.Animations)
		lv.rigVerts = rl.verts
		rep, ok := e.audit(rl, cds, lvl)
		if !ok {
			return nil, false
		}
		lv.audit = rep
	}
	// Colliders hang off the root, after its sockets.
	for _, c := range cols {
		data := map[string]any{"format": "greenware.collider/1", "name": c.name, "shape": c.shape}
		for k, v := range c.data {
			data[k] = v
		}
		doc.Nodes = append(doc.Nodes, gltf.Node{Name: "collider." + c.name, Extras: mustJSON(map[string]any{"greenware": data})})
		doc.Nodes[rootIdx].Children = append(doc.Nodes[rootIdx].Children, len(doc.Nodes)-1)
	}
	doc.Scenes = []gltf.Scene{{Nodes: []int{top}}}
	if len(alts) > 0 {
		doc.Scenes = append(doc.Scenes, gltf.Scene{Name: "alternates", Nodes: alts})
	}
	doc.Scene = gltf.Int(0)
	lv.alternates = len(alts)

	// What the file stores and what each look draws.
	lv.tris, lv.draws = look(root, defaults)
	lv.tris, lv.draws = lv.tris+bakedAdd.tris, lv.draws+bakedAdd.draws
	lv.trisWorst, lv.drawsWorst = lv.tris, lv.draws
	set := map[string]bool{}
	condNames(root, set)
	if len(set) > 0 {
		var params []string
		for p := range set {
			params = append(params, p)
		}
		all, ok := resolve.Assignments(e.r.Params, params)
		if !ok {
			e.report(diag.CodeLimit, e.spanOf(""), "the runtime conditions have more than %d assignments to count the worst look over", maxAssignments)
			return nil, false
		}
		for _, a := range all {
			merged := resolve.Defaults(e.r.Params)
			for k, v := range a {
				merged[k] = v
			}
			t, d := look(root, merged)
			lv.trisWorst, lv.drawsWorst = max(lv.trisWorst, t+bakedAdd.tris), max(lv.drawsWorst, d+bakedAdd.draws)
		}
		if n := len(bindingsOfKind(bindings, "visibility")); n > maxBranches {
			e.report(diag.CodeLimit, e.spanOf(""), "a level has %d selectable branches; the class allows %d", n, maxBranches)
			return nil, false
		}
	}

	for _, p := range e.r.Plugs {
		at := vec3(p, "at")
		pn := gltf.Node{Name: "plug." + p.Name}
		if rel := mesh.Sub(at, root.t); rel != (mesh.Vec3{}) {
			t := rel.F32()
			pn.Translation = &t
		}
		pn.Extras = mustJSON(map[string]any{"greenware": map[string]any{
			"format": "greenware.plug/1", "name": p.Name, "kind": label(p, "kind"),
		}})
		doc.Nodes = append(doc.Nodes, pn)
		doc.Nodes[rootIdx].Children = append(doc.Nodes[rootIdx].Children, len(doc.Nodes)-1)
	}
	asset := map[string]any{"format": "greenware.asset/1", "fir_sha256": fir, "lod": lvl, "lod_error": e.lodErr}
	if len(e.r.Plugs) > 0 {
		var plugs []any
		for _, p := range e.r.Plugs {
			plugs = append(plugs, map[string]any{"name": p.Name, "kind": label(p, "kind"), "at": vecJSON(vec3(p, "at"))})
		}
		asset["plugs"] = plugs
	}
	if rl != nil {
		e.rigExtras(asset, rl, cds, &bindings)
	}
	if rl != nil {
		e.mountExtras(asset, rl, bakes)
	}
	if rt := e.runtimeTable(bindings); rt != nil {
		asset["runtime"] = rt
	}
	doc.Extras = mustJSON(map[string]any{"greenware": asset})
	// Morph deltas are stored sparse when that is smaller (glTF sparse
	// accessors); the decoded values are the same. Only the bytes change.
	doc.AutoSparse()
	lv.doc = doc
	lv.mats = len(names) + bakedAdd.mats
	lv.nodes = len(doc.Nodes)
	lv.texBytes, lv.images = tt.decoded+bakedAdd.texBytes, append(append([]ImageInfo(nil), tt.infos...), bakedAdd.images...)
	return lv, true
}

// maxAssignments mirrors the resolver's bound on listed assignments.
const maxAssignments = 4096

func bindingsOfKind(bs []binding, kind string) []binding {
	var out []binding
	for _, b := range bs {
		if b.kind == kind {
			out = append(out, b)
		}
	}
	return out
}

// runtimeTable writes greenware.runtime/1: each runtime parameter that has a
// binding on a material in this file. It is nil when no binding remains.
func (e *elab) runtimeTable(bs []binding) map[string]any {
	if len(bs) == 0 {
		return nil
	}
	var names []string
	by := map[string][]binding{} // lookup only
	for _, b := range bs {
		if _, ok := by[b.param]; !ok {
			names = append(names, b.param)
		}
		by[b.param] = append(by[b.param], b)
	}
	sort.Strings(names)
	var params []any
	for _, nm := range names {
		var p resolve.Param
		for _, q := range e.r.Params {
			if q.Name == nm {
				p = q
			}
		}
		ent := map[string]any{"name": nm, "type": p.Type, "default": paramDefault(p)}
		if p.Lo != nil && p.Hi != nil && p.Value.IsNumber() {
			ent["range"] = []f32{f32(det.Round32(p.Lo.Num)), f32(det.Round32(p.Hi.Num))}
		}
		if len(p.Members) > 0 {
			ent["values"] = p.Members
		}
		bl := by[nm]
		sort.SliceStable(bl, func(i, j int) bool {
			if bl[i].kind != bl[j].kind {
				return bl[i].kind < bl[j].kind
			}
			if bl[i].kind == "visibility" || bl[i].extra != nil {
				return bl[i].node < bl[j].node
			}
			if bl[i].material != bl[j].material {
				return bl[i].material < bl[j].material
			}
			return bl[i].field < bl[j].field
		})
		var bj []any
		for _, b := range bl {
			if b.extra != nil {
				bj = append(bj, b.extra)
				continue
			}
			if b.kind == "visibility" {
				test := map[string]any{"op": b.cond.Op}
				if b.cond.Value != "" {
					test["value"] = b.cond.Value
				}
				bj = append(bj, map[string]any{
					"kind": "visibility", "node": b.node, "parent": b.parent,
					"translation": vecJSON(b.translation), "scene": b.scene, "visible": b.visible, "test": test,
				})
				continue
			}
			ent := map[string]any{"kind": "material", "material": b.material, "field": b.field, "map": b.mapping}
			if b.target != "" {
				ent["target"] = b.target
			}
			bj = append(bj, ent)
		}
		ent["bindings"] = bj
		params = append(params, ent)
	}
	return map[string]any{"format": "greenware.runtime/1", "params": params}
}

func metaString(r *resolve.Resolved, name string) string {
	for _, f := range r.Meta {
		if f.Name == name {
			return f.Value.Str
		}
	}
	return ""
}

// mustJSON encodes a value built from maps, slices, strings, ints and f32. The
// encoder sorts map keys, so the bytes do not depend on iteration order.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("compiler: " + err.Error())
	}
	return b
}

// colliders turns the resolved collision block into collider shapes. The
// reflected copy of a mirror takes the pair name.
func (e *elab) colliders() []collider {
	var out []collider
	var one func(n *resolve.Node, refl bool, plane mesh.Plane, mirrored bool)
	one = func(n *resolve.Node, refl bool, plane mesh.Plane, mirrored bool) {
		switch n.Kind {
		case "mirror":
			if mirrored {
				e.unsupported("", "a mirror inside a mirror is not supported yet")
				return
			}
			pl := planeOf(label(n, "plane"))
			for _, ch := range n.Children {
				one(ch, false, pl, true)
			}
			for _, ch := range n.Children {
				one(ch, true, pl, true)
			}
		case "when":
			e.unsupported("", "a `when` on a runtime parameter in collision is not supported yet")
		case "capsule", "sphere", "box":
			out = append(out, e.collider(n, refl, plane))
		default:
			e.unsupported(n.Name, "the collider `%s` is not supported", n.Kind)
		}
	}
	for _, n := range e.r.Collision {
		one(n, false, mesh.PlaneX, false)
	}
	return out
}

func (e *elab) collider(n *resolve.Node, refl bool, plane mesh.Plane) collider {
	pt := func(name string) mesh.Vec3 {
		v := vec3(n, name)
		if refl {
			v = mesh.MirrorAffine(plane).M.MulVec(v)
		}
		return v
	}
	c := collider{name: effName(n, refl), shape: n.Kind, data: map[string]any{}}
	if f, ok := n.Get("joint"); ok && f.Valid() && f.Kind == unitsRef {
		// The shape stays in model space; the joint says what it follows.
		c.data["joint"] = e.jointName(f.Str, refl)
	}
	switch n.Kind {
	case "capsule":
		c.data["from"] = vecJSON(pt("from"))
		c.data["to"] = vecJSON(pt("to"))
		c.data["radius"] = f32(det.Round32(num(n, "radius")))
	case "sphere":
		c.data["at"] = vecJSON(pt("at"))
		c.data["radius"] = f32(det.Round32(num(n, "radius")))
	case "box":
		if !isTuple(n, "rot", 0) {
			e.unsupported(n.Name, "`rot` on a box collider is not supported yet")
		}
		c.data["at"] = vecJSON(pt("at"))
		c.data["size"] = vecJSON(vec3(n, "size"))
	}
	return c
}
