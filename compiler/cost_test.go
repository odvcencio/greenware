package compiler

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
)

// The O1 total of plan.md 4.2 and compiler.md 3.1 for the goal post: two
// outputs, six files, six outline comparisons.
const (
	o1Total       uint64 = 8_750_000
	o1TicketCount        = 1 + 2 + 6 + 6 + 6 + 6 + 6 + 1
)

func acceptFile(t testing.TB) *ast.File { return loadFile(t, acceptPath) }

func buildCost(t testing.TB, src *ast.File, limit uint64, acc *ast.File) (*Product, *Receipt, []diag.Diagnostic) {
	t.Helper()
	return BuildWith(src, "", Options{Policy: Policy{MaxTotalWork: limit}, Accept: acc})
}

func limitDiags(ds []diag.Diagnostic) []diag.Diagnostic {
	var out []diag.Diagnostic
	for _, d := range ds {
		if d.Code == diag.CodeLimit {
			out = append(out, d)
		}
	}
	return out
}

func TestTicketTableMatchesSchedule(t *testing.T) {
	// compiler.md 3.1, in reservation order.
	want := []struct {
		name  string
		units uint64
	}{
		{"snapshot", 1_000_000}, {"plan", 100_000}, {"surface", 200_000}, {"encode", 100_000},
		{"decode", 150_000}, {"measure", 300_000}, {"outline", 500_000}, {"publish", 50_000},
	}
	got := Tickets()
	if len(got) != len(want) {
		t.Fatalf("%d tickets, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Name != w.name || got[i].Units != w.units {
			t.Errorf("ticket %d is %s %d, want %s %d", i, got[i].Name, got[i].Units, w.name, w.units)
		}
	}
}

func TestCostGoalPostTotalIsPlanTotal(t *testing.T) {
	p, rc, ds := buildCost(t, loadFile(t, examplePath), 0, acceptFile(t))
	if p == nil {
		t.Fatalf("build failed: %v", ds)
	}
	if rc.Limit != DefaultMaxTotalWork {
		t.Errorf("a zero policy gives limit %d, want the default %d", rc.Limit, DefaultMaxTotalWork)
	}
	if rc.Total != o1Total {
		t.Errorf("total_work %d, want the plan's %d", rc.Total, o1Total)
	}
	if len(rc.Charges) != o1TicketCount {
		t.Errorf("%d tickets, want %d", len(rc.Charges), o1TicketCount)
	}
	// Per phase: the rows of the worked table in compiler.md 3.1.
	byTicket := map[string]struct {
		n     int
		units uint64
	}{}
	var sum uint64
	for _, c := range rc.Charges {
		e := byTicket[c.Ticket]
		e.n++
		e.units += c.Units
		byTicket[c.Ticket] = e
		sum += c.Units
	}
	if sum != rc.Total {
		t.Errorf("tickets sum to %d, receipt says %d", sum, rc.Total)
	}
	for name, want := range map[string]struct {
		n     int
		units uint64
	}{
		"snapshot": {1, 1_000_000}, "plan": {2, 200_000}, "surface": {6, 1_200_000}, "encode": {6, 600_000},
		"decode": {6, 900_000}, "measure": {6, 1_800_000}, "outline": {6, 3_000_000}, "publish": {1, 50_000},
	} {
		if got := byTicket[name]; got != want {
			t.Errorf("%s: %d tickets, %d units; want %d, %d", name, got.n, got.units, want.n, want.units)
		}
	}
	// Order: phase by phase, then output name, then level.
	var order []string
	for _, c := range rc.Charges {
		order = append(order, c.String())
	}
	wantOrder := []string{
		"snapshot", "plan practice", "plan stadium",
		"surface practice level 0", "surface practice level 1", "surface practice level 2",
		"surface stadium level 0", "surface stadium level 1", "surface stadium level 2",
	}
	for i, w := range wantOrder {
		if order[i] != w {
			t.Errorf("ticket %d is %q, want %q", i, order[i], w)
		}
	}
	if last := order[len(order)-1]; last != "publish" {
		t.Errorf("last ticket is %q, want publish", last)
	}
	// Every outline ticket names the acceptance's outline expect, the eighth.
	for _, c := range rc.Charges {
		if c.Ticket == TicketOutline && (c.Expect == nil || *c.Expect != 7) {
			t.Errorf("%s has expect %v, want ordinal 7", c, c.Expect)
		}
	}
	t.Logf("receipt: %d tickets, %d work units", len(rc.Charges), rc.Total)
}

