package compiler

import (
	"fmt"

	"m31labs.dev/greenware/resolve"
)

// The athlete's tickets (milestone M4). compiler.md 3.1 asks for bounded
// tickets for per-level skin and morph construction, keys x poses x points,
// the ground, section and fit tests, multi-image encoding and split sets
// before an athlete profile is admitted, and it names none. These names,
// prices and bounds are an assumption of this compiler, kept apart from the
// M2, helmet, look and rig schedules so that their receipts are unchanged. An
// output with a skeleton uses the profile; any other output reserves none of
// these tickets.
//
// Decision 0018 (H2) says the athlete's budgets are measured per output from a
// census and that the old limits stay as refusal tests. The bounds below are
// those measured figures with headroom; the census is in the build record, and
// TestAthleteCensusIsInsideItsClasses holds the measured figures against the
// bounds. The old classes (paint, surface_branches, encode_images) still refuse
// the athlete's figures, and tests pin that.
//
// Split sets (a descriptor with an external buffer and external images) have
// no ticket: the compiler writes none (see the unsupported note), and a
// ticket for work it does not do would only spend the ceiling.
//
//   - paint_large replaces paint for each atlas of a rigged output. Class: at
//     most 262,144 texels (512 x 512), 64 cells and 12,000,000 scalar painter
//     operations (the athlete's atlas: 250,000 texels, 21 cells, 8,727,280
//     operations). Price 500,000.
//   - mount, once for each gear output a mount builds in a rigged output: the
//     output's selected gear and each other gear output the runtime points
//     choose. It buys the nested build of the gear and its bounded copy into
//     the host. Class: the helmet profile (at most 4,096 stored triangles per
//     level, 4 images and 192 KiB per file). Price 700,000.
//   - surface_rig replaces surface for each level of a rigged output. Class: at
//     most 18,432 stored triangles in the level, a baked gear included (the
//     most measured is 14,782, in home and away). Price 400,000.
//   - audit, per level of a rigged output with `check rig`: the points x poses
//     x positions of the ground and edge tests. Class: at most 256 runtime
//     points, 4,096 poses for each point and 16,384 skinned positions. Price
//     400,000.
//   - fit, per level and mount with a `fit` check: the gap between the posed
//     gear and the posed skin at each pose of each point. Class: at most 256
//     points, 4,096 poses and 4,096 gear triangles. Price 200,000.
//   - encode_large replaces encode (and encode_images) for each level file of a
//     rigged output: packing, hashing and verification of a GLB of at most 4
//     embedded images and 2 MiB (the most measured is 3 images and 1,316,688
//     bytes, in home). Price 250,000.
const (
	TicketPaintLarge  = "paint_large"
	TicketSurfaceRig  = "surface_rig"
	TicketAudit       = "audit"
	TicketFit         = "fit"
	TicketEncodeLarge = "encode_large"
)

const (
	maxLargeTexels = 262144
	maxLargePaint  = 12_000_000
	maxRigSurface  = 18432
	maxAuditPoints = 256
	maxAuditPoses  = 4096
	maxFitGearTris = 4096
	maxLargeImages = 4
	maxLargeBytes  = 2 << 20
)

var ticketTableAthlete = []TicketClass{
	{TicketPaintLarge, 500_000, "atlas of a rigged output that a drawn material samples", "at most 262,144 texels, 64 cells and 12,000,000 scalar painter operations"},
	{TicketMount, 700_000, "gear output a mount builds in a rigged output", "the helmet profile: at most 4,096 stored triangles per level, 4 images and 192 KiB per file"},
	{TicketSurfaceRig, 400_000, "level of a rigged output", "at most 18,432 stored triangles in the level, every branch and baked gear counted"},
	{TicketAudit, 400_000, "level of a rigged output with `check rig`", "at most 256 runtime points, 4,096 poses for each and 16,384 skinned positions"},
	{TicketFit, 200_000, "level and mount of a rigged output with `fit`", "at most 256 runtime points, 4,096 poses and 4,096 gear triangles"},
	{TicketEncodeLarge, 250_000, "level file of a rigged output", "at most 4 embedded images and 2 MiB per file"},
}

// AthleteTickets returns the athlete profile's tickets.
func AthleteTickets() []TicketClass { return append([]TicketClass(nil), ticketTableAthlete...) }

func athleteUnits(name string) (uint64, bool) {
	for _, t := range ticketTableAthlete {
		if t.Name == name {
			return t.Units, true
		}
	}
	return 0, false
}

// isRigged reports whether an output belongs to the athlete profile: it has a
// skeleton and uses what the profile prices (a mount, an audit or an atlas).
// A rig alone (the arm) keeps the helmet profile's tickets, so that its
// receipt does not change.
func isRigged(r *resolve.Resolved) bool {
	return rigCountsOf(r).skeleton && (len(r.Mounts) > 0 || r.RigCheck != nil || r.RuntimeCheck != nil || len(atlasesUsed(r)) > 0)
}

