package athleteparity

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

// DefaultHelmetYards is the helmet width of the game's bake (baseline at game
// commit 3848013). It stands in for a model that has no helmet shell, so that
// the helmet-width columns can still be printed; a report that uses it says so.
const DefaultHelmetYards = 0.270

// Options choose what Run measures.
type Options struct {
	Clip string  // the clip to pose; the game's tool uses "ready"
	T    float64 // the time in the clip, in seconds
	// HelmetYards replaces the model's helmet width when the model has no
	// helmet shell (0 means DefaultHelmetYards).
	HelmetYards float64
	// GatePx is the picture height of the silhouette gate (0 means 96).
	GatePx float64
	// NoGate skips the pixel gate.
	NoGate bool
}

// LedgerRow is one row of the ledger: the counts of the shapes in a part.
type LedgerRow struct {
	Name string
	T, V int
	Part *Part // the ceilings; nil for the unassigned row
}

// TargetRow is the reach of one morph target.
type TargetRow struct {
	Name  string
	Moved int
	Max   float64 // yards
}

// SilRow is one row of the silhouette table.
type SilRow struct {
	Name string
	Cols [SilColumns]float64
}

// Report is everything one run measures.
type Report struct {
	Name           string
	Vertices       int
	Triangles      int
	Shapes         int
	Morphs         int
	Ledger         []LedgerRow
	Unassigned     *LedgerRow
	UnassignedList []string
	Targets        []TargetRow
	Sil            []SilRow
	Gate           []GateResult
	GatePx         float64
	Warnings       []string
}

func (r *Report) warn(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	for _, w := range r.Warnings {
		if w == s {
			return
		}
	}
	r.Warnings = append(r.Warnings, s)
}

// Run measures the model: the ledger of its shapes, the silhouette rows of
// the looks and, unless Options.NoGate, the pixel gate.
func (m *Model) Run(looks *LookFile, opt Options) *Report {
	if opt.HelmetYards == 0 {
		opt.HelmetYards = DefaultHelmetYards
	}
	if opt.GatePx == 0 {
		opt.GatePx = 96
	}
	r := &Report{Name: m.Name, Vertices: len(m.Bind), Triangles: len(m.Tri), Shapes: len(m.Parts), Morphs: len(m.TargetNames), GatePx: opt.GatePx}
	for _, w := range m.Warnings {
		r.warn("%s", w)
	}
	m.ledger(r)
	m.reach(r)

	// What the looks need and the file lacks.
	wanted := map[string]bool{}
	scaled := map[string]bool{}
	var all []Look
	all = append(all, looks.Wearers...)
	all = append(all, looks.Roster...)
	for _, l := range all {
		for n, w := range l.ShapeWeights {
			if w != 0 {
				wanted[n] = true
			}
		}
		for n := range l.JointScales {
			scaled[n] = true
		}
	}
	var names []string
	for n := range wanted {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, ok := m.Targets[n]; !ok {
			r.warn("morph target %q is not in the file: its weight is ignored in every look", n)
		}
	}
	names = names[:0]
	for n := range scaled {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		found := false
		for _, nd := range m.Doc.Nodes {
			if nd.Name == n {
				found = true
			}
		}
		if !found {
			r.warn("joint %q (scaled by the look) is not a node of the file: its scale is ignored", n)
		}
	}
	clip := opt.Clip
	if clip != "" && !m.HasClip(clip) {
		r.warn("clip %q is not in the file: the figures are posed from the node transforms as stored", clip)
		clip = ""
	}
	for _, sh := range measured {
		have := false
		for i := range m.Bind {
			if m.VertexShell(i) == sh {
				have = true
				break
			}
		}
		if !have {
			r.warn("no shape maps to shell %q: the widths cut from it are 0 (shapes are matched by leaf name; see ShellName)", sh)
		}
	}

	posed := func(l Look) Figure { return m.PoseLook(l, clip, opt.T) }
	unit := func(w Widths) float64 {
		if w.Helmet == 0 {
			r.warn("the figure has no helmet width: the columns in helmet widths use %.3f yd (-helmet-yd)", opt.HelmetYards)
			return opt.HelmetYards
		}
		return w.Helmet
	}
	for _, l := range looks.Wearers {
		w := posed(l).Widths
		r.Sil = append(r.Sil, SilRow{Name: l.Name, Cols: w.Columns(unit(w))})
	}
	figs := map[string][]Figure{}
	for _, g := range Groups {
		var ws []Widths
		for _, l := range looks.Members(g) {
			f := posed(l)
			figs[l.Pos] = append(figs[l.Pos], f)
			ws = append(ws, f.Widths)
		}
		if len(ws) == 0 {
			continue
		}
		w := MedianWidths(ws)
		r.Sil = append(r.Sil, SilRow{Name: g.Name, Cols: w.Columns(unit(w))})
	}
	if !opt.NoGate {
		var heavy, light []Figure
		heavy = append(heavy, figs["OL"]...)
		heavy = append(heavy, figs["DL"]...)
		light = figs["WR"]
		r.Gate = m.Gate(heavy, light, opt.GatePx)
	}
	return r
}

