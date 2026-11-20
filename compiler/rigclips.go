package compiler

import (
	"fmt"
	"sort"

	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/rig"
	"m31labs.dev/greenware/resolve"
)

// clipData is a sampled clip as the kernel sees it: authored local values,
// conditioned to start at 0 s and end at the duration.
type clipData struct {
	src  *resolve.RigClip
	clip *rig.Clip
	// has reports the (joint, path) pairs that carry a channel.
	has map[chanKey]bool
}

type chanKey struct {
	joint int
	path  rig.Path
}

// kernelClip builds the kernel clip of a sampled clip: a rotation channel for
// each joint the pose names and a translation channel for the root. A pose
// rotation Q is in the bind frame, so the authored local rotation is R0 Q for
// the joint's local rest rotation R0.
func (e *elab) kernelClip(c *resolve.RigClip, rl *rigLevel) (*clipData, bool) {
	sk := rl.sk
	out := &clipData{src: c, has: map[chanKey]bool{}}
	cl := &rig.Clip{Name: c.Export, Duration: c.Duration, Loop: c.Loop, Airborne: c.Airborne}
	interp := rig.Linear
	if c.Interp == "step" {
		interp = rig.Step
	}
	times := make([]float64, len(c.Samples))
	for i, s := range c.Samples {
		times[i] = s.Time
	}
	if len(c.Samples) == 0 {
		return out, true
	}
	// One rotation channel for each joint of the first sample.
	for _, r := range c.Samples[0].Rots {
		ji := sk.Index(r.Joint)
		ch := rig.Channel{Target: ji, Path: rig.Rotation, Interp: interp, Times: times}
		for _, s := range c.Samples {
			q := e.poseQuat(s, r.Joint)
			v := rl.rest[ji].R.Mul(q).Normalize()
			ch.Values = append(ch.Values, v[0], v[1], v[2], v[3])
		}
		cond, err := rig.Condition(ch, c.Duration)
		if err != nil {
			e.report(diag.CodeClipKey, c.Span, "the clip `%s`, joint `%s`: %v", c.Name, r.Joint, err)
			return nil, false
		}
		if i := rig.TimesAre32(cond.Times); i >= 0 {
			e.report(diag.CodeClipKey, c.Span, "the clip `%s`: key %d is not later than key %d once written as float32", c.Name, i, i-1)
			return nil, false
		}
		cl.Channels = append(cl.Channels, cond)
		out.has[chanKey{ji, rig.Rotation}] = true
	}
	if c.Samples[0].HasRoot {
		ji := sk.Root()
		ch := rig.Channel{Target: ji, Path: rig.Translation, Interp: interp, Times: times}
		for _, s := range c.Samples {
			ch.Values = append(ch.Values, s.Root[0], s.Root[1], s.Root[2])
		}
		cond, err := rig.Condition(ch, c.Duration)
		if err != nil {
			e.report(diag.CodeClipKey, c.Span, "the clip `%s`, root: %v", c.Name, err)
			return nil, false
		}
		cl.Channels = append(cl.Channels, cond)
		out.has[chanKey{ji, rig.Translation}] = true
	}
	out.clip = cl
	return out, true
}

// poseQuat is the pose rotation Q the sample gives a joint, reflected for the
// copy of a mirror.
func (e *elab) poseQuat(s resolve.RigSample, joint string) rig.Quat {
	for _, r := range s.Rots {
		if r.Joint == joint {
			q := rig.QEuler(r.Euler[0], r.Euler[1], r.Euler[2])
			if r.Reflect != "" {
				q = reflectQuat(q, r.Reflect)
			}
			return q.Normalize()
		}
	}
	return rig.QIdentity
}

// checkLimits tests every pose key against the joint's band and hard limit.
// It never clamps.
func (e *elab) checkLimits(c *resolve.RigClip, rl *rigLevel) {
	for _, s := range c.Samples {
		for _, r := range s.Rots {
			ji := rl.sk.Index(r.Joint)
			q := e.poseQuat(s, r.Joint)
			name := rl.sk.Joints[ji].Name
			if v := rl.sd.limit[ji]; v.AnySet {
				for _, x := range rl.sk.Check(ji, q, v) {
					e.report(diag.CodeRigLimit, c.Span, "the clip `%s` at %s leaves the hard limit of `%s`: %s", c.Name, fmtTime(s.Time), name, describeViolation(x))
				}
			}
			if v := rl.sd.band[ji]; v.AnySet {
				for _, x := range rl.sk.Check(ji, q, v) {
					d := diag.New(diag.CodeRigBand, c.Span, "the clip `%s` at %s leaves the natural band of `%s`: %s", c.Name, fmtTime(s.Time), name, describeViolation(x))
					if rc := e.r.RigCheck; rc != nil && rc.BandsError {
						d.Severity = diag.Error
					}
					e.diags = append(e.diags, d)
				}
			}
		}
	}
}

func fmtTime(t float64) string { return fmt.Sprintf("%.6gs", t) }

func describeViolation(v rig.Violation) string {
	if v.Undefined {
		return v.Component + " is undefined at a pure 180 degree swing"
	}
	return fmt.Sprintf("%s is %.4g deg, outside %.4g..%.4g deg", v.Component, rad2deg(v.Value), rad2deg(v.Range.Lo), rad2deg(v.Range.Hi))
}

