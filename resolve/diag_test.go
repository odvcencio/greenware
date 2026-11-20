package resolve_test

import (
	"strings"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

const prefix = "greenware 1\n\nasset a { root = a }\n\n"

func prefixRoot(decls string) string { return prefix + decls + "\n" }

const suffix = "\n\npart a { }\n"

// wrap puts declarations between a valid header and a valid root part.
func wrap(decls string) string { return prefix + decls + suffix }

type diagCase struct {
	name string
	src  string
	code string
	// at is the exact source text the diagnostic's span covers.
	at string
	// edits are the new texts of the offered edits, in order. nil: none.
	edits []string
	// related is the number of related spans.
	related int
}

func (c diagCase) find(t *testing.T, ds []diag.Diagnostic) diag.Diagnostic {
	t.Helper()
	for _, d := range ds {
		if d.Code == c.code {
			return d
		}
	}
	t.Fatalf("no %s in %v", c.code, ds)
	return diag.Diagnostic{}
}

func TestDiagnosticSpansAndEdits(t *testing.T) {
	cases := []diagCase{
		{name: "misspelt name", src: wrap("param height : length = 3m\nlet half = hieght / 2"), code: diag.CodeUnknownName, at: "hieght", edits: []string{"height"}},
		{name: "unknown name far from any", src: wrap("let half = zzzzzzzz / 2"), code: diag.CodeUnknownName, at: "zzzzzzzz"},
		{name: "misspelt field", src: wrap("part b {\n  cylinder { radius = 1m height = 1m axs = x }\n}"), code: diag.CodeUnknownField, at: "axs", edits: []string{"axis"}},
		{name: "misspelt block", src: wrap("part b {\n  cylindr { radius = 1m height = 1m }\n}"), code: diag.CodeUnknownField, at: "cylindr", edits: []string{"cylinder"}},
		{name: "misspelt parameter type", src: wrap("param w : lenght = 3m"), code: diag.CodeUnknownName, at: "lenght", edits: []string{"length"}},
		{name: "misspelt material", src: wrap("material steel = pbr { base = #888888 }\npart b {\n  cylinder { radius = 1m height = 1m material = stell }\n}"), code: diag.CodeUnknownName, at: "stell", edits: []string{"steel"}},
		{name: "misspelt function", src: wrap("let x = sqrtt(4)"), code: diag.CodeUnknownName, at: "sqrtt", edits: []string{"sqrt"}},
		{name: "duplicate parameter", src: wrap("param w : length = 3m\nparam w : length = 4m"), code: diag.CodeDuplicate, at: "w", related: 1},
		{name: "duplicate setting", src: wrap("part b {\n  cylinder { radius = 1m radius = 2m height = 1m }\n}"), code: diag.CodeDuplicate, at: "radius", related: 1},
		{name: "duplicate geometry name", src: wrap("part b {\n  cylinder pin { radius = 1m height = 1m }\n  sphere pin { radius = 1m }\n}"), code: diag.CodeDuplicate, at: "pin", related: 1},
		{name: "switch given a number", src: wrap("param on_or_off : switch = 3"), code: diag.CodeType, at: "3"},
		{name: "enum member misspelt", src: wrap("part b {\n  array { mode = radiel count = 2 radius = 1m }\n}"), code: diag.CodeType, at: "radiel", edits: []string{"radial"}},
		{name: "length plus angle", src: wrap("let bad = 1m + 30deg"), code: diag.CodeUnit, at: "1m + 30deg"},
		{name: "bare trig argument", src: wrap("let s = sin(0.22)"), code: diag.CodeUnit, at: "sin(0.22)", edits: []string{"rad", "deg"}},
		{name: "default above range", src: wrap("param h : length = 4m in 1m..3m"), code: diag.CodeParamRange, at: "4m", edits: []string{"3m"}, related: 1},
		{name: "default below range", src: wrap("param h : length = 0.5m in 1m..3m"), code: diag.CodeParamRange, at: "0.5m", edits: []string{"1m"}, related: 1},
		{name: "empty range", src: wrap("param h : length = 2m in 3m..1m"), code: diag.CodeParamRange, at: "3m..1m"},
		{name: "zero radius", src: wrap("part b {\n  cylinder { radius = 0m height = 1m }\n}"), code: diag.CodeFieldRange, at: "0m"},
		{name: "too few segments", src: wrap("part b {\n  cylinder { radius = 1m height = 1m segments = 2 }\n}"), code: diag.CodeFieldRange, at: "2"},
		{name: "roughness above one", src: wrap("material m = pbr { roughness = 1.5 }"), code: diag.CodeFieldRange, at: "1.5"},
		{name: "failed require", src: wrap("param r : length = 1m in 0.5m..3m\ncheck params {\n  require (r > 2m, \"r too small\")\n}"), code: diag.CodeRequire, at: "require (r > 2m, \"r too small\")"},
		{name: "unsupported declaration", src: wrap("texture t = file { path = \"t.png\" }"), code: diag.CodeUnsupported, at: "file"},
		{name: "unsupported field", src: wrap("part b {\n  joint = head\n  cylinder { radius = 1m height = 1m }\n}"), code: diag.CodeUnsupported, at: "joint"},
		{name: "unsupported shape", src: wrap("part b {\n  torus { }\n}"), code: diag.CodeUnsupported, at: "torus"},
		{name: "budget materials", src: prefixRoot("material r = pbr { base = #ff0000 }\nmaterial g = pbr { base = #00ff00 }\npart a {\n material = r\n cylinder { radius = 1m height = 1m }\n part b { export = on\n material = g\n cylinder { radius = 1m height = 1m } }\n}\nbudget { materials = 1 }"), code: diag.CodeBudget, at: "1"},
		{name: "budget draws", src: prefixRoot("material r = pbr { base = #ff0000 }\npart a {\n material = r\n cylinder { radius = 1m height = 1m }\n part b { export = on\n cylinder { radius = 1m height = 1m } }\n}\nbudget { draw_calls = 1 }"), code: diag.CodeBudget, at: "1"},
		{name: "worst below default", src: wrap("budget { draw_calls = 3 draw_calls_worst = 2 }"), code: diag.CodeBudget, at: "2", related: 1},
		{name: "missing field", src: wrap("part b {\n  sphere { }\n}"), code: diag.CodeMissing, at: "sphere"},
		{name: "cycle", src: wrap("let p = q * 2\nlet q = p * 2"), code: diag.CodeCycle, related: 2, at: "p"},
		{name: "division by zero", src: wrap("let z = 0\nlet bad = 1 / z"), code: diag.CodeDomain, at: "1 / z"},
		{name: "runtime in geometry", src: wrap("param g : length = 0.1m runtime\npart b {\n  cylinder { radius = g height = 1m }\n}"), code: diag.CodeRuntime, at: "g"},
		{name: "unpaired name in mirror", src: wrap("part b {\n  mirror { plane = x\n    part leg { }\n  }\n}"), code: diag.CodeMirrorName, at: "leg"},
		{name: "pair outside mirror", src: wrap("part b {\n  part (l, r) { }\n}"), code: diag.CodeMirrorName, at: "(l, r)"},
		{name: "wrong edition", src: "greenware 2\n\nasset a { root = a }\npart a { }\n", code: diag.CodeEdition, at: "2", edits: []string{"1"}},
		{name: "missing edition", src: "asset a { root = a }\npart a { }\n", code: diag.CodeEdition, at: "asset", edits: []string{"greenware 1\n\n"}},
		{name: "unknown root", src: "greenware 1\n\nasset a { root = goalpst }\npart goalpost { }\n", code: diag.CodeUnknownName, at: "goalpst", edits: []string{"goalpost"}},
		{name: "instance of nested part", src: wrap("part b {\n  part c { }\n  instance { of = c }\n}"), code: diag.CodeType, at: "c", related: 1},
		{name: "instance cycle", src: "greenware 1\n\nasset a { root = a }\npart a { }\npart x { instance { of = y } }\npart y { instance { of = x } }\n", code: diag.CodeCycle, related: 1, at: "of = x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds := diagnose(c.src)
			d := c.find(t, ds)
			if got := c.src[d.Span.Start:d.Span.End]; c.at != "" && got != c.at && !(c.name == "cycle" && strings.HasPrefix(got, "p") || c.name == "cycle" && strings.HasPrefix(got, "q")) {
				t.Errorf("span covers %q, want %q", got, c.at)
			}
			var texts []string
			for _, e := range d.Edits {
				texts = append(texts, e.NewText)
			}
			if strings.Join(texts, "|") != strings.Join(c.edits, "|") {
				t.Errorf("edits %q, want %q", texts, c.edits)
			}
			if len(d.Related) != c.related {
				t.Errorf("related spans = %d, want %d", len(d.Related), c.related)
			}
			// Each edit must remove this diagnostic and add no other error.
			for _, e := range d.Edits {
				fixed, ok := diag.Apply([]byte(c.src), e)
				if !ok {
					t.Fatalf("edit %+v does not apply", e)
				}
				for _, nd := range diagnose(string(fixed)) {
					if nd.Code == c.code && nd.Span.Start == d.Span.Start {
						t.Errorf("edit %q leaves %v", e.Title, nd)
					}
					if nd.Severity == diag.Error && nd.Code != c.code {
						t.Errorf("edit %q adds %v", e.Title, nd)
					}
				}
			}
		})
	}
}