func TestCostWithoutAcceptanceHasNoDecodeMeasureOutline(t *testing.T) {
	p, rc, ds := buildCost(t, loadFile(t, examplePath), 0, nil)
	if p == nil {
		t.Fatalf("%v", ds)
	}
	// snapshot + 2 plan + 6 surface + 6 encode + publish.
	want := uint64(1_000_000 + 2*100_000 + 6*200_000 + 6*100_000 + 50_000)
	if rc.Total != want {
		t.Errorf("total %d, want %d", rc.Total, want)
	}
	for _, c := range rc.Charges {
		switch c.Ticket {
		case TicketDecode, TicketMeasure, TicketOutline:
			t.Errorf("unexpected ticket %s without an acceptance file", c)
		}
	}
}

func TestCostExactCeilingPassesAndOneBelowRefusesPublish(t *testing.T) {
	src, acc := loadFile(t, examplePath), acceptFile(t)
	pass, passRc, ds := buildCost(t, src, o1Total, acc)
	if pass == nil {
		t.Fatalf("ceiling %d must pass: %v", o1Total, ds)
	}
	if passRc.Total != o1Total || passRc.Limit != o1Total || passRc.Refused != nil {
		t.Errorf("pass receipt: total %d limit %d refused %v", passRc.Total, passRc.Limit, passRc.Refused)
	}
	if len(pass.Outputs) != 2 {
		t.Errorf("%d outputs built", len(pass.Outputs))
	}

	p, rc, ds := buildCost(t, src, o1Total-1, acc)
	if p != nil {
		t.Fatal("ceiling 8,749,999 must not give a product")
	}
	r := rc.Refused
	if r == nil || r.Ticket != TicketPublish || r.Requested != 50_000 || r.Remaining != 49_999 {
		t.Fatalf("refusal %+v, want publish, 50000 requested, 49999 remaining", r)
	}
	ls := limitDiags(ds)
	if len(ls) != 1 || len(ds) != 1 {
		t.Fatalf("diagnostics %v, want exactly one GW-LIMIT", ds)
	}
	for _, w := range []string{"publish", "50000", "49999", "8749999"} {
		if !strings.Contains(ls[0].Message, w) {
			t.Errorf("message %q lacks %q", ls[0].Message, w)
		}
	}
	// The same receipt prefix as the passing run: the refusal is at the end.
	if len(rc.Charges) != o1TicketCount-1 {
		t.Errorf("refused receipt holds %d tickets, want %d", len(rc.Charges), o1TicketCount-1)
	}
	for i, c := range rc.Charges {
		if c.String() != passRc.Charges[i].String() || c.Units != passRc.Charges[i].Units {
			t.Errorf("ticket %d is %s, the passing run has %s", i, c, passRc.Charges[i])
		}
	}
	if rc.Total != o1Total-50_000 {
		t.Errorf("refused total %d, want %d", rc.Total, o1Total-50_000)
	}
}

// Every ticket is the first unaffordable one at the ceiling one unit below its
// running total, and affordable at the running total.
func TestCostEveryTicketBoundary(t *testing.T) {
	src, acc := loadFile(t, examplePath), acceptFile(t)
	_, full, _ := buildCost(t, src, o1Total, acc)
	var run uint64
	for i, c := range full.Charges {
		run += c.Units
		p, rc, ds := buildCost(t, src, run-1, acc)
		if p != nil || rc.Refused == nil {
			t.Fatalf("ticket %d (%s): ceiling %d did not refuse", i, c, run-1)
		}
		if rc.Refused.Charge.String() != c.String() || rc.Refused.Requested != c.Units || rc.Refused.Remaining != c.Units-1 {
			t.Errorf("ticket %d: refused %s requested %d remaining %d; want %s, %d, %d",
				i, rc.Refused.Charge, rc.Refused.Requested, rc.Refused.Remaining, c, c.Units, c.Units-1)
		}
		if len(rc.Charges) != i || len(limitDiags(ds)) != 1 {
			t.Errorf("ticket %d: %d tickets reserved before the refusal", i, len(rc.Charges))
		}
	}
}

func TestCostReceiptIsRepeatable(t *testing.T) {
	src, acc := loadFile(t, examplePath), acceptFile(t)
	a, ra, _ := buildCost(t, src, o1Total, acc)
	b, rb, _ := buildCost(t, src, o1Total, acc)
	ja, _ := json.Marshal(ra)
	jb, _ := json.Marshal(rb)
	if !bytes.Equal(ja, jb) {
		t.Errorf("two runs give different receipts:\n%s\n%s", ja, jb)
	}
	if !bytes.Equal(a.Record(), b.Record()) {
		t.Error("two runs give different build records")
	}
}

