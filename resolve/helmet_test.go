package resolve_test

import (
	"math"
	"strings"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

func evaluate(t *testing.T, src string) (*resolve.Resolved, []diag.Diagnostic) {
	t.Helper()
	f, ds := ast.Lower(lang.Parse([]byte(src)))
	if diag.HasErrors(ds) {
		t.Fatalf("lowering: %v", ds)
	}
	r, more := resolve.Evaluate(f, nil)
	return r, diag.Dedup(append(ds, more...))
}

func errorsOf(ds []diag.Diagnostic) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range ds {
		if d.Severity == diag.Error {
			out = append(out, d)
		}
	}
	return out
}

func hasMsg(ds []diag.Diagnostic, code, msg string) bool {
	for _, d := range ds {
		if d.Code == code && strings.Contains(d.Message, msg) {
			return true
		}
	}
	return false
}

const enumHead = "greenware 1\n\nasset a { root = a length = m }\n"

func TestEnumMembersAreNamesInComparisonsAndSet(t *testing.T) {
	src := enumHead + `
param style : enum(classic, aero) = classic
param mask : enum(open, cage) = cage runtime
part a {
  when style == classic {
    box { size = (1m, 1m, 1m) }
  } else {
    box { size = (2m, 2m, 2m) }
  }
  part m { when mask == cage { box { size = (1m, 1m, 1m) } } }
  part n { when open == mask { box { size = (1m, 1m, 1m) } } }
}
output o { file = "o.glb" set { style = aero } }
`
	f, ds := ast.Lower(lang.Parse([]byte(src)))
	if diag.HasErrors(ds) {
		t.Fatal(ds)
	}
	r, ds := resolve.Evaluate(f, nil)
	if e := errorsOf(ds); len(e) > 0 {
		t.Fatalf("defaults: %v", e)
	}
	// classic picks the first branch: a 1 m box.
	if got := r.Root.Children[0]; got.Kind != "box" {
		t.Fatalf("bake when left %v", got.Kind)
	}
	if v, _ := r.Root.Children[0].Get("size"); v.Elems[0].Num != 1 {
		t.Fatalf("style classic chose the wrong branch: %v", v.Format())
	}
	// The runtime tests keep both sides and record a structured condition.
	w := resolve.VisibilityWhen(r.Root.Children[1])
	if w == nil || w.Cond == nil || *w.Cond != (resolve.Cond{Param: "mask", Op: "eq", Value: "cage"}) || w.Test != "mask == cage" {
		t.Fatalf("condition %+v", w)
	}
	if w := resolve.VisibilityWhen(r.Root.Children[2]); w == nil || w.Cond.Value != "open" {
		t.Fatalf("a member on the left: %+v", w)
	}
	// The output's set block chooses the other branch.
	r2, ds := resolve.EvaluateOutput(f, "o")
	if e := errorsOf(ds); len(e) > 0 {
		t.Fatalf("output: %v", e)
	}
	if v, _ := r2.Root.Children[0].Get("size"); v.Elems[0].Num != 2 {
		t.Fatalf("style aero chose the wrong branch: %v", v.Format())
	}
	if v, ok := r.Param("style"); !ok || v.Kind != units.KLabel || v.Str != "classic" {
		t.Fatalf("param value %+v", v)
	}
	for _, p := range r.Params {
		if p.Name == "mask" && (len(p.Members) != 2 || p.Members[1] != "cage" || !p.Runtime) {
			t.Fatalf("members %+v", p)
		}
	}
}

