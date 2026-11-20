package resolve_test

import (
	"math"
	"strings"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/resolve"
)

const rigPrefix = `greenware 1

asset a { root = a idle = rest length = m angle = deg wrappers = ["slide"] }

param reach : ratio = 1 in 0.8..1.25 runtime
param bulk : ratio = 0 in 0..1 runtime
param species : enum(human, vael, brannoc) = human runtime

`

const rigBase = `
skeleton s {
  joint hip {
    at = (0, 1, 0)
    joint knee { at = (0, 0.5, 0)
      joint foot { at = (0, 0, 0.1) }
    }
  }
}

part a {
  joint = hip
  sphere ball { radius = 0.1 }
  sphere shin { radius = 0.1 joint = knee at = (0, 0.5, 0) }
}

clip rest {
  duration = 1s
  pose { hip = rot(0, 0, 0) }
}
`

func rigDiags(src string) ([]diag.Diagnostic, *resolve.Resolved) {
	f, ds := ast.Lower(lang.Parse([]byte(src)))
	r, more := resolve.Evaluate(f, nil)
	return diag.Dedup(append(ds, more...)), r
}

func has(ds []diag.Diagnostic, code string, frag string) bool {
	for _, d := range ds {
		if d.Code == code && strings.Contains(d.Message, frag) {
			return true
		}
	}
	return false
}

func TestRigBaseIsClean(t *testing.T) {
	ds, r := rigDiags(rigPrefix + rigBase)
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	sk := r.Rig.Skeleton
	if len(sk.Joints) != 3 || sk.Joints[0].Name != "hip" || sk.Joints[2].Parent != "knee" {
		t.Errorf("joints %+v", sk.Joints)
	}
	// Distances are metres, angles radians.
	if sk.Joints[1].At != [3]float64{0, 0.5, 0} {
		t.Errorf("knee at %v", sk.Joints[1].At)
	}
	if r.Rig.Idle != "rest" || len(r.Rig.Clips) != 1 || len(r.Rig.Clips[0].Samples) != 2 {
		t.Errorf("rig %+v", r.Rig)
	}
	if r.Rig.Clips[0].Samples[1].Time != 1 {
		t.Errorf("the last sample is at %v s", r.Rig.Clips[0].Samples[1].Time)
	}
}

