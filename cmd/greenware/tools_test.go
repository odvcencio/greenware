package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const fx = "../../testdata/tools/"

// keyOrder returns the top-level keys of the JSON object in s, in order, and
// fails unless s holds exactly one value.
func keyOrder(t *testing.T, s string) []string {
	t.Helper()
	oneJSON(t, s)
	dec := json.NewDecoder(strings.NewReader(s))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not an object: %v %v", tok, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---- fix ---------------------------------------------------------------------

func TestFixAlternativesNeedChoose(t *testing.T) {
	before := readFile(t, fx+"unit.gw")
	code, out, errOut := exec(t, "fix", fx+"unit.gw")
	if code != 1 {
		t.Fatalf("exit %d, want 1 (the unit error stays)\n%s%s", code, out, errOut)
	}
	if out != "" || !strings.Contains(errOut, "alternatives") || !strings.Contains(errOut, "1. write 30rad") || !strings.Contains(errOut, "2. write 30deg") {
		t.Errorf("stdout %q stderr %q", out, errOut)
	}
	if readFile(t, fx+"unit.gw") != before {
		t.Error("a dry run changed the file")
	}
}

func TestFixChooseDryRunPrintsDiff(t *testing.T) {
	code, out, errOut := exec(t, "fix", fx+"unit.gw", "--choose", "2")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	if !strings.HasPrefix(out, "--- a/") || !strings.Contains(out, "-let lift = sin(30) * 1m\n+let lift = sin(30deg) * 1m\n") {
		t.Errorf("diff:\n%s", out)
	}
	code, out, errOut = exec(t, "fix", fx+"unit.gw", "--choose", "1")
	if code != 0 || !strings.Contains(out, "sin(30rad)") {
		t.Errorf("--choose 1: exit %d\n%s%s", code, out, errOut)
	}
	if code, _, _ := exec(t, "fix", fx+"unit.gw", "--choose", "0"); code != 2 {
		t.Errorf("--choose 0: exit %d, want 2", code)
	}
}

func TestFixWriteIsIdempotentAndCheckPasses(t *testing.T) {
	for _, name := range []string{"GW-EDITION", "GW-UNKNOWN-NAME", "GW-PARAM-RANGE"} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "a.gw")
			copyFile(t, "../../testdata/diag/"+name+".gw", file)
			if code, _, _ := exec(t, "check", file); code != 1 {
				t.Fatalf("fixture does not fail check: %d", code)
			}
			code, out, errOut := exec(t, "fix", file, "--write")
			if code != 0 {
				t.Fatalf("fix --write: exit %d\n%s%s", code, out, errOut)
			}
			if code, _, errOut := exec(t, "check", file); code != 0 {
				t.Fatalf("check after fix: exit %d\n%s", code, errOut)
			}
			fixed := readFile(t, file)
			code, out, _ = exec(t, "fix", file, "--write")
			if code != 0 || !strings.Contains(out, "nothing to fix") || readFile(t, file) != fixed {
				t.Errorf("second run: exit %d %q; file changed: %v", code, out, readFile(t, file) != fixed)
			}
		})
	}
}

func TestFixCodeFilter(t *testing.T) {
	file := filepath.Join(t.TempDir(), "two.gw")
	copyFile(t, fx+"two.gw", file)
	code, out, errOut := exec(t, "fix", file, "--code", "GW-PARAM-RANGE", "--write")
	if code != 1 {
		t.Fatalf("exit %d: the unknown name remains\n%s%s", code, out, errOut)
	}
	got := readFile(t, file)
	if !strings.Contains(got, "height : length = 3m") || !strings.Contains(got, "hieght") {
		t.Errorf("only the range fault should be fixed:\n%s", got)
	}
	if code, _, _ := exec(t, "fix", file, "--code", "GW-NOPE"); code != 2 {
		t.Errorf("unknown code: exit %d, want 2", code)
	}
	if code, _, errOut := exec(t, "fix", file, "--write"); code != 0 {
		t.Errorf("fixing the rest: exit %d\n%s", code, errOut)
	}
}

