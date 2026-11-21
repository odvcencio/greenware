package compiler

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/resolve"
)

// The qualification audit of the weight solver (milestone M5, conformance
// section 8): the solved skin against the hand skin, joint by joint, measure
// by measure, clip by clip, and the held-out protocol that chooses the
// solver's parameters. This file defines what a regression is; the tests in
// solveprotocol_test.go run it and pin the result in testdata/solve.

const (
	armHandPath = "../examples/arm/arm.gw"
	// clipLibraryDir is where the football clip library will be written. Every
	// *.gw file in it is appended to the frozen athlete; the audit takes
	// whatever is there and needs no change when more clips appear.
	clipLibraryDir = "../examples/athlete/clips"
)

// ---- what a regression is --------------------------------------------------------

// A measure is regressed when the solved skin is worse than the hand skin by
// more than the measure's epsilon:
//
//	strain, strained, torn (smaller is better): solved > hand * 1.0 + eps
//	lowest (a floor, bigger is better):         solved < hand - eps
//
// The epsilons only absorb float noise and sub-visible differences: they are
// smaller than one edge of the smallest region (1/240 of its share) and than
// the 0.5 mm the earlier table used for the ground.
var measureEps = map[string]float64{
	"strain":   0.001,  // an edge length ratio (r or 1/r, 1 = unchanged)
	"strained": 0.0005, // a share of the region's edges outside 1/1.5..1.5
	"torn":     0.0005, // a share outside 1/4..3
	"lowest":   0.0001, // metres: 0.1 mm
}

// measureScale ranks regressions of different measures against each other: an
// excess of one scale counts as 1. It is used to choose parameters only, never
// to pass or fail a row.
var measureScale = map[string]float64{"strain": 0.1, "strained": 0.01, "torn": 0.01, "lowest": 0.001}

// measureOrder is the order the measures are written in.
var measureOrder = []string{"strain", "strained", "torn", "lowest"}

// groundContact says a region touches the floor: only such a region has a
// ground measure, because the band is for the lowest vertex of the model.
const groundContact = 0.05

// auditRow is one comparison: a measure of one joint region over one scope
// ("all" poses including the bind pose, or one clip).
type auditRow struct {
	Case    string  `json:"case"`
	Joint   string  `json:"joint"`
	Fold    string  `json:"fold"`
	Scope   string  `json:"scope"`
	Measure string  `json:"measure"`
	Hand    float64 `json:"hand"`
	Solved  float64 `json:"solved"`
	Eps     float64 `json:"eps"`
}

// excess is how far the solved value is past the allowed one; above 0 is a
// regression.
func (r auditRow) excess() float64 {
	if r.Measure == "lowest" {
		return (r.Hand - r.Eps) - r.Solved
	}
	return r.Solved - (r.Hand*1.0 + r.Eps)
}

func (r auditRow) regressed() bool { return r.excess() > 0 }

// score is the excess in units of the measure's scale, for ranking.
func (r auditRow) score() float64 { return r.excess() / measureScale[r.Measure] }

// foldOf names the fold of an athlete joint: A is the pelvis, chest, head and
// both arms, elbows and hands; B is the legs, knees, ankles and feet. The
// arm example is its own set, "arm", and is in every training set.
func foldOf(caseName, joint string) string {
	if caseName == "arm" {
		return "arm"
	}
	for _, p := range []string{"legl", "legr", "kneel", "kneer", "anklel", "ankler"} {
		if joint == p {
			return "B"
		}
	}
	return "A"
}

// compareAudits lists every comparison of two audits of one asset.
func compareAudits(caseName string, hand, solved *AuditReport) []auditRow {
	var rows []auditRow
	add := func(joint, scope, measure string, h, s float64) {
		rows = append(rows, auditRow{caseName, joint, foldOf(caseName, joint), scope, measure, h, s, measureEps[measure]})
	}
	for j := range hand.joints {
		h, s := hand.joints[j], solved.joints[j]
		if h.Edges == 0 {
			continue
		}
		add(h.Joint, "all", "strain", h.StrainMax, s.StrainMax)
		add(h.Joint, "all", "strained", h.Strained, s.Strained)
		add(h.Joint, "all", "torn", h.Torn, s.Torn)
		if h.Lowest < groundContact {
			add(h.Joint, "all", "lowest", h.Lowest, s.Lowest)
		}
		for c := range h.ByClip {
			hc, sc := h.ByClip[c], s.ByClip[c]
			add(h.Joint, hc.Clip, "strain", hc.StrainMax, sc.StrainMax)
			add(h.Joint, hc.Clip, "strained", hc.Strained, sc.Strained)
			add(h.Joint, hc.Clip, "torn", hc.Torn, sc.Torn)
			if hc.Lowest < groundContact {
				add(h.Joint, hc.Clip, "lowest", hc.Lowest, sc.Lowest)
			}
		}
	}
	return rows
}

