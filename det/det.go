package det

import "math"

// Constants for range reduction. pio2a, pio2b and pio2c are the leading
// 33-bit pieces of pi/2, so k*pio2a, k*pio2b and k*pio2c are exact for any
// integer k below 2^20. pio2t is the remainder after the three pieces.
const (
	twoOverPi = 0.6366197723675814
	pio2a     = 1.5707963267341256
	pio2b     = 6.077100506303966e-11
	pio2c     = 2.0222662487111665e-21
	pio2t     = 8.4784276603689e-32
	pio2      = 1.5707963267948966
	pi        = 3.141592653589793
)

// MulAdd returns a*b+c with the product rounded to float64 before the add.
// It never fuses, so it is the same on every architecture.
func MulAdd(a, b, c float64) float64 { return float64(a*b) + c }

// Round32 rounds x to the nearest float32 (ties to even) and returns it.
func Round32(x float64) float32 { return float32(x) }

// Lerp returns a*(1-t)+b*t. It returns exactly a at t=0 and exactly b at t=1.
func Lerp(a, b, t float64) float64 {
	return float64(a*(1-t)) + float64(b*t)
}

// Sqrt returns the square root of x. IEEE 754 requires it to be correctly
// rounded, so it is identical on every architecture.
func Sqrt(x float64) float64 { return math.Sqrt(x) }

// Hypot returns sqrt(x*x+y*y) without undue overflow or underflow.
func Hypot(x, y float64) float64 {
	if math.IsInf(x, 0) || math.IsInf(y, 0) {
		return math.Inf(1)
	}
	if math.IsNaN(x) || math.IsNaN(y) {
		return math.NaN()
	}
	x, y = abs(x), abs(y)
	if x < y {
		x, y = y, x
	}
	if x == 0 {
		return 0
	}
	q := y / x
	return x * math.Sqrt(1+float64(q*q))
}

func abs(x float64) float64 {
	return math.Float64frombits(math.Float64bits(x) &^ (1 << 63))
}

// Pow returns x raised to the integer power n by repeated squaring. Every
// product is rounded to float64. A negative n gives 1/x^|n|.
func Pow(x float64, n int) float64 {
	neg := n < 0
	var e uint64
	if neg {
		e = uint64(-(n + 1)) + 1
	} else {
		e = uint64(n)
	}
	r := 1.0
	b := x
	for e > 0 {
		if e&1 == 1 {
			r = float64(r * b)
		}
		b = float64(b * b)
		e >>= 1
	}
	if neg {
		return 1 / r
	}
	return r
}

// reduce returns r in about [-pi/4, pi/4] and the quadrant q (0..3) such that
// x = k*(pi/2) + r with q = k mod 4.
func reduce(x float64) (r float64, q int) {
	if x >= -0.785398163397448 && x <= 0.785398163397448 {
		return x, 0
	}
	k := roundHalfAway(float64(x * twoOverPi))
	r = x - float64(k*pio2a)
	r = r - float64(k*pio2b)
	r = r - float64(k*pio2c)
	r = r - float64(k*pio2t)
	// k mod 4 on a float64 that may be large: use the remainder by 4.
	m := k - float64(4*floor(k/4))
	return r, int(m)
}

func floor(x float64) float64 {
	if abs(x) >= 1<<52 {
		return x
	}
	t := float64(int64(x))
	if t > x {
		t--
	}
	return t
}

func roundHalfAway(x float64) float64 {
	if abs(x) >= 1<<52 {
		return x
	}
	if x < 0 {
		return -floor(-x + 0.5)
	}
	return floor(x + 0.5)
}

// Taylor coefficients, exact constant expressions rounded once by the compiler.
const (
	s3  = -1.0 / 6
	s5  = 1.0 / 120
	s7  = -1.0 / 5040
	s9  = 1.0 / 362880
	s11 = -1.0 / 39916800
	s13 = 1.0 / 6227020800
	s15 = -1.0 / 1307674368000
	s17 = 1.0 / 355687428096000
	s19 = -1.0 / 121645100408832000
	s21 = 1.0 / 51090942171709440000
)

const (
	c2  = -1.0 / 2
	c4  = 1.0 / 24
	c6  = -1.0 / 720
	c8  = 1.0 / 40320
	c10 = -1.0 / 3628800
	c12 = 1.0 / 479001600
	c14 = -1.0 / 87178291200
	c16 = 1.0 / 20922789888000
	c18 = -1.0 / 6402373705728000
	c20 = 1.0 / 2432902008176640000
)

func sinKernel(x float64) float64 {
	z := float64(x * x)
	p := s21
	p = s19 + float64(z*p)
	p = s17 + float64(z*p)
	p = s15 + float64(z*p)
	p = s13 + float64(z*p)
	p = s11 + float64(z*p)
	p = s9 + float64(z*p)
	p = s7 + float64(z*p)
	p = s5 + float64(z*p)
	p = s3 + float64(z*p)
	return x + float64(float64(x*z)*p)
}

