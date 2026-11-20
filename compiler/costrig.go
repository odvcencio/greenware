package compiler

import (
	"fmt"

	"m31labs.dev/greenware/resolve"
)

// The rig's tickets (milestone M4, the athlete's path). compiler.md 3.1
// qualifies the M2 profile only; it says to register bounded tickets for
// "joints x keys x runtime points" before an athlete profile is admitted, and
// it names none. These names, prices and bounds are an assumption of this
// compiler, kept apart from the M2, helmet and look schedules so that their
// receipts are unchanged. An output that declares no skeleton reserves none
// of them.
//
//   - rig, once per selected output with a skeleton. It buys the bind frames,
//     the inverse binds, the joint order and the socket frames. Class: at most
//     64 joints and 64 sockets. Price 100,000.
//   - weights, per level of a rigged output. It buys the rigid binding, the
//     ordered skin steps, the normalisation and the influence limit on the
//     level's own topology. Class: at most 32 steps and 8,192 bound
//     positions. Price 300,000.
//   - morph, per level of a rigged output with morph targets. It buys the
//     construction of each target on the finished bind mesh, the recomputed
//     normals and the joint offsets of `joints = follow`. Class: at most 10
//     targets (the athlete asks for ten; the default budget is eight) and
//     8,192 bound positions. Price 150,000.
//   - clip, once per selected output with clips. It buys the sampling of every
//     clip, the conditioning of every channel and the swing-and-twist test of
//     every pose key. Class: at most 32 clips and 4,096 keys in all. Price
//     200,000.
//
// The class bounds on counts that exist only after the surface is built (the
// bound positions) are checked on the built level, as the surface ticket's
// triangle bound is.
const (
	TicketRig     = "rig"
	TicketWeights = "weights"
	TicketMorph   = "morph"
	TicketClip    = "clip"
	// TicketMount buys building the gear a mount selects; see costathlete.go.
	TicketMount = "mount"
)

const (
	maxRigJoints    = 64
	maxRigSockets   = 64
	maxRigSteps     = 32
	maxRigPositions = 8192
	maxRigMorphs    = 10
	maxRigClips     = 32
	maxRigKeys      = 4096
)

// ticketTableRig is the rig's rows, in reservation order.
var ticketTableRig = []TicketClass{
	{TicketRig, 100_000, "selected output with a skeleton", "at most 64 joints and 64 sockets"},
	{TicketWeights, 300_000, "level of an output with a skeleton", "at most 32 skin steps and 8,192 bound positions"},
	{TicketMorph, 150_000, "level of an output with morph targets", "at most 10 targets and 8,192 bound positions"},
	{TicketClip, 200_000, "selected output with clips", "at most 32 clips and 4,096 keys in all"},
}

// RigTickets returns the rig's tickets.
func RigTickets() []TicketClass { return append([]TicketClass(nil), ticketTableRig...) }

// rigCounts are the counts the rig tickets bound, known from the front end.
type rigCounts struct {
	skeleton               bool
	joints, sockets, steps int
	morphs, clips, keys    int
}

func rigCountsOf(r *resolve.Resolved) rigCounts {
	var c rigCounts
	rg := r.Rig
	if rg == nil {
		return c
	}
	if rg.Skeleton != nil {
		c.skeleton = true
		c.joints, c.sockets = len(rg.Skeleton.Joints), len(rg.Skeleton.Sockets)
	}
	if rg.Skin != nil {
		c.steps = len(rg.Skin.Steps)
	}
	c.morphs, c.clips = len(rg.Morphs), len(rg.Clips)
	for _, cl := range rg.Clips {
		c.keys += len(cl.Samples) + len(cl.Rows)
	}
	return c
}

// rigClassBound returns what a rig ticket's operand exceeds, or "".
func rigClassBound(c Charge, outs map[string]resolvedOutput) string {
	o := outs[c.Output]
	n := rigCountsOf(o.r)
	switch c.Ticket {
	case TicketRig:
		switch {
		case n.joints > maxRigJoints:
			return fmt.Sprintf("output %s has %d joints; the class allows %d", c.Output, n.joints, maxRigJoints)
		case n.sockets > maxRigSockets:
			return fmt.Sprintf("output %s has %d joint sockets; the class allows %d", c.Output, n.sockets, maxRigSockets)
		}
	case TicketWeights:
		if n.steps > maxRigSteps {
			return fmt.Sprintf("output %s has %d skin steps; the class allows %d", c.Output, n.steps, maxRigSteps)
		}
	case TicketMorph:
		if n.morphs > maxRigMorphs {
			return fmt.Sprintf("output %s has %d morph targets; the class allows %d", c.Output, n.morphs, maxRigMorphs)
		}
	case TicketClip:
		switch {
		case n.clips > maxRigClips:
			return fmt.Sprintf("output %s has %d clips; the class allows %d", c.Output, n.clips, maxRigClips)
		case n.keys > maxRigKeys:
			return fmt.Sprintf("output %s has %d keys in all; the class allows %d", c.Output, n.keys, maxRigKeys)
		}
	}
	return ""
}

// rigClassBoundFiles checks the bounds that only the built level shows: the
// bound positions of the `weights` and `morph` tickets.
func rigClassBoundFiles(op *OutputProduct) *Refusal {
	for _, f := range op.Files {
		lv := f.Level
		if f.SkinnedVertices > maxRigPositions {
			return &Refusal{Charge: Charge{Ticket: TicketWeights, Output: op.Name, Level: &lv},
				Bound: fmt.Sprintf("output %s level %d binds %d positions; the class allows %d", op.Name, lv, f.SkinnedVertices, maxRigPositions)}
		}
	}
	return nil
}
