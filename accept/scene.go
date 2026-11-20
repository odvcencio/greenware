package accept

import (
	"encoding/json"

	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/mesh"
)

// Every floating-point multiply in this package is wrapped in float64(...) so
// that arm64 and amd64 give the same bits.

// affine is a 3x3 matrix (row major) and a translation.
type affine struct {
	m [3][3]float64
	t [3]float64
}

var identity = affine{m: [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}}

func (a affine) apply(p [3]float64) [3]float64 {
	var o [3]float64
	for i := 0; i < 3; i++ {
		o[i] = float64(a.m[i][0]*p[0]) + float64(a.m[i][1]*p[1]) + float64(a.m[i][2]*p[2]) + a.t[i]
	}
	return o
}

// then returns the transform that applies b first and a second.
func (a affine) then(b affine) affine {
	var o affine
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			o.m[i][j] = float64(a.m[i][0]*b.m[0][j]) + float64(a.m[i][1]*b.m[1][j]) + float64(a.m[i][2]*b.m[2][j])
		}
	}
	o.t = a.apply(b.t)
	return o
}

func (a affine) det() float64 {
	m := a.m
	return float64(m[0][0]*(float64(m[1][1]*m[2][2])-float64(m[1][2]*m[2][1]))) -
		float64(m[0][1]*(float64(m[1][0]*m[2][2])-float64(m[1][2]*m[2][0]))) +
		float64(m[0][2]*(float64(m[1][0]*m[2][1])-float64(m[1][1]*m[2][0])))
}

// localTransform is T * R * S of a node.
func localTransform(n *gltf.Node) affine {
	a := identity
	if n.Scale != nil {
		for i := 0; i < 3; i++ {
			a.m[i][i] = float64(n.Scale[i])
		}
	}
	if n.Rotation != nil {
		x, y, z, w := float64(n.Rotation[0]), float64(n.Rotation[1]), float64(n.Rotation[2]), float64(n.Rotation[3])
		l := det.Sqrt(float64(x*x) + float64(y*y) + float64(z*z) + float64(w*w))
		if l > 0 {
			x, y, z, w = x/l, y/l, z/l, w/l
		}
		xx, yy, zz := float64(x*x), float64(y*y), float64(z*z)
		xy, xz, yz := float64(x*y), float64(x*z), float64(y*z)
		wx, wy, wz := float64(w*x), float64(w*y), float64(w*z)
		r := [3][3]float64{
			{1 - float64(2*(yy+zz)), float64(2 * (xy - wz)), float64(2 * (xz + wy))},
			{float64(2 * (xy + wz)), 1 - float64(2*(xx+zz)), float64(2 * (yz - wx))},
			{float64(2 * (xz - wy)), float64(2 * (yz + wx)), 1 - float64(2*(xx+yy))},
		}
		a = affine{m: r}.then(a)
	}
	if n.Translation != nil {
		for i := 0; i < 3; i++ {
			a.t[i] = float64(n.Translation[i])
		}
	}
	return a
}

type nodeInfo struct {
	parent int // -1 for a root
	world  affine
	// active: reachable from the default scene. A node outside the scene is an
	// alternate: present, but never drawn.
	active bool
	// visible: active, and neither the node nor an ancestor is hidden by
	// KHR_node_visibility.
	visible bool
}

// scene is one decoded GLB prepared for measurement.
type scene struct {
	doc    *gltf.Document
	nodes  []nodeInfo
	byName map[string][]int
	tris   map[int][]tri // own visible triangles, by node
}

// tri is a triangle in the model frame. Corners are float32 positions held as
// float64, so exact equality of corners is exact equality of binary32 values.
type tri [3]mesh.Vec3

