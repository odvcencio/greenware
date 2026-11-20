package rig

import (
	"m31labs.dev/greenware/kernel/mesh"
)

// Instance is the state a pose is built from: the skeleton with its original
// rest, the parent-local offsets the morph weights give the joints, and the
// optional root scale. A parameter edit changes an Instance, never the shared
// mesh or another instance.
type Instance struct {
	Sk      *Skeleton
	Rest    []TRS    // original local rest
	Offsets []Offset // at weight 1, parent-local
	Weights []float64
	// RootScale, when set, multiplies the up component of a keyed root
	// translation.
	HasRootScale bool
	RootScale    float64
	Up           int // 0, 1 or 2
}

// Adjusted returns the rest with the weighted offsets added.
func (in *Instance) Adjusted() []TRS { return AdjustedRest(in.Rest, in.Offsets, in.Weights) }

// InverseBinds returns the inverse of the adjusted world rest. At adjusted
// rest every skin matrix is the identity.
func (in *Instance) InverseBinds() []Rigid {
	return InverseBinds(in.Sk.World(in.Adjusted()))
}

// Locals returns the local transform of every joint at time t of the clip. A
// nil clip gives the adjusted rest.
//
// A translation key is relative to the original rest: the applied translation
// is the key plus the weighted offsets, not the adjusted rest plus the key.
// For the keyed root with a root scale, the up component is the key's up
// component times the scale and receives no offset; the horizontal components
// are not scaled and do receive the offsets. Without a translation key the
// joint holds the adjusted rest. A rotation key is applied as authored; a
// joint without one holds its rest rotation.
func (in *Instance) Locals(c *Clip, t float64) []TRS {
	adj := in.Adjusted()
	sum := OffsetSum(len(in.Sk.Joints), in.Offsets, in.Weights)
	out := append([]TRS(nil), adj...)
	if c == nil {
		return out
	}
	root := in.Sk.Root()
	for _, ch := range c.Channels {
		if ch.Target < 0 || ch.Target >= len(out) {
			continue
		}
		v := ch.At(t)
		switch ch.Path {
		case Translation:
			k := Vec{v[0], v[1], v[2]}
			if ch.Target == root && in.HasRootScale {
				k[in.Up] = float64(k[in.Up] * in.RootScale)
				o := sum[ch.Target]
				o[in.Up] = 0
				out[ch.Target].T = mesh.Add(k, o)
			} else {
				out[ch.Target].T = mesh.Add(k, sum[ch.Target])
			}
		case Rotation:
			out[ch.Target].R = Quat{v[0], v[1], v[2], v[3]}
		}
	}
	return out
}

// SkinMatrices returns W_j I_j for each joint.
func SkinMatrices(world, ibm []Rigid) []Rigid {
	out := make([]Rigid, len(world))
	for i := range world {
		out[i] = world[i].Compose(ibm[i])
	}
	return out
}

// Skin deforms bind positions by linear blend skinning: the weighted sum of
// each joint's skin matrix applied to the position, in influence order.
func Skin(pos []Vec, infl [][]Influence, skin []Rigid) []Vec {
	out := make([]Vec, len(pos))
	for i, p := range pos {
		var acc Vec
		for _, in := range infl[i] {
			acc = mesh.Add(acc, mesh.Scale(skin[in.Joint].Apply(p), in.W))
		}
		out[i] = acc
	}
	return out
}

// SkinNormals deforms bind normals the same way, with the rotation part only
// (a skin matrix is rigid) and renormalised.
func SkinNormals(nrm []Vec, infl [][]Influence, skin []Rigid) []Vec {
	out := make([]Vec, len(nrm))
	for i, n := range nrm {
		var acc Vec
		for _, in := range infl[i] {
			acc = mesh.Add(acc, mesh.Scale(skin[in.Joint].Dir(n), in.W))
		}
		out[i] = mesh.Normalize(acc)
	}
	return out
}

// Lowest returns the smallest coordinate along axis among positions.
func Lowest(pos []Vec, axis int) float64 {
	lo := pos[0][axis]
	for _, p := range pos[1:] {
		if p[axis] < lo {
			lo = p[axis]
		}
	}
	return lo
}

// GroundShift is the correction a `ground` constraint adds to the root along
// up: weight*(clearance-lowest). It may be negative.
func GroundShift(lowest, clearance, weight float64) float64 {
	return float64(weight * (clearance - lowest))
}