// ---- the two assets ----------------------------------------------------------------

// auditPair is the hand audit and the solved audit of one asset.
type auditPair struct {
	name         string
	hand, solved *AuditReport
}

// widenCheck makes the build report its measures whatever they are: the
// default pass marks refuse a skin that fails them, and a refused build has no
// report to read.
func widenCheck(src string) string {
	src = strings.Replace(src, "ground = -5mm..20mm", "ground = -2m..2m", 1)
	src = strings.Replace(src, "bands = error", "bands = warning", 1)
	// The clip library makes the file larger than the athlete's budget; the
	// size is not what the audit measures.
	src = strings.Replace(src, "  file_size = 1MiB ", "  file_size = 8MiB ", 1)
	return strings.Replace(src, "torn = 1%", "torn = 100%", 1)
}

// armWithCheck adds a `check rig` block to the arm, which has none, and
// optionally swaps its solve step for solve.
func armWithCheck(solve string) func(string) string {
	return func(src string) string {
		if solve != "" {
			i := strings.Index(src, "  solve {")
			j := strings.Index(src[i:], "\n")
			if i < 0 || j < 0 {
				panic("the solved arm's solve step moved")
			}
			src = src[:i] + solve + src[i+j+1:]
		}
		k := strings.Index(src, "output ")
		if k < 0 {
			panic("the arm has no output")
		}
		return src[:k] + "check rig { ground = -2m..2m torn = 100% }\n\n" + src[k:]
	}
}

// auditOf builds the first output of the file at path, with its source edited,
// and returns its audit.
//
// The source is read from path and lowered as if it sat at lowerAs, which sets
// the project the imports are read in: the frozen athlete is read from
// testdata and lowered as the file of examples/athlete, where its imports
// point.
func auditOf(t testing.TB, path, lowerAs string, edit func(string) string, output string) *AuditReport {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	src := edit(string(b))
	f := lowerProjectSource(t, lowerAs, []byte(src))
	r, ds := resolve.EvaluateOutput(f, output)
	if r == nil {
		t.Fatalf("resolve %s: %v", path, ds)
	}
	op, ds, _ := buildOutput(f, r, output)
	if op == nil {
		t.Fatalf("%s does not build: %v", path, ds)
	}
	if op.Files[0].Audit == nil || len(op.Files[0].Audit.joints) == 0 {
		t.Fatalf("%s has no per-joint audit", path)
	}
	return op.Files[0].Audit
}

// clipLibrary returns the project files of the clip library in dir, as
// import paths from the athlete's directory, in name order; none when the
// directory is absent. The audit installs every one of them with `use_clips`.
func clipLibrary(dir string) []string {
	names, _ := filepath.Glob(filepath.Join(filepath.FromSlash(dir), "*.gw"))
	sort.Strings(names)
	var out []string
	for _, n := range names {
		out = append(out, "clips/"+filepath.Base(n))
	}
	return out
}

// removeClip cuts the declaration `clip name { ... }` out of a source.
func removeClip(src, name string) string {
	i := strings.Index(src, "\nclip "+name+" {")
	if i < 0 {
		panic("the frozen athlete has no clip " + name)
	}
	depth, k := 0, i+1
	for ; k < len(src); k++ {
		if src[k] == '{' {
			depth++
		} else if src[k] == '}' {
			depth--
			if depth == 0 {
				break
			}
		}
	}
	return src[:i] + src[k+1:]
}

