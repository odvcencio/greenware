package compiler

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"

	"m31labs.dev/greenware/gltf"
)

// The output field `grid = n` snaps every number written to the GLB to a
// multiple of 2^-n, so two builds on different machines (or a build and a
// tool that rounds the same way) write the same bytes. The rule has two
// steps, because the encoder derives some numbers itself:
//
//  1. snapDoc, before encoding: mesh positions, UVs, skin weights and morph
//     deltas. The encoder measures bounds and chooses sparse entries from
//     these, so they must be snapped first.
//  2. snapGLB, after encoding: every dense float accessor with no bounds and
//     no sparse part (normals, clip keys, inverse bind matrices). Accessors
//     with bounds keep them; their data was snapped in step 1 or is a clip
//     time that the bounds already describe.

// snapTo rounds v to a multiple of 2^-n. Scaling by a power of two is exact,
// so the rounding is the same on every machine. Zero is always +0.
func snapTo(v float64, n int) float64 {
	scale := math.Ldexp(1, n)
	r := math.Round(v*scale) / scale
	if r == 0 {
		return 0
	}
	return r
}

func snap32(f float32, n int) float32 { return float32(snapTo(float64(f), n)) }

func snapAll(fs []float32, n int) {
	for i, f := range fs {
		fs[i] = snap32(f, n)
	}
}

// snapDoc snaps the mesh inputs of doc in place. After snapping, the largest
// weight of each vertex takes up the remainder so the four still sum to one.
func snapDoc(doc *gltf.Document, n int) {
	for mi := range doc.Meshes {
		for pi := range doc.Meshes[mi].Primitives {
			p := &doc.Meshes[mi].Primitives[pi]
			snapAll(p.Positions, n)
			snapAll(p.UV0, n)
			snapWeights(p.Weights0, n)
			for ti := range p.Targets {
				snapAll(p.Targets[ti].Positions, n)
				snapAll(p.Targets[ti].Normals, n)
			}
		}
	}
}

func snapWeights(w []float32, n int) {
	for i := 0; i+4 <= len(w); i += 4 {
		big, rest := 0, 0.0
		var v [4]float64
		for k := 0; k < 4; k++ {
			v[k] = snapTo(float64(w[i+k]), n)
			if v[k] > v[big] {
				big = k
			}
		}
		for k := 0; k < 4; k++ {
			if k != big {
				rest += v[k]
			}
		}
		v[big] = 1 - rest
		for k := 0; k < 4; k++ {
			w[i+k] = float32(v[k])
		}
	}
}

// snapGLB snaps, in a written GLB's binary chunk, every dense float accessor
// without bounds and without a sparse part. It changes values in place,
// never sizes or the JSON.
func snapGLB(glb []byte, n int) ([]byte, error) {
	if len(glb) < 20 {
		return nil, fmt.Errorf("GLB of %d bytes", len(glb))
	}
	jsonLen := int(binary.LittleEndian.Uint32(glb[12:]))
	js := 20
	bin := js + jsonLen + 8
	if bin > len(glb) {
		return nil, fmt.Errorf("GLB has no binary chunk")
	}
	var d struct {
		Accessors []struct {
			BufferView    *int            `json:"bufferView"`
			ByteOffset    int             `json:"byteOffset"`
			ComponentType int             `json:"componentType"`
			Count         int             `json:"count"`
			Type          string          `json:"type"`
			Min           json.RawMessage `json:"min"`
			Max           json.RawMessage `json:"max"`
			Sparse        json.RawMessage `json:"sparse"`
		} `json:"accessors"`
		BufferViews []struct {
			Buffer     int `json:"buffer"`
			ByteOffset int `json:"byteOffset"`
			ByteStride int `json:"byteStride"`
		} `json:"bufferViews"`
	}
	if err := json.Unmarshal(glb[js:js+jsonLen], &d); err != nil {
		return nil, err
	}
	comps := map[string]int{"SCALAR": 1, "VEC2": 2, "VEC3": 3, "VEC4": 4, "MAT2": 4, "MAT3": 9, "MAT4": 16}
	const componentFloat = 5126
	out := append([]byte(nil), glb...)
	for _, a := range d.Accessors {
		if a.ComponentType != componentFloat || a.Sparse != nil || a.BufferView == nil || a.Min != nil || a.Max != nil {
			continue
		}
		v := d.BufferViews[*a.BufferView]
		nc, ok := comps[a.Type]
		if !ok {
			return nil, fmt.Errorf("accessor type %q", a.Type)
		}
		stride := v.ByteStride
		if stride == 0 {
			stride = 4 * nc
		}
		for e := 0; e < a.Count; e++ {
			for c := 0; c < nc; c++ {
				at := bin + v.ByteOffset + a.ByteOffset + e*stride + 4*c
				if at+4 > len(out) {
					return nil, fmt.Errorf("accessor runs past the binary chunk")
				}
				f := math.Float32frombits(binary.LittleEndian.Uint32(out[at:]))
				binary.LittleEndian.PutUint32(out[at:], math.Float32bits(snap32(f, n)))
			}
		}
	}
	return out, nil
}
