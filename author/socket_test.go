package author_test

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"m31labs.dev/greenware/author"
	"m31labs.dev/greenware/kernel/build"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/kernel/rig"
	"m31labs.dev/greenware/socket"
)

func TestAuthorSocketRoundTrip(t *testing.T) {
	body, err := build.Box(build.BoxParams{Size: mesh.Vec3{1, 1, 1}})
	if err != nil {
		t.Fatal(err)
	}
	asset := author.Asset{Name: "stand", Parts: []author.Part{
		{Name: "body", Mesh: body, Translation: mesh.Vec3{4, 5, 6}},
	}}
	s := author.Socket{
		Name: "tip", Parent: "body", Kind: "flag", Translation: mesh.Vec3{1, 2, 3},
		Rotation: rig.Quat{0, 0, 1e308, 1e308},
	}
	original, err := author.Build(asset)
	if err != nil {
		t.Fatal(err)
	}
	without, err := author.BuildWithSockets(asset, nil)
	if err != nil || !bytes.Equal(original, without) {
		t.Fatalf("socket-free output differs: %v", err)
	}
	encoded, err := author.BuildWithSockets(asset, []author.Socket{s, {Name: "floor"}})
	if err != nil {
		t.Fatal(err)
	}
	found, err := socket.ReadGLB(encoded)
	if err != nil {
		t.Fatal(err)
	}
	floor, err := found.Find("floor")
	if err != nil || floor.Kind != "anchor" || floor.Transform != mesh.IdentityAffine {
		t.Fatalf("root frame = %+v (%v)", floor, err)
	}
	got, err := found.Find("tip")
	if err != nil || got.Kind != "flag" || got.Transform.T != (mesh.Vec3{5, 7, 9}) {
		t.Fatalf("part frame = %+v (%v)", got, err)
	}
	if xAxis := got.Transform.M.MulVec(mesh.Vec3{1, 0, 0}); mesh.Len(mesh.Sub(xAxis, mesh.Vec3{0, 1, 0})) > 1e-9 {
		t.Errorf("socket X axis = %v", xAxis)
	}
	for _, bad := range []struct {
		sockets []author.Socket
		text    string
	}{
		{[]author.Socket{s, s}, "already used"},
		{[]author.Socket{{Name: "tip", Parent: "unknown"}}, "unknown parent"},
		{[]author.Socket{{Name: "tip", Rotation: rig.Quat{0, 0, math.NaN(), 1}}}, "must be finite"},
	} {
		if encoded, err := author.BuildWithSockets(asset, bad.sockets); encoded != nil || err == nil || !strings.Contains(err.Error(), bad.text) {
			t.Errorf("invalid socket: %d bytes, %v; want %q", len(encoded), err, bad.text)
		}
	}
}