// ledger counts the shapes by ledger part.
func (m *Model) ledger(r *Report) {
	rows := map[string]*LedgerRow{}
	for i := range Parts {
		rows[Parts[i].Name] = &LedgerRow{Name: Parts[i].Name, Part: &Parts[i]}
	}
	un := &LedgerRow{Name: Unassigned}
	unknown := map[string]bool{}
	for i, p := range m.Parts {
		row := rows[PartOfShell(m.Shell[i])]
		if row == nil {
			row = un
			unknown[p.Name] = true
		}
		row.T += p.Triangles
		row.V += p.Vertices
	}
	for i := range Parts {
		r.Ledger = append(r.Ledger, *rows[Parts[i].Name])
	}
	if un.T != 0 || un.V != 0 || len(unknown) > 0 {
		r.Unassigned = un
		for n := range unknown {
			r.UnassignedList = append(r.UnassignedList, n)
		}
		sort.Strings(r.UnassignedList)
		r.warn("%d shapes are in no ledger part (counted in the unassigned row)", len(r.UnassignedList))
	}
}

// reach measures each morph target.
func (m *Model) reach(r *Report) {
	for _, n := range m.TargetNames {
		row := TargetRow{Name: n}
		for _, d := range m.Targets[n] {
			if l := math.Sqrt(d[0]*d[0] + d[1]*d[1] + d[2]*d[2]); l > 1e-6 {
				row.Moved++
				row.Max = math.Max(row.Max, l)
			}
		}
		r.Targets = append(r.Targets, row)
	}
}

// WriteLedger prints the ledger in the layout of the game's `-ledger`, less
// the gear table and the outfit lines (the athlete's body is the only model
// here).
func (r *Report) WriteLedger(w io.Writer) {
	fmt.Fprintf(w, "%s body: %d indexed vertices, %d triangles, %d shapes, %d morph targets\n\n", r.Name, r.Vertices, r.Triangles, r.Shapes, r.Morphs)
	fmt.Fprintf(w, "%-28s %13s %13s %13s %13s\n", "part (body only)", "now t/v", "R t/v", "B t/v", "F t/v")
	for _, row := range r.Ledger {
		p := row.Part
		fmt.Fprintf(w, "%-28s %6d/%-6d %6d/%-6d %6d/%-6d %6d/%-6d\n", row.Name, row.T, row.V, p.RT, p.RV, p.BT, p.BV, p.FT, p.FV)
	}
	if u := r.Unassigned; u != nil {
		fmt.Fprintf(w, "%-28s %6d/%-6d\n", u.Name, u.T, u.V)
	}
	fmt.Fprintf(w, "\n%-12s %10s %12s\n", "target", "verts moved", "max yd")
	for _, t := range r.Targets {
		fmt.Fprintf(w, "%-12s %10d %12.4f\n", t.Name, t.Moved, t.Max)
	}
	if len(r.UnassignedList) > 0 {
		fmt.Fprintf(w, "\nnot in any part: %s\n", strings.Join(r.UnassignedList, ", "))
	}
}

// WriteSilhouette prints the silhouette table in the layout of the game's
// `-silhouette -`.
func (r *Report) WriteSilhouette(w io.Writer) {
	fmt.Fprint(w, SilHeader)
	for _, row := range r.Sil {
		fmt.Fprint(w, FormatRow(row.Name, row.Cols))
	}
}

// WriteWarnings prints what could not be matched.
func (r *Report) WriteWarnings(w io.Writer) {
	if len(r.Warnings) == 0 {
		fmt.Fprintln(w, "warnings: none")
		return
	}
	fmt.Fprintf(w, "warnings: %d\n", len(r.Warnings))
	for _, s := range r.Warnings {
		fmt.Fprintf(w, "  warning: %s\n", s)
	}
}

// Write prints the whole report: ledger, silhouette, gate, warnings.
func (r *Report) Write(w io.Writer) {
	fmt.Fprintln(w, "== LEDGER ==")
	r.WriteLedger(w)
	fmt.Fprintln(w, "\n== SILHOUETTE ==")
	r.WriteSilhouette(w)
	if len(r.Gate) > 0 {
		fmt.Fprintln(w, "\n== GATE ==")
		WriteGate(w, r.Gate, r.GatePx)
	}
	fmt.Fprintln(w)
	r.WriteWarnings(w)
}
