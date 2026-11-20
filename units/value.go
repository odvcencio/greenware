// Package units holds the values, dimensions and arithmetic of Greenware
// expressions.
//
// Lengths are metres, angles are radians, durations are seconds and byte sizes
// are bytes once a number has a value. A bare literal is "pending": it takes
// its dimension from the expression or slot around it, using the asset's
// default length and angle units (language reference, section 2.3).
//
// Scalar math that reaches a written value uses m31labs.dev/greenware/det, and
// every product is wrapped in float64() so that no target fuses a multiply
// with a later add.
package units

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Dim is a dimension as exponents of the base dimensions. The zero Dim is a
// plain number (a ratio, a count, a scale).
type Dim struct {
	Length, Angle, Time, Pixel, Byte int8
}

// Common dimensions.
var (
	Number   = Dim{}
	Length   = Dim{Length: 1}
	Angle    = Dim{Angle: 1}
	Duration = Dim{Time: 1}
	Pixels   = Dim{Pixel: 1}
	Bytes    = Dim{Byte: 1}
)

// Mul returns the dimension of a product.
func (d Dim) Mul(o Dim) Dim {
	return Dim{d.Length + o.Length, d.Angle + o.Angle, d.Time + o.Time, d.Pixel + o.Pixel, d.Byte + o.Byte}
}

// Div returns the dimension of a quotient.
func (d Dim) Div(o Dim) Dim {
	return Dim{d.Length - o.Length, d.Angle - o.Angle, d.Time - o.Time, d.Pixel - o.Pixel, d.Byte - o.Byte}
}

func (d Dim) String() string {
	if d == Number {
		return "number"
	}
	parts := []struct {
		name string
		exp  int8
	}{{"length", d.Length}, {"angle", d.Angle}, {"duration", d.Time}, {"pixels", d.Pixel}, {"bytes", d.Byte}}
	var num, den []string
	for _, p := range parts {
		switch {
		case p.exp == 1:
			num = append(num, p.name)
		case p.exp > 1:
			num = append(num, fmt.Sprintf("%s^%d", p.name, p.exp))
		case p.exp == -1:
			den = append(den, p.name)
		case p.exp < -1:
			den = append(den, fmt.Sprintf("%s^%d", p.name, -p.exp))
		}
	}
	s := strings.Join(num, "*")
	if s == "" {
		s = "1"
	}
	if len(den) > 0 {
		s += "/" + strings.Join(den, "*")
	}
	return s
}

// Kind is the kind of a Value.
type Kind uint8

const (
	// Invalid marks a value that already produced a diagnostic. Operations on
	// it produce Invalid with no further error.
	Invalid Kind = iota
	KNumber
	KSwitch
	KColor
	KString
	KTuple
	KList
	KAxis
	KLabel
	KRef
	KProfile
	KRange
)

// Color is a straight sRGB colour with 8-bit channels.
type Color struct{ R, G, B, A uint8 }

// Hex returns the colour as #rrggbbaa.
func (c Color) Hex() string { return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A) }

// Value is a typed value. Only the fields for its Kind mean anything.
type Value struct {
	Kind Kind
	// Dim and Num: a number in canonical units. A pending number holds the
	// literal as written in Num, with no unit scaling.
	Dim     Dim
	Pending bool
	Num     float64
	Bool    bool
	Str     string // string text, label, axis ("+y"), reference name, profile function
	Color   Color
	Elems   []Value // tuple, list, range ends, profile arguments
	// Runtime is true when the value depends on a runtime parameter.
	Runtime bool
}

// Constructors.
func Num(v float64) Value      { return Value{Kind: KNumber, Num: v} }
func Len(m float64) Value      { return Value{Kind: KNumber, Dim: Length, Num: m} }
func Ang(rad float64) Value    { return Value{Kind: KNumber, Dim: Angle, Num: rad} }
func Dur(s float64) Value      { return Value{Kind: KNumber, Dim: Duration, Num: s} }
func Byt(b float64) Value      { return Value{Kind: KNumber, Dim: Bytes, Num: b} }
func Switch(b bool) Value      { return Value{Kind: KSwitch, Bool: b} }
func Str(s string) Value       { return Value{Kind: KString, Str: s} }
func Col(c Color) Value        { return Value{Kind: KColor, Color: c} }
func Axis(s string) Value      { return Value{Kind: KAxis, Str: s} }
func Label(s string) Value     { return Value{Kind: KLabel, Str: s} }
func Ref(s string) Value       { return Value{Kind: KRef, Str: s} }
func Tuple(vs ...Value) Value  { return Value{Kind: KTuple, Elems: vs} }
func List(vs ...Value) Value   { return Value{Kind: KList, Elems: vs} }
func Pair(lo, hi Value) Value  { return Value{Kind: KRange, Elems: []Value{lo, hi}} }
func Bad() Value               { return Value{} }
func (v Value) IsNumber() bool { return v.Kind == KNumber }
func (v Value) Valid() bool    { return v.Kind != Invalid }

