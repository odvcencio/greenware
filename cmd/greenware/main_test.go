package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goalpost = "../../testdata/syntax/goalpost.gw"

func exec(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, &o, &e)
	return code, o.String(), e.String()
}

// oneJSON decodes stdout as exactly one JSON value.
func oneJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, s)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Fatalf("stdout holds more than one JSON value:\n%s", s)
	}
	return v
}

func TestBuildGoalPost(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	code, out, errOut := exec(t, "build", goalpost, "--out-dir", dir, "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	rep := oneJSON(t, out)
	if rep["ok"] != true || rep["written"] != true {
		t.Errorf("report: %v", rep)
	}
	outs := rep["outputs"].([]any)
	if len(outs) != 2 {
		t.Fatalf("outputs: %v", outs)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 7 {
		t.Errorf("%d files written", len(ents))
	}
	for _, name := range []string{"goalpost.glb", "goalpost-lod1.glb", "goalpost-lod2.glb", "goalpost-practice.glb", "goalpost-practice-lod1.glb", "goalpost-practice-lod2.glb", "goalpost.build.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s", name)
		}
	}
	// One output only.
	dir2 := filepath.Join(t.TempDir(), "out")
	if code, _, e := exec(t, "build", "--output", "practice", goalpost, "--out-dir="+dir2); code != 0 {
		t.Fatalf("exit %d: %s", code, e)
	}
	if ents, _ := os.ReadDir(dir2); len(ents) != 4 {
		t.Errorf("practice alone wrote %d files", len(ents))
	}
}

func TestBuildTextOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "o")
	code, out, errOut := exec(t, "build", goalpost, "--out-dir", dir)
	if code != 0 || !strings.Contains(out, "goalpost.glb  1340 triangles, 3 draws") {
		t.Errorf("exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "manifest") {
		t.Errorf("no note about the manifest: %s", errOut)
	}
}

func TestBuildBudgetFailureWritesNothing(t *testing.T) {
	src, _ := os.ReadFile(goalpost)
	bad := strings.Replace(string(src), "triangles = 1600          // drawn at lod 0 in the default look", "triangles = 1300", 1)
	if bad == string(src) {
		t.Fatal("replacement did not apply")
	}
	tmp := t.TempDir()
	path := filepath.Join(tmp, "bad.gw")
	os.WriteFile(path, []byte(bad), 0o644)
	dir := filepath.Join(tmp, "out")
	code, out, _ := exec(t, "build", path, "--out-dir", dir, "--json")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	rep := oneJSON(t, out)
	if rep["ok"] != false || rep["written"] != false || !strings.Contains(out, "GW-BUDGET") {
		t.Errorf("report: %s", out)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the output directory exists")
	}
}

func TestSyntaxErrorIsExitOne(t *testing.T) {
	code, out, _ := exec(t, "check", "../../testdata/diag/GW-SYNTAX.gw", "--json")
	if code != 1 || !strings.Contains(out, "GW-SYNTAX") {
		t.Errorf("exit %d\n%s", code, out)
	}
	oneJSON(t, out)
	if code, _, _ := exec(t, "build", "../../testdata/diag/GW-SYNTAX.gw", "--out-dir", t.TempDir()); code != 1 {
		t.Errorf("build of a broken file: exit %d", code)
	}
}

func TestCheck(t *testing.T) {
	code, out, _ := exec(t, "check", goalpost, "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if rep := oneJSON(t, out); rep["ok"] != true {
		t.Errorf("%v", rep)
	}
	if code, out, _ := exec(t, "check", goalpost); code != 0 || !strings.Contains(out, "ok") {
		t.Errorf("text check: exit %d %q", code, out)
	}
	// check writes nothing and builds nothing.
	if code, _, _ := exec(t, "check", "../../testdata/diag/GW-BUDGET.gw"); code != 1 {
		t.Errorf("GW-BUDGET fixture: exit %d", code)
	}
}

func TestUsageErrorsAreExitTwo(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"frobnicate"},
		{"build"},
		{"build", goalpost, "--nope"},
		{"build", goalpost, "--out-dir"},
		{"build", "a.gw", "b.gw"},
		{"build", "/no/such/file.gw"},
		{"check"},
		{"fmt", goalpost, "--out-dir", "x"},
	} {
		if code, _, _ := exec(t, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestAcceptIsNotWiredYet(t *testing.T) {
	code, out, errOut := exec(t, "accept", goalpost)
	if code != 2 || out != "" || !strings.Contains(errOut, "not built yet") {
		t.Errorf("exit %d %q %q", code, out, errOut)
	}
}

func TestFmt(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "m.gw")
	messy := "greenware 1\nasset a   {  root = a }\n\n\n\npart a { sphere { radius=1 segments=8 } }\n"
	os.WriteFile(path, []byte(messy), 0o644)
	if code, _, _ := exec(t, "fmt", path, "--check"); code != 1 {
		t.Errorf("--check on a messy file: exit %d", code)
	}
	code, out, _ := exec(t, "fmt", path)
	if code != 0 || out == messy || !strings.HasPrefix(out, "greenware 1\n") {
		t.Errorf("fmt: exit %d\n%s", code, out)
	}
	if b, _ := os.ReadFile(path); string(b) != messy {
		t.Error("fmt without --write changed the file")
	}
	if code, _, _ := exec(t, "fmt", path, "--write"); code != 0 {
		t.Errorf("--write: exit %d", code)
	}
	if code, _, _ := exec(t, "fmt", path, "--check"); code != 0 {
		t.Errorf("--check after --write: exit %d", code)
	}
	code, out, _ = exec(t, "fmt", path, "--check", "--json")
	if code != 0 {
		t.Errorf("json: exit %d", code)
	}
	if rep := oneJSON(t, out); rep["changed"] != false {
		t.Errorf("%v", rep)
	}
	os.WriteFile(path, []byte("greenware 1\npart {{"), 0o644)
	if code, _, _ := exec(t, "fmt", path); code != 1 {
		t.Errorf("syntax error: exit %d", code)
	}
}
