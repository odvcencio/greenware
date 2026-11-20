package resolve_test

import (
	"encoding/json"
	"os"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

func load(t testing.TB, path string) *ast.File {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, ds := ast.Lower(lang.Parse(src))
	for _, d := range ds {
		t.Fatalf("lowering %s: %v", path, d)
	}
	return f
}

func TestGoalPostResolves(t *testing.T) {
	f := load(t, "../testdata/syntax/goalpost.gw")
	all, ds := resolve.EvaluateAll(f)
	for _, d := range ds {
		t.Errorf("%v", d)
	}
	if len(all) != 3 {
		t.Fatalf("results = %d", len(all))
	}
	_ = diag.Error
}

func marshal(t testing.TB, v any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

// The golden file is a candidate: the lead has it reviewed independently
// before it is approved. Set GW_UPDATE_GOLDEN=1 to rewrite it.
const goldenPath = "../testdata/resolve/goalpost.default.candidate.json"

func TestGoalPostGolden(t *testing.T) {
	f := load(t, "../testdata/syntax/goalpost.gw")
	r, ds := resolve.Evaluate(f, nil)
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	got := marshal(t, r)
	if os.Getenv("GW_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("resolved goal post differs from %s (run with GW_UPDATE_GOLDEN=1 to rewrite, then have it reviewed)", goldenPath)
	}
}

func TestGoalPostDefaultValues(t *testing.T) {
	f := load(t, "../testdata/syntax/goalpost.gw")
	r, _ := resolve.Evaluate(f, nil)
	approx := func(name string, got, want float64) {
		t.Helper()
		if d := got - want; d > 1e-9 || d < -1e-9 {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	for name, want := range map[string]float64{"bend_start": 1.22, "half_span": 2.87, "post_r": 0.1} {
		v, ok := r.Let(name)
		if !ok {
			t.Fatalf("no let %s", name)
		}
		approx(name, v.Num, want)
	}
	// The sampled path has the first point and nine samples: ten rows.
	post := r.Root.Children[1].Children[0]
	var path *resolve.Node
	for _, c := range post.Children {
		if c.Kind == "path" {
			path = c
		}
	}
	if path == nil || len(path.Rows) != 10 {
		t.Fatalf("path rows = %v", path)
	}
	last := path.Rows[9].Values
	approx("last y", last[1].Num, 1.22+1.83) // bend_start + setback * sin(90deg)
	approx("last z", last[2].Num, 0)
	if r.Look.DrawCalls != 3 || len(r.Look.Materials) != 3 {
		t.Errorf("look = %+v", r.Look)
	}
}

func TestPracticeOverrides(t *testing.T) {
	f := load(t, "../testdata/syntax/goalpost.gw")
	r, ds := resolve.EvaluateOutput(f, "practice")
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	if v, _ := r.Param("padding"); v.Bool {
		t.Error("padding should be off")
	}
	if v, _ := r.Param("upright_rise"); v.Num != 6.1 {
		t.Errorf("upright_rise = %v", v.Num)
	}
	for _, c := range r.Root.Children {
		if c.Name == "pad" {
			t.Error("the practice post has a pad")
		}
	}
	if r.Look.DrawCalls != 2 || len(r.Look.Materials) != 2 {
		t.Errorf("look = %+v", r.Look)
	}
	// The upright is 6.1 m + 0.06 m tall and its tip sits 6.1 m above the bar.
	mirror := r.Root.Children[3]
	up := mirror.Children[0]
	var sphere *resolve.Node
	for _, c := range up.Children {
		if c.Kind == "sphere" {
			sphere = c
		}
	}
	at, _ := sphere.Get("at")
	v3, _ := at.Vec3()
	if d := v3[1] - 9.15; d > 1e-9 || d < -1e-9 {
		t.Errorf("tip y = %v", v3[1])
	}
	found := false
	for _, f := range r.Budget {
		if f.Name == "draw_calls" && f.Value.Num == 2 {
			found = true
		}
	}
	if !found {
		t.Errorf("the practice budget should replace draw_calls with 2: %+v", r.Budget)
	}
}

func TestDeterministic(t *testing.T) {
	src, _ := os.ReadFile("../testdata/syntax/goalpost.gw")
	var first [][]byte
	for i := 0; i < 3; i++ {
		f, _ := ast.Lower(lang.Parse(src))
		all, ds := resolve.EvaluateAll(f)
		out := marshal(t, all)
		dj, _ := diag.Marshal(ds, nil)
		if i == 0 {
			first = [][]byte{out, dj}
			continue
		}
		if string(out) != string(first[0]) || string(dj) != string(first[1]) {
			t.Fatalf("run %d differs from run 0", i)
		}
	}
	// Bit-equal values, not only equal text.
	f1, _ := ast.Lower(lang.Parse(src))
	f2, _ := ast.Lower(lang.Parse(src))
	a, _ := resolve.Evaluate(f1, nil)
	b, _ := resolve.Evaluate(f2, nil)
	for i := range a.Lets {
		if !units.Equal(a.Lets[i].Value, b.Lets[i].Value) {
			t.Errorf("let %s differs", a.Lets[i].Name)
		}
	}
}

func TestAcceptFileHasNoDiagnostics(t *testing.T) {
	f := load(t, "../testdata/syntax/goalpost.accept.gw")
	if _, ds := resolve.EvaluateAll(f); len(ds) != 0 {
		t.Errorf("%v", ds)
	}
}
