package compiler

import (
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/kernel/geom"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/kernel/rig"
	"m31labs.dev/greenware/resolve"
)

// The audit (language reference 5.9, `facet.audit/1` here `greenware.audit/1`).
// For every selected runtime point, level and delivered clip key it poses the
// skin of the host and measures:
//
//   - the lowest skinned vertex against the ground band, at every key of every
//     clip that is not airborne (the bind pose is not a standing pose and is
//     not tested);
//   - the share of geometric edges whose posed length is more than three times
//     or less than a quarter of the bind length (torn), and outside 1/1.5..1.5
//     (strained), at the worst key;
//   - for every mount that declares `fit`, the least distance between the
//     posed gear surface and the posed host skin.
//
// The natural bands and hard limits are measured where the clip is written
// (they do not depend on the point); `bands = error` promotes the band
// warning there. Not measured here: the waist section and seam probes and the
// hip crossing (5.9 items 3 and 4). A build that declares them says so in its
// unsupported notes.

// AuditReport is the measured result of the audit of one level.
type AuditReport struct {
	Output string `json:"output"`
	Level  int    `json:"level"`
	Points int    `json:"points"`
	// Poses is the number of clip keys and bind poses measured in all.
	Poses int `json:"poses"`
	// Partial says the runtime points were fewer than the full product.
	Partial bool   `json:"partial,omitempty"`
	Omitted uint64 `json:"omitted_combinations,omitempty"`
	// The lowest skinned vertex of each non-airborne pose is measured against
	// the ground band. GroundLowest is the smallest such height over every
	// pose and point, GroundHighest the largest, in metres above the ground.
	GroundLowest  Measure `json:"ground_lowest"`
	GroundHighest Measure `json:"ground_highest"`
	// Torn and Strained are the worst shares over every pose.
	Torn     Measure `json:"torn_share"`
	Strained Measure `json:"strained_share"`
	// Fit has one entry per mount with a fit check.
	Fit []FitReport `json:"fit,omitempty"`
}

// Measure is a measured number and where it was found.
type Measure struct {
	Value float64 `json:"value"`
	Point string  `json:"point"`
	Clip  string  `json:"clip"`
	Time  float64 `json:"time"`
}

// FitReport is the least gap of one mount.
type FitReport struct {
	Mount    string  `json:"mount"`
	Required float64 `json:"required"`
	// Clearance is the least measured distance in metres, or the search limit
	// when no pair came closer (AtLeast).
	Clearance float64 `json:"clearance"`
	AtLeast   bool    `json:"at_least,omitempty"`
	Point     string  `json:"point"`
	Clip      string  `json:"clip"`
	Time      float64 `json:"time"`
	Gear      string  `json:"gear_output"`
	GearPart  string  `json:"gear_part"`
	HostPart  string  `json:"host_part"`
}

// auditMesh is the host skin as the audit sees it: the bind positions of
// every piece, the morph deltas, the influences and the triangles.
type auditMesh struct {
	base  []mesh.Vec3
	delta [][]mesh.Vec3 // [target][position]
	infl  [][]rig.Influence
	tris  [][3]int32
	edges [][2]int32
	// part names the piece each triangle belongs to.
	part   []string
	partOf []int32 // piece index per triangle
}

