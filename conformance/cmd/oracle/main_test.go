package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/greenware/conformance/internal/fixture"
)

func write(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func post() []byte {
	b := fixture.New()
	l := b.Mesh("l", fixture.Cylinder(-2.87, 0, 0.05, 0, 12.19, 12, 0, true))
	r := b.Mesh("r", fixture.Cylinder(2.87, 0, 0.05, 0, 12.19, 12, 0, true))
	root := b.Node("goalpost", -1, [3]float64{})
	b.Child(root, b.Node("upright_l", l, [3]float64{}))
	b.Child(root, b.Node("upright_r", r, [3]float64{}))
	b.Child(root, b.Node("socket.inside_l", -1, [3]float64{-2.82, 7, 0}))
	b.Scene(root)
	return b.Bytes()
}

func runArgs(args ...string) (int, []byte, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.Bytes(), errb.String()
}

func TestCleanFileExitsZeroWithLabelledReport(t *testing.T) {
	p := write(t, "post.glb", post())
	code, out, errs := runArgs("check", "--node", "goalpost", "--gap", "goalpost:x:0,7,0", p)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, errs, out)
	}
	var rep Report
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("report is not one JSON value: %v\n%s", err, out)
	}
	if !strings.Contains(rep.Validator, "not the Khronos validator") {
		t.Errorf("label missing: %q", rep.Validator)
	}
	if rep.Measurements == nil || rep.Measurements.DrawCalls != 2 || len(rep.Measurements.Sockets) != 1 {
		t.Fatalf("measurements: %+v", rep.Measurements)
	}
	if len(rep.Measurements.Gaps) != 1 || !rep.Measurements.Gaps[0].Result.OK() ||
		rep.Measurements.Gaps[0].Result.Distance < 5.639 || rep.Measurements.Gaps[0].Result.Distance > 5.641 {
		t.Errorf("gap: %+v", rep.Measurements.Gaps)
	}
	if rep.SHA256 == "" || rep.Findings == nil {
		t.Errorf("report fields: %+v", rep)
	}
}

func TestMalformedFileExitsOne(t *testing.T) {
	data := post()
	p := write(t, "cut.glb", data[:len(data)/2])
	code, out, _ := runArgs("check", p)
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	var rep Report
	if err := json.Unmarshal(out, &rep); err != nil || rep.Counts.Errors == 0 || rep.Measurements != nil || rep.Skipped == "" {
		t.Fatalf("report: %v %+v", err, rep)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	for _, args := range [][]string{
		{}, {"check"}, {"verify", "x.glb"}, {"check", "--bogus", "x.glb"},
		{"check", "--gap", "bad", "x.glb"}, {"check", "/no/such/file.glb"}, {"check", "a.glb", "b.glb"},
	} {
		if code, _, _ := runArgs(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
