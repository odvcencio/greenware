package fixture

import (
	"encoding/binary"
	"math"
)

// SparseOpts describes one FLOAT accessor with a sparse block. Every field
// that a malformed test needs to break has an override. The bytes are written
// as the glTF 2.0 specification lays them out: indices and values live in
// bufferViews with no target and no byteStride.
type SparseOpts struct {
	Type  string    // "SCALAR", "VEC2", "VEC3" or "VEC4"; default "VEC3"
	Count int       // accessor element count
	Base  []float32 // flat base values; nil writes no bufferView (the base is all zeros)
	// Indices and Values are the sparse substitution. Values is flat, with
	// len(Indices) elements.
	Indices   []uint32
	Values    []float32
	IndexType int // 5121, 5123 or 5125; default 5123 (any value is written as given)
	// SparseCount overrides "count" in the sparse block when non-nil.
	SparseCount *int
	// IndexOffset and ValuesOffset are byteOffset values inside their views.
	// The builder writes that many zero bytes before the data.
	IndexOffset  int
	ValuesOffset int
	// IndexBytes replaces the encoded indices when non-nil (for widths the
	// encoder cannot write, such as an invalid componentType).
	IndexBytes []byte
	// DropValueBytes removes that many bytes from the end of the values view.
	DropValueBytes int
	// MinMax writes the exact extrema of the applied values.
	MinMax bool
	// ViewTarget sets a target on the indices view (a fault) when non-zero.
	ViewTarget int
}

func encodeIndices(ix []uint32, ct int) []byte {
	var out []byte
	for _, v := range ix {
		switch ct {
		case 5121:
			out = append(out, byte(v))
		case 5125:
			out = binary.LittleEndian.AppendUint32(out, v)
		default:
			out = binary.LittleEndian.AppendUint16(out, uint16(v))
		}
	}
	return out
}

// Applied returns the flat values the accessor describes: the base (or
// zeros) with the sparse values written over it. It is the specification's
// reading of section 3.6.2.3, used to set min and max.
func (o SparseOpts) Applied() []float32 {
	comps := map[string]int{"SCALAR": 1, "VEC2": 2, "VEC3": 3, "VEC4": 4}[o.typ()]
	out := make([]float32, o.Count*comps)
	copy(out, o.Base)
	for k, ix := range o.Indices {
		if int(ix) < o.Count && (k+1)*comps <= len(o.Values) {
			copy(out[int(ix)*comps:], o.Values[k*comps:(k+1)*comps])
		}
	}
	return out
}

func (o SparseOpts) typ() string {
	if o.Type == "" {
		return "VEC3"
	}
	return o.Type
}

// Sparse adds the accessor and returns its index.
func (b *Builder) Sparse(o SparseOpts) int {
	typ := o.typ()
	comps := map[string]int{"SCALAR": 1, "VEC2": 2, "VEC3": 3, "VEC4": 4}[typ]
	ct := o.IndexType
	if ct == 0 {
		ct = 5123
	}
	a := map[string]any{"componentType": 5126, "count": o.Count, "type": typ}
	if o.Base != nil {
		a["bufferView"] = b.view(floatBytes(o.Base), 34962)
	}
	ib := o.IndexBytes
	if ib == nil {
		ib = encodeIndices(o.Indices, ct)
	}
	idxData := append(make([]byte, o.IndexOffset), ib...)
	vb := floatBytes(o.Values)
	vb = vb[:len(vb)-o.DropValueBytes]
	valData := append(make([]byte, o.ValuesOffset), vb...)
	n := len(o.Indices)
	if o.SparseCount != nil {
		n = *o.SparseCount
	}
	iv := b.view(idxData, o.ViewTarget)
	vv := b.view(valData, 0)
	idx := map[string]any{"bufferView": iv, "componentType": ct}
	if o.IndexOffset != 0 {
		idx["byteOffset"] = o.IndexOffset
	}
	val := map[string]any{"bufferView": vv}
	if o.ValuesOffset != 0 {
		val["byteOffset"] = o.ValuesOffset
	}
	a["sparse"] = map[string]any{"count": n, "indices": idx, "values": val}
	if o.MinMax && o.Count > 0 {
		ap := o.Applied()
		lo, hi := make([]float64, comps), make([]float64, comps)
		for k := 0; k < comps; k++ {
			lo[k], hi[k] = math.Inf(1), math.Inf(-1)
			for e := 0; e < o.Count; e++ {
				x := float64(ap[e*comps+k])
				lo[k], hi[k] = math.Min(lo[k], x), math.Max(hi[k], x)
			}
		}
		a["min"], a["max"] = lo, hi
	}
	return b.acc(a)
}

func floatBytes(v []float32) []byte {
	var out []byte
	for _, x := range v {
		out = binary.LittleEndian.AppendUint32(out, math.Float32bits(x))
	}
	return out
}