func buildAuditMesh(rl *rigLevel) *auditMesh {
	a := &auditMesh{}
	nt := len(rl.morphs)
	a.delta = make([][]mesh.Vec3, nt)
	var names []string
	for pi, p := range rl.pieces {
		off := int32(len(a.base))
		a.base = append(a.base, p.m.Positions...)
		a.infl = append(a.infl, p.rig.infl...)
		for t := 0; t < nt; t++ {
			a.delta[t] = append(a.delta[t], p.rig.pos[t]...)
		}
		names = append(names, firstName(p))
		seen := map[[2]int32]bool{}
		for _, tr := range p.m.Tris {
			var idx [3]int32
			for k := 0; k < 3; k++ {
				idx[k] = p.m.Corners[tr[k]].Pos + off
			}
			a.tris = append(a.tris, idx)
			a.partOf = append(a.partOf, int32(pi))
			for k := 0; k < 3; k++ {
				u, v := idx[k], idx[(k+1)%3]
				if u > v {
					u, v = v, u
				}
				if u != v && !seen[[2]int32{u, v}] {
					seen[[2]int32{u, v}] = true
					a.edges = append(a.edges, [2]int32{u, v})
				}
			}
		}
	}
	a.part = names
	// Sort edges so that the order does not depend on map iteration.
	sort.Slice(a.edges, func(i, j int) bool {
		if a.edges[i][0] != a.edges[j][0] {
			return a.edges[i][0] < a.edges[j][0]
		}
		return a.edges[i][1] < a.edges[j][1]
	})
	return a
}

// bindAt is the bind mesh after the point's morphs.
func (a *auditMesh) bindAt(w []float64) []mesh.Vec3 {
	out := append([]mesh.Vec3(nil), a.base...)
	for t, wt := range w {
		if wt == 0 {
			continue
		}
		for i, d := range a.delta[t] {
			out[i] = mesh.Add(out[i], mesh.Scale(d, wt))
		}
	}
	return out
}

// poseRef names a measured pose.
type poseRef struct {
	clip string
	time float64
}

// poseList is the bind pose and the key times of every delivered skeletal
// clip: the sorted union of the times of all its channels.
func poseList(cds []*clipData) []poseRef {
	out := []poseRef{{clip: "bind"}}
	for _, cd := range cds {
		set := map[float64]bool{}
		for _, ch := range cd.clip.Channels {
			for _, t := range ch.Times {
				set[t] = true
			}
		}
		var ts []float64
		for t := range set {
			ts = append(ts, t)
		}
		sort.Float64s(ts)
		for _, t := range ts {
			out = append(out, poseRef{cd.src.Name, t})
		}
	}
	return out
}

// gearCache holds a mount's gear at one output: its triangles in the host's
// bind frame and their tree.
type gearCache struct {
	tris   []geom.Tri
	label  []string
	bvh    *geom.BVH
	box    [2]mesh.Vec3
	joint  int
	output string
}

// gearBindTris returns the triangles of a gear file's default look in the
// host's bind frame, with the node each belongs to.
func (e *elab) gearBindTris(rl *rigLevel, m resolve.Mount, gf *File) (*gearCache, bool) {
	joint, spos, sq, ok := e.socketFrame(rl, m)
	if !ok {
		return nil, false
	}
	frame := nodeWorld{q: sq, s: mesh.Vec3{1, 1, 1}}
	frame.t = mesh.Sub(spos, sq.Rotate(mesh.Vec3(m.PlugAt)))
	gc := &gearCache{joint: joint}
	first := true
	for _, d := range defaultLook(gf.Doc) {
		p := d.prim
		pos := func(v uint32) mesh.Vec3 {
			pt := d.world.point(mesh.Vec3{float64(p.Positions[3*v]), float64(p.Positions[3*v+1]), float64(p.Positions[3*v+2])})
			return frame.point(pt)
		}
		for i := 0; i+2 < len(p.Indices); i += 3 {
			t := geom.Tri{pos(p.Indices[i]), pos(p.Indices[i+1]), pos(p.Indices[i+2])}
			gc.tris = append(gc.tris, t)
			gc.label = append(gc.label, d.nodeName)
			for _, c := range t {
				if first {
					gc.box, first = [2]mesh.Vec3{c, c}, false
					continue
				}
				for k := 0; k < 3; k++ {
					gc.box[0][k], gc.box[1][k] = math.Min(gc.box[0][k], c[k]), math.Max(gc.box[1][k], c[k])
				}
			}
		}
	}
	gc.bvh = geom.NewBVH(gc.tris)
	return gc, true
}

// pointScan is what one runtime point measured over every pose.
type pointScan struct {
	lowest, highest, torn, strained       float64
	lowestAt, highestAt, tornAt, strainAt poseRef
	fit                                   map[string]*fitScan
	poses                                 int
}

