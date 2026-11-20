package rig

import (
	"fmt"
	"math"
	"sort"

	"m31labs.dev/greenware/det"
)

// Path is the property a channel drives.
type Path int

// Channel paths.
const (
	Translation Path = iota
	Rotation
)

// Comps is the number of floats of one value of the path.
func (p Path) Comps() int {
	if p == Rotation {
		return 4
	}
	return 3
}

// Interp is how a channel blends between keys.
type Interp int

// Interpolation modes. Step holds the preceding value. Linear interpolates
// translations linearly and rotations on the shortest arc. CubicSpline is the
// glTF Hermite spline: each key holds an in-tangent, a value and an out-tangent.
const (
	Linear Interp = iota
	Step
	CubicSpline
)

// Channel is one animated property of one target. Target is a joint index for
// the skeleton; a caller may use it for another node kind.
type Channel struct {
	Target int
	Path   Path
	Interp Interp
	Times  []float64
	// Values holds Path.Comps() floats per key; a cubic spline holds three
	// such groups per key (in-tangent, value, out-tangent).
	Values []float64
}

// Clip is a set of channels over [0, Duration].
type Clip struct {
	Name     string
	Duration float64
	Loop     bool
	Airborne bool
	Channels []Channel
}

// SampleTimes returns the times of a sampled clip: keys values at
// duration*(i/(keys-1)), including both ends.
func SampleTimes(duration float64, keys int) []float64 {
	out := make([]float64, keys)
	for i := range out {
		out[i] = float64(duration * (float64(i) / float64(keys-1)))
	}
	return out
}

func (c Channel) stride() int {
	n := c.Path.Comps()
	if c.Interp == CubicSpline {
		return 3 * n
	}
	return n
}

// Condition checks a channel against the clip duration and returns the
// channel as it is delivered: rotation keys normalised and made continuous
// (each dot product with the previous key nonnegative), a copy of the first
// value prepended at 0 s when the first key is later, and a copy of the last
// appended at the duration when the last key is earlier. Times must be
// finite, nonnegative and strictly increasing, and none later than the
// duration; nothing is sorted, merged or repaired.
func Condition(c Channel, duration float64) (Channel, error) {
	n := c.Path.Comps()
	st := c.stride()
	if len(c.Times) == 0 {
		return c, fmt.Errorf("a channel needs at least one key")
	}
	if len(c.Values) != len(c.Times)*st {
		return c, fmt.Errorf("a channel has %d times and %d values; it needs %d values", len(c.Times), len(c.Values), len(c.Times)*st)
	}
	if !(duration > 0) || math.IsInf(duration, 0) {
		return c, fmt.Errorf("the duration must be positive and finite")
	}
	for i, t := range c.Times {
		switch {
		case math.IsNaN(t) || math.IsInf(t, 0):
			return c, fmt.Errorf("key %d has a time that is not finite", i)
		case t < 0:
			return c, fmt.Errorf("key %d has the negative time %v", i, t)
		case t > duration:
			return c, fmt.Errorf("key %d at %v s is later than the duration %v s", i, t, duration)
		case i > 0 && !(t > c.Times[i-1]):
			return c, fmt.Errorf("key %d at %v s does not come after key %d at %v s; times must increase strictly", i, t, i-1, c.Times[i-1])
		}
	}
	out := Channel{Target: c.Target, Path: c.Path, Interp: c.Interp}
	out.Times = append([]float64(nil), c.Times...)
	out.Values = append([]float64(nil), c.Values...)
	if c.Path == Rotation {
		valueAt := func(k int) []float64 {
			if c.Interp == CubicSpline {
				return out.Values[k*st+n : k*st+2*n]
			}
			return out.Values[k*st : k*st+n]
		}
		for k := range out.Times {
			v := valueAt(k)
			q := Quat{v[0], v[1], v[2], v[3]}
			if q.Dot(q) == 0 {
				return c, fmt.Errorf("key %d is a zero quaternion", k)
			}
			q = q.Normalize()
			if k > 0 {
				pv := valueAt(k - 1)
				if q.Dot(Quat{pv[0], pv[1], pv[2], pv[3]}) < 0 {
					q = q.Neg()
				}
			}
			copy(v, q[:])
		}
	}
	// A copy of an endpoint value: the value group, with zero tangents.
	endCopy := func(k int) []float64 {
		g := make([]float64, st)
		if c.Interp == CubicSpline {
			copy(g[n:2*n], out.Values[k*st+n:k*st+2*n])
		} else {
			copy(g, out.Values[k*st:(k+1)*st])
		}
		return g
	}
	if out.Times[0] > 0 {
		g := endCopy(0)
		out.Times = append([]float64{0}, out.Times...)
		out.Values = append(g, out.Values...)
	}
	if last := len(out.Times) - 1; out.Times[last] < duration {
		g := endCopy(last)
		out.Times = append(out.Times, duration)
		out.Values = append(out.Values, g...)
	}
	return out, nil
}

// At samples the channel at time t, clamped to its first and last key.
// Rotation results are unit quaternions.
func (c Channel) At(t float64) []float64 {
	n := c.Path.Comps()
	st := c.stride()
	last := len(c.Times) - 1
	val := func(k int) []float64 {
		if c.Interp == CubicSpline {
			return c.Values[k*st+n : k*st+2*n]
		}
		return c.Values[k*st : k*st+n]
	}
	if t <= c.Times[0] {
		return append([]float64(nil), val(0)...)
	}
	if t >= c.Times[last] {
		return append([]float64(nil), val(last)...)
	}
	// The segment k with Times[k] <= t < Times[k+1].
	k := sort.Search(len(c.Times), func(i int) bool { return c.Times[i] > t }) - 1
	t0, t1 := c.Times[k], c.Times[k+1]
	a, b := val(k), val(k+1)
	switch c.Interp {
	case Step:
		return append([]float64(nil), a...)
	case CubicSpline:
		td := t1 - t0
		s := (t - t0) / td
		s2 := float64(s * s)
		s3 := float64(s2 * s)
		h00 := float64(2*s3) - float64(3*s2) + 1
		h10 := s3 - float64(2*s2) + s
		h01 := float64(-2*s3) + float64(3*s2)
		h11 := s3 - s2
		outT := c.Values[k*st+2*n : k*st+3*n]  // out-tangent of key k
		inT := c.Values[(k+1)*st : (k+1)*st+n] // in-tangent of key k+1
		out := make([]float64, n)
		for i := range out {
			out[i] = float64(h00*a[i]) + float64(h10*float64(td*outT[i])) + float64(h01*b[i]) + float64(h11*float64(td*inT[i]))
		}
		if c.Path == Rotation {
			q := Quat{out[0], out[1], out[2], out[3]}.Normalize()
			copy(out, q[:])
		}
		return out
	}
	f := (t - t0) / (t1 - t0)
	if c.Path == Rotation {
		q := Slerp(Quat{a[0], a[1], a[2], a[3]}, Quat{b[0], b[1], b[2], b[3]}, f)
		return q[:]
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = det.Lerp(a[i], b[i], f)
	}
	return out
}

// TimesAre32 reports the first index at which float32 rounding of the times
// makes them stop increasing strictly, or -1.
func TimesAre32(times []float64) int {
	for i := 1; i < len(times); i++ {
		if !(det.Round32(times[i]) > det.Round32(times[i-1])) {
			return i
		}
	}
	return -1
}
