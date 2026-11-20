// Package registry is the table of what each kind of declaration accepts: its
// fields with type, unit and default, its child blocks and rows, and which of
// them are required. Lowering, name resolution and evaluation read it, and
// reference documentation can be generated from it (see Reference).
//
// The table is plain Go data in table.go. Nothing here parses text.
package registry

import (
	"sort"

	"m31labs.dev/greenware/units"
)

// TypeKind is the kind of value a slot takes.
type TypeKind uint8

const (
	TLength TypeKind = iota + 1
	TAngle
	TNumber   // dimensionless real; ratio and number share it
	TInteger  // whole number
	TDuration //
	TBytes    //
	TSwitch
	TColor
	TString
	TStringList
	TAxis     // x, y, z, +x, -x, ...
	TEnum     // one of Type.Enum
	TLabel    // a plain identifier, such as a socket kind
	TRef      // a name in Type.Ns
	TRefList  // a list of names in Type.Ns
	TPoint    // three lengths
	TExtent   // three lengths
	TRotation // three angles
	TScale    // three positive plain numbers
	TProfile  // circle(r, n)
	TAngleRange
	TCount // positive integer, or three of them as a tuple
	// TNames is a tuple of distinct names, such as a loft's columns.
	TNames
	// TExpr is an expression kept as written and evaluated later, once for
	// each place it applies (a loft's vertex rule).
	TExpr
	// TPixels is a whole, positive number of pixels, such as 64px.
	TPixels
	// TGrid is a pair of positive whole numbers, (columns, rows).
	TGrid
	// TPaint is a painter call such as finish.gloss(), or a colour.
	TPaint
	// TTexture is a texture reference, or paint(atlas).
	TTexture
)

// Namespace names a set of declarations that a reference slot searches.
type Namespace uint8

const (
	NsNone     Namespace = iota
	NsGeometry           // parts, named shapes and arrays
	NsMaterial
	NsSocket
	NsCollider
	NsOutput
	NsTexture
)

func (n Namespace) String() string {
	switch n {
	case NsGeometry:
		return "geometry"
	case NsMaterial:
		return "material"
	case NsSocket:
		return "socket"
	case NsCollider:
		return "collider"
	case NsOutput:
		return "output"
	case NsTexture:
		return "texture"
	}
	return "none"
}

// Type is a slot type.
type Type struct {
	Kind TypeKind
	Enum []string  // for TEnum
	Ns   Namespace // for TRef, TRefList
}

// Dim returns the dimension of a numeric type, and false for other types.
func (t Type) Dim() (units.Dim, bool) {
	switch t.Kind {
	case TLength, TPoint, TExtent:
		return units.Length, true
	case TAngle, TRotation, TAngleRange:
		return units.Angle, true
	case TNumber, TInteger, TScale, TCount, TGrid:
		return units.Number, true
	case TPixels:
		return units.Pixels, true
	case TDuration:
		return units.Duration, true
	case TBytes:
		return units.Bytes, true
	}
	return units.Dim{}, false
}

// Name is the type as the reference writes it.
func (t Type) Name() string {
	switch t.Kind {
	case TLength:
		return "length"
	case TAngle:
		return "angle"
	case TNumber:
		return "number"
	case TInteger:
		return "integer"
	case TDuration:
		return "duration"
	case TBytes:
		return "bytes"
	case TSwitch:
		return "switch"
	case TColor:
		return "colour"
	case TString:
		return "string"
	case TStringList:
		return "list of strings"
	case TAxis:
		return "axis"
	case TEnum:
		s := "one of"
		for i, e := range t.Enum {
			if i > 0 {
				s += ","
			}
			s += " " + e
		}
		return s
	case TLabel:
		return "label"
	case TRef:
		return "reference to a " + t.Ns.String()
	case TRefList:
		return "list of " + t.Ns.String() + " references"
	case TPoint:
		return "point (three lengths)"
	case TExtent:
		return "extent (three lengths)"
	case TRotation:
		return "rotation (three angles)"
	case TScale:
		return "scale (three positive numbers)"
	case TProfile:
		return "profile"
	case TAngleRange:
		return "angle range"
	case TCount:
		return "count (integer, or three for a grid)"
	case TNames:
		return "tuple of names"
	case TExpr:
		return "expression"
	case TPixels:
		return "pixels (a whole number, such as 64px)"
	case TGrid:
		return "grid (columns, rows)"
	case TPaint:
		return "painter call or colour"
	case TTexture:
		return "texture reference or paint(atlas)"
	}
	return "?"
}