func TestCostIsInTheRecordAndNotInTheFiles(t *testing.T) {
	src := loadFile(t, examplePath)
	with, _, _ := buildCost(t, src, o1Total, acceptFile(t))
	without, _, _ := buildCost(t, src, 0, nil)
	var rec struct {
		Cost *Receipt `json:"cost"`
	}
	if err := json.Unmarshal(with.Record(), &rec); err != nil || rec.Cost == nil {
		t.Fatalf("no cost in the record: %v", err)
	}
	if rec.Cost.Version != CostVersion || rec.Cost.Total != o1Total || len(rec.Cost.Charges) != o1TicketCount {
		t.Errorf("record cost: %+v", rec.Cost)
	}
	for i, o := range with.Outputs {
		for j, f := range o.Files {
			if f.SHA256 != without.Outputs[i].Files[j].SHA256 {
				t.Errorf("%s differs between policies", f.Name)
			}
		}
	}
}

// ---- operands above a class bound ---------------------------------------------------

const tinyAsset = `greenware 1
asset a { root = a }
output demo { file = "a.glb" }
`

func lowered(t testing.TB, src string) *ast.File { return lowerSource(t, []byte(src)) }

func buildOne(t testing.TB, src string, limit uint64, acc *ast.File) (*Product, *Receipt, []diag.Diagnostic) {
	t.Helper()
	return BuildWith(lowered(t, src), "", Options{Policy: Policy{MaxTotalWork: limit}, Accept: acc})
}

func requireLimit(t *testing.T, ds []diag.Diagnostic, words ...string) {
	t.Helper()
	ls := limitDiags(ds)
	if len(ls) != 1 {
		t.Fatalf("diagnostics %v, want one GW-LIMIT", ds)
	}
	for _, w := range words {
		if !strings.Contains(ls[0].Message, w) {
			t.Errorf("message %q lacks %q", ls[0].Message, w)
		}
	}
}

func TestLimitOversizedArray(t *testing.T) {
	src := tinyAsset + `
part bolt { cylinder { radius = 0.01m height = 0.02m segments = 6 } }
part a {
  array bolts {
    mode = linear
    count = 10000000
    step = (0.03, 0, 0)
    instance { of = bolt }
  }
}
`
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	p, _, ds := buildOne(t, src, 0, nil)
	runtime.ReadMemStats(&after)
	if p != nil {
		t.Fatal("ten million copies must not build")
	}
	requireLimit(t, ds, "plan", "an array makes at most 4096 copies")
	// Ten million copies of even one triangle would take hundreds of MB.
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 64<<20 {
		t.Errorf("the refusal allocated %d bytes: the array was expanded first", grown)
	}
	// The catalogue fixture reports the same code.
	p, _, ds = BuildWith(loadFile(t, "../testdata/diag/GW-LIMIT.gw"), "", Options{})
	if p != nil {
		t.Fatal("the fixture built")
	}
	requireLimit(t, ds)
	if len(ds) != 1 {
		t.Errorf("fixture diagnostics %v, want only GW-LIMIT", ds)
	}
}