// WithRuntime returns v marked as depending on a runtime parameter.
func (v Value) WithRuntime(r bool) Value {
	if r {
		v.Runtime = true
	}
	return v
}

// Vec3 returns the three numbers of a 3-element tuple.
func (v Value) Vec3() ([3]float64, bool) {
	var out [3]float64
	if v.Kind != KTuple || len(v.Elems) != 3 {
		return out, false
	}
	for i, e := range v.Elems {
		if e.Kind != KNumber {
			return out, false
		}
		out[i] = e.Num
	}
	return out, true
}

// DependsOnRuntime reports whether v or any element depends on a runtime parameter.
func (v Value) DependsOnRuntime() bool {
	if v.Runtime {
		return true
	}
	for _, e := range v.Elems {
		if e.DependsOnRuntime() {
			return true
		}
	}
	return false
}

// Equal reports whether a and b are bit-equal (it ignores the Runtime flag).
func Equal(a, b Value) bool {
	if a.Kind != b.Kind || a.Dim != b.Dim || a.Pending != b.Pending || a.Bool != b.Bool ||
		a.Str != b.Str || a.Color != b.Color || len(a.Elems) != len(b.Elems) ||
		math.Float64bits(a.Num) != math.Float64bits(b.Num) {
		return false
	}
	for i := range a.Elems {
		if !Equal(a.Elems[i], b.Elems[i]) {
			return false
		}
	}
	return true
}

// Describe names the type of v for a message: "length", "angle", "switch".
func (v Value) Describe() string {
	switch v.Kind {
	case Invalid:
		return "invalid value"
	case KNumber:
		if v.Pending {
			return "bare number"
		}
		return v.Dim.String()
	case KSwitch:
		return "switch"
	case KColor:
		return "colour"
	case KString:
		return "string"
	case KTuple:
		return fmt.Sprintf("tuple of %d", len(v.Elems))
	case KList:
		return "list"
	case KAxis:
		return "axis"
	case KLabel:
		return "label"
	case KRef:
		return "reference"
	case KProfile:
		return "profile"
	case KRange:
		return "range"
	}
	return "value"
}

// Format writes v for a message, with lengths in metres and angles in
// degrees, using at most nine significant digits so the text is stable.
func (v Value) Format() string {
	f := func(x float64) string { return strconv.FormatFloat(x, 'g', 9, 64) }
	switch v.Kind {
	case KNumber:
		if v.Pending {
			return f(v.Num)
		}
		switch v.Dim {
		case Number:
			return f(v.Num)
		case Length:
			return f(v.Num) + "m"
		case Angle:
			return f(float64(v.Num/DegToRad)) + "deg"
		case Duration:
			return f(v.Num) + "s"
		case Bytes:
			return f(v.Num) + "B"
		}
		return f(v.Num) + " " + v.Dim.String()
	case KSwitch:
		if v.Bool {
			return "on"
		}
		return "off"
	case KColor:
		return v.Color.Hex()
	case KString:
		return strconv.Quote(v.Str)
	case KAxis, KLabel, KRef:
		return v.Str
	case KTuple, KList, KRange, KProfile:
		parts := make([]string, len(v.Elems))
		for i, e := range v.Elems {
			parts[i] = e.Format()
		}
		switch v.Kind {
		case KList:
			return "[" + strings.Join(parts, ", ") + "]"
		case KRange:
			return strings.Join(parts, "..")
		case KProfile:
			return v.Str + "(" + strings.Join(parts, ", ") + ")"
		}
		return "(" + strings.Join(parts, ", ") + ")"
	}
	return "?"
}

// Finish ends inference at a binding: a pending number becomes a plain
// number, in tuples and lists too (language reference, section 2.3, rule 5).
func Finish(v Value) Value {
	if v.Kind == KNumber && v.Pending {
		v.Pending = false
		v.Dim = Number
	}
	if len(v.Elems) > 0 {
		es := make([]Value, len(v.Elems))
		for i, e := range v.Elems {
			es[i] = Finish(e)
		}
		v.Elems = es
	}
	return v
}
