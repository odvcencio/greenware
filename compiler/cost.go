package compiler

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

const unitsRef = units.KRef

// The cost ledger: schedule `greenware.cost/1`, compiler.md section 3.1 of the
// design documents.
//
// A build reserves a fixed price for each operation it will run, in a fixed
// order, before it runs anything. A ticket buys a class of work, not the work
// observed inside the class, so a faster machine, a cache hit or a smaller
// input never lowers a price. The first ticket that does not fit under the
// ceiling stops the build with GW-LIMIT. Nothing is written for such a build.
//
// What this package enforces: the ticket prices and their order, the ceiling
// (`total_work`), and these class bounds: sealed input bytes, the levels,
// parameters, materials and expanded items of one output, the non-image
// expectations of one level, the final triangles of one level file and the
// size of one level file. What it does not check yet: syntax leaves and depth
// of the source, corners, shells and material groups per level, intermediate
// (discarded) triangles, accessor counts of a decoded file and the pixel size
// of an outline reference. Those bounds need counters that the kernel does not
// expose.

// CostVersion names the ticket schedule written into every receipt.
const CostVersion = "greenware.cost/1"

// DefaultMaxTotalWork is the edition's default `total_work` ceiling.
const DefaultMaxTotalWork uint64 = 20_000_000

// Ticket names, in the order a build reserves them (compiler.md 3.1).
const (
	TicketSnapshot = "snapshot"
	TicketPlan     = "plan"
	TicketSurface  = "surface"
	TicketEncode   = "encode"
	TicketDecode   = "decode"
	TicketMeasure  = "measure"
	TicketOutline  = "outline"
	TicketPublish  = "publish"
)

// Tickets of the helmet profile (milestone M3). compiler.md 3.1 qualifies the
// M2 profile only and says to register analogous bounded tickets before a
// helmet build is admitted, but it does not name or price them. These names,
// prices and bounds are an assumption of this compiler, kept apart from the
// M2 schedule so that the goal post's receipt is unchanged:
//
//   - select, once per selected output that has a runtime `when`. It buys the
//     enumeration of the jointly dependent discrete conditions for the worst
//     look and the writing of the visibility bindings.
//   - surface_branches, per level of an output that uses loft, ellipsoid or
//     tube or has a runtime `when`. It replaces `surface` for that output. It
//     admits twice the triangles, because the file stores every branch.
//
// The look adds two more (milestone M3: atlas and painter, default-colour
// fallback and encoded images). They are also an assumption of this
// compiler:
//
//   - paint, once per atlas of a selected output that a drawn material
//     samples. It buys painting the atlas on the CPU, the default-colour
//     fallback image of a palette atlas, and encoding the PNG bytes of both.
//     The class is an image of at most 65,536 texels (256 KiB decoded), at
//     most 64 cells and at most 20,000,000 scalar painter operations (the
//     limit P of the language reference); the price is 250,000 units.
//   - encode_images, per level file of an output with images. It replaces
//     `encode` for that output: packing, hashing and length verification of a
//     self-contained GLB with at most 4 embedded PNG images and at most
//     192 KiB in all; the price is 200,000 units (the `encode` price plus
//     50,000 for each of two images). The image bytes are produced by `paint`;
//     the ticket covers embedding them.
const (
	TicketSelect          = "select"
	TicketSurfaceBranches = "surface_branches"
	TicketPaint           = "paint"
	TicketEncodeImages    = "encode_images"
)

// Class bounds of the M2 tickets (compiler.md 3.1).
const (
	maxSnapshotInputs  = 64
	maxSnapshotBytes   = 4 << 20
	maxSnapshotFile    = 2 << 20
	maxPlanLevels      = 8
	maxPlanItems       = 4096
	maxPlanParams      = 64
	maxPlanMaterials   = 16
	maxSurfaceTris     = 2048
	maxEncodeBytes     = 96 << 10
	maxMeasureExpects  = 16
	maxExpandDepth     = 64
	saturatedItemCount = math.MaxInt32

	// The helmet profile's classes (see TicketSelect).
	maxSurfaceBranchTris = 4096
	maxSelectBranches    = 16
	maxSelectParams      = 8

	// The look's classes (see TicketPaint, TicketEncodeImages).
	maxPaintTexels      = 65536
	maxPaintCells       = 64
	maxPaintOps         = 20_000_000
	maxEncodeImageBytes = 192 << 10
	maxEncodeImageCount = 4
)

