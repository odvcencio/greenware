package fixture

import (
	"bytes"
	"encoding/binary"
	"math"
)

// Raw adds an accessor over bytes the caller has laid out. count and typ must
// match the bytes. It writes no min or max. target is 34962, 34963 or 0.
func (b *Builder) Raw(typ string, componentType, count int, normalized bool, data []byte, target int) int {
	a := map[string]any{"bufferView": b.view(data, target), "componentType": componentType, "count": count, "type": typ}
	if normalized {
		a["normalized"] = true
	}
	return b.acc(a)
}

// Mat4s adds a MAT4 FLOAT accessor (column-major, as glTF stores them).
func (b *Builder) Mat4s(ms [][16]float32) int {
	buf := new(bytes.Buffer)
	for _, m := range ms {
		for _, v := range m {
			_ = binary.Write(buf, binary.LittleEndian, v)
		}
	}
	return b.Raw("MAT4", 5126, len(ms), false, buf.Bytes(), 0)
}

// Joints8 adds a JOINTS_n accessor with unsigned byte components.
func (b *Builder) Joints8(js [][4]uint8) int {
	var data []byte
	for _, j := range js {
		data = append(data, j[:]...)
	}
	return b.Raw("VEC4", 5121, len(js), false, data, 34962)
}

// Joints16 adds a JOINTS_n accessor with unsigned short components.
func (b *Builder) Joints16(js [][4]uint16) int {
	var data []byte
	for _, j := range js {
		for _, v := range j {
			data = binary.LittleEndian.AppendUint16(data, v)
		}
	}
	return b.Raw("VEC4", 5123, len(js), false, data, 34962)
}

// Weights8 adds a WEIGHTS_n accessor of normalized unsigned bytes.
func (b *Builder) Weights8(ws [][4]uint8) int {
	var data []byte
	for _, w := range ws {
		data = append(data, w[:]...)
	}
	return b.Raw("VEC4", 5121, len(ws), true, data, 34962)
}

// Weights16 adds a WEIGHTS_n accessor of normalized unsigned shorts.
func (b *Builder) Weights16(ws [][4]uint16) int {
	var data []byte
	for _, w := range ws {
		for _, v := range w {
			data = binary.LittleEndian.AppendUint16(data, v)
		}
	}
	return b.Raw("VEC4", 5123, len(ws), true, data, 34962)
}

// Indices16 adds an index accessor of unsigned shorts.
func (b *Builder) Indices16(idx []uint32) int {
	var data []byte
	for _, i := range idx {
		data = binary.LittleEndian.AppendUint16(data, uint16(i))
	}
	return b.Raw("SCALAR", 5123, len(idx), false, data, 34963)
}

// Vec3s adds a VEC3 FLOAT accessor from points, with exact min and max when
// minmax is true.
func (b *Builder) Vec3s(pts [][3]float32, minmax bool) int {
	flat := make([]float32, 0, len(pts)*3)
	for _, p := range pts {
		flat = append(flat, p[:]...)
	}
	return b.Floats("VEC3", len(pts), flat, minmax)
}

// RawMesh adds a mesh from accessors that already exist. Each primitive is a
// triangle list over attributes and idx; targets lists per-primitive morph
// targets as semantic-to-accessor maps. extras and weights are written when
// non-nil.
func (b *Builder) RawMesh(name string, attrs map[string]int, idx int, targets []map[string]int, weights []float64, extras map[string]any) int {
	at := map[string]any{}
	for k, v := range attrs {
		at[k] = v
	}
	prim := map[string]any{"attributes": at, "indices": idx, "mode": 4}
	if len(targets) > 0 {
		var ts []any
		for _, t := range targets {
			m := map[string]any{}
			for k, v := range t {
				m[k] = v
			}
			ts = append(ts, m)
		}
		prim["targets"] = ts
	}
	mesh := map[string]any{"name": name, "primitives": []any{prim}}
	if weights != nil {
		mesh["weights"] = weights
	}
	if extras != nil {
		mesh["extras"] = extras
	}
	b.meshes = append(b.meshes, mesh)
	return len(b.meshes) - 1
}

// Skin adds a skin. ibm<0 omits inverseBindMatrices; skeleton<0 omits skeleton.
func (b *Builder) Skin(joints []int, ibm, skeleton int) int {
	s := map[string]any{"joints": joints}
	if ibm >= 0 {
		s["inverseBindMatrices"] = ibm
	}
	if skeleton >= 0 {
		s["skeleton"] = skeleton
	}
	b.skins = append(b.skins, s)
	return len(b.skins) - 1
}

