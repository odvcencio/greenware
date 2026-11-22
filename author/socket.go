package author

import (
	"fmt"
	"math"

	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/internal/socketmeta"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/kernel/rig"
)

// Socket defines an attachment frame relative to a named part. An empty
// parent attaches to the scene root. Rotation is a quaternion in glTF order
// (x, y, z, w). The zero value means identity. An empty Kind means "anchor".
type Socket struct {
	Name        string
	Kind        string
	Parent      string
	Translation mesh.Vec3
	Rotation    rig.Quat
}

const maxSockets = 4096

func attachSockets(doc *gltf.Document, parts map[string]int, sockets []Socket) error {
	seen := make(map[string]bool, len(sockets))
	for _, s := range sockets {
		nodeName := socketmeta.Prefix + s.Name
		_, used := parts[nodeName]
		if !validName(s.Name) || used || seen[s.Name] {
			return fmt.Errorf("author: socket name %q is empty, invalid or already used", s.Name)
		}
		seen[s.Name] = true
		parent, ok := parts[s.Parent]
		if s.Parent != "" && !ok {
			return fmt.Errorf("author: socket %q uses unknown parent %q", s.Name, s.Parent)
		}
		kind := s.Kind
		if kind == "" {
			kind = "anchor"
		}
		if !validName(kind) {
			return fmt.Errorf("author: socket %q needs a valid UTF-8 kind", s.Name)
		}
		q, err := encodeSocketRotation(s.Rotation)
		if err != nil {
			return fmt.Errorf("author: socket %q: %w", s.Name, err)
		}
		node := gltf.Node{
			Name: nodeName, Rotation: q,
			Extras: socketmeta.Encode(socketmeta.Value{Name: s.Name, Kind: kind}),
		}
		if s.Translation != (mesh.Vec3{}) {
			t := s.Translation.F32()
			node.Translation = &t
		}
		doc.Nodes[parent].Children = append(doc.Nodes[parent].Children, len(doc.Nodes))
		doc.Nodes = append(doc.Nodes, node)
	}
	return nil
}

func encodeSocketRotation(q rig.Quat) (*[4]float32, error) {
	longest := 0.0
	for _, component := range q {
		if math.IsInf(component, 0) || math.IsNaN(component) {
			return nil, fmt.Errorf("rotation quaternion must be finite")
		}
		longest = max(longest, math.Abs(component))
	}
	if longest == 0 {
		return nil, nil
	}
	// Scale before normalizing so finite inputs cannot overflow the norm.
	for i := range q {
		q[i] /= longest
	}
	q = q.Normalize()
	if q == rig.QIdentity {
		return nil, nil
	}
	rounded := [4]float32{det.Round32(q[0]), det.Round32(q[1]), det.Round32(q[2]), det.Round32(q[3])}
	return &rounded, nil
}
