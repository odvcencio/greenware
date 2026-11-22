package compiler

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"m31labs.dev/greenware/registry"
)

// The held-out protocol for the solver's parameters (see solveaudit_test.go for
// what a regression is).
//
//  1. A fixed grid of parameter vectors (own, cutoff, smooth); the other
//     fields keep their defaults.
//  2. Every vector is built on the arm example and on the frozen athlete, and
//     compared with the hand skin: every joint, every measure, over all poses
//     and over each clip alone.
//  3. Folds: A is the pelvis, chest, head and both arms, elbows and hands; B is
//     the legs, knees, ankles and feet; "arm" is the arm example, which is in
//     every training set.
//  4. A split chooses a vector on its training folds and is judged on the
//     folds it did not see. The choice is, in order: fewest regressed rows
//     after the worst excess is clamped at 0, then the most vertices blended
//     (the solver should smooth as much as the audit allows), then the first
//     in grid order. Splits: arm+A -> B, arm+B -> A, arm -> A+B.
//  5. The final vector is the split's choice with the smallest held-out worst
//     excess, then fewest held-out failures, then the first split in the list.
//  6. The verdict is that vector over all rows. M5 is qualified only if the
//     verdict has no failed joint, every split passed on its held-out folds,
//     and the athlete has the full body clip library.
//
// testdata/solve/protocol.json records the grid, the folds, the measures, every
// vector's score and the result. The test recomputes all of it and compares.

const (
	protocolPath = "../testdata/solve/protocol.json"
	// bodyClipsRequired is the size of the football body clip library that
	// conformance section 8 asks the solver to be audited on.
	bodyClipsRequired = 19
)

var (
	gridOwn    = []float64{16, 32, 64, 128}
	gridCutoff = []float64{0.02, 0.05, 0.1}
	gridSmooth = []float64{0.25, 0.5, 1, 2}
)

func protocolGrid() []solveParams {
	var g []solveParams
	for _, o := range gridOwn {
		for _, c := range gridCutoff {
			for _, s := range gridSmooth {
				g = append(g, solveParams{o, c, s})
			}
		}
	}
	return g
}

// blended counts, per fold, the vertices that carry two or more weights.
type blended struct{ Arm, A, B int }

func (b blended) in(folds []string) (n int) {
	for _, f := range folds {
		switch f {
		case "arm":
			n += b.Arm
		case "A":
			n += b.A
		case "B":
			n += b.B
		}
	}
	return n
}

func countBlended(caseName string, r *AuditReport) (b blended) {
	for v, in := range r.mesh.infl {
		if len(in) < 2 {
			continue
		}
		switch foldOf(caseName, r.mesh.joints[r.mesh.region[v]]) {
		case "arm":
			b.Arm++
		case "A":
			b.A++
		case "B":
			b.B++
		}
	}
	return b
}

// gridPoint is one evaluated parameter vector.
type gridPoint struct {
	Params  solveParams `json:"params"`
	Blended blended     `json:"blended"`
	Arm     foldScore   `json:"arm"`
	A       foldScore   `json:"A"`
	B       foldScore   `json:"B"`
	rows    []auditRow
}

// foldScore is the worst excess clamped at 0 (in scales) and the regressed
// rows of one fold.
type foldScore struct {
	Worst  float64 `json:"worst_excess"`
	Failed int     `json:"failed_rows"`
}

func scoreOf(rows []auditRow, folds ...string) foldScore {
	w, n := worst(rows, inFolds(folds...))
	return foldScore{round6(math.Max(w, 0)), n}
}

func round6(x float64) float64 { return math.Round(x*1e6) / 1e6 }

// evaluateGrid builds every vector once.
func evaluateGrid(t testing.TB, grid []solveParams, library []string) (points []gridPoint, handBlend blended) {
	t.Helper()
	armH, athH := handAudits(t, library)
	handBlend = countBlended("arm", armH)
	hb := countBlended("athlete", athH)
	handBlend.A, handBlend.B = hb.A, hb.B
	for _, th := range grid {
		armS, athS := solvedAudits(t, th, library)
		rows := append(compareAudits("arm", armH, armS), compareAudits("athlete", athH, athS)...)
		sb := countBlended("athlete", athS)
		sb.Arm = countBlended("arm", armS).Arm
		points = append(points, gridPoint{
			Params: th, Blended: sb, rows: rows,
			Arm: scoreOf(rows, "arm"), A: scoreOf(rows, "A"), B: scoreOf(rows, "B"),
		})
	}
	return points, handBlend
}

