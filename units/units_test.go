package units_test

import (
	"math"
	"testing"

	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/units"
)

func parse(t *testing.T, s string) units.Value {
	t.Helper()
	v, err := units.ParseNumber(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParseNumber(t *testing.T) {
	cases := []struct {
		text string
		dim  units.Dim
		want float64
		pend bool
	}{
		{"3.05m", units.Length, 3.05, false},
		{"6mm", units.Length, 0.006, false},
		{"2cm", units.Length, 0.02, false},
		{"1ft", units.Length, 0.3048, false},
		{"2yd", units.Length, 1.8288, false},
		{"1in", units.Length, 0.0254, false},
		{"0.015", units.Number, 0.015, true},
		{"90deg", units.Angle, float64(90 * units.DegToRad), false},
		{"1.39rad", units.Angle, 1.39, false},
		{"0.25turn", units.Angle, float64(0.25 * units.TwoPi), false},
		{"7%", units.Number, 0.07, false},
		{"240ms", units.Duration, 0.24, false},
		{"96KiB", units.Bytes, 98304, false},
		{"128px", units.Pixels, 128, false},
	}
	for _, c := range cases {
		v := parse(t, c.text)
		if v.Dim != c.dim || v.Pending != c.pend || v.Num != c.want {
			t.Errorf("%s = %+v, want dim %s value %v pending %v", c.text, v, c.dim, c.want, c.pend)
		}
	}
	if _, err := units.ParseNumber("5furlong"); err == nil {
		t.Error("an unknown unit must fail")
	}
}

func TestColor(t *testing.T) {
	c, err := units.ParseColor("#1d4e89")
	if err != nil || c != (units.Color{R: 0x1d, G: 0x4e, B: 0x89, A: 255}) {
		t.Errorf("%v %v", c, err)
	}
	c, _ = units.ParseColor("#00000000")
	if c.A != 0 {
		t.Error("alpha lost")
	}
	if _, err := units.ParseColor("#12345"); err == nil {
		t.Error("five digits must fail")
	}
}

func TestDimensionRules(t *testing.T) {
	d := units.DefaultUnits()
	m, deg := parse(t, "2m"), parse(t, "30deg")
	pend := parse(t, "2")
	n := units.Finish(pend)

	if _, err := units.Add(m, deg, d); err == nil || err.Kind != units.ErrUnit {
		t.Errorf("length + angle: %v", err)
	}
	if _, err := units.Add(m, n, d); err == nil || err.Kind != units.ErrUnit {
		t.Errorf("length + number: %v", err)
	}
	if v, err := units.Add(m, pend, d); err != nil || v.Dim != units.Length || v.Num != 4 {
		t.Errorf("length + bare: %v %v", v, err)
	}
	if v, err := units.Mul(m, n, d); err != nil || v.Dim != units.Length || v.Num != 4 {
		t.Errorf("length * number: %v %v", v, err)
	}
	if v, err := units.Div(m, m, d); err != nil || v.Dim != units.Number || v.Num != 1 {
		t.Errorf("length / length: %v %v", v, err)
	}
	if v, err := units.Mul(m, m, d); err != nil || v.Dim != (units.Dim{Length: 2}) {
		t.Errorf("length * length: %v %v", v, err)
	} else if _, err := units.Coerce(v, units.Length, d); err == nil {
		t.Error("an area must not fill a length slot")
	}
	if _, err := units.Div(pend, m, d); err == nil || err.Kind != units.ErrUnit {
		t.Errorf("bare / length: %v", err)
	}
	if _, err := units.Div(m, parse(t, "0"), d); err == nil || err.Kind != units.ErrDomain {
		t.Errorf("division by zero: %v", err)
	}
	if v, _ := units.Mul(pend, pend, d); !v.Pending {
		t.Error("two bare numbers make a bare number")
	}
	if v, _ := units.Mul(pend, n, d); !v.Pending {
		t.Error("a bare number times a plain number stays bare")
	}
	// A foot document reads a bare 2 next to 1m as 2 feet.
	ft := units.Defaults{LengthScale: 0.3048, AngleScale: units.DegToRad}
	if v, _ := units.Add(parse(t, "1m"), pend, ft); v.Num != 1+float64(2*0.3048) {
		t.Errorf("1m + 2 in feet = %v", v.Num)
	}
}

func TestFunctions(t *testing.T) {
	d := units.DefaultUnits()
	call := func(name string, args ...units.Value) (units.Value, *units.Error) { return units.Call(name, args, d) }
	ninety := parse(t, "90deg")
	if v, err := call("sin", ninety); err != nil || v.Num != det.Sin(float64(90*units.DegToRad)) {
		t.Errorf("sin: %v %v", v, err)
	}
	if v, _ := call("cos", parse(t, "0deg")); v.Num != 1 {
		t.Errorf("cos 0 = %v", v.Num)
	}
	if _, err := call("sin", parse(t, "0.22")); err == nil || !err.BareAngle {
		t.Errorf("bare sin: %v", err)
	}
	if _, err := call("sin", parse(t, "1m")); err == nil || err.BareAngle || err.Kind != units.ErrUnit {
		t.Errorf("sin of a length: %v", err)
	}
	if v, _ := call("sqrt", parse(t, "9")); v.Num != 3 {
		t.Errorf("sqrt = %v", v.Num)
	}
	if _, err := call("sqrt", units.Num(-1)); err == nil || err.Kind != units.ErrDomain {
		t.Errorf("sqrt(-1): %v", err)
	}
	if v, _ := call("pow", parse(t, "2"), parse(t, "10")); v.Num != 1024 {
		t.Errorf("pow = %v", v.Num)
	}
	if _, err := call("pow", parse(t, "2"), parse(t, "1.5")); err == nil || err.Kind != units.ErrUnsupported {
		t.Errorf("fractional pow: %v", err)
	}
	if v, _ := call("mod", units.Len(-1), parse(t, "3m")); v.Num != 2 || v.Dim != units.Length {
		t.Errorf("mod = %v", v)
	}
	if v, _ := call("round", parse(t, "2.5")); v.Num != 2 {
		t.Errorf("round ties to even: %v", v.Num)
	}
	if v, _ := call("floor", units.Num(-0.5)); v.Num != -1 {
		t.Errorf("floor: %v", v.Num)
	}
	if v, _ := call("clamp", parse(t, "9m"), parse(t, "1m"), parse(t, "3m")); v.Num != 3 {
		t.Errorf("clamp: %v", v.Num)
	}
	if v, _ := call("lerp", parse(t, "0m"), parse(t, "10m"), parse(t, "0.25")); v.Num != 2.5 {
		t.Errorf("lerp: %v", v.Num)
	}
	if v, _ := call("smoothstep", parse(t, "0"), parse(t, "1"), parse(t, "0.5")); v.Num != 0.5 {
		t.Errorf("smoothstep: %v", v.Num)
	}
	if v, _ := call("degrees", ninety); math.Abs(v.Num-90) > 1e-12 {
		t.Errorf("degrees: %v", v.Num)
	}
	if v, _ := call("rot", parse(t, "0"), parse(t, "90"), parse(t, "0deg")); v.Kind != units.KTuple || v.Elems[1].Num != float64(90*units.DegToRad) {
		t.Errorf("rot: %v", v)
	}
	if _, err := call("abs"); err == nil {
		t.Error("abs with no argument must fail")
	}
	if v, _ := call("min", parse(t, "3"), parse(t, "2m")); v.Dim != units.Length || v.Num != 2 {
		t.Errorf("min of bare and length: %v", v)
	}
}

// Every product that a later add uses is rounded to float64 first, so a
// target with fused multiply-add gives the same bits.
func TestNoFusion(t *testing.T) {
	d := units.DefaultUnits()
	a, b, c := 0.1, 0.3, -0.03
	prod, _ := units.Mul(units.Len(a), units.Num(b), d)
	sum, _ := units.Add(prod, units.Len(c), d)
	want := float64(a*b) + c
	if sum.Num != want {
		t.Errorf("got %v, want the unfused %v", sum.Num, want)
	}
}

func TestFormat(t *testing.T) {
	if got := units.Len(1.2199999999999998).Format(); got != "1.22m" {
		t.Errorf("%q", got)
	}
	if got := units.Tuple(units.Num(1), units.Switch(true)).Format(); got != "(1, on)" {
		t.Errorf("%q", got)
	}
}

func TestJSON(t *testing.T) {
	cases := map[string]units.Value{
		`{"dim":"length","value":3.05}`:              units.Len(3.05),
		`true`:                                       units.Switch(true),
		`{"color":"#1d4e89ff","runtime":true}`:       units.Col(units.Color{R: 0x1d, G: 0x4e, B: 0x89, A: 255}).WithRuntime(true),
		`{"tuple":[{"dim":"number","value":1},"x"]}`: units.Tuple(units.Num(1), units.Str("x")),
		`{"axis":"-z"}`:                              units.Axis("-z"),
		`{"dim":"pending","value":2}`:                {Kind: units.KNumber, Pending: true, Num: 2},
	}
	for want, v := range cases {
		b, err := v.MarshalJSON()
		if err != nil || string(b) != want {
			t.Errorf("got %s, want %s (%v)", b, want, err)
		}
	}
}