// Two arrays that each pass the per-array cap multiply past the plan class.
func TestLimitNestedArraysMultiply(t *testing.T) {
	src := tinyAsset + `
part cell { cylinder { radius = 0.01m height = 0.02m segments = 6 } }
part row {
  array cells {
    mode = linear
    count = 100
    step = (0.03, 0, 0)
    instance { of = cell }
  }
}
part a {
  array rows {
    mode = linear
    count = 100
    step = (0, 0, 0.03)
    instance { of = row }
  }
}
`
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	p, rc, ds := buildOne(t, src, 0, nil)
	runtime.ReadMemStats(&after)
	if p != nil {
		t.Fatal("10,000 expanded copies must not build")
	}
	requireLimit(t, ds, "plan", "output demo", "allows 4096")
	if rc.Refused == nil || rc.Refused.Ticket != TicketPlan || rc.Refused.Output != "demo" || rc.Refused.Bound == "" {
		t.Errorf("refusal %+v", rc.Refused)
	}
	if len(rc.Charges) != 1 || rc.Charges[0].Ticket != TicketSnapshot {
		t.Errorf("receipt %+v, want only the snapshot ticket", rc.Charges)
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 64<<20 {
		t.Errorf("the refusal allocated %d bytes", grown)
	}
	// A 10 x 10 nest is 100 copies of the row part and fits.
	ok := strings.NewReplacer("count = 100", "count = 10").Replace(src)
	if p, _, ds := buildOne(t, ok, 0, nil); p == nil {
		t.Errorf("10 x 10 copies must build: %v", ds)
	}
}

func TestLimitSurfaceTriangles(t *testing.T) {
	src := tinyAsset + `
part a { cylinder { radius = 0.1m height = 1m segments = 1100 } }
`
	p, rc, ds := buildOne(t, src, 0, nil)
	if p != nil {
		t.Fatal("a level of more than 2048 triangles must not build")
	}
	requireLimit(t, ds, "surface", "allows 2048")
	if rc.Refused == nil || rc.Refused.Ticket != TicketSurface || rc.Refused.Level == nil || *rc.Refused.Level != 0 {
		t.Errorf("refusal %+v", rc.Refused)
	}
}

func TestLimitSnapshotBytes(t *testing.T) {
	pad := strings.Repeat("// pad\n", (2<<20)/7+1) // just over 2 MiB
	src := tinyAsset + "part a { cylinder { radius = 0.1m height = 1m } }\n" + pad
	if len(src) <= 2<<20 {
		t.Fatalf("test source is %d bytes", len(src))
	}
	p, rc, ds := buildOne(t, src, 0, nil)
	if p != nil {
		t.Fatal("a source of more than 2 MiB must not build")
	}
	requireLimit(t, ds, "snapshot", "per file")
	if rc.Refused == nil || rc.Refused.Ticket != TicketSnapshot || len(rc.Charges) != 0 {
		t.Errorf("refusal %+v, tickets %v", rc.Refused, rc.Charges)
	}
	// The same asset without the padding builds.
	if p, _, ds := buildOne(t, tinyAsset+"part a { cylinder { radius = 0.1m height = 1m } }\n", 0, nil); p == nil {
		t.Errorf("%v", ds)
	}
}

func TestLimitMeasureExpectations(t *testing.T) {
	asset := tinyAsset + "part a { cylinder { radius = 0.1m height = 1m } }\n"
	acceptText := func(n int) string {
		var b strings.Builder
		b.WriteString("greenware 1\naccept a {\n  outputs = [demo]\n  lods = [0]\n")
		for i := 0; i < n; i++ {
			b.WriteString("  expect (bounds().min.y > -1mm, \"floor\")\n")
		}
		b.WriteString("}\n")
		return b.String()
	}
	if p, rc, ds := buildOne(t, asset, 0, lowered(t, acceptText(16))); p == nil {
		t.Errorf("16 expectations must fit: %v", ds)
	} else if rc.Total != 1_000_000+100_000+200_000+100_000+150_000+300_000+50_000 {
		t.Errorf("total %d", rc.Total)
	}
	p, rc, ds := buildOne(t, asset, 0, lowered(t, acceptText(17)))
	if p != nil {
		t.Fatal("17 non-image expectations must not build")
	}
	requireLimit(t, ds, "measure", "17 non-image expectations")
	if rc.Refused == nil || rc.Refused.Ticket != TicketMeasure {
		t.Errorf("refusal %+v", rc.Refused)
	}
}

// An outline under a `when` is charged only where the condition holds.
func TestCostOutlineFollowsWhen(t *testing.T) {
	acceptText := `greenware 1
accept goalpost {
  outputs = [stadium, practice]
  lods = all
  expect (bounds().min.y > -1mm, "floor")
  when output == stadium && lod < 2 {
    expect (outline(front, "ref/a.png", within = 3px) >= 0.95, "stadium front")
  } else {
    expect (outline(side, "ref/b.png", within = 3px) >= 0.95, "side")
  }
  when has_node(pad) {
    expect (outline(top, "ref/c.png", within = 3px) >= 0.95, "undecidable: both branches")
  }
}
`
	_, rc, ds := buildCost(t, loadFile(t, examplePath), 0, lowered(t, acceptText))
	if rc == nil || rc.Refused != nil {
		t.Fatalf("%v", ds)
	}
	count := map[string]int{}
	for _, c := range rc.Charges {
		if c.Ticket == TicketOutline {
			count[c.String()]++
		}
	}
	// stadium levels 0, 1: front (ordinal 1) and the undecidable top (ordinal 3);
	// stadium level 2 and the practice levels: side (ordinal 2) and top (3).
	want := map[string]int{
		"outline stadium level 0 expect 1": 1, "outline stadium level 0 expect 3": 1,
		"outline stadium level 1 expect 1": 1, "outline stadium level 1 expect 3": 1,
		"outline stadium level 2 expect 2": 1, "outline stadium level 2 expect 3": 1,
		"outline practice level 0 expect 2": 1, "outline practice level 0 expect 3": 1,
		"outline practice level 1 expect 2": 1, "outline practice level 1 expect 3": 1,
		"outline practice level 2 expect 2": 1, "outline practice level 2 expect 3": 1,
	}
	for k, v := range want {
		if count[k] != v {
			t.Errorf("%s: %d tickets, want %d", k, count[k], v)
		}
	}
	if len(count) != len(want) {
		t.Errorf("outline tickets %v", count)
	}
}
