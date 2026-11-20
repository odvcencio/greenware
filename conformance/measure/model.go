// Package measure takes geometry measurements from a decoded GLB: bounds,
// draw calls, triangle counts, closure, connectedness, ray gaps and socket
// positions. It reads only the decoded file (package glb) and shares no code
// with the Greenware compiler. All lengths are metres in the model frame after
// node transforms.
package measure

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"m31labs.dev/greenware/conformance/glb"
)

// Tri is one world-space triangle. Corners are binary32 values, so exact
// equality of corners is exact equality of the stored (and transformed)
// binary32 positions.
type Tri [3][3]float32

// Draw is one drawn primitive instance.
type Draw struct {
	Node     int
	Mesh     int
	Prim     int
	Material int // -1 for none
	Tris     []Tri
}

type nodeInfo struct {
	name      string
	parent    int
	children  []int
	world     [16]float64
	inDefault bool // reachable from the default scene
	order     int  // depth-first position, for stable subtree listing
}

// Model is a decoded file prepared for measurement.
type Model struct {
	f      *glb.File
	nodes  []nodeInfo
	scene  int
	draws  []Draw
	byNode map[int][]int // node -> indices in draws
}

// Load prepares f for measurement. It fails if the file has no readable
// document or a drawn primitive cannot be read; run glb.Decode first and stop
// on error findings.
func Load(f *glb.File) (*Model, error) {
	if f == nil || f.Doc == nil {
		return nil, errors.New("measure: no decoded document")
	}
	d := f.Doc
	m := &Model{f: f, byNode: map[int][]int{}}
	n := len(d.Nodes)
	m.nodes = make([]nodeInfo, n)
	for i := range m.nodes {
		m.nodes[i] = nodeInfo{name: d.Nodes[i].Name, parent: -1}
	}
	for i, nd := range d.Nodes {
		for _, c := range nd.Children {
			if c < 0 || c >= n || c == i || m.nodes[c].parent != -1 {
				return nil, fmt.Errorf("measure: node %d has an invalid child %d", i, c)
			}
			m.nodes[c].parent = i
			m.nodes[i].children = append(m.nodes[i].children, c)
		}
	}
	// world matrices, depth first from every root, with a visit guard
	order := 0
	seen := make([]bool, n)
	for r := 0; r < n; r++ {
		if m.nodes[r].parent != -1 {
			continue
		}
		stack := []int{r}
		m.nodes[r].world = localMatrix(d.Nodes[r])
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[i] {
				return nil, fmt.Errorf("measure: node %d reached twice", i)
			}
			seen[i] = true
			m.nodes[i].order = order
			order++
			for _, c := range m.nodes[i].children {
				m.nodes[c].world = mul(m.nodes[i].world, localMatrix(d.Nodes[c]))
				stack = append(stack, c)
			}
		}
	}
	for i := range seen {
		if !seen[i] {
			return nil, fmt.Errorf("measure: node %d is on a cycle", i)
		}
	}
	// default scene
	if len(d.Scenes) > 0 {
		m.scene = 0
		if d.Scene != nil {
			m.scene = *d.Scene
		}
		if m.scene < 0 || m.scene >= len(d.Scenes) {
			return nil, fmt.Errorf("measure: default scene %d is out of range", m.scene)
		}
		for _, r := range d.Scenes[m.scene].Nodes {
			if r < 0 || r >= n {
				return nil, fmt.Errorf("measure: scene root %d is out of range", r)
			}
			m.markDefault(r)
		}
	}
	for i, nd := range d.Nodes {
		if nd.Mesh == nil {
			continue
		}
		if err := m.buildDraws(i, nd); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Model) markDefault(r int) {
	stack := []int{r}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		m.nodes[i].inDefault = true
		stack = append(stack, m.nodes[i].children...)
	}
}

