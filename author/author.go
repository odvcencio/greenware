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
	"m31labs.dev/greenware/kernel/rig"
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

// Assembly describes a rigid scene with reusable meshes. Prototypes and
// materials are referenced by name. Instances are written in slice order.
type Assembly struct {
	Name       string
	Prototypes []Prototype
	Instances  []Instance
	Materials  []Material
	Sockets    []Socket
}

// Prototype is a shareable mesh with one optional material.
type Prototype struct {
	Name     string
	Mesh     *mesh.Mesh
	Material string
}

// Instance places a prototype below the scene root. Sockets can name an
// instance as their parent. A zero Rotation or Scale uses identity.
type Instance struct {
	Name        string
	Prototype   string
	Translation mesh.Vec3
	Rotation    rig.Quat
	Scale       mesh.Vec3
}

const maxParts, maxMaterials = 4096, 4096

// Build packs kernel meshes into a self-contained GLB. Caller-owned input is
// read during the call and can be reused or changed after Build returns.
func Build(asset Asset) ([]byte, error) {
	return BuildWithSockets(asset, nil)
}

// BuildWithSockets packs a rigid asset and adds attachment frames to its root
// or to named parts. Sockets are written in slice order.
func BuildWithSockets(asset Asset, sockets []Socket) ([]byte, error) {
	if len(asset.Parts) > maxParts {
		return nil, fmt.Errorf("author: an asset allows at most %d parts", maxParts)
	}
	assembly := Assembly{Name: asset.Name, Materials: asset.Materials, Sockets: sockets,
		Prototypes: make([]Prototype, 0, len(asset.Parts)), Instances: make([]Instance, 0, len(asset.Parts))}
	for _, part := range asset.Parts {
		assembly.Prototypes = append(assembly.Prototypes, Prototype{Name: part.Name, Mesh: part.Mesh, Material: part.Material})
		assembly.Instances = append(assembly.Instances, Instance{Name: part.Name, Prototype: part.Name, Translation: part.Translation})
	}
	return BuildAssembly(assembly)
}

// BuildAssembly packs referenced prototypes once, then writes each instance
// as a glTF node with its own transform. Unused prototypes emit no mesh.
// The compiler and renderer need no instancing extension to read the result.
func BuildAssembly(asset Assembly) ([]byte, error) {
	if !validName(asset.Name) {
		return nil, fmt.Errorf("author: an asset needs a nonempty UTF-8 name")
	}
	if len(asset.Instances) == 0 {
		return nil, fmt.Errorf("author: asset %q has no instances", asset.Name)
	}
	if len(asset.Instances) > maxParts || len(asset.Prototypes) > maxParts || len(asset.Materials) > maxMaterials {
		return nil, fmt.Errorf("author: an asset allows at most %d instances, %d prototypes and %d materials",
			maxParts, maxParts, maxMaterials)
	}
	if len(asset.Sockets) > maxSockets {
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
	prototypes := make(map[string]Prototype, len(asset.Prototypes))
	for _, entry := range asset.Prototypes {
		if _, used := prototypes[entry.Name]; !validName(entry.Name) || used {
			return nil, fmt.Errorf("author: prototype name %q is empty, invalid or already used", entry.Name)
		}
		prototypes[entry.Name] = entry
	}
	meshIndices := make(map[string]int, len(prototypes))
	names := map[string]int{asset.Name: 0}
	for _, instance := range asset.Instances {
		if _, used := names[instance.Name]; !validName(instance.Name) || used {
			return nil, fmt.Errorf("author: instance name %q is empty, invalid or already used", instance.Name)
		}
		prototype, ok := prototypes[instance.Prototype]
		if !ok {
			return nil, fmt.Errorf("author: instance %q uses unknown prototype %q", instance.Name, instance.Prototype)
		}
		index, packed := meshIndices[instance.Prototype]
		if !packed {
			m, err := packPrototype(prototype, indices, &remaining, maxSurfaceElements)
			if err != nil {
				return nil, err
			}
			index = len(doc.Meshes)
			meshIndices[instance.Prototype] = index
			doc.Meshes = append(doc.Meshes, m)
		}
		node, err := instanceNode(instance, index)
		if err != nil {
			return nil, fmt.Errorf("author: instance %q: %w", instance.Name, err)
		}
		names[instance.Name] = len(doc.Nodes)
		doc.Nodes[0].Children = append(doc.Nodes[0].Children, len(doc.Nodes))
		doc.Nodes = append(doc.Nodes, node)
	}
	if err := attachSockets(doc, names, asset.Sockets); err != nil {
		return nil, err
	}
	glb, err := gltf.Encode(doc)
	if err != nil {
		return nil, fmt.Errorf("author: asset %q: %w", asset.Name, err)
	}
	return glb, nil
}

func packPrototype(p Prototype, materials map[string]int, remaining *int, limit int) (gltf.Mesh, error) {
	if p.Mesh == nil || len(p.Mesh.Tris) == 0 {
		return gltf.Mesh{}, fmt.Errorf("author: prototype %q has no triangles", p.Name)
	}
	for _, count := range []int{len(p.Mesh.Positions), len(p.Mesh.Corners), len(p.Mesh.Tris)} {
		if count > *remaining {
			return gltf.Mesh{}, fmt.Errorf("author: prototype %q exceeds the %d surface element limit", p.Name, limit)
		}
		*remaining -= count
	}
	if err := p.Mesh.Validate(); err != nil {
		return gltf.Mesh{}, fmt.Errorf("author: prototype %q: %w", p.Name, err)
	}
	var primitive gltf.Primitive
	if p.Material != "" {
		index, ok := materials[p.Material]
		if !ok {
			return gltf.Mesh{}, fmt.Errorf("author: prototype %q uses unknown material %q", p.Name, p.Material)
		}
		primitive.Material = gltf.Int(index)
	}
	meshglb.Append(&primitive, p.Mesh)
	return gltf.Mesh{Name: p.Name, Primitives: []gltf.Primitive{primitive}}, nil
}

func instanceNode(instance Instance, index int) (gltf.Node, error) {
	q, err := encodeRotation(instance.Rotation)
	if err != nil {
		return gltf.Node{}, err
	}
	node := gltf.Node{Name: instance.Name, Mesh: gltf.Int(index), Rotation: q}
	if instance.Translation != (mesh.Vec3{}) {
		t := instance.Translation.F32()
		node.Translation = &t
	}
	if instance.Scale != (mesh.Vec3{}) {
		s := instance.Scale.F32()
		for _, axis := range s {
			if axis == 0 {
				return gltf.Node{}, fmt.Errorf("scale axes must be nonzero at glTF precision")
			}
		}
		node.Scale = &s
	}
	return node, nil
}

func validName(name string) bool { return name != "" && utf8.ValidString(name) }

func factor(v float64) bool { return v >= 0 && v <= 1 }
