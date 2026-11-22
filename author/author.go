// Package author builds rigid Greenware assets from Go geometry.
package author

import (
	"fmt"
	"image/color"
	"sort"
	"unicode/utf8"

	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/internal/meshglb"
	"m31labs.dev/greenware/internal/pbrcolor"
	"m31labs.dev/greenware/kernel/mesh"
)

// Asset describes one rigid scene. Parts are written in slice order. Materials
// are named and put in name order so their declaration order is immaterial.
type Asset struct {
	Name      string
	Parts     []Part
	Materials []Material
}

// Material holds portable metallic-roughness factors. Base uses non-premultiplied
// sRGB bytes with linear alpha. Set Blend to make alpha affect rendering.
type Material struct {
	Name                string
	Base                color.NRGBA
	Metallic, Roughness float64
	Blend, DoubleSided  bool
}

// Part is one named surface in metres. Translation is relative to the scene
// root. Material selects a named material; an empty name uses glTF's default.
type Part struct {
	Name        string
	Mesh        *mesh.Mesh
	Material    string
	Translation mesh.Vec3
}

// Build packs kernel meshes into a self-contained GLB. Caller-owned input is
// read during the call and can be reused or changed after Build returns.
func Build(asset Asset) ([]byte, error) {
	return BuildWithSockets(asset, nil)
}

// BuildWithSockets packs a rigid asset and adds attachment frames to its root
// or to named parts. Sockets are written in slice order.
func BuildWithSockets(asset Asset, sockets []Socket) ([]byte, error) {
	if !validName(asset.Name) {
		return nil, fmt.Errorf("author: an asset needs a nonempty UTF-8 name")
	}
	if len(asset.Parts) == 0 {
		return nil, fmt.Errorf("author: asset %q has no parts", asset.Name)
	}
	const maxParts, maxMaterials = 4096, 4096
	if len(asset.Parts) > maxParts || len(asset.Materials) > maxMaterials {
		return nil, fmt.Errorf("author: an asset allows at most %d parts and %d materials", maxParts, maxMaterials)
	}
	if len(sockets) > maxSockets {
		return nil, fmt.Errorf("author: an asset allows at most %d sockets", maxSockets)
	}
	materials := append([]Material(nil), asset.Materials...)
	sort.SliceStable(materials, func(i, j int) bool { return materials[i].Name < materials[j].Name })
	indices := make(map[string]int, len(materials))
	packedMaterials := make([]gltf.Material, 0, len(materials))
	for i, material := range materials {
		if !validName(material.Name) {
			return nil, fmt.Errorf("author: material %d needs a nonempty UTF-8 name", i)
		}
		if _, exists := indices[material.Name]; exists {
			return nil, fmt.Errorf("author: material %q appears more than once", material.Name)
		}
		if !factor(material.Metallic) || !factor(material.Roughness) {
			return nil, fmt.Errorf("author: material %q needs metallic and roughness in [0, 1]", material.Name)
		}
		indices[material.Name] = i
		base := material.Base
		core := gltf.Material{
			Name: material.Name,
			BaseColor: [4]float32{
				det.Round32(pbrcolor.Linear(base.R)), det.Round32(pbrcolor.Linear(base.G)),
				det.Round32(pbrcolor.Linear(base.B)), det.Round32(float64(base.A) / 255),
			},
			Metallic: det.Round32(material.Metallic), Roughness: det.Round32(material.Roughness),
			DoubleSided: material.DoubleSided,
		}
		if material.Blend {
			core.AlphaMode = gltf.AlphaBlend
		}
		packedMaterials = append(packedMaterials, core)
	}
	const maxSurfaceElements = 1 << 20
	remaining := maxSurfaceElements
	doc := &gltf.Document{
		Asset:     gltf.Asset{Generator: meshglb.Generator},
		Materials: packedMaterials,
		Nodes:     []gltf.Node{{Name: asset.Name}},
		Scenes:    []gltf.Scene{{Name: "default", Nodes: []int{0}}},
		Scene:     gltf.Int(0),
	}
	names := map[string]int{asset.Name: 0}
	for _, part := range asset.Parts {
		if _, used := names[part.Name]; !validName(part.Name) || used {
			return nil, fmt.Errorf("author: part name %q is empty, invalid or already used", part.Name)
		}
		names[part.Name] = len(doc.Nodes)
		if part.Mesh == nil || len(part.Mesh.Tris) == 0 {
			return nil, fmt.Errorf("author: part %q has no triangles", part.Name)
		}
		for _, count := range []int{len(part.Mesh.Positions), len(part.Mesh.Corners), len(part.Mesh.Tris)} {
			if count > remaining {
				return nil, fmt.Errorf("author: asset %q exceeds the %d surface element limit", asset.Name, maxSurfaceElements)
			}
			remaining -= count
		}
		if err := part.Mesh.Validate(); err != nil {
			return nil, fmt.Errorf("author: part %q: %w", part.Name, err)
		}
		var primitive gltf.Primitive
		if part.Material != "" {
			index, ok := indices[part.Material]
			if !ok {
				return nil, fmt.Errorf("author: part %q uses unknown material %q", part.Name, part.Material)
			}
			primitive.Material = gltf.Int(index)
		}
		meshglb.Append(&primitive, part.Mesh)
		node := gltf.Node{Name: part.Name, Mesh: gltf.Int(len(doc.Meshes))}
		if part.Translation != (mesh.Vec3{}) {
			translation := part.Translation.F32()
			node.Translation = &translation
		}
		doc.Meshes = append(doc.Meshes, gltf.Mesh{Name: part.Name, Primitives: []gltf.Primitive{primitive}})
		doc.Nodes[0].Children = append(doc.Nodes[0].Children, len(doc.Nodes))
		doc.Nodes = append(doc.Nodes, node)
	}
	if err := attachSockets(doc, names, sockets); err != nil {
		return nil, err
	}
	glb, err := gltf.Encode(doc)
	if err != nil {
		return nil, fmt.Errorf("author: asset %q: %w", asset.Name, err)
	}
	return glb, nil
}

func validName(name string) bool { return name != "" && utf8.ValidString(name) }

func factor(v float64) bool { return v >= 0 && v <= 1 }
