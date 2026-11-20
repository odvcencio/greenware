package rig

import (
	"fmt"

	"m31labs.dev/greenware/kernel/mesh"
)

// Range is an inclusive angular range in radians. An unset range imposes no
// test.
type Range struct {
	Set    bool
	Lo, Hi float64
}

// Ranges are the angular ranges of a joint: the twist about its twist axis
// and the swing about each frame axis. A swing range about the twist axis
// itself is meaningless and is ignored.
type Ranges struct {
	Twist  Range
	Swing  [3]Range // swing about x, y and z
	AnySet bool
}

// Joint is one joint of a skeleton.
type Joint struct {
	Name   string
	Parent int  // index in Skeleton.Joints; -1 for the root
	Pos    Vec  // model-space bind position
	Rot    Quat // model-space bind orientation
	// TwistAxis is the frame axis (0, 1, 2) of the twist, or -1 to choose the
	// axis closest to the bone (see Skeleton.TwistAxis).
	TwistAxis int
	// Band is the natural range and Limit the hard limit.
	Band, Limit Ranges
}

// Skeleton is a joint tree. Joints are in joint order, which need not put a
// parent before its children: every computation goes by ancestry.
type Skeleton struct {
	Joints []Joint
}

// NewSkeleton checks a joint list: exactly one root, valid parents, unique
// names, no cycle.
func NewSkeleton(js []Joint) (*Skeleton, error) {
	if len(js) == 0 {
		return nil, fmt.Errorf("a skeleton needs at least one joint")
	}
	names := map[string]int{} // lookup only
	roots := 0
	for i, j := range js {
		if prev, dup := names[j.Name]; dup {
			return nil, fmt.Errorf("joint name %q is used by joints %d and %d", j.Name, prev, i)
		}
		names[j.Name] = i
		switch {
		case j.Parent == -1:
			roots++
		case j.Parent < 0 || j.Parent >= len(js):
			return nil, fmt.Errorf("joint %q has parent index %d out of range", j.Name, j.Parent)
		case j.Parent == i:
			return nil, fmt.Errorf("joint %q is its own parent", j.Name)
		}
	}
	if roots != 1 {
		return nil, fmt.Errorf("a skeleton has exactly one root joint, found %d", roots)
	}
	s := &Skeleton{Joints: append([]Joint(nil), js...)}
	for i := range s.Joints {
		steps := 0
		for k := i; k != -1; k = s.Joints[k].Parent {
			if steps++; steps > len(js) {
				return nil, fmt.Errorf("joint %q is on a parent cycle", s.Joints[i].Name)
			}
		}
	}
	return s, nil
}

// Clone returns a deep copy.
func (s *Skeleton) Clone() *Skeleton {
	return &Skeleton{Joints: append([]Joint(nil), s.Joints...)}
}

// Index returns the index of the named joint, or -1.
func (s *Skeleton) Index(name string) int {
	for i, j := range s.Joints {
		if j.Name == name {
			return i
		}
	}
	return -1
}

// Root returns the index of the root joint.
func (s *Skeleton) Root() int {
	for i, j := range s.Joints {
		if j.Parent == -1 {
			return i
		}
	}
	return -1
}

// Children returns the child indices of joint i in joint order.
func (s *Skeleton) Children(i int) []int {
	var out []int
	for k, j := range s.Joints {
		if j.Parent == i {
			out = append(out, k)
		}
	}
	return out
}

// InSubtree reports whether joint k is joint root or one of its descendants.
func (s *Skeleton) InSubtree(root, k int) bool {
	for steps := 0; k != -1 && steps <= len(s.Joints); steps++ {
		if k == root {
			return true
		}
		k = s.Joints[k].Parent
	}
	return false
}

// TRS is a local transform: a rotation and a translation, no scale.
type TRS struct {
	T Vec
	R Quat
}

func (t TRS) rigid() Rigid { return Rigid{R: t.R, T: t.T} }

// BindWorld returns the model-space bind of each joint.
func (s *Skeleton) BindWorld() []Rigid {
	out := make([]Rigid, len(s.Joints))
	for i, j := range s.Joints {
		out[i] = Rigid{R: j.Rot, T: j.Pos}
	}
	return out
}

// Rest returns the local rest of each joint: L = B_p^-1 B_j, and B_j for the
// root.
func (s *Skeleton) Rest() []TRS {
	b := s.BindWorld()
	out := make([]TRS, len(s.Joints))
	for i, j := range s.Joints {
		l := b[i]
		if j.Parent != -1 {
			l = b[j.Parent].Inverse().Compose(b[i])
		}
		out[i] = TRS{T: l.T, R: l.R}
	}
	return out
}

// World composes local transforms by ancestry into world transforms. The
// result does not depend on the order of the joints.
func (s *Skeleton) World(local []TRS) []Rigid {
	out := make([]Rigid, len(s.Joints))
	done := make([]bool, len(s.Joints))
	var visit func(i int) Rigid
	visit = func(i int) Rigid {
		if done[i] {
			return out[i]
		}
		w := local[i].rigid()
		if p := s.Joints[i].Parent; p != -1 {
			w = visit(p).Compose(w)
		}
		out[i], done[i] = w, true
		return w
	}
	for i := range s.Joints {
		visit(i)
	}
	return out
}

// InverseBinds returns the inverse of each world rest, in model space.
func InverseBinds(world []Rigid) []Rigid {
	out := make([]Rigid, len(world))
	for i, w := range world {
		out[i] = w.Inverse()
	}
	return out
}

// Offset is a parent-local translation delta a morph target gives a joint at
// weight 1.
type Offset struct {
	Joint int
	Morph int
	Delta Vec
}

// AdjustedRest returns the rest with each joint's translation moved by the
// weighted offsets: T' = T0 + sum w_i delta_ij. Rest rotations do not change.
// Offsets apply in the order given, so the sum is the same on every run.
func AdjustedRest(rest []TRS, offs []Offset, w []float64) []TRS {
	out := append([]TRS(nil), rest...)
	for _, o := range offs {
		out[o.Joint].T = mesh.Add(out[o.Joint].T, mesh.Scale(o.Delta, w[o.Morph]))
	}
	return out
}

// OffsetSum returns the weighted sum of the offsets of each joint.
func OffsetSum(n int, offs []Offset, w []float64) []Vec {
	out := make([]Vec, n)
	for _, o := range offs {
		out[o.Joint] = mesh.Add(out[o.Joint], mesh.Scale(o.Delta, w[o.Morph]))
	}
	return out
}

// LocalPoint converts a model-space bind point to a point in the frame of
// joint i: R_i^-1 (p - pos_i).
func (s *Skeleton) LocalPoint(i int, p Vec) Vec {
	j := s.Joints[i]
	return j.Rot.Conj().Rotate(mesh.Sub(p, j.Pos))
}

// LocalFrame converts a model-space frame (a bind point and a rotation
// relative to the joint's own axes) to joint-local translation and rotation.
func (s *Skeleton) LocalFrame(i int, at Vec, rot Quat) TRS {
	return TRS{T: s.LocalPoint(i, at), R: rot}
}
