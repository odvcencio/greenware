package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildReportAndRecordCarryLevelDistances(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	code, out, errOut := exec(t, "build", goalpost, "--out-dir", dir, "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	rep := oneJSON(t, out)
	first := rep["outputs"].([]any)[0].(map[string]any)
	levels := first["levels"].([]any)
	if len(levels) != 3 {
		t.Fatalf("levels: %v", levels)
	}
	l0 := levels[0].(map[string]any)
	if v, present := l0["distance_m"]; !present || v != nil {
		t.Errorf("level 0 distance_m = %v (present %v), want null", v, present)
	}
	for i, want := range []float64{40, 90} {
		l := levels[i+1].(map[string]any)
		if l["distance_m"] != want || l["level"] != float64(i+1) {
			t.Errorf("level %d: %v", i+1, l)
		}
	}
	rec, err := os.ReadFile(filepath.Join(dir, "goalpost.build.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rec), `"distance_m": 40`) || !strings.Contains(string(rec), `"distance_m": 90`) {
		t.Errorf("the record lacks the distances:\n%s", rec)
	}
}