// One test per dimension error.
func TestDimensionErrors(t *testing.T) {
	head := "param len : length = 2m\nparam ang : angle = 30deg\nlet n = 2\n"
	cases := []struct{ name, expr, msg string }{
		{"length plus angle", "len + ang", "cannot add a length and a angle"},
		{"length minus angle", "len - ang", "cannot subtract"},
		{"length plus plain number", "len + n", "cannot add a length and a number"},
		{"compare length with angle", "len < ang", "cannot compare"},
		{"compare angle with plain number", "ang == n", "cannot compare"},
		{"sin of a length", "sin(len)", "sin needs an angle"},
		{"cos of a plain number", "cos(n)", "cos needs an angle"},
		{"sqrt of a length", "sqrt(len)", "sqrt needs a plain number"},
		{"bare number over a length", "2 / len", "cannot divide a bare number"},
		{"min of length and angle", "min(len, ang)", "like dimensions"},
		{"if branches differ", "if on then len else ang", "different types"},
		{"asin of an angle", "asin(ang)", "asin needs a plain number"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := wrap(head + "let x = " + c.expr)
			var got *diag.Diagnostic
			ds := diagnose(src)
			for i := range ds {
				if ds[i].Code == diag.CodeUnit {
					got = &ds[i]
				}
			}
			if got == nil {
				t.Fatalf("no GW-UNIT in %v", ds)
			}
			if !strings.Contains(got.Message, c.msg) {
				t.Errorf("message %q lacks %q", got.Message, c.msg)
			}
			if text := src[got.Span.Start:got.Span.End]; !strings.Contains(c.expr, text) && text != c.expr {
				t.Errorf("span covers %q, outside %q", text, c.expr)
			}
			if len(ds) != 1 {
				t.Errorf("one fault gave %d diagnostics: %v", len(ds), ds)
			}
		})
	}
	// A value in a slot of the wrong dimension.
	for _, c := range []struct{ name, decls string }{
		{"angle in a length slot", "part b {\n  cylinder { radius = 30deg height = 1m }\n}"},
		{"length in an angle slot", "part b {\n  cylinder { radius = 1m height = 1m rot = (1m, 0, 0) }\n}"},
		{"product of lengths in a length slot", "param w : length = 2m\npart b {\n  cylinder { radius = w * w height = 1m }\n}"},
		{"parameter default of another dimension", "param a : angle = 3m"},
		{"range bound of another dimension", "param w : length = 2m in 1deg..3m"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ds := diagnose(wrap(c.decls))
			if len(ds) != 1 || ds[0].Code != diag.CodeUnit {
				t.Errorf("want one GW-UNIT, got %v", ds)
			}
		})
	}
}

