package compiler

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"runtime"
	"strings"
	"testing"

	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/resolve"
)

const athletePath = "../examples/athlete/athlete.gw"

// buildAthlete compiles the athlete's `match` output. edit rewrites the source
// first. The output's own budget is the one the file declares: nothing is
// lifted.
func buildAthlete(t testing.TB, edit func(string) string) (*OutputProduct, *gltf.Document) {
	t.Helper()
	requireFullSuite(t)
	b, err := os.ReadFile(athletePath)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if edit != nil {
		src = edit(src)
	}
	f := lowerProjectSource(t, athletePath, []byte(src))
	r, ds := resolve.EvaluateOutput(f, "match")
	if r == nil {
		t.Fatalf("resolve: %v", ds)
	}
	op, ds, _ := buildOutput(f, r, "match")
	if op == nil {
		t.Fatalf("the athlete does not build: %v", ds)
	}
	d, err := gltf.Decode(op.Files[0].GLB)
	if err != nil {
		t.Fatal(err)
	}
	return op, d
}

func TestAthleteRigNumbers(t *testing.T) {
	op, d := buildAthlete(t, nil)
	f := op.Files[0]
	t.Logf("athlete L0: %d joints, %d vertices, %d triangles, max influences %d, %d morph targets, %d animations, %d keys, %d bytes, %d draws",
		f.Joints, f.Vertices, f.Triangles, f.Influences, f.Morphs, f.Animations, f.AnimKeys, f.Size, f.Draws)
	g := op.Files[1]
	t.Logf("athlete L1: %d vertices, %d triangles, %d bytes", g.Vertices, g.Triangles, g.Size)
	if f.Joints != 15 || f.Influences != 2 || f.Morphs != 7 || f.Animations != 32 {
		t.Errorf("joints %d influences %d morphs %d animations %d", f.Joints, f.Influences, f.Morphs, f.Animations)
	}
	var order []string
	for _, j := range d.Skins[0].Joints {
		order = append(order, d.Nodes[j].Name)
	}
	want := "pelvis chest head arml elbowl handl legl kneel anklel armr elbowr handr legr kneer ankler"
	if got := strings.Join(order, " "); got != want {
		t.Errorf("joint order %q", got)
	}
	names := strings.Join(d.Meshes[0].TargetNames, " ")
	if names != "mass belly pads chest-deep limb-lean waist-seat leg-power" {
		t.Errorf("targets %q", names)
	}
	var anims []string
	for _, a := range d.Animations {
		anims = append(anims, a.Name)
	}
	// The 27 body clips of clips/football.gw and clips/solved.gw, the four lookup channels and the `bind` clip of bind.gw, by name.
	if got := strings.Join(anims, " "); got != "backpedal bind block carry catch caught-l caught-r crossover-l crossover-r cut-l cut-r field-x field-z handoff heading hip-flip-l hip-flip-r juke kick kick-slide ready run shuffle-l shuffle-r sprint stance stance-height stumble tackle throw wrap-l wrap-r" {
		t.Errorf("animations %q", got)
	}
	// The four wrappers sit above the root node, outermost first.
	var chain []string
	for n := nodeNamed(t, d, "field lateral"); ; {
		chain = append(chain, d.Nodes[n].Name)
		if len(d.Nodes[n].Children) == 0 || d.Nodes[n].Name == "body" {
			break
		}
		n = d.Nodes[n].Children[0]
	}
	if got := strings.Join(chain, " > "); got != "field lateral > field depth > stance height > body heading > body" {
		t.Errorf("wrappers %q", got)
	}
	// Lookup clips: field-x maps 0 s to -64 yd and 128 s to 64 yd, in metres.
	for _, a := range d.Animations {
		if a.Name == "field-x" {
			o := a.Samplers[0].Output
			if math.Abs(float64(o[0])+64*0.9144) > 1e-4 || math.Abs(float64(o[3])-64*0.9144) > 1e-4 || a.Samplers[0].Input[1] != 128 {
				t.Errorf("field-x output %v input %v", o, a.Samplers[0].Input)
			}
		}
	}
	// The budget the file declares holds on the measured numbers.
	if f.Joints > 15 || f.Influences > 2 || f.Morphs > 7 {
		t.Error("over the declared budget")
	}
}

