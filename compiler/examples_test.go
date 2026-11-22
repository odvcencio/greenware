package compiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/greenware/accept"
	"m31labs.dev/greenware/gltf"
)

const (
	examplePath    = "../examples/goalpost/goalpost.gw"
	acceptPath     = "../examples/goalpost/goalpost.accept.gw"
	movedPath      = "../testdata/compiler/goalpost-moved-upright.gw"
	exampleGolden  = "../testdata/compiler/examples-goalpost.candidate.sha256"
	gapExpect      = "uprights' inside faces 18 ft 6 in apart"
	socketsExpect  = "the rule anchors agree"
	closedExpect   = "no holes in the painted frame"
	outlineExpect  = "front outline"
	outlineExpects = 6 // one outline expect, at three levels, for two outputs
)

// buildInto compiles a source file and writes its GLBs to a fresh folder.
func buildInto(t testing.TB, path string) (*Product, string) {
	t.Helper()
	p, ds := Build(loadFile(t, path), "")
	if p == nil {
		t.Fatalf("build %s failed: %v", path, ds)
	}
	dir := t.TempDir()
	if err := Write(p, dir); err != nil {
		t.Fatal(err)
	}
	return p, dir
}

// runAccept runs the acceptance file with the same entry point as the command.
func runAccept(t testing.TB, src, dir string) (int, *accept.Report) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := accept.Command([]string{src, acceptPath, "--glb-dir", dir, "--json"}, &out, &errOut)
	var rep accept.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("exit %d, not a report: %v\n%s\n%s", code, err, out.String(), errOut.String())
	}
	return code, &rep
}

func logTable(t testing.TB, rep *accept.Report) {
	for _, l := range rep.Levels {
		for _, e := range l.Expects {
			var ms []string
			for _, m := range e.Measures {
				ms = append(ms, m.Name+"="+m.Value)
			}
			t.Logf("%-9s lod%d %-11s %-48s %s", l.Output, l.LOD, e.Verdict, e.Message, strings.Join(ms, "; "))
		}
	}
}

func TestExampleGoalPostAcceptance(t *testing.T) {
	_, dir := buildInto(t, examplePath)
	code, rep := runAccept(t, examplePath, dir)
	logTable(t, rep)
	if code != accept.ExitPass {
		t.Errorf("exit %d, want %d", code, accept.ExitPass)
	}
	outlines, checks := 0, 0
	for _, l := range rep.Levels {
		if l.Problem != "" {
			t.Errorf("%s lod %d: %s", l.Output, l.LOD, l.Problem)
		}
		for _, e := range l.Expects {
			checks++
			if e.Message == outlineExpect {
				outlines++
			}
			if e.Verdict != accept.Pass {
				t.Errorf("%s lod %d: %q is %s: %s", l.Output, l.LOD, e.Message, e.Verdict, e.Detail)
			}
		}
	}
	if checks != 60 || outlines != outlineExpects {
		t.Errorf("got %d checks with %d outlines, want 60 and %d", checks, outlines, outlineExpects)
	}
}

// Move the uprights and caps 12 cm outward, past the reference tolerance.
// Leave the sockets at their authored positions.
func TestApprovedGoalPostOutlineRejectsMovedUprights(t *testing.T) {
	source, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatal(err)
	}
	const original = "at = (half_span, crossbar_height +"
	if got := strings.Count(string(source), original); got != 3 {
		t.Fatalf("found %d upright placements, want 3", got)
	}
	changed := strings.Replace(string(source), original, "at = (half_span + 0.12m, crossbar_height +", 2)
	path := filepath.Join(t.TempDir(), "moved.gw")
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	_, dir := buildInto(t, path)
	code, rep := runAccept(t, path, dir)
	logTable(t, rep)
	if code != accept.ExitFail {
		t.Errorf("exit %d, want %d", code, accept.ExitFail)
	}
	outlines := 0
	for _, l := range rep.Levels {
		for _, e := range l.Expects {
			if e.Message == outlineExpect {
				outlines++
				if e.Verdict != accept.Fail {
					t.Errorf("%s lod %d: outline is %s, want fail", l.Output, l.LOD, e.Verdict)
				}
			}
		}
	}
	if outlines != outlineExpects {
		t.Errorf("got %d outlines, want %d", outlines, outlineExpects)
	}
}

func TestOriginalGoalPostFailsClosed(t *testing.T) {
	_, dir := buildInto(t, goalpostPath)
	code, rep := runAccept(t, goalpostPath, dir)
	logTable(t, rep)
	if code != accept.ExitFail {
		t.Errorf("exit %d, want 1", code)
	}
	failed := 0
	for _, l := range rep.Levels {
		for _, e := range l.Expects {
			if e.Message == closedExpect {
				if e.Verdict != accept.Fail {
					t.Errorf("%s lod %d: closed is %s, want fail", l.Output, l.LOD, e.Verdict)
				}
				failed++
			}
		}
	}
	if failed != 6 {
		t.Errorf("closed was evaluated %d times, want 6", failed)
	}
}