func newScene(doc *gltf.Document) *scene {
	s := &scene{doc: doc, nodes: make([]nodeInfo, len(doc.Nodes)), byName: map[string][]int{}, tris: map[int][]tri{}}
	for i := range s.nodes {
		s.nodes[i].parent = -1
	}
	for i, n := range doc.Nodes {
		for _, c := range n.Children {
			if c >= 0 && c < len(doc.Nodes) && s.nodes[c].parent < 0 && c != i {
				s.nodes[c].parent = i
			}
		}
		if n.Name != "" {
			s.byName[n.Name] = append(s.byName[n.Name], i)
		}
	}
	// World transforms for every node, active or not, so that an alternate
	// socket still has a position through its intended parent.
	done := make([]bool, len(doc.Nodes))
	var world func(i int, depth int) affine
	world = func(i int, depth int) affine {
		if done[i] {
			return s.nodes[i].world
		}
		local := localTransform(&doc.Nodes[i])
		w := local
		if p := s.nodes[i].parent; p >= 0 && depth < len(doc.Nodes) {
			w = world(p, depth+1).then(local)
		}
		s.nodes[i].world = w
		done[i] = true
		return w
	}
	for i := range doc.Nodes {
		world(i, 0)
	}
	// Activity and visibility by walking down from the default scene's roots.
	var roots []int
	switch {
	case len(doc.Scenes) == 0:
		for i := range doc.Nodes {
			if s.nodes[i].parent < 0 {
				roots = append(roots, i)
			}
		}
	case doc.Scene != nil && *doc.Scene >= 0 && *doc.Scene < len(doc.Scenes):
		roots = doc.Scenes[*doc.Scene].Nodes
	default:
		roots = doc.Scenes[0].Nodes
	}
	seen := make([]bool, len(doc.Nodes))
	var walk func(i int, parentVisible bool)
	walk = func(i int, parentVisible bool) {
		if i < 0 || i >= len(doc.Nodes) || seen[i] {
			return
		}
		seen[i] = true
		vis := parentVisible && !hiddenByExtension(&doc.Nodes[i])
		s.nodes[i].active = true
		s.nodes[i].visible = vis
		for _, c := range doc.Nodes[i].Children {
			walk(c, vis)
		}
	}
	for _, r := range roots {
		walk(r, true)
	}
	return s
}

// hiddenByExtension reads KHR_node_visibility {"visible": false}.
func hiddenByExtension(n *gltf.Node) bool {
	raw, ok := n.Extensions["KHR_node_visibility"]
	if !ok {
		return false
	}
	var v struct {
		Visible *bool `json:"visible"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Visible == nil {
		return false
	}
	return !*v.Visible
}

// position is the model-frame origin of node i.
func (s *scene) position(i int) mesh.Vec3 { return s.nodes[i].world.t }

// ownTris returns the triangles of the node's own primitives (not its
// children's), in the model frame. A node that is not visible has none.
func (s *scene) ownTris(i int) []tri {
	if t, ok := s.tris[i]; ok {
		return t
	}
	var out []tri
	n := &s.doc.Nodes[i]
	if n.Mesh != nil && s.nodes[i].visible && *n.Mesh >= 0 && *n.Mesh < len(s.doc.Meshes) {
		w := s.nodes[i].world
		flip := w.det() < 0 // a mirrored node reverses the winding
		for _, p := range s.doc.Meshes[*n.Mesh].Primitives {
			out = appendPrimitive(out, p, w, flip)
		}
	}
	s.tris[i] = out
	return out
}

// primitives counts the visible primitives of node i. One visible primitive
// on one node is one draw (language reference, section 6.4).
func (s *scene) primitives(i int) int {
	n := &s.doc.Nodes[i]
	if n.Mesh == nil || !s.nodes[i].visible || *n.Mesh < 0 || *n.Mesh >= len(s.doc.Meshes) {
		return 0
	}
	return len(s.doc.Meshes[*n.Mesh].Primitives)
}

func appendPrimitive(out []tri, p gltf.Primitive, w affine, flip bool) []tri {
	nv := len(p.Positions) / 3
	pos := func(v uint32) (mesh.Vec3, bool) {
		if int(v) >= nv {
			return mesh.Vec3{}, false
		}
		q := w.apply([3]float64{float64(p.Positions[3*v]), float64(p.Positions[3*v+1]), float64(p.Positions[3*v+2])})
		// Canonical model-frame binary32 position.
		return mesh.Vec3{float64(float32(q[0])), float64(float32(q[1])), float64(float32(q[2]))}, true
	}
	count := len(p.Indices)
	if count == 0 {
		count = nv
	}
	idx := func(k int) uint32 {
		if len(p.Indices) == 0 {
			return uint32(k)
		}
		return p.Indices[k]
	}
	for k := 0; k+2 < count; k += 3 {
		a, ok1 := pos(idx(k))
		b, ok2 := pos(idx(k + 1))
		c, ok3 := pos(idx(k + 2))
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		if flip {
			b, c = c, b
		}
		out = append(out, tri{a, b, c})
	}
	return out
}

// subtree returns node i and its active, visible descendants.
func (s *scene) subtree(i int) []int {
	var out []int
	var walk func(i int)
	walk = func(i int) {
		if !s.nodes[i].visible {
			return
		}
		out = append(out, i)
		for _, c := range s.doc.Nodes[i].Children {
			if c >= 0 && c < len(s.doc.Nodes) && s.nodes[c].parent == i {
				walk(c)
			}
		}
	}
	walk(i)
	return out
}

// allVisible lists every visible node in index order.
func (s *scene) allVisible() []int {
	var out []int
	for i := range s.nodes {
		if s.nodes[i].visible {
			out = append(out, i)
		}
	}
	return out
}