func TestAthleteWeightsAndBindPose(t *testing.T) {
	_, d := buildAthlete(t, nil)
	js, ws := skinAttrs(d)
	most := 0
	for v := range ws {
		var sum float64
		n := 0
		for l := 0; l < 4; l++ {
			sum += float64(ws[v][l])
			if ws[v][l] > 0 {
				n++
			}
			if l > 0 && ws[v][l] > ws[v][l-1] {
				t.Fatalf("vertex %d weights %v are not sorted", v, ws[v])
			}
			if int(js[v][l]) >= 15 {
				t.Fatalf("vertex %d joint %d", v, js[v][l])
			}
		}
		if math.Abs(sum-1) > 1e-6 {
			t.Fatalf("vertex %d weights sum to %.9g", v, sum)
		}
		most = max(most, n)
	}
	if most > 2 {
		t.Errorf("%d influences", most)
	}
	// With every joint at its original rest and every weight at zero, the skinned mesh is the bind mesh.
	var ex struct {
		Greenware struct {
			RigBase struct {
				Rest []struct {
					Joint       string
					Translation [3]float64
					Rotation    *[4]float64
				}
			} `json:"rig_base"`
		}
	}
	if err := json.Unmarshal(d.Extras, &ex); err != nil {
		t.Fatal(err)
	}
	cp := *d
	cp.Nodes = append([]gltf.Node(nil), d.Nodes...)
	for _, r := range ex.Greenware.RigBase.Rest {
		i := nodeNamed(t, d, r.Joint)
		tr := [3]float32{float32(r.Translation[0]), float32(r.Translation[1]), float32(r.Translation[2])}
		cp.Nodes[i].Translation, cp.Nodes[i].Rotation = &tr, nil
		if r.Rotation != nil {
			q := [4]float32{float32(r.Rotation[0]), float32(r.Rotation[1]), float32(r.Rotation[2]), float32(r.Rotation[3])}
			cp.Nodes[i].Rotation = &q
		}
	}
	zero := make([]float64, 7)
	got := rigEval{&cp}.posed("", 0, zero)
	want := bindPositions(d)
	worst := 0.0
	for i := range got {
		worst = math.Max(worst, dist3(got[i], want[i]))
	}
	if worst > 1e-5 {
		t.Errorf("the bind pose is reproduced to %g m", worst)
	}
	t.Logf("athlete bind pose reproduced to %.3g m over %d vertices", worst, len(got))
}

// At the default point the `ready` pose puts the lowest vertex of the model on
// the ground within the audit's band of -5 mm to +20 mm.
func TestAthleteReadyStandsOnTheGround(t *testing.T) {
	_, d := buildAthlete(t, nil)
	ev := rigEval{d}
	for _, tm := range []float64{0, 0.45, 0.9} {
		lo := 1e9
		for _, v := range ev.posed("ready", tm, nil) {
			lo = math.Min(lo, v[1])
		}
		if lo < -0.005 || lo > 0.020 {
			t.Errorf("ready at %g s: the lowest vertex is %.4f m, outside -5 mm..20 mm", tm, lo)
		}
	}
	// The unplayed file shows the bind pose (`idle = bind`, as the game's bake
	// writes its nodes): the soles of the bind shape sit 0.035 yd, 32 mm, above
	// the ground.
	lo := 1e9
	for _, v := range ev.posed("", 0, nil) {
		lo = math.Min(lo, v[1])
	}
	if lo < 0.030 || lo > 0.034 {
		t.Errorf("display pose: lowest vertex %.4f m, want the bind shape's 0.032 m", lo)
	}
}

