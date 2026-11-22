package author_test

import (
	"bytes"
	"math"
	"testing"

	"m31labs.dev/greenware/author"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/build"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/kernel/rig"
	"m31labs.dev/greenware/socket"
)

func TestAssemblyPreservesPackingAndSharesPrototypes(t *testing.T) {
	block, err := build.Box(build.BoxParams{Size: mesh.Vec3{1, 1, 1}})
	if err != nil {
		t.Fatal(err)
	}
	asset := author.Assembly{
		Name:       "kit",
		Prototypes: []author.Prototype{{Name: "block", Mesh: block}},
		Instances:  []author.Instance{{Name: "block", Prototype: "block"}},
	}
	fromAssembly := func() []byte {
		t.Helper()
		b, err := author.BuildAssembly(asset)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	legacy, err := author.Build(author.Asset{Name: "kit", Parts: []author.Part{{Name: "block", Mesh: block}}})
	if err != nil || !bytes.Equal(legacy, fromAssembly()) {
		t.Fatalf("equivalent flat build differs: %v", err)
	}
	asset.Prototypes = append(asset.Prototypes, author.Prototype{Name: "unused", Mesh: block})
	asset.Instances = append(asset.Instances, author.Instance{
		Name: "turned", Prototype: "block", Translation: mesh.Vec3{10, 20, 30},
		Rotation: rig.QAxis(mesh.Vec3{0, 1, 0}, math.Pi/2), Scale: mesh.Vec3{2, 3, 4},
	})
	asset.Sockets = []author.Socket{{Name: "edge", Parent: "turned", Translation: mesh.Vec3{0, 1, 1}}}
	b := fromAssembly()
	asset.Prototypes[0], asset.Prototypes[1] = asset.Prototypes[1], asset.Prototypes[0]
	if !bytes.Equal(b, fromAssembly()) {
		t.Fatal("prototype declaration order changed the GLB")
	}
	doc, err := gltf.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Meshes) != 1 || *doc.Nodes[1].Mesh != *doc.Nodes[2].Mesh {
		t.Fatal("placement duplicated the shared prototype")
	}
	set, err := socket.FromDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := set.Find("edge")
	if err != nil || mesh.Len(mesh.Sub(frame.Transform.T, mesh.Vec3{14, 23, 30})) > 1e-8 {
		t.Fatalf("socket missed the rotated and scaled instance: %+v (%v)", frame, err)
	}
	asset.Instances[1].Scale = mesh.Vec3{1, 0, 1}
	if data, err := author.BuildAssembly(asset); err == nil || data != nil {
		t.Fatal("singular instance scale was accepted")
	}
}
