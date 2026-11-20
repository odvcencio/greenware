package glb

import (
	"encoding/binary"
	"fmt"
	"math"
)

// accInfo is the resolved layout of one accessor.
type accInfo struct {
	structOK   bool // layout checks passed
	readable   bool // bytes are available
	csize      int  // bytes per component
	ncomp      int  // components per element
	rows       int  // column height (== ncomp for non-matrix types)
	colStride  int  // bytes between matrix columns
	elem       int  // bytes per element including matrix column padding
	stride     int  // bytes between elements
	data       []byte
	sparseIdx  []byte
	sparseVals []byte
}

func compSize(ct int) (int, bool) {
	switch ct {
	case 5120, 5121:
		return 1, true
	case 5122, 5123:
		return 2, true
	case 5125, 5126:
		return 4, true
	}
	return 0, false
}

// typeInfo returns components, rows and whether the type is known.
func typeInfo(t string) (ncomp, rows int, ok bool) {
	switch t {
	case "SCALAR":
		return 1, 1, true
	case "VEC2":
		return 2, 2, true
	case "VEC3":
		return 3, 3, true
	case "VEC4":
		return 4, 4, true
	case "MAT2":
		return 4, 2, true
	case "MAT3":
		return 9, 3, true
	case "MAT4":
		return 16, 4, true
	}
	return 0, 0, false
}

func align4(n int) int { return (n + 3) &^ 3 }

func (f *File) validateBufferViews(c *collector) []bool {
	d := f.Doc
	ok := make([]bool, len(d.BufferViews))
	for i, v := range d.BufferViews {
		path := fmt.Sprintf("/bufferViews/%d", i)
		good := true
		if v.Buffer >= len(d.Buffers) {
			c.errf("VIEW-BUFFER", path+"/buffer", "buffer index %d is out of range (%d buffers)", v.Buffer, len(d.Buffers))
			good = false
		} else if end := v.ByteOffset + v.ByteLength; end > d.Buffers[v.Buffer].ByteLength {
			c.errf("VIEW-BOUNDS", path, "byteOffset+byteLength = %d exceeds buffer %d length %d", end, v.Buffer, d.Buffers[v.Buffer].ByteLength)
			good = false
		}
		if v.ByteStride != 0 && (v.ByteStride < 4 || v.ByteStride > 252 || v.ByteStride%4 != 0) {
			c.errf("VIEW-STRIDE", path+"/byteStride", "byteStride %d must be a multiple of 4 from 4 to 252", v.ByteStride)
		}
		if v.Target != 0 && v.Target != 34962 && v.Target != 34963 {
			c.errf("VIEW-TARGET", path+"/target", "target %d is neither 34962 nor 34963", v.Target)
		}
		ok[i] = good
	}
	return ok
}

func (f *File) validateAccessors(c *collector, viewOK []bool) {
	d := f.Doc
	f.accs = make([]accInfo, len(d.Accessors))
	for i, a := range d.Accessors {
		path := fmt.Sprintf("/accessors/%d", i)
		info := &f.accs[i]
		csize, okc := compSize(a.ComponentType)
		if !okc {
			c.errf("ACC-COMPONENT", path+"/componentType", "component type %d is not a glTF component type", a.ComponentType)
			continue
		}
		ncomp, rows, okt := typeInfo(a.Type)
		if !okt {
			c.errf("ACC-TYPE", path+"/type", "type %q is not a glTF accessor type", a.Type)
			continue
		}
		if a.Count < 1 {
			continue // already reported by the schema pass
		}
		if a.Normalized && (a.ComponentType == 5126 || a.ComponentType == 5125) {
			c.errf("ACC-NORMALIZED", path+"/normalized", "normalized is not allowed for component type %d", a.ComponentType)
		}
		info.csize, info.ncomp, info.rows = csize, ncomp, rows
		if ncomp > rows { // matrix: columns are padded to 4 bytes
			info.colStride = align4(rows * csize)
			info.elem = info.colStride * (ncomp / rows)
		} else {
			info.colStride = ncomp * csize
			info.elem = ncomp * csize
		}
		if a.Count*ncomp > maxElemCount {
			c.errf("ACC-TOO-LARGE", path+"/count", "count %d is larger than the oracle will read", a.Count)
			continue
		}
		info.structOK = true
		info.readable = true
		info.stride = info.elem
		if a.BufferView != nil {
			bi := *a.BufferView
			if bi >= len(d.BufferViews) {
				c.errf("ACC-VIEW", path+"/bufferView", "bufferView %d is out of range (%d views)", bi, len(d.BufferViews))
				info.structOK, info.readable = false, false
				continue
			}
			bv := d.BufferViews[bi]
			if bv.ByteStride != 0 {
				info.stride = bv.ByteStride
				if bv.ByteStride < info.elem {
					c.errf("ACC-STRIDE", path, "view byteStride %d is smaller than the element size %d", bv.ByteStride, info.elem)
					info.structOK, info.readable = false, false
					continue
				}
				if bv.ByteStride%csize != 0 {
					c.errf("ACC-ALIGN", path, "view byteStride %d is not a multiple of the component size %d", bv.ByteStride, csize)
				}
			}
			if a.ByteOffset%csize != 0 || (bv.ByteOffset+a.ByteOffset)%csize != 0 {
				c.errf("ACC-ALIGN", path+"/byteOffset", "accessor offset %d (absolute %d) is not a multiple of the component size %d", a.ByteOffset, bv.ByteOffset+a.ByteOffset, csize)
			}
			need := a.ByteOffset + info.stride*(a.Count-1) + info.elem
			if need > bv.ByteLength {
				c.errf("ACC-BOUNDS", path, "accessor needs %d bytes from its view's start but bufferView %d has %d", need, bi, bv.ByteLength)
				info.structOK, info.readable = false, false
				continue
			}
			if !viewOK[bi] {
				info.readable = false
			} else if buf := f.buffers[bv.Buffer]; buf == nil {
				info.readable = false
			} else {
				info.data = buf[bv.ByteOffset+a.ByteOffset:]
			}
		}
		if s := a.Sparse; s != nil {
			f.validateSparse(c, i, a, info, viewOK)
		}
	}
}

