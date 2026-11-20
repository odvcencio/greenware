package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/greenware/accept"
)

// Geometry controls of plan.md 4.2 step 2. Each control is a one-change copy
// of examples/goalpost/goalpost.gw under testdata/compiler/. The test builds
// it, writes the GLBs, runs the owner's acceptance file on the decoded bytes
// and compares the verdicts with the unchanged example. A control proves a
// check only if the right expect fails where the counts and anchors still
// pass, so every test also asserts what did NOT change.

const (
	connectedExpect = "the painted frame is one piece: post, crossbar and uprights touch"
	turfExpect      = "nothing below the turf"
	centreYExpect   = "crossbar 10 ft up"
	centreZExpect   = "crossbar centre above the end line"
)

// control is one built and measured copy of the goal post.
type control struct {
	product *Product
	report  *accept.Report
	code    int
}

// tris returns the triangle count of every file, in output and level order.
func (c *control) tris() []int {
	var out []int
	for _, o := range c.product.Outputs {
		for _, f := range o.Files {
			out = append(out, f.Triangles)
		}
	}
	return out
}

// runControl builds the file at path and runs the owner's acceptance on it.
func runControl(t testing.TB, path string) *control {
	t.Helper()
	p, dir := buildInto(t, path)
	code, rep := runAccept(t, path, dir)
	return &control{product: p, report: rep, code: code}
}

// verdict is one expect at one output and level.
type verdict struct {
	where    string // "stadium lod 0"
	verdict  accept.Verdict
	measures string
	openEdge int // value of the "open edges" measure, when the expect has one
	groups   int // value of the "contact groups" measure, when the expect has one
}

func (v verdict) String() string {
	return fmt.Sprintf("%s: %s [%s]", v.where, v.verdict, v.measures)
}

// verdicts returns every verdict of the expect with the given message.
func (c *control) verdicts(msg string) []verdict {
	var out []verdict
	for _, l := range c.report.Levels {
		for _, e := range l.Expects {
			if e.Message != msg {
				continue
			}
			v := verdict{where: fmt.Sprintf("%s lod %d", l.Output, l.LOD), verdict: e.Verdict}
			var ms []string
			for _, m := range e.Measures {
				ms = append(ms, m.Name+"="+m.Value)
				if strings.HasSuffix(m.Name, "open edges") {
					fmt.Sscan(m.Value, &v.openEdge)
				}
				if strings.HasSuffix(m.Name, "contact groups") {
					fmt.Sscan(m.Value, &v.groups)
				}
			}
			v.measures = strings.Join(ms, "; ")
			out = append(out, v)
		}
	}
	return out
}

// wantAll requires the expect to have been evaluated six times (two outputs,
// three levels) and every verdict to be want. The failure text carries the
// measured values.
func (c *control) wantAll(t *testing.T, msg string, want accept.Verdict) []verdict {
	t.Helper()
	vs := c.verdicts(msg)
	if len(vs) != 6 {
		t.Errorf("%q was evaluated %d times, want 6", msg, len(vs))
	}
	for _, v := range vs {
		if v.verdict != want {
			t.Errorf("%q at %s is %s, want %s; measured: %s", msg, v.where, v.verdict, want, v.measures)
		}
	}
	return vs
}

// variantSource rebuilds a control's text from the example: the example with
// one replacement, and the control's header comment after the first line. It
// is how the files in testdata/compiler were made, and TestControlFilesMatch
// checks that nobody edited them by hand.
func variantSource(t testing.TB, header, old, new string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.FromSlash(examplePath))
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if strings.Count(s, old) != 1 {
		t.Fatalf("the example holds %d copies of %q, want 1", strings.Count(s, old), old)
	}
	s = strings.Replace(s, old, new, 1)
	first, rest, _ := strings.Cut(s, "\n")
	return first + "\n\n" + strings.Trim(header, "\n") + "\n" + rest
}

const (
	crossbarCapOld = "      caps = on\n    }\n    socket crossbar_centre"
	uprightCapOld  = "        segments = 12\n      }\n      sphere {"
	postStartOld   = "        point (0, 0, setback)\n"
	postWholeOld   = "      caps = both                                 // closed ends, so the frame has no open edges\n"
)

var controlFiles = []struct {
	file, header, old, new string
}{
	{"goalpost-no-crossbar-cap.gw", `
// REGRESSION CONTROL, not a goal post to ship: examples/goalpost/goalpost.gw
// with the crossbar's end caps removed (` + "`caps = on` became `caps = off`" + `).
// Every point, socket and collider is where it was, so anchors and the gap
// still pass; the painted frame has open edges, so ` + "`closed`" + ` must fail.
`, crossbarCapOld, "      caps = off\n    }\n    socket crossbar_centre"},
	{"goalpost-no-upright-cap.gw", `
// REGRESSION CONTROL, not a goal post to ship: examples/goalpost/goalpost.gw
// with the end caps of both upright pipes removed (` + "`caps = off`" + ` added to the
// upright cylinder). Anchors and the gap are unchanged; ` + "`closed`" + ` must fail.
`, uprightCapOld, "        segments = 12\n        caps = off\n      }\n      sphere {"},
	{"goalpost-post-start-shifted.gw", `
// REGRESSION CONTROL, not a goal post to ship: examples/goalpost/goalpost.gw
// with the post sweep's first path point moved 3 cm sideways (x = 0.03). The
// path's end, the crossbar, every socket and every triangle count are where
// they were.
`, postStartOld, "        point (0.03, 0, setback)\n"},
	{"goalpost-post-off-crossbar.gw", `
// REGRESSION CONTROL, not a goal post to ship: examples/goalpost/goalpost.gw
// with the whole post sweep moved 7 cm toward +z (` + "`at = (0, 0, 0.07m)`" + `). Its
// end ring no longer reaches the 6 cm crossbar. Every socket, collider and
// triangle count is where it was; ` + "`connected`" + ` must fail.
`, postWholeOld, "      at = (0, 0, 0.07m)\n" + postWholeOld},
}

