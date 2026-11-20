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

// rigged is the two-joint strip with a 90 degree bend clip on j1 and a morph target.
func rigged(t *testing.T) []byte {
	t.Helper()
	b, ids := fixture.NewStrip(fixture.Strip{Morph: true, MorphNames: []string{"lift"}})
	var flat []float32
	for _, q := range [][4]float32{{0, 0, 0, 1}, fixture.Quat90Z} {
		flat = append(flat, q[:]...)
	}
	b.Animation("bend", []fixture.Sampler{{Input: b.Floats32("SCALAR", []float32{0, 1}), Output: b.Floats32("VEC4", flat)}},
		[]fixture.Channel{{Sampler: 0, Node: ids.J1, Path: "rotation"}})
	return b.Bytes()
}

func TestCheckReportsTheRig(t *testing.T) {
	p := write(t, "rig.glb", rigged(t))
	code, out, errs := runArgs("check", p)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, errs, out)
	}
	var rep Report
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatal(err)
	}
	r := rep.Rig
	if r == nil || r.JointCount != 2 || len(r.Skins) != 1 || len(r.Animations) != 1 || r.Animations[0].Name != "bend" ||
		r.Animations[0].Duration != 1 || r.Animations[0].Keys != 2 || len(r.Morphs) != 1 || r.Morphs[0].Names[0] != "lift" {
		t.Fatalf("rig: %+v", r)
	}
	if !strings.Contains(string(out), `"rig"`) || !strings.Contains(string(out), `"joint_count"`) {
		t.Fatalf("the JSON has no rig section:\n%s", out)
	}
}

func TestCheckReportsRigFindings(t *testing.T) {
	b, _ := fixture.NewStrip(fixture.Strip{Weights: [][4]float32{{1}, {1}, {0.5, 0.4}, {0.5, 0.5}, {1}, {1}}})
	p := write(t, "bad.glb", b.Bytes())
	code, out, _ := runArgs("check", p)
	var rep Report
	if err := json.Unmarshal(out, &rep); err != nil || code != 1 || len(rep.Findings) != 1 || rep.Findings[0].Code != "WEIGHTS-SUM" || rep.Rig != nil {
		t.Fatalf("exit %d, %v, %+v", code, err, rep)
	}
}

func TestPoseCommand(t *testing.T) {
	p := write(t, "rig.glb", rigged(t))
	code, out, errs := runArgs("pose", p, "--clip", "bend", "--time", "1", "--distance", "j0:j1")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, errs, out)
	}
	var rep PoseReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("not one JSON value: %v\n%s", err, out)
	}
	if rep.Bounds == nil || rep.Lowest == nil || len(rep.Joints) != 2 || len(rep.Distances) != 1 {
		t.Fatalf("report: %+v", rep)
	}
	near := func(what string, got, want float64) {
		if d := got - want; d > 1e-6 || d < -1e-6 {
			t.Errorf("%s = %v, want %v", what, got, want)
		}
	}
	// Bent 90 degrees at j1: vertices reach x = -1; y spans 0 to 1.5; j1 stays at (0,1,0).
	near("min x", rep.Bounds.Min[0], -1)
	near("max y", rep.Bounds.Max[1], 1.5)
	near("lowest y", rep.Lowest.Y, 0)
	near("j1.y", rep.Joints[1].Position[1], 1)
	near("distance", rep.Distances[0].Distance, 1)

	// --node limits the joint list; no clip is the rest pose
	code, out, _ = runArgs("pose", p, "--node", "j1")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	rep = PoseReport{}
	if err := json.Unmarshal(out, &rep); err != nil || len(rep.Joints) != 1 || rep.Joints[0].Name != "j1" || rep.Clip != "" {
		t.Fatalf("%v %+v", err, rep)
	}
	near("rest max y", rep.Bounds.Max[1], 2)
}

func TestPoseUsageErrors(t *testing.T) {
	p := write(t, "rig.glb", rigged(t))
	for _, args := range [][]string{
		{"pose"}, {"pose", p, "--clip", "nope"}, {"pose", p, "--time", "x"}, {"pose", p, "--bogus"},
		{"pose", p, "--node", "ghost"}, {"pose", p, "--distance", "j0"}, {"pose", p, "--distance", "j0:ghost"},
		{"pose", "/no/such.glb"}, {"pose", p, "b.glb"},
	} {
		if code, _, _ := runArgs(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	// an invalid file is reported with exit 1, not posed
	bad, _ := fixture.NewStrip(fixture.Strip{Joints: [][4]uint8{{0}, {0}, {0}, {0}, {0}, {9}}})
	code, out, _ := runArgs("pose", write(t, "bad.glb", bad.Bytes()))
	if code != 1 || !strings.Contains(string(out), "SKIN-JOINT-INDEX") {
		t.Fatalf("exit %d: %s", code, out)
	}
}