// The combined fixture of language reference 5.9: height 1.08. The root's up
// value is 0.969486 yd x 1.08 = 1.047045 yd with no added root offset, though
// the `tall` morph is at weight 1. The athlete in examples/ no longer has a
// height parameter (the game scales a player as a whole model, and its bake has
// no tall or short target), so this fixture runs on the frozen copy of the first
// port, testdata/athlete-frozen (buildFrozenAthlete, in solveaudit_test.go).
func TestFrozenAthleteRootScaleAtHeight108(t *testing.T) {
	_, d := buildFrozenAthlete(t, func(s string) string {
		return strings.Replace(s, "param height : ratio = 1 in", "param height : ratio = 1.08 in", 1)
	})
	if w := d.Meshes[0].Weights; math.Abs(float64(w[3])-1) > 1e-6 || w[4] != 0 {
		t.Fatalf("tall/short weights %v %v", w[3], w[4])
	}
	pelvis := d.Nodes[nodeNamed(t, d, "pelvis")]
	want := 0.969486 * 1.08 * 0.9144
	if pelvis.Translation == nil || math.Abs(float64(pelvis.Translation[1])-want) > 2e-6 {
		t.Errorf("pelvis at %v, want y = %.6f m (1.047045 yd)", pelvis.Translation, want)
	}
	// The head follows the stretch: the inverse bind of the pelvis moves by the root's own offset.
	var ex struct {
		Greenware struct {
			Runtime struct {
				Params []struct {
					Name     string
					Bindings []struct {
						Kind, Joint, Morph string
						Delta              [3]float64
					}
				}
			}
		}
	}
	if err := json.Unmarshal(d.Extras, &ex); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range ex.Greenware.Runtime.Params {
		for _, b := range p.Bindings {
			if b.Kind == "joint_offset" && b.Morph == "tall" && b.Joint == "pelvis" {
				found = true
				// from 0 to 1.62 yd by 8 %: the root at 1 yd moves 0.08 yd.
				if math.Abs(b.Delta[1]-0.08*1*0.9144) > 1e-5 {
					t.Errorf("pelvis offset %v", b.Delta)
				}
			}
		}
	}
	if !found {
		t.Error("the tall morph records no offset for the pelvis")
	}
	// Publish the lowest vertex of each foot at ready key 0 (the spec asks for the measurement).
	ev := rigEval{d}
	posed := ev.posed("ready", 0, nil)
	lowL, lowR := 1e9, 1e9
	for _, v := range posed {
		if v[0] > 0 {
			lowL = math.Min(lowL, v[1])
		} else {
			lowR = math.Min(lowR, v[1])
		}
	}
	t.Logf("height 1.08, ready key 0: pelvis y %.6f m, lowest left-side vertex %.4f m, right-side %.4f m", pelvis.Translation[1], lowL, lowR)
}

func TestAthleteIsDeterministic(t *testing.T) {
	a, _ := buildAthlete(t, nil)
	b, _ := buildAthlete(t, nil)
	for i := range a.Files {
		if !bytes.Equal(a.Files[i].GLB, b.Files[i].GLB) {
			t.Errorf("level %d differs between two builds", i)
		}
	}
}

// The digests of the athlete's two levels are pinned in a candidate file, as
// the arm's are. The same test runs on amd64 and arm64.
func TestAthleteDigestsMatchCandidate(t *testing.T) {
	op, _ := buildAthlete(t, nil)
	var got []string
	for _, f := range op.Files {
		got = append(got, f.Name+" "+f.SHA256)
	}
	b, err := os.ReadFile("../testdata/compiler/athlete.candidate.sha256")
	if err != nil {
		t.Fatalf("no candidate digests: %v\n%s", err, strings.Join(got, "\n"))
	}
	if want := strings.TrimSpace(string(b)); strings.Join(got, "\n") != want {
		t.Errorf("athlete digests on %s:\n%s\nthe candidate says:\n%s", runtimeName(), strings.Join(got, "\n"), want)
	}
}

func runtimeName() string { return runtime.GOOS + "/" + runtime.GOARCH }

// All six athlete files, with the helmet baked into home and away, are pinned
// in a candidate file. The same test runs on amd64 and arm64.
func TestAthleteAllDigestsMatchCandidate(t *testing.T) {
	p := athleteAll(t)
	var got []string
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			got = append(got, f.Name+" "+f.SHA256)
		}
	}
	b, err := os.ReadFile("../testdata/compiler/athlete-all.candidate.sha256")
	if err != nil {
		t.Fatalf("no candidate digests: %v\n%s", err, strings.Join(got, "\n"))
	}
	if want := strings.TrimSpace(string(b)); strings.Join(got, "\n") != want {
		t.Errorf("athlete digests on %s:\n%s\nthe candidate says:\n%s", runtimeName(), strings.Join(got, "\n"), want)
	}
}