func TestControlFilesMatch(t *testing.T) {
	for _, c := range controlFiles {
		got, err := os.ReadFile(filepath.Join("..", "testdata", "compiler", c.file))
		if err != nil {
			t.Fatal(err)
		}
		if want := variantSource(t, c.header, c.old, c.new); string(got) != want {
			t.Errorf("testdata/compiler/%s is not the example with the one stated change", c.file)
		}
	}
}

func controlPath(name string) string { return "../testdata/compiler/" + name }

// The baseline all controls compare with: the owner's example passes every
// expect the evaluator can measure.
func baseline(t *testing.T) *control {
	t.Helper()
	c := runControl(t, examplePath)
	for _, msg := range []string{closedExpect, connectedExpect, gapExpect, socketsExpect, turfExpect, centreYExpect, centreZExpect} {
		c.wantAll(t, msg, accept.Pass)
	}
	return c
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Control 1, cap removal. Plan 4.2 step 2: closed fails with open edges;
// triangle counts change; anchors and sockets do not.
func TestControlCapRemoval(t *testing.T) {
	base := baseline(t)
	for _, f := range []struct {
		file string
		part string
	}{
		{"goalpost-no-crossbar-cap.gw", "crossbar"},
		{"goalpost-no-upright-cap.gw", "uprights"},
	} {
		t.Run(f.part, func(t *testing.T) {
			c := runControl(t, controlPath(f.file))
			for _, v := range c.wantAll(t, closedExpect, accept.Fail) {
				if v.openEdge <= 0 {
					t.Errorf("closed at %s fails with %d open edges, want more than 0; measured: %s", v.where, v.openEdge, v.measures)
				}
				t.Logf("closed at %s: %s", v.where, v.measures)
			}
			// What the check must not depend on: gap, anchors, connection.
			for _, msg := range []string{gapExpect, socketsExpect, centreYExpect, centreZExpect, turfExpect, connectedExpect} {
				c.wantAll(t, msg, accept.Pass)
			}
			if sameInts(c.tris(), base.tris()) {
				t.Errorf("triangle counts are unchanged at %v; removing caps must change them", c.tris())
			}
			t.Logf("triangles per file: example %v, %s caps off %v", base.tris(), f.part, c.tris())
			if c.code != accept.ExitFail {
				t.Errorf("acceptance exit %d, want %d", c.code, accept.ExitFail)
			}
		})
	}
}

// Control 2a, the post sweep's start moved 3 cm sideways. Plan 4.2 step 2
// predicts that connected or closed fails while sockets and counts pass. It
// does not: the strip stays attached at both ends and every edge keeps its
// two uses. What fails is "nothing below the turf", because the start ring is
// perpendicular to the now tilted first segment and its lower rim dips under
// y = 0. The test records that measured outcome.
func TestControlShiftedSweepStart(t *testing.T) {
	base := baseline(t)
	c := runControl(t, controlPath("goalpost-post-start-shifted.gw"))
	if !sameInts(c.tris(), base.tris()) {
		t.Errorf("triangle counts changed from %v to %v", base.tris(), c.tris())
	}
	for _, msg := range []string{socketsExpect, centreYExpect, centreZExpect, gapExpect} {
		c.wantAll(t, msg, accept.Pass)
	}
	// Measured: closed and connected still pass. The plan's prediction is not met.
	c.wantAll(t, closedExpect, accept.Pass)
	c.wantAll(t, connectedExpect, accept.Pass)
	for _, v := range c.wantAll(t, turfExpect, accept.Fail) {
		t.Logf("turf at %s: %s", v.where, v.measures)
	}
}

// Control 2b, the whole post sweep moved 7 cm toward +z, so its end ring
// stops 10 mm short of the 6 cm crossbar. Counts, sockets and the gap pass;
// the shells split into two contact groups and connected fails. closed
// still passes, because the post's own surface stays closed.
func TestControlShiftedSweepDetaches(t *testing.T) {
	base := baseline(t)
	c := runControl(t, controlPath("goalpost-post-off-crossbar.gw"))
	if !sameInts(c.tris(), base.tris()) {
		t.Errorf("triangle counts changed from %v to %v", base.tris(), c.tris())
	}
	for _, msg := range []string{socketsExpect, centreYExpect, centreZExpect, gapExpect, turfExpect, closedExpect} {
		c.wantAll(t, msg, accept.Pass)
	}
	for _, v := range c.wantAll(t, connectedExpect, accept.Fail) {
		if v.groups != 2 {
			t.Errorf("connected at %s measures %d contact groups, want 2; measured: %s", v.where, v.groups, v.measures)
		}
		t.Logf("connected at %s: %s", v.where, v.measures)
	}
	if c.code != accept.ExitFail {
		t.Errorf("acceptance exit %d, want %d", c.code, accept.ExitFail)
	}
}

// Controls 3 and 4 already exist: TestMovedUprightIsCaught (moved pipe, fixed
// sockets) and TestOriginalGoalPostFailsClosed (exact coincidence, the
// original sample).