func TestRigDiagnostics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
		frag string
	}{
		{"two roots", rigPrefix + strings.Replace(rigBase, "  joint hip {", "  joint extra { at = (1, 1, 0) }\n  joint hip {", 1), diag.CodeRigStructure, "exactly one"},
		{"unbound shape", rigPrefix + strings.Replace(rigBase, "  joint = hip\n", "", 1), diag.CodeRigUnbound, "has no joint"},
		{"unknown joint", rigPrefix + strings.Replace(rigBase, "joint = knee", "joint = kne", 1), diag.CodeUnknownName, "kne"},
		{"pose joint misspelt", rigPrefix + strings.Replace(rigBase, "hip = rot", "hep = rot", 1), diag.CodeUnknownName, "hep"},
		{"idle missing", strings.Replace(rigPrefix, "idle = rest", "", 1) + rigBase, diag.CodeMissing, "needs `idle`"},
		{"idle unknown", strings.Replace(rigPrefix, "idle = rest", "idle = rst", 1) + rigBase, diag.CodeUnknownName, "rst"},
		{"joint order short", rigPrefix + strings.Replace(rigBase, "skeleton s {", "skeleton s {\n  joint_order = [hip, knee]", 1), diag.CodeRigStructure, "leaves out the joint `foot`"},
		{"joint order twice", rigPrefix + strings.Replace(rigBase, "skeleton s {", "skeleton s {\n  joint_order = [hip, knee, foot, foot]", 1), diag.CodeRigStructure, "twice"},
		{"root scale not runtime", rigPrefix + "param k : ratio = 1\n" + strings.Replace(rigBase, "skeleton s {", "skeleton s {\n  root_scale = k", 1), diag.CodeType, "numeric runtime"},
		{"duplicate joint", rigPrefix + strings.Replace(rigBase, "joint foot { at = (0, 0, 0.1) }", "joint foot { at = (0, 0, 0.1) }\n joint foot { at = (0, 0, 0.2) }", 1), diag.CodeDuplicate, "foot"},
		{"explicit axis swing", rigPrefix + strings.Replace(rigBase, "joint foot {", "joint foot { twist_axis = y swing_y = -10..10", 1), diag.CodeFieldRange, "swing about the twist axis"},
		{"empty range", rigPrefix + strings.Replace(rigBase, "joint foot {", "joint foot { twist = 40..-40", 1), diag.CodeFieldRange, "empty"},
		{"mount", rigPrefix + rigBase + "\nmount m { asset = x socket = y }", diag.CodeUnsupported, "mount"},
		{"lengthen two children", rigPrefix + strings.Replace(rigBase, "joint foot { at = (0, 0, 0.1) }", "joint foot { at = (0, 0, 0.1) }\n joint toe { at = (0, 0, 0.2) }", 1) + "\nskin { lengthen { joint = knee to = 0.6 } }", diag.CodeRigStructure, "exactly one child"},
		{"blend not a child", rigPrefix + rigBase + "\nskin { blend { parent = hip child = foot centre = (0, 0.5, 0) axis = (0, -1, 0) width = 0.1 } }", diag.CodeRigStructure, "immediate child"},
		{"blend zero axis", rigPrefix + rigBase + "\nskin { blend { parent = hip child = knee centre = (0, 0.5, 0) axis = (0, 0, 0) width = 0.1 } }", diag.CodeRigStructure, "nonzero"},
		{"blend zero width", rigPrefix + rigBase + "\nskin { blend { parent = hip child = knee centre = (0, 0.5, 0) axis = (0, 1, 0) width = 0 } }", diag.CodeFieldRange, "width"},
		{"blend joint() of nothing", rigPrefix + rigBase + "\nskin { blend { parent = hip child = knee centre = joint(nope) axis = (0, 1, 0) width = 0.1 } }", diag.CodeUnknownName, "nope"},
		{"skin step unknown", rigPrefix + rigBase + "\nskin { smooth { } }", diag.CodeUnknownField, "smooth"},
		{"max influences 5", rigPrefix + rigBase + "\nskin { max_influences = 5 }", diag.CodeFieldRange, "max_influences"},
		{"anchor 1", rigPrefix + rigBase + "\nskin { lengthen { joint = hip to = 0.6 anchor = 1 } }", diag.CodeFieldRange, "anchor"},
		{"clip duration missing", rigPrefix + strings.Replace(rigBase, "  duration = 1s\n", "", 1), diag.CodeMissing, "needs `duration`"},
		{"clip both", rigPrefix + rigBase + "\nclip x { duration = 1s pose { hip = rot(0, 0, 0) } key (0s, (0, 0, 0)) }", diag.CodeType, "not both"},
		{"clip empty", rigPrefix + rigBase + "\nclip x { duration = 1s }", diag.CodeMissing, "`pose` block or `key` rows"},
		{"clip duplicate name", rigPrefix + rigBase + "\nclip other { name = \"rest\" duration = 1s pose { hip = rot(0, 0, 0) } }", diag.CodeDuplicate, "\"rest\""},
		{"keys reversed", rigPrefix + rigBase + "\nclip x { node = \"slide\" key (2s, (0, 0, 0)) key (1s, (1, 0, 0)) }", diag.CodeClipKey, "strictly"},
		{"keys repeated", rigPrefix + rigBase + "\nclip x { node = \"slide\" key (1s, (0, 0, 0)) key (1s, (1, 0, 0)) }", diag.CodeClipKey, "strictly"},
		{"key later than duration", rigPrefix + rigBase + "\nclip x { duration = 1s node = \"slide\" key (0s, (0, 0, 0)) key (2s, (1, 0, 0)) }", diag.CodeClipKey, "later than the clip's duration"},
		{"key negative", rigPrefix + rigBase + "\nclip x { node = \"slide\" key (-1s, (0, 0, 0)) key (1s, (1, 0, 0)) }", diag.CodeClipKey, "negative"},
		{"key mixes", rigPrefix + rigBase + "\nclip x { node = \"slide\" key (0s, (0, 0, 0)) key (1s, rot(0, 0, 0)) }", diag.CodeClipKey, "not both"},
		{"key float32 equal", rigPrefix + rigBase + "\nclip x { node = \"slide\" key (1s, (0, 0, 0)) key (1.0000000001s, (1, 0, 0)) }", diag.CodeClipKey, "float32"},
		{"key node unknown", rigPrefix + rigBase + "\nclip x { node = \"nope\" key (0s, (0, 0, 0)) key (1s, (1, 0, 0)) }", diag.CodeUnknownName, "wrapper"},
		{"key needs node", rigPrefix + rigBase + "\nclip x { key (0s, (0, 0, 0)) }", diag.CodeMissing, "needs `node`"},
		{"morph needs weight", rigPrefix + rigBase + "\nmorph m { offset { parts = [ball] by = (0, 0.1, 0) } }", diag.CodeMissing, "weight"},
		{"morph needs op", rigPrefix + rigBase + "\nmorph m { weight = 1 }", diag.CodeMissing, "at least one operation"},
		{"morph two params", rigPrefix + rigBase + "\nmorph m { weight = reach * bulk offset { parts = [ball] by = (0, 0.1, 0) } }", diag.CodeRuntime, "one runtime parameter"},
		{"morph product", rigPrefix + rigBase + "\nmorph m { weight = reach * reach offset { parts = [ball] by = (0, 0.1, 0) } }", diag.CodeRuntime, "piecewise linear"},
		{"morph sine", rigPrefix + rigBase + "\nmorph m { weight = sqrt(reach) offset { parts = [ball] by = (0, 0.1, 0) } }", diag.CodeRuntime, "not piecewise linear"},
		{"morph duplicate export", rigPrefix + rigBase + "\nmorph a1 { name = \"x\" weight = 1 offset { parts = [ball] by = (0, 0.1, 0) } }\nmorph a2 { name = \"x\" weight = 1 offset { parts = [ball] by = (0, 0.1, 0) } }", diag.CodeDuplicate, "\"x\""},
		{"stretch from above to", rigPrefix + rigBase + "\nmorph m { weight = 1 stretch { axis = y from = 2 to = 1 by = 10% } }", diag.CodeFieldRange, "from"},
		{"stretch negative axis", rigPrefix + rigBase + "\nmorph m { weight = 1 stretch { axis = -y from = 1 to = 2 by = 10% } }", diag.CodeUnsupported, "negative axis"},
		{"follow with offset", rigPrefix + rigBase + "\nmorph m { weight = 1 joints = follow offset { parts = [ball] by = (0, 0.1, 0) } }", diag.CodeUnsupported, "follow"},
		{"inflate below -1", rigPrefix + rigBase + "\nmorph m { weight = 1 inflate { parts = [ball] by = -150% } }", diag.CodeFieldRange, "above -1"},
		{"morph part unknown", rigPrefix + rigBase + "\nmorph m { weight = 1 offset { parts = [bal] by = (0, 0.1, 0) } }", diag.CodeUnknownName, "bal"},
		{"when in rig", rigPrefix + rigBase + "\nmorph m { weight = 1 when true { offset { parts = [ball] by = (0, 0.1, 0) } } }", diag.CodeUnsupported, "when"},
		{"second skeleton", rigPrefix + rigBase + "\nskeleton t { joint j { at = (0, 0, 0) } }", diag.CodeUnsupported, "second skeleton"},
		{"clip ground", rigPrefix + strings.Replace(rigBase, "duration = 1s", "duration = 1s ground = x", 1), diag.CodeUnsupported, "ground"},
		{"mirror needs pair", rigPrefix + strings.Replace(rigBase, "joint foot { at = (0, 0, 0.1) }", "mirror { plane = x joint foot { at = (0, 0, 0.1) } }", 1), diag.CodeMirrorName, "foot"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds, _ := rigDiags(c.src)
			if !has(ds, c.code, c.frag) {
				t.Errorf("want %s mentioning %q, got %v", c.code, c.frag, ds)
			}
		})
	}
}