func TestEnumMemberMistakes(t *testing.T) {
	cases := []struct{ name, body, msg string }{
		{"not a member", "when style == clasic { box { size = (1m,1m,1m) } }", "not a member of `style`"},
		{"member of nothing", "when other == aero { box { size = (1m,1m,1m) } }", "`other` is not a parameter"},
		{"member where a name is needed", "let x = classic", "`classic` is not a parameter"},
	}
	for _, c := range cases {
		src := enumHead + "param style : enum(classic, aero) = classic\npart a {\n" + c.body + "\n}\n"
		if c.name == "member where a name is needed" {
			src = enumHead + "param style : enum(classic, aero) = classic\n" + c.body + "\npart a { }\n"
		}
		_, ds := evaluate(t, src)
		if !hasMsg(ds, diag.CodeUnknownName, c.msg) && !hasMsg(ds, diag.CodeType, c.msg) {
			t.Errorf("%s: %v", c.name, ds)
		}
	}
	// A near miss carries the fix.
	_, ds := evaluate(t, enumHead+"param style : enum(classic, aero) = classic\npart a {\n when style == clasic { box { size = (1m,1m,1m) } }\n}\n")
	var fixed bool
	for _, d := range ds {
		for _, e := range d.Edits {
			fixed = fixed || e.NewText == "classic"
		}
	}
	if !fixed {
		t.Errorf("no edit to classic: %v", ds)
	}
	// A default that is not a member says so, once.
	_, ds = evaluate(t, enumHead+"param style : enum(classic, aero) = modern\npart a { }\n")
	if !hasMsg(ds, diag.CodeType, "`modern` is not a member of the enum") || len(errorsOf(ds)) != 1 {
		t.Errorf("bad default: %v", ds)
	}
}

func TestRuntimeWhenForms(t *testing.T) {
	head := enumHead + "param s : switch = on runtime\nparam e : enum(a, b, c) = a runtime\nparam t : switch = on\npart a {\n"
	ok := map[string]resolve.Cond{
		"s":      {Param: "s", Op: "is"},
		"!s":     {Param: "s", Op: "not"},
		"(s)":    {Param: "s", Op: "is"},
		"e == b": {Param: "e", Op: "eq", Value: "b"},
		"e != c": {Param: "e", Op: "ne", Value: "c"},
	}
	for cond, want := range ok {
		r, ds := evaluate(t, head+" part p { when "+cond+" { box { size = (1m,1m,1m) } } }\n}\n")
		if e := errorsOf(ds); len(e) > 0 {
			t.Errorf("%s: %v", cond, e)
			continue
		}
		w := resolve.VisibilityWhen(r.Root.Children[0])
		if w == nil || *w.Cond != want {
			t.Errorf("%s: %+v", cond, w)
		}
	}
	for _, cond := range []string{"s && e == a", "s || !s", "e == a || e == b"} {
		_, ds := evaluate(t, head+" part p { when "+cond+" { box { size = (1m,1m,1m) } } }\n}\n")
		if !hasMsg(ds, diag.CodeUnsupported, "runtime `when` can test") {
			t.Errorf("%s: %v", cond, ds)
		}
	}
	// A bake switch is not a runtime condition: the branch is chosen.
	r, ds := evaluate(t, head+" part p { when t { box { size = (1m,1m,1m) } } }\n}\n")
	if e := errorsOf(ds); len(e) > 0 || resolve.VisibilityWhen(r.Root.Children[0]) != nil || len(r.Root.Children[0].Children) != 1 {
		t.Errorf("bake when: %v", ds)
	}
}

func TestWorstLookCountsExclusiveBranchesOnce(t *testing.T) {
	src := enumHead + `
material m = pbr { }
material g = pbr { }
param mask : enum(open, two, cage) = cage runtime
param visor : switch = off runtime
part a {
  material = m
  box { size = (1m, 1m, 1m) }
  part two { when mask == two { box { size = (1m, 1m, 1m) } } }
  part cage { when mask == cage { box { size = (1m, 1m, 1m) } } }
  part v { material = g
    when visor { box { size = (1m, 1m, 1m) } } }
}
`
	r, ds := evaluate(t, src)
	if e := errorsOf(ds); len(e) > 0 {
		t.Fatal(e)
	}
	// Default: the root and the cage. Worst: the root, one mask, the visor.
	if r.Look.DrawCalls != 2 || r.Look.DrawCallsWorst != 3 {
		t.Fatalf("draws %d and %d, want 2 and 3", r.Look.DrawCalls, r.Look.DrawCallsWorst)
	}
	if len(r.Look.Materials) != 2 {
		t.Fatalf("materials %v", r.Look.Materials)
	}
}