type fitScan struct {
	dist      float64
	found     bool
	at        poseRef
	gearPart  string
	hostPart  string
	gearOut   string
	searchMax float64
}

// audit runs the audit of one level and reports its findings.
func (e *elab) audit(rl *rigLevel, cds []*clipData, lvl int) (*AuditReport, bool) {
	rc := e.r.RigCheck
	var fitMounts []*mountGear
	for _, g := range e.mounts {
		if g.m.HasFit && g.mode != "bake" {
			fitMounts = append(fitMounts, g)
		}
		if g.m.HasFit && g.mode == "bake" {
			// A baked gear is part of the host mesh; its fit is checked on the same terms.
			fitMounts = append(fitMounts, g)
		}
	}
	if rc == nil && len(fitMounts) == 0 {
		return nil, true
	}
	pl := e.planPoints(e.policy.RuntimePoints)
	if pl.partial {
		d := diag.New(diag.CodeRuntimePart, e.spanOf(""), "output %s level %d: %s", e.oname, lvl, pl.partialMessage())
		e.diags = append(e.diags, d)
	}
	am := buildAuditMesh(rl)
	poses := poseList(cds)
	clipOf := map[string]*clipData{}
	for _, cd := range cds {
		clipOf[cd.src.Name] = cd
	}
	// The gear of every (mount, output) the points select.
	gears := map[string]*gearCache{}
	for _, g := range fitMounts {
		outs := map[string]bool{g.m.Output: true}
		for _, p := range pl.points {
			outs[p.gear[g.m.Name]] = true
		}
		var names []string
		for o := range outs {
			names = append(names, o)
		}
		sort.Strings(names)
		for _, o := range names {
			prod, ok := e.gearProduct(g, o)
			if !ok {
				return nil, false
			}
			f := prodFile(prod, lvl)
			gc, ok := e.gearBindTris(rl, g.m, f)
			if !ok {
				return nil, false
			}
			gc.output = o
			gears[g.m.Name+"/"+o] = gc
		}
	}
	up := e.upIndex()
	results := make([]*pointScan, len(pl.points))
	var wg sync.WaitGroup
	next := make(chan int)
	workers := min(runtime.GOMAXPROCS(0), len(pl.points))
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				results[i] = e.scanPoint(rl, am, poses, clipOf, pl.points[i], fitMounts, gears, up, lvl)
			}
		}()
	}
	for i := range pl.points {
		next <- i
	}
	close(next)
	wg.Wait()

	rep := &AuditReport{Output: e.oname, Level: lvl, Points: len(pl.points), Partial: pl.partial, Omitted: pl.omitted}
	rep.GroundLowest.Value, rep.GroundHighest.Value = math.Inf(1), math.Inf(-1)
	for i, sc := range results {
		pt := pl.points[i].label
		rep.Poses += sc.poses
		if sc.lowest < rep.GroundLowest.Value {
			rep.GroundLowest = Measure{sc.lowest, pt, sc.lowestAt.clip, sc.lowestAt.time}
		}
		if sc.highest > rep.GroundHighest.Value {
			rep.GroundHighest = Measure{sc.highest, pt, sc.highestAt.clip, sc.highestAt.time}
		}
		if sc.torn > rep.Torn.Value {
			rep.Torn = Measure{sc.torn, pt, sc.tornAt.clip, sc.tornAt.time}
		}
		if sc.strained > rep.Strained.Value {
			rep.Strained = Measure{sc.strained, pt, sc.strainAt.clip, sc.strainAt.time}
		}
	}
	for _, g := range fitMounts {
		fr := FitReport{Mount: g.m.Name, Required: g.m.Clearance, Clearance: math.Max(10*g.m.Clearance, 0.01), AtLeast: true}
		best := math.Inf(1)
		for i, sc := range results {
			fs := sc.fit[g.m.Name]
			if fs == nil || !fs.found || fs.dist >= best {
				continue
			}
			best = fs.dist
			fr = FitReport{Mount: g.m.Name, Required: g.m.Clearance, Clearance: fs.dist, Point: pl.points[i].label,
				Clip: fs.at.clip, Time: fs.at.time, Gear: fs.gearOut, GearPart: fs.gearPart, HostPart: fs.hostPart}
		}
		rep.Fit = append(rep.Fit, fr)
	}
	e.judgeAudit(rep, rc)
	return rep, true
}