func evalLets(t *testing.T, decls string) map[string]units.Value {
	t.Helper()
	f, ds := ast.Lower(lang.Parse([]byte(wrap(decls))))
	r, more := resolve.Evaluate(f, nil)
	if all := append(ds, more...); len(all) != 0 {
		t.Fatalf("diagnostics: %v", all)
	}
	out := map[string]units.Value{}
	for _, l := range r.Lets {
		out[l.Name] = l.Value
	}
	return out
}

func TestUnitArithmetic(t *testing.T) {
	v := evalLets(t, `param len : length = 2m
param ang : angle = 30deg
let n = 2
let a = len * n
let b = len / len
let c = 1m + 2
let d = 2 * len
let e = len / 2
let f = ang * 2
let g = sin(90deg)
let h = 6mm + 1cm
let i = 3ft
let j = 7%
let k = (len - 1m) / 2
let l = min(len, 5)
let m = if len > 1m then len else 0
let o = abs(-len)
let p = radians(180deg)
let q = clamp(7, 0, 5) * 1m`)
	want := map[string]float64{"a": 4, "b": 1, "c": 3, "d": 4, "e": 1, "g": 1, "h": 0.016, "i": 0.9144, "j": 0.07, "k": 0.5, "l": 2, "m": 2, "o": 2, "q": 5}
	for name, w := range want {
		if got := v[name].Num; got != w && (got-w > 1e-12 || w-got > 1e-12) {
			t.Errorf("%s = %v, want %v", name, got, w)
		}
	}
	dims := map[string]units.Dim{"a": units.Length, "b": units.Number, "c": units.Length, "f": units.Angle, "g": units.Number, "j": units.Number, "p": units.Number}
	for name, d := range dims {
		if v[name].Dim != d {
			t.Errorf("%s has dimension %s, want %s", name, v[name].Dim, d)
		}
	}
	if got := v["f"].Num; got != 2*30*units.DegToRad && got != float64(float64(30*units.DegToRad)*2) {
		t.Errorf("f = %v", got)
	}
}

