package units

import (
	"fmt"
	"math"
)

// ErrKind classifies an arithmetic failure so the caller can pick a diagnostic.
type ErrKind uint8

const (
	// ErrUnit: dimensions that cannot combine.
	ErrUnit ErrKind = iota + 1
	// ErrType: operands of the wrong type.
	ErrType
	// ErrDomain: an operation undefined for its operands.
	ErrDomain
	// ErrUnsupported: valid, but outside this compiler's subset.
	ErrUnsupported
)

// Error is a failed operation.
type Error struct {
	Kind ErrKind
	Msg  string
	// BareAngle is set when a trigonometric function got a number with no
	// unit: the caller can offer deg and rad as alternative edits.
	BareAngle bool
}

func (e *Error) Error() string { return e.Msg }

func errUnit(f string, a ...any) *Error   { return &Error{Kind: ErrUnit, Msg: fmt.Sprintf(f, a...)} }
func errType(f string, a ...any) *Error   { return &Error{Kind: ErrType, Msg: fmt.Sprintf(f, a...)} }
func errDomain(f string, a ...any) *Error { return &Error{Kind: ErrDomain, Msg: fmt.Sprintf(f, a...)} }

func finite(v Value) (Value, *Error) {
	if v.Kind == KNumber {
		if math.IsNaN(v.Num) || math.IsInf(v.Num, 0) {
			return Bad(), errDomain("the result is not a finite number")
		}
		if v.Num == 0 {
			v.Num = 0 // canonical zero: no negative zero
		}
	}
	return v, nil
}

func runtime2(r Value, a, b Value) Value {
	r.Runtime = a.Runtime || b.Runtime
	return r
}

// unify gives two numbers a common dimension. A pending operand takes the
// dimension of its peer; two pending operands stay pending.
func unify(a, b Value, d Defaults, what string) (Value, Value, *Error) {
	switch {
	case a.Pending && b.Pending:
		return a, b, nil
	case a.Pending:
		a = d.Fix(a, b.Dim)
	case b.Pending:
		b = d.Fix(b, a.Dim)
	}
	if a.Dim != b.Dim {
		return a, b, errUnit("cannot %s a %s and a %s", what, a.Dim, b.Dim)
	}
	return a, b, nil
}

func needNumbers(op string, a, b Value) *Error {
	if a.Kind != KNumber || b.Kind != KNumber {
		return errType("cannot %s a %s and a %s", op, a.Describe(), b.Describe())
	}
	return nil
}

// Add returns a+b. Tuples add componentwise.
func Add(a, b Value, d Defaults) (Value, *Error) { return addSub(a, b, d, false) }

// Sub returns a-b.
func Sub(a, b Value, d Defaults) (Value, *Error) { return addSub(a, b, d, true) }

func addSub(a, b Value, d Defaults, sub bool) (Value, *Error) {
	op := "add"
	if sub {
		op = "subtract"
	}
	if a.Kind == KTuple && b.Kind == KTuple {
		if len(a.Elems) != len(b.Elems) {
			return Bad(), errType("cannot %s tuples of %d and %d", op, len(a.Elems), len(b.Elems))
		}
		out := make([]Value, len(a.Elems))
		for i := range out {
			v, err := addSub(a.Elems[i], b.Elems[i], d, sub)
			if err != nil {
				return Bad(), err
			}
			out[i] = v
		}
		return runtime2(Tuple(out...), a, b), nil
	}
	if err := needNumbers(op, a, b); err != nil {
		return Bad(), err
	}
	x, y, err := unify(a, b, d, op)
	if err != nil {
		return Bad(), err
	}
	r := x
	if sub {
		r.Num = x.Num - y.Num
	} else {
		r.Num = x.Num + y.Num
	}
	r.Pending = x.Pending && y.Pending
	r, err = finite(r)
	return runtime2(r, a, b), err
}

// Neg returns -a.
func Neg(a Value) (Value, *Error) {
	switch a.Kind {
	case KNumber:
		a.Num = -a.Num
		return finite(a)
	case KTuple:
		out := make([]Value, len(a.Elems))
		for i, e := range a.Elems {
			v, err := Neg(e)
			if err != nil {
				return Bad(), err
			}
			out[i] = v
		}
		a.Elems = out
		return a, nil
	}
	return Bad(), errType("cannot negate a %s", a.Describe())
}

// Mul returns a*b. A pending factor next to a dimensioned one is a plain
// scalar; a tuple times a scalar scales each component.
func Mul(a, b Value, d Defaults) (Value, *Error) {
	if a.Kind == KTuple && b.Kind == KNumber {
		return scaleTuple(a, b, d, false)
	}
	if a.Kind == KNumber && b.Kind == KTuple {
		return scaleTuple(b, a, d, false)
	}
	if err := needNumbers("multiply", a, b); err != nil {
		return Bad(), err
	}
	var r Value
	switch {
	case a.Pending && b.Pending:
		r = Value{Kind: KNumber, Pending: true, Num: float64(a.Num * b.Num)}
	case a.Pending:
		r = Value{Kind: KNumber, Dim: b.Dim, Pending: b.Dim == Number, Num: float64(a.Num * b.Num)}
	case b.Pending:
		r = Value{Kind: KNumber, Dim: a.Dim, Pending: a.Dim == Number, Num: float64(a.Num * b.Num)}
	default:
		r = Value{Kind: KNumber, Dim: a.Dim.Mul(b.Dim), Num: float64(a.Num * b.Num)}
	}
	r, err := finite(r)
	return runtime2(r, a, b), err
}

