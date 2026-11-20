package units

import (
	"fmt"
	"math/big"
	"strings"
)

// DegToRad is the binary64 constant nearest to pi/180.
const DegToRad = 0.017453292519943295

// TwoPi is the binary64 constant nearest to 2*pi.
const TwoPi = 6.283185307179586

// Defaults are the asset's default units for pending literals, as scale
// factors to metres and radians.
type Defaults struct {
	LengthScale float64
	AngleScale  float64
}

// DefaultUnits is the language's default: metres and degrees.
func DefaultUnits() Defaults { return Defaults{LengthScale: 1, AngleScale: DegToRad} }

// LengthUnits and AngleUnits list the unit names `asset` accepts.
var (
	LengthUnits = []string{"m", "mm", "cm", "km", "yd", "ft", "in"}
	AngleUnits  = []string{"deg", "rad", "turn"}
)

// LengthUnit returns the metres in one unit of name.
func LengthUnit(name string) (float64, bool) {
	r, ok := rationalUnit(name, Length)
	if !ok {
		return 0, false
	}
	f, _ := r.Float64()
	return f, true
}

// AngleUnit returns the radians in one unit of name.
func AngleUnit(name string) (float64, bool) {
	switch name {
	case "deg":
		return DegToRad, true
	case "rad":
		return 1, true
	case "turn":
		return TwoPi, true
	}
	return 0, false
}

type unitDef struct {
	suffix   string
	dim      Dim
	num, den int64
}

// Order matters: longer suffixes first so "mm" is not read as "m".
var unitDefs = []unitDef{
	{"mm", Length, 1, 1000}, {"cm", Length, 1, 100}, {"km", Length, 1000, 1},
	{"yd", Length, 9144, 10000}, {"ft", Length, 3048, 10000}, {"in", Length, 254, 10000},
	{"m", Length, 1, 1},
	{"ms", Duration, 1, 1000}, {"s", Duration, 1, 1},
	{"px", Pixels, 1, 1},
	{"%", Number, 1, 100},
	{"KiB", Bytes, 1024, 1}, {"MiB", Bytes, 1048576, 1}, {"GiB", Bytes, 1073741824, 1}, {"B", Bytes, 1, 1},
}

func rationalUnit(name string, dim Dim) (*big.Rat, bool) {
	for _, u := range unitDefs {
		if u.suffix == name && u.dim == dim {
			return big.NewRat(u.num, u.den), true
		}
	}
	return nil, false
}

// ParseNumber reads a number token such as 0.015, 6mm, 90deg or 7%. A number
// with no suffix is pending. A decimal with a rational unit factor is
// converted exactly and rounded once to binary64, ties to even. Degrees
// multiply the converted degree value by DegToRad, as the language reference
// requires.
func ParseNumber(text string) (Value, error) {
	i := 0
	for i < len(text) && (text[i] >= '0' && text[i] <= '9' || text[i] == '.') {
		i++
	}
	digits, suffix := text[:i], text[i:]
	if digits == "" {
		return Bad(), fmt.Errorf("%q is not a number", text)
	}
	r, ok := new(big.Rat).SetString(digits)
	if !ok {
		return Bad(), fmt.Errorf("%q is not a number", text)
	}
	if suffix == "" {
		f, _ := r.Float64()
		return Value{Kind: KNumber, Pending: true, Num: f}, nil
	}
	switch suffix {
	case "deg", "rad", "turn":
		f, _ := r.Float64()
		scale, _ := AngleUnit(suffix)
		if suffix == "rad" {
			return Ang(f), nil
		}
		return Ang(float64(f * scale)), nil
	}
	for _, u := range unitDefs {
		if u.suffix == suffix {
			scaled := new(big.Rat).Mul(r, big.NewRat(u.num, u.den))
			f, _ := scaled.Float64()
			return Value{Kind: KNumber, Dim: u.dim, Num: f}, nil
		}
	}
	return Bad(), fmt.Errorf("unknown unit %q", suffix)
}

// ParseColor reads #rrggbb or #rrggbbaa.
func ParseColor(text string) (Color, error) {
	h := strings.TrimPrefix(text, "#")
	if len(h) != 6 && len(h) != 8 {
		return Color{}, fmt.Errorf("colour %q needs six or eight hex digits", text)
	}
	var b [4]uint8
	b[3] = 255
	for i := 0; i < len(h)/2; i++ {
		var v uint8
		for _, c := range h[2*i : 2*i+2] {
			var d uint8
			switch {
			case c >= '0' && c <= '9':
				d = uint8(c - '0')
			case c >= 'a' && c <= 'f':
				d = uint8(c-'a') + 10
			case c >= 'A' && c <= 'F':
				d = uint8(c-'A') + 10
			default:
				return Color{}, fmt.Errorf("colour %q has a non-hex digit", text)
			}
			v = v<<4 | d
		}
		b[i] = v
	}
	return Color{b[0], b[1], b[2], b[3]}, nil
}

// Fix gives a pending number the dimension dim, using the default unit once.
// A non-pending value is returned unchanged.
func (d Defaults) Fix(v Value, dim Dim) Value {
	if v.Kind != KNumber || !v.Pending {
		return v
	}
	v.Pending = false
	v.Dim = dim
	switch dim {
	case Length:
		v.Num = float64(v.Num * d.LengthScale)
	case Angle:
		v.Num = float64(v.Num * d.AngleScale)
	}
	return v
}
