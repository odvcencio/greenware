// Package meshglb packs kernel surfaces as indexed glTF primitives.
package meshglb

import (
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/mesh"
)

// Generator is the generator identity carried by Greenware GLBs.
const Generator = "greenware 0.1"

type vertexKey struct {
	position [3]float32
	normal   [3]float32
	uv       [2]float32
}

// Append adds one valid surface to a primitive and returns the number of
// vertices written for it. Vertex identity includes both corner attributes so
// UV seams and hard edges survive packing. Indices preserve triangle order.
func Append(primitive *gltf.Primitive, surface *mesh.Mesh) int {
	first := len(primitive.Positions) / 3
	seen := make(map[vertexKey]uint32)
	remap := make([]uint32, len(surface.Corners))
	for i, corner := range surface.Corners {
		key := vertexKey{
			position: surface.Positions[corner.Pos].F32(),
			normal:   corner.Normal.F32(),
			uv:       corner.UV.F32(),
		}
		index, ok := seen[key]
		if !ok {
			index = uint32(len(primitive.Positions) / 3)
			seen[key] = index
			primitive.Positions = append(primitive.Positions, key.position[:]...)
			primitive.Normals = append(primitive.Normals, key.normal[:]...)
			primitive.UV0 = append(primitive.UV0, key.uv[:]...)
		}
		remap[i] = index
	}
	for _, triangle := range surface.Tris {
		primitive.Indices = append(primitive.Indices, remap[triangle[0]], remap[triangle[1]], remap[triangle[2]])
	}
	return len(primitive.Positions)/3 - first
}
