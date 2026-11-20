// Package gltf reads and writes glTF 2.0 binary (GLB) files. It knows nothing
// about Greenware, GoSX or Kiln, and it imports only m31labs.dev/greenware/det.
//
// The package is a codec. Encode turns a Document into canonical GLB bytes;
// Decode checks GLB bytes and returns the Document.
//
// # API
//
//	func Encode(doc *Document) ([]byte, error)
//	func Decode(b []byte) (*Document, error)
//	func (d *Document) Validate() error
//
// Errors are *Error values. Path names the JSON location, for example
// "meshes[0].primitives[0].indices"; header and chunk faults use the path
// "glb". Decode never panics, whatever the input.
//
// # What Encode writes (canonical form)
//
// The same Document always gives the same bytes:
//
//   - JSON keys come in one fixed order: asset, extensionsUsed, scene, scenes,
//     nodes, meshes, materials, textures, images, samplers, skins, animations,
//     buffers, bufferViews, accessors, extras. Fields inside each object also have a
//     fixed order. Map keys (node and material extensions) are sorted.
//   - Extras are re-written as compact JSON with sorted object keys. Numbers
//     keep their written form.
//   - One binary buffer. Per primitive the data comes in this order, each in
//     its own bufferView aligned to 4 bytes: POSITION, NORMAL, TEXCOORD_0,
//     indices, JOINTS_0, WEIGHTS_0, then each morph target (POSITION, then
//     NORMAL). Image bytes follow, in image order. Then come the inverse bind
//     matrices of each skin that has them, in skin order, and the input and
//     output of each animation sampler, in animation and sampler order.
//     Accessor i is the i-th block in that order (images are bufferViews
//     only). Skin and animation views carry no target.
//   - The JSON chunk is padded with spaces, the BIN chunk with zeros.
//   - Indices are uint16 when the largest index is below 65535, else uint32
//     (65535 is the primitive restart value, which glTF forbids in uint16
//     data).
//   - POSITION accessors carry exact float32 min and max.
//   - Floats are float32, little-endian. Negative zero is written as zero.
//     NaN and infinity are errors.
//   - extensionsUsed is the sorted set of extension names on nodes and
//     materials. The writer does not deduplicate vertices.
//   - JOINTS_0 is unsigned byte when every joint index of the primitive is
//     255 or below, else unsigned short. WEIGHTS_0 is float32.
//   - Morph target POSITION deltas carry exact float32 min and max, like
//     POSITION. Each animation sampler input carries its exact min and max
//     (glTF requires them). Interpolation is always written; "" becomes
//     "LINEAR".
//   - Material fields at their defaults are not written: emissiveFactor,
//     alphaMode OPAQUE, alphaCutoff (written for MASK only), doubleSided,
//     texCoord 0. Mesh TargetNames are merged into the mesh extras as
//     "targetNames".
//   - Material pbrMetallicRoughness always lists baseColorFactor,
//     metallicFactor and roughnessFactor, so zero values are exact.
//
// # What Decode reads
//
//   - POSITION, NORMAL and TEXCOORD_0 (float32 only), indices of any
//     component type (ubyte, ushort, uint) and strided bufferViews.
//   - Only mode 4 (triangles). JOINTS_0 (ubyte or ushort) and WEIGHTS_0
//     (float, or normalized ubyte or ushort) are read; integer weights are
//     divided by their per-vertex integer sum, so they add to 1 despite the
//     rounding that glTF allows. JOINTS_1, WEIGHTS_1, TANGENT, COLOR_0 and
//     other attributes are dropped silently.
//   - Morph targets (POSITION and NORMAL deltas, float32), mesh weights, a
//     "targetNames" extras member of the right length (moved to
//     Mesh.TargetNames), skins, and animations (float32 times and values;
//     translation, rotation, scale and weights paths).
//   - Sparse accessors, quantized animation output, external buffers and
//     cameras are not read; sparse accessors and external buffers that the
//     codec needs are errors, cameras are dropped silently.
//   - Node matrices are decomposed into Translation, Rotation and Scale. A
//     matrix with shear, a zero scale, or a last row other than 0 0 0 1 is an
//     error. A node with both a matrix and T/R/S is an error.
//   - Extras and extensions are kept as raw JSON, canonicalised as above.
//   - Absent alphaMode decodes as "" (OPAQUE); MASK without a cutoff decodes
//     with AlphaCutoff 0.5. An absent interpolation decodes as "LINEAR".
//   - Absent materials factors take the glTF defaults (all 1). Empty lists
//     decode as nil, and Encode treats an empty list like a nil one.
//   - Names with invalid UTF-8 are written with U+FFFD in their place.
//
// # Rig checks
//
// Validate (and so Encode and Decode) rejects: weights that are not finite,
// are negative, or sum more than 1e-5 from 1 (a vertex whose four weights
// are all zero is allowed); JOINTS_0 without WEIGHTS_0, or counts other than
// 4 per vertex; a joint index at or above the joint count of a skin that a
// node uses with the mesh; a node with a skin but no mesh; repeated or
// out-of-range skin joints; morph target arrays that differ in length from
// POSITION; primitives of one mesh with different target counts; mesh weights
// or names that differ in count from the targets; animation times that are
// not finite, negative, or not strictly increasing; output lengths that do
// not match path, key count and interpolation (CUBICSPLINE has three values
// per key and needs two keys); two channels on the same node and path; a
// sampler that no channel uses, or that channels use with different value
// sizes; and a weights channel on a node whose mesh has no morph targets.
//
// Example:
//
//	doc := &gltf.Document{
//		Scenes: []gltf.Scene{{Nodes: []int{0}}},
//		Nodes:  []gltf.Node{{Mesh: gltf.Int(0)}},
//		Meshes: []gltf.Mesh{{Primitives: []gltf.Primitive{{
//			Positions: []float32{0, 0, 0, 1, 0, 0, 0, 1, 0},
//			Indices:   []uint32{0, 1, 2},
//		}}}},
//	}
//	glb, err := gltf.Encode(doc)
package gltf
