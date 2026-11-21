package athleteparity

import (
	"bytes"
	"m31labs.dev/greenware/compiler"
	"math"
	"os"
	"strings"
	"testing"
)

func TestPartsOwnTheVerticesAndTriangles(t *testing.T) {
	f := newFixture(t, false, nil)
	m := f.model
	if len(m.Bind) != 16+8+8+8+8+8+8 || len(m.Tri) != 24+8+8+8+8+8+8 {
		t.Fatalf("%d vertices %d triangles", len(m.Bind), len(m.Tri))
	}
	for i, o := range m.Owner {
		if o < 0 {
			t.Errorf("vertex %d has no owner", i)
		}
	}
	for i, o := range m.TriOwner {
		if o < 0 {
			t.Errorf("triangle %d has no owner", i)
		}
	}
	// The first 16 vertices and 24 triangles are the torso.
	if m.Owner[0] != 0 || m.Owner[15] != 0 || m.Owner[16] != 1 || m.TriOwner[23] != 0 || m.TriOwner[24] != 1 {
		t.Errorf("owners %v %v", m.Owner[:18], m.TriOwner[22:26])
	}
}

func TestPartsThatDisagreeWithTheFileAreRefused(t *testing.T) {
	f := newFixture(t, false, nil)
	bad := append([]compiler.CensusPart(nil), f.parts...)
	bad[0].Vertices += 100
	if _, err := NewModel("x", f.doc, bad, nil); err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("err = %v", err)
	}
}

func TestMeasureHandComputed(t *testing.T) {
	m := newFixture(t, false, nil).model
	w := m.Measure(m.Posed(nil, nil, 1, "", 0))
	const tol = 1e-5
	near(t, "height", w.Height, 1.9, tol)
	near(t, "helmet", w.Helmet, .27, tol)
	near(t, "shoulder", w.Shoulder, 1.0, tol)
	near(t, "ribs", w.Ribs, .70, tol)
	near(t, "ribs deep", w.RibsDeep, .44, tol)
	near(t, "waist", w.Waist, .60, tol)
	near(t, "waist deep", w.WaistDeep, .40, tol)
	near(t, "seat", w.Seat, .50, tol)
	near(t, "seat deep", w.SeatDeep, .30, tol)
	near(t, "thigh", w.Thigh, .20, tol)
	near(t, "thigh deep", w.ThighDeep, .20, tol)
}

func TestBodyScaleScalesEveryWidth(t *testing.T) {
	m := newFixture(t, false, nil).model
	a := m.Measure(m.Posed(nil, nil, 1, "", 0))
	b := m.Measure(m.Posed(nil, nil, 1.1, "", 0))
	near(t, "height", b.Height, 1.1*a.Height, 1e-5)
	near(t, "waist", b.Waist, 1.1*a.Waist, 1e-5)
	near(t, "thigh deep", b.ThighDeep, 1.1*a.ThighDeep, 1e-5)
}

func TestHeadScaleScalesTheHelmetAboutTheJoint(t *testing.T) {
	m := newFixture(t, false, nil).model
	w := m.Measure(m.Posed(nil, map[string]float64{"head": 0.5}, 1, "", 0))
	near(t, "helmet", w.Helmet, .135, 1e-5)
	// The helmet shrinks about the head joint at y = 1.7: its top ring moves
	// from 1.9 to 1.7 + .2*.5 = 1.8.
	near(t, "height", w.Height, 1.8, 1e-5)
}

func TestMorphFoldsAtTheWeight(t *testing.T) {
	m := newFixture(t, true, nil).model
	w := m.Measure(m.Posed(map[string]float64{"mass": .5}, nil, 1, "", 0))
	near(t, "waist", w.Waist, .60+.05, 1e-5) // .05 yd out on each side at weight 1, times .5
	near(t, "ribs unchanged", w.Ribs, .70, 1e-5)
	// A weight for a target the file lacks changes nothing.
	m2 := newFixture(t, false, nil).model
	near(t, "no target", m2.Measure(m2.Posed(map[string]float64{"mass": 1}, nil, 1, "", 0)).Waist, .60, 1e-5)
}

