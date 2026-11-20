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
	f.Add(fixture.RichRigSparse())
	sb := fixture.New()
	sb.Sparse(fixture.SparseOpts{Count: 5, Base: make([]float32, 15), Indices: []uint32{0, 2, 4}, Values: []float32{1, 2, 3, 4, 5, 6, 7, 8, 9}, IndexType: 5125, MinMax: true})
	sb.Sparse(fixture.SparseOpts{Type: "SCALAR", Count: 4, Indices: []uint32{3, 1}, Values: []float32{1, 2}, IndexType: 5121})
	f.Add(sb.Bytes())
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

// FuzzSparseBin keeps the JSON of a valid file with sparse accessors (a rig with
// a sparse morph target, and a file of sparse accessors with indices of each
// width) and fills its binary chunk with fuzz bytes: the indices take any
// value, in or out of order, and the values take any bit pattern. The decoder
// must not fault, and every accessor that reads must have finite indices
// inside its element count.
func FuzzSparseBin(f *testing.F) {
	sb := fixture.New()
	for _, ct := range []int{5121, 5123, 5125} {
		sb.Sparse(fixture.SparseOpts{Count: 6, Base: make([]float32, 18), Indices: []uint32{1, 3, 5}, Values: make([]float32, 9), IndexType: ct})
	}
	seeds := [][]byte{fixture.RichRigSparse(), sb.Bytes()}
	for i, seed := range seeds {
		off, n, _ := fixture.BinRange(seed)
		f.Add(seed[off:off+n], i)
	}
	f.Fuzz(func(t *testing.T, bin []byte, which int) {
		seed := seeds[((which%len(seeds))+len(seeds))%len(seeds)]
		off, n, _ := fixture.BinRange(seed)
		data := append([]byte(nil), seed...)
		copy(data[off:off+n], bin)
		file, fs := glb.Decode(data)
		for _, x := range fs {
			if x.Code == "ORACLE-INTERNAL" {
				t.Fatalf("internal fault: %s", x.Message)
			}
		}
		if file != nil && file.Doc != nil {
			for i, a := range file.Doc.Accessors {
				v, nc, ok := file.Floats(i)
				if ok && len(v) != a.Count*nc {
					t.Fatalf("accessor %d reads %d values for %d elements of %d components", i, len(v), a.Count, nc)
				}
			}
		}
	})
}