// The control moves the uprights 2 cm outward and leaves the sockets. The mesh
// check must notice what the anchor check cannot.
func TestMovedUprightIsCaught(t *testing.T) {
	_, dir := buildInto(t, movedPath)
	code, rep := runAccept(t, movedPath, dir)
	logTable(t, rep)
	if code != accept.ExitFail {
		t.Errorf("exit %d, want 1", code)
	}
	var gaps int
	for _, l := range rep.Levels {
		for _, e := range l.Expects {
			switch e.Message {
			case gapExpect:
				gaps++
				if e.Verdict != accept.Fail {
					t.Errorf("%s lod %d: gap is %s, want fail", l.Output, l.LOD, e.Verdict)
				}
			case socketsExpect:
				if e.Verdict != accept.Pass {
					t.Errorf("%s lod %d: sockets expect is %s, want pass", l.Output, l.LOD, e.Verdict)
				}
			}
		}
	}
	if gaps != 6 {
		t.Errorf("gap evaluated %d times, want 6", gaps)
	}
}

// lodErrorOf reads extras.greenware.lod_error from a built level.
func lodErrorOf(t testing.TB, f *File) float64 {
	t.Helper()
	doc, err := gltf.Decode(f.GLB)
	if err != nil {
		t.Fatal(err)
	}
	var ex struct {
		Greenware struct {
			LODError *float64 `json:"lod_error"`
		} `json:"greenware"`
	}
	if err := json.Unmarshal(doc.Extras, &ex); err != nil || ex.Greenware.LODError == nil {
		t.Fatalf("%s has no extras.greenware.lod_error: %v", f.Name, err)
	}
	return *ex.Greenware.LODError
}

func TestLODErrorIsRecorded(t *testing.T) {
	p, _ := buildInto(t, examplePath)
	maxErr := []float64{0, 0.005, 0.030} // the goal post's lod 1 and 2 max_error
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			got := lodErrorOf(t, f)
			t.Logf("%s lod %d lod_error %.9g m", f.Name, f.Level, got)
			if f.Level == 0 {
				// plate radius 0.35 m, 24 segments: 0.35 * (1 - cos(pi/24)).
				want := 0.35 * (1 - math.Cos(math.Pi/24))
				if math.Abs(got-want) > 1e-12 {
					t.Errorf("%s: lod_error %.9g, want %.9g", f.Name, got, want)
				}
				if math.Abs(got-0.00299) > 5e-6 {
					t.Errorf("%s: lod_error %.9g is not about 2.99 mm", f.Name, got)
				}
				continue
			}
			if got <= 0 || got > maxErr[f.Level]*(1+1e-9) {
				t.Errorf("%s: lod_error %.9g is outside (0, %g]", f.Name, got, maxErr[f.Level])
			}
		}
	}
}

func exampleGoldenText(p *Product) string {
	var b strings.Builder
	b.WriteString("# sha256 bytes name: the six goal post files from examples/goalpost/goalpost.gw, post-upright/2, with lod_error recorded (candidate; a reviewer approves it)\n")
	var lines []string
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			lines = append(lines, fmt.Sprintf("%s %d %s\n", f.SHA256, len(f.GLB), f.Name))
		}
	}
	sortStrings(lines, func(l string) string { return l[strings.LastIndex(l, " ")+1:] })
	return b.String() + strings.Join(lines, "")
}

func TestExampleGoldenHashes(t *testing.T) {
	p, _ := buildInto(t, examplePath)
	got := exampleGoldenText(p)
	if os.Getenv("GREENWARE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(filepath.FromSlash(exampleGolden), []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(filepath.FromSlash(exampleGolden))
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("the example goal post files changed.\nrecorded:\n%s\nbuilt:\n%s", want, got)
	}
}

func TestExampleBuildIsRepeatable(t *testing.T) {
	a, _ := buildInto(t, examplePath)
	b, _ := buildInto(t, examplePath)
	if exampleGoldenText(a) != exampleGoldenText(b) {
		t.Error("two builds of the example differ")
	}
}

// TestProvisionalBaseline compares a fresh build with the digests a
// reviewer approved as a provisional guard. There is no update flag: a
// change in output bytes needs a new review.
func TestProvisionalBaseline(t *testing.T) {
	want, err := os.ReadFile("../testdata/compiler/approved/examples-goalpost.provisional.sha256")
	if err != nil {
		t.Fatal(err)
	}
	_, dir := buildInto(t, examplePath)
	got := map[string]string{}
	for _, f := range []string{"goalpost.glb", "goalpost-lod1.glb", "goalpost-lod2.glb", "goalpost-practice.glb", "goalpost-practice-lod1.glb", "goalpost-practice-lod2.glb"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		got[f] = hex.EncodeToString(sum[:]) + " " + strconv.Itoa(len(b))
	}
	n := 0
	for _, line := range strings.Split(string(want), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fs := strings.Fields(line)
		if len(fs) != 3 {
			t.Fatalf("bad baseline line %q", line)
		}
		n++
		if g := got[fs[2]]; g != fs[0]+" "+fs[1] {
			t.Errorf("%s: built %s, approved %s %s", fs[2], g, fs[0], fs[1])
		}
	}
	if n != 6 {
		t.Fatalf("baseline lists %d files, want 6", n)
	}
}