func TestTemplateExpansion(t *testing.T) {
	src := enumHead + `
param n : integer = 4
template rail(y0: length, reach: length = 0.5m, count: integer = 3) {
  tube {
    radius = 0.01m
    sample a in 0deg..90deg by count -> point (reach * sin(a), y0, reach * cos(a))
  }
}
part a {
  rail (1m)
  rail (2m, 0.25m)
  rail (y0 = 3m, count = n)
  rail (3m, count = 2)
}
`
	r, ds := evaluate(t, src)
	if e := errorsOf(ds); len(e) > 0 {
		t.Fatal(e)
	}
	kids := r.Root.Children
	if len(kids) != 4 {
		t.Fatalf("%d children: a template call adds the tubes it holds, no node of its own", len(kids))
	}
	for i, k := range kids {
		if k.Kind != "tube" || k.Name != "" {
			t.Fatalf("child %d is %q %q", i, k.Kind, k.Name)
		}
	}
	rows := []int{3, 3, 4, 2}
	for i, want := range rows {
		if len(kids[i].Rows) != want {
			t.Errorf("call %d: %d rows, want %d", i, len(kids[i].Rows), want)
		}
	}
	// The default reach is 0.5 m: the last point of the first call is at x = 0.5.
	if x := kids[0].Rows[2].Values[0].Num; math.Abs(x-0.5) > 1e-12 {
		t.Errorf("default reach gave x = %v", x)
	}
	if y := kids[1].Rows[0].Values[1].Num; y != 2 {
		t.Errorf("y0 = %v", y)
	}
	if x := kids[1].Rows[2].Values[0].Num; math.Abs(x-0.25) > 1e-12 {
		t.Errorf("reach 0.25 m gave x = %v", x)
	}
}

func TestTemplateUnitsFollowTheAssetNotTheCaller(t *testing.T) {
	// A bare number in a length argument is in the asset's unit, once.
	src := "greenware 1\n\nasset a { root = a length = yd }\n" + `
template post(h: length) { tube { radius = 0.01 point (0, 0, 0) point (0, h, 0) } }
part a { post (2) }
`
	r, ds := evaluate(t, src)
	if e := errorsOf(ds); len(e) > 0 {
		t.Fatal(e)
	}
	if y := r.Root.Children[0].Rows[1].Values[1].Num; math.Abs(y-1.8288) > 1e-12 {
		t.Fatalf("2 yd became %v m", y)
	}
	if rad, _ := r.Root.Children[0].Get("radius"); math.Abs(rad.Num-0.009144) > 1e-12 {
		t.Fatalf("radius %v", rad.Num)
	}
}

func TestTemplateMistakes(t *testing.T) {
	head := enumHead + "template t(a: length, b: length = 1m) { box { size = (a, b, 1m) } }\npart a {\n"
	cases := []struct{ name, call, code, msg string }{
		{"too many", "t (1m, 2m, 3m)", diag.CodeFieldRange, "takes 2 arguments"},
		{"unknown name", "t (1m, c = 2m)", diag.CodeUnknownField, "no argument `c`"},
		{"given twice", "t (1m, a = 2m)", diag.CodeDuplicate, "given twice"},
		{"positional after named", "t (a = 1m, 2m)", diag.CodeType, "positional arguments come before named"},
		{"wrong unit", "t (30deg)", diag.CodeUnit, ""},
		{"unknown template", "tt (1m)", diag.CodeUnknownName, "no template is named `tt`"},
		{"qualified", "finish.gloss (1m)", diag.CodeUnsupported, "qualified template"},
		{"missing needs", "t (b = 1m)", diag.CodeMissing, "needs the argument `a`"},
	}
	for _, c := range cases {
		_, ds := evaluate(t, head+c.call+"\n}\n")
		if !hasMsg(ds, c.code, c.msg) {
			t.Errorf("%s: %v", c.name, ds)
		}
	}
	// A misspelt template name offers the real one.
	_, ds := evaluate(t, head+"tt (1m)\n}\n")
	var edit string
	for _, d := range ds {
		for _, e := range d.Edits {
			edit = e.NewText
		}
	}
	if edit != "t" {
		t.Errorf("edit %q", edit)
	}
	// Duplicate template names.
	_, ds = evaluate(t, enumHead+"template t(a: length) { }\ntemplate t(a: length) { }\npart a { }\n")
	if !hasMsg(ds, diag.CodeDuplicate, "declared twice") {
		t.Errorf("duplicate: %v", ds)
	}
}