func TestFixJSONAndRevision(t *testing.T) {
	code, out, _ := exec(t, "fix", fx+"two.gw", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	want := []string{"schema", "file", "input_revision", "output_revision", "ok", "changed", "written", "refused", "errors_before", "errors_after", "applied", "skipped", "diff"}
	if got := keyOrder(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("keys %v, want %v", got, want)
	}
	rep := oneJSON(t, out)
	if rep["changed"] != true || rep["written"] != false || len(rep["applied"].([]any)) != 2 {
		t.Errorf("report: %v", rep)
	}
	// An edit is bound to the revision it was computed from.
	file := filepath.Join(t.TempDir(), "a.gw")
	copyFile(t, fx+"two.gw", file)
	before := readFile(t, file)
	code, out, _ = exec(t, "fix", file, "--write", "--revision", "sha256:"+strings.Repeat("0", 64), "--json")
	rep = oneJSON(t, out)
	if code != 1 || rep["refused"] != true || rep["written"] != false || readFile(t, file) != before {
		t.Errorf("stale revision: exit %d %v", code, rep)
	}
	rev := oneJSON(t, out)["input_revision"].(string)
	if code, _, _ := exec(t, "fix", file, "--write", "--revision", rev); code != 0 {
		t.Errorf("matching revision: exit %d", code)
	}
}

func TestFixUsageAndReadErrors(t *testing.T) {
	if code, _, errOut := exec(t, "fix"); code != 2 || !strings.Contains(errOut, "usage: greenware fix") {
		t.Errorf("no file: %d %q", code, errOut)
	}
	if code, _, _ := exec(t, "fix", fx+"two.gw", "--bogus"); code != 2 {
		t.Errorf("bad flag: %d", code)
	}
	if code, _, _ := exec(t, "fix", fx+"missing.gw"); code != 2 {
		t.Errorf("missing file: %d", code)
	}
}

// ---- diff --------------------------------------------------------------------

func TestDiffIgnoresLayoutAndComments(t *testing.T) {
	code, out, errOut := exec(t, "diff", fx+"post_a.gw", fx+"post_a_reformatted.gw")
	if code != 0 || out != "no semantic changes\n" {
		t.Errorf("exit %d %q %q", code, out, errOut)
	}
	code, out, _ = exec(t, "diff", fx+"post_a.gw", fx+"post_a_reformatted.gw", "--json", "--fail-on", "change")
	rep := oneJSON(t, out)
	if code != 0 || rep["changed"] != false || len(rep["changes"].([]any)) != 0 {
		t.Errorf("json: exit %d %v", code, rep)
	}
}

func TestDiffSemanticChanges(t *testing.T) {
	code, out, errOut := exec(t, "diff", fx+"post_a.gw", fx+"post_b.gw", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	want := []string{"schema", "old", "new", "changed", "breaking", "failed", "changes"}
	if got := keyOrder(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("keys %v, want %v", got, want)
	}
	var rep struct {
		Breaking bool
		Changes  []struct {
			Kind, Name, Change, From string
			Breaking                 bool
			Fields                   []struct{ Field, Old, New string }
		}
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range rep.Changes {
		s := c.Kind + " " + c.Name + " " + c.Change
		if c.From != "" {
			s += " from " + c.From
		}
		if c.Breaking {
			s += " BREAKING"
		}
		got = append(got, s)
	}
	want = []string{
		"param height changed",
		"param radius changed BREAKING",
		"param slope added",
		"param tint removed BREAKING",
		"material paint changed",
		"material wood changed",
		"part finial renamed from top",
		"part rail added",
		"socket flag changed",
		"budget budget changed",
		"output practice added",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !rep.Breaking {
		t.Error("breaking is false")
	}
	// Default and range detail.
	for _, c := range rep.Changes {
		if c.Kind == "param" && c.Name == "height" && (len(c.Fields) != 1 || c.Fields[0].Field != "default" || c.Fields[0].Old != "1.2m" || c.Fields[0].New != "1.5m") {
			t.Errorf("height fields: %+v", c.Fields)
		}
	}
	// Same bytes on a second run.
	_, out2, _ := exec(t, "diff", fx+"post_a.gw", fx+"post_b.gw", "--json")
	if out != out2 {
		t.Error("two runs differ")
	}
}

func TestDiffFailOn(t *testing.T) {
	if code, _, _ := exec(t, "diff", fx+"post_a.gw", fx+"post_b.gw", "--fail-on", "breaking"); code != 1 {
		t.Errorf("breaking gate: %d, want 1", code)
	}
	// Reverse: removing the output and adding a runtime param is breaking too,
	// but a pure addition is not.
	if code, _, _ := exec(t, "diff", fx+"post_a.gw", fx+"post_b.gw", "--fail-on", "change"); code != 1 {
		t.Errorf("change gate: %d, want 1", code)
	}
	if code, _, _ := exec(t, "diff", fx+"post_a.gw", fx+"post_a_reformatted.gw", "--fail-on", "change"); code != 0 {
		t.Errorf("change gate on equal: %d, want 0", code)
	}
	code, out, _ := exec(t, "diff", fx+"post_b.gw", fx+"post_a.gw", "--fail-on", "breaking", "--json")
	rep := oneJSON(t, out)
	// Going back adds the runtime param tint (safe) but also removes slope (a
	// bake param, safe) and the practice output (breaking).
	if code != 1 || rep["breaking"] != true {
		t.Errorf("reverse: exit %d %v", code, rep["breaking"])
	}
	if code, _, _ := exec(t, "diff", fx+"post_a.gw", fx+"post_b.gw", "--fail-on", "x"); code != 2 {
		t.Errorf("bad gate value: %d", code)
	}
	if code, _, _ := exec(t, "diff", fx+"post_a.gw"); code != 2 {
		t.Errorf("one file: %d", code)
	}
	if code, _, _ := exec(t, "diff", fx+"post_a.gw", fx+"nope.gw"); code != 2 {
		t.Errorf("missing file: %d", code)
	}
}

// ---- describe ----------------------------------------------------------------

func TestDescribeJSON(t *testing.T) {
	code, out, errOut := exec(t, "describe", goalpost, "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	wantKeys := []string{"schema", "file", "ok", "asset", "meta", "imports", "params", "outputs", "levels", "parts", "materials", "sockets",
		"plugs", "mounts", "colliders", "rigs", "tables", "templates", "painters", "textures", "budget", "evaluated", "unresolved", "diagnostics"}
	if got := keyOrder(t, out); !reflect.DeepEqual(got, wantKeys) {
		t.Errorf("keys %v\nwant %v", got, wantKeys)
	}
	var d struct {
		Params []struct {
			Name, Type, Unit, Default, Lo, Hi, Binding string
		}
		Outputs []struct {
			Name   string
			Levels []int
		}
		Parts []struct {
			Name, Role string
			Children   []struct{ Name, Pair, Condition, Mirror string }
		}
		Sockets   []struct{ Name, Part, Kind string }
		Materials []struct{ Name, Kind string }
		Budget    []struct{ Name, Value string }
		Evaluated bool
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Params) != 6 || d.Params[0].Name != "crossbar_height" || d.Params[0].Type != "length" || d.Params[0].Unit != "m" ||
		d.Params[0].Default != "3.05m" || d.Params[0].Lo != "2.5m" || d.Params[0].Hi != "3.5m" || d.Params[0].Binding != "bake" {
		t.Errorf("params: %+v", d.Params)
	}
	if p := d.Params[5]; p.Name != "pad_color" || p.Binding != "runtime" {
		t.Errorf("pad_color: %+v", p)
	}
	if len(d.Outputs) != 2 || d.Outputs[0].Name != "stadium" || !reflect.DeepEqual(d.Outputs[0].Levels, []int{0, 1, 2}) {
		t.Errorf("outputs: %+v", d.Outputs)
	}
	if len(d.Parts) != 2 || d.Parts[1].Name != "goalpost" || d.Parts[1].Role != "root" || d.Parts[0].Role != "template" {
		t.Errorf("parts: %+v", d.Parts)
	}
	kids := d.Parts[1].Children
	if len(kids) != 5 || kids[3].Pair != "upright_r" || kids[3].Mirror != "x" || kids[4].Name != "pad" || kids[4].Condition != "padding" {
		t.Errorf("children: %+v", kids)
	}
	if len(d.Sockets) != 3 || d.Sockets[0].Name != "crossbar_centre" || d.Sockets[0].Part != "crossbar" || d.Sockets[0].Kind != "anchor" {
		t.Errorf("sockets: %+v", d.Sockets)
	}
	if len(d.Materials) != 3 || d.Materials[0].Kind != "selena" || len(d.Budget) != 6 || !d.Evaluated {
		t.Errorf("materials %+v budget %+v evaluated %v", d.Materials, d.Budget, d.Evaluated)
	}
}

func TestDescribeText(t *testing.T) {
	code, out, _ := exec(t, "describe", fx+"post_a.gw")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"parameters (4):", "tint : color = #aa3322  runtime", "height : length [m] = 1.2m in 0.5m..2m  bake", "outputs (1):", "stadium file=post.glb", "lid [part] when cap", "flag on top"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestDescribeInvalidAndMissing(t *testing.T) {
	code, out, errOut := exec(t, "describe", "../../testdata/diag/GW-UNKNOWN-NAME.gw", "--json")
	rep := oneJSON(t, out)
	if code != 1 || rep["ok"] != false || !strings.Contains(out, "GW-UNKNOWN-NAME") {
		t.Errorf("exit %d %v %s", code, rep["ok"], errOut)
	}
	if code, _, _ := exec(t, "describe", fx+"missing.gw"); code != 2 {
		t.Errorf("missing: %d", code)
	}
	if code, _, _ := exec(t, "describe"); code != 2 {
		t.Errorf("no file: %d", code)
	}
}

// ---- ir ----------------------------------------------------------------------

func TestIRBareEqualsGolden(t *testing.T) {
	code, out, errOut := exec(t, "ir", goalpost)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if out != readFile(t, "../../testdata/resolve/goalpost.default.candidate.json") {
		t.Error("ir output differs from the resolve golden")
	}
}

func TestIRJSONOutput(t *testing.T) {
	code, out, errOut := exec(t, "ir", goalpost, "--output", "practice", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	want := []string{"schema", "stage", "file", "output", "ok", "model", "diagnostics"}
	if got := keyOrder(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("keys %v", got)
	}
	var rep struct {
		Model struct {
			Output string
			Params []struct {
				Name  string
				Value json.RawMessage
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Model.Output != "practice" {
		t.Errorf("output %q", rep.Model.Output)
	}
	for _, p := range rep.Model.Params {
		if p.Name == "padding" && string(p.Value) != "false" {
			t.Errorf("padding under practice = %s", p.Value)
		}
	}
	_, out2, _ := exec(t, "ir", goalpost, "--output", "practice", "--json")
	if out != out2 {
		t.Error("two runs differ")
	}
}

func TestIRErrors(t *testing.T) {
	code, out, _ := exec(t, "ir", goalpost, "--output", "nope", "--json")
	rep := oneJSON(t, out)
	if code != 1 || rep["model"] != nil || rep["ok"] != false {
		t.Errorf("unknown output: exit %d %v", code, rep)
	}
	if code, _, _ := exec(t, "ir", "../../testdata/diag/GW-UNKNOWN-NAME.gw"); code != 1 {
		t.Errorf("invalid file: %d", code)
	}
	if code, _, _ := exec(t, "ir", fx+"missing.gw"); code != 2 {
		t.Errorf("missing file: %d", code)
	}
}

// ---- init --------------------------------------------------------------------

func TestInitCreatesBuildableProject(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-assets")
	code, out, errOut := exec(t, "init", dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	for _, f := range []string{"greenware.mod", "my_assets.gw", "greenware.sum"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if got := readFile(t, filepath.Join(dir, "greenware.mod")); got != "project my_assets\ngreenware 1\n" {
		t.Errorf("greenware.mod = %q", got)
	}
	src := filepath.Join(dir, "my_assets.gw")
	if code, _, errOut := exec(t, "check", src); code != 0 {
		t.Fatalf("check: %d\n%s", code, errOut)
	}
	if code, _, errOut := exec(t, "fmt", src, "--check"); code != 0 {
		t.Errorf("the example is not formatted: %d %s", code, errOut)
	}
	out2 := filepath.Join(t.TempDir(), "out")
	if code, _, errOut := exec(t, "build", src, "--out-dir", out2); code != 0 {
		t.Fatalf("build: %d\n%s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(out2, "my_assets.glb")); err != nil {
		t.Error("build wrote no glb")
	}
	if code, _, errOut := exec(t, "lock", dir, "--check"); code != 0 {
		t.Errorf("lock --check right after init: %d %s", code, errOut)
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "greenware.mod")
	os.WriteFile(mod, []byte("mine\n"), 0o644)
	code, out, errOut := exec(t, "init", dir, "--name", "x", "--json")
	rep := oneJSON(t, out)
	if code != 1 || rep["ok"] != false || !strings.Contains(out, "greenware.mod") {
		t.Errorf("exit %d %v %s", code, rep, errOut)
	}
	if readFile(t, mod) != "mine\n" {
		t.Error("init overwrote greenware.mod")
	}
	if _, err := os.Stat(filepath.Join(dir, "x.gw")); err == nil {
		t.Error("init wrote a file next to a conflict")
	}
	if code, _, _ := exec(t, "init", dir, "--name", "x"); code != 1 {
		t.Errorf("text mode: %d", code)
	}
}

func TestInitFlags(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	code, out, _ := exec(t, "init", dir, "--name", "crate", "--dry-run", "--json")
	rep := oneJSON(t, out)
	if code != 0 || rep["dry_run"] != true {
		t.Errorf("dry run: %d %v", code, rep)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a dry run created the directory")
	}
	want := []string{"schema", "project", "edition", "dir", "files", "dry_run", "ok", "conflicts"}
	if got := keyOrder(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("keys %v", got)
	}
	if code, _, _ := exec(t, "init", dir, "--name", "Bad Name"); code != 2 {
		t.Errorf("bad name: %d", code)
	}
	if code, _, _ := exec(t, "init", dir, "--author", "x"); code != 2 {
		t.Errorf("author without license: %d", code)
	}
	if code, _, errOut := exec(t, "init", dir, "--name", "crate", "--author", "A B", "--license", "CC0-1.0"); code != 0 {
		t.Fatalf("with meta: %d %s", code, errOut)
	}
	src := readFile(t, filepath.Join(dir, "crate.gw"))
	if !strings.Contains(src, `author = "A B"`) || !strings.Contains(src, `license = "CC0-1.0"`) {
		t.Errorf("meta missing:\n%s", src)
	}
	if code, _, errOut := exec(t, "check", filepath.Join(dir, "crate.gw")); code != 0 {
		t.Errorf("check with meta: %d %s", code, errOut)
	}
	if code, _, _ := exec(t, "init"); code != 2 {
		t.Errorf("no dir: %d", code)
	}
}

// ---- lock --------------------------------------------------------------------

func TestLockWriteCheckAndDrift(t *testing.T) {
	dir := t.TempDir()
	copyFile(t, fx+"proj/greenware.mod", filepath.Join(dir, "greenware.mod"))
	copyFile(t, fx+"proj/a.gw", filepath.Join(dir, "a.gw"))
	copyFile(t, fx+"proj/sub/b.gw", filepath.Join(dir, "sub/b.gw"))

	if code, _, _ := exec(t, "lock", dir, "--check"); code != 1 {
		t.Errorf("check with no sum: %d, want 1", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "greenware.sum")); err == nil {
		t.Fatal("--check wrote a sum")
	}
	code, out, errOut := exec(t, "lock", dir)
	if code != 0 {
		t.Fatalf("lock: %d\n%s%s", code, out, errOut)
	}
	sum := readFile(t, filepath.Join(dir, "greenware.sum"))
	lines := strings.Split(strings.TrimSuffix(sum, "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "compiler greenware ") ||
		!strings.HasPrefix(lines[1], "source a.gw project sha256:") || !strings.HasPrefix(lines[2], "source sub/b.gw project sha256:") {
		t.Errorf("sum:\n%s", sum)
	}
	if !strings.Contains(lines[1], " CC0-1.0") || !strings.HasSuffix(lines[2], " -") {
		t.Errorf("licences: %q %q", lines[1], lines[2])
	}
	if code, _, _ := exec(t, "lock", dir, "--check"); code != 0 {
		t.Errorf("check after lock: %d", code)
	}
	// Writing again changes nothing.
	exec(t, "lock", dir)
	if readFile(t, filepath.Join(dir, "greenware.sum")) != sum {
		t.Error("lock is not idempotent")
	}

	// Drift: an edited source, an added source, a removed source.
	os.WriteFile(filepath.Join(dir, "a.gw"), []byte(readFile(t, filepath.Join(dir, "a.gw"))+"\n// edit\n"), 0o644)
	code, out, _ = exec(t, "lock", dir, "--check", "--json")
	rep := oneJSON(t, out)
	if code != 1 || rep["drift"] != true || rep["written"] != false || !strings.Contains(out, `"name": "a.gw"`) {
		t.Errorf("edited source: exit %d\n%s", code, out)
	}
	want := []string{"schema", "dir", "mode", "before_sha256", "after_sha256", "changes", "drift", "written", "ok"}
	if got := keyOrder(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("keys %v", got)
	}
	exec(t, "lock", dir)
	copyFile(t, fx+"proj/a.gw", filepath.Join(dir, "c.gw"))
	if code, out, _ := exec(t, "lock", dir, "--check"); code != 1 || !strings.Contains(out, "+ source c.gw") {
		t.Errorf("added source: %d %q", code, out)
	}
	exec(t, "lock", dir)
	os.Remove(filepath.Join(dir, "c.gw"))
	if code, out, _ := exec(t, "lock", dir, "--check"); code != 1 || !strings.Contains(out, "- source c.gw") {
		t.Errorf("removed source: %d %q", code, out)
	}
	// A dry run neither writes nor fails.
	before := readFile(t, filepath.Join(dir, "greenware.sum"))
	if code, _, _ := exec(t, "lock", dir, "--dry-run"); code != 0 || readFile(t, filepath.Join(dir, "greenware.sum")) != before {
		t.Error("dry run wrote or failed")
	}
}

func TestLockPinsLibrariesAndInputs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "greenware.mod"), []byte("project p\ngreenware 1\n"), 0o644)
	copyFile(t, "../../testdata/syntax/goalpost.gw", filepath.Join(dir, "goalpost.gw"))
	os.WriteFile(filepath.Join(dir, "goalpost.gw"), []byte(readFile(t, filepath.Join(dir, "goalpost.gw"))+"\nimport \"std/finish\"\n"), 0o644)
	// The goal post names a Selena file that must exist.
	if code, _, errOut := exec(t, "lock", dir); code != 1 || !strings.Contains(errOut, "materials/painted_metal.sel") {
		t.Fatalf("missing selena file: %d %q", code, errOut)
	}
	os.MkdirAll(filepath.Join(dir, "materials"), 0o755)
	os.WriteFile(filepath.Join(dir, "materials/painted_metal.sel"), []byte("material {}\n"), 0o644)
	if code, _, errOut := exec(t, "lock", dir); code != 0 {
		t.Fatalf("lock: %d %s", code, errOut)
	}
	sum := readFile(t, filepath.Join(dir, "greenware.sum"))
	for _, want := range []string{"\nlib std/finish std sha256:", "\nselena materials/painted_metal.sel project sha256:", "\nsource goalpost.gw project sha256:"} {
		if !strings.Contains(sum, want) {
			t.Errorf("sum lacks %q:\n%s", want, sum)
		}
	}
	if code, _, _ := exec(t, "lock", dir, "--check"); code != 0 {
		t.Errorf("check: %d", code)
	}
	os.WriteFile(filepath.Join(dir, "materials/painted_metal.sel"), []byte("material { changed }\n"), 0o644)
	if code, out, _ := exec(t, "lock", dir, "--check"); code != 1 || !strings.Contains(out, "~ selena materials/painted_metal.sel") {
		t.Errorf("changed selena file: %d %q", code, out)
	}
}

func TestLockUsage(t *testing.T) {
	if code, _, errOut := exec(t, "lock", t.TempDir()); code != 2 || !strings.Contains(errOut, "no greenware.mod") {
		t.Errorf("no mod: %d %q", code, errOut)
	}
	if code, _, _ := exec(t, "lock", "a", "b"); code != 2 {
		t.Errorf("two dirs: %d", code)
	}
}

// ---- explain -----------------------------------------------------------------

func TestExplain(t *testing.T) {
	code, out, _ := exec(t, "explain", "GW-UNIT")
	if code != 0 || !strings.HasPrefix(out, "GW-UNIT (error)\n") || !strings.Contains(out, "fixture: testdata/diag/GW-UNIT.gw") || !strings.Contains(out, "trigonometric") {
		t.Errorf("text: %d\n%s", code, out)
	}
	code, out, _ = exec(t, "explain", "gw-rig-band", "--json")
	rep := oneJSON(t, out)
	if code != 0 || rep["severity"] != "warning" || rep["code"] != "GW-RIG-BAND" {
		t.Errorf("json: %d %v", code, rep)
	}
	want := []string{"schema", "code", "severity", "doc", "fixture", "fix"}
	if got := keyOrder(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("keys %v", got)
	}
	if code, _, errOut := exec(t, "explain", "GW-NOPE"); code != 2 || !strings.Contains(errOut, "GW-UNIT") {
		t.Errorf("unknown: %d %q", code, errOut)
	}
	if code, _, _ := exec(t, "explain"); code != 2 {
		t.Errorf("no code: %d", code)
	}
}

// ---- search ------------------------------------------------------------------

func TestSearch(t *testing.T) {
	code, out, errOut := exec(t, "search", "upright", "../../testdata/syntax")
	if code != 0 {
		t.Fatalf("exit %d %s", code, errOut)
	}
	want := `../../testdata/syntax/goalpost.gw:32:7 param upright_gap
../../testdata/syntax/goalpost.gw:33:7 param upright_rise
../../testdata/syntax/goalpost.gw:40:5 let upright_r
../../testdata/syntax/goalpost.gw:138:11 part upright_l
../../testdata/syntax/goalpost.gw:138:22 part upright_r
../../testdata/syntax/goalpost.gw:222:14 collider upright_l
`
	if out != want {
		t.Errorf("search:\n%s\nwant:\n%s", out, want)
	}
	code, out, _ = exec(t, "search", "UPRIGHT", "../../testdata/syntax", "--kind", "param", "--json")
	var rep struct {
		Files   int
		Matches []struct {
			Kind, Name string
			Line       int
		}
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if code != 0 || len(rep.Matches) != 2 || rep.Matches[0].Name != "upright_gap" || rep.Files == 0 {
		t.Errorf("kind filter: %d %+v", code, rep)
	}
	want2 := []string{"schema", "query", "dir", "kind", "files", "matches"}
	if got := keyOrder(t, out); !reflect.DeepEqual(got, want2) {
		t.Errorf("keys %v", got)
	}
}

func TestSearchClipsOutputsDiagnostics(t *testing.T) {
	_, out, _ := exec(t, "search", "wave", "../../examples/arm", "--kind", "clip")
	if !strings.Contains(out, " clip ") {
		t.Errorf("no clip found for wave in the arm example:\n%s", out)
	}
	_, out, _ = exec(t, "search", "stad", "../../testdata/syntax", "--kind", "output")
	if !strings.Contains(out, "output stadium") {
		t.Errorf("output: %q", out)
	}
	_, out, _ = exec(t, "search", "GW-UNKNOWN", "../../testdata/diag")
	if !strings.Contains(out, "GW-UNKNOWN-NAME.gw:6:12 diagnostic GW-UNKNOWN-NAME") {
		t.Errorf("diagnostic: %q", out)
	}
	code, out, _ := exec(t, "search", "zzz-no-such-name", "../../testdata/syntax")
	if code != 0 || out != "" {
		t.Errorf("no match: %d %q", code, out)
	}
	code, out, _ = exec(t, "search", "zzz", "../../testdata/syntax", "--json")
	if code != 0 || !strings.Contains(out, `"matches": []`) {
		t.Errorf("empty json: %d %s", code, out)
	}
	if code, _, _ := exec(t, "search", "x", "../../testdata/nope"); code != 2 {
		t.Errorf("bad dir: %d", code)
	}
	if code, _, _ := exec(t, "search"); code != 2 {
		t.Errorf("no query: %d", code)
	}
}

// ---- trace -------------------------------------------------------------------

func TestTrace(t *testing.T) {
	code, out, errOut := exec(t, "trace", goalpost, "setback", "--json")
	if code != 0 {
		t.Fatalf("exit %d %s", code, errOut)
	}
	want := []string{"schema", "file", "name", "definition", "uses", "reaches"}
	if got := keyOrder(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("keys %v", got)
	}
	var rep struct {
		Definition struct {
			Kind string
			Line int
		}
		Uses []struct {
			Line int
			In   struct{ Kind, Name string }
			Via  []string
		}
		Reaches []struct{ Kind, Name string }
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Definition.Kind != "param" || rep.Definition.Line != 34 {
		t.Errorf("definition: %+v", rep.Definition)
	}
	direct, via := 0, 0
	sawLet, sawCheck := false, false
	for _, u := range rep.Uses {
		if len(u.Via) == 0 {
			direct++
			if u.In.Kind == "let" && u.In.Name == "bend_start" {
				sawLet = true
			}
		} else {
			via++
			if u.In.Kind == "check" && u.Via[0] == "bend_start" {
				sawCheck = true
			}
		}
	}
	if direct != 9 || via != 3 || !sawLet || !sawCheck {
		t.Errorf("direct %d via %d let %v check %v\n%s", direct, via, sawLet, sawCheck, out)
	}
	reach := map[string]bool{}
	for _, r := range rep.Reaches {
		reach[r.Kind+" "+r.Name] = true
	}
	for _, want := range []string{"part post", "part pad", "check params", "collider post_lower", "shape plate", "array bolts"} {
		if !reach[want] {
			t.Errorf("reaches lacks %q: %v", want, reach)
		}
	}
}

func TestTraceOutputSetAndUnused(t *testing.T) {
	_, out, _ := exec(t, "trace", goalpost, "padding")
	if !strings.Contains(out, "in output practice") || !strings.Contains(out, "in when") && !strings.Contains(out, "in part goalpost") {
		t.Errorf("padding trace:\n%s", out)
	}
	if !strings.HasPrefix(out, "param padding  ") {
		t.Errorf("header: %q", out)
	}
	_, out, _ = exec(t, "trace", fx+"post_a.gw", "cap_height")
	if !strings.Contains(out, "in shape ball") || !strings.Contains(out, "in socket flag") {
		t.Errorf("let trace:\n%s", out)
	}
	_, out, _ = exec(t, "trace", fx+"post_a.gw", "tint", "--json")
	if !strings.Contains(out, `"name": "paint"`) {
		t.Errorf("tint is used by the paint material:\n%s", out)
	}
	if code, _, errOut := exec(t, "trace", goalpost, "nonesuch"); code != 1 || !strings.Contains(errOut, "nonesuch") {
		t.Errorf("unknown name: %d %q", code, errOut)
	}
	if code, _, _ := exec(t, "trace", goalpost); code != 2 {
		t.Errorf("one operand: %d", code)
	}
	if code, _, _ := exec(t, "trace", fx+"missing.gw", "x"); code != 2 {
		t.Errorf("missing file: %d", code)
	}
}

// ---- dispatch ----------------------------------------------------------------

func TestToolCommandsAreInUsage(t *testing.T) {
	_, out, _ := exec(t, "help")
	for name := range toolCommands {
		if !strings.Contains(out, "greenware "+name+" ") {
			t.Errorf("usage does not list %s", name)
		}
	}
}
