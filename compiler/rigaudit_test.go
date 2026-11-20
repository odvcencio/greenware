package compiler

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/resolve"
)

func hasCode(ds []diag.Diagnostic, code string, frag string) *diag.Diagnostic {
	for i := range ds {
		if ds[i].Code == code && strings.Contains(ds[i].Message, frag) {
			return &ds[i]
		}
	}
	return nil
}

// The diagnostic fixtures that only a build reports.
func TestAuditFixtures(t *testing.T) {
	cases := []struct {
		code     string
		severity diag.Severity
		policy   Policy
	}{
		{diag.CodeRigGround, diag.Error, Policy{}},
		{diag.CodeRigTorn, diag.Error, Policy{}},
		{diag.CodeRuntimePart, diag.Warning, Policy{RuntimePoints: 3}},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			f := loadFile(t, "../testdata/diag/"+c.code+".gw")
			_, _, ds := BuildWith(f, "", Options{Policy: c.policy})
			d := hasCode(ds, c.code, "")
			if d == nil {
				t.Fatalf("the fixture does not report %s: %v", c.code, ds)
			}
			if d.Severity != c.severity {
				t.Errorf("severity %v", d.Severity)
			}
			for _, o := range ds {
				if o.Code != c.code && o.Severity == diag.Error {
					t.Errorf("the fixture also reports %s", o)
				}
			}
		})
	}
}

// The lowest vertex of a standing pose is held to -5 mm..20 mm, inclusive, and
// an airborne clip is exempt.
func TestAuditGroundBand(t *testing.T) {
	src, err := os.ReadFile("../testdata/diag/GW-RIG-GROUND.gw")
	if err != nil {
		t.Fatal(err)
	}
	build := func(y string, airborne bool) []diag.Diagnostic {
		s := strings.Replace(string(src), "at = (0, 0.5, 0)", "at = (0, "+y+", 0)", 1)
		if airborne {
			s = strings.Replace(s, "duration = 1s", "duration = 1s airborne = on", 1)
		}
		_, _, ds := BuildWith(lowerSource(t, []byte(s)), "", Options{})
		return ds
	}
	for _, c := range []struct {
		y    string
		pass bool
	}{{"0.1199", true}, {"0.1201", false}, {"0.0951", true}, {"0.0949", false}, {"0.1", true}} {
		ds := build(c.y, false)
		if got := hasCode(ds, diag.CodeRigGround, "") == nil; got != c.pass {
			t.Errorf("ball at y = %s (lowest %.1f mm): passes %v, want %v: %v", c.y, 1000*(parse(c.y)-0.1), got, c.pass, ds)
		}
	}
	if ds := build("0.5", true); hasCode(ds, diag.CodeRigGround, "") != nil {
		t.Errorf("an airborne clip is held to the ground band: %v", ds)
	}
	// A tighter band in the source refuses what the default admits.
	s := strings.Replace(string(src), "ground = -5mm..20mm", "ground = -1mm..1mm", 1)
	s = strings.Replace(s, "at = (0, 0.5, 0)", "at = (0, 0.1199, 0)", 1)
	if _, _, ds := BuildWith(lowerSource(t, []byte(s)), "", Options{}); hasCode(ds, diag.CodeRigGround, "") == nil {
		t.Error("ground = -1mm..1mm does not refuse a vertex at 19.9 mm")
	}
}

func parse(s string) float64 {
	var f float64
	fmt.Sscan(s, &f)
	return f
}