func TestMorphWeightTables(t *testing.T) {
	type pt = resolve.RigBreak
	cases := []struct {
		name   string
		expr   string
		breaks []pt
		def    float64
	}{
		{"parameter itself", "bulk", []pt{{0, 0}, {1, 1}}, 0},
		{"clamped line", "clamp((reach - 1) / 0.25, 0, 1)", []pt{{0.8, 0}, {1, 0}, {1.25, 1}}, 0},
		{"clamp both ways", "clamp((reach - 1) / 0.1, -1, 1)", []pt{{0.8, -1}, {0.9, -1}, {1.1, 1}, {1.25, 1}}, 0},
		{"max of line and zero", "max(0, (1 - reach) / 0.2)", []pt{{0.8, 1}, {1, 0}, {1.25, 0}}, 0},
		{"abs", "abs(reach - 1)", []pt{{0.8, 0.2}, {1, 0}, {1.25, 0.25}}, 0},
		{"scaled", "2 * bulk + 1", []pt{{0, 1}, {1, 3}}, 1},
		{"min", "min(bulk, 0.4)", []pt{{0, 0}, {0.4, 0.4}, {1, 0.4}}, 0},
		{"athlete stature", "clamp((reach - 1) / 0.08, 0, 1)", []pt{{0.8, 0}, {1, 0}, {1.08, 1}, {1.25, 1}}, 0},
		{"negated and divided", "-bulk / 4", []pt{{0, 0}, {1, -0.25}}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds, r := rigDiags(rigPrefix + rigBase + "\nmorph m { weight = " + c.expr + " offset { parts = [ball] by = (0, 0.1, 0) } }")
			if len(ds) != 0 {
				t.Fatalf("diagnostics: %v", ds)
			}
			w := r.Rig.Morphs[0].Weight
			if len(w.Breaks) != len(c.breaks) {
				t.Fatalf("breaks %v, want %v", w.Breaks, c.breaks)
			}
			for i, b := range c.breaks {
				if math.Abs(w.Breaks[i].X-b.X) > 1e-12 || math.Abs(w.Breaks[i].Y-b.Y) > 1e-12 {
					t.Errorf("breaks %v, want %v", w.Breaks, c.breaks)
					break
				}
			}
			if math.Abs(w.Default-c.def) > 1e-12 {
				t.Errorf("default %v, want %v", w.Default, c.def)
			}
		})
	}
	// An enum parameter gives a total lookup in declaration order.
	ds, r := rigDiags(rigPrefix + rigBase + "\nmorph m { weight = if species == vael then 1 else 0 offset { parts = [ball] by = (0, 0.1, 0) } }")
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	w := r.Rig.Morphs[0].Weight
	want := []resolve.RigMember{{Name: "human", W: 0}, {Name: "vael", W: 1}, {Name: "brannoc", W: 0}}
	if w.Param != "species" || len(w.Members) != 3 {
		t.Fatalf("weight %+v", w)
	}
	for i := range want {
		if w.Members[i] != want[i] {
			t.Errorf("members %v", w.Members)
		}
	}
	// A constant has no parameter.
	_, r = rigDiags(rigPrefix + rigBase + "\nmorph m { weight = 0.25 offset { parts = [ball] by = (0, 0.1, 0) } }")
	if w := r.Rig.Morphs[0].Weight; w.Param != "" || w.Const != 0.25 || w.Default != 0.25 {
		t.Errorf("constant %+v", w)
	}
	// The exported name defaults to the declaration and `name` overrides it.
	_, r = rigDiags(rigPrefix + rigBase + "\nmorph m { name = \"neck-long\" weight = 1 offset { parts = [ball] by = (0, 0.1, 0) } }")
	if r.Rig.Morphs[0].Export != "neck-long" || r.Rig.Morphs[0].Name != "m" {
		t.Errorf("morph names %+v", r.Rig.Morphs[0])
	}
}