// judgeAudit turns the measurements into diagnostics.
func (e *elab) judgeAudit(rep *AuditReport, rc *resolve.RigCheck) {
	if rc != nil {
		lo, hi := rc.GroundLo, rc.GroundHi
		if rep.GroundLowest.Value < lo || rep.GroundHighest.Value > hi {
			m, what := rep.GroundLowest, "smallest"
			if rep.GroundHighest.Value > hi {
				m, what = rep.GroundHighest, "largest"
			}
			e.report(diag.CodeRigGround, e.r.RigCheck.Span,
				"output %s level %d: the lowest skinned vertex of a non-airborne pose, at its %s over every pose and point, is %.1f mm above the ground, outside the band %.1f..%.1f mm (clip %s at %s, runtime point %s)",
				e.oname, rep.Level, what, float64(m.Value*1000), float64(lo*1000), float64(hi*1000), m.Clip, fmtTime(m.Time), m.Point)
		}
		if rep.Torn.Value > rc.Torn {
			e.report(diag.CodeRigTorn, rc.Span,
				"output %s level %d: %.2f%% of the edges of the posed skin are torn (longer than 3x or shorter than 1/4 of their bind length), above the %.2f%% allowed (clip %s at %s, runtime point %s)",
				e.oname, rep.Level, float64(rep.Torn.Value*100), float64(rc.Torn*100), rep.Torn.Clip, fmtTime(rep.Torn.Time), rep.Torn.Point)
		}
	}
	for _, f := range rep.Fit {
		if f.AtLeast || (f.Clearance >= f.Required && f.Clearance > 0) {
			continue
		}
		span := e.r.Mounts[0].FitSpan
		for _, m := range e.r.Mounts {
			if m.Name == f.Mount {
				span = m.FitSpan
			}
		}
		verb := "comes within"
		if f.Clearance == 0 {
			verb = "touches or penetrates"
		}
		e.report(diag.CodeMount, span,
			"output %s level %d: the gear %s of the mount `%s` %s %.2f mm of the host skin (gear part %s, host part %s), but `fit` requires %.2f mm (clip %s at %s, runtime point %s)",
			e.oname, rep.Level, f.Gear, f.Mount, verb, float64(f.Clearance*1000), f.GearPart, f.HostPart, float64(f.Required*1000), f.Clip, fmtTime(f.Time), f.Point)
	}
}