// choose picks the vector for a training set: the fewest failed rows after
// the clamped worst excess, then the most blended, then the grid order.
func choose(points []gridPoint, train []string) int {
	best := 0
	key := func(i int) (float64, int, int) {
		s := scoreOf(points[i].rows, train...)
		return s.Worst, s.Failed, -points[i].Blended.in(train)
	}
	bw, bf, bb := key(0)
	for i := 1; i < len(points); i++ {
		w, f, b := key(i)
		if w < bw || (w == bw && (f < bf || (f == bf && b < bb))) {
			best, bw, bf, bb = i, w, f, b
		}
	}
	return best
}

// split is one train-and-test round of the protocol.
type split struct {
	Name     string      `json:"name"`
	Train    []string    `json:"train"`
	Test     []string    `json:"held_out"`
	Chosen   solveParams `json:"chosen"`
	TrainSc  foldScore   `json:"train_score"`
	TestSc   foldScore   `json:"held_out_score"`
	Failing  []string    `json:"held_out_failing,omitempty"`
	Pass     bool        `json:"held_out_pass"`
	chosenAt int
}

func runSplit(points []gridPoint, name string, train, test []string) split {
	i := choose(points, train)
	ts := scoreOf(points[i].rows, test...)
	return split{
		Name: name, Train: train, Test: test, Chosen: points[i].Params,
		TrainSc: scoreOf(points[i].rows, train...), TestSc: ts,
		Failing: failing(points[i].rows, inFolds(test...)), Pass: ts.Failed == 0, chosenAt: i,
	}
}

// jointVerdict is the pass or fail of one joint region of one asset.
type jointVerdict struct {
	Case    string   `json:"case"`
	Joint   string   `json:"joint"`
	Fold    string   `json:"fold"`
	Pass    bool     `json:"pass"`
	Failing []string `json:"failing,omitempty"`
}

// solveVerdict is the qualification verdict of one parameter vector.
type solveVerdict struct {
	Schema    string         `json:"schema"`
	Params    solveParams    `json:"params"`
	Clips     []string       `json:"clips"`
	Rows      int            `json:"rows"`
	Joints    []jointVerdict `json:"joints"`
	Pass      bool           `json:"pass"`
	Qualified bool           `json:"m5_qualified"`
	Reasons   []string       `json:"not_qualified_because,omitempty"`
}

func judge(rows []auditRow, th solveParams, clips []string, splits []split) solveVerdict {
	v := solveVerdict{Schema: "greenware.solve-verdict/1", Params: th, Clips: clips, Rows: len(rows), Pass: true}
	idx := map[string]int{}
	for _, r := range rows {
		k := r.Case + "/" + r.Joint
		i, ok := idx[k]
		if !ok {
			i = len(v.Joints)
			idx[k] = i
			v.Joints = append(v.Joints, jointVerdict{Case: r.Case, Joint: r.Joint, Fold: r.Fold, Pass: true})
		}
		if r.regressed() {
			v.Joints[i].Pass = false
			v.Joints[i].Failing = append(v.Joints[i].Failing, fmt.Sprintf("%s %s %.6g -> %.6g", r.Scope, r.Measure, r.Hand, r.Solved))
			v.Pass = false
		}
	}
	if !v.Pass {
		v.Reasons = append(v.Reasons, "a joint regressed on a measure")
	}
	for _, s := range splits {
		if !s.Pass {
			v.Reasons = append(v.Reasons, "held-out failure: "+s.Name)
		}
	}
	if len(clips) < bodyClipsRequired {
		v.Reasons = append(v.Reasons, fmt.Sprintf("audited on %d body clips, conformance asks for %d", len(clips), bodyClipsRequired))
	}
	v.Qualified = len(v.Reasons) == 0
	return v
}

// protocolResult is everything the protocol computes.
type protocolResult struct {
	Grid     []gridPoint
	HandBlnd blended
	Splits   []split
	Final    int // index of the split whose choice is the final vector
	Verdict  solveVerdict
	Rows     []auditRow
}

func runProtocol(t testing.TB, library []string) protocolResult {
	t.Helper()
	points, hb := evaluateGrid(t, protocolGrid(), library)
	res := protocolResult{Grid: points, HandBlnd: hb}
	res.Splits = []split{
		runSplit(points, "arm+A -> B", []string{"arm", "A"}, []string{"B"}),
		runSplit(points, "arm+B -> A", []string{"arm", "B"}, []string{"A"}),
		runSplit(points, "arm -> A+B", []string{"arm"}, []string{"A", "B"}),
	}
	for i, s := range res.Splits {
		f := res.Splits[res.Final]
		if i > 0 && (s.TestSc.Worst < f.TestSc.Worst || (s.TestSc.Worst == f.TestSc.Worst && s.TestSc.Failed < f.TestSc.Failed)) {
			res.Final = i
		}
	}
	fin := points[res.Splits[res.Final].chosenAt]
	res.Rows = fin.rows
	res.Verdict = judge(fin.rows, fin.Params, athleteClips(fin.rows), res.Splits)
	return res
}