const mirrorRig = `
skeleton s {
  joint hips {
    at = (0, 1, 0)
    mirror {
      plane = x
      joint (arml, armr) {
        at = (0.3, 1, 0)
        orient = rot(0, 0, 10)
        swing_x = -20..100
        joint (handl, handr) {
          at = (0.6, 1, 0)
          socket (gl, gr) { kind = hand at = (0.65, 1, 0) }
        }
      }
    }
  }
}

part a {
  joint = hips
  mirror {
    plane = x
    sphere (pad_l, pad_r) { radius = 0.1 joint = arml at = (0.3, 1, 0) }
  }
}

skin {
  mirror {
    plane = x
    blend (b_l, b_r) { parent = arml child = handl centre = joint(handl) + (0, 0.02, 0) axis = (1, 0, 0) width = 0.1 fade_x = 0..0.2 only = [pad_l] }
    lengthen { joint = arml to = 0.35 rigid = [pad_l] }
  }
}

clip rest {
  duration = 1s
  keys = 3
  let wob = sin(180deg * t)
  pose {
    hips = rot(0, 0, 0)
    mirror {
      plane = x
      let w = side * wob
      arml = rot(10 * w, 5, 20)
      handl = rot(0, 0, 10)
    }
  }
}
`

func TestMirrorExpansion(t *testing.T) {
	ds, r := rigDiags(rigPrefix + mirrorRig)
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	sk := r.Rig.Skeleton
	var names []string
	for _, j := range sk.Joints {
		names = append(names, j.Name)
	}
	// Depth first, the original before its reflection.
	if got := strings.Join(names, " "); got != "hips arml handl armr handr" {
		t.Fatalf("joint order %q", got)
	}
	l, rr := sk.Joints[1], sk.Joints[3]
	if l.Reflect != "" || rr.Reflect != "x" || rr.Parent != "hips" || sk.Joints[4].Parent != "armr" {
		t.Errorf("joints %+v", sk.Joints)
	}
	// The copy's values are the original's expressions; the compiler reflects them.
	if l.At != rr.At || l.Orient != rr.Orient {
		t.Errorf("at/orient differ before reflection: %+v %+v", l, rr)
	}
	if len(sk.Sockets) != 2 || sk.Sockets[0].Name != "gl" || sk.Sockets[1].Name != "gr" || sk.Sockets[1].Joint != "handr" || sk.Sockets[1].Reflect != "x" {
		t.Errorf("sockets %+v", sk.Sockets)
	}
	// Skin steps: the copy's references take the pair; the centre reads the original joint.
	steps := r.Rig.Skin.Steps
	if len(steps) != 4 {
		t.Fatalf("%d steps", len(steps))
	}
	b0, b1 := steps[0].Blend, steps[2].Blend
	if b0 == nil || b1 == nil || steps[1].Lengthen == nil || steps[3].Lengthen == nil {
		t.Fatalf("steps %+v", steps)
	}
	if b0.Name != "b_l" || b0.Parent != "arml" || b0.Child != "handl" || b0.Only[0] != "pad_l" || b0.FadeSign != 1 {
		t.Errorf("original blend %+v", b0)
	}
	if b1.Name != "b_r" || b1.Parent != "armr" || b1.Child != "handr" || b1.Only[0] != "pad_r" || b1.FadeSign != -1 || b1.Reflect != "x" {
		t.Errorf("reflected blend %+v", b1)
	}
	if b1.Centre.Joint != "handl" || b1.Centre.Offset != [3]float64{0, 0.02, 0} {
		t.Errorf("the centre of the copy reads the original symbols: %+v", b1.Centre)
	}
	if steps[3].Lengthen.Joint != "armr" || steps[3].Lengthen.Rigid[0] != "pad_r" {
		t.Errorf("reflected lengthen %+v", steps[3].Lengthen)
	}
	// Clip samples: side is 1 on the original and -1 on the copy.
	c := r.Rig.Clips[0]
	if len(c.Samples) != 3 {
		t.Fatalf("samples %d", len(c.Samples))
	}
	mid := c.Samples[1] // t = 0.5: wob = sin(90 deg) = 1
	var gotL, gotR *resolve.RigRot
	for i := range mid.Rots {
		switch mid.Rots[i].Joint {
		case "arml":
			gotL = &mid.Rots[i]
		case "armr":
			gotR = &mid.Rots[i]
		}
	}
	if gotL == nil || gotR == nil {
		t.Fatalf("rots %+v", mid.Rots)
	}
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	if math.Abs(gotL.Euler[0]-rad(10)) > 1e-12 || math.Abs(gotR.Euler[0]-rad(-10)) > 1e-12 || gotL.Reflect != "" || gotR.Reflect != "x" {
		t.Errorf("arml %+v armr %+v", gotL, gotR)
	}
	if mid.Time != 0.5 {
		t.Errorf("time %v", mid.Time)
	}
}

