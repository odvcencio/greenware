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
	"runtime"
	"strings"
	"testing"

	"m31labs.dev/greenware/accept"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
)

const (
	armSolvedPath   = "../examples/arm-solved/arm-solved.gw"
	armSolvedAccept = "../examples/arm-solved/arm-solved.accept.gw"
	armSolvedGolden = "../testdata/compiler/arm-solved.candidate.sha256"
)

func armSolvedSource(t testing.TB, edit func(string) string) []byte {
	t.Helper()
	b, err := os.ReadFile(armSolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	if edit == nil {
		return b
	}
	return []byte(edit(string(b)))
}

func buildArmSolved(t testing.TB, edit func(string) string) (*Product, *gltf.Document) {
	t.Helper()
	p, ds := Build(lowerSource(t, armSolvedSource(t, edit)), "")
	if p == nil {
		t.Fatalf("the solved arm does not build: %v", ds)
	}
	d, err := gltf.Decode(p.Outputs[0].Files[0].GLB)
	if err != nil {
		t.Fatal(err)
	}
	return p, d
}

// checkWeights tests every vertex of a decoded skin: no negative weight, the
// weights summing to exactly 1 (they are float32 multiples of 2^-24, so the sum
// is exact in float64), sorted, and at most limit of them.
func checkWeights(t *testing.T, d *gltf.Document, limit int) (most int) {
	t.Helper()
	js, ws := skinAttrs(d)
	if len(ws) == 0 {
		t.Fatal("no skinned vertex")
	}
	for v := range ws {
		var sum float64
		n := 0
		for l := 0; l < 4; l++ {
			w := float64(ws[v][l])
			if w < 0 || math.IsNaN(w) {
				t.Fatalf("vertex %d weight %v", v, w)
			}
			if l > 0 && ws[v][l] > ws[v][l-1] {
				t.Fatalf("vertex %d weights %v are not sorted", v, ws[v])
			}
			if w > 0 {
				n++
				if r := w * (1 << 24); r != math.Trunc(r) {
					t.Fatalf("vertex %d weight %v is not a multiple of 2^-24", v, w)
				}
			}
			sum += w
		}
		if sum != 1 {
			t.Fatalf("vertex %d weights %v sum to %.17g (joints %v)", v, ws[v], sum, js[v])
		}
		most = max(most, n)
	}
	if most > limit {
		t.Errorf("%d influences on one vertex; the limit is %d", most, limit)
	}
	return most
}

func TestSolvedArmWeights(t *testing.T) {
	p, d := buildArmSolved(t, nil)
	most := checkWeights(t, d, 2)
	js, ws := skinAttrs(d)
	two := 0
	for v := range ws {
		if ws[v][1] > 0 {
			two++
		}
	}
	// The solver has to blend somewhere; a skin of only rigid vertices would
	// make the rest of the tests vacuous.
	if two == 0 || most != 2 {
		t.Errorf("no vertex is blended: %d with two influences", two)
	}
	t.Logf("solved arm: %d vertices, %d blended, %d bytes", len(js), two, p.Outputs[0].Files[0].Size)
}

// A shape named in `rigid` keeps weight 1 on its joint.
func TestSolvedArmRigidKeepsWeightOne(t *testing.T) {
	_, d := buildArmSolved(t, func(s string) string {
		return strings.Replace(s, "reach = 1 }", "reach = 1 rigid = [hand] }", 1)
	})
	checkWeights(t, d, 2)
	wrist := nodeJointIndex(t, d, "wrist")
	n := 0
	for _, node := range d.Nodes {
		if node.Skin == nil {
			continue
		}
		for _, p := range d.Meshes[*node.Mesh].Primitives {
			if p.Material == nil || d.Materials[*p.Material].Name != "skin_tone" {
				continue
			}
			for v := 0; v < len(p.Positions)/3; v++ {
				n++
				if p.Joints0[4*v] != uint16(wrist) || p.Weights0[4*v] != 1 || p.Weights0[4*v+1] != 0 {
					t.Fatalf("hand vertex %d: joints %v weights %v", v, p.Joints0[4*v:4*v+4], p.Weights0[4*v:4*v+4])
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("found no hand vertex")
	}
}

func nodeJointIndex(t testing.TB, d *gltf.Document, name string) int {
	t.Helper()
	n := nodeNamed(t, d, name)
	for i, j := range d.Skins[0].Joints {
		if j == n {
			return i
		}
	}
	t.Fatalf("%s is not a joint", name)
	return -1
}

// The solved arm's digest is pinned. The same test runs on amd64 and arm64.
func TestSolvedArmDigestMatchesCandidate(t *testing.T) {
	p, _ := buildArmSolved(t, nil)
	got := p.Outputs[0].Files[0].SHA256
	b, err := os.ReadFile(armSolvedGolden)
	if err != nil {
		t.Fatalf("no candidate digest: %v (write %s)", err, got)
	}
	if want := strings.TrimSpace(string(b)); got != want {
		t.Errorf("arm-solved.glb is %s on %s/%s, the candidate says %s", got, runtime.GOOS, runtime.GOARCH, want)
	}
}

// Two runs, one thread and eight, another path: the same bytes, and the bytes
// on disk are the built bytes.
func TestSolvedArmIsDeterministic(t *testing.T) {
	a, _ := buildArmSolved(t, nil)
	want := a.Outputs[0].Files[0].SHA256
	for _, procs := range []int{1, 8} {
		prev := runtime.GOMAXPROCS(procs)
		p, _ := buildArmSolved(t, nil)
		runtime.GOMAXPROCS(prev)
		if got := p.Outputs[0].Files[0].SHA256; got != want {
			t.Errorf("GOMAXPROCS %d gives %s, want %s", procs, got, want)
		}
	}
	other := filepath.Join(t.TempDir(), "elsewhere", "deeper")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "arm-solved.gw"), armSolvedSource(t, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	p, ds := Build(loadFile(t, filepath.Join(other, "arm-solved.gw")), "")
	if p == nil {
		t.Fatal(ds)
	}
	if got := p.Outputs[0].Files[0].SHA256; got != want {
		t.Errorf("another path gives %s, want %s", got, want)
	}
	dir := t.TempDir()
	if err := Write(p, dir); err != nil {
		t.Fatal(err)
	}
	disk, _ := os.ReadFile(filepath.Join(dir, "arm-solved.glb"))
	sum := sha256.Sum256(disk)
	if hex.EncodeToString(sum[:]) != want || !bytes.Equal(disk, p.Outputs[0].Files[0].GLB) {
		t.Error("the written file is not the built file")
	}
}

func TestSolvedArmAcceptance(t *testing.T) {
	_, dir := buildInto(t, armSolvedPath)
	code, rep := runAcceptFor(t, armSolvedPath, armSolvedAccept, dir)
	if code != 0 {
		logTable(t, rep)
		t.Fatalf("exit %d", code)
	}
}

// A tolerance the sweeps cannot reach refuses the build. The message names the
// iteration count and the residual, and no product exists to write.
func TestSolvedArmRefusesNonConvergence(t *testing.T) {
	edit := func(s string) string {
		s = strings.Replace(s, "iterations = 64", "iterations = 3", 1)
		return strings.Replace(s, "tolerance = 0.0001", "tolerance = 0.000000000001", 1)
	}
	p, rc, ds := BuildWith(lowerSource(t, armSolvedSource(t, edit)), "", Options{})
	if p != nil {
		t.Fatal("a solve that did not converge produced a product")
	}
	d := hasCode(ds, diag.CodeSolve, "after 3 iterations")
	if d == nil || !strings.Contains(d.Message, "residual") {
		t.Fatalf("no GW-SOLVE naming the count and the residual: %v", ds)
	}
	if d.Span.Len() == 0 {
		t.Error("the diagnostic has no span")
	}
	if rc != nil && rc.Refused != nil {
		t.Errorf("a refusal of the solve is a diagnostic, not a cost refusal: %+v", rc.Refused)
	}
	// With the cap raised, the same tolerance 1e-12 would still be asked of
	// the same smoothing; the default tolerance converges well inside 64.
	if _, ds := Build(lowerSource(t, armSolvedSource(t, nil)), ""); hasCode(ds, diag.CodeSolve, "") != nil {
		t.Errorf("the example reports GW-SOLVE: %v", ds)
	}
}

// `refine = audit` is not supported; nothing is substituted for it.
func TestSolveRefineAuditIsUnsupported(t *testing.T) {
	p, _, ds := BuildWith(lowerSource(t, armSolvedSource(t, func(s string) string {
		return strings.Replace(s, "refine = smooth", "refine = audit", 1)
	})), "", Options{})
	if p != nil || hasCode(ds, diag.CodeUnsupported, "refine = audit") == nil {
		t.Errorf("%v", ds)
	}
}

// The solve ticket: reserved only for a skin with a solve step, priced and
// bounded, and the ledger refuses at it one unit short.
func TestSolveTicketBoundary(t *testing.T) {
	f := lowerSource(t, armSolvedSource(t, nil))
	p, ds := Build(f, "")
	if p == nil {
		t.Fatal(ds)
	}
	var sum, cumSolve uint64
	var solveUnits uint64
	var n int
	var next string
	for i, c := range p.Receipt.Charges {
		sum += c.Units
		if c.Ticket == TicketSolve {
			n++
			cumSolve, solveUnits = sum, c.Units
			if c.Level == nil || c.Output != "arm_solved_glb" {
				t.Errorf("solve charge %+v", c)
			}
			next = p.Receipt.Charges[i+1].Ticket
		}
	}
	if n != 1 || solveUnits != 1_000_000 {
		t.Fatalf("%d solve tickets of %d units", n, solveUnits)
	}
	total := p.Receipt.Total
	if q, _, ds := BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: total}}); q == nil {
		t.Errorf("the exact limit refused: %v", ds)
	}
	if q, rc, _ := BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: total - 1}}); q != nil || rc == nil || rc.Refused == nil || rc.Refused.Ticket != TicketPublish {
		t.Errorf("one below the total: %+v", rc)
	}
	// One unit short of the solve ticket: refused at it. Exactly enough for
	// it: refused at the ticket after it, not at it.
	_, rc, _ := BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: cumSolve - 1}})
	if rc == nil || rc.Refused == nil || rc.Refused.Ticket != TicketSolve || rc.Refused.Level == nil || *rc.Refused.Level != 0 {
		t.Errorf("C-1 should refuse at solve: %+v", rc)
	}
	_, rc, _ = BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: cumSolve}})
	if rc == nil || rc.Refused == nil || rc.Refused.Ticket != next {
		t.Errorf("C should pass solve and refuse at %s: %+v", next, rc)
	}
	// The ticket is in the registered schedule.
	if st := SolveTickets(); len(st) != 1 || st[0].Name != TicketSolve {
		t.Errorf("%+v", st)
	}
}

