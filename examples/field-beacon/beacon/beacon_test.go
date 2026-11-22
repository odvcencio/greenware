package beacon

import (
	"bytes"
	"testing"

	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/socket"
)

func TestRecipeControlsGeometryAndSharesMeshes(t *testing.T) {
	encode := func(r Recipe) []byte {
		t.Helper()
		b, err := Build(r)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	r := Recipe{Seed: 3, Tiers: 3, Panels: 5}
	large := encode(r)
	if !bytes.Equal(large, encode(r)) {
		t.Fatal("an identical recipe produced different bytes")
	}
	r.Seed++
	if bytes.Equal(large, encode(r)) {
		t.Fatal("changing seed produced the same bytes")
	}
	for _, tc := range []struct {
		data         []byte
		instances    int
		payloadCount int
	}{
		{large, 52, 15}, {encode(Recipe{Seed: 2, Tiers: 1, Panels: 2}), 11, 2},
	} {
		doc, err := gltf.Decode(tc.data)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(doc.Meshes); got != 7 {
			t.Fatalf("got %d meshes, want seven shared prototypes", got)
		}
		if got := len(doc.Nodes[0].Children); got != tc.instances+1 {
			t.Fatalf("root has %d children, want %d", got, tc.instances+1)
		}
		frames, err := socket.FromDocument(doc)
		if err != nil || len(frames) != tc.payloadCount+2 {
			t.Fatalf("sockets = %v (%v)", frames, err)
		}
		s, err := frames.Find("payload_0_0")
		if err != nil || s.Transform.T[1] < .38 || s.Kind != "panel" {
			t.Fatalf("payload socket = %+v (%v)", s, err)
		}
	}
	if data, err := Build(Recipe{Tiers: 6, Panels: 4}); err == nil || data != nil {
		t.Fatal("unsupported recipe produced an asset")
	}
}
