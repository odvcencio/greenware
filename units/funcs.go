package units

import (
	"math"
	"sort"

	"m31labs.dev/greenware/det"
)

// Func describes a builtin function for messages and registry docs.
type Func struct {
	Name      string
	Signature string
	Arity     int
}

var funcs = []Func{
	{"abs", "abs(x: Q) -> Q", 1},
	{"acos", "acos(x: N) -> A", 1},
	{"asin", "asin(x: N) -> A", 1},
	{"atan2", "atan2(y: Q, x: Q) -> A", 2},
	{"clamp", "clamp(x: Q, lo: Q, hi: Q) -> Q", 3},
	{"cos", "cos(a: A) -> N", 1},
	{"degrees", "degrees(a: A) -> N", 1},
	{"floor", "floor(x: N) -> N", 1},
	{"lerp", "lerp(a: V, b: V, t: N) -> V", 3},
	{"max", "max(a: Q, b: Q) -> Q", 2},
	{"min", "min(a: Q, b: Q) -> Q", 2},
	{"mod", "mod(x: Q, period: Q) -> Q", 2},
	{"pow", "pow(base: N, exponent: N) -> N", 2},
	{"radians", "radians(a: A) -> N", 1},
	{"rot", "rot(x: A, y: A, z: A) -> rotation", 3},
	{"round", "round(x: N) -> N", 1},
	{"seconds", "seconds(x: N) -> D", 1},
	{"sin", "sin(a: A) -> N", 1},
	{"smoothstep", "smoothstep(lo: Q, hi: Q, x: Q) -> N", 3},
	{"sqrt", "sqrt(x: N) -> N", 1},
	{"tan", "tan(a: A) -> N", 1},
}

// Funcs returns the builtins in name order.
func Funcs() []Func { return funcs }

// FuncNames returns the builtin names in sorted order.
func FuncNames() []string {
	out := make([]string, len(funcs))
	for i, f := range funcs {
		out[i] = f.Name
	}
	sort.Strings(out)
	return out
}

// LookupFunc returns the builtin with the given name.
func LookupFunc(name string) (Func, bool) {
	for _, f := range funcs {
		if f.Name == name {
			return f, true
		}
	}
	return Func{}, false
}

func plain(v Value, what string, d Defaults) (Value, *Error) {
	if v.Kind != KNumber {
		return Bad(), errType("%s needs a number, found a %s", what, v.Describe())
	}
	if v.Pending {
		v = d.Fix(v, Number)
	}
	if v.Dim != Number {
		return Bad(), errUnit("%s needs a plain number, found a %s", what, v.Dim)
	}
	return v, nil
}

func angleArg(v Value, fn string, d Defaults) (Value, *Error) {
	if v.Kind != KNumber {
		return Bad(), errType("%s needs an angle, found a %s", fn, v.Describe())
	}
	if v.Pending {
		return Bad(), &Error{Kind: ErrUnit, BareAngle: true,
			Msg: fn + " needs an angle with a unit: write the argument with deg or rad"}
	}
	if v.Dim != Angle {
		return Bad(), errUnit("%s needs an angle, found a %s", fn, v.Dim)
	}
	return v, nil
}

func like(vs []Value, d Defaults, what string) ([]Value, *Error) {
	out := append([]Value(nil), vs...)
	for _, v := range out {
		if v.Kind != KNumber {
			return nil, errType("%s needs numbers, found a %s", what, v.Describe())
		}
	}
	// Find the first non-pending operand and fix the others to it.
	var ref *Value
	for i := range out {
		if !out[i].Pending {
			ref = &out[i]
			break
		}
	}
	if ref == nil {
		return out, nil
	}
	for i := range out {
		if out[i].Pending {
			out[i] = d.Fix(out[i], ref.Dim)
		}
		if out[i].Dim != ref.Dim {
			return nil, errUnit("%s needs like dimensions, found a %s and a %s", what, ref.Dim, out[i].Dim)
		}
	}
	return out, nil
}

func anyRuntime(vs []Value) bool {
	for _, v := range vs {
		if v.DependsOnRuntime() {
			return true
		}
	}
	return false
}

// Call evaluates a builtin on evaluated arguments.
func Call(name string, args []Value, d Defaults) (Value, *Error) {
	f, ok := LookupFunc(name)
	if !ok {
		return Bad(), errType("unknown function %q", name)
	}
	if len(args) != f.Arity {
		return Bad(), errType("%s takes %d argument(s), found %d", name, f.Arity, len(args))
	}
	v, err := call(name, args, d)
	if err != nil {
		return Bad(), err
	}
	v, err = finite(v)
	if err != nil {
		return Bad(), err
	}
	v.Runtime = anyRuntime(args)
	return v, nil
}