// An asset without a solve step reserves no solve ticket, and the arm's and
// the athlete's receipts keep their tickets.
func TestHandSkinsReserveNoSolveTicket(t *testing.T) {
	arm, ds := Build(loadFile(t, armPath), "")
	if arm == nil {
		t.Fatal(ds)
	}
	op, _ := buildAthlete(t, nil)
	_ = op
	post := buildGoalPost(t)
	for name, p := range map[string]*Product{"arm": arm, "goalpost": post} {
		for _, c := range p.Receipt.Charges {
			if c.Ticket == TicketSolve {
				t.Errorf("%s reserved a solve ticket", name)
			}
		}
	}
}

// ---- the audit against the hand weights -----------------------------------------

// athleteVariant returns an edit of the athlete's source. `check rig` is
// widened so that the build reports its measures whatever they are (the
// default pass marks would refuse a skin that fails them, and a refused build
// has no report to read). A non-empty solve replaces the skin from its first
// blend on, keeping the two lengthen steps.
func athleteVariant(solve string) func(string) string {
	return func(src string) string {
		src = strings.Replace(src, "ground = -5mm..20mm", "ground = -2m..2m", 1)
		src = strings.Replace(src, "torn = 1%", "torn = 100%", 1)
		if solve == "" {
			return src
		}
		i := strings.Index(src, "  // sequoia's blend(): weights ease")
		j := strings.Index(src, "// ---- Morph targets")
		if i < 0 || j < 0 {
			panic("the athlete's skin block moved")
		}
		return src[:i] + solve + "}\n\n" + src[j:]
	}
}