func cosKernel(x float64) float64 {
	z := float64(x * x)
	p := c20
	p = c18 + float64(z*p)
	p = c16 + float64(z*p)
	p = c14 + float64(z*p)
	p = c12 + float64(z*p)
	p = c10 + float64(z*p)
	p = c8 + float64(z*p)
	p = c6 + float64(z*p)
	p = c4 + float64(z*p)
	p = c2 + float64(z*p)
	return 1 + float64(z*p)
}

// Sin returns the sine of x (radians).
func Sin(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return math.NaN()
	}
	if x == 0 {
		return x
	}
	r, q := reduce(x)
	switch q {
	case 0:
		return sinKernel(r)
	case 1:
		return cosKernel(r)
	case 2:
		return -sinKernel(r)
	default:
		return -cosKernel(r)
	}
}

// Cos returns the cosine of x (radians).
func Cos(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return math.NaN()
	}
	r, q := reduce(x)
	switch q {
	case 0:
		return cosKernel(r)
	case 1:
		return -sinKernel(r)
	case 2:
		return -cosKernel(r)
	default:
		return sinKernel(r)
	}
}

// Tan returns the tangent of x (radians), computed as Sin(x)/Cos(x).
func Tan(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return math.NaN()
	}
	if x == 0 {
		return x
	}
	r, q := reduce(x)
	s, c := sinKernel(r), cosKernel(r)
	if q&1 == 1 {
		return -c / s
	}
	return s / c
}

// atanKernel returns atan(x) for x in [0, 1] by halving the angle three
// times (atan x = 2 atan(x / (1 + sqrt(1 + x^2)))) and summing a Taylor series.
func atanKernel(x float64) float64 {
	for i := 0; i < 3; i++ {
		x = x / (1 + math.Sqrt(1+float64(x*x)))
	}
	z := float64(x * x)
	p := 1.0 / 23
	p = -1.0/21 + float64(z*p)
	p = 1.0/19 + float64(z*p)
	p = -1.0/17 + float64(z*p)
	p = 1.0/15 + float64(z*p)
	p = -1.0/13 + float64(z*p)
	p = 1.0/11 + float64(z*p)
	p = -1.0/9 + float64(z*p)
	p = 1.0/7 + float64(z*p)
	p = -1.0/5 + float64(z*p)
	p = 1.0/3 + float64(z*p)
	p = -p
	r := x + float64(float64(x*z)*p)
	return 8 * r
}

// Atan returns the arctangent of x in radians.
func Atan(x float64) float64 {
	if math.IsNaN(x) {
		return x
	}
	neg := math.Signbit(x)
	a := abs(x)
	var r float64
	switch {
	case math.IsInf(a, 0):
		r = pio2
	case a > 1:
		r = pio2 - atanKernel(1/a)
		// pio2 is rounded; add the tail of pi/2 for the last bit.
		r += 6.123233995736766e-17
	default:
		r = atanKernel(a)
	}
	if neg {
		return -r
	}
	return r
}

// Atan2 returns the angle of the point (x, y) in (-pi, pi], with the usual
// IEEE special cases for zeros and infinities.
func Atan2(y, x float64) float64 {
	switch {
	case math.IsNaN(x) || math.IsNaN(y):
		return math.NaN()
	case y == 0:
		if x >= 0 && !math.Signbit(x) {
			return y
		}
		if math.Signbit(y) {
			return -pi - 1.2246467991473532e-16
		}
		return pi + 1.2246467991473532e-16
	case x == 0:
		if y > 0 {
			return pio2
		}
		return -pio2
	case math.IsInf(x, 0):
		if math.IsInf(x, 1) {
			if math.IsInf(y, 0) {
				return copysign(pi/4, y)
			}
			return copysign(0, y)
		}
		if math.IsInf(y, 0) {
			return copysign(3*pi/4, y)
		}
		return copysign(pi, y)
	case math.IsInf(y, 0):
		return copysign(pio2, y)
	}
	q := Atan(y / x)
	if x < 0 {
		if q <= 0 {
			return q + pi + 1.2246467991473532e-16
		}
		return q - pi - 1.2246467991473532e-16
	}
	return q
}

func copysign(x, sign float64) float64 {
	return math.Float64frombits(math.Float64bits(x)&^(1<<63) | math.Float64bits(sign)&(1<<63))
}

// Asin returns the arcsine of x, or NaN when |x| > 1.
func Asin(x float64) float64 {
	if math.IsNaN(x) || abs(x) > 1 {
		return math.NaN()
	}
	return Atan2(x, math.Sqrt(float64((1-x)*(1+x))))
}

// Acos returns the arccosine of x, or NaN when |x| > 1.
func Acos(x float64) float64 {
	if math.IsNaN(x) || abs(x) > 1 {
		return math.NaN()
	}
	return Atan2(math.Sqrt(float64((1-x)*(1+x))), x)
}
