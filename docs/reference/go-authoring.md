# Build a rigid asset from Go

`m31labs.dev/greenware/author` accepts the same `mesh.Mesh` type that Greenware's shape builders return. Call `author.Build` to produce a self-contained GLB. The kernel and compiler use metres. Coordinates are right-handed with Y up.

Pin the Greenware module to a commit in your application's `go.mod`. This program builds a blue block and writes it to `block.glb`.

```go
package main

import (
	"image/color"
	"log"
	"os"

	"m31labs.dev/greenware/author"
	"m31labs.dev/greenware/kernel/build"
	"m31labs.dev/greenware/kernel/mesh"
)

func main() {
	block, err := build.Box(build.BoxParams{
		Size: mesh.Vec3{0.4, 0.2, 0.3},
	})
	if err != nil {
		log.Fatal(err)
	}
	glb, err := author.Build(author.Asset{
		Name: "block",
		Parts: []author.Part{{
			Name: "body", Mesh: block, Material: "blue",
			Translation: mesh.Vec3{0, 0.1, 0},
		}},
		Materials: []author.Material{{
			Name: "blue", Base: color.NRGBA{R: 44, G: 85, B: 166, A: 255},
			Metallic: 0.1, Roughness: 0.55,
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("block.glb", glb, 0o644); err != nil {
		log.Fatal(err)
	}
}
```

The API writes one rigid scene. Parts are placed in slice order, directly below the named root. Material declaration order does not affect the output. The encoder preserves hard edges and UV seams and validates the mesh before packing. Each build accepts up to 4,096 parts and materials and a combined 1,048,576 positions, corners and triangles. Supply non-premultiplied sRGB bytes for the base colour and an explicit alpha. Set `Blend` when alpha should affect rendering. Metallic and roughness are linear factors in `[0, 1]`.

This API covers rigid meshes and portable PBR factors. For source-authored levels, textures, skins, clips, and build receipts, use the `.gw` compiler.

## Publish and read attachment frames

Use `author.BuildWithSockets` with a slice of `author.Socket` values to attach an anchor to the scene root or to a named part. A socket's translation is in metres relative to its parent. Its rotation is a quaternion in glTF order `(x, y, z, w)`. The zero-value rotation uses identity, and an empty kind defaults to `anchor`. Builds allow up to 4,096 sockets with unique names.

```go
glb, err := author.BuildWithSockets(asset, []author.Socket{
    {Name: "ribbon_l", Kind: "ribbon", Parent: "body", Translation: mesh.Vec3{0, 1, 0}},
})
```

Consumers import `m31labs.dev/greenware/socket`. The same API works with Go-authored and source-authored GLBs.

```go
sockets, err := socket.ReadGLB(glb)
// Handle err before looking up an attachment.
ribbon, err := sockets.Find("ribbon_l")
// Handle err before using ribbon.Transform.
```

Use `socket.FromDocument` when the compiler or decoder has already returned a `gltf.Document`. Each socket gives its node index, kind and optional authored joint name. `Transform.T` is the model-space origin and `Transform.M` is its row-major linear transform. Map a local point `p` with `mesh.Add(frame.M.MulVec(p), frame.T)`. Keep the full linear transform when ancestors have nonuniform scale.

The reader uses the initial default scene and falls back to the first scene when the default is unspecified. It reads the initial node transforms on rigs. A caller animating a rig can use the node index to track the attachment in its renderer. Use the node index to distinguish repeated names. `Find` reports a missing or ambiguous name as an error. Legacy meshless `socket.<name>` nodes have an empty kind and joint.

## Check decoded material factors

A `.gw` acceptance file can measure a visible node with one assigned material. `material(node).base` converts the stored linear factor back to opaque sRGB. `.alpha`, `.metallic` and `.roughness` report the stored linear factors. Compare fractional values with an appropriate tolerance for glTF's binary32 representation.

```text
expect (material(body).base == #2c55a6, "the body is blue")
expect (abs(material(body).roughness - 0.55) < 0.00001, "the surface roughness")
```

The query measures the node's own primitives. Hidden nodes, unassigned materials and mixed materials refuse the measurement. The factor check does not sample textures or assess lighting.