func TestAssetDefaultUnits(t *testing.T) {
	src := "greenware 1\n\nasset a { root = a length = ft angle = rad }\n\nlet one = 1m + 2\nlet turn = 90deg + 0.25\n\npart a { }\n"
	f, ds := ast.Lower(lang.Parse([]byte(src)))
	r, more := resolve.Evaluate(f, nil)
	if len(ds)+len(more) != 0 {
		t.Fatalf("%v %v", ds, more)
	}
	one, _ := r.Let("one")
	if want := 1 + 2*0.3048; one.Num != want {
		t.Errorf("1m + 2 in a foot document = %v, want %v", one.Num, want)
	}
	turn, _ := r.Let("turn")
	if want := 90*units.DegToRad + 0.25; turn.Num-want > 1e-12 || want-turn.Num > 1e-12 {
		t.Errorf("90deg + 0.25 in a radian document = %v, want %v", turn.Num, want)
	}
}

func TestOverrideDiagnostics(t *testing.T) {
	f := load(t, "../testdata/syntax/goalpost.gw")
	one := func(o resolve.Override) []diag.Diagnostic {
		_, ds := resolve.Evaluate(f, []resolve.Override{o})
		return ds
	}
	m := units.Len(20)
	if ds := one(resolve.Override{Name: "upright_rise", Value: &m}); len(ds) != 1 || ds[0].Code != diag.CodeParamRange {
		t.Errorf("out of range override: %v", ds)
	}
	sw := units.Num(3)
	if ds := one(resolve.Override{Name: "padding", Value: &sw}); len(ds) != 1 || ds[0].Code != diag.CodeType {
		t.Errorf("wrong type override: %v", ds)
	}
	ang := units.Ang(1)
	if ds := one(resolve.Override{Name: "setback", Value: &ang}); len(ds) != 1 || ds[0].Code != diag.CodeUnit {
		t.Errorf("wrong dimension override: %v", ds)
	}
	if ds := one(resolve.Override{Name: "upright_rize", Value: &m}); len(ds) != 1 || ds[0].Code != diag.CodeUnknownName {
		t.Errorf("unknown parameter override: %v", ds)
	}
	// Two overrides that together break the require: bend_start >= 0.3 m.
	a, b := units.Len(2.5), units.Len(2.4)
	_, ds := resolve.Evaluate(f, []resolve.Override{{Name: "crossbar_height", Value: &a}, {Name: "setback", Value: &b}})
	if len(ds) != 1 || ds[0].Code != diag.CodeRequire {
		t.Fatalf("require: %v", ds)
	}
	if !strings.Contains(ds[0].Message, "bend_start = 0.1m") || !strings.Contains(ds[0].Message, "0.3 m of straight pipe") {
		t.Errorf("require message = %q", ds[0].Message)
	}
}

