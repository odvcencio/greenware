package resolve_test

import (
	"testing"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

func codes(ds []diag.Diagnostic) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Code)
	}
	return out
}

func hasCode(ds []diag.Diagnostic, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

// A let that is first reached from a branch that is not taken still reports
// its own division by zero.
func TestLetFirstReachedFromUntakenBranchReportsItsErrors(t *testing.T) {
	ds := diagnose(wrap("param off : switch = off\nlet a = if off then b else 1m\nlet b = 1m / 0"))
	if !hasCode(ds, diag.CodeDomain) {
		t.Fatalf("division by zero was lost: %v", codes(ds))
	}
}

// Both branches of `if` are unified (spec 2.3 rule 1): a pending number next
// to a length is a length whichever branch runs.
func TestIfUnifiesBranches(t *testing.T) {
	for _, wide := range []string{"on", "off"} {
		v := evalLets(t, "param wide : switch = "+wide+"\nlet w = if wide then 2 else 1m")
		if v["w"].Dim != units.Length || v["w"].Pending {
			t.Errorf("wide=%s: w = %v (%s), want a length", wide, v["w"].Num, v["w"].Dim)
		}
	}
	// Two pending branches stay pending, so a later use can still pick the unit.
	v := evalLets(t, "param wide : switch = on\nlet n = if wide then 2 else 3\nlet a = n * 1m")
	if v["a"].Dim != units.Length || v["a"].Num != 2 {
		t.Errorf("a = %+v", v["a"])
	}
}

// A branch that is a tuple of another length is a different type whichever
// branch runs.
func TestIfRejectsTuplesOfDifferentShape(t *testing.T) {
	for _, c := range []string{"on", "off"} {
		src := wrap("param c : switch = " + c + "\nlet t = if c then (1m, 2m) else (1m, 2m, 3m)")
		if ds := diagnose(src); !hasCode(ds, diag.CodeUnit) {
			t.Errorf("c=%s: %v", c, codes(ds))
		}
		src = wrap("param c : switch = " + c + "\nlet t = if c then (1m, 2m) else (1deg, 2deg)")
		if ds := diagnose(src); !hasCode(ds, diag.CodeUnit) {
			t.Errorf("c=%s angle tuple: %v", c, codes(ds))
		}
	}
}

// The unknown-name diagnostic covers the first part of a dotted name only, so
// its edit keeps the `.x`.
func TestQualifiedNameSpanIsTheFirstPart(t *testing.T) {
	src := wrap("param height : length = 3m\nlet q = hieght.x")
	ds := diagnose(src)
	for _, d := range ds {
		if d.Code != diag.CodeUnknownName {
			continue
		}
		if got := src[d.Span.Start:d.Span.End]; got != "hieght" {
			t.Errorf("span covers %q, want hieght", got)
		}
		return
	}
	t.Fatalf("no unknown-name diagnostic: %v", codes(ds))
}

// A member error points at the member, not at the whole chain.
func TestQualifiedMemberErrorSpan(t *testing.T) {
	src := wrap("param p : length = 3m\nlet q = p.x")
	ds := diagnose(src)
	for _, d := range ds {
		if d.Code == diag.CodeType {
			if got := src[d.Span.Start:d.Span.End]; got != "x" {
				t.Errorf("span covers %q, want x", got)
			}
			return
		}
	}
	t.Fatalf("no type diagnostic: %v", codes(ds))
}

// Indexing a list with a runtime index depends on the runtime parameter.
func TestIndexKeepsRuntime(t *testing.T) {
	src := wrap("param i : integer = 0 runtime\nlet l = [1m, 2m]\npart b {\n  cylinder { radius = l[i] height = 1m }\n}")
	if ds := diagnose(src); !hasCode(ds, diag.CodeRuntime) {
		t.Errorf("runtime index accepted: %v", codes(ds))
	}
	src = wrap("param r : length = 1m runtime\nlet t = (r, 2m, 3m)\npart b {\n  cylinder { radius = t[0] height = 1m }\n}")
	if ds := diagnose(src); !hasCode(ds, diag.CodeRuntime) {
		t.Errorf("runtime tuple element accepted: %v", codes(ds))
	}
}

// A sampled row's range and count are bake-time (spec 2.7).
func TestSampleRejectsRuntimeBounds(t *testing.T) {
	body := func(rng, n string) string {
		return wrap("param top : length = 1m runtime\nparam n : integer = 4 runtime\npart b {\n  lathe {\n    point (0.1, 0)\n    sample h in " + rng + " by " + n + " -> point (0.1, h)\n  }\n}")
	}
	for name, src := range map[string]string{
		"upper bound": body("0m..top", "4"),
		"count":       body("0m..1m", "n"),
	} {
		if ds := diagnose(src); !hasCode(ds, diag.CodeRuntime) {
			t.Errorf("%s: %v", name, codes(ds))
		}
	}
}

// An angle range is a bake-time field too.
func TestAngleRangeRejectsRuntime(t *testing.T) {
	src := wrap("param sweep : angle = 90deg runtime\npart b {\n  lathe {\n    arc = 0deg..sweep\n    point (0.1, 0)\n    point (0.1, 1)\n  }\n}")
	if ds := diagnose(src); !hasCode(ds, diag.CodeRuntime) {
		t.Errorf("runtime arc accepted: %v", codes(ds))
	}
}

// After an out-of-range override is reported, the parameter keeps its default
// (spec 2.7: rejection keeps the last valid state).
func TestOutOfRangeOverrideKeepsDefault(t *testing.T) {
	f := load(t, "../testdata/syntax/goalpost.gw")
	def, _ := resolve.Evaluate(f, nil)
	want, _ := def.Param("upright_rise")
	bad := units.Len(20)
	r, ds := resolve.Evaluate(f, []resolve.Override{{Name: "upright_rise", Value: &bad}})
	if len(ds) != 1 || ds[0].Code != diag.CodeParamRange {
		t.Fatalf("%v", ds)
	}
	got, _ := r.Param("upright_rise")
	if !units.Equal(got, want) {
		t.Errorf("upright_rise = %v, want the default %v", got.Format(), want.Format())
	}
}
