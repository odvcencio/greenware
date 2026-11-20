package gltf

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
)

// This file holds the sparse accessor support for morph target deltas: the
// size estimate, AutoSparse, the encoder half and the decoder half.

// sparseIndexLimit is the first vertex index that needs a uint32 index.
const sparseIndexLimit = 65536

// sparseSplit returns the vertices of data (xyz triples) whose delta is not
// exactly zero in every component, with their three values each. Negative
// zero counts as zero, because Encode writes it as zero.
func sparseSplit(data []float32) (idx []uint32, vals []float32) {
	for v := 0; v < len(data)/3; v++ {
		x, y, z := data[3*v], data[3*v+1], data[3*v+2]
		if x == 0 && y == 0 && z == 0 {
			continue
		}
		idx = append(idx, uint32(v))
		vals = append(vals, x, y, z)
	}
	return idx, vals
}

func pad4(n int) int { return (n + 3) &^ 3 }

// sparseAttrBytes is the binary size of one attribute written sparse: the
// padded index view plus the padded value view. An attribute with no moved
// vertex is written dense, so its size is the dense size.
func sparseAttrBytes(data []float32) int {
	if len(data) == 0 {
		return 0
	}
	idx, vals := sparseSplit(data)
	if len(idx) == 0 {
		return 4 * len(data)
	}
	isz := 2
	if idx[len(idx)-1] >= sparseIndexLimit {
		isz = 4
	}
	return pad4(isz*len(idx)) + 4*len(vals)
}

// SparseSaves returns the binary size of one morph target's POSITION and
// NORMAL deltas written dense and written sparse. Either slice may be empty.
// Sizes count the bufferView bytes with their 4-byte padding; the few dozen
// bytes of extra JSON per sparse accessor are not counted. An attribute with
// no non-zero delta is written dense in both forms (glTF forbids a sparse
// count of 0), so it adds the same to both sizes. A caller may choose sparse
// when sparseBytes < denseBytes, which AutoSparse does.
func SparseSaves(positions, normals []float32) (denseBytes, sparseBytes int) {
	denseBytes = 4*len(positions) + 4*len(normals)
	sparseBytes = sparseAttrBytes(positions) + sparseAttrBytes(normals)
	return denseBytes, sparseBytes
}

// AutoSparse sets Sparse on each morph target of p to true when its sparse
// form is smaller than its dense form (see SparseSaves), and to false when
// not. glTF advises sparse storage when well under half of the vertices
// move; this rule follows the actual byte counts.
func (p *Primitive) AutoSparse() {
	for i := range p.Targets {
		t := &p.Targets[i]
		d, s := SparseSaves(t.Positions, t.Normals)
		t.Sparse = s < d
	}
}

// AutoSparse calls Primitive.AutoSparse on every primitive of d.
func (d *Document) AutoSparse() {
	for i := range d.Meshes {
		for j := range d.Meshes[i].Primitives {
			d.Meshes[i].Primitives[j].AutoSparse()
		}
	}
}

// addMorphAcc writes one morph target attribute (xyz deltas for n vertices)
// and returns its accessor number. With sparse set and at least one moved
// vertex it writes a sparse accessor; otherwise a dense one. withMinMax adds
// the exact min and max of the dense array, which stays correct for a sparse
// accessor because the unmoved vertices count as zero.
func (e *encoder) addMorphAcc(data []float32, n int, sparse, withMinMax bool) (int, error) {
	a := accRec{comp: compFloat, count: n, typ: "VEC3"}
	if withMinMax {
		a.min, a.max = minMax3(data)
	}
	var idx []uint32
	var vals []float32
	if sparse {
		idx, vals = sparseSplit(data)
	}
	if len(idx) == 0 {
		return e.addAcc(floatBytes(data), targetArray, a)
	}
	a.comp2 = compUshort
	var ib []byte
	if idx[len(idx)-1] >= sparseIndexLimit {
		a.comp2 = compUint
		for _, v := range idx {
			ib = binary.LittleEndian.AppendUint32(ib, v)
		}
	} else {
		for _, v := range idx {
			ib = binary.LittleEndian.AppendUint16(ib, uint16(v))
		}
	}
	iv, err := e.addView(ib, 0)
	if err != nil {
		return 0, err
	}
	vv, err := e.addView(floatBytes(vals), 0)
	if err != nil {
		return 0, err
	}
	a.view = -1
	a.sparseCount, a.idxView, a.valView = len(idx), iv, vv
	e.accs = append(e.accs, a)
	return len(e.accs) - 1, nil
}

// sparseJSON returns the "sparse" member of accessor a.
func sparseJSON(a accRec) string {
	ix := fields{}
	ix.add("bufferView", fmt.Sprint(a.idxView))
	ix.add("componentType", fmt.Sprint(a.comp2))
	vs := fields{}
	vs.add("bufferView", fmt.Sprint(a.valView))
	sf := fields{}
	sf.add("count", fmt.Sprint(a.sparseCount))
	sf.add("indices", ix.String())
	sf.add("values", vs.String())
	return sf.String()
}

type jSparseIndices struct {
	BufferView    *int  `json:"bufferView"`
	ByteOffset    int64 `json:"byteOffset"`
	ComponentType int   `json:"componentType"`
}
type jSparseValues struct {
	BufferView *int  `json:"bufferView"`
	ByteOffset int64 `json:"byteOffset"`
}
type jSparse struct {
	Count   *int64          `json:"count"`
	Indices *jSparseIndices `json:"indices"`
	Values  *jSparseValues  `json:"values"`
}