// athleteWithClips edits the frozen athlete: widened pass marks, the clip
// library installed (the library carries `ready` and `run` itself, so the
// frozen athlete's own two are cut), and, when solve is not empty, the hand
// blends replaced.
func athleteWithClips(solve string, library []string) func(string) string {
	return func(src string) string {
		src = athleteVariant(solve)(src)
		if len(library) == 0 {
			return src
		}
		src = removeClip(removeClip(src, "ready"), "run")
		var uses, imports string
		for _, f := range library {
			q := "lib_" + strings.TrimSuffix(filepath.Base(f), ".gw")
			uses += "  use_clips { of = " + q + " }\n"
			imports += "import \"" + f + "\" as " + q + "\n"
		}
		src = strings.Replace(src, "  idle = ready ", uses+"  idle = ready ", 1)
		return strings.Replace(src, "import \"../helmet/helmet.gw\" as helmet\n", "import \"../helmet/helmet.gw\" as helmet\n"+imports, 1)
	}
}

// frozenAthletePath is the copy of the athlete the audit runs on; the file in
// examples/athlete is rewritten by another lane.
const frozenAthletePath = "../testdata/athlete-frozen/athlete.gw"

// athleteLowerAs is where the frozen athlete's source is lowered.
const athleteLowerAs = "../examples/athlete/athlete.gw"

// handAudits builds the two hand-skinned assets once.
func handAudits(t testing.TB, library []string) (arm, athlete *AuditReport) {
	t.Helper()
	arm = auditOf(t, armHandPath, armHandPath, armWithCheck(""), "arm_glb")
	athlete = auditOf(t, frozenAthletePath, athleteLowerAs, athleteWithClips("", library), "match")
	return arm, athlete
}

// solvedAudits builds the two solved assets with one parameter vector.
func solvedAudits(t testing.TB, th solveParams, library []string) (arm, athlete *AuditReport) {
	t.Helper()
	arm = auditOf(t, armSolvedPath, armSolvedPath, armWithCheck("  "+th.text()), "arm_solved_glb")
	athlete = auditOf(t, frozenAthletePath, athleteLowerAs, athleteWithClips("  "+th.text()+"\n", library), "match")
	return arm, athlete
}

// ---- parameters and their scores --------------------------------------------------

// solveParams is the part of a solve step the protocol varies. The other
// fields keep their defaults.
type solveParams struct {
	Own    float64 `json:"own"`
	Cutoff float64 `json:"cutoff"`
	Smooth float64 `json:"smooth"`
}

func (p solveParams) text() string {
	return fmt.Sprintf("solve { own = %v cutoff = %v smooth = %v }", p.Own, p.Cutoff, p.Smooth)
}

// worst is the largest score of the rows (the most regressed, as a number of
// scales; at most 0 when none regressed), and failed counts the regressed rows.
func worst(rows []auditRow, keep func(auditRow) bool) (w float64, failed int) {
	w = math.Inf(-1)
	for _, r := range rows {
		if !keep(r) {
			continue
		}
		w = math.Max(w, r.score())
		if r.regressed() {
			failed++
		}
	}
	return w, failed
}

func inFolds(folds ...string) func(auditRow) bool {
	return func(r auditRow) bool {
		for _, f := range folds {
			if r.Fold == f {
				return true
			}
		}
		return false
	}
}

// failing lists the regressed rows, one line each, in row order.
func failing(rows []auditRow, keep func(auditRow) bool) []string {
	var out []string
	for _, r := range rows {
		if keep(r) && r.regressed() {
			out = append(out, fmt.Sprintf("%s %s %s %s %.6g -> %.6g", r.Case, r.Joint, r.Scope, r.Measure, r.Hand, r.Solved))
		}
	}
	return out
}

// buildFrozenAthlete compiles the `match` output of the frozen athlete, lowered
// as the file of examples/athlete, with its own two clips. edit rewrites the
// source first.
func buildFrozenAthlete(t testing.TB, edit func(string) string) (*OutputProduct, *gltf.Document) {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(frozenAthletePath))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if edit != nil {
		src = edit(src)
	}
	f := lowerProjectSource(t, athleteLowerAs, []byte(src))
	r, ds := resolve.EvaluateOutput(f, "match")
	if r == nil {
		t.Fatalf("resolve: %v", ds)
	}
	op, ds, _ := buildOutput(f, r, "match")
	if op == nil {
		t.Fatalf("the frozen athlete does not build: %v", ds)
	}
	d, err := gltf.Decode(op.Files[0].GLB)
	if err != nil {
		t.Fatal(err)
	}
	return op, d
}