func (m *Model) buildDraws(ni int, nd glb.Node) error {
	d := m.f.Doc
	if *nd.Mesh < 0 || *nd.Mesh >= len(d.Meshes) {
		return fmt.Errorf("measure: node %d has mesh %d out of range", ni, *nd.Mesh)
	}
	mesh := d.Meshes[*nd.Mesh]
	weights := nd.Weights
	if weights == nil {
		weights = mesh.Weights
	}
	for pi, p := range mesh.Primitives {
		dr := Draw{Node: ni, Mesh: *nd.Mesh, Prim: pi, Material: -1}
		if p.Material != nil {
			dr.Material = *p.Material
		}
		if p.Mode == 4 {
			world := m.nodes[ni].world
			if nd.Skin != nil {
				world = identity // a skinned mesh node's own transform is ignored
			}
			tris, err := m.primitiveTriangles(p, weights, world)
			if err != nil {
				return fmt.Errorf("measure: node %d mesh %d primitive %d: %w", ni, *nd.Mesh, pi, err)
			}
			dr.Tris = tris
		}
		m.byNode[ni] = append(m.byNode[ni], len(m.draws))
		m.draws = append(m.draws, dr)
	}
	return nil
}

func (m *Model) primitiveTriangles(p glb.Primitive, weights []float64, w [16]float64) ([]Tri, error) {
	pos, nc, ok := m.f.Floats(p.Attributes["POSITION"])
	if !ok || nc != 3 {
		return nil, errors.New("POSITION is unreadable")
	}
	nv := len(pos) / 3
	pts := make([][3]float64, nv)
	for i := range pts {
		pts[i] = [3]float64{pos[3*i], pos[3*i+1], pos[3*i+2]}
	}
	for ti, t := range p.Targets {
		if ti >= len(weights) || weights[ti] == 0 {
			continue
		}
		ai, has := t["POSITION"]
		if !has {
			continue
		}
		dv, nc, ok := m.f.Floats(ai)
		if !ok || nc != 3 || len(dv) != len(pos) {
			return nil, errors.New("morph target POSITION is unreadable")
		}
		for i := range pts {
			for k := 0; k < 3; k++ {
				pts[i][k] += weights[ti] * dv[3*i+k]
			}
		}
	}
	var idx []uint32
	if p.Indices != nil {
		var ok bool
		if idx, ok = m.f.Indices(*p.Indices); !ok {
			return nil, errors.New("indices are unreadable")
		}
	} else {
		idx = make([]uint32, nv)
		for i := range idx {
			idx[i] = uint32(i)
		}
	}
	world := make([][3]float32, nv)
	for i, q := range pts {
		world[i] = transform(w, q)
	}
	tris := make([]Tri, 0, len(idx)/3)
	for k := 0; k+2 < len(idx); k += 3 {
		a, b, c := idx[k], idx[k+1], idx[k+2]
		if int(a) >= nv || int(b) >= nv || int(c) >= nv {
			return nil, errors.New("index out of range")
		}
		tris = append(tris, Tri{world[a], world[b], world[c]})
	}
	return tris, nil
}

// transform applies a column-major affine matrix and rounds to binary32. For
// the identity matrix this is exact.
func transform(m [16]float64, p [3]float64) [3]float32 {
	if m == identity {
		return [3]float32{float32(p[0]), float32(p[1]), float32(p[2])}
	}
	var o [3]float32
	for r := 0; r < 3; r++ {
		o[r] = float32(m[r]*p[0] + m[4+r]*p[1] + m[8+r]*p[2] + m[12+r])
	}
	return o
}

var identity = [16]float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}

func mul(a, b [16]float64) [16]float64 {
	var o [16]float64
	for c := 0; c < 4; c++ {
		for r := 0; r < 4; r++ {
			var s float64
			for k := 0; k < 4; k++ {
				s += a[k*4+r] * b[c*4+k]
			}
			o[c*4+r] = s
		}
	}
	return o
}

