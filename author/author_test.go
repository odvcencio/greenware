package author_test

import (
	"bytes"
	"image/color"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/author"
	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/build"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/lang"
)

func TestGoAssetMatchesCompiledSurface(t *testing.T) {
	surface, err := build.Box(build.BoxParams{Size: mesh.Vec3{1, 2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	material := author.Material{
		Name: "paint", Base: color.NRGBA{R: 128, G: 44, B: 18, A: 255},
		Metallic: 0.25, Roughness: 0.5,
	}
	asset := author.Asset{
		Name: "crate", Parts: []author.Part{{Name: "body", Mesh: surface, Material: "paint"}},
		Materials: []author.Material{material},
	}
	first, err := author.Build(asset)
	if err != nil {
		t.Fatal(err)
	}
	second, err := author.Build(asset)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("repeated Go build differs: %v", err)
	}
	doc, err := gltf.Decode(first)
	if err != nil {
		t.Fatal(err)
	}
	f, ds := ast.Lower(lang.Parse([]byte(`greenware 1
asset crate { root = crate }
material paint = pbr { base = #802c12 metallic = 0.25 roughness = 0.5 }
part crate {
  part body {
    export = on
    material = paint
    box { size = (1m, 2m, 3m) }
  }
}
output main { file = "crate.glb" }
`)))
	if len(ds) != 0 {
		t.Fatalf("lower: %v", ds)
	}
	product, ds := compiler.Build(f, "")
	if len(ds) != 0 || product == nil {
		t.Fatalf("make .gw cube: %v", ds)
	}
	language, err := gltf.Decode(product.Outputs[0].Files[0].GLB)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Meshes) != 1 || !reflect.DeepEqual(doc.Meshes, language.Meshes) {
		t.Fatal("the Go and .gw paths produced different primitives")
	}
	if !reflect.DeepEqual(doc.Materials, language.Materials) {
		t.Fatalf("the Go and .gw paths produced different PBR factors: %v / %v", doc.Materials, language.Materials)
	}
	broken := surface.Clone()
	broken.Tris[0][0] = int32(len(broken.Corners))
	asset.Parts[0].Mesh = broken
	if out, err := author.Build(asset); out != nil || err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("invalid corner index reached output: %d bytes, %v", len(out), err)
	}
}