func call(name string, a []Value, d Defaults) (Value, *Error) {
	switch name {
	case "sin", "cos", "tan":
		x, err := angleArg(a[0], name, d)
		if err != nil {
			return Bad(), err
		}
		switch name {
		case "sin":
			return Num(det.Sin(x.Num)), nil
		case "cos":
			return Num(det.Cos(x.Num)), nil
		}
		if c := det.Cos(x.Num); c == 0 || math.Abs(c) < 1e-15 {
			return Bad(), errDomain("tan is undefined at this angle")
		}
		return Num(det.Tan(x.Num)), nil
	case "asin", "acos":
		x, err := plain(a[0], name, d)
		if err != nil {
			return Bad(), err
		}
		if x.Num < -1 || x.Num > 1 {
			return Bad(), errDomain("%s needs a value from -1 to 1, found %s", name, x.Format())
		}
		if name == "asin" {
			return Ang(det.Asin(x.Num)), nil
		}
		return Ang(det.Acos(x.Num)), nil
	case "atan2":
		v, err := like(a, d, "atan2")
		if err != nil {
			return Bad(), err
		}
		if v[0].Num == 0 && v[1].Num == 0 {
			return Bad(), errDomain("atan2(0, 0) is undefined")
		}
		return Ang(det.Atan2(v[0].Num, v[1].Num)), nil
	case "sqrt":
		x, err := plain(a[0], "sqrt", d)
		if err != nil {
			return Bad(), err
		}
		if x.Num < 0 {
			return Bad(), errDomain("sqrt of a negative number")
		}
		return Num(det.Sqrt(x.Num)), nil
	case "pow":
		b, err := plain(a[0], "pow", d)
		if err != nil {
			return Bad(), err
		}
		e, err := plain(a[1], "pow", d)
		if err != nil {
			return Bad(), err
		}
		if e.Num == 0 {
			return Num(1), nil
		}
		whole := IsIntegral(e.Num)
		switch {
		case b.Num == 0:
			if e.Num < 0 {
				return Bad(), errDomain("pow of zero with a negative exponent")
			}
			return Num(0), nil
		case whole && math.Abs(e.Num) <= 1024:
			// Exact repeated squaring, for any sign of base.
			return Num(det.Pow(b.Num, int(e.Num))), nil
		case b.Num < 0 && !whole:
			return Bad(), errDomain("pow of a negative number needs a whole exponent")
		case b.Num < 0:
			return Bad(), &Error{Kind: ErrUnsupported, Msg: "pow of a negative number with a very large exponent is outside this compiler's subset"}
		}
		// A positive base with a real exponent: exp(e * log(b)).
		return Num(Exp(float64(e.Num * Log(b.Num)))), nil
	case "abs":
		if a[0].Kind != KNumber {
			return Bad(), errType("abs needs a number, found a %s", a[0].Describe())
		}
		r := a[0]
		r.Num = math.Abs(r.Num)
		return r, nil
	case "min", "max":
		v, err := like(a, d, name)
		if err != nil {
			return Bad(), err
		}
		r := v[0]
		if (name == "min" && v[1].Num < r.Num) || (name == "max" && v[1].Num > r.Num) {
			r = v[1]
		}
		r.Pending = v[0].Pending && v[1].Pending
		return r, nil
	case "clamp":
		v, err := like(a, d, "clamp")
		if err != nil {
			return Bad(), err
		}
		if v[1].Num > v[2].Num {
			return Bad(), errDomain("clamp needs lo <= hi")
		}
		r := v[0]
		r.Num = math.Min(math.Max(v[0].Num, v[1].Num), v[2].Num)
		r.Pending = v[0].Pending && v[1].Pending && v[2].Pending
		return r, nil
	case "lerp":
		t, err := plain(a[2], "lerp", d)
		if err != nil {
			return Bad(), err
		}
		diff, err := Sub(a[1], a[0], d)
		if err != nil {
			return Bad(), err
		}
		scaled, err := Mul(diff, t, d)
		if err != nil {
			return Bad(), err
		}
		return Add(a[0], scaled, d)
	case "smoothstep":
		v, err := like(a, d, "smoothstep")
		if err != nil {
			return Bad(), err
		}
		lo, hi, x := v[0].Num, v[1].Num, v[2].Num
		if !(lo < hi) {
			return Bad(), errDomain("smoothstep needs lo < hi")
		}
		u := math.Min(math.Max((x-lo)/(hi-lo), 0), 1)
		return Num(float64(float64(u*u) * (3 - float64(2*u)))), nil
	case "mod":
		v, err := like(a, d, "mod")
		if err != nil {
			return Bad(), err
		}
		if !(v[1].Num > 0) {
			return Bad(), errDomain("mod needs a positive period")
		}
		r := v[0]
		r.Num = v[0].Num - float64(v[1].Num*math.Floor(v[0].Num/v[1].Num))
		if r.Num >= v[1].Num || r.Num < 0 {
			r.Num = 0
		}
		r.Pending = v[0].Pending && v[1].Pending
		return r, nil
	case "floor", "round":
		x, err := plain(a[0], name, d)
		if err != nil {
			return Bad(), err
		}
		r := math.Floor(x.Num)
		if name == "round" {
			r = math.RoundToEven(x.Num)
		}
		if !IsIntegral(r) {
			return Bad(), errDomain("%s result is outside the integer range", name)
		}
		return Num(r), nil
	case "radians":
		x, err := angleArg(a[0], name, d)
		if err != nil {
			return Bad(), err
		}
		return Num(x.Num), nil
	case "degrees":
		x, err := angleArg(a[0], name, d)
		if err != nil {
			return Bad(), err
		}
		return Num(x.Num / DegToRad), nil
	case "seconds":
		x, err := plain(a[0], name, d)
		if err != nil {
			return Bad(), err
		}
		return Dur(x.Num), nil
	case "rot":
		out := make([]Value, 3)
		for i := range out {
			v, err := Coerce(a[i], Angle, d)
			if err != nil {
				return Bad(), err
			}
			out[i] = v
		}
		return Tuple(out...), nil
	}
	return Bad(), errType("unknown function %q", name)
}