func localMatrix(n glb.Node) [16]float64 {
	if n.Matrix != nil {
		return *n.Matrix
	}
	x, y, z, w := n.Rotation[0], n.Rotation[1], n.Rotation[2], n.Rotation[3]
	if l := math.Sqrt(x*x + y*y + z*z + w*w); l > 0 {
		x, y, z, w = x/l, y/l, z/l, w/l
	}
	sx, sy, sz := n.Scale[0], n.Scale[1], n.Scale[2]
	return [16]float64{
		(1 - 2*(y*y+z*z)) * sx, (2 * (x*y + z*w)) * sx, (2 * (x*z - y*w)) * sx, 0,
		(2 * (x*y - z*w)) * sy, (1 - 2*(x*x+z*z)) * sy, (2 * (y*z + x*w)) * sy, 0,
		(2 * (x*z + y*w)) * sz, (2 * (y*z - x*w)) * sz, (1 - 2*(x*x+y*y)) * sz, 0,
		n.Translation[0], n.Translation[1], n.Translation[2], 1,
	}
}

// Select names the node whose primitives a measurement uses.
type Select struct {
	// Name is a node name. Empty selects every drawn primitive in the scene.
	Name string
	// Own restricts the selection to the node's own mesh. When false, the node's
	// whole subtree is used.
	Own bool
}

// Whole selects the entire default scene.
var Whole = Select{}

// Sub selects a node's subtree.
func Sub(name string) Select { return Select{Name: name} }

// OwnOf selects a node's own primitives only.
func OwnOf(name string) Select { return Select{Name: name, Own: true} }

// NodeID finds the first node (in file order) with this name, in either the
// default hierarchy or an alternate one.
func (m *Model) NodeID(name string) (int, bool) {
	for i, n := range m.nodes {
		if n.name == name {
			return i, true
		}
	}
	return -1, false
}

// HasNode reports whether any node, drawn or not, has this name.
func (m *Model) HasNode(name string) bool { _, ok := m.NodeID(name); return ok }

// NodeNames lists every node name in file order, empty names omitted.
func (m *Model) NodeNames() []string {
	var out []string
	for _, n := range m.nodes {
		if n.name != "" {
			out = append(out, n.name)
		}
	}
	return out
}

// InDefaultScene reports whether the named node is reachable from the default scene.
func (m *Model) InDefaultScene(name string) bool {
	i, ok := m.NodeID(name)
	return ok && m.nodes[i].inDefault
}

func (m *Model) subtree(root int) []int {
	var out []int
	stack := []int{root}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		out = append(out, i)
		stack = append(stack, m.nodes[i].children...)
	}
	return out
}

// selectedDraws lists the drawn primitives in the selection. A primitive is
// drawn when its node is in the default scene and it has at least one triangle.
func (m *Model) selectedDraws(s Select) ([]*Draw, error) {
	ids, err := m.selectedNodes(s)
	if err != nil {
		return nil, err
	}
	var out []*Draw
	for _, i := range ids {
		for _, di := range m.byNode[i] {
			if len(m.draws[di].Tris) > 0 {
				out = append(out, &m.draws[di])
			}
		}
	}
	return out, nil
}

// selectedNodes lists the nodes of a selection that are in the default scene.
func (m *Model) selectedNodes(s Select) ([]int, error) {
	var ids []int
	if s.Name == "" {
		for i := range m.nodes {
			ids = append(ids, i)
		}
	} else {
		root, ok := m.NodeID(s.Name)
		if !ok {
			return nil, fmt.Errorf("no node named %q", s.Name)
		}
		if s.Own {
			ids = []int{root}
		} else {
			ids = m.subtree(root)
		}
	}
	out := ids[:0]
	for _, i := range ids {
		if m.nodes[i].inDefault {
			out = append(out, i)
		}
	}
	return out, nil
}

// Triangles returns the drawn triangles of the selection.
func (m *Model) Triangles(s Select) ([]Tri, error) {
	ds, err := m.selectedDraws(s)
	if err != nil {
		return nil, err
	}
	var out []Tri
	for _, d := range ds {
		out = append(out, d.Tris...)
	}
	return out, nil
}

