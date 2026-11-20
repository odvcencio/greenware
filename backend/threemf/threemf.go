// Package threemf writes and reads 3MF files: the print format, a ZIP package
// (Open Packaging Conventions) that holds one XML model of triangle meshes.
//
// Frame and units. A 3MF model is right-handed with +z up, and this package
// writes the unit `millimeter` and nothing else. Every coordinate a caller
// gives is already in millimetres: the caller converts from metres once, at
// its own boundary (compiler.BuildPrint multiplies by 1000 and by the print
// scale there). Nothing in this package converts units.
//
// Bytes. Encode is deterministic. The ZIP has three stored (uncompressed)
// entries in bytewise name order, the fixed timestamp 1980-01-01 00:00:00, no
// extra fields and explicit CRC-32. Every coordinate is written with
// FormatMM. Objects, vertices and triangles keep the order the caller gave
// them; the caller owns the canonical order (BuildPrint numbers vertices by
// first use in triangle order). The XML has no timestamps and no maps.
//
// Validation. Validate is the print gate. It does not repair anything. An
// object must be a closed 2-manifold shell with outward winding and positive
// volume; the findings name the object, the kind of fault and the first
// example.
package threemf

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Millimeter is the only unit this package writes. It is the 3MF attribute
// value.
const Millimeter = "millimeter"

// Namespaces and content types of the package parts.
const (
	CoreNamespace  = "http://schemas.microsoft.com/3dmanufacturing/core/2015/02"
	typesNamespace = "http://schemas.openxmlformats.org/package/2006/content-types"
	relsNamespace  = "http://schemas.openxmlformats.org/package/2006/relationships"
	modelRelType   = "http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"
	modelType      = "application/vnd.ms-package.3dmanufacturing-3dmodel+xml"
	relsType       = "application/vnd.openxmlformats-package.relationships+xml"

	// ModelPath is the part that holds the model.
	ModelPath = "3D/3dmodel.model"
)

// Vec3 is a point in millimetres.
type Vec3 [3]float64

// Tri is a triangle as three vertex indices. It is counter-clockwise seen
// from outside the solid (outward winding).
type Tri [3]int

// Object is one solid: a triangle mesh with its own vertices.
type Object struct {
	ID        int // positive; strictly increasing along Model.Objects
	Name      string
	Vertices  []Vec3
	Triangles []Tri
}

// Item is one build item: an object placed with the identity transform.
type Item struct {
	ObjectID int
}

// Model is a 3MF model.
type Model struct {
	Unit    string // Millimeter
	Title   string // written as metadata "Title"; may be empty
	Objects []Object
	Items   []Item
}

// NewModel returns a millimetre model with one build item per object, in
// object order.
func NewModel(title string, objects []Object) *Model {
	m := &Model{Unit: Millimeter, Title: title, Objects: objects}
	for _, o := range objects {
		m.Items = append(m.Items, Item{ObjectID: o.ID})
	}
	return m
}

// FormatMM writes a coordinate: fixed notation with six decimals (a
// nanometre), rounded half to even on the exact value of the float64, with
// negative zero and any value that rounds to zero written as 0.000000. The
// rule is that of strconv.FormatFloat(v, 'f', 6, 64), which rounds the exact
// binary value and takes the even digit on an exact tie (0.0078125 is
// 0.007812, 0.0234375 is 0.023438). It is pure integer arithmetic, so it
// gives the same text on every architecture.
func FormatMM(v float64) string {
	s := strconv.FormatFloat(v, 'f', 6, 64)
	if s == "-0.000000" {
		return "0.000000"
	}
	return s
}

// ErrNotFinite is returned when a coordinate is NaN or infinite.
var ErrNotFinite = errors.New("threemf: a coordinate is not finite")

// Finding is one fault Validate found in a model.
type Finding struct {
	Object int    // object id; 0 for a fault of the model
	Name   string // object name
	Code   string // one of the Code constants
	Count  int    // how many times the fault occurs in the object
	Detail string // the first example, in millimetres
}

// Finding codes.
const (
	CodeUnit           = "unit"
	CodeObjectID       = "object-id"
	CodeEmpty          = "empty"
	CodeIndexRange     = "index-range"
	CodeDegenerate     = "degenerate-triangle"
	CodeOpenEdge       = "open-edge"
	CodeNonManifold    = "non-manifold-edge"
	CodeWinding        = "inconsistent-winding"
	CodeVolume         = "non-positive-volume"
	CodeBuildItem      = "build-item"
	CodeNotFiniteCoord = "non-finite-coordinate"
)