// TicketClass is one row of the registered schedule.
type TicketClass struct {
	Name  string
	Units uint64 // work units charged per ticket
	Per   string // what one ticket buys
	Class string // the bound a ticket's operand must meet
}

var ticketTable = []TicketClass{
	{TicketSnapshot, 1_000_000, "once per invocation", "at most 64 inputs, 4 MiB in all, 2 MiB per file"},
	{TicketPlan, 100_000, "selected output", "at most 8 levels, 4096 expanded items, 64 parameters, 16 materials"},
	{TicketSurface, 200_000, "level", "at most 2048 triangles in the level"},
	{TicketEncode, 100_000, "level file", "at most 96 KiB per file"},
	{TicketDecode, 150_000, "level file the acceptance reads", "the encode bounds"},
	{TicketMeasure, 300_000, "level the acceptance reads", "at most 16 non-image expectations"},
	{TicketOutline, 500_000, "outline comparison", "one reference, one render"},
	{TicketPublish, 50_000, "once per invocation", "the staged files of the build"},
}

// ticketTableBranches is the helmet profile's additional rows, in reservation
// order: select follows plan, and surface_branches stands where surface does.
var ticketTableBranches = []TicketClass{
	{TicketSelect, 100_000, "selected output with a runtime `when`", "at most 16 branches, 8 tested parameters and 4096 assignments"},
	{TicketSurfaceBranches, 400_000, "level of an output with loft, ellipsoid, tube or runtime branches", "at most 4096 stored triangles in the level, every branch counted"},
}

// ticketTableLook is the look's additional rows: paint follows select, and
// encode_images stands where encode does.
var ticketTableLook = []TicketClass{
	{TicketPaint, 250_000, "atlas of a selected output that a drawn material samples", "at most 65,536 texels, 64 cells and 20,000,000 scalar painter operations"},
	{TicketEncodeImages, 200_000, "level file of an output with images", "at most 4 embedded images and 192 KiB per file"},
}

// Tickets returns the registered M2 schedule in reservation order.
func Tickets() []TicketClass { return append([]TicketClass(nil), ticketTable...) }

// BranchTickets returns the helmet profile's additional tickets.
func BranchTickets() []TicketClass { return append([]TicketClass(nil), ticketTableBranches...) }

// LookTickets returns the tickets of the atlas, painter and image classes.
func LookTickets() []TicketClass { return append([]TicketClass(nil), ticketTableLook...) }

func ticketUnits(name string) uint64 {
	for _, t := range ticketTable {
		if t.Name == name {
			return t.Units
		}
	}
	for _, t := range ticketTableBranches {
		if t.Name == name {
			return t.Units
		}
	}
	for _, t := range ticketTableLook {
		if t.Name == name {
			return t.Units
		}
	}
	panic("compiler: unregistered ticket " + name)
}

// usesBranchClass reports whether an output belongs to the helmet profile: it
// builds a loft, an ellipsoid or a tube, or a runtime `when` selects a branch.
func usesBranchClass(r *resolve.Resolved) bool {
	found := false
	var walk func(n *resolve.Node, depth int)
	defs := map[string]*resolve.Node{}
	for _, d := range r.Definitions {
		defs[d.Name] = d
	}
	walk = func(n *resolve.Node, depth int) {
		if n == nil || found || depth > maxExpandDepth {
			return
		}
		switch n.Kind {
		case "loft", "ellipsoid", "tube":
			found = true
		case "when":
			if n.Cond != nil {
				found = true
			}
		case "instance":
			of, _ := n.Get("of")
			walk(defs[of.Str], depth+1)
		}
		for _, k := range n.Children {
			walk(k, depth+1)
		}
		for _, k := range n.Else {
			walk(k, depth+1)
		}
	}
	walk(r.Root, 0)
	for _, d := range r.Definitions {
		walk(d, 0)
	}
	return found
}