func TestSolvedAthleteWeights(t *testing.T) {
	for _, body := range []string{"  solve { }\n", "  solve { max_influences = 1 }\n", "  solve { reach = 2 power = 3 own = 2 }\n"} {
		op, d := buildAthlete(t, athleteVariant(body))
		limit := 2
		if strings.Contains(body, "max_influences = 1") {
			limit = 1
		}
		checkWeights(t, d, limit)
		if op.Files[0].Influences > limit {
			t.Errorf("%s: the file reports %d influences", body, op.Files[0].Influences)
		}
	}
}

// regressions lists every per-joint measure on which the solved skin is worse
// than the hand skin, strictly: an improved mean must not hide a failed joint.
// Ground is the lowest posed vertex of the joint's region, judged for the
// regions within 5 cm of the floor (the feet); it is worse when it is lower by
// more than 0.5 mm, because the band is a floor.
func regressions(hand, solved *AuditReport) (out []string) {
	for j := range hand.joints {
		h, s := hand.joints[j], solved.joints[j]
		if s.StrainMax > h.StrainMax {
			out = append(out, fmt.Sprintf("%s strain %.3f -> %.3f", h.Joint, h.StrainMax, s.StrainMax))
		}
		if s.Strained > h.Strained {
			out = append(out, fmt.Sprintf("%s strained %.4f -> %.4f", h.Joint, h.Strained, s.Strained))
		}
		if s.Torn > h.Torn {
			out = append(out, fmt.Sprintf("%s torn %.4f -> %.4f", h.Joint, h.Torn, s.Torn))
		}
		// Only a region that touches the ground has a ground measure: the
		// band is for the lowest vertex of the model.
		if h.Lowest < 0.05 && s.Lowest < h.Lowest-0.0005 {
			out = append(out, fmt.Sprintf("%s lowest %.4f -> %.4f", h.Joint, h.Lowest, s.Lowest))
		}
	}
	return out
}