// Div returns a/b. Division by zero is an ErrDomain. A pending number divided
// by a dimensioned one is an ErrUnit: no reciprocal dimension is inferred.
func Div(a, b Value, d Defaults) (Value, *Error) {
	if a.Kind == KTuple && b.Kind == KNumber {
		return scaleTuple(a, b, d, true)
	}
	if err := needNumbers("divide", a, b); err != nil {
		return Bad(), err
	}
	if b.Num == 0 {
		return Bad(), errDomain("division by zero")
	}
	var r Value
	switch {
	case a.Pending && b.Pending:
		r = Value{Kind: KNumber, Pending: true, Num: a.Num / b.Num}
	case a.Pending && b.Dim == Number:
		r = Value{Kind: KNumber, Pending: true, Num: a.Num / b.Num}
	case a.Pending:
		return Bad(), errUnit("cannot divide a bare number by a %s: write the unit, or divide by the number first", b.Dim)
	case b.Pending:
		r = Value{Kind: KNumber, Dim: a.Dim, Pending: false, Num: a.Num / b.Num}
	default:
		r = Value{Kind: KNumber, Dim: a.Dim.Div(b.Dim), Num: a.Num / b.Num}
	}
	r, err := finite(r)
	return runtime2(r, a, b), err
}

func scaleTuple(t, s Value, d Defaults, div bool) (Value, *Error) {
	if !s.Pending && s.Dim != Number {
		return Bad(), errUnit("a tuple can only be scaled by a plain number, not a %s", s.Dim)
	}
	out := make([]Value, len(t.Elems))
	for i, e := range t.Elems {
		var v Value
		var err *Error
		if div {
			v, err = Div(e, s, d)
		} else {
			v, err = Mul(e, s, d)
		}
		if err != nil {
			return Bad(), err
		}
		out[i] = v
	}
	return runtime2(Tuple(out...), t, s), nil
}

// Compare evaluates a comparison operator: == != < <= > >=. Order needs like
// numeric dimensions. Equality also compares switches, strings, colours,
// labels and, componentwise, tuples.
func Compare(op string, a, b Value, d Defaults) (Value, *Error) {
	eq := op == "==" || op == "!="
	var res bool
	switch {
	case a.Kind == KNumber && b.Kind == KNumber:
		x, y, err := unify(a, b, d, "compare")
		if err != nil {
			return Bad(), err
		}
		switch op {
		case "==":
			res = x.Num == y.Num
		case "!=":
			res = x.Num != y.Num
		case "<":
			res = x.Num < y.Num
		case "<=":
			res = x.Num <= y.Num
		case ">":
			res = x.Num > y.Num
		case ">=":
			res = x.Num >= y.Num
		}
	case eq && a.Kind == b.Kind && (a.Kind == KSwitch || a.Kind == KString || a.Kind == KColor || a.Kind == KLabel || a.Kind == KAxis):
		same := a.Bool == b.Bool && a.Str == b.Str && a.Color == b.Color
		res = same == (op == "==")
	case eq && a.Kind == KTuple && b.Kind == KTuple && len(a.Elems) == len(b.Elems):
		all := true
		for i := range a.Elems {
			v, err := Compare("==", a.Elems[i], b.Elems[i], d)
			if err != nil {
				return Bad(), err
			}
			all = all && v.Bool
		}
		res = all == (op == "==")
	default:
		return Bad(), errType("cannot compare a %s with a %s using %s", a.Describe(), b.Describe(), op)
	}
	return runtime2(Switch(res), a, b), nil
}

// Coerce checks that v is a number of dimension want and returns it. A
// pending number takes the default unit for that dimension.
func Coerce(v Value, want Dim, d Defaults) (Value, *Error) {
	if v.Kind != KNumber {
		return Bad(), errType("expected a %s, found a %s", want, v.Describe())
	}
	if v.Pending {
		v = d.Fix(v, want)
		return v, nil
	}
	if v.Dim != want {
		return Bad(), errUnit("expected a %s, found a %s", want, v.Dim)
	}
	return v, nil
}

// Compatible reports whether a and b can unify (for the two branches of if).
func Compatible(a, b Value, d Defaults) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Kind == KNumber {
		_, _, err := unify(a, b, d, "unify")
		return err == nil
	}
	return true
}

const maxInt = 9007199254740991 // 2^53 - 1

// IsIntegral reports whether x is a whole number in the exact integer range.
func IsIntegral(x float64) bool {
	return x == math.Trunc(x) && math.Abs(x) <= maxInt
}