// DrawnTriangles counts drawn triangles in the selection. A node that exists
// but is hidden (outside the default scene) counts zero; an unknown name is an error.
func (m *Model) DrawnTriangles(s Select) (int, error) {
	ds, err := m.selectedDraws(s)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range ds {
		n += len(d.Tris)
	}
	return n, nil
}

// DrawCalls counts drawn primitives in the default scene: one per primitive
// of every node instance, so a mesh with two materials is two draws and two
// nodes that share a material are still two draws. Primitives without
// triangles are not drawn.
func (m *Model) DrawCalls(s Select) (int, error) {
	ds, err := m.selectedDraws(s)
	return len(ds), err
}

// Materials lists the material indices of the drawn primitives in the selection.
func (m *Model) Materials(s Select) ([]int, error) {
	ds, err := m.selectedDraws(s)
	if err != nil {
		return nil, err
	}
	var out []int
	for _, d := range ds {
		out = append(out, d.Material)
	}
	return out, nil
}

// Box is an axis-aligned box.
type Box struct{ Min, Max [3]float64 }

// Bounds returns the box of all drawn vertices in the selection (the whole
// subtree unless Own is set). ok is false when nothing is drawn.
func (m *Model) Bounds(s Select) (Box, bool, error) {
	tris, err := m.Triangles(s)
	if err != nil || len(tris) == 0 {
		return Box{}, false, err
	}
	b := Box{Min: [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}, Max: [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}}
	for _, t := range tris {
		for _, v := range t {
			for k := 0; k < 3; k++ {
				b.Min[k] = math.Min(b.Min[k], float64(v[k]))
				b.Max[k] = math.Max(b.Max[k], float64(v[k]))
			}
		}
	}
	return b, true, nil
}

// Socket is a meshless node named socket.<name>, with its world frame.
type Socket struct {
	Name     string // without the "socket." prefix
	Node     int
	Position [3]float64
	// Axes holds the normalised world X, Y and Z axes of the node.
	Axes    [3][3]float64
	HasMesh bool
}

// Sockets lists every node named socket.<name>, in file order, wherever it sits.
func (m *Model) Sockets() []Socket {
	var out []Socket
	for i, n := range m.nodes {
		name, ok := strings.CutPrefix(n.name, "socket.")
		if !ok {
			continue
		}
		s := Socket{Name: name, Node: i, HasMesh: m.f.Doc.Nodes[i].Mesh != nil}
		w := n.world
		s.Position = [3]float64{w[12], w[13], w[14]}
		for a := 0; a < 3; a++ {
			v := [3]float64{w[a*4], w[a*4+1], w[a*4+2]}
			l := math.Sqrt(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])
			if l > 0 {
				v = [3]float64{v[0] / l, v[1] / l, v[2] / l}
			}
			s.Axes[a] = v
		}
		out = append(out, s)
	}
	return out
}

// Socket finds one socket by name, with or without the "socket." prefix.
func (m *Model) Socket(name string) (Socket, bool) {
	name = strings.TrimPrefix(name, "socket.")
	for _, s := range m.Sockets() {
		if s.Name == name {
			return s, true
		}
	}
	return Socket{}, false
}

// Collider is a node named collider.<name>.
type Collider struct {
	Name    string
	HasMesh bool
}

// Colliders lists nodes named collider.<name>.
func (m *Model) Colliders() []Collider {
	var out []Collider
	for i, n := range m.nodes {
		if name, ok := strings.CutPrefix(n.name, "collider."); ok {
			out = append(out, Collider{name, m.f.Doc.Nodes[i].Mesh != nil})
		}
	}
	return out
}

// Distance is the straight-line distance between two socket positions.
func Distance(a, b Socket) float64 {
	dx, dy, dz := a.Position[0]-b.Position[0], a.Position[1]-b.Position[1], a.Position[2]-b.Position[2]
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}