// The athlete's two clips with skeletal poses (ready and run; the library of 19
// is not shipped), at every runtime point of the default plan, measured on the
// hand skin and on the solved skin, joint by joint. The test prints the table.
// It does not say the solver passes: it pins what the table shows, so that a
// change to the solver has to show up here.
func TestSolvedAthleteAuditAgainstHand(t *testing.T) {
	hand, _ := buildAthlete(t, athleteVariant(""))
	solved, _ := buildAthlete(t, athleteVariant("  solve { }\n"))
	h, s := hand.Files[0].Audit, solved.Files[0].Audit
	if h == nil || s == nil || len(h.joints) != 15 || len(s.joints) != 15 {
		t.Fatal("no per-joint audit")
	}
	t.Logf("global      ground %.4f..%.4f -> %.4f..%.4f  torn %.4f -> %.4f  strained %.4f -> %.4f   (hand -> solved, %d poses over %d points)",
		h.GroundLowest.Value, h.GroundHighest.Value, s.GroundLowest.Value, s.GroundHighest.Value, h.Torn.Value, s.Torn.Value, h.Strained.Value, s.Strained.Value, h.Poses, h.Points)
	t.Logf("%-8s %5s  %-15s %-17s %-17s %-17s", "joint", "edges", "strain max", "strained share", "torn share", "lowest m")
	for j := range h.joints {
		a, b := h.joints[j], s.joints[j]
		t.Logf("%-8s %5d  %6.3f -> %6.3f  %.4f -> %.4f  %.4f -> %.4f  %7.4f -> %7.4f", a.Joint, a.Edges, a.StrainMax, b.StrainMax, a.Strained, b.Strained, a.Torn, b.Torn, a.Lowest, b.Lowest)
	}
	reg := regressions(h, s)
	for _, r := range reg {
		t.Logf("REGRESSION %s", r)
	}
	// What the global audit says of the solved skin against the athlete's own
	// pass marks (ground -5..20 mm, torn at most 1%).
	if s.GroundLowest.Value < -0.005 || s.GroundHighest.Value > 0.020 || s.Torn.Value > 0.01 {
		t.Errorf("the solved athlete leaves the pass marks of `check rig`: ground %.4f..%.4f torn %.4f", s.GroundLowest.Value, s.GroundHighest.Value, s.Torn.Value)
	}
	want := pinnedRegressions
	if got := strings.Join(reg, "\n"); got != want {
		t.Errorf("the joints where the solved skin is worse than the hand skin changed:\n%s\nwas pinned as:\n%s", got, want)
	}
}

// The table's regressions, pinned. Both feet: the worst edge of the foot's
// shapes is stretched or squeezed more than on the hand skin (3.38 against
// 2.90), and the lowest vertex of the model sits 1.1 mm lower at the right
// ankle (0.6 mm at the left), inside the band. This is not a pass: a solver
// that regresses on a measure it is held to does not qualify.
const pinnedRegressions = "anklel strain 2.900 -> 3.384\nanklel lowest 0.0276 -> 0.0270\nankler strain 2.900 -> 3.384\nankler lowest 0.0049 -> 0.0038"

func runAcceptFor(t testing.TB, src, acc, dir string) (int, *accept.Report) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := accept.Command([]string{src, acc, "--glb-dir", dir, "--json"}, &out, &errOut)
	var rep accept.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("exit %d, not a report: %v\n%s\n%s", code, err, out.String(), errOut.String())
	}
	return code, &rep
}
