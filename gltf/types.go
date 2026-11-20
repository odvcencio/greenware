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
	// Skins and Animations are optional; see Skin and Animation.
	Skins      []Skin
	Animations []Animation
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
	Name string
	Mesh *int
	// Skin is an index into Document.Skins, or nil. A node with a skin must
	// also have a mesh.
	Skin        *int
	Children    []int
	Translation *[3]float32
	Rotation    *[4]float32
	Scale       *[3]float32
	Extras      json.RawMessage
	// Extensions maps an extension name to its raw JSON value.
	Extensions map[string]json.RawMessage
}

// Mesh is a list of primitives.
//
// Weights are the default morph target weights; they are empty or have one
// value per morph target. TargetNames are the target names, empty or one per
// target. They travel in the mesh extras as "targetNames", the glTF
// convention, so Extras must be a JSON object (or empty) when TargetNames is
// set and must not already hold a "targetNames" member.
type Mesh struct {
	Name        string
	Primitives  []Primitive
	Weights     []float32
	TargetNames []string
	Extras      json.RawMessage
}

// Primitive is one triangle list. Positions and Normals hold xyz triples and
// UV0 holds uv pairs. Normals and UV0 are optional; when set they cover the
// same vertices as Positions. Indices may be empty (the vertices are then
// read in order).
//
// Joints0 and Weights0 are the skin attributes JOINTS_0 and WEIGHTS_0, four
// values per vertex. They are both empty or both set. Targets are the morph
// targets; every primitive of a mesh has the same number of them.
type Primitive struct {
	Positions []float32
	Normals   []float32
	UV0       []float32
	Indices   []uint32
	Joints0   []uint16
	Weights0  []float32
	Targets   []MorphTarget
	Material  *int
	Extras    json.RawMessage
}

// MorphTarget holds per-vertex deltas added to the base attributes. Each
// slice is empty or has the same length as the base Positions.
//
// Sparse asks Encode to write the POSITION and NORMAL deltas as glTF sparse
// accessors, which store only the vertices that move. Decode sets it when a
// target's accessor was sparse. It changes the bytes written, not the
// meaning: Positions and Normals are always the full dense arrays. See
// SparseSaves and Primitive.AutoSparse.
type MorphTarget struct {
	Positions []float32
	Normals   []float32
	Sparse    bool
}

// Skin binds joint nodes to the meshes that use it. InverseBindMatrices is
// empty (identity matrices) or has one column-major matrix per joint.
type Skin struct {
	Name                string
	Joints              []int // node indices; at least one, no repeats
	InverseBindMatrices [][16]float32
	Skeleton            *int // node index, or nil
}

// Path names the node property an animation channel drives.
const (
	PathTranslation = "translation"
	PathRotation    = "rotation"
	PathScale       = "scale"
	PathWeights     = "weights"
)

// Interpolation names how a sampler blends between keyframes.
const (
	InterpLinear      = "LINEAR"
	InterpStep        = "STEP"
	InterpCubicSpline = "CUBICSPLINE"
)

// Animation is a set of channels that drive node properties from samplers.
// Every sampler must be used by at least one channel.
type Animation struct {
	Name     string
	Channels []Channel
	Samplers []AnimSampler
}

// Channel connects Samplers[Sampler] to one property of Nodes[Node].
type Channel struct {
	Node    int
	Path    string // PathTranslation, PathRotation, PathScale or PathWeights
	Sampler int
}

// AnimSampler holds keyframe times (seconds) and the values at each time.
// Output is flat: 3 floats per key for translation and scale, 4 for rotation,
// one per morph target for weights. CUBICSPLINE writes three such groups per
// key (in-tangent, value, out-tangent). Interpolation "" means LINEAR and
// decodes as "LINEAR".
type AnimSampler struct {
	Input         []float32
	Output        []float32
	Interpolation string
}

// Material is a metallic-roughness material.
type Material struct {
	Name      string
	BaseColor [4]float32
	Metallic  float32
	Roughness float32
	// BaseColorTexture is an index into Document.Textures, or nil.
	BaseColorTexture *int
	// BaseColorTexCoord is the texCoord set of the base color texture; 0 is
	// the default and is not written.
	BaseColorTexCoord int
	// EmissiveFactor is linear RGB; zero (the default) is not written.
	EmissiveFactor [3]float32
	// AlphaMode is "" or "OPAQUE" (the default, not written), "MASK" or
	// "BLEND". It decodes as "" for OPAQUE. AlphaCutoff is written for MASK
	// only (an absent cutoff decodes as 0.5) and must be 0 for other modes.
	AlphaMode   string
	AlphaCutoff float32
	DoubleSided bool
	Extras      json.RawMessage
	Extensions  map[string]json.RawMessage
}

// Alpha modes for Material.AlphaMode.
const (
	AlphaOpaque = "OPAQUE"
	AlphaMask   = "MASK"
	AlphaBlend  = "BLEND"
)

// Texture pairs an image with a sampler.
type Texture struct {
	Name    string
	Sampler *int
	Source  *int
	Extras  json.RawMessage
}

// Image is either embedded bytes (Data plus MimeType, stored in the binary
// buffer) or a URI. Exactly one of Data and URI is set.
type Image struct {
	Name     string
	MimeType string
	Data     []byte
	URI      string
	Extras   json.RawMessage
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
