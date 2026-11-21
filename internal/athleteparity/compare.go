package athleteparity

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Baseline is the game's printed numbers: the "now t/v" column of its ledger
// and the rows of its silhouette table, read back from the text the game's
// tools print (`-ledger` and `-silhouette -`).
type Baseline struct {
	Ledger map[string][2]int // part name -> triangles, vertices
	Sil    map[string][SilColumns]float64
	// Order of the rows as they appear.
	LedgerOrder, SilOrder []string
}

var ledgerLine = regexp.MustCompile(`^(\S.*?)\s+(\d+)/(\d+)\s+(\d+)/(\d+)\s+(\d+)/(\d+)\s+(\d+)/(\d+)\s*$`)

// ParseBaseline reads the text of the game's ledger and silhouette blocks.
// Lines it does not recognise (headings, the gear table, code fences) are
// ignored.
func ParseBaseline(r io.Reader) (*Baseline, error) {
	b := &Baseline{Ledger: map[string][2]int{}, Sil: map[string][SilColumns]float64{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := ledgerLine.FindStringSubmatch(line); m != nil {
			t, _ := strconv.Atoi(m[2])
			v, _ := strconv.Atoi(m[3])
			b.Ledger[m[1]] = [2]int{t, v}
			b.LedgerOrder = append(b.LedgerOrder, m[1])
			continue
		}
		if name, cols, ok := parseSilLine(line); ok {
			b.Sil[name] = cols
			b.SilOrder = append(b.SilOrder, name)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(b.Ledger) == 0 && len(b.Sil) == 0 {
		return nil, fmt.Errorf("no ledger or silhouette rows found")
	}
	return b, nil
}

// parseSilLine reads "name height helmet | 6 numbers | 4 numbers | 2 numbers".
func parseSilLine(line string) (string, [SilColumns]float64, bool) {
	var cols [SilColumns]float64
	segs := strings.Split(line, "|")
	if len(segs) != 4 {
		return "", cols, false
	}
	head := strings.Fields(segs[0])
	if len(head) < 3 {
		return "", cols, false
	}
	var nums []float64
	for _, f := range head[len(head)-2:] {
		x, err := strconv.ParseFloat(f, 64)
		if err != nil {
			return "", cols, false
		}
		nums = append(nums, x)
	}
	name := strings.Join(head[:len(head)-2], " ")
	for _, s := range segs[1:] {
		for _, f := range strings.Fields(s) {
			x, err := strconv.ParseFloat(f, 64)
			if err != nil {
				return "", cols, false
			}
			nums = append(nums, x)
		}
	}
	if len(nums) != SilColumns {
		return "", cols, false
	}
	copy(cols[:], nums)
	return name, cols, true
}

// Tolerances for the verdict.
type Tolerances struct {
	// LedgerPercent is the largest relative difference, in percent, of a
	// ledger row's triangles or vertices from the game's.
	LedgerPercent float64
	// SilHelmets is the largest absolute difference of a silhouette column, in
	// helmet widths. The two columns in yards (height and helmet) are divided
	// by the game's helmet width first. The two ratio columns are shown and
	// not judged.
	SilHelmets float64
}

// DefaultTolerances are 5 percent and 0.03 helmet widths.
var DefaultTolerances = Tolerances{LedgerPercent: 5, SilHelmets: 0.03}

// silShort names the columns of the difference table.
var silShort = [SilColumns]string{"ht yd", "helm yd", "shldr", "ribs", "waist", "seat", "thigh", "ht/H", "ribs-d", "waist-d", "seat-d", "thigh-d", "h/sh", "sh/w"}

func signed(x float64, prec int) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return "n/a"
	}
	return fmt.Sprintf("%+.*f", prec, x)
}

// Compare prints the difference table, Greenware minus Go, and the verdict;
// it returns true when every judged number is inside its tolerance. A row or
// column that one side lacks, or that is not a number, fails.
func Compare(w io.Writer, r *Report, b *Baseline, tol Tolerances) bool {
	pass := true
	fmt.Fprintf(w, "== COMPARE: %s minus the Go bake ==\n", r.Name)
	fmt.Fprintf(w, "tolerances: ledger rows within %g percent (triangles and vertices); silhouette columns within %g H (helmet widths)\n\n", tol.LedgerPercent, tol.SilHelmets)

	// Ledger.
	fmt.Fprintf(w, "ledger, 'now' column\n")
	fmt.Fprintf(w, "%-28s %12s %12s %9s %8s %9s %8s  %s\n", "part", "Go t/v", "Greenware", "d tris", "tris %", "d verts", "verts %", "verdict")
	got := map[string]LedgerRow{}
	for _, row := range r.Ledger {
		got[row.Name] = row
	}
	pct := func(d, base int) float64 {
		if base == 0 {
			if d == 0 {
				return 0
			}
			return math.Inf(1)
		}
		return 100 * float64(d) / float64(base)
	}
	ledgerFail := 0
	for _, name := range b.LedgerOrder {
		base := b.Ledger[name]
		g, ok := got[name]
		if !ok {
			fmt.Fprintf(w, "%-28s %5d/%-6d %12s  FAIL (no such row in the Greenware ledger)\n", name, base[0], base[1], "-")
			pass = false
			ledgerFail++
			continue
		}
		dt, dv := g.T-base[0], g.V-base[1]
		pt, pv := pct(dt, base[0]), pct(dv, base[1])
		verdict := "PASS"
		if math.Abs(pt) > tol.LedgerPercent || math.Abs(pv) > tol.LedgerPercent {
			verdict = "FAIL"
			pass = false
			ledgerFail++
		}
		fmt.Fprintf(w, "%-28s %5d/%-6d %5d/%-6d %+9d %8s %+9d %8s  %s\n", name, base[0], base[1], g.T, g.V, dt, signed(pt, 1), dv, signed(pv, 1), verdict)
	}
	if u := r.Unassigned; u != nil {
		fmt.Fprintf(w, "%-28s %12s %5d/%-6d  (Greenware shapes in no ledger part; the Go bake has none)\n", u.Name, "-", u.T, u.V)
	}
	fmt.Fprintf(w, "ledger: %d of %d rows outside tolerance\n\n", ledgerFail, len(b.LedgerOrder))

	// Silhouette.
	fmt.Fprintf(w, "silhouette: difference, then relative percent; '*' marks a column outside tolerance\n")
	fmt.Fprintf(w, "%-24s", "wearer")
	for _, n := range silShort {
		fmt.Fprintf(w, " %8s", n)
	}
	fmt.Fprintln(w)
	gotSil := map[string]SilRow{}
	for _, row := range r.Sil {
		gotSil[row.Name] = row
	}
	silFail, silCols := 0, 0
	for _, name := range b.SilOrder {
		base := b.Sil[name]
		g, ok := gotSil[name]
		if !ok {
			fmt.Fprintf(w, "%-24s FAIL (no such row in the Greenware silhouette)\n", name)
			pass = false
			silFail += 12
			silCols += 12
			continue
		}
		var d, rel [SilColumns]string
		for c := 0; c < SilColumns; c++ {
			diff := g.Cols[c] - base[c]
			prec := 3
			if c >= 2 {
				prec = 2
			}
			d[c] = signed(diff, prec)
			rel[c] = "n/a"
			if base[c] != 0 && !math.IsNaN(diff) {
				rel[c] = signed(100*diff/base[c], 1) + "%"
			}
			if c >= 12 {
				continue // shown, not judged
			}
			silCols++
			judged := diff
			if c < 2 {
				judged = diff / base[1] // yards to helmet widths of the game's helmet
			}
			if math.IsNaN(diff) || math.Abs(judged) > tol.SilHelmets {
				d[c] += "*"
				silFail++
				pass = false
			}
		}
		fmt.Fprintf(w, "%-24s", name)
		for _, s := range d {
			fmt.Fprintf(w, " %8s", s)
		}
		fmt.Fprintf(w, "\n%-24s", "")
		for _, s := range rel {
			fmt.Fprintf(w, " %8s", s)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "silhouette: %d of %d judged cells outside tolerance\n\n", silFail, silCols)
	if pass {
		fmt.Fprintln(w, "PASS")
	} else {
		fmt.Fprintln(w, "FAIL")
	}
	return pass
}
