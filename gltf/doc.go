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
//     nodes, meshes, materials, textures, images, samplers, buffers,
//     bufferViews, accessors, extras. Fields inside each object also have a
//     fixed order. Map keys (node and material extensions) are sorted.
//   - Extras are re-written as compact JSON with sorted object keys. Numbers
//     keep their written form.
//   - One binary buffer. Per primitive the data comes in this order, each in
//     its own bufferView aligned to 4 bytes: POSITION, NORMAL, TEXCOORD_0,
//     indices. Image bytes follow, in image order. Accessor i is the i-th
//     geometry block in that order.
//   - The JSON chunk is padded with spaces, the BIN chunk with zeros.
//   - Indices are uint16 when the largest index is below 65535, else uint32
//     (65535 is the primitive restart value, which glTF forbids in uint16
//     data).
//   - POSITION accessors carry exact float32 min and max.
//   - Floats are float32, little-endian. Negative zero is written as zero.
//     NaN and infinity are errors.
//   - extensionsUsed is the sorted set of extension names on nodes and
//     materials. The writer does not deduplicate vertices.
//   - Material pbrMetallicRoughness always lists baseColorFactor,
//     metallicFactor and roughnessFactor, so zero values are exact.
//
// # What Decode reads
//
//   - POSITION, NORMAL and TEXCOORD_0 (float32 only), indices of any
//     component type (ubyte, ushort, uint) and strided bufferViews.
//   - Only mode 4 (triangles). Other attributes, morph targets, skins,
//     cameras, animations, sparse accessors and external buffers are not read;
//     sparse accessors and external buffers that the codec needs are errors,
//     the rest is dropped silently.
//   - Node matrices are decomposed into Translation, Rotation and Scale. A
//     matrix with shear, a zero scale, or a last row other than 0 0 0 1 is an
//     error. A node with both a matrix and T/R/S is an error.
//   - Extras and extensions are kept as raw JSON, canonicalised as above.
//   - Absent materials factors take the glTF defaults (all 1). Empty lists
//     decode as nil, and Encode treats an empty list like a nil one.
//   - Names with invalid UTF-8 are written with U+FFFD in their place.
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