// Field is one setting of a kind.
type Field struct {
	Name string
	Type Type
	// Required: the field has no default.
	Required bool
	// Default is the value when the field is absent; nil with Derived == ""
	// means the field has no effect when absent.
	Default *units.Value
	// Derived describes a default computed from other fields, in words.
	Derived string
	// Positive requires a value greater than zero; NonNegative, at least zero.
	Positive, NonNegative bool
	// Min and Max are inclusive bounds in canonical units.
	Min, Max *float64
	// AtLeast is a minimum for TInteger (for example 3 segments).
	// Runtime: the value may depend on a runtime parameter.
	Runtime bool
	// Unsupported, when set, names why this valid field is outside the
	// compiler's subset. Using it reports GW-UNSUPPORTED.
	Unsupported string
	Doc         string
}

// Column is one column of a row.
type Column struct {
	Name string
	Type Type
	// NonNegative requires a value of at least zero.
	NonNegative bool
}

// RowSpec is a row a kind accepts, such as point (x, y, z).
type RowSpec struct {
	Name    string
	Columns []Column
	// MinRows is the least number of rows (after sampled rows expand).
	MinRows int
	// Dynamic: the columns are set by a field of the owning kind (a loft's
	// `columns`), and Columns lists the default layout.
	Dynamic bool
}

// NameRule says whether a block of this kind takes a declared name.
type NameRule uint8

const (
	NameNone NameRule = iota
	NameOptional
	NameRequired
)

// Context is where a kind may appear.
type Context uint8

const (
	// CtxDocument: a declaration at the top level or one of its bodies.
	CtxDocument Context = iota + 1
	// CtxPart: a block inside a part, array, mirror or when.
	CtxPart
	// CtxShape: a block inside a shape (bevel, path).
	CtxShape
	// CtxCollision: a block inside collision or a mirror in it.
	CtxCollision
	// CtxAtlas: a block inside an atlas texture.
	CtxAtlas
)

// Kind is a declaration kind: what its body accepts.
type Kind struct {
	Name    string
	Context Context
	Doc     string
	Fields  []Field
	// Rows the body accepts.
	Rows []RowSpec
	// Blocks lists child block kinds a shape-like body accepts (bevel, path).
	Blocks []string
	// Contains is true for kinds whose body holds part items: part, array,
	// mirror.
	Contains bool
	// Open: the body accepts settings that are not listed (Selena descriptor
	// parameters, `set` assignments, acceptance settings).
	Open  bool
	Names NameRule
	// Ns is the namespace the declared name enters.
	Ns Namespace
	// IsShape marks kinds that make triangles.
	IsShape bool
}

// Field returns the field called name.
func (k *Kind) Field(name string) (*Field, bool) {
	for i := range k.Fields {
		if k.Fields[i].Name == name {
			return &k.Fields[i], true
		}
	}
	return nil, false
}

// Row returns the row spec called name.
func (k *Kind) Row(name string) (*RowSpec, bool) {
	for i := range k.Rows {
		if k.Rows[i].Name == name {
			return &k.Rows[i], true
		}
	}
	return nil, false
}

// FieldNames returns the field names in table order.
func (k *Kind) FieldNames() []string {
	out := make([]string, len(k.Fields))
	for i, f := range k.Fields {
		out[i] = f.Name
	}
	return out
}

// RowNames returns the row names in table order.
func (k *Kind) RowNames() []string {
	out := make([]string, len(k.Rows))
	for i, r := range k.Rows {
		out[i] = r.Name
	}
	return out
}

// Suggest returns the candidate nearest to name, if one is near enough to be a
// likely misspelling. Ties go to the first in sorted order.
func Suggest(name string, candidates []string) (string, bool) {
	cs := append([]string(nil), candidates...)
	sort.Strings(cs)
	best, bestD := "", 1<<30
	for _, c := range cs {
		d := editDistance(name, c)
		if d < bestD {
			best, bestD = c, d
		}
	}
	limit := 2
	if n := len(name) / 3; n > limit {
		limit = n
	}
	if best == "" || bestD > limit || bestD >= len(name) {
		return "", false
	}
	return best, true
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