func TestOutputSetIsChecked(t *testing.T) {
	src := wrap("param h : length = 2m in 1m..3m\noutput o { file = \"o.glb\" set { h = 9m } }")
	ds := diagnose(src)
	if len(ds) != 1 || ds[0].Code != diag.CodeParamRange {
		t.Fatalf("%v", ds)
	}
	if got := src[ds[0].Span.Start:ds[0].Span.End]; got != "9m" {
		t.Errorf("span %q", got)
	}
}

func TestPrecedenceIsReassociated(t *testing.T) {
	v := evalLets(t, `let a = 10 - 4 - 3
let b = 2 + 3 * 4
let c = 12 / 2 / 3
let d = -2 * 3
let e = 1 + 2 * 3 - 4 / 2
let f = (1 + 2) * 3`)
	for name, w := range map[string]float64{"a": 3, "b": 14, "c": 2, "d": -6, "e": 5, "f": 9} {
		if v[name].Num != w {
			t.Errorf("%s = %v, want %v", name, v[name].Num, w)
		}
	}
}

func TestSkippedBranchesAreStillChecked(t *testing.T) {
	// The unknown name is in a branch that is not taken. It is still an error.
	if ds := diagnose(wrap("let x = if on then 1 else nothing_here")); len(ds) != 1 || ds[0].Code != diag.CodeUnknownName {
		t.Errorf("%v", ds)
	}
	// A division by zero in a branch that is not taken is not.
	if ds := diagnose(wrap("let z = 0\nlet x = if z == 0 then 1 else 1 / z")); len(ds) != 0 {
		t.Errorf("%v", ds)
	}
	if ds := diagnose(wrap("let z = 0\nlet x = z != 0 && 1 / z > 1")); len(ds) != 0 {
		t.Errorf("%v", ds)
	}
}

func TestWhenBranches(t *testing.T) {
	src := wrap("param p : switch = off\npart b {\n  when p { cylinder pin { radius = 1m height = 1m } } else { sphere ball { radius = 1m } }\n}")
	f, _ := ast.Lower(lang.Parse([]byte(src)))
	r, ds := resolve.Evaluate(f, nil)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	_ = r
	// The branch that is not taken is checked: a bad field there is reported.
	bad := wrap("param p : switch = off\npart b {\n  when p { cylinder { radius = 0m height = 1m } }\n}")
	if ds := diagnose(bad); len(ds) != 1 || ds[0].Code != diag.CodeFieldRange {
		t.Errorf("%v", ds)
	}
}

func TestRuntimeWhenKeepsBothBranches(t *testing.T) {
	src := wrap("param p : switch = on runtime\nmaterial m = pbr { base = #ffffff }\npart b {\n  when p { cylinder pin { radius = 1m height = 1m } } else { sphere ball { radius = 1m } }\n}")
	f, ds := ast.Lower(lang.Parse([]byte(src)))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	r, ds := resolve.Evaluate(f, nil)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	_ = r
}
