package accept

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/greenware/gltf"
)

// The goal post's chord errors, from the spec: 2.99 mm at level 0 (the
// plate's 24 segments), then the lod blocks' max_error.
var goalpostLODError = [3]float64{0.00299, 0.005, 0.03}

func ownerAccept(t testing.TB) string {
	t.Helper()
	b, err := os.ReadFile("../testdata/syntax/goalpost.accept.gw")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// products builds the inputs for both outputs and three levels from a model.
// Level 2 drops the base and the pad, as the source's lod block does, and the
// practice output has no pad.
func products(t testing.TB, m goalpostModel) Inputs {
	t.Helper()
	in := Inputs{}
	for _, out := range []string{"stadium", "practice"} {
		for lod := 0; lod < 3; lod++ {
			mm := m
			if out == "practice" {
				mm.pad = false
			}
			if lod == 2 {
				mm.pad, mm.base = false, false
			}
			in[Key{out, lod}] = Product{Doc: roundTrip(t, mm.document()), File: out + ".glb", LODError: goalpostLODError[lod]}
		}
	}
	return in
}

func find(t testing.TB, r *Report, output string, lod int) *LevelReport {
	t.Helper()
	for i := range r.Levels {
		if r.Levels[i].Output == output && r.Levels[i].LOD == lod {
			return &r.Levels[i]
		}
	}
	t.Fatalf("no level %s %d in report", output, lod)
	return nil
}

func expectNamed(t testing.TB, l *LevelReport, message string) ExpectReport {
	t.Helper()
	for _, e := range l.Expects {
		if e.Message == message {
			return e
		}
	}
	t.Fatalf("%s lod %d has no expect %q (has %d)", l.Output, l.LOD, message, len(l.Expects))
	return ExpectReport{}
}

func measure(t testing.TB, e ExpectReport, name string) float64 {
	t.Helper()
	for _, m := range e.Measures {
		if m.Name == name {
			f, err := m.Number.Float64()
			if err != nil {
				t.Fatalf("measure %q has no number: %v", name, m)
			}
			return f
		}
	}
	t.Fatalf("no measure %q in %+v", name, e.Measures)
	return 0
}

const (
	msgGap       = "uprights' inside faces 18 ft 6 in apart"
	msgAnchors   = "the rule anchors agree"
	msgClosed    = "no holes in the painted frame"
	msgConnected = "the painted frame is one piece: post, crossbar and uprights touch"
	msgOutline   = "front outline"
)

func TestModelWindsOutward(t *testing.T) {
	g := defaultModel().frame()
	if v := signedVolume(g); v <= 0 {
		t.Fatalf("signed volume %v: the test prisms must wind outward", v)
	}
}

// The owner's file on a correct synthetic post: everything passes except the
// outline, which is unavailable (and counted apart from the passes).
func TestOwnerFilePasses(t *testing.T) {
	acc := parseAccept(t, ownerAccept(t))
	rep, ds := Run(acc, products(t, defaultModel()))
	// 6 levels. Per level: 8 shared expects; stadium adds 2 (height, draws),
	// practice adds none; stadium lod<2 adds 1 (pad); practice or lod 2 adds 1.
	wantExpects := map[string]int{"stadium0": 11, "stadium1": 11, "stadium2": 11, "practice0": 9, "practice1": 9, "practice2": 9}
	for _, l := range rep.Levels {
		key := l.Output + string(rune('0'+l.LOD))
		if len(l.Expects) != wantExpects[key] {
			t.Errorf("%s: %d expects, want %d", key, len(l.Expects), wantExpects[key])
		}
		for _, e := range l.Expects {
			switch {
			case e.Message == msgOutline:
				if e.Verdict != Unavailable {
					t.Errorf("%s: outline verdict %s, want unavailable", key, e.Verdict)
				}
			case e.Verdict != Pass:
				t.Errorf("%s: %q is %s: %s %+v", key, e.Message, e.Verdict, e.Detail, e.Measures)
			}
		}
	}
	if rep.Totals.Unavailable != 6 || rep.Totals.Fail != 0 || rep.Totals.Refused != 0 || rep.Totals.Missing != 0 {
		t.Errorf("totals %+v", rep.Totals)
	}
	if got := rep.Totals.Pass; got != 60-6 {
		t.Errorf("pass = %d, want 54", got)
	}
	if rep.ExitCode() != ExitUnavailable {
		t.Errorf("exit = %d, want 3", rep.ExitCode())
	}
	for _, d := range ds {
		if d.Severity.String() == "error" {
			t.Errorf("unexpected error diagnostic: %v", d)
		}
	}
	// The six unavailable outlines are warnings that name the reference file.
	warn := 0
	for _, d := range ds {
		if strings.Contains(d.Message, "ref/goalpost-stadium.png") || strings.Contains(d.Message, "ref/goalpost-practice.png") {
			warn++
		}
	}
	if warn != 6 {
		t.Errorf("%d warnings name the reference mask, want 6: %v", warn, ds)
	}
}

// An upright gap measured through the middle of a flat of a 12-gon is shorter
// than the gap of the ideal circle: the inside face of a 12-gon upright of
// circumradius 0.05 m, seen at the midpoint of an edge, is 0.05*cos(15 deg) =
// 48.296 mm from the axis, not 50 mm. Two uprights at +-2.87 m then give
// 5.6434 m, not 5.640 m. That is why the owner's tolerance is 1 mm plus
// 2 * lod_error: the 12-gon's chord error is 1.704 mm per side.
func TestGapAtEdgeMidpointNeedsChordTolerance(t *testing.T) {
	src := `greenware 1
accept a {
  of = "x.gw"
  outputs = [stadium]
  lods = [0]
  expect (abs(gap(node(goalpost), axis = x, at = (0, 7m, 0)) - 5.64m) < 1mm + 2 * lod_error, "with chord tolerance")
  expect (abs(gap(node(goalpost), axis = x, at = (0, 7m, 0)) - 5.64m) < 1mm, "without chord tolerance")
}
`
	acc := parseAccept(t, src)
	ideal := math.Cos(math.Pi / 12) // 0.9659
	wantEdge := 2 * (2.87 - 0.05*ideal)
	cases := []struct {
		name  string
		phase float64
		want  float64
		pass2 bool
	}{
		{"vertex at the inside", 0, 5.64, true},
		{"edge midpoint at the inside", math.Pi / 12, wantEdge, false},
	}
	for _, c := range cases {
		m := defaultModel()
		m.phase = c.phase
		in := Inputs{{"stadium", 0}: {Doc: roundTrip(t, m.document()), LODError: chordError(0.05, 12)}}
		rep, _ := Run(acc, in)
		l := find(t, rep, "stadium", 0)
		with := expectNamed(t, l, "with chord tolerance")
		without := expectNamed(t, l, "without chord tolerance")
		got := measure(t, with, "gap(node(goalpost), axis = x, at = (0, 7m, 0))")
		if math.Abs(got-c.want) > 1e-6 {
			t.Errorf("%s: gap %.6f, want %.6f", c.name, got, c.want)
		}
		if with.Verdict != Pass {
			t.Errorf("%s: with chord tolerance: %s %v", c.name, with.Verdict, with.Measures)
		}
		if (without.Verdict == Pass) != c.pass2 {
			t.Errorf("%s: without chord tolerance: %s, want pass=%v", c.name, without.Verdict, c.pass2)
		}
	}
	if math.Abs(wantEdge-5.6434) > 1e-4 {
		t.Errorf("analytic edge-midpoint gap %.5f, the design notes say about 5.6434", wantEdge)
	}
}

// The regression control: one upright moved 2 cm outward, sockets left in
// place. The gap expect must fail, with the measured 5.660 m in the evidence.
// The socket-distance expect passes, because the sockets were not moved: that
// is the point of measuring the mesh and not the anchors (spec appendix A.2:
// "the socket-distance assertion passes"). The design notes also say the gap
// and the socket agreement should both fail; with sockets in place only the
// gap can, and this test records the spec's reading.
func TestMovedUprightFails(t *testing.T) {
	m := defaultModel()
	m.movedRight = 0.02
	acc := parseAccept(t, ownerAccept(t))
	rep, ds := Run(acc, products(t, m))
	if rep.ExitCode() != ExitFail {
		t.Fatalf("exit = %d, want 1", rep.ExitCode())
	}
	for _, l := range rep.Levels {
		gapE := expectNamed(t, &l, msgGap)
		anch := expectNamed(t, &l, msgAnchors)
		// lod 2 tolerates 1 mm + 60 mm, so a 20 mm move passes there: the
		// owner's tolerance grows with the level.
		wantGap := Fail
		if l.LOD == 2 {
			wantGap = Pass
		}
		if gapE.Verdict != wantGap {
			t.Errorf("%s lod %d: gap verdict %s, want %s: %v", l.Output, l.LOD, gapE.Verdict, wantGap, gapE.Measures)
		}
		if anch.Verdict != Pass {
			t.Errorf("%s lod %d: anchors %s", l.Output, l.LOD, anch.Verdict)
		}
		if l.LOD == 0 {
			got := measure(t, gapE, "gap(node(goalpost), axis = x, at = (0, 7m, 0))")
			if math.Abs(got-5.66) > 1e-6 {
				t.Errorf("measured gap %.6f, want 5.66", got)
			}
		}
	}
	var msg string
	for _, d := range ds {
		if strings.Contains(d.Message, "output stadium lod 0: "+msgGap) {
			msg = d.Message
		}
	}
	if !strings.Contains(msg, "5.66m") || !strings.Contains(msg, "expectation failed") {
		t.Errorf("the diagnostic should show the measured gap, got %q", msg)
	}
}

// Twelve rim edges used four times: two bodies, each closed on its own, share
// a ring of 12 edges exactly. Every edge count is 2 or 4, there are no open
// edges, yet the mesh is not closed (plan section 4.2: 164 triangles and zero
// open edges do not imply closed). Exact welding must not be loosened to pass.
func TestExactCoincidentRimIsNotClosed(t *testing.T) {
	src := `greenware 1
accept a {
  of = "x.gw"
  outputs = [stadium]
  lods = [0]
  expect (closed(node(goalpost)), "closed")
  expect (open_edges(node(goalpost)) == 0, "no open edges")
}
`
	acc := parseAccept(t, src)
	m := defaultModel()
	m.stack = true
	rep, _ := Run(acc, Inputs{{"stadium", 0}: {Doc: roundTrip(t, m.document())}})
	l := find(t, rep, "stadium", 0)
	if e := expectNamed(t, l, "closed"); e.Verdict != Fail {
		t.Errorf("closed: %s, want fail", e.Verdict)
	} else if over := measure(t, e, "closed(node(goalpost)) overused edges"); over != 12 {
		t.Errorf("overused edges = %v, want 12", over)
	}
	if e := expectNamed(t, l, "no open edges"); e.Verdict != Pass {
		t.Errorf("open_edges == 0 is %s: a count of zero open edges is the trap, not the proof", e.Verdict)
	}
	// The same body without the stacked prism is closed.
	m.stack = false
	rep, _ = Run(acc, Inputs{{"stadium", 0}: {Doc: roundTrip(t, m.document())}})
	if e := expectNamed(t, find(t, rep, "stadium", 0), "closed"); e.Verdict != Pass {
		t.Errorf("plain frame: closed is %s %v", e.Verdict, e.Measures)
	}
}

// A deleted cap leaves the 12 rim edges open.
func TestMissingCapIsOpen(t *testing.T) {
	src := `greenware 1
accept a {
  outputs = [stadium]
  lods = [0]
  expect (closed(node(goalpost)), "closed")
  expect (open_edges(node(goalpost)) == 0, "no open edges")
}
`
	m := defaultModel()
	// The first upright's side has 24 triangles, its top cap 12 follow.
	// Triangle 24..35 is the top cap (centre fan).
	for i := 24; i < 36; i++ {
		m.drop = append(m.drop, i)
	}
	rep, _ := Run(parseAccept(t, src), Inputs{{"stadium", 0}: {Doc: roundTrip(t, m.document())}})
	l := find(t, rep, "stadium", 0)
	if e := expectNamed(t, l, "closed"); e.Verdict != Fail {
		t.Errorf("closed with a cap missing: %s", e.Verdict)
	}
	e := expectNamed(t, l, "no open edges")
	if e.Verdict != Fail {
		t.Fatalf("open edges: %s", e.Verdict)
	}
	if got := measure(t, e, "open_edges(node(goalpost))"); got != 12 {
		t.Errorf("open edges = %v, want 12", got)
	}
}

func TestConnectedFrame(t *testing.T) {
	src := `greenware 1
accept a {
  outputs = [stadium]
  lods = [0]
  expect (connected(node(goalpost)), "one piece")
}
`
	acc := parseAccept(t, src)
	// Uprights and crossbar cross, so the frame is one piece.
	rep, _ := Run(acc, Inputs{{"stadium", 0}: {Doc: roundTrip(t, defaultModel().document())}})
	e := expectNamed(t, find(t, rep, "stadium", 0), "one piece")
	if e.Verdict != Pass {
		t.Errorf("plain frame: %s %v", e.Verdict, e.Measures)
	}
	if n := measure(t, e, "connected(node(goalpost)) contact groups"); n != 1 {
		t.Errorf("groups = %v", n)
	}
	// Without the crossbar the two uprights are 5.64 m apart: two groups.
	m := defaultModel()
	m.crossbar = false
	rep, _ = Run(acc, Inputs{{"stadium", 0}: {Doc: roundTrip(t, m.document())}})
	e = expectNamed(t, find(t, rep, "stadium", 0), "one piece")
	if e.Verdict != Fail || measure(t, e, "connected(node(goalpost)) contact groups") != 2 {
		t.Errorf("no crossbar: %s %v", e.Verdict, e.Measures)
	}
	// A floating box above the frame is a third group.
	m = defaultModel()
	m.floaterGap = 1
	rep, _ = Run(acc, Inputs{{"stadium", 0}: {Doc: roundTrip(t, m.document())}})
	e = expectNamed(t, find(t, rep, "stadium", 0), "one piece")
	if e.Verdict != Fail || measure(t, e, "connected(node(goalpost)) contact groups") != 2 {
		t.Errorf("floater: %s %v", e.Verdict, e.Measures)
	}
}

// The 1 mm contact rule, tested on triangles directly: two unit boxes a given
// distance apart.
func TestShellContactDistance(t *testing.T) {
	for _, c := range []struct {
		sep  float64
		want int
	}{{0.0005, 1}, {0.0009, 1}, {0.0011, 2}, {1, 2}} {
		g := &geom{}
		g.box(0, 0, 0, 1, 1, 1)
		g.box(1+c.sep, 0, 0, 2+c.sep, 1, 1)
		var tris []tri
		for i := 0; i+2 < len(g.idx); i += 3 {
			var tr tri
			for k := 0; k < 3; k++ {
				v := g.idx[i+k]
				tr[k] = v3{float64(g.pos[3*v]), float64(g.pos[3*v+1]), float64(g.pos[3*v+2])}
			}
			tris = append(tris, tr)
		}
		tp := analyse(tris)
		if got := shellComponents(tris, tp); got != c.want {
			t.Errorf("separation %v: %d groups, want %d", c.sep, got, c.want)
		}
	}
}

// Two touching pairs of shells that do not touch each other are closed and
// not connected (spec section 2.7, decoder fixtures).
func TestTwoTouchingPairsNotConnected(t *testing.T) {
	g := &geom{}
	// Each pair crosses: the second box pokes through the first box's +x face.
	// No corner positions coincide, so each box is its own closed shell.
	g.box(0, 0, 0, 1, 1, 1)
	g.box(0.5, 0.25, 0.25, 1.5, 0.75, 0.75)
	g.box(10, 0, 0, 11, 1, 1)
	g.box(10.5, 0.25, 0.25, 11.5, 0.75, 0.75)
	doc := &gltf.Document{
		Meshes: []gltf.Mesh{{Primitives: []gltf.Primitive{g.primitive()}}},
		Nodes:  []gltf.Node{{Name: "four", Mesh: gltf.Int(0)}},
		Scenes: []gltf.Scene{{Nodes: []int{0}}},
	}
	acc := parseAccept(t, `greenware 1
accept a {
  outputs = [o]
  lods = [0]
  expect (closed(node(four)), "closed")
  expect (connected(node(four)), "connected")
}
`)
	rep, _ := Run(acc, Inputs{{"o", 0}: {Doc: roundTrip(t, doc)}})
	l := find(t, rep, "o", 0)
	if e := expectNamed(t, l, "closed"); e.Verdict != Pass {
		t.Errorf("closed: %s %v", e.Verdict, e.Measures)
	}
	e := expectNamed(t, l, "connected")
	if e.Verdict != Fail {
		t.Errorf("connected: %s", e.Verdict)
	}
	if n := measure(t, e, "connected(node(four)) contact groups"); n != 2 {
		t.Errorf("contact groups = %v, want 2", n)
	}
}

func TestWhenBlocks(t *testing.T) {
	src := `greenware 1
accept a {
  outputs = [stadium, practice]
  lods = all
  when output == stadium {
    expect (true_, "A stadium")
  }
  when output == stadium && lod < 2 {
    expect (true_, "B stadium near")
  }
  when output == practice || lod == 2 {
    expect (true_, "C practice or far")
  } else {
    expect (true_, "D otherwise")
  }
}
`
	src = strings.ReplaceAll(src, "true_", "1 == 1")
	rep, ds := Run(parseAccept(t, src), products(t, defaultModel()))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	want := map[string]string{
		"stadium0":  "A stadium,B stadium near,D otherwise",
		"stadium1":  "A stadium,B stadium near,D otherwise",
		"stadium2":  "A stadium,C practice or far",
		"practice0": "C practice or far",
		"practice1": "C practice or far",
		"practice2": "C practice or far",
	}
	for _, l := range rep.Levels {
		var msgs []string
		for _, e := range l.Expects {
			msgs = append(msgs, e.Message)
		}
		if got := strings.Join(msgs, ","); got != want[l.Output+string(rune('0'+l.LOD))] {
			t.Errorf("%s lod %d ran %q, want %q", l.Output, l.LOD, got, want[l.Output+string(rune('0'+l.LOD))])
		}
	}
}

func TestRefusals(t *testing.T) {
	cases := []struct {
		name, expr, detail string
	}{
		{"ray miss above the post", "gap(node(goalpost), axis = x, at = (0, 20m, 0)) > 0m", "misses the mesh"},
		{"ray miss on one side", "gap(node(goalpost), axis = x, at = (4m, 7m, 0)) > 0m", "misses the mesh"},
		{"inside an upright", "gap(node(goalpost), axis = x, at = (2.87m, 7m, 0)) > 0m", "inside the mesh"},
		{"on a surface", "gap(node(goalpost), axis = x, at = (2.82m, 7m, 0)) > 0m", "on a surface"},
		{"missing node", "gap(node(wing), axis = x, at = (0, 7m, 0)) > 0m", `no node named "wing"`},
		{"missing socket", "distance(socket(nope), socket(inside_l)) > 0m", `no socket named "nope"`},
		{"lod_error unknown", "lod_error > 0m", "lod_error is not known"},
	}
	for _, c := range cases {
		src := "greenware 1\naccept a {\n  outputs = [o]\n  lods = [0]\n  expect (" + c.expr + ", \"m\")\n}\n"
		in := Inputs{{"o", 0}: {Doc: roundTrip(t, defaultModel().document()), LODErrorUnknown: strings.Contains(c.expr, "lod_error")}}
		rep, ds := Run(parseAccept(t, src), in)
		e := rep.Levels[0].Expects[0]
		if e.Verdict != Refused || !strings.Contains(e.Detail, c.detail) {
			t.Errorf("%s: verdict %s detail %q, want refused containing %q", c.name, e.Verdict, e.Detail, c.detail)
		}
		if rep.ExitCode() != ExitFail {
			t.Errorf("%s: a refused measurement must fail the run, exit %d", c.name, rep.ExitCode())
		}
		if len(ds) != 1 || ds[0].Severity.String() != "error" {
			t.Errorf("%s: diagnostics %v", c.name, ds)
		}
	}
}

// Socket nodes have no mesh. closed, connected and gap of a node with no own
// triangles do not pass.
func TestEmptyMeshRules(t *testing.T) {
	src := `greenware 1
accept a {
  outputs = [o]
  lods = [0]
  expect (closed(node(base)), "closed base")
  expect (connected(node(base)), "connected base")
  expect (open_edges(node(base)) == 0, "base has no open edges")
  expect (gap(node(base), axis = x, at = (0, 1m, 1.83m)) > 0m, "gap of base")
}
`
	m := defaultModel()
	m.base = true
	doc := m.document()
	// Make base an empty node: drop its mesh.
	for i := range doc.Nodes {
		if doc.Nodes[i].Name == "base" {
			doc.Nodes[i].Mesh = nil
		}
	}
	rep, _ := Run(parseAccept(t, src), Inputs{{"o", 0}: {Doc: roundTrip(t, doc)}})
	l := rep.Levels[0]
	if e := expectNamed(t, &l, "closed base"); e.Verdict != Fail {
		t.Errorf("closed of an empty mesh: %s, spec says off", e.Verdict)
	}
	if e := expectNamed(t, &l, "connected base"); e.Verdict != Fail {
		t.Errorf("connected of an empty mesh: %s, spec says off", e.Verdict)
	}
	if e := expectNamed(t, &l, "base has no open edges"); e.Verdict != Pass {
		t.Errorf("open_edges of an empty mesh: %s, spec says 0", e.Verdict)
	}
	if e := expectNamed(t, &l, "gap of base"); e.Verdict != Refused {
		t.Errorf("gap of an empty mesh: %s, spec says fails", e.Verdict)
	}
}

// outline needs reference masks that do not exist: unavailable, never a pass.
func TestOutlineIsUnavailable(t *testing.T) {
	src := `greenware 1
accept a {
  outputs = [stadium]
  lods = [0]
  expect (outline(front, "ref/goalpost-{output}.png", within = 3px) >= 0.95, "front outline")
  expect (outline(front, "ref/goalpost-{output}.png", within = 3px) < 0.95, "front outline, inverted")
  expect (1 == 1, "something that passes")
}
`
	rep, ds := Run(parseAccept(t, src), Inputs{{"stadium", 0}: {Doc: roundTrip(t, defaultModel().document())}})
	l := rep.Levels[0]
	for _, e := range l.Expects[:2] {
		if e.Verdict != Unavailable {
			t.Errorf("%q: %s, want unavailable", e.Message, e.Verdict)
		}
		if !strings.Contains(e.Detail, `"ref/goalpost-stadium.png"`) {
			t.Errorf("detail should name the reference mask: %q", e.Detail)
		}
	}
	if rep.Totals != (Totals{Pass: 1, Unavailable: 2}) {
		t.Errorf("totals %+v", rep.Totals)
	}
	if rep.ExitCode() != ExitUnavailable {
		t.Errorf("exit %d, want 3", rep.ExitCode())
	}
	if len(ds) != 2 || ds[0].Severity.String() != "warning" {
		t.Errorf("diagnostics %v", ds)
	}
	// A real failure beside an unavailable outline still exits 1.
	src2 := strings.Replace(src, `"something that passes"`, `"something that fails"`, 1)
	src2 = strings.Replace(src2, "expect (1 == 1,", "expect (1 == 2,", 1)
	rep, _ = Run(parseAccept(t, src2), Inputs{{"stadium", 0}: {Doc: roundTrip(t, defaultModel().document())}})
	if rep.ExitCode() != ExitFail {
		t.Errorf("exit %d, want 1", rep.ExitCode())
	}
}

// Drawn and present are different: a hidden node is present (has_node) and
// draws nothing (drawn_triangles, draw_calls).
func TestHiddenNodeIsPresentNotDrawn(t *testing.T) {
	src := `greenware 1
accept a {
  outputs = [o]
  lods = [0]
  expect (has_node(pad), "present")
  expect (drawn_triangles(pad) == 0, "draws nothing")
  expect (draw_calls() == 2, "two draws: frame and base")
  expect (!has_node(wing), "absent")
}
`
	m := defaultModel()
	m.hidePad = true
	rep, _ := Run(parseAccept(t, src), Inputs{{"o", 0}: {Doc: roundTrip(t, m.document())}})
	for _, e := range rep.Levels[0].Expects {
		if e.Verdict != Pass {
			t.Errorf("%q: %s %s %v", e.Message, e.Verdict, e.Detail, e.Measures)
		}
	}
}

func TestDrawCallsCountsPrimitives(t *testing.T) {
	src := `greenware 1
accept a {
  outputs = [o]
  lods = [0]
  expect (draw_calls() <= 3, "three draws or fewer")
}
`
	m := defaultModel()
	m.extraDraw = true
	rep, _ := Run(parseAccept(t, src), Inputs{{"o", 0}: {Doc: roundTrip(t, m.document())}})
	e := rep.Levels[0].Expects[0]
	if e.Verdict != Fail || measure(t, e, "draw_calls()") != 4 {
		t.Errorf("%s %v", e.Verdict, e.Measures)
	}
}

// Node transforms are applied before measurement: a root translated up by 1 m
// moves sockets and bounds, and a root turned 90 degrees about y turns the
// x gap into a z gap.
func TestNodeTransforms(t *testing.T) {
	doc := defaultModel().document()
	doc.Nodes = append(doc.Nodes, gltf.Node{Name: "world", Children: []int{0}, Translation: &[3]float32{0, 1, 0}})
	doc.Scenes[0].Nodes = []int{len(doc.Nodes) - 1}
	src := `greenware 1
accept a {
  outputs = [o]
  lods = [0]
  expect (abs(position(socket(crossbar_centre)).y - 4.05m) < 0.001mm, "socket moved up")
  expect (bounds().min.y > 0.99m, "bounds moved up")
  expect (abs(gap(node(goalpost), axis = x, at = (0, 8m, 0)) - 5.64m) < 0.001mm, "gap unchanged")
}
`
	rep, _ := Run(parseAccept(t, src), Inputs{{"o", 0}: {Doc: roundTrip(t, doc)}})
	for _, e := range rep.Levels[0].Expects {
		if e.Verdict != Pass {
			t.Errorf("%q: %s %s %v", e.Message, e.Verdict, e.Detail, e.Measures)
		}
	}

	// Quarter turn about y (quaternion (0, sin 45, 0, cos 45)): x maps to -z.
	doc2 := defaultModel().document()
	s := float32(math.Sqrt(0.5))
	doc2.Nodes[0].Rotation = &[4]float32{0, s, 0, s}
	src2 := `greenware 1
accept a {
  outputs = [o]
  lods = [0]
  expect (abs(gap(node(goalpost), axis = z, at = (0, 7m, 0)) - 5.64m) < 0.01mm, "gap now along z")
  expect (closed(node(goalpost)), "still closed")
}
`
	rep, _ = Run(parseAccept(t, src2), Inputs{{"o", 0}: {Doc: roundTrip(t, doc2)}})
	for _, e := range rep.Levels[0].Expects {
		if e.Verdict != Pass {
			t.Errorf("%q: %s %s %v", e.Message, e.Verdict, e.Detail, e.Measures)
		}
	}
}

// A mirrored node (negative scale) reverses the winding; the evaluator turns it
// back, so the inside of the post is still outside the mesh.
func TestMirroredNodeKeepsOutwardWinding(t *testing.T) {
	doc := defaultModel().document()
	doc.Nodes[0].Scale = &[3]float32{-1, 1, 1}
	src := `greenware 1
accept a {
  outputs = [o]
  lods = [0]
  expect (abs(gap(node(goalpost), axis = x, at = (0, 7m, 0)) - 5.64m) < 0.01mm, "gap")
  expect (closed(node(goalpost)), "closed")
}
`
	rep, _ := Run(parseAccept(t, src), Inputs{{"o", 0}: {Doc: roundTrip(t, doc)}})
	for _, e := range rep.Levels[0].Expects {
		if e.Verdict != Pass {
			t.Errorf("%q: %s %s %v", e.Message, e.Verdict, e.Detail, e.Measures)
		}
	}
}

func TestMissingProductFails(t *testing.T) {
	acc := parseAccept(t, ownerAccept(t))
	in := products(t, defaultModel())
	in[Key{"stadium", 1}] = Product{File: "goalpost-lod1.glb", Problem: "cannot read goalpost-lod1.glb: no such file"}
	delete(in, Key{"practice", 2})
	rep, ds := Run(acc, in)
	if rep.Totals.Missing != 1 || rep.ExitCode() != ExitFail {
		t.Errorf("totals %+v exit %d", rep.Totals, rep.ExitCode())
	}
	found := false
	for _, d := range ds {
		if strings.Contains(d.Message, "output stadium lod 1: cannot read goalpost-lod1.glb") {
			found = true
		}
	}
	if !found {
		t.Errorf("no diagnostic for the missing file: %v", ds)
	}
	for _, l := range rep.Levels {
		if l.Output == "practice" && l.LOD == 2 {
			t.Error("practice lod 2 was not supplied, and `lods = all` means the levels supplied")
		}
	}
}

func TestReportJSONIsStable(t *testing.T) {
	acc := parseAccept(t, ownerAccept(t))
	a, _ := Run(acc, products(t, defaultModel()))
	b, _ := Run(acc, products(t, defaultModel()))
	ja, err := a.JSON()
	if err != nil {
		t.Fatal(err)
	}
	jb, _ := b.JSON()
	if !bytes.Equal(ja, jb) {
		t.Fatal("two runs gave different JSON")
	}
	var back map[string]any
	if err := json.Unmarshal(ja, &back); err != nil {
		t.Fatal(err)
	}
	s := string(ja)
	order := []string{`"schema"`, `"accept"`, `"of"`, `"totals"`, `"levels"`}
	last := -1
	for _, k := range order {
		i := strings.Index(s, k)
		if i < 0 || i < last {
			t.Fatalf("key %s is missing or out of order in:\n%s", k, s[:min(len(s), 400)])
		}
		last = i
	}
	if !strings.Contains(s, `"verdict": "unavailable"`) || !strings.Contains(s, `"lod_error": 0.00299`) {
		t.Errorf("report lacks the verdicts or the lod_error:\n%s", s[:min(len(s), 1200)])
	}
}

func TestBadExpressionsAreRefused(t *testing.T) {
	src := `greenware 1
accept a {
  outputs = [o]
  lods = [0]
  expect (gap(node(goalpost), axis = x, at = (0, 7m, 0)) - 5.64 < 1mm, "bare number next to a length")
  expect (bounds().min.y + 1deg > 0m, "length plus angle")
  expect (frobnicate() > 0m, "unknown function")
  expect (3m, "not a switch")
}
`
	rep, _ := Run(parseAccept(t, src), Inputs{{"o", 0}: {Doc: roundTrip(t, defaultModel().document())}})
	// A bare 5.64 beside a length takes the default unit (metres), so the
	// first one is a pass; the rest are refused.
	want := []Verdict{Pass, Refused, Refused, Refused}
	for i, e := range rep.Levels[0].Expects {
		if e.Verdict != want[i] {
			t.Errorf("%q: %s (%s), want %s", e.Message, e.Verdict, e.Detail, want[i])
		}
	}
}

// Command -----------------------------------------------------------------

func writeGLBs(t *testing.T, dir string, m goalpostModel, skip map[string]bool) {
	t.Helper()
	names := map[string]string{
		"goalpost.glb": "stadium0", "goalpost-lod1.glb": "stadium1", "goalpost-lod2.glb": "stadium2",
		"goalpost-practice.glb": "practice0", "goalpost-practice-lod1.glb": "practice1", "goalpost-practice-lod2.glb": "practice2",
	}
	for file, key := range names {
		if skip[file] {
			continue
		}
		mm := m
		if strings.HasPrefix(key, "practice") {
			mm.pad = false
		}
		if strings.HasSuffix(key, "2") {
			mm.pad, mm.base = false, false
		}
		if err := os.WriteFile(filepath.Join(dir, file), encode(t, mm.document()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func command(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Command(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestCommandExitCodes(t *testing.T) {
	dir := t.TempDir()
	src := "../testdata/syntax/goalpost.gw"
	acc := "../testdata/syntax/goalpost.accept.gw"

	// Usage.
	if code, _, _ := command(t); code != ExitUsage {
		t.Errorf("no args: exit %d, want 2", code)
	}
	if code, _, _ := command(t, src, acc); code != ExitUsage {
		t.Errorf("no --glb-dir: exit %d, want 2", code)
	}
	if code, _, _ := command(t, src, acc, "--glb-dir", dir, "--nope"); code != ExitUsage {
		t.Errorf("unknown flag: exit %d, want 2", code)
	}

	// All the owner's expects pass but the outline is unavailable: exit 3.
	writeGLBs(t, dir, defaultModel(), nil)
	code, out, errs := command(t, src, acc, "--glb-dir", dir, "--lod0-error", "2.99mm")
	if code != ExitUnavailable {
		t.Fatalf("exit %d, want 3\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}
	if !strings.Contains(out, "pass 54, fail 0, refused 0, unavailable 6, missing 0") {
		t.Errorf("summary missing:\n%s", out)
	}

	// Without --lod0-error the level-0 gap cannot read lod_error: refused, exit 1.
	code, out, errs = command(t, src, acc, "--glb-dir", dir)
	if code != ExitFail || !strings.Contains(errs, "lod_error is not known") {
		t.Errorf("no lod0 error: exit %d\n%s", code, errs)
	}

	// JSON mode.
	code, out, _ = command(t, src, acc, "--glb-dir="+dir, "--lod0-error=2.99mm", "--json")
	if code != ExitUnavailable {
		t.Errorf("json: exit %d", code)
	}
	var rep Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil || rep.Totals.Pass != 54 || len(rep.Levels) != 6 {
		t.Errorf("json report: %v %+v", err, rep.Totals)
	}
	if rep.Levels[0].Output != "stadium" || rep.Levels[0].File != "goalpost.glb" {
		t.Errorf("first level %+v", rep.Levels[0])
	}

	// An accept file with no outline passes outright: exit 0.
	noOutline := filepath.Join(t.TempDir(), "a.accept.gw")
	text, _ := os.ReadFile(acc)
	var kept []string
	for _, line := range strings.Split(string(text), "\n") {
		if !strings.Contains(line, "outline(") {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(noOutline, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, errs := command(t, src, noOutline, "--glb-dir", dir, "--lod0-error", "2.99mm"); code != ExitPass {
		t.Errorf("no outline: exit %d\n%s\n%s", code, out, errs)
	}

	// One file missing: exit 1, and the message names the file.
	dir2 := t.TempDir()
	writeGLBs(t, dir2, defaultModel(), map[string]bool{"goalpost-lod1.glb": true})
	code, _, errs = command(t, src, noOutline, "--glb-dir", dir2, "--lod0-error", "2.99mm")
	if code != ExitFail || !strings.Contains(errs, "goalpost-lod1.glb") {
		t.Errorf("missing file: exit %d\n%s", code, errs)
	}

	// The moved-upright control fails with the measured gap in the message.
	dir3 := t.TempDir()
	m := defaultModel()
	m.movedRight = 0.02
	writeGLBs(t, dir3, m, nil)
	code, _, errs = command(t, src, noOutline, "--glb-dir", dir3, "--lod0-error", "2.99mm")
	if code != ExitFail || !strings.Contains(errs, "5.66m") || !strings.Contains(errs, "error GW-ACCEPT") {
		t.Errorf("moved upright: exit %d\n%s", code, errs)
	}
	// The failure is located at the expect row of the acceptance file.
	if !strings.Contains(errs, "goalpost.accept.gw") && !strings.Contains(errs, "a.accept.gw") {
		t.Errorf("no file name in diagnostics:\n%s", errs)
	}
}