func rad2deg(r float64) float64 { return float64(r*180) / 3.141592653589793 }

// animNodes are the nodes the animations drive: the node of each joint and
// the wrapper nodes by name. They exist already when a clip is written.
type animNodes struct {
	joint   []int          // node of each joint
	wrapper map[string]int // lookup only
}

// skeletal writes the animation of a sampled clip. Values are the ones a
// reader applies at the default look: translation keys plus the weighted
// parent-local offsets (the root's up component scaled by the root scale),
// rotations as authored. A joint property that the idle clip sets away from
// the adjusted rest and this clip leaves out gets a constant channel at the
// adjusted rest, so that playing the clip does not inherit the idle pose.
func (e *elab) skeletal(cd *clipData, rl *rigLevel, nn animNodes, idle *clipData) (gltf.Animation, bool) {
	adj := rl.inst.Adjusted()
	var chans []animChan
	for _, ch := range cd.clip.Channels {
		c := animChan{node: nn.joint[ch.Target], path: ch.Path, times: ch.Times, step: ch.Interp == rig.Step}
		for _, t := range ch.Times {
			loc := rl.inst.Locals(cd.clip, t)[ch.Target]
			if ch.Path == rig.Translation {
				c.vals = append(c.vals, loc.T[0], loc.T[1], loc.T[2])
			} else {
				c.vals = append(c.vals, loc.R[0], loc.R[1], loc.R[2], loc.R[3])
			}
		}
		chans = append(chans, c)
	}
	if idle != nil && idle != cd {
		il := rl.inst.Locals(idle.clip, 0)
		for _, ich := range idle.clip.Channels {
			k := chanKey{ich.Target, ich.Path}
			if cd.has[k] {
				continue
			}
			j := ich.Target
			var rest []float64
			if ich.Path == rig.Translation {
				if il[j].T == adj[j].T {
					continue
				}
				rest = []float64{adj[j].T[0], adj[j].T[1], adj[j].T[2]}
			} else {
				if il[j].R.Dot(adj[j].R) >= 1-1e-12 {
					continue
				}
				rest = []float64{adj[j].R[0], adj[j].R[1], adj[j].R[2], adj[j].R[3]}
			}
			d := cd.clip.Duration
			chans = append(chans, animChan{node: nn.joint[j], path: ich.Path, times: []float64{0, d}, vals: append(append([]float64(nil), rest...), rest...)})
		}
	}
	return e.finishAnim(cd.src.Export, chans), true
}

// animChan is one channel of an animation before it is written: the node, the
// property, the key times and the flat values.
type animChan struct {
	node  int
	path  rig.Path
	times []float64
	vals  []float64
	step  bool
}

// finishAnim sorts the channels by node and property and writes the
// animation.
func (e *elab) finishAnim(name string, chans []animChan) gltf.Animation {
	sort.SliceStable(chans, func(a, b int) bool {
		if chans[a].node != chans[b].node {
			return chans[a].node < chans[b].node
		}
		return chans[a].path < chans[b].path
	})
	an := gltf.Animation{Name: name}
	for _, c := range chans {
		s := gltf.AnimSampler{}
		if c.step {
			s.Interpolation = gltf.InterpStep
		}
		for _, t := range c.times {
			s.Input = append(s.Input, det.Round32(t))
		}
		for _, v := range c.vals {
			s.Output = append(s.Output, det.Round32(v))
		}
		path := gltf.PathTranslation
		if c.path == rig.Rotation {
			path = gltf.PathRotation
		}
		an.Samplers = append(an.Samplers, s)
		an.Channels = append(an.Channels, gltf.Channel{Node: c.node, Path: path, Sampler: len(an.Samplers) - 1})
	}
	return an
}

// keyed writes the animation of a clip of `key` rows on a wrapper node.
func (e *elab) keyed(c *resolve.RigClip, nn animNodes) (gltf.Animation, bool) {
	node, ok := nn.wrapper[c.Node]
	if !ok {
		e.report(diag.CodeUnknownName, c.Span, "the clip `%s` drives the wrapper %q, which the file does not write", c.Name, c.Node)
		return gltf.Animation{}, false
	}
	rot := c.Rows[0].Rot
	path := rig.Translation
	if rot {
		path = rig.Rotation
	}
	ch := rig.Channel{Path: path}
	if c.Interp == "step" {
		ch.Interp = rig.Step
	}
	for _, k := range c.Rows {
		ch.Times = append(ch.Times, k.Time)
		if rot {
			q := rig.QEuler(k.Value[0], k.Value[1], k.Value[2]).Normalize()
			ch.Values = append(ch.Values, q[0], q[1], q[2], q[3])
		} else {
			ch.Values = append(ch.Values, k.Value[0], k.Value[1], k.Value[2])
		}
	}
	cond, err := rig.Condition(ch, c.Duration)
	if err != nil {
		e.report(diag.CodeClipKey, c.Span, "the clip `%s`: %v", c.Name, err)
		return gltf.Animation{}, false
	}
	if i := rig.TimesAre32(cond.Times); i >= 0 {
		e.report(diag.CodeClipKey, c.Span, "the clip `%s`: key %d is not later than key %d once written as float32", c.Name, i, i-1)
		return gltf.Animation{}, false
	}
	return e.finishAnim(c.Export, []animChan{{node: node, path: path, times: cond.Times, vals: cond.Values, step: ch.Interp == rig.Step}}), true
}