func TestPoseRotatesTheFigureButNotTheBands(t *testing.T) {
	// A quarter turn about y at t = 0 (the clip's first key): what was across
	// the front is now front to back, and the bands still select by bind height.
	q := [4]float32{0, float32(math.Sqrt2 / 2), 0, float32(math.Sqrt2 / 2)}
	m := newFixture(t, false, &q).model
	w := m.Measure(m.Posed(nil, nil, 1, "ready", 0))
	near(t, "waist", w.Waist, .40, 1e-5)
	near(t, "waist deep", w.WaistDeep, .60, 1e-5)
	near(t, "height", w.Height, 1.9, 1e-5)
	// At the second key the rotation is identity again.
	w = m.Measure(m.Posed(nil, nil, 1, "ready", 1))
	near(t, "waist at t=1", w.Waist, .60, 1e-5)
	// Halfway is a 45 degree turn: the corners of the .60 by .40 rectangle
	// reach |x| = .30 cos 45 + .20 sin 45.
	c, s := math.Cos(math.Pi/4), math.Sin(math.Pi/4)
	w = m.Measure(m.Posed(nil, nil, 1, "ready", .5))
	near(t, "waist at t=.5", w.Waist, 2*(.30*c+.20*s), 1e-5)
}

func TestMedianTakesTheMiddleElement(t *testing.T) {
	ws := []Widths{{Waist: 3}, {Waist: 1}, {Waist: 2}, {Waist: 10}}
	if got := MedianWidths(ws).Waist; got != 3 { // sorted 1 2 3 10, index len/2 = 2
		t.Errorf("median %v", got)
	}
}

func TestShellNames(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"torso", "torso"}, {"thigh_l", "thigh"}, {"thigh-r", "thigh"}, {"shoulder_pad_l", "shoulderpad"},
		{"sleeve_hem_r", "sleevehem"}, {"helmet", "helmet"}, {"jersey", "jersey"}, {"upper_arm.l", "upperarm"},
	} {
		if got := ShellName(tc.in, nil); got != tc.want {
			t.Errorf("ShellName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := ShellName("jersey", map[string]string{"jersey": "torso"}); got != "torso" {
		t.Errorf("alias gave %q", got)
	}
}

func TestLedgerRowsAddUp(t *testing.T) {
	m := newFixture(t, false, nil).model
	r := m.Run(&LookFile{Wearers: []Look{look("OL", 0, 1, 1)}}, Options{NoGate: true})
	var tris, verts int
	rows := map[string]LedgerRow{}
	for _, row := range r.Ledger {
		tris += row.T
		verts += row.V
		rows[row.Name] = row
	}
	if r.Unassigned != nil {
		t.Errorf("unassigned %+v", r.Unassigned)
	}
	if tris != len(m.Tri) || verts != len(m.Bind) {
		t.Errorf("ledger %d/%d, file %d/%d", tris, verts, len(m.Tri), len(m.Bind))
	}
	if g := rows["jersey torso, collar, belt"]; g.T != 24 || g.V != 16 {
		t.Errorf("torso row %+v", g)
	}
	if g := rows["hips, thighs, knee plates"]; g.T != 8+8+8 || g.V != 24 {
		t.Errorf("hips row %+v", g)
	}
	if g := rows["boots and collars"]; g.T != 0 || g.V != 0 {
		// "sole" is a boots shell in the game's table: 8 triangles, 8 vertices.
		if g.T != 8 || g.V != 8 {
			t.Errorf("boots row %+v", g)
		}
	}
	var buf bytes.Buffer
	r.WriteLedger(&buf)
	if !strings.Contains(buf.String(), "jersey torso, collar, belt") {
		t.Errorf("ledger text:\n%s", buf.String())
	}
}

func TestRunWarnsAboutWhatIsMissing(t *testing.T) {
	m := newFixture(t, false, nil).model
	lf := &LookFile{Wearers: []Look{{Name: "w", ShapeWeights: map[string]float64{"mass": .5, "belly": .2}, JointScales: map[string]float64{"neck": .9}, BodyScale: 1}}}
	r := m.Run(lf, Options{Clip: "ready", NoGate: true})
	var text bytes.Buffer
	r.WriteWarnings(&text)
	for _, want := range []string{`morph target "mass"`, `morph target "belly"`, `joint "neck"`, `clip "ready"`} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("no warning for %s in:\n%s", want, text.String())
		}
	}
	if strings.Contains(text.String(), "no shape maps") {
		t.Errorf("a shape is missing that the fixture has:\n%s", text.String())
	}
}