// mountModes returns each mount of the output with its effective mode.
func mountModes(r *resolve.Resolved) []*mountGear {
	var out []*mountGear
	mode := ""
	for _, o := range r.Outputs {
		if o.Name == r.Output {
			mode = o.Mounts
		}
	}
	for _, m := range r.Mounts {
		md := m.Mode
		if mode != "" {
			md = mode
		}
		var gear *resolve.GearImport
		for i := range r.Gears {
			if r.Gears[i].Qualifier == m.Gear {
				gear = &r.Gears[i]
			}
		}
		out = append(out, &mountGear{m: m, mode: md, gear: gear})
	}
	return out
}

// mountBuilds lists the gear outputs the output builds, as "mount/output", in
// order: for each mount that bakes or checks fit, its selected output, then the
// other outputs the runtime points choose.
func mountBuilds(r *resolve.Resolved, limit int) []string {
	e := &elab{r: r, mounts: mountModes(r)}
	var out []string
	for _, g := range e.mounts {
		if g.mode != "bake" && !g.m.HasFit {
			continue
		}
		seen := map[string]bool{g.m.Output: true}
		list := []string{g.m.Output}
		if g.mode != "bake" && g.m.HasFit {
			for _, p := range e.planPoints(limit).points {
				if o := p.gear[g.m.Name]; !seen[o] {
					seen[o] = true
					list = append(list, o)
				}
			}
		}
		for _, o := range list {
			out = append(out, g.m.Name+"/"+o)
		}
	}
	return out
}

// auditCounts are the operands of the audit's class: the runtime points and
// the poses of each point.
func auditCounts(r *resolve.Resolved, limit int) (points, poses int) {
	e := &elab{r: r, mounts: mountModes(r)}
	points = len(e.planPoints(limit).points)
	poses = 1
	if r.Rig != nil {
		for _, c := range r.Rig.Clips {
			poses += len(c.Samples)
		}
	}
	return points, poses
}

func fitMountCount(r *resolve.Resolved) int {
	n := 0
	for _, g := range mountModes(r) {
		if g.m.HasFit {
			n++
		}
	}
	return n
}

// athleteClassBound is classBound for the athlete's tickets: what the operand
// exceeds before the operation runs.
func athleteClassBound(c Charge, o resolvedOutput, pol Policy) string {
	switch c.Ticket {
	case TicketPaintLarge:
		a := o.r.Atlas(c.Row)
		if a == nil {
			break
		}
		if texels := a.Layout.Width() * a.Layout.Height(); texels > maxLargeTexels {
			return fmt.Sprintf("the atlas %s is %d x %d texels (%d); the class allows %d", a.Name, a.Layout.Width(), a.Layout.Height(), texels, maxLargeTexels)
		}
		if len(a.Cells) > maxPaintCells {
			return fmt.Sprintf("the atlas %s has %d cells; the class allows %d", a.Name, len(a.Cells), maxPaintCells)
		}
		if a.Ops > maxLargePaint {
			return fmt.Sprintf("the atlas %s needs %d scalar painter operations; the class allows %d", a.Name, a.Ops, maxLargePaint)
		}
	case TicketAudit, TicketFit:
		points, poses := auditCounts(o.r, pol.RuntimePoints)
		if points > maxAuditPoints {
			return fmt.Sprintf("output %s audits %d runtime points; the class allows %d", c.Output, points, maxAuditPoints)
		}
		if poses > maxAuditPoses {
			return fmt.Sprintf("output %s audits %d poses for each point; the class allows %d", c.Output, poses, maxAuditPoses)
		}
	}
	return ""
}

// athleteClassBoundFiles checks what only the built files show, for an output
// of the athlete profile: the stored triangles of a level, its images and its
// bytes, the skinned positions the audit reads.
func athleteClassBoundFiles(op *OutputProduct) *Refusal {
	for _, f := range op.Files {
		lv := f.Level
		if f.TrianglesStored > maxRigSurface {
			return &Refusal{Charge: Charge{Ticket: TicketSurfaceRig, Output: op.Name, Level: &lv},
				Bound: fmt.Sprintf("output %s level %d holds %d triangles; the class allows %d", op.Name, lv, f.TrianglesStored, maxRigSurface)}
		}
		if len(f.Images) > maxLargeImages {
			return &Refusal{Charge: Charge{Ticket: TicketEncodeLarge, Output: op.Name, Level: &lv},
				Bound: fmt.Sprintf("%s embeds %d images; the class allows %d", f.Name, len(f.Images), maxLargeImages)}
		}
		if f.Size > maxLargeBytes {
			return &Refusal{Charge: Charge{Ticket: TicketEncodeLarge, Output: op.Name, Level: &lv},
				Bound: fmt.Sprintf("%s is %d bytes; the class allows %d", f.Name, f.Size, maxLargeBytes)}
		}
		if f.Audit != nil && f.SkinnedVertices > maxRigPositions {
			return &Refusal{Charge: Charge{Ticket: TicketAudit, Output: op.Name, Level: &lv},
				Bound: fmt.Sprintf("output %s level %d audits %d skinned positions; the class allows %d", op.Name, lv, f.SkinnedVertices, maxRigPositions)}
		}
	}
	return nil
}
