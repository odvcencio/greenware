# gltf

A small glTF 2.0 binary (GLB) codec in Go, standard library plus `m31labs.dev/greenware/det`.
`Encode` writes one canonical byte sequence for a document. `Decode` checks a file
and names the JSON path of the first fault.

```go
doc := &gltf.Document{
	Scenes: []gltf.Scene{{Nodes: []int{0}}},
	Nodes:  []gltf.Node{{Mesh: gltf.Int(0)}},
	Meshes: []gltf.Mesh{{Primitives: []gltf.Primitive{{
		Positions: []float32{0, 0, 0, 1, 0, 0, 0, 1, 0},
		Indices:   []uint32{0, 1, 2},
	}}}},
}
glb, err := gltf.Encode(doc) // same bytes on every run and every machine

back, err := gltf.Decode(glb)
// A bad file gives an error such as:
// gltf: meshes[0].primitives[0].indices: index 2 (value 7) is not below vertex count 3
```

## What it covers

- Nodes (translation, rotation, scale, children, mesh), meshes with POSITION, NORMAL,
  TEXCOORD_0 and indices, metallic-roughness materials, textures, images, samplers,
  and raw-JSON extras and extensions.
- Decode reads indices as ubyte, ushort or uint, and strided bufferViews. It turns a
  node `matrix` into translation, rotation and scale.
- Skins (JOINTS_0, WEIGHTS_0, inverse bind matrices), morph targets (POSITION and
  NORMAL deltas, mesh weights, target names), animations (translation, rotation,
  scale and weights; LINEAR, STEP and CUBICSPLINE), and material alpha mode,
  double-sided, emissive factor and texCoord.
- Sparse morph targets: set `MorphTarget.Sparse` (or call `Primitive.AutoSparse`) and
  POSITION and NORMAL deltas are written as sparse accessors that list only the
  vertices that move. Decode reads them back into the same dense arrays and keeps
  the flag. `SparseSaves` gives the byte counts so a caller can choose.
- Not covered: cameras, sparse accessors anywhere but morph targets, other vertex
  attributes, quantized animation output, external buffers. See the package comment for each choice.

## Canonical output

Fixed key order, no map iteration, one binary buffer, 4-byte alignment (JSON padded
with spaces, BIN with zeros), uint16 indices when the largest index is below 65535,
exact float32 min and max on POSITION, `-0` written as `0`, NaN and infinity
rejected. The writer does not merge duplicate vertices.

## Tests

    go test ./...
    go test -run xxx -fuzz FuzzDecode -fuzztime 30s .
    go test -update -run TestGolden .   # only after an intended format change

`testdata/triangle.glb`, `testdata/rigged.glb` and `testdata/rigged-sparse.glb` are the
golden files. Their SHA-256 values are in `gltf_test.go`, `rig_test.go` and
`sparse_test.go`. Rewrite them with
`go test -update -run 'TestGolden|TestRigGolden|TestSparseGolden' .`.
