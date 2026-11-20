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
	f.Add(fixture.RichRig())
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

// FuzzRigBin keeps the JSON of a valid rig and replaces its binary chunk with
// fuzz bytes, so the weights, joint indices, times, quaternions and inverse
// bind matrices take arbitrary values behind a well-formed document. The
// decoder must not fault.
func FuzzRigBin(f *testing.F) {
	seed := fixture.RichRig()
	off, n, _ := fixture.BinRange(seed)
	f.Add(seed[off : off+n])
	f.Add(make([]byte, n))
	f.Add(append([]byte(nil), seed[off:off+n/2]...))
	f.Fuzz(func(t *testing.T, bin []byte) {
		data := append([]byte(nil), seed...)
		copy(data[off:off+n], bin)
		file, fs := glb.Decode(data)
		for _, x := range fs {
			if x.Code == "ORACLE-INTERNAL" {
				t.Fatalf("internal fault: %s", x.Message)
			}
		}
		if file != nil && file.Doc != nil {
			for i := range file.Doc.Accessors {
				file.Floats(i)
			}
		}
	})
}