// branchCount counts the runtime `when` nodes of an output and lists the
// parameters they test.
func branchCount(r *resolve.Resolved) (branches int, params []string) {
	set := map[string]bool{}
	var walk func(n *resolve.Node, depth int)
	walk = func(n *resolve.Node, depth int) {
		if n == nil || depth > maxExpandDepth {
			return
		}
		if n.Kind == "when" && n.Cond != nil {
			branches++
			set[n.Cond.Param] = true
		}
		for _, k := range n.Children {
			walk(k, depth+1)
		}
		for _, k := range n.Else {
			walk(k, depth+1)
		}
	}
	walk(r.Root, 0)
	for _, d := range r.Definitions {
		walk(d, 0)
	}
	for p := range set {
		params = append(params, p)
	}
	sort.Strings(params)
	return branches, params
}

// Policy is the production policy a build runs under.
type Policy struct {
	// MaxTotalWork is the `total_work` ceiling in work units. Zero means
	// DefaultMaxTotalWork.
	MaxTotalWork uint64
}

func (p Policy) limit() uint64 {
	if p.MaxTotalWork == 0 {
		return DefaultMaxTotalWork
	}
	return p.MaxTotalWork
}

// Options are the additional inputs of BuildWith.
type Options struct {
	Policy Policy
	// Accept is the owner's acceptance document, lowered. The build does not
	// run it (greenware accept does); it reserves the decode, measure and
	// outline tickets of the run it plans. Nil means no acceptance step.
	Accept *ast.File
}

// Charge is one reserved ticket. Output, Level and Expect are the absent
// components of the ticket identity when empty or nil.
type Charge struct {
	Ticket string `json:"ticket"`
	Output string `json:"output,omitempty"`
	// Row names the item a ticket of a kind with several per output buys: the
	// atlas of a `paint` ticket.
	Row   string `json:"row,omitempty"`
	Level *int   `json:"level,omitempty"`
	// Expect is the source ordinal, from 0, of the acceptance expect that asked
	// for an outline comparison.
	Expect *int   `json:"expect,omitempty"`
	Units  uint64 `json:"units"`
}

func (c Charge) String() string {
	var b strings.Builder
	b.WriteString(c.Ticket)
	if c.Output != "" {
		b.WriteString(" " + c.Output)
	}
	if c.Row != "" {
		b.WriteString(" " + c.Row)
	}
	if c.Level != nil {
		b.WriteString(" level " + strconv.Itoa(*c.Level))
	}
	if c.Expect != nil {
		b.WriteString(" expect " + strconv.Itoa(*c.Expect))
	}
	return b.String()
}

// Refusal says which ticket stopped the build.
type Refusal struct {
	Charge
	// Requested and Remaining are work units. For a class-bound refusal they
	// are zero and Bound says what the operand exceeded.
	Requested uint64 `json:"requested"`
	Remaining uint64 `json:"remaining"`
	Bound     string `json:"bound,omitempty"`
}

// Receipt is the ordered list of tickets a build reserved. It holds no time,
// path or host detail: the same inputs give the same receipt, with or without
// a cache.
type Receipt struct {
	Version string   `json:"version"`
	Limit   uint64   `json:"limit"`
	Total   uint64   `json:"total_work"`
	Charges []Charge `json:"tickets"`
	// Refused is set, on a receipt that BuildWith returns with a refusal, to
	// the first ticket that did not fit or whose class bound was exceeded.
	Refused *Refusal `json:"refused,omitempty"`
}

func intp(i int) *int { return &i }

// ledger reserves tickets against a ceiling.
type ledger struct {
	rc *Receipt
}

func newLedger(limit uint64) *ledger {
	return &ledger{rc: &Receipt{Version: CostVersion, Limit: limit, Charges: []Charge{}}}
}

// reserve adds the charge, or returns the refusal when it does not fit.
func (l *ledger) reserve(c Charge) *Refusal {
	remaining := l.rc.Limit - l.rc.Total // Total never exceeds Limit
	if c.Units > remaining {
		return &Refusal{Charge: c, Requested: c.Units, Remaining: remaining}
	}
	l.rc.Total += c.Units
	l.rc.Charges = append(l.rc.Charges, c)
	return nil
}

func limitDiag(sp diag.Span, r *Refusal, limit uint64) diag.Diagnostic {
	if r.Bound != "" {
		return diag.New(diag.CodeLimit, sp, "the `%s` ticket (%s) cannot take this operand: %s", r.Ticket, r.Charge, r.Bound)
	}
	return diag.New(diag.CodeLimit, sp, "the `%s` ticket (%s) needs %d work units, but %d remain of the `total_work` ceiling %d; nothing was built or written",
		r.Ticket, r.Charge, r.Requested, r.Remaining, limit)
}

