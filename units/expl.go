package units

import "math"

// Exp and Log are the exponential and natural logarithm that `pow` uses for a
// real exponent. They use only +, -, *, / and exact bit operations
// (math.Frexp, math.Ldexp, math.Floor), and every multiply is wrapped in
// float64(...), so a fused multiply-add on arm64 cannot change the bits and
// every target gives the same result. Both agree with the C library to about
// 1e-15 relative error on the range checked in the tests.

// ln2Hi has trailing zero bits, so k*ln2Hi is exact for |k| < 2^11. The
// pair ln2Hi + ln2Lo equals ln 2 to about 1e-26 (the fdlibm split).
const (
	ln2Hi = 6.93147180369123816490e-01
	ln2Lo = 1.90821492927058770002e-10
)

// Exp returns e**x. It returns +Inf above about 709.78 and 0 below about
// -745.13.
func Exp(x float64) float64 {
	switch {
	case math.IsNaN(x):
		return x
	case x > 709.782712893384:
		return math.Inf(1)
	case x < -745.1332191019412:
		return 0
	}
	k := math.Floor(float64(x/ln2Hi) + 0.5)
	r := float64(x - float64(k*ln2Hi))
	r = float64(r - float64(k*ln2Lo))
	// Taylor series of e**r for |r| <= 0.35, by Horner's rule from degree 20.
	p := 1.0
	for i := 20; i >= 1; i-- {
		p = 1 + float64(float64(r*p)/float64(i))
	}
	return math.Ldexp(p, int(k))
}

// Log returns the natural logarithm of x. It returns NaN for x < 0, -Inf for
// 0 and +Inf for +Inf.
func Log(x float64) float64 {
	switch {
	case math.IsNaN(x) || x < 0:
		return math.NaN()
	case x == 0:
		return math.Inf(-1)
	case math.IsInf(x, 1):
		return x
	}
	m, e := math.Frexp(x) // x = m * 2^e, m in [0.5, 1)
	if m < math.Sqrt2/2 {
		m *= 2
		e--
	}
	// log m = 2 atanh(s), s = (m-1)/(m+1), |s| <= 0.1716.
	s := (m - 1) / (m + 1)
	z := float64(s * s)
	// 1 + z/3 + z^2/5 + ... to z^14/29, by Horner's rule.
	p := 0.0
	for i := 14; i >= 1; i-- {
		p = float64(1/float64(2*i+1)) + float64(z*p)
	}
	lm := float64(2 * s)
	lm = float64(lm * (1 + float64(z*p)))
	fe := float64(e)
	return float64(fe*ln2Hi) + (lm + float64(fe*ln2Lo))
}