func (f Finding) String() string {
	who := "model"
	if f.Object != 0 {
		who = fmt.Sprintf("object %d %q", f.Object, f.Name)
	}
	s := fmt.Sprintf("%s: %s", who, f.Code)
	if f.Count > 1 {
		s += fmt.Sprintf(" (%d times)", f.Count)
	}
	if f.Detail != "" {
		s += ": " + f.Detail
	}
	return s
}

// Volume returns the signed volume of the object in cubic millimetres by the
// divergence theorem. It is positive when the winding is outward. It
// is meaningful for a closed object only.
func Volume(o *Object) float64 {
	var v float64
	for _, t := range o.Triangles {
		if !inRange(o, t) {
			continue
		}
		a, b, c := o.Vertices[t[0]], o.Vertices[t[1]], o.Vertices[t[2]]
		v += dot(a, cross(b, c))
	}
	return v / 6
}

// Bounds returns the lowest and highest corner of the object's vertices that
// a triangle uses. ok is false for an object with no triangle.
func Bounds(o *Object) (lo, hi Vec3, ok bool) {
	for _, t := range o.Triangles {
		if !inRange(o, t) {
			continue
		}
		for _, vi := range t {
			p := o.Vertices[vi]
			if !ok {
				lo, hi, ok = p, p, true
				continue
			}
			for k := 0; k < 3; k++ {
				if p[k] < lo[k] {
					lo[k] = p[k]
				}
				if p[k] > hi[k] {
					hi[k] = p[k]
				}
			}
		}
	}
	return lo, hi, ok
}

func inRange(o *Object, t Tri) bool {
	for _, vi := range t {
		if vi < 0 || vi >= len(o.Vertices) {
			return false
		}
	}
	return true
}

func dot(a, b Vec3) float64 {
	return float64(a[0]*b[0]) + float64(a[1]*b[1]) + float64(a[2]*b[2])
}

func cross(a, b Vec3) Vec3 {
	return Vec3{
		float64(a[1]*b[2]) - float64(a[2]*b[1]),
		float64(a[2]*b[0]) - float64(a[0]*b[2]),
		float64(a[0]*b[1]) - float64(a[1]*b[0]),
	}
}

func sub(a, b Vec3) Vec3 { return Vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }

func fmtPoint(p Vec3) string {
	return "(" + FormatMM(p[0]) + ", " + FormatMM(p[1]) + ", " + FormatMM(p[2]) + ") mm"
}

// Validate checks a model and returns every finding, in object order and then
// code order. An empty result means the model is printable under the rules in
// the package comment. It checks:
//
//   - the unit is Millimeter, object ids are positive and strictly increasing,
//     every build item names an object;
//   - every coordinate is finite;
//   - every triangle index is in range;
//   - no triangle is degenerate: two vertices at one position, or zero area;
//   - every undirected edge (vertices joined by exact position, as a slicer
//     joins them) is used by exactly two triangles that run along it in
//     opposite directions: a count of one is an open edge, a count above two
//     is a non-manifold edge, two same-direction uses are inconsistent
//     winding;
//   - the signed volume is positive (outward winding).
//
// It does not detect two solids that intersect each other or a shell that
// intersects itself, and it checks no wall thickness.
func Validate(m *Model) []Finding {
	var out []Finding
	if m.Unit != Millimeter {
		out = append(out, Finding{Code: CodeUnit, Detail: fmt.Sprintf("unit %q; this package writes %q only", m.Unit, Millimeter)})
	}
	ids := map[int]bool{}
	prev := 0
	for i := range m.Objects {
		o := &m.Objects[i]
		if o.ID <= prev {
			out = append(out, Finding{Object: o.ID, Name: o.Name, Code: CodeObjectID, Detail: fmt.Sprintf("id %d follows %d; ids must be positive and strictly increasing", o.ID, prev)})
		}
		prev = o.ID
		ids[o.ID] = true
		out = append(out, validateObject(o)...)
	}
	for _, it := range m.Items {
		if !ids[it.ObjectID] {
			out = append(out, Finding{Code: CodeBuildItem, Detail: fmt.Sprintf("build item names object %d, which does not exist", it.ObjectID)})
		}
	}
	if len(m.Items) == 0 && len(m.Objects) > 0 {
		out = append(out, Finding{Code: CodeBuildItem, Detail: "the model has objects and no build item"})
	}
	return out
}

type edgeKey struct{ a, b int } // welded ids, a < b

