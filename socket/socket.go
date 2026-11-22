// Package socket reads Greenware attachment frames from a built glTF scene.
package socket

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/internal/socketmeta"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/kernel/rig"
)

// Socket describes a meshless attachment node exported as "socket.<name>".
type Socket struct {
	Name  string
	Kind  string // from Greenware metadata, empty in a legacy file
	Joint string // authored joint name for a rig socket, otherwise empty
	Node  int    // index in the glTF document
	// Transform maps socket-local points to model coordinates in metres.
	// It includes the node's initial TRS and the full ancestor chain.
	Transform mesh.Affine
}

// Set holds sockets ordered by name, then node index.
// Instances with the same name are retained.
type Set []Socket

// Find returns the uniquely named socket. A missing or ambiguous name is an error.
func (s Set) Find(name string) (Socket, error) {
	var found Socket
	matched := false
	for _, item := range s {
		if item.Name != name {
			continue
		}
		if matched {
			return Socket{}, fmt.Errorf("socket %q is ambiguous (nodes %d and %d)", name, found.Node, item.Node)
		}
		found, matched = item, true
	}
	if !matched {
		return Socket{}, fmt.Errorf("no socket named %q in the scene", name)
	}
	return found, nil
}

// ReadGLB decodes a GLB and returns the sockets in its initial scene.
func ReadGLB(data []byte) (Set, error) {
	doc, err := gltf.Decode(data)
	if err != nil {
		return nil, err
	}
	return FromDocument(doc)
}

// FromDocument reads the default scene, or the first scene if the file leaves
// the default unspecified. A document without scenes has no sockets. Only
// reachable nodes are inspected. Visibility does not affect attachment frames.
// Animated rigs are read at their initial node transforms, without sampling a clip.
// The document is not modified.
func FromDocument(doc *gltf.Document) (Set, error) {
	if doc == nil {
		return nil, fmt.Errorf("nil glTF document")
	}
	out := make(Set, 0)
	if doc.Scene == nil && len(doc.Scenes) == 0 {
		return out, nil
	}
	scene := 0
	if doc.Scene != nil {
		scene = *doc.Scene
	}
	if scene < 0 || scene >= len(doc.Scenes) {
		return nil, fmt.Errorf("default scene index %d is out of range", scene)
	}
	type edge struct {
		node   int
		parent mesh.Affine
	}
	var pending []edge
	push := func(nodes []int, transform mesh.Affine) {
		for i := len(nodes) - 1; i >= 0; i-- {
			pending = append(pending, edge{node: nodes[i], parent: transform})
		}
	}
	push(doc.Scenes[scene].Nodes, mesh.IdentityAffine)
	seen := make([]bool, len(doc.Nodes))
	for len(pending) != 0 {
		next := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if next.node < 0 || next.node >= len(doc.Nodes) {
			return nil, fmt.Errorf("scene %d references out-of-range node %d", scene, next.node)
		}
		if seen[next.node] {
			return nil, fmt.Errorf("node %d reached more than once in scene %d", next.node, scene)
		}
		seen[next.node] = true
		node := &doc.Nodes[next.node]
		local, err := localFrame(node)
		if err != nil {
			return nil, fmt.Errorf("node %d (%q): %w", next.node, node.Name, err)
		}
		world := local.Then(next.parent)
		if !finiteFrame(world) {
			return nil, fmt.Errorf("node %d (%q): non-finite world transform", next.node, node.Name)
		}
		if name, ok := strings.CutPrefix(node.Name, socketmeta.Prefix); ok && name != "" && node.Mesh == nil {
			item := Socket{Name: name, Node: next.node, Transform: world}
			meta, ok, err := socketmeta.Decode(node.Extras)
			if err != nil {
				return nil, fmt.Errorf("node %d (%q): %w", next.node, node.Name, err)
			}
			if ok {
				if meta.Name != name {
					return nil, fmt.Errorf("node %d (%q): socket metadata name %q differs from node name", next.node, node.Name, meta.Name)
				}
				item.Kind, item.Joint = meta.Kind, meta.Joint
			}
			out = append(out, item)
		}
		push(node.Children, world)
	}
	slices.SortFunc(out, func(a, b Socket) int {
		if order := strings.Compare(a.Name, b.Name); order != 0 {
			return order
		}
		return a.Node - b.Node
	})
	return out, nil
}

func localFrame(n *gltf.Node) (mesh.Affine, error) {
	f := mesh.IdentityAffine
	if s := n.Scale; s != nil {
		f = mesh.ScaleAffine(mesh.Vec3{float64(s[0]), float64(s[1]), float64(s[2])})
	}
	if r := n.Rotation; r != nil {
		q := rig.Quat{float64(r[0]), float64(r[1]), float64(r[2]), float64(r[3])}
		if q == (rig.Quat{}) {
			return mesh.Affine{}, fmt.Errorf("zero rotation quaternion")
		}
		f.M = q.Normalize().Mat3().Mul(f.M)
	}
	if t := n.Translation; t != nil {
		f.T = mesh.Vec3{float64(t[0]), float64(t[1]), float64(t[2])}
	}
	return f, nil
}

func finiteFrame(f mesh.Affine) bool {
	for i, row := range f.M {
		if !finite(f.T[i]) {
			return false
		}
		for _, x := range row {
			if !finite(x) {
				return false
			}
		}
	}
	return true
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