// Channel is one animation channel: a sampler (by index into the animation's
// sampler list) aimed at a node property.
type Channel struct {
	Sampler int
	Node    int
	Path    string
}

// Sampler is one animation sampler over existing accessors.
type Sampler struct {
	Input, Output int
	Interpolation string // empty writes no interpolation key (LINEAR by default)
}

// Animation adds a clip.
func (b *Builder) Animation(name string, ss []Sampler, cs []Channel) int {
	var sj, cj []any
	for _, s := range ss {
		m := map[string]any{"input": s.Input, "output": s.Output}
		if s.Interpolation != "" {
			m["interpolation"] = s.Interpolation
		}
		sj = append(sj, m)
	}
	for _, c := range cs {
		cj = append(cj, map[string]any{"sampler": c.Sampler, "target": map[string]any{"node": c.Node, "path": c.Path}})
	}
	an := map[string]any{"samplers": sj, "channels": cj}
	if name != "" {
		an["name"] = name
	}
	b.animations = append(b.animations, an)
	return len(b.animations) - 1
}

// Translation4 is a column-major translation matrix.
func Translation4(x, y, z float32) [16]float32 {
	return [16]float32{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, x, y, z, 1}
}

// Strip is a two-joint skinned strip, small enough to bend by hand.
//
// Joint "j0" sits at the origin and has child "j1" at (0,1,0). The mesh is a
// flat ribbon in the XY plane: vertex 2r+s is at x = -0.5 (s=0) or +0.5 (s=1),
// y = r, z = 0, for rows r = 0, 1, 2. Row 0 follows j0 alone, row 1 is an even
// blend of j0 and j1, row 2 follows j1 alone. The inverse bind matrices are
// the inverse of each joint's world translation, so the strip at the rest pose
// equals its stored positions.
type Strip struct {
	// Overrides. A nil or zero field keeps the default.
	Weights    [][4]float32 // 6 entries
	Joints     [][4]uint8   // 6 entries
	IBM        [][16]float32
	Morph      bool // add one POSITION target "lift" moving vertex 5 by (0,0,1), default weight 0
	MorphNames []string
	// MorphSparse stores the "lift" target as a sparse accessor (one index,
	// no base view) instead of six dense deltas.
	MorphSparse bool
	MeshMove    [3]float64 // translation of the skinned mesh node (ignored by skinning)
}

// StripIDs names the parts of a built strip.
type StripIDs struct {
	Root, J0, J1, Body int
	Mesh, Skin         int
	Pos, Idx           int
}

// StripPositions are the six stored vertex positions.
var StripPositions = [][3]float32{
	{-0.5, 0, 0}, {0.5, 0, 0}, {-0.5, 1, 0}, {0.5, 1, 0}, {-0.5, 2, 0}, {0.5, 2, 0},
}

// StripIndices are four counter-clockwise triangles seen from +Z.
var StripIndices = []uint32{0, 1, 3, 0, 3, 2, 2, 3, 5, 2, 5, 4}

// Quat90Z is a rotation of 90 degrees about +Z.
var Quat90Z = [4]float32{0, 0, float32(math.Sqrt2 / 2), float32(math.Sqrt2 / 2)}

