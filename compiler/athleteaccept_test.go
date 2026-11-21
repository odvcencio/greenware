package compiler

import (
	"os"
	"strings"
	"testing"

	"m31labs.dev/greenware/accept"
	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/lang"
)

const athleteAcceptPath = "../examples/athlete/athlete.accept.gw"

func runAthleteAccept(t testing.TB) *accept.Report {
	t.Helper()
	p := athleteAll(t)
	src, err := os.ReadFile(athleteAcceptPath)
	if err != nil {
		t.Fatal(err)
	}
	f, ds := ast.Lower(lang.Parse(src))
	if len(ds) > 0 || len(f.Accepts) != 1 {
		t.Fatalf("the acceptance file does not lower: %v", ds)
	}
	in := accept.Inputs{}
	for _, o := range p.Outputs {
		for _, fl := range o.Files {
			d, err := gltf.Decode(fl.GLB)
			if err != nil {
				t.Fatal(err)
			}
			in[accept.Key{Output: o.Name, LOD: fl.Level}] = accept.Product{Doc: d, File: fl.Name}
		}
	}
	rep, _ := accept.Run(f.Accepts[0], in)
	return rep
}

// The athlete's acceptance runs on the six delivered files. What passes, what
// fails and what cannot be measured is the state of milestone M4, written down.
func TestAthleteAcceptance(t *testing.T) {
	rep := runAthleteAccept(t)
	verdict := map[string]map[string]string{} // message -> "output/lod" -> verdict
	for _, l := range rep.Levels {
		for _, e := range l.Expects {
			if verdict[e.Message] == nil {
				verdict[e.Message] = map[string]string{}
			}
			verdict[e.Message][l.Output+"/"+string(rune('0'+l.LOD))] = string(e.Verdict)
		}
	}
	want := map[string]string{
		"15 joints": "pass", "seven morph targets": "pass", "the four lookup channels and two of the body clips": "pass",
		"31 animations": "pass", "the 17 other body clips of the historical file": "pass", "the 8 footwork clips of the game's bake": "pass",
		"48 extreme runtime points": "pass", "pelvis at 0.969486 yd": "pass",
		"the lowest sole vertex is at most 5 mm below the ground": "pass", "the lowest sole vertex is at most 20 mm above the ground": "pass",
		"the head_gear socket stays on the centre line": "pass", "head_gear is above the pelvis": "pass",
		"default ready, key 0, stands on the ground": "pass", "default ready, last key, stands on the ground": "pass",
	}
	for msg, w := range want {
		found := false
		for m, vs := range verdict {
			if !strings.HasPrefix(m, msg) {
				continue
			}
			found = true
			for k, got := range vs {
				if got != w {
					t.Errorf("%q at %s: %s, want %s", msg, k, got, w)
				}
			}
		}
		if !found {
			t.Errorf("no expect %q", msg)
		}
	}
	for m, vs := range verdict {
		switch {
		case strings.HasPrefix(m, "the mounted helmet keeps 2 mm"):
			// Baked outputs measure it and fail on the geometry; match cannot.
			for k, got := range vs {
				w := "fail"
				if strings.HasPrefix(k, "match") {
					w = "unavailable"
				}
				if got != w {
					t.Errorf("clearance at %s: %s, want %s", k, got, w)
				}
			}
		case strings.HasPrefix(m, "the match file draws"), strings.HasPrefix(m, "a baked file draws"):
			for k, got := range vs {
				if got != "pass" {
					t.Errorf("%q at %s: %s", m, k, got)
				}
			}
		}
	}
}

// The acceptance poses the delivered file by its own rules and agrees with
// the compiler's audit at the default point: the two evaluators share no code.
func TestAcceptancePoseAgreesWithTheAudit(t *testing.T) {
	p := athleteAll(t)
	f := p.Outputs[0].Files[0]
	rep := runAthleteAccept(t)
	var lowest float64
	for _, l := range rep.Levels {
		if l.Output != "match" || l.LOD != 0 {
			continue
		}
		for _, e := range l.Expects {
			if e.Message == "default ready, key 0, stands on the ground" {
				for _, m := range e.Measures {
					if strings.HasPrefix(m.Name, "posed_lowest(clip = ready, time = 0s)") && m.Number != "" {
						x, _ := m.Number.Float64()
						lowest = x
					}
				}
			}
		}
	}
	a := f.Audit
	// The audit's smallest lowest-vertex height over every point is at most the
	// default point's; the default point lies inside the audit's range.
	if lowest < a.GroundLowest.Value-1e-6 || lowest > a.GroundHighest.Value+1e-6 {
		t.Errorf("default ready key 0 lowest vertex %.6f m, outside the audit's range %.6f..%.6f", lowest, a.GroundLowest.Value, a.GroundHighest.Value)
	}
}

// The rig functions refuse what they cannot measure and say why.
func TestAcceptRigFunctionsRefuse(t *testing.T) {
	p := athleteAll(t)
	d, err := gltf.Decode(p.Outputs[0].Files[0].GLB)
	if err != nil {
		t.Fatal(err)
	}
	run := func(expr string) accept.ExpectReport {
		t.Helper()
		f, ds := ast.Lower(lang.Parse([]byte("greenware 1\naccept a { outputs = [match] lods = [0]\n expect (" + expr + ", \"x\") }\n")))
		if len(ds) > 0 {
			t.Fatalf("%v", ds)
		}
		rep, _ := accept.Run(f.Accepts[0], accept.Inputs{accept.Key{Output: "match", LOD: 0}: accept.Product{Doc: d, File: "athlete.glb"}})
		return rep.Levels[0].Expects[0]
	}
	for _, c := range []struct {
		expr    string
		verdict accept.Verdict
		frag    string
	}{
		{"has_animation(ready)", accept.Pass, ""},
		{`has_animation("field-x") && has_animation(sprint) && !has_animation(gallop)`, accept.Pass, ""},
		{"posed_lowest(clip = ready, time = 0s, colour = red) > 0m", accept.Refused, "not a runtime parameter"},
		{"posed_lowest(clip = gallop) > 0m", accept.Refused, "no animation named"},
		{"posed_lowest(clip = ready, time = 0s, build = 1) < 25mm", accept.Pass, ""},
		{"posed_position(nobody, clip = ready) == (0m, 0m, 0m)", accept.Refused, "0 nodes"},
		{"mounted_clearance() > 0m", accept.Unavailable, "no baked gear"},
		// The athlete has no stature parameter: the game scales a player as a whole model.
		{"posed_position(pelvis, clip = ready, time = 0s, height = 1.08).y > 0m", accept.Refused, "not a runtime parameter"},
		// A heavier build leaves the feet where they are: the targets widen the body about its rings and never move a joint.
		{"abs(posed_lowest(clip = ready, time = 0s, build = 1) - posed_lowest(clip = ready, time = 0s, build = 0)) < 1mm", accept.Pass, ""},
	} {
		e := run(c.expr)
		if e.Verdict != c.verdict || !strings.Contains(e.Detail, c.frag) {
			t.Errorf("%s: %s %q, want %s %q", c.expr, e.Verdict, e.Detail, c.verdict, c.frag)
		}
	}
}