// ---- the planned acceptance run -----------------------------------------------

// acceptLevel is what the acceptance reads of one output level.
type acceptLevel struct {
	output   string
	level    int
	nonImage int
	outlines []int // source ordinals of the outline comparisons
}

// planAccept lists, for each built output and level the acceptance selects,
// how many non-image expectations and which outline comparisons it asks for.
// levels maps an output name to its number of levels. Output and level order
// is by output name, then level.
func planAccept(f *ast.File, levels map[string]int) []acceptLevel {
	if f == nil {
		return nil
	}
	var names []string
	for n := range levels {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []acceptLevel
	ord := 0
	for _, acc := range f.Accepts {
		ordinals := map[*ast.Expect]int{}
		numberExpects(acc.Items, &ord, ordinals)
		outs := names
		if s := ast.FindSetting(acc.Settings, "outputs"); s != nil {
			if sel, ok := nameList(s.Value); ok {
				outs = nil
				for _, n := range names {
					if contains(sel, n) {
						outs = append(outs, n)
					}
				}
			}
		}
		for _, o := range outs {
			for _, lv := range selectedLevels(acc, levels[o]) {
				al := acceptLevel{output: o, level: lv}
				walkPlan(acc.Items, o, lv, ordinals, &al)
				out = append(out, al)
			}
		}
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// nameList reads `[a, b]`, a single name or string. `all` selects everything.
func nameList(e ast.Expr) ([]string, bool) {
	switch v := e.(type) {
	case *ast.Name:
		if v.Name == "all" {
			return nil, false
		}
		return []string{v.Name}, true
	case *ast.StringLit:
		return []string{v.Value}, true
	case *ast.List:
		var out []string
		for _, el := range v.Elems {
			switch x := el.(type) {
			case *ast.Name:
				out = append(out, x.Name)
			case *ast.StringLit:
				out = append(out, x.Value)
			}
		}
		return out, true
	}
	return nil, false
}

// selectedLevels follows the acceptance file's `lods` setting: `all` or none
// means every level of the output.
func selectedLevels(acc *ast.Accept, n int) []int {
	all := make([]int, n)
	for i := range all {
		all[i] = i
	}
	s := ast.FindSetting(acc.Settings, "lods")
	if s == nil {
		return all
	}
	var want []int
	num := func(e ast.Expr) {
		if x, ok := e.(*ast.NumberLit); ok && x.Value.Num == math.Trunc(x.Value.Num) {
			want = append(want, int(x.Value.Num))
		}
	}
	switch v := s.Value.(type) {
	case *ast.List:
		for _, el := range v.Elems {
			num(el)
		}
	case *ast.NumberLit:
		num(v)
	}
	if len(want) == 0 {
		return all
	}
	sort.Ints(want)
	var out []int
	for _, l := range want {
		if l >= 0 && l < n && (len(out) == 0 || out[len(out)-1] != l) {
			out = append(out, l)
		}
	}
	return out
}

// numberExpects gives each expect its source ordinal, depth first, `then`
// before `else`.
func numberExpects(items []ast.AcceptItem, next *int, m map[*ast.Expect]int) {
	for _, it := range items {
		switch x := it.(type) {
		case *ast.Expect:
			m[x] = *next
			*next++
		case *ast.AcceptWhen:
			numberExpects(x.Then, next, m)
			numberExpects(x.Else, next, m)
		}
	}
}

func walkPlan(items []ast.AcceptItem, output string, lod int, ord map[*ast.Expect]int, al *acceptLevel) {
	for _, it := range items {
		switch x := it.(type) {
		case *ast.Expect:
			n := countCalls(x.Cond, "outline")
			for i := 0; i < n; i++ {
				al.outlines = append(al.outlines, ord[x])
			}
			if n == 0 {
				al.nonImage++
			}
		case *ast.AcceptWhen:
			// A condition this planner cannot decide charges both branches:
			// the ticket reserves a class and must not undercount.
			v, ok := evalWhen(x.Cond, output, lod)
			switch {
			case !ok:
				walkPlan(x.Then, output, lod, ord, al)
				walkPlan(x.Else, output, lod, ord, al)
			case v.b:
				walkPlan(x.Then, output, lod, ord, al)
			default:
				walkPlan(x.Else, output, lod, ord, al)
			}
		}
	}
}

func countCalls(e ast.Expr, fn string) int {
	switch x := e.(type) {
	case *ast.Call:
		n := 0
		if x.Func == fn {
			n = 1
		}
		for _, a := range x.Args {
			n += countCalls(a.Value, fn)
		}
		return n
	case *ast.Binary:
		return countCalls(x.L, fn) + countCalls(x.R, fn)
	case *ast.Unary:
		return countCalls(x.X, fn)
	case *ast.Paren:
		return countCalls(x.X, fn)
	case *ast.If:
		return countCalls(x.Cond, fn) + countCalls(x.Then, fn) + countCalls(x.Else, fn)
	case *ast.Index:
		return countCalls(x.X, fn) + countCalls(x.I, fn)
	case *ast.Member:
		return countCalls(x.X, fn)
	case *ast.Tuple:
		n := 0
		for _, el := range x.Elems {
			n += countCalls(el, fn)
		}
		return n
	case *ast.List:
		n := 0
		for _, el := range x.Elems {
			n += countCalls(el, fn)
		}
		return n
	}
	return 0
}

// wv is a value of the small `when` evaluator: a name, a number or a switch.
type wv struct {
	kind byte // 'n' name, '#' number, 'b' switch
	s    string
	f    float64
	b    bool
}

// evalWhen decides a `when` condition made of `output`, `lod`, names, numbers,
// comparisons, `!`, `&&` and `||`. It reports false for anything else.
func evalWhen(e ast.Expr, output string, lod int) (wv, bool) {
	switch x := e.(type) {
	case *ast.Paren:
		return evalWhen(x.X, output, lod)
	case *ast.BoolLit:
		return wv{kind: 'b', b: x.Value}, true
	case *ast.NumberLit:
		return wv{kind: '#', f: x.Value.Num}, true
	case *ast.Name:
		switch x.Name {
		case "output":
			return wv{kind: 'n', s: output}, true
		case "lod":
			return wv{kind: '#', f: float64(lod)}, true
		}
		return wv{kind: 'n', s: x.Name}, true
	case *ast.Unary:
		if x.Op != "!" {
			return wv{}, false
		}
		v, ok := evalWhen(x.X, output, lod)
		if !ok || v.kind != 'b' {
			return wv{}, false
		}
		return wv{kind: 'b', b: !v.b}, true
	case *ast.Binary:
		l, lok := evalWhen(x.L, output, lod)
		r, rok := evalWhen(x.R, output, lod)
		switch x.Op {
		case "&&":
			switch {
			case lok && l.kind == 'b' && !l.b, rok && r.kind == 'b' && !r.b:
				return wv{kind: 'b'}, true
			case lok && rok && l.kind == 'b' && r.kind == 'b':
				return wv{kind: 'b', b: true}, true
			}
			return wv{}, false
		case "||":
			switch {
			case lok && l.kind == 'b' && l.b, rok && r.kind == 'b' && r.b:
				return wv{kind: 'b', b: true}, true
			case lok && rok && l.kind == 'b' && r.kind == 'b':
				return wv{kind: 'b'}, true
			}
			return wv{}, false
		}
		if !lok || !rok || l.kind != r.kind || l.kind == 'b' {
			return wv{}, false
		}
		var cmp int
		if l.kind == 'n' {
			cmp = strings.Compare(l.s, r.s)
			if x.Op != "==" && x.Op != "!=" {
				return wv{}, false
			}
		} else {
			switch {
			case l.f < r.f:
				cmp = -1
			case l.f > r.f:
				cmp = 1
			}
		}
		var res bool
		switch x.Op {
		case "==":
			res = cmp == 0
		case "!=":
			res = cmp != 0
		case "<":
			res = cmp < 0
		case "<=":
			res = cmp <= 0
		case ">":
			res = cmp > 0
		case ">=":
			res = cmp >= 0
		default:
			return wv{}, false
		}
		return wv{kind: 'b', b: res}, true
	}
	return wv{}, false
}

// ---- the plan of a build -------------------------------------------------------

// resolvedOutput is one output after the front end, with what the ledger needs.
type resolvedOutput struct {
	name   string
	r      *resolve.Resolved
	levels int
}

// schedule lists the tickets of the planned run in reservation order:
// by phase in table order, then output name, then level, then expect ordinal.
func schedule(outs []resolvedOutput, acc []acceptLevel) []Charge {
	sorted := append([]resolvedOutput(nil), outs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	var cs []Charge
	add := func(ticket, output string, level, expect *int) {
		cs = append(cs, Charge{Ticket: ticket, Output: output, Level: level, Expect: expect, Units: ticketUnits(ticket)})
	}
	addRow := func(ticket, output, row string) {
		cs = append(cs, Charge{Ticket: ticket, Output: output, Row: row, Units: ticketUnits(ticket)})
	}
	add(TicketSnapshot, "", nil, nil)
	for _, o := range sorted {
		add(TicketPlan, o.name, nil, nil)
	}
	for _, o := range sorted {
		if b, _ := branchCount(o.r); b > 0 {
			add(TicketSelect, o.name, nil, nil)
		}
	}
	for _, o := range sorted {
		for _, a := range atlasesUsed(o.r) {
			addRow(TicketPaint, o.name, a.Name)
		}
	}
	for _, phase := range []string{TicketSurface, TicketEncode} {
		for _, o := range sorted {
			ticket := phase
			if phase == TicketSurface && usesBranchClass(o.r) {
				ticket = TicketSurfaceBranches
			}
			if phase == TicketEncode && len(atlasesUsed(o.r)) > 0 {
				ticket = TicketEncodeImages
			}
			for lv := 0; lv < o.levels; lv++ {
				add(ticket, o.name, intp(lv), nil)
			}
		}
	}
	for _, phase := range []string{TicketDecode, TicketMeasure} {
		for _, a := range acc {
			add(phase, a.output, intp(a.level), nil)
		}
	}
	for _, a := range acc {
		for _, e := range a.outlines {
			add(TicketOutline, a.output, intp(a.level), intp(e))
		}
	}
	add(TicketPublish, "", nil, nil)
	return cs
}

type accKey struct {
	output string
	level  int
}

// classBound returns what a ticket's operand exceeds, or "" when it fits. It
// reads only facts known before the operation runs.
func classBound(c Charge, outs map[string]resolvedOutput, snapshotBytes []int, acc map[accKey]acceptLevel) string {
	switch c.Ticket {
	case TicketSnapshot:
		total := 0
		for _, n := range snapshotBytes {
			total += n
			if n > maxSnapshotFile {
				return fmt.Sprintf("a source file of %d bytes exceeds the %d bytes allowed per file", n, maxSnapshotFile)
			}
		}
		if len(snapshotBytes) > maxSnapshotInputs {
			return fmt.Sprintf("%d inputs exceed the %d allowed", len(snapshotBytes), maxSnapshotInputs)
		}
		if total > maxSnapshotBytes {
			return fmt.Sprintf("%d bytes of input exceed the %d allowed", total, maxSnapshotBytes)
		}
	case TicketPlan:
		o := outs[c.Output]
		switch {
		case o.levels > maxPlanLevels:
			return fmt.Sprintf("output %s has %d levels; the class allows %d", c.Output, o.levels, maxPlanLevels)
		case len(o.r.Params) > maxPlanParams:
			return fmt.Sprintf("output %s has %d parameters; the class allows %d", c.Output, len(o.r.Params), maxPlanParams)
		case len(o.r.Materials) > maxPlanMaterials:
			return fmt.Sprintf("output %s has %d materials; the class allows %d", c.Output, len(o.r.Materials), maxPlanMaterials)
		}
		if n := expandedItems(o.r); n > maxPlanItems {
			return fmt.Sprintf("output %s expands to %s items; the class allows %d", c.Output, itemText(n), maxPlanItems)
		}
	case TicketSelect:
		o := outs[c.Output]
		b, params := branchCount(o.r)
		if b > maxSelectBranches {
			return fmt.Sprintf("output %s has %d selectable branches; the class allows %d", c.Output, b, maxSelectBranches)
		}
		if len(params) > maxSelectParams {
			return fmt.Sprintf("output %s tests %d runtime parameters; the class allows %d", c.Output, len(params), maxSelectParams)
		}
		if _, ok := resolve.Assignments(o.r.Params, params); !ok {
			return fmt.Sprintf("output %s has more than %d assignments of its tested parameters; the class allows %d", c.Output, maxAssignments, maxAssignments)
		}
	case TicketPaint:
		a := outs[c.Output].r.Atlas(c.Row)
		if a == nil {
			break
		}
		if texels := a.Layout.Width() * a.Layout.Height(); texels > maxPaintTexels {
			return fmt.Sprintf("the atlas %s is %d x %d texels (%d); the class allows %d", a.Name, a.Layout.Width(), a.Layout.Height(), texels, maxPaintTexels)
		}
		if len(a.Cells) > maxPaintCells {
			return fmt.Sprintf("the atlas %s has %d cells; the class allows %d", a.Name, len(a.Cells), maxPaintCells)
		}
		if a.Ops > maxPaintOps {
			return fmt.Sprintf("the atlas %s needs %d scalar painter operations; the class allows %d", a.Name, a.Ops, maxPaintOps)
		}
	case TicketMeasure:
		if a := acc[accKey{c.Output, *c.Level}]; a.nonImage > maxMeasureExpects {
			return fmt.Sprintf("output %s level %d has %d non-image expectations; the class allows %d", c.Output, *c.Level, a.nonImage, maxMeasureExpects)
		}
	}
	return ""
}

func itemText(n int) string {
	if n >= saturatedItemCount {
		return "more than " + strconv.Itoa(saturatedItemCount)
	}
	return strconv.Itoa(n)
}

// expandedItems counts the occurrences an output expands to, with copies
// multiplied out, before anything is allocated: parts, shapes, sockets,
// instances (with the part each places), mirrored sides, array copies, path
// rows and colliders. It saturates, so a huge nest costs no time.
func expandedItems(r *resolve.Resolved) int {
	defs := map[string]*resolve.Node{}
	for _, d := range r.Definitions {
		defs[d.Name] = d
	}
	total := 0
	var walk func(n *resolve.Node, mult, depth int)
	add := func(mult, k int) {
		if mult > saturatedItemCount/(k+1) {
			total = saturatedItemCount
			return
		}
		total += mult * k
		if total > saturatedItemCount {
			total = saturatedItemCount
		}
	}
	walk = func(n *resolve.Node, mult, depth int) {
		if n == nil || total >= saturatedItemCount || depth > maxExpandDepth {
			return
		}
		add(mult, 1+len(n.Rows))
		kidMult := mult
		switch n.Kind {
		case "mirror":
			kidMult = mult * 2
		case "array":
			c, _ := n.Get("count")
			copies := 1.0
			if len(c.Elems) > 0 {
				for _, e := range c.Elems {
					copies *= e.Num
				}
			} else if c.Num > 0 {
				copies = c.Num
			}
			if copies > saturatedItemCount || float64(mult)*copies > saturatedItemCount {
				total = saturatedItemCount
				return
			}
			kidMult = mult * int(copies)
		case "instance":
			of, _ := n.Get("of")
			walk(defs[of.Str], mult, depth+1)
		}
		for _, k := range n.Children {
			walk(k, kidMult, depth+1)
		}
		for _, k := range n.Else {
			walk(k, kidMult, depth+1)
		}
	}
	walk(r.Root, 1, 0)
	for _, c := range r.Collision {
		walk(c, 1, 0)
	}
	return total
}

// atlasesUsed lists, by name, the atlases that a drawn material of the output
// samples: the base texture of a material or of a Selena fallback, and the
// texture a Selena descriptor names.
func atlasesUsed(r *resolve.Resolved) []*resolve.Atlas {
	if len(r.Atlases) == 0 {
		return nil
	}
	reach := map[string]bool{}
	for _, m := range r.Look.Materials {
		reach[m] = true
	}
	seen := map[string]*resolve.Atlas{}
	var visit func(n *resolve.Node)
	visit = func(n *resolve.Node) {
		for _, f := range n.Fields {
			if f.Value.Kind != unitsRef {
				continue
			}
			name := f.Value.Str
			if a, painted := resolve.ParsePaintRef(name); painted {
				name = a
			}
			if at := r.Atlas(name); at != nil {
				seen[at.Name] = at
			}
		}
		for _, c := range n.Children {
			visit(c)
		}
	}
	for _, m := range r.Materials {
		if reach[m.Name] {
			visit(m)
		}
	}
	var names []string
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*resolve.Atlas, len(names))
	for i, n := range names {
		out[i] = seen[n]
	}
	return out
}
