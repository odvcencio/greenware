package socket_test

import (
	"math"
	"strings"
	"testing"

	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/socket"
)

func TestSocketsComposeInitialFrame(t *testing.T) {
	const halfTurn = 0.7071067811865476
	doc := &gltf.Document{
		Scene:  gltf.Int(1),
		Scenes: []gltf.Scene{{Nodes: []int{4}}, {Nodes: []int{0}}},
		Nodes: []gltf.Node{
			{Children: []int{1, 3}, Translation: &[3]float32{10, 20, 30}, Rotation: &[4]float32{0, 0, halfTurn, halfTurn}, Scale: &[3]float32{2, 3, -4}},
			{Children: []int{2}, Translation: &[3]float32{1, 2, 3}},
			{Name: "socket.tip", Translation: &[3]float32{1, 0, 1}, Rotation: &[4]float32{0, 0, halfTurn, halfTurn},
				Extras: []byte(`{"greenware":{"format":"greenware.socket/1","name":"tip","kind":"ribbon","joint":"upright"}}`)},
			{Name: "socket.pin", Extras: []byte("12")},
			{Name: "socket.alternate"},
		},
	}
	b, err := gltf.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := socket.ReadGLB(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "pin" || got[0].Kind != "" {
		t.Fatalf("initial scene sockets = %+v", got)
	}
	tip, err := got.Find("tip")
	if err != nil {
		t.Fatal(err)
	}
	if tip.Node != 2 || tip.Kind != "ribbon" || tip.Joint != "upright" {
		t.Fatalf("tip = %+v", tip)
	}
	if delta := mesh.Sub(tip.Transform.T, mesh.Vec3{4, 24, 14}); mesh.Len(delta) > 1e-9 {
		t.Errorf("tip translation = %v", tip.Transform.T)
	}
	want := mesh.Mat3{{-3, 0, 0}, {0, -2, 0}, {0, 0, -4}}
	for i, row := range tip.Transform.M {
		for j, value := range row {
			if math.Abs(value-want[i][j]) > 1e-9 {
				t.Errorf("tip basis = %v, want %v", tip.Transform.M, want)
			}
		}
	}
	doc.Scene = nil
	first, err := socket.FromDocument(doc)
	if err != nil || len(first) != 1 || first[0].Name != "alternate" {
		t.Errorf("first scene = %+v (%v)", first, err)
	}
}

func TestSocketAmbiguityAndMalformedDocument(t *testing.T) {
	doc := &gltf.Document{
		Scenes: []gltf.Scene{{Nodes: []int{0, 1}}},
		Nodes:  []gltf.Node{{Name: "socket.grip"}, {Name: "socket.grip"}},
	}
	set, err := socket.FromDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := set.Find("grip"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("duplicate lookup = %v", err)
	}
	if _, err := set.Find("missing"); err == nil {
		t.Error("missing name accepted")
	}
	doc.Nodes[0].Extras = []byte(`{"greenware":{"format":"greenware.socket/1","name":"different"}}`)
	if _, err := socket.FromDocument(doc); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Errorf("metadata mismatch = %v", err)
	}
	doc.Nodes[0].Extras = nil
	doc.Nodes[0].Children = []int{0}
	if _, err := socket.FromDocument(doc); err == nil {
		t.Error("cycle accepted")
	}
	doc.Nodes[0].Children = []int{100}
	if _, err := socket.FromDocument(doc); err == nil {
		t.Error("invalid child accepted")
	}
}