// athleteClips lists the clips the athlete rows were measured over, in order of
// first appearance, without "all".
func athleteClips(rows []auditRow) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Case == "athlete" && r.Scope != "all" && !seen[r.Scope] {
			seen[r.Scope] = true
			out = append(out, r.Scope)
		}
	}
	return out
}

// ---- the record --------------------------------------------------------------------

func compact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// foldMembers lists the joints of each fold of the frozen athlete.
func foldMembers(rows []auditRow) map[string][]string {
	m := map[string][]string{}
	seen := map[string]bool{}
	for _, r := range rows {
		k := r.Case + r.Joint
		if !seen[k] {
			seen[k] = true
			m[r.Fold] = append(m[r.Fold], r.Joint)
		}
	}
	return m
}

// record renders the protocol as the text of testdata/solve/protocol.json. Rows
// and grid points are one per line so that a change reads as a diff.
func (r protocolResult) record() string {
	return r.recordHead() + recordFinal(r.Splits[r.Final].Name, r.Verdict, r.Rows)
}

func (r protocolResult) recordHead() string {
	var sb strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&sb, format+"\n", a...) }
	line("{")
	line(`  "schema": "greenware.solve-protocol/1",`)
	line(`  "note": "Regenerate with GW_SOLVE_PROTOCOL=write go test ./compiler -run TestSolveProtocolMatchesRecord. The default test rebuilds only the final vector and compares everything from final_from_split on. Values are rounded to 6 decimals.",`)
	line(`  "regression": %s,`, compact("a measure is regressed when solved > hand * 1.0 + eps (strain, strained, torn) or solved < hand - eps (lowest, a floor, only for a region whose hand lowest is under 0.05 m); judged for all poses and for each clip alone; a joint passes when none of its measures is regressed"))
	line(`  "measures": %s,`, compact(measureTable()))
	line(`  "grid_axes": %s,`, compact(map[string][]float64{"own": gridOwn, "cutoff": gridCutoff, "smooth": gridSmooth}))
	fm := foldMembers(r.Rows)
	line(`  "folds": %s,`, compact(map[string][]string{"A": fm["A"], "B": fm["B"], "arm": fm["arm"]}))
	line(`  "selection": %s,`, compact("fewest regressed rows after the worst excess is clamped at 0; then the most vertices blended on the training folds; then grid order. The final vector is the split choice with the smallest held-out worst excess, then fewest held-out failures, then split order."))
	line(`  "hand_blended_vertices": %s,`, compact(r.HandBlnd))
	line(`  "grid": [`)
	for i, g := range r.Grid {
		sep := ","
		if i == len(r.Grid)-1 {
			sep = ""
		}
		line("    %s%s", compact(g), sep)
	}
	line(`  ],`)
	line(`  "splits": [`)
	for i, s := range r.Splits {
		sep := ","
		if i == len(r.Splits)-1 {
			sep = ""
		}
		line("    %s%s", compact(s), sep)
	}
	line(`  ],`)
	return sb.String()
}

// recordFinal renders what follows the grid: the split the final vector came
// from, the verdict and the table of every comparison.
func recordFinal(from string, v solveVerdict, rows []auditRow) string {
	var sb strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&sb, format+"\n", a...) }
	line(`  "final_from_split": %s,`, compact(from))
	line(`  "verdict": %s,`, compact(v))
	line(`  "table": [`)
	for i, row := range rows {
		row.Hand, row.Solved = round6(row.Hand), round6(row.Solved)
		sep := ","
		if i == len(rows)-1 {
			sep = ""
		}
		line("    %s%s", compact(row), sep)
	}
	line(`  ]`)
	line("}")
	return sb.String()
}

func measureTable() []map[string]any {
	var out []map[string]any
	better := map[string]string{"strain": "smaller", "strained": "smaller", "torn": "smaller", "lowest": "larger"}
	for _, m := range measureOrder {
		out = append(out, map[string]any{"name": m, "better": better[m], "eps": measureEps[m], "scale": measureScale[m]})
	}
	return out
}

// ---- the tests -----------------------------------------------------------------------

// recorded is the part of the record the default test needs.
type recorded struct {
	Splits []split      `json:"splits"`
	From   string       `json:"final_from_split"`
	Final  solveVerdict `json:"verdict"`
}

func readRecord(t testing.TB) (recorded, string) {
	t.Helper()
	b, err := os.ReadFile(protocolPath)
	if err != nil {
		t.Fatalf("no record: %v (GW_SOLVE_PROTOCOL=write writes it)", err)
	}
	var rec recorded
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	return rec, string(b)
}