// NewStrip builds the strip. The caller adds animations, edits Parts, and so on.
func NewStrip(o Strip) (*Builder, StripIDs) {
	b := New()
	var ids StripIDs
	ids.Pos = b.Vec3s(StripPositions, true)
	ids.Idx = b.Indices16(StripIndices)
	js := o.Joints
	if js == nil {
		js = [][4]uint8{{0}, {0}, {0, 1}, {0, 1}, {1}, {1}}
	}
	ws := o.Weights
	if ws == nil {
		ws = [][4]float32{{1}, {1}, {0.5, 0.5}, {0.5, 0.5}, {1}, {1}}
	}
	var wflat []float32
	for _, w := range ws {
		wflat = append(wflat, w[:]...)
	}
	ibm := o.IBM
	if ibm == nil {
		ibm = [][16]float32{Translation4(0, 0, 0), Translation4(0, -1, 0)}
	}
	attrs := map[string]int{
		"POSITION":  ids.Pos,
		"JOINTS_0":  b.Joints8(js),
		"WEIGHTS_0": b.Floats("VEC4", len(ws), wflat, false),
	}
	var targets []map[string]int
	var weights []float64
	var extras map[string]any
	if o.Morph {
		delta := make([][3]float32, 6)
		delta[5] = [3]float32{0, 0, 1}
		if o.MorphSparse {
			targets = []map[string]int{{"POSITION": b.Sparse(SparseOpts{
				Count: 6, Indices: []uint32{5}, Values: []float32{0, 0, 1}, IndexType: 5121, MinMax: true})}}
		} else {
			targets = []map[string]int{{"POSITION": b.Vec3s(delta, true)}}
		}
		weights = []float64{0}
		if o.MorphNames != nil {
			extras = map[string]any{"targetNames": o.MorphNames}
		}
	}
	ids.Mesh = b.RawMesh("strip", attrs, ids.Idx, targets, weights, extras)
	ids.J0 = b.Node("j0", -1, [3]float64{})
	ids.J1 = b.Node("j1", -1, [3]float64{0, 1, 0})
	b.Child(ids.J0, ids.J1)
	ids.Skin = b.Skin([]int{ids.J0, ids.J1}, b.Mat4s(ibm), -1)
	ids.Body = b.Node("body", ids.Mesh, o.MeshMove)
	b.Set(ids.Body, "skin", ids.Skin)
	ids.Root = b.Node("rig", -1, [3]float64{})
	b.Child(ids.Root, ids.J0)
	b.Scene(ids.Root, ids.Body)
	return b, ids
}

// Floats32 adds a FLOAT accessor of type typ from a flat list, with exact
// min and max. The element count is derived from typ.
func (b *Builder) Floats32(typ string, vals []float32) int {
	comps := map[string]int{"SCALAR": 1, "VEC2": 2, "VEC3": 3, "VEC4": 4}[typ]
	return b.Floats(typ, len(vals)/comps, vals, true)
}

// RichRig is a valid strip with a morph target and four clips: LINEAR
// rotation ("bend"), STEP translation ("step"), CUBICSPLINE translation
// ("cubic") and morph weights ("morph"). Tests and fuzz seeds use it.
func RichRig() []byte { return richRig(false) }

// RichRigSparse is RichRig with the "lift" target stored as a sparse accessor.
func RichRigSparse() []byte { return richRig(true) }

func richRig(sparse bool) []byte {
	b, ids := NewStrip(Strip{Morph: true, MorphSparse: sparse, MorphNames: []string{"lift"}})
	t2 := b.Floats32("SCALAR", []float32{0, 1})
	b.Animation("bend", []Sampler{{Input: t2, Output: b.Floats32("VEC4", []float32{0, 0, 0, 1, Quat90Z[0], Quat90Z[1], Quat90Z[2], Quat90Z[3]})}},
		[]Channel{{Sampler: 0, Node: ids.J1, Path: "rotation"}})
	b.Animation("step", []Sampler{{Input: b.Floats32("SCALAR", []float32{0, 1, 2}), Output: b.Floats32("VEC3", []float32{0, 0, 0, 0, 1, 0, 0, 2, 0}), Interpolation: "STEP"}},
		[]Channel{{Sampler: 0, Node: ids.J0, Path: "translation"}})
	b.Animation("cubic", []Sampler{{Input: b.Floats32("SCALAR", []float32{0, 2}),
		Output: b.Floats32("VEC3", []float32{0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0}), Interpolation: "CUBICSPLINE"}},
		[]Channel{{Sampler: 0, Node: ids.J0, Path: "translation"}})
	b.Animation("morph", []Sampler{{Input: b.Floats32("SCALAR", []float32{0, 1}), Output: b.Floats32("SCALAR", []float32{0, 1})}},
		[]Channel{{Sampler: 0, Node: ids.Body, Path: "weights"}})
	return b.Bytes()
}

// BinRange returns the offset and length of the BIN chunk of a GLB that has
// one JSON chunk followed by one BIN chunk, as the Builder writes it. ok is
// false for any other layout.
func BinRange(glb []byte) (off, n int, ok bool) {
	if len(glb) < 20 {
		return 0, 0, false
	}
	jl := int(binary.LittleEndian.Uint32(glb[12:]))
	start := 20 + jl
	if len(glb) < start+8 {
		return 0, 0, false
	}
	n = int(binary.LittleEndian.Uint32(glb[start:]))
	if start+8+n > len(glb) {
		return 0, 0, false
	}
	return start + 8, n, true
}