// scanPoint measures every pose of one runtime point.
func (e *elab) scanPoint(rl *rigLevel, am *auditMesh, poses []poseRef, clipOf map[string]*clipData, pt runtimePoint,
	fitMounts []*mountGear, gears map[string]*gearCache, up, lvl int) *pointScan {
	sc := &pointScan{lowest: math.Inf(1), highest: math.Inf(-1), fit: map[string]*fitScan{}}
	w := make([]float64, len(rl.morphs))
	for i, m := range rl.morphs {
		w[i] = weightAt(m.weight, pt.vals)
	}
	inst := *rl.inst
	inst.Weights = w
	if rs := e.r.Rig.Skeleton.RootScale; rs != "" {
		if v, ok := pt.vals[rs]; ok {
			inst.HasRootScale, inst.RootScale = true, v.Num
		}
	}
	bind := am.bindAt(w)
	blen := make([]float64, len(am.edges))
	for i, ed := range am.edges {
		blen[i] = mesh.Len(mesh.Sub(bind[ed[0]], bind[ed[1]]))
	}
	ibm := inst.InverseBinds()
	posed := make([]mesh.Vec3, len(bind))
	for _, ps := range poses {
		var clip *rig.Clip
		airborne := false
		if ps.clip != "bind" {
			cd := clipOf[ps.clip]
			clip, airborne = cd.clip, cd.src.Airborne
		}
		skin := rig.SkinMatrices(rl.sk.World(inst.Locals(clip, ps.time)), ibm)
		for i, p := range bind {
			var acc mesh.Vec3
			for _, in := range am.infl[i] {
				acc = mesh.Add(acc, mesh.Scale(skin[in.Joint].Apply(p), in.W))
			}
			posed[i] = acc
		}
		sc.poses++
		if !airborne && ps.clip != "bind" {
			lo := posed[0][up]
			for _, p := range posed[1:] {
				lo = math.Min(lo, p[up])
			}
			// The lowest vertex of this pose; the band is for this number.
			if lo < sc.lowest {
				sc.lowest, sc.lowestAt = lo, ps
			}
			if lo > sc.highest {
				sc.highest, sc.highestAt = lo, ps
			}
		}
		torn, strained := 0, 0
		n := 0
		for i, ed := range am.edges {
			if blen[i] == 0 {
				continue
			}
			n++
			r := mesh.Len(mesh.Sub(posed[ed[0]], posed[ed[1]])) / blen[i]
			if r < 0.25 || r > 3 {
				torn++
			}
			if r < 1/1.5 || r > 1.5 {
				strained++
			}
		}
		if n > 0 {
			if s := float64(torn) / float64(n); s > sc.torn {
				sc.torn, sc.tornAt = s, ps
			}
			if s := float64(strained) / float64(n); s > sc.strained {
				sc.strained, sc.strainAt = s, ps
			}
		}
		for _, g := range fitMounts {
			gc := gears[g.m.Name+"/"+pt.gear[g.m.Name]]
			fs := sc.fit[g.m.Name]
			if fs == nil {
				fs = &fitScan{dist: math.Inf(1), searchMax: math.Max(10*g.m.Clearance, 0.01)}
				sc.fit[g.m.Name] = fs
			}
			e.fitPose(am, posed, skin[gc.joint], gc, fs, ps, g.m.Clearance)
		}
	}
	return sc
}

// fitPose measures the gap of one pose. The gear is rigid on its joint, so the
// host skin is moved into the gear's frame by the inverse of the joint's skin
// matrix and the gear tree is built once.
func (e *elab) fitPose(am *auditMesh, posed []mesh.Vec3, skin rig.Rigid, gc *gearCache, fs *fitScan, ps poseRef, clearance float64) {
	inv := skin.Inverse()
	limit := fs.searchMax
	lo, hi := gc.box[0], gc.box[1]
	for k := 0; k < 3; k++ {
		lo[k] -= limit
		hi[k] += limit
	}
	local := make([]mesh.Vec3, len(posed))
	for i, p := range posed {
		local[i] = inv.Apply(p)
	}
	var tris []geom.Tri
	var idx []int
	for ti, t := range am.tris {
		a, b, c := local[t[0]], local[t[1]], local[t[2]]
		out := false
		for k := 0; k < 3; k++ {
			if math.Max(a[k], math.Max(b[k], c[k])) < lo[k] || math.Min(a[k], math.Min(b[k], c[k])) > hi[k] {
				out = true
				break
			}
		}
		if !out {
			tris = append(tris, geom.Tri{a, b, c})
			idx = append(idx, ti)
		}
	}
	if len(tris) == 0 {
		return
	}
	res := geom.MinDistance(gc.bvh, geom.NewBVH(tris), math.Min(limit, fs.dist))
	// Search below the best so far, so that the first of equal gaps is kept.
	if !res.Found {
		return
	}
	fs.dist, fs.found, fs.at = res.Distance, true, ps
	fs.gearPart = gc.label[res.A]
	fs.hostPart = am.part[am.partOf[idx[res.B]]]
	fs.gearOut = gc.output
}

func (p poseRef) String() string { return fmt.Sprintf("%s@%s", p.clip, fmtTime(p.time)) }
