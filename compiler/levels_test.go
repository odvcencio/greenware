package compiler

import (
	"encoding/json"
	"strings"
	"testing"
)

type levelsRecord struct {
	Outputs []struct {
		Name   string      `json:"name"`
		Levels []LevelInfo `json:"levels"`
	} `json:"outputs"`
}

func TestRecordCarriesLevelDistances(t *testing.T) {
	p := buildGoalPost(t)
	var rec levelsRecord
	if err := json.Unmarshal(p.Record(), &rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Outputs) != 2 {
		t.Fatalf("%d outputs", len(rec.Outputs))
	}
	f64 := func(x float64) *float64 { return &x }
	for _, o := range rec.Outputs {
		want := []LevelInfo{
			{Level: 0, File: "", DistanceM: nil, MaxErrorM: 0},
			{Level: 1, DistanceM: f64(40), MaxErrorM: 0.005},
			{Level: 2, DistanceM: f64(90), MaxErrorM: 0.03},
		}
		if len(o.Levels) != 3 {
			t.Fatalf("%s: %d levels", o.Name, len(o.Levels))
		}
		for i, w := range want {
			g := o.Levels[i]
			if g.Level != w.Level || g.MaxErrorM != w.MaxErrorM || (g.DistanceM == nil) != (w.DistanceM == nil) || (w.DistanceM != nil && *g.DistanceM != *w.DistanceM) {
				t.Errorf("%s level %d: %+v, want distance %v and max error %v", o.Name, i, g, w.DistanceM, w.MaxErrorM)
			}
		}
	}
	// The numbers match the files: the same file names and counts as the census.
	for oi, o := range p.Outputs {
		for i, f := range o.Files {
			g := rec.Outputs[oi].Levels[i]
			if g.File != f.Name || g.Triangles != f.Triangles || g.Draws != f.Draws {
				t.Errorf("%s: level row %+v does not match file %s (%d triangles, %d draws)", o.Name, g, f.Name, f.Triangles, f.Draws)
			}
		}
	}
	// Level 0 writes null, not a missing key and not zero.
	if !strings.Contains(string(p.Record()), `"distance_m": null`) {
		t.Error(`level 0 should write "distance_m": null`)
	}
}

func TestLevelDistancesDoNotChangeTheFiles(t *testing.T) {
	a := buildGoalPost(t)
	b, ds := Build(lowerSource(t, goalpostSource(t, "distance = 40m", "distance = 41m")), "")
	if b == nil {
		t.Fatal(ds)
	}
	for oi, o := range a.Outputs {
		for i, f := range o.Files {
			if f.SHA256 != b.Outputs[oi].Files[i].SHA256 {
				t.Errorf("%s changed when only a distance changed", f.Name)
			}
		}
	}
	var rec levelsRecord
	if err := json.Unmarshal(b.Record(), &rec); err != nil {
		t.Fatal(err)
	}
	if d := rec.Outputs[0].Levels[1].DistanceM; d == nil || *d != 41 {
		t.Errorf("distance %v, want 41", d)
	}
}

func TestManifestNoteSaysWhereTheDistancesAre(t *testing.T) {
	p := buildGoalPost(t)
	for _, n := range p.Unsupported {
		if strings.Contains(n, "manifest") {
			if !strings.Contains(n, "build record") || !strings.Contains(n, "layout") {
				t.Errorf("note %q", n)
			}
			return
		}
	}
	t.Error("no manifest note")
}
