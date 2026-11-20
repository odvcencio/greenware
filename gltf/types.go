package gltf

import (
	"encoding/json"
	"fmt"
)

// Int returns a pointer to v, for the optional index fields.
func Int(v int) *int { return &v }

// Document is a glTF 2.0 asset reduced to what a mesh pipeline needs.
type Document struct {
	Asset     Asset
	Scene     *int // default scene, an index into Scenes
	Scenes    []Scene
	Nodes     []Node
	Meshes    []Mesh
	Materials []Material
	Textures  []Texture
	Images    []Image
	Samplers  []Sampler
	// Extras is raw JSON for the top-level "extras" member, or nil.
	Extras json.RawMessage
}

// Asset is the "asset" object. The version is always "2.0" and is not stored.
type Asset struct {
	Generator string
	Copyright string
}

// Scene lists the root nodes of one scene.
type Scene struct {
	Name   string
	Nodes  []int
	Extras json.RawMessage
}

// Node is one scene-graph node. Translation, Rotation (x y z w quaternion) and
// Scale are nil when the node does not set them.
type Node struct {
	Name        string
	Mesh        *int
	Children    []int
	Translation *[3]float32
	Rotation    *[4]float32
	Scale       *[3]float32
	Extras      json.RawMessage
	// Extensions maps an extension name to its raw JSON value.
	Extensions map[string]json.RawMessage
}

// Mesh is a list of primitives.
type Mesh struct {
	Name       string
	Primitives []Primitive
	Extras     json.RawMessage
}

// Primitive is one triangle list. Positions and Normals hold xyz triples and
// UV0 holds uv pairs. Normals and UV0 are optional; when set they cover the
// same vertices as Positions. Indices may be empty (the vertices are then
// read in order).
type Primitive struct {
	Positions []float32
	Normals   []float32
	UV0       []float32
	Indices   []uint32
	Material  *int
	Extras    json.RawMessage
}

// Material is a metallic-roughness material.
type Material struct {
	Name      string
	BaseColor [4]float32
	Metallic  float32
	Roughness float32
	// BaseColorTexture is an index into Document.Textures, or nil.
	BaseColorTexture *int
	Extras           json.RawMessage
	Extensions       map[string]json.RawMessage
}

// Texture pairs an image with a sampler.
type Texture struct {
	Name    string
	Sampler *int
	Source  *int
}

// Image is either embedded bytes (Data plus MimeType, stored in the binary
// buffer) or a URI. Exactly one of Data and URI is set.
type Image struct {
	Name     string
	MimeType string
	Data     []byte
	URI      string
}

// Sampler holds OpenGL enum values. Zero means "not set".
type Sampler struct {
	MagFilter int
	MinFilter int
	WrapS     int
	WrapT     int
}

// Error reports a fault and the JSON path where it was found.
type Error struct {
	Path string
	Msg  string
}

func (e *Error) Error() string { return "gltf: " + e.Path + ": " + e.Msg }

func errf(path, format string, args ...any) *Error {
	return &Error{Path: path, Msg: fmt.Sprintf(format, args...)}
}