// The runtime points of the athlete: the default, the 2^4 x 3 x 2 product and
// the named case, each once, with the case's label kept.
func TestRuntimePointsOfTheAthlete(t *testing.T) {
	f := loadProject(t, athletePath)
	r, ds := resolve.EvaluateOutput(f, "match")
	if r == nil {
		t.Fatal(ds)
	}
	e := &elab{r: r, mounts: mountModes(r)}
	pl := e.planPoints(0)
	if pl.partial || pl.full != 96 || len(pl.points) != 98 {
		t.Fatalf("full %d, %d points, partial %v; want the 96-point product, the default and the case", pl.full, len(pl.points), pl.partial)
	}
	if pl.points[0].label != "default" || pl.points[len(pl.points)-1].label != "case brannoc_lineman_aero" {
		t.Errorf("first %q last %q", pl.points[0].label, pl.points[len(pl.points)-1].label)
	}
	seen := map[string]bool{}
	for _, p := range pl.points {
		if seen[p.key()] {
			t.Errorf("point %q repeats", p.label)
		}
		seen[p.key()] = true
	}
	// The case sets species, build and belly and leaves height and pads at
	// their defaults, which are not corners.
	last := pl.points[len(pl.points)-1]
	if last.vals["species"].Str != "brannoc" || last.vals["build"].Num != 1 || last.vals["belly"].Num != 1 ||
		last.vals["height"].Num != 1 || last.vals["pads"].Num != 0.5 || last.gear["headgear"] != "aero" {
		t.Errorf("case values %v gear %v", last.vals, last.gear)
	}
	// Every corner and every member appears.
	have := map[string]bool{}
	for _, p := range pl.points {
		for _, k := range []string{"build", "pads", "belly", "height"} {
			have[fmt.Sprintf("%s=%v", k, p.vals[k].Num)] = true
		}
		have["species="+p.vals["species"].Str] = true
		have["gear="+p.gear["headgear"]] = true
	}
	for _, w := range []string{"build=0", "build=1", "pads=0", "pads=1", "belly=0", "belly=1", "height=0.94", "height=1.08",
		"species=human", "species=vael", "species=brannoc", "gear=classic", "gear=aero"} {
		if !have[w] {
			t.Errorf("no point has %s", w)
		}
	}
	// A bake mount has no gear axis: the helmet is fixed by the mount.
	rb, _ := resolve.EvaluateOutput(f, "home")
	eb := &elab{r: rb, mounts: mountModes(rb)}
	if n := len(eb.planPoints(0).points); n != 50 {
		t.Errorf("home has %d points, want 48 + default + case", n)
	}
}

// Above the limit the plan keeps the default, every case and one axis at a
// time, says what it left out, and never drops a named case.
func TestRuntimePointsPartial(t *testing.T) {
	f := loadProject(t, athletePath)
	r, _ := resolve.EvaluateOutput(f, "match")
	e := &elab{r: r, mounts: mountModes(r)}
	pl := e.planPoints(50)
	if !pl.partial {
		t.Fatal("96 points under a limit of 50 must be partial")
	}
	// default + case + one axis at a time: 4x2 + 3 + 2 values, less repeats.
	if len(pl.points) < 12 || len(pl.points) > 16 {
		t.Errorf("%d points", len(pl.points))
	}
	hasCase := false
	for _, p := range pl.points {
		if strings.HasPrefix(p.label, "case ") {
			hasCase = true
		}
	}
	if !hasCase || pl.points[0].label != "default" {
		t.Error("the default and the named case are mandatory")
	}
	if pl.omitted == 0 || pl.omitted > 96 {
		t.Errorf("omitted %d combinations", pl.omitted)
	}
	msg := pl.partialMessage()
	for _, w := range []string{"96 points", "limit of 50", "build{", "species{human,vael,brannoc}", "gear headgear{classic,aero}"} {
		if !strings.Contains(msg, w) {
			t.Errorf("message lacks %q: %s", w, msg)
		}
	}
	// The build reports it as a warning and still builds.
	p, _, ds := BuildWith(f, "match", Options{Policy: Policy{RuntimePoints: 50}})
	if p == nil {
		t.Fatalf("a partial plan builds: %v", ds)
	}
	if d := hasCode(ds, diag.CodeRuntimePart, "limit of 50"); d == nil || d.Severity != diag.Warning {
		t.Errorf("no GW-RUNTIME-PARTIAL warning: %v", ds)
	}
	a := p.Outputs[0].Files[0].Audit
	if !a.Partial || a.Omitted == 0 || a.Points != len(pl.points) {
		t.Errorf("audit %+v", a)
	}
}

// A named case equal to a product point keeps its label beside the product's.
func TestRuntimePointsKeepCaseLabels(t *testing.T) {
	src, _ := os.ReadFile(athletePath)
	s := strings.Replace(string(src), "set { species = brannoc build = 1 belly = 1 }", "set { species = brannoc build = 1 belly = 1 pads = 1 height = 1.08 }", 1)
	f := lowerProjectSource(t, athletePath, []byte(s))
	r, _ := resolve.EvaluateOutput(f, "match")
	e := &elab{r: r, mounts: mountModes(r)}
	pl := e.planPoints(0)
	if len(pl.points) != 97 {
		t.Fatalf("%d points: the case equals a corner and must not add one", len(pl.points))
	}
	found := false
	for _, p := range pl.points {
		if strings.Contains(p.label, "case brannoc_lineman_aero") && strings.Contains(p.label, "species=brannoc") {
			found = true
		}
	}
	if !found {
		t.Error("the case label was dropped")
	}
}