func (f *File) validateSparse(c *collector, i int, a Accessor, info *accInfo, viewOK []bool) {
	d := f.Doc
	path := fmt.Sprintf("/accessors/%d/sparse", i)
	s := a.Sparse
	if s.Count > a.Count {
		c.errf("SPARSE-COUNT", path+"/count", "sparse count %d exceeds accessor count %d", s.Count, a.Count)
		info.readable = false
		return
	}
	isz, ok := compSize(s.IndicesComponentType)
	if !ok || s.IndicesComponentType == 5120 || s.IndicesComponentType == 5122 || s.IndicesComponentType == 5126 {
		c.errf("SPARSE-INDEX-TYPE", path+"/indices/componentType", "sparse indices must be unsigned byte, short or int; got %d", s.IndicesComponentType)
		info.readable = false
		return
	}
	check := func(view, off, need int, key string) []byte {
		if view >= len(d.BufferViews) {
			c.errf("SPARSE-VIEW", path+"/"+key+"/bufferView", "bufferView %d is out of range", view)
			return nil
		}
		bv := d.BufferViews[view]
		if off+need > bv.ByteLength {
			c.errf("SPARSE-BOUNDS", path+"/"+key, "needs %d bytes at offset %d but bufferView %d has %d", need, off, view, bv.ByteLength)
			return nil
		}
		if !viewOK[view] || f.buffers[bv.Buffer] == nil {
			return nil
		}
		return f.buffers[bv.Buffer][bv.ByteOffset+off:]
	}
	info.sparseIdx = check(s.IndicesView, s.IndicesOffset, s.Count*isz, "indices")
	info.sparseVals = check(s.ValuesView, s.ValuesOffset, s.Count*info.elem, "values")
	if info.sparseIdx == nil || info.sparseVals == nil {
		info.readable = false
		return
	}
	prev := -1
	for k := 0; k < s.Count; k++ {
		idx := int(readUint(info.sparseIdx[k*isz:], isz))
		if idx >= a.Count || idx <= prev {
			c.errf("SPARSE-INDEX", fmt.Sprintf("%s/indices/%d", path, k), "sparse index %d is out of range or not strictly increasing", idx)
			info.readable = false
			return
		}
		prev = idx
	}
}

func readUint(b []byte, size int) uint64 {
	switch size {
	case 1:
		return uint64(b[0])
	case 2:
		return uint64(binary.LittleEndian.Uint16(b))
	default:
		return uint64(binary.LittleEndian.Uint32(b))
	}
}

func readComp(b []byte, ct int) float64 {
	le := binary.LittleEndian
	switch ct {
	case 5120:
		return float64(int8(b[0]))
	case 5121:
		return float64(b[0])
	case 5122:
		return float64(int16(le.Uint16(b)))
	case 5123:
		return float64(le.Uint16(b))
	case 5125:
		return float64(le.Uint32(b))
	}
	return float64(math.Float32frombits(le.Uint32(b)))
}

func (info *accInfo) element(data []byte, e, ct int, out []float64) {
	base := e * info.stride
	if info.stride == 0 {
		return
	}
	for k := 0; k < info.ncomp; k++ {
		col, row := k/info.rows, k%info.rows
		off := base + col*info.colStride + row*info.csize
		out[k] = readComp(data[off:], ct)
	}
}