func TestJointOrderOverride(t *testing.T) {
	src := rigPrefix + strings.Replace(mirrorRig, "skeleton s {", "skeleton s {\n  joint_order = [handr, armr, handl, arml, hips]", 1)
	ds, r := rigDiags(src)
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	if got := strings.Join(r.Rig.Skeleton.Order, " "); got != "handr armr handl arml hips" {
		t.Errorf("order %q", got)
	}
}

func TestRigDoesNotChangeOtherAssets(t *testing.T) {
	_, r := rigDiags("greenware 1\n\nasset a { root = a }\npart a { sphere s { radius = 1m } }\n")
	if r.Rig != nil {
		t.Errorf("a file without rig declarations has a rig: %+v", r.Rig)
	}
	// An idle clip without a rig is an unknown clip.
	ds, _ := rigDiags("greenware 1\n\nasset a { root = a idle = rest }\npart a { sphere s { radius = 1m } }\n")
	if !has(ds, diag.CodeUnknownName, "rest") {
		t.Errorf("diagnostics %v", ds)
	}
}

func TestKeyedClipRows(t *testing.T) {
	src := rigPrefix + rigBase + `
clip x {
  name = "field-x"
  node = "slide"
  key (0s, (-64, 0, 0))
  key (128s, (64, 0, 0))
}
clip heading {
  node = "slide"
  sample h in -180deg..180deg by 5 -> key (seconds(radians(h + 180deg)), rot(0, h, 0))
}
`
	ds, r := rigDiags(strings.Replace(src, `wrappers = ["slide"]`, `wrappers = ["slide"]`, 1))
	// two clips drive the same wrapper: allowed, they are separate animations.
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	x := r.Rig.Clips[1]
	if x.Export != "field-x" || x.Duration != 128 || x.HasDuration || len(x.Rows) != 2 {
		t.Fatalf("clip %+v", x)
	}
	// -64 yd would be converted: here the asset's length is metres, so the value is as written.
	if x.Rows[0].Value != [3]float64{-64, 0, 0} || x.Rows[0].Rot {
		t.Errorf("row %+v", x.Rows[0])
	}
	h := r.Rig.Clips[2]
	if len(h.Rows) != 5 || !h.Rows[0].Rot {
		t.Fatalf("heading rows %+v", h.Rows)
	}
	// Times are radians(h + 180deg) seconds: 0 to 2 pi in four equal steps.
	for i, k := range h.Rows {
		if want := float64(i) * math.Pi / 2; math.Abs(k.Time-want) > 1e-12 {
			t.Errorf("row %d at %v s, want %v", i, k.Time, want)
		}
		if want := -math.Pi + float64(i)*math.Pi/2; math.Abs(k.Value[1]-want) > 1e-12 {
			t.Errorf("row %d rotation %v, want %v", i, k.Value[1], want)
		}
	}
	if math.Abs(h.Duration-2*math.Pi) > 1e-12 {
		t.Errorf("duration %v", h.Duration)
	}
}