func firstDifference(t testing.TB, want, got string) {
	t.Helper()
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gl) && i < len(wl); i++ {
		if gl[i] != wl[i] {
			t.Fatalf("the record differs from the recomputed protocol at its line %d:\nrecorded: %.600s\ncomputed: %.600s", i+1, wl[i], gl[i])
		}
	}
	t.Fatalf("the record has %d lines, the recomputed protocol %d", len(wl), len(gl))
}

// The whole protocol: 48 vectors, each built on the arm and on the athlete with
// every clip of the library. It takes minutes, so it runs on request:
//
//	GW_SOLVE_PROTOCOL=1 go test ./compiler -run TestSolveProtocolMatchesRecord
//
// GW_SOLVE_PROTOCOL=write rewrites testdata/solve/protocol.json first;
// GW_SOLVE_PROTOCOL=print writes the record to the log between <<<RECORD and
// >>>RECORD, for a machine whose files do not come back.
func TestSolveProtocolMatchesRecord(t *testing.T) {
	requireFullSuite(t)
	mode := os.Getenv("GW_SOLVE_PROTOCOL")
	if mode == "" {
		t.Skip("set GW_SOLVE_PROTOCOL=1 to rebuild the 48 vectors of the grid")
	}
	res := runProtocol(t, clipLibrary(clipLibraryDir))
	got := res.record()
	if mode == "print" {
		// For a machine that must not write the file: the record between marks.
		fmt.Printf("<<<RECORD\n%s>>>RECORD\n", got)
	}
	if mode == "write" {
		if err := os.MkdirAll("../testdata/solve", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(protocolPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", protocolPath)
	}
	if mode != "print" {
		_, want := readRecord(t)
		if want != got {
			firstDifference(t, want, got)
		}
	}
	for _, s := range res.Splits {
		t.Logf("split %-12s chose %s: train %+v, held out %+v pass=%v", s.Name, s.Chosen.text(), s.TrainSc, s.TestSc, s.Pass)
		for _, f := range s.Failing {
			t.Logf("   regressed on held-out: %s", f)
		}
	}
	b, _ := json.MarshalIndent(res.Verdict, "", "  ")
	t.Logf("%s", b)
}

// The final vector is rebuilt on the arm and on the athlete with the clip
// library, and everything the record says of it is compared: the verdict and
// every row of the table. A change to the solver or to the audit, or a clip
// added to the library, shows up here as a difference; rerun the protocol
// (see above) to record it. The verdict is printed as JSON.
func TestSolveFinalVectorMatchesRecord(t *testing.T) {
	requireFullSuite(t)
	rec, text := readRecord(t)
	fin := rec.Final.Params
	lib := clipLibrary(clipLibraryDir)
	armH, athH := handAudits(t, lib)
	armS, athS := solvedAudits(t, fin, lib)
	rows := append(compareAudits("arm", armH, armS), compareAudits("athlete", athH, athS)...)
	v := judge(rows, fin, athleteClips(rows), rec.Splits)
	b, _ := json.MarshalIndent(v, "", "  ")
	t.Logf("%s", b)
	got := recordFinal(rec.From, v, rows)
	i := strings.Index(text, `  "final_from_split"`)
	if i < 0 {
		t.Fatal("the record has no final part")
	}
	if text[i:] != got {
		firstDifference(t, text[i:], got)
	}
	// What the verdict says, as the milestone reads it: no joint may fail, the
	// splits may not fail, and 19 clips are needed before M5 is qualified.
	if v.Qualified && (!v.Pass || len(v.Clips) < bodyClipsRequired) {
		t.Errorf("qualified with pass=%v on %d clips", v.Pass, len(v.Clips))
	}
	for _, s := range rec.Splits {
		if !s.Pass && v.Qualified {
			t.Errorf("qualified although the split %s failed on held-out folds", s.Name)
		}
	}
}

// The solver's defaults are the vector the protocol chose, so that `solve { }`
// is the solver that was audited.
func TestSolveDefaultsAreTheProtocolChoice(t *testing.T) {
	rec, _ := readRecord(t)
	k, ok := registry.Lookup(registry.CtxRig, "solve")
	if !ok {
		t.Fatal("no solve kind")
	}
	for name, want := range map[string]float64{"own": rec.Final.Params.Own, "cutoff": rec.Final.Params.Cutoff, "smooth": rec.Final.Params.Smooth} {
		f, ok := k.Field(name)
		if !ok || f.Default == nil {
			t.Fatalf("no default for %s", name)
		}
		if f.Default.Num != want {
			t.Errorf("solve %s defaults to %v, the protocol chose %v", name, f.Default.Num, want)
		}
	}
}