// raw returns the stored component values (no normalisation), with sparse
// substitution applied, or nil when the accessor cannot be read.
func (f *File) raw(i int) []float64 {
	if i < 0 || i >= len(f.accs) {
		return nil
	}
	info := &f.accs[i]
	if !info.structOK || !info.readable {
		return nil
	}
	a := f.Doc.Accessors[i]
	out := make([]float64, a.Count*info.ncomp)
	if a.BufferView != nil {
		for e := 0; e < a.Count; e++ {
			info.element(info.data, e, a.ComponentType, out[e*info.ncomp:(e+1)*info.ncomp])
		}
	}
	if s := a.Sparse; s != nil {
		isz, _ := compSize(s.IndicesComponentType)
		vi := *info
		vi.stride = vi.elem
		for k := 0; k < s.Count; k++ {
			idx := int(readUint(info.sparseIdx[k*isz:], isz))
			vi.element(info.sparseVals, k, a.ComponentType, out[idx*info.ncomp:(idx+1)*info.ncomp])
		}
	}
	return out
}

// Floats returns the components of accessor i as float64. Normalised integer
// accessors are converted to [0,1] or [-1,1] per the glTF rules. ok is false
// when the accessor is invalid or unreadable.
func (f *File) Floats(i int) (vals []float64, ncomp int, ok bool) {
	if f.Doc == nil || i < 0 || i >= len(f.accs) {
		return nil, 0, false
	}
	if v, hit := f.cache[i]; hit {
		return v, f.accs[i].ncomp, v != nil
	}
	v := f.raw(i)
	a := f.Doc.Accessors[i]
	if v != nil && a.Normalized {
		for k, x := range v {
			switch a.ComponentType {
			case 5120:
				v[k] = math.Max(x/127, -1)
			case 5121:
				v[k] = x / 255
			case 5122:
				v[k] = math.Max(x/32767, -1)
			case 5123:
				v[k] = x / 65535
			}
		}
	}
	f.cache[i] = v
	return v, f.accs[i].ncomp, v != nil
}

// Indices reads an index accessor as unsigned integers.
func (f *File) Indices(i int) ([]uint32, bool) {
	if f.Doc == nil || i < 0 || i >= len(f.accs) {
		return nil, false
	}
	a := f.Doc.Accessors[i]
	if a.Type != "SCALAR" || (a.ComponentType != 5121 && a.ComponentType != 5123 && a.ComponentType != 5125) {
		return nil, false
	}
	v := f.raw(i)
	if v == nil {
		return nil, false
	}
	out := make([]uint32, len(v))
	for k, x := range v {
		out[k] = uint32(x)
	}
	return out, true
}

// checkAccessorData verifies stored values: finite floats and min/max.
func (f *File) checkAccessorData(c *collector) {
	for i, a := range f.Doc.Accessors {
		path := fmt.Sprintf("/accessors/%d", i)
		info := &f.accs[i]
		if !info.structOK {
			continue
		}
		if a.HasMinMax {
			if len(a.Min) != info.ncomp || len(a.Max) != info.ncomp {
				c.errf("ACC-MINMAX-LENGTH", path, "min and max must both have %d items (min has %d, max has %d)", info.ncomp, len(a.Min), len(a.Max))
				continue
			}
		}
		if a.Sparse != nil && !info.readable {
			continue
		}
		v := f.raw(i)
		if v == nil {
			continue
		}
		if a.ComponentType == 5126 {
			bad := false
			for k, x := range v {
				if math.IsNaN(x) || math.IsInf(x, 0) {
					c.errf("ACC-NONFINITE", path, "element %d component %d is %v", k/info.ncomp, k%info.ncomp, x)
					bad = true
					break
				}
			}
			if bad {
				continue
			}
		}
		if !a.HasMinMax {
			continue
		}
		for k := 0; k < info.ncomp; k++ {
			lo, hi := math.Inf(1), math.Inf(-1)
			for e := 0; e < a.Count; e++ {
				x := v[e*info.ncomp+k]
				lo, hi = math.Min(lo, x), math.Max(hi, x)
			}
			if a.Min[k] != lo {
				c.errf("ACC-MIN", fmt.Sprintf("%s/min/%d", path, k), "min is %v but the smallest stored value is %v", a.Min[k], lo)
			}
			if a.Max[k] != hi {
				c.errf("ACC-MAX", fmt.Sprintf("%s/max/%d", path, k), "max is %v but the largest stored value is %v", a.Max[k], hi)
			}
		}
	}
}