func hasSparse(a *jAccessor) bool {
	return len(a.Sparse) > 0 && string(a.Sparse) != "null"
}

// sparseBytes checks that count elements of size esize starting at byteOffset
// of bufferView bv lie inside the buffer, and returns them. The view may not
// have a byteStride and the offset must be aligned to align.
func (d *decoder) sparseBytes(path, loc string, bv *int, byteOffset int64, count int64, esize, align int) ([]byte, error) {
	if bv == nil {
		return nil, at(path, loc+".bufferView", "missing")
	}
	v, data, err := d.view(path, *bv)
	if err != nil {
		return nil, err
	}
	if v.ByteStride != 0 {
		return nil, at(path, loc+".bufferView", "bufferViews[%d] has a byteStride; sparse data must be tightly packed", *bv)
	}
	if byteOffset < 0 || byteOffset > int64(len(data)) {
		return nil, at(path, loc+".byteOffset", "%d is outside the %d-byte bufferView", byteOffset, len(data))
	}
	if (v.ByteOffset+byteOffset)%int64(align) != 0 {
		return nil, at(path, loc+".byteOffset", "offset %d is not a multiple of %d", v.ByteOffset+byteOffset, align)
	}
	need := count * int64(esize)
	if need > int64(len(data))-byteOffset {
		return nil, at(path, loc, "%d elements need %d bytes from offset %d; the bufferView has %d", count, need, byteOffset, len(data))
	}
	return data[byteOffset : byteOffset+need], nil
}

// morphFloats reads a morph target attribute (accessor idx, VEC3 float32)
// for a primitive of nv vertices. A sparse accessor is expanded to a dense
// array over its base bufferView, or over zeros when it has none. The bool is
// true when the accessor was sparse.
func (d *decoder) morphFloats(path string, idx, nv int) ([]float32, bool, error) {
	sp, err := d.accessorX(path, idx, "VEC3", false, true, compFloat)
	if err != nil {
		return nil, false, err
	}
	if sp.count != nv {
		return nil, false, errf(path, "count %d differs from POSITION count %d", sp.count, nv)
	}
	a := &d.j.Accessors[idx]
	if !hasSparse(a) {
		out, err := sp.floats(path, 3)
		return out, false, err
	}
	ap := fmt.Sprintf("accessors[%d]", idx)
	var out []float32
	if sp.data == nil {
		out = make([]float32, 3*nv)
	} else if out, err = sp.floats(path, 3); err != nil {
		return nil, false, err
	}
	var s jSparse
	if err := json.Unmarshal(a.Sparse, &s); err != nil {
		return nil, false, at(path, ap+".sparse", "not a valid sparse object: %v", err)
	}
	if s.Count == nil {
		return nil, false, at(path, ap+".sparse.count", "missing")
	}
	if *s.Count < 1 || *s.Count > a.Count {
		return nil, false, at(path, ap+".sparse.count", "%d must be from 1 to the accessor count %d", *s.Count, a.Count)
	}
	if s.Indices == nil {
		return nil, false, at(path, ap+".sparse.indices", "missing")
	}
	if s.Values == nil {
		return nil, false, at(path, ap+".sparse.values", "missing")
	}
	ic := s.Indices.ComponentType
	if ic != compUbyte && ic != compUshort && ic != compUint {
		return nil, false, at(path, ap+".sparse.indices.componentType", "%d is not 5121, 5123 or 5125", ic)
	}
	isz := compSize[ic]
	ib, err := d.sparseBytes(path, ap+".sparse.indices", s.Indices.BufferView, s.Indices.ByteOffset, *s.Count, isz, isz)
	if err != nil {
		return nil, false, err
	}
	vb, err := d.sparseBytes(path, ap+".sparse.values", s.Values.BufferView, s.Values.ByteOffset, *s.Count, 12, 4)
	if err != nil {
		return nil, false, err
	}
	prev := int64(-1)
	for k := 0; k < int(*s.Count); k++ {
		var ix int64
		switch ic {
		case compUbyte:
			ix = int64(ib[k])
		case compUshort:
			ix = int64(binary.LittleEndian.Uint16(ib[2*k:]))
		default:
			ix = int64(binary.LittleEndian.Uint32(ib[4*k:]))
		}
		if ix <= prev {
			return nil, false, at(path, ap+".sparse.indices", "index %d (value %d) is not above the one before (%d); indices must increase strictly", k, ix, prev)
		}
		if ix >= a.Count {
			return nil, false, at(path, ap+".sparse.indices", "index %d (value %d) is not below the accessor count %d", k, ix, a.Count)
		}
		prev = ix
		for c := 0; c < 3; c++ {
			f := math.Float32frombits(binary.LittleEndian.Uint32(vb[12*k+4*c:]))
			if !finite32(f) {
				return nil, false, errf(fmt.Sprintf("%s.sparse.values[%d]", path, 3*k+c), "value is %v; NaN and infinity are not allowed", f)
			}
			if f == 0 {
				f = 0
			}
			out[3*int(ix)+c] = f
		}
	}
	return out, true, nil
}