type edgeUse struct {
	fwd, back int // uses a->b and b->a
}

func validateObject(o *Object) []Finding {
	f := func(code string, count int, detail string) Finding {
		return Finding{Object: o.ID, Name: o.Name, Code: code, Count: count, Detail: detail}
	}
	var out []Finding
	if len(o.Triangles) == 0 {
		return []Finding{f(CodeEmpty, 1, "the object has no triangle")}
	}
	for i, p := range o.Vertices {
		for _, v := range p {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return []Finding{f(CodeNotFiniteCoord, 1, fmt.Sprintf("vertex %d has a coordinate that is not finite", i))}
			}
		}
	}
	bad := 0
	firstBad := ""
	for i, t := range o.Triangles {
		if !inRange(o, t) {
			if bad == 0 {
				firstBad = fmt.Sprintf("triangle %d uses vertices %d, %d, %d of %d", i, t[0], t[1], t[2], len(o.Vertices))
			}
			bad++
		}
	}
	if bad > 0 {
		return []Finding{f(CodeIndexRange, bad, firstBad)}
	}

	// Slicers join vertices by position. Number the distinct positions.
	type key [3]uint64
	weld := make([]int, len(o.Vertices))
	seen := map[key]int{} // lookup only
	for i, p := range o.Vertices {
		var k key
		for j, v := range p {
			if v == 0 {
				v = 0 // folds -0 into +0
			}
			k[j] = math.Float64bits(v)
		}
		id, ok := seen[k]
		if !ok {
			id = len(seen)
			seen[k] = id
		}
		weld[i] = id
	}

	degenerate := 0
	firstDeg := ""
	uses := map[edgeKey]*edgeUse{} // lookup only; reported in sorted order
	for i, t := range o.Triangles {
		a, b, c := o.Vertices[t[0]], o.Vertices[t[1]], o.Vertices[t[2]]
		w := [3]int{weld[t[0]], weld[t[1]], weld[t[2]]}
		n := cross(sub(b, a), sub(c, a))
		if w[0] == w[1] || w[1] == w[2] || w[0] == w[2] || dot(n, n) == 0 {
			if degenerate == 0 {
				firstDeg = fmt.Sprintf("triangle %d at %s", i, fmtPoint(a))
			}
			degenerate++
			continue
		}
		for k := 0; k < 3; k++ {
			from, to := w[k], w[(k+1)%3]
			key := edgeKey{from, to}
			fwd := true
			if from > to {
				key = edgeKey{to, from}
				fwd = false
			}
			u := uses[key]
			if u == nil {
				u = &edgeUse{}
				uses[key] = u
			}
			if fwd {
				u.fwd++
			} else {
				u.back++
			}
		}
	}
	if degenerate > 0 {
		out = append(out, f(CodeDegenerate, degenerate, firstDeg))
	}

	// One representative position per welded id, for the examples.
	rep := make([]Vec3, len(seen))
	for i, p := range o.Vertices {
		rep[weld[i]] = p
	}
	keys := make([]edgeKey, 0, len(uses))
	for k := range uses {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].b < keys[j].b
	})
	var open, many, wind int
	var eo, em, ew string
	for _, k := range keys {
		u := uses[k]
		total := u.fwd + u.back
		desc := fmtPoint(rep[k.a]) + " to " + fmtPoint(rep[k.b])
		switch {
		case total == 1:
			if open == 0 {
				eo = desc
			}
			open++
		case total > 2:
			if many == 0 {
				em = fmt.Sprintf("%d triangles meet at the edge %s", total, desc)
			}
			many++
		case u.fwd != 1: // two uses in the same direction
			if wind == 0 {
				ew = fmt.Sprintf("two triangles run the same way along the edge %s", desc)
			}
			wind++
		}
	}
	if open > 0 {
		out = append(out, f(CodeOpenEdge, open, "first open edge "+eo))
	}
	if many > 0 {
		out = append(out, f(CodeNonManifold, many, em))
	}
	if wind > 0 {
		out = append(out, f(CodeWinding, wind, ew))
	}
	// The volume of a shell with open or inconsistent edges means nothing;
	// report it only for a shell that is closed and consistent.
	if open == 0 && many == 0 && wind == 0 && degenerate == 0 {
		if v := Volume(o); !(v > 0) {
			out = append(out, f(CodeVolume, 1, fmt.Sprintf("signed volume %s mm3: the triangles wind inward", FormatMM(v))))
		}
	}
	return out
}