func TestRigLengthUnitsAreConverted(t *testing.T) {
	src := strings.Replace(rigPrefix, "length = m", "length = yd", 1) + `
skeleton s { joint hip { at = (0, 1, 0) } }
part a { joint = hip sphere ball { radius = 0.1 } }
clip rest {
  duration = 1s
  pose { hip = rot(0, 0, 0) root = (0, 1, 0) }
}
`
	ds, r := rigDiags(src)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	if got := r.Rig.Skeleton.Joints[0].At[1]; math.Abs(got-0.9144) > 1e-12 {
		t.Errorf("1 yd is %v m", got)
	}
	if got := r.Rig.Clips[0].Samples[0].Root[1]; math.Abs(got-0.9144) > 1e-12 {
		t.Errorf("root key 1 yd is %v m", got)
	}
}

// `if` takes the branch its condition selects. It once returned the `then`
// value whenever both branches were numbers.
func TestIfSelectsItsBranch(t *testing.T) {
	src := "greenware 1\n\nasset a { root = a }\nlet yes = if 1 < 2 then 10 else 20\nlet no = if 1 > 2 then 10 else 20\nlet len = if 1 > 2 then 1m else 3 * 2m\npart a { }\n"
	ds, r := rigDiags(src)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	for name, want := range map[string]float64{"yes": 10, "no": 20, "len": 6} {
		if v, _ := r.Let(name); v.Num != want {
			t.Errorf("%s = %v, want %v", name, v.Num, want)
		}
	}
}
