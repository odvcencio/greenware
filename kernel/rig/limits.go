package rig

import (
	"fmt"
	"math"

	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/kernel/mesh"
)

// tieEps is how close two axis components count as equal when the twist axis
// is chosen, so that rounding does not decide a tie.
const tieEps = 1e-9

const twoPi = 6.283185307179586

// TwistAxisOf returns the frame axis (0, 1, 2) a joint twists about. An
// explicit axis wins. Otherwise it is the axis of the joint's own frame
// closest to the bone: the direction to the first child (in joint order), or
// from the parent for a leaf. Ties go to x, then y, then z. A zero direction
// and a lone root give y.
func (s *Skeleton) TwistAxisOf(i int) int {
	j := s.Joints[i]
	if j.TwistAxis >= 0 {
		return j.TwistAxis
	}
	var dir Vec
	if kids := s.Children(i); len(kids) > 0 {
		dir = mesh.Sub(s.Joints[kids[0]].Pos, j.Pos)
	} else if j.Parent != -1 {
		dir = mesh.Sub(j.Pos, s.Joints[j.Parent].Pos)
	}
	local := j.Rot.Conj().Rotate(dir)
	best, bestV := 1, 0.0
	if mesh.Len(local) == 0 {
		return 1
	}
	for a := 0; a < 3; a++ {
		v := math.Abs(local[a])
		if a == 0 || v > bestV+tieEps {
			best, bestV = a, v
		}
	}
	return best
}

// SwingTwist splits the pose rotation q (relative to rest) about the unit
// frame axis into a twist angle on (-pi, pi] and a swing rotation vector.
// ok is false at a pure 180 degree transverse swing, where the projected
// twist has zero norm and no twist is defined.
func SwingTwist(q Quat, axis int) (twist float64, swing Vec, ok bool) {
	q = q.Normalize()
	a := Vec{}
	a[axis] = 1
	proj := q[axis]
	n := det.Sqrt(float64(proj*proj) + float64(q[3]*q[3]))
	if n < 1e-12 {
		return 0, Vec{}, false
	}
	t := Quat{}
	t[axis] = proj / n
	t[3] = q[3] / n
	twist = float64(2 * det.Atan2(t[axis], t[3]))
	for twist > math.Pi {
		twist -= twoPi
	}
	for twist <= -math.Pi {
		twist += twoPi
	}
	s := q.Mul(t.Conj()).Normalize()
	if s[3] < 0 {
		s = s.Neg()
	}
	vn := det.Sqrt(float64(s[0]*s[0]) + float64(s[1]*s[1]) + float64(s[2]*s[2]))
	if vn == 0 {
		return twist, Vec{}, true
	}
	ang := float64(2 * det.Atan2(vn, s[3]))
	swing = mesh.Scale(Vec{s[0] / vn, s[1] / vn, s[2] / vn}, ang)
	return twist, swing, true
}

// Violation is one range a pose leaves.
type Violation struct {
	Joint     int
	Component string // "twist", "swing_x", "swing_y" or "swing_z"
	Value     float64
	Range     Range
	Undefined bool // the pose is a pure 180 degree swing: the test is undefined
}

func (v Violation) String() string {
	if v.Undefined {
		return fmt.Sprintf("joint %d: %s is undefined at a pure 180 degree swing", v.Joint, v.Component)
	}
	return fmt.Sprintf("joint %d: %s %.6g rad is outside %.6g..%.6g", v.Joint, v.Component, v.Value, v.Range.Lo, v.Range.Hi)
}

// Check tests a pose rotation q (relative to rest) of joint i against the
// ranges r. It never clamps. Components without a set range are not tested.
func (s *Skeleton) Check(i int, q Quat, r Ranges) []Violation {
	if !r.AnySet {
		return nil
	}
	axis := s.TwistAxisOf(i)
	twist, swing, ok := SwingTwist(q, axis)
	names := [3]string{"swing_x", "swing_y", "swing_z"}
	var out []Violation
	if !ok {
		if r.Twist.Set {
			out = append(out, Violation{Joint: i, Component: "twist", Range: r.Twist, Undefined: true})
		}
		for c := 0; c < 3; c++ {
			if c != axis && r.Swing[c].Set {
				out = append(out, Violation{Joint: i, Component: names[c], Range: r.Swing[c], Undefined: true})
			}
		}
		return out
	}
	if r.Twist.Set && (twist < r.Twist.Lo || twist > r.Twist.Hi) {
		out = append(out, Violation{Joint: i, Component: "twist", Value: twist, Range: r.Twist})
	}
	for c := 0; c < 3; c++ {
		if c == axis || !r.Swing[c].Set {
			continue
		}
		if swing[c] < r.Swing[c].Lo || swing[c] > r.Swing[c].Hi {
			out = append(out, Violation{Joint: i, Component: names[c], Value: swing[c], Range: r.Swing[c]})
		}
	}
	return out
}
