// Package det holds deterministic scalar math: operations whose results are
// the same bits on every architecture, because each rounding step is explicit.
//
// The no-fusion rule. A fused multiply-add computes x*y+z with one rounding
// instead of two. Go may emit one on arm64 (and ppc64, s390x, riscv64) but
// does not on amd64, so the same source can give different bits on a Mac and
// on a Linux server. The Go specification allows fusion across operations but
// forbids it across an explicit conversion: "An explicit floating-point type
// conversion rounds to the precision of the target type, preventing fusion
// that would discard that rounding."
//
// Every multiply in this package whose result feeds an add or a subtract is
// therefore written as float64(x*y)+z. Keep that form when you edit the
// code. A test (TestNoUnwrappedMultiply) scans the sources for a bare
// multiply next to an add or subtract and fails if it finds one.
//
// The package does not call math.Sin, math.Cos or any other math function
// except math.Sqrt (correctly rounded by IEEE 754 on every architecture),
// math.Float64bits, math.Float64frombits, math.IsNaN, math.IsInf and
// math.Inf/NaN. The algorithms are written here: Cody-Waite range reduction
// with a three-part pi/2, Taylor kernels with Horner evaluation, and
// argument halving for the arctangent.
//
// Accuracy: Sin and Cos agree with math.Sin and math.Cos to 1e-15 absolute on
// [-1000, 1000] (tested). Beyond about 1e6 the range reduction loses bits;
// results stay deterministic but are not accurate. NaN and infinite inputs
// give NaN.
package det
