package glb_test

import (
	"testing"

	"m31labs.dev/greenware/conformance/glb"
	"m31labs.dev/greenware/conformance/internal/fixture"
)

// FuzzDecode feeds arbitrary bytes to the decoder. It fails on any internal
// fault (a recovered panic) and exercises every accessor read.
func FuzzDecode(f *testing.F) {
	f.Add(cubeFile())
	b := fixture.New()
	m := b.Mesh("cyl", fixture.Cylinder(0, 0, 1, 0, 2, 12, 0, true), fixture.Sphere(0, 3, 0, 1, 8, 4))
	b.Scene(b.Node("a", m, [3]float64{1, 2, 3}))
	f.Add(b.Bytes())
	f.Add([]byte("glTF"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		file, fs := glb.Decode(data)
		for _, x := range fs {
			if x.Code == "ORACLE-INTERNAL" {
				t.Fatalf("internal fault: %s", x.Message)
			}
		}
		if file != nil && file.Doc != nil {
			for i := range file.Doc.Accessors {
				file.Floats(i)
				file.Indices(i)
			}
		}
	})
}
