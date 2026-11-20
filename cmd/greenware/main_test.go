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

const (
	exampleSrc = "../../examples/goalpost/goalpost.gw"
	exampleAcc = "../../examples/goalpost/goalpost.accept.gw"
)

func TestAcceptCommand(t *testing.T) {
	// A missing folder or a missing argument is a usage error.
	if code, _, _ := exec(t, "accept", exampleSrc); code != 2 {
		t.Errorf("one argument: exit %d, want 2", code)
	}
	dir := filepath.Join(t.TempDir(), "out")
	if code, out, errOut := exec(t, "build", exampleSrc, "--out-dir", dir); code != 0 {
		t.Fatalf("build: exit %d\n%s\n%s", code, out, errOut)
	}
	// Only the outline expects cannot be measured, so the exit status is 3.
	code, out, _ := exec(t, "accept", exampleSrc, exampleAcc, "--glb-dir", dir, "--json")
	if code != 3 {
		t.Errorf("exit %d, want 3", code)
	}
	if rep := oneJSON(t, out); rep["schema"] != "greenware.accept/1" {
		t.Errorf("%v", rep["schema"])
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

const (
	example      = "../../examples/goalpost/goalpost.gw"
	exampleAccep = "../../examples/goalpost/goalpost.accept.gw"
)

// dirBytes reads every file of dir, by name.
func dirBytes(t *testing.T, dir string) map[string]string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		m[e.Name()] = string(b)
	}
	return m
}

func costOf(t *testing.T, rep map[string]any) map[string]any {
	t.Helper()
	c, ok := rep["cost"].(map[string]any)
	if !ok {
		t.Fatalf("no cost in the report: %v", rep)
	}
	return c
}

func TestBuildMaxWorkBoundary(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	code, out, errOut := exec(t, "build", example, "--accept", exampleAccep, "--max-work", "8750000", "--out-dir", dir, "--json")
	if code != 0 {
		t.Fatalf("exit %d at the exact ceiling\n%s\n%s", code, out, errOut)
	}
	cost := costOf(t, oneJSON(t, out))
	if cost["total_work"] != float64(8750000) || cost["limit"] != float64(8750000) || len(cost["tickets"].([]any)) != 34 {
		t.Errorf("cost: %v", cost)
	}
	before := dirBytes(t, dir)
	if !strings.Contains(before["goalpost.build.json"], `"total_work": 8750000`) {
		t.Errorf("the record holds no receipt:\n%s", before["goalpost.build.json"])
	}

	// One unit below: refused at publish, and the old set stays exactly as it was.
	code, out, _ = exec(t, "build", example, "--accept", exampleAccep, "--max-work=8749999", "--out-dir", dir, "--json")
	if code != 1 {
		t.Fatalf("exit %d at 8749999, want 1\n%s", code, out)
	}
	rep := oneJSON(t, out)
	if rep["ok"] != false || rep["written"] != false || !strings.Contains(out, "GW-LIMIT") {
		t.Errorf("report: %s", out)
	}
	ref := costOf(t, rep)["refused"].(map[string]any)
	if ref["ticket"] != "publish" || ref["requested"] != float64(50000) || ref["remaining"] != float64(49999) {
		t.Errorf("refusal: %v", ref)
	}
	after := dirBytes(t, dir)
	if len(after) != len(before) {
		t.Errorf("the folder held %d files, now %d", len(before), len(after))
	}
	for name, b := range before {
		if after[name] != b {
			t.Errorf("%s changed", name)
		}
	}

	// A refused build into a new folder creates nothing.
	fresh := filepath.Join(t.TempDir(), "never")
	if code, _, _ := exec(t, "build", example, "--accept", exampleAccep, "--max-work", "8749999", "--out-dir", fresh); code != 1 {
		t.Errorf("exit %d", code)
	}
	if _, err := os.Stat(fresh); err == nil {
		t.Error("the refused build created its folder")
	}
}

func TestBuildMaxWorkText(t *testing.T) {
	code, _, errOut := exec(t, "build", example, "--max-work", "1000", "--out-dir", filepath.Join(t.TempDir(), "o"))
	if code != 1 || !strings.Contains(errOut, "GW-LIMIT") || !strings.Contains(errOut, "snapshot") {
		t.Errorf("exit %d\n%s", code, errOut)
	}
}

func TestBuildMaxWorkUsage(t *testing.T) {
	for _, v := range []string{"0", "-5", "many", "8,750,000"} {
		if code, _, errOut := exec(t, "build", example, "--max-work", v); code != 2 || !strings.Contains(errOut, "--max-work") {
			t.Errorf("--max-work %s: exit %d\n%s", v, code, errOut)
		}
	}
	if code, _, _ := exec(t, "build", example, "--accept", "no-such-file.gw", "--out-dir", filepath.Join(t.TempDir(), "o")); code != 2 {
		t.Errorf("a missing acceptance file: exit %d", code)
	}
}