func TestTemplateCycles(t *testing.T) {
	src := enumHead + `
template p(a: length) { q (a) }
template q(a: length) { p (a) }
template self(a: length) { self (a) }
part a { p (1m) self (1m) }
`
	_, ds := evaluate(t, src)
	if !hasMsg(ds, diag.CodeCycle, "p -> q -> p") || !hasMsg(ds, diag.CodeCycle, "self -> self") {
		t.Fatalf("%v", ds)
	}
}

func TestTemplateReadsDocumentNamesNotTheCallSite(t *testing.T) {
	src := enumHead + `
let step = 2m
template t(a: length) { box { size = (a, step, 1m) } }
part a {
  let step = 5m
  t (1m)
}
`
	r, ds := evaluate(t, src)
	if e := errorsOf(ds); len(e) > 0 {
		t.Fatal(e)
	}
	if v, _ := r.Root.Children[0].Get("size"); v.Elems[1].Num != 2 {
		t.Fatalf("the template read the caller's let: %v", v.Format())
	}
}

const loftHead = "greenware 1\n\nasset a { root = a length = yd }\n"

func TestLoftRows(t *testing.T) {
	src := loftHead + `
part a {
  loft {
    segments = 8
    caps = off
    columns = (y, rx, rz, drop)
    vertex = (rx * sin(a), y + drop * j, rz * cos(a) + k * 0.01)
    ring (1, 0.1, 0.2, 0)
    ring (2, 0.1, 0.2, 1)
  }
  loft {
    ring (0, 1, 0, 0.5, 0.25)
    ring (1m, 1, 0, 0.5, 0.25)
  }
}
`
	r, ds := evaluate(t, src)
	if e := errorsOf(ds); len(e) > 0 {
		t.Fatal(e)
	}
	l := r.Root.Children[0]
	if l.Loft == nil || len(l.Rows) != 2 || len(l.Rows[0].Values) != 4 {
		t.Fatalf("loft node %+v", l)
	}
	// Every column is a length in metres, converted once from yards.
	if y := l.Rows[1].Values[0].Num; math.Abs(y-1.8288) > 1e-12 {
		t.Fatalf("ring 1 y = %v", y)
	}
	// The rule is evaluated at a column count of its own: a = 360deg * j / n,
	// j and k are the whole numbers of the column and the ring.
	pts, err := l.Loft.Points(4, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 || len(pts[0]) != 4 {
		t.Fatalf("grid %d x %d", len(pts), len(pts[0]))
	}
	// Ring 1, column 1: a = 90deg, rx = 0.1 yd, y + drop * j = 2 + 1 = 3 yd.
	if p := pts[1][1]; math.Abs(p[0]-0.09144) > 1e-12 || math.Abs(p[1]-3*0.9144) > 1e-12 {
		t.Fatalf("point %v", p)
	}
	// z = rz * cos(90deg) + k * 0.01 = 0.2 * 0 + 0.01 yd.
	if p := pts[1][1]; math.Abs(p[2]-0.01*0.9144) > 1e-12 {
		t.Fatalf("z %v", p[2])
	}
	arc, err := l.Loft.Points(2, true, 0, math.Pi)
	if err != nil || len(arc[0]) != 3 {
		t.Fatalf("arc grid: %v %v", err, arc)
	}
	// The second loft takes the default columns and no rule.
	d := r.Root.Children[1]
	if d.Loft != nil || len(d.Rows[0].Values) != 5 || d.Rows[1].Values[0].Num != 1 {
		t.Fatalf("default loft %+v", d)
	}
	if segs, _ := d.Get("segments"); segs.Num != 14 {
		t.Fatalf("default segments %v", segs.Num)
	}
}

func TestLoftMistakes(t *testing.T) {
	cases := []struct{ name, body, code, msg string }{
		{"one ring", "ring (0, 1, 0, 1, 1)", diag.CodeMissing, "at least 2"},
		{"wrong column count", "ring (0, 1, 0, 1) ring (1, 1, 0, 1)", diag.CodeType, "takes 5 values"},
		{"custom columns without a rule", "columns = (y, rx) ring (0, 1) ring (1, 1)", diag.CodeMissing, "needs a `vertex` rule"},
		{"duplicate column", "columns = (y, y) vertex = (0, y, 0) ring (0, 1) ring (1, 1)", diag.CodeDuplicate, "listed twice"},
		{"reserved column", "columns = (y, a) vertex = (0, y, 0) ring (0, 1) ring (1, 1)", diag.CodeFieldRange, "own angle or index"},
		{"arc with caps", "arc = 10deg..100deg ring (0, 1, 0, 1, 1) ring (1, 1, 0, 1, 1)", diag.CodeFieldRange, "caps = off"},
		{"arc over a turn", "arc = 0deg..400deg caps = off ring (0, 1, 0, 1, 1) ring (1, 1, 0, 1, 1)", diag.CodeFieldRange, "at most one turn"},
		{"negative radius", "ring (0, 1, 0, -1, 1) ring (1, 1, 0, 1, 1)", diag.CodeFieldRange, "must not be negative"},
		{"runtime in a ring", "ring (0, 1, 0, g, 1) ring (1, 1, 0, 1, 1)", diag.CodeRuntime, "cannot depend on a runtime parameter"},
		{"runtime in the rule", "columns = (y) vertex = (0, y + g, 0) caps = off ring (0) ring (1)", diag.CodeRuntime, "vertex rule depends on a runtime"},
		{"rule not a point", "columns = (y) vertex = (y, y) ring (0) ring (1)", diag.CodeType, ""},
		{"rule domain fault", "columns = (y) vertex = (0, y / (j - j), 0) caps = off ring (0) ring (1)", diag.CodeDomain, "divi"},
		{"rule unknown name", "columns = (y) vertex = (0, y, zz) caps = off ring (0) ring (1)", diag.CodeUnknownName, "`zz`"},
		{"column not a length", "columns = (y) vertex = (0, y, 0) caps = off ring (0deg) ring (1)", diag.CodeUnit, ""},
	}
	for _, c := range cases {
		src := loftHead + "param g : length = 1yd runtime\npart a {\n loft { " + c.body + " }\n}\n"
		_, ds := evaluate(t, src)
		if !hasMsg(ds, c.code, c.msg) {
			t.Errorf("%s: want %s %q, got %v", c.name, c.code, c.msg, ds)
		}
	}
	// A rule's fault is reported once, where the expression is written.
	_, ds := evaluate(t, loftHead+"part a {\n loft { columns = (y) vertex = (0, y / (j - j), 0) caps = off ring (0) ring (1) }\n}\n")
	n := 0
	for _, d := range ds {
		if d.Code == diag.CodeDomain {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d domain diagnostics, want 1", n)
	}
}

func TestEllipsoidAndTubeMistakes(t *testing.T) {
	cases := []struct{ name, body, code, msg string }{
		{"zero radius", "ellipsoid { radii = (1, 0, 1) }", diag.CodeFieldRange, "greater than zero"},
		{"no radii", "ellipsoid { }", diag.CodeMissing, "radii"},
		{"too few rings", "ellipsoid { radii = (1, 1, 1) rings = 1 }", diag.CodeFieldRange, "at least 2"},
		{"tube radius", "tube { radius = 0 point (0,0,0) point (1,0,0) }", diag.CodeFieldRange, "greater than zero"},
		{"one point", "tube { radius = 1 point (0,0,0) }", diag.CodeMissing, "at least 2"},
		{"closed needs three", "tube { radius = 1 closed = on point (0,0,0) point (1,0,0) }", diag.CodeMissing, "at least 3 points"},
		{"tube segments", "tube { radius = 1 segments = 2 point (0,0,0) point (1,0,0) }", diag.CodeFieldRange, "at least 3"},
	}
	for _, c := range cases {
		_, ds := evaluate(t, loftHead+"part a {\n "+c.body+"\n}\n")
		if !hasMsg(ds, c.code, c.msg) {
			t.Errorf("%s: want %s %q, got %v", c.name, c.code, c.msg, ds)
		}
	}
	// Defaults of the new kinds.
	r, ds := evaluate(t, loftHead+"part a {\n ellipsoid { radii = (1, 2, 3) }\n tube { radius = 1 point (0,0,0) point (1,0,0) }\n}\n")
	if e := errorsOf(ds); len(e) > 0 {
		t.Fatal(e)
	}
	e, tb := r.Root.Children[0], r.Root.Children[1]
	if s, _ := e.Get("segments"); s.Num != 12 {
		t.Errorf("ellipsoid segments %v", s.Num)
	}
	if s, _ := e.Get("rings"); s.Num != 7 {
		t.Errorf("ellipsoid rings %v", s.Num)
	}
	if s, _ := tb.Get("segments"); s.Num != 7 {
		t.Errorf("tube segments %v", s.Num)
	}
}

func TestVertexRuleIsPartOfTheMeaning(t *testing.T) {
	// The vertex expression and the asset unit are in the node's fields, so a
	// change in either changes the meaning hash that is built from them.
	a, _ := evaluate(t, loftHead+"part a { loft { columns = (y) vertex = (0, y, 0) caps = off ring (0) ring (1) } }\n")
	b, _ := evaluate(t, loftHead+"part a { loft { columns = (y) vertex = (0, y + 0.1, 0) caps = off ring (0) ring (1) } }\n")
	c, _ := evaluate(t, strings.Replace(loftHead, "yd", "m", 1)+"part a { loft { columns = (y) vertex = (0, y, 0) caps = off ring (0) ring (1) } }\n")
	get := func(r *resolve.Resolved) string {
		v, _ := r.Root.Children[0].Get("vertex")
		return v.Str
	}
	if get(a) == get(b) || get(a) == get(c) {
		t.Fatalf("vertex fields do not tell the rules apart: %q %q %q", get(a), get(b), get(c))
	}
}

func TestGeometryOnlyLowering(t *testing.T) {
	src := "greenware 1\n\nasset a { root = a length = m }\n" + `
import "std/finish"
plug head { kind = helmet at = (0, 1, 0) }
texture t = atlas { grid = (2, 2) }
material m = selena { source = "x.sel" name = "X" mask = t fallback = pbr { base_texture = paint(t) } }
part a { material = m
  box { size = (1m, 1m, 1m) cell = shell }
}
`
	f, ds := ast.LowerWith(lang.Parse([]byte(src)), ast.Options{GeometryOnly: true})
	if diag.HasErrors(ds) {
		t.Fatalf("geometry only: %v", ds)
	}
	if len(f.Skipped) != 5 {
		t.Fatalf("skipped %v", f.Skipped)
	}
	if len(f.Materials) != 1 || f.Materials[0].Kind != "pbr" || f.Materials[0].Fallback != nil {
		t.Fatalf("material %+v", f.Materials[0])
	}
	if _, ds := resolve.Evaluate(f, nil); diag.HasErrors(ds) {
		t.Fatalf("resolve: %v", ds)
	}
	// The full lowering reads each of them.
	if _, ds = ast.Lower(lang.Parse([]byte(src))); diag.HasErrors(ds) {
		t.Errorf("full lowering: %v", ds)
	}
}
