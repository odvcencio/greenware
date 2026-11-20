package compiler

import (
	"encoding/json"
	"sort"
	"strconv"

	"m31labs.dev/greenware/det"
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
	tris  int
	draws int
	verts int
	mats  int
	nodes int
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

// assemble builds the glTF document of one level from its node tree.
func (e *elab) assemble(root *snode, cols []collider, lvl int, fir string) (*level, bool) {
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
	for i, nm := range names {
		m, ok := e.material(nm)
		if !ok {
			return nil, false
		}
		matIndex[nm] = i
		doc.Materials = append(doc.Materials, m.m)
		bindings = append(bindings, m.bindings...)
	}

	lv := &level{n: lvl}
	var addNode func(n *snode) int
	addNode = func(n *snode) int {
		idx := len(doc.Nodes)
		node := gltf.Node{Name: n.name}
		if n.t != (mesh.Vec3{}) {
			t := n.t.F32()
			node.Translation = &t
		}
		doc.Nodes = append(doc.Nodes, node)
		if len(n.pieces) > 0 {
			// One primitive per material, in the order the materials first appear.
			var order []string
			groups := map[string][]piece{} // lookup only
			for _, p := range n.pieces {
				if _, ok := groups[p.mat]; !ok {
					order = append(order, p.mat)
				}
				groups[p.mat] = append(groups[p.mat], p)
			}
			m := gltf.Mesh{Name: n.name}
			for _, mat := range order {
				prim := primitiveOf(groups[mat])
				if mat != "" {
					prim.Material = gltf.Int(matIndex[mat])
				}
				lv.tris += len(prim.Indices) / 3
				lv.verts += len(prim.Positions) / 3
				lv.draws++
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
			kids = append(kids, addNode(k))
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
		return idx
	}
	rootIdx := addNode(root)
	// Colliders hang off the root, after its sockets.
	for _, c := range cols {
		data := map[string]any{"format": "greenware.collider/1", "name": c.name, "shape": c.shape}
		for k, v := range c.data {
			data[k] = v
		}
		doc.Nodes = append(doc.Nodes, gltf.Node{Name: "collider." + c.name, Extras: mustJSON(map[string]any{"greenware": data})})
		doc.Nodes[rootIdx].Children = append(doc.Nodes[rootIdx].Children, len(doc.Nodes)-1)
	}
	doc.Scenes = []gltf.Scene{{Nodes: []int{rootIdx}}}
	doc.Scene = gltf.Int(0)

	asset := map[string]any{"format": "greenware.asset/1", "fir_sha256": fir, "lod": lvl, "lod_error": e.lodErr}
	if rt := e.runtimeTable(bindings); rt != nil {
		asset["runtime"] = rt
	}
	doc.Extras = mustJSON(map[string]any{"greenware": asset})
	lv.doc = doc
	lv.mats = len(names)
	lv.nodes = len(doc.Nodes)
	return lv, true
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
		bl := by[nm]
		sort.SliceStable(bl, func(i, j int) bool {
			if bl[i].material != bl[j].material {
				return bl[i].material < bl[j].material
			}
			return bl[i].field < bl[j].field
		})
		var bj []any
		for _, b := range bl {
			bj = append(bj, map[string]any{"kind": "material", "material": b.material, "field": b.field, "map": b.mapping})
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