func TestRowFormatMatchesTheGamesLayout(t *testing.T) {
	// The first row of the game's baseline.
	w := Widths{Height: 2.025, Helmet: .27, Shoulder: 3.48 * .27, Ribs: 2.00 * .27, Waist: 1.94 * .27, Seat: 1.86 * .27, Thigh: 1.17 * .27,
		RibsDeep: 1.40 * .27, WaistDeep: 1.36 * .27, SeatDeep: 1.24 * .27, ThighDeep: 1.25 * .27}
	got := FormatRow("lineman (OL starter)", w.Columns(w.Helmet))
	want := "lineman (OL starter)       2.025 0.270 | 3.48 2.00 1.94 1.86 1.17 7.50 | 1.40 1.36 1.24 1.25 | 0.29 1.79\n"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestBaselineParses(t *testing.T) {
	f, err := os.Open("../../testdata/parity/baseline-go-bake.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := ParseBaseline(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Ledger) != 12 || b.Ledger["face and neck"] != [2]int{2172, 1116} || b.Ledger["visor (transparent)"] != [2]int{0, 0} {
		t.Errorf("ledger %v", b.Ledger)
	}
	if len(b.Sil) != 7 {
		t.Fatalf("%d silhouette rows", len(b.Sil))
	}
	c := b.Sil["WR/CB median"]
	if c[0] != 1.924 || c[2] != 3.17 || c[11] != 1.00 || c[13] != 2.26 {
		t.Errorf("WR/CB row %v", c)
	}
}

// reportFrom makes a report whose numbers are the baseline's, optionally moved.
func reportFrom(b *Baseline, tris, shoulder float64) *Report {
	r := &Report{Name: "x"}
	for _, n := range b.LedgerOrder {
		v := b.Ledger[n]
		r.Ledger = append(r.Ledger, LedgerRow{Name: n, T: int(float64(v[0]) * tris), V: v[1]})
	}
	for _, n := range b.SilOrder {
		c := b.Sil[n]
		c[2] += shoulder
		r.Sil = append(r.Sil, SilRow{Name: n, Cols: c})
	}
	return r
}

func TestCompareVerdict(t *testing.T) {
	f, _ := os.Open("../../testdata/parity/baseline-go-bake.txt")
	defer f.Close()
	b, err := ParseBaseline(f)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if !Compare(&out, reportFrom(b, 1, 0), b, DefaultTolerances) {
		t.Errorf("identical numbers fail:\n%s", out.String())
	}
	out.Reset()
	// Triangles 4 percent high and shoulders .02 H wide are inside 5 percent and .03 H.
	if !Compare(&out, reportFrom(b, 1.04, .02), b, DefaultTolerances) {
		t.Errorf("inside tolerance fails:\n%s", out.String())
	}
	out.Reset()
	if Compare(&out, reportFrom(b, 1.06, 0), b, DefaultTolerances) {
		t.Errorf("6 percent passes")
	}
	out.Reset()
	if Compare(&out, reportFrom(b, 1, .05), b, DefaultTolerances) {
		t.Errorf(".05 H passes")
	}
	if !strings.Contains(out.String(), "*") {
		t.Errorf("no column marked:\n%s", out.String())
	}
	out.Reset()
	if !Compare(&out, reportFrom(b, 1, .05), b, Tolerances{LedgerPercent: 5, SilHelmets: .06}) {
		t.Errorf("flag tolerance not used")
	}
}

func TestRenderCoverageAndExtents(t *testing.T) {
	// One quad, 1 yd wide and 1 yd tall, standing on y = 0, seen from the front.
	p := [][3]float64{{-.5, 0, 0}, {.5, 0, 0}, {.5, 1, 0}, {-.5, 1, 0}}
	tri := [][3]int{{0, 1, 2}, {0, 2, 3}}
	mk := Render(tri, p, 40, false)
	if mk.W != 64 || mk.H != 64 {
		t.Fatalf("picture %dx%d, want 64x64 (the figure plus 12 px twice)", mk.W, mk.H)
	}
	if e := mk.RowExtent(32); e != 40 {
		t.Errorf("row extent %d, want 40", e)
	}
	if e := mk.RowExtent(5); e != 0 {
		t.Errorf("row above the figure covers %d", e)
	}
	near(t, "band extent", mk.BandExtent(.4, .6), 40, 1e-9)
	// Side view of a figure 1 yd deep and 0.5 yd across: the extent is the depth.
	p2 := [][3]float64{{-.25, 0, -.5}, {.25, 0, .5}, {.25, 1, .5}, {-.25, 1, -.5}}
	side := Render([][3]int{{0, 1, 2}, {0, 2, 3}}, p2, 40, true)
	near(t, "side extent", float64(side.RowExtent(side.H/2)), 40, 1)
}

func TestGateSeesAWiderWaist(t *testing.T) {
	m := newFixture(t, true, nil).model
	heavy := []Figure{m.PoseLook(look("OL", 1, 1, 1), "", 0)}
	light := []Figure{m.PoseLook(look("WR", 0, 1, 1), "", 0)}
	gs := m.Gate(heavy, light, 96)
	if len(gs) != 2 || gs[0].Convention != "equal scale" {
		t.Fatalf("gate %+v", gs)
	}
	waist := gs[0].Metrics[0]
	// Equal scale: 96 / 1.9 yd = 50.5 px per yd. The heavy waist is .10 yd
	// wider up to y = 1.15 and tapers to the ribs above it: about .093 yd
	// over the band, 4.7 px.
	near(t, "waist diff px", waist.Diff, 4.7, 1.0)
	if !waist.Pass {
		t.Errorf("a 4.7 px waist does not pass a 3 px gate: %+v", waist)
	}
	// The thighs and lower ribs are the same in both figures.
	if d := gs[0].Metrics[1].Diff; math.Abs(d) > 1e-9 {
		t.Errorf("thigh diff %v", d)
	}
	var buf bytes.Buffer
	WriteGate(&buf, gs, 96)
	if !strings.Contains(buf.String(), "PASS") || !strings.Contains(buf.String(), "FAIL") {
		t.Errorf("gate text:\n%s", buf.String())
	}
}

func TestRunIsDeterministic(t *testing.T) {
	m := newFixture(t, true, nil).model
	lf := &LookFile{
		Wearers: []Look{look("OL", 1, .9, 1.05)},
		Roster:  []Look{look("OL", 1, .9, 1.05), look("WR", 0, .9, 1)},
	}
	var a, b bytes.Buffer
	m.Run(lf, Options{Clip: ""}).Write(&a)
	m.Run(lf, Options{Clip: ""}).Write(&b)
	if a.String() != b.String() {
		t.Error("two runs differ")
	}
}

// The looks file the game's tool wrote: the two named wearers and a roster of
// every school's three deep at nine positions, with the same morph names the
// ledger lists.
func TestLooksFileLoads(t *testing.T) {
	lf, err := LoadLooks("../../examples/athlete/testdata/looks.json")
	if err != nil {
		t.Fatal(err)
	}
	if lf.GameCommit == "" || len(lf.Wearers) != 2 || lf.Wearers[0].Name != "lineman (OL starter)" || lf.Wearers[0].Number != 72 || lf.Wearers[1].Number != 1 {
		t.Errorf("wearers %+v commit %q", lf.Wearers, lf.GameCommit)
	}
	if len(lf.Roster) == 0 || len(lf.Roster)%27 != 0 {
		t.Fatalf("%d roster rows: want 9 positions x 3 depths per school", len(lf.Roster))
	}
	for _, name := range MorphNames {
		if _, ok := lf.Wearers[0].ShapeWeights[name]; !ok {
			t.Errorf("no weight for %s", name)
		}
	}
	for _, g := range Groups {
		if len(lf.Members(g)) == 0 {
			t.Errorf("group %s has no members", g.Name)
		}
	}
	if s := lf.Wearers[0].JointScales["head"]; s <= 0 || s > 1.2 {
		t.Errorf("head scale %v", s)
	}
}

func TestLooksParseRefusesARaggedRow(t *testing.T) {
	_, err := ParseLooks([]byte(`{"wearers":[{"pos":"OL"}],"roster_columns":["school","pos"],"roster":[[0]]}`), "x")
	if err == nil || !strings.Contains(err.Error(), "roster row 0") {
		t.Errorf("err = %v", err)
	}
}
