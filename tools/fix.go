package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/greenware/diag"
)

const fixUsage = "greenware fix <file> [--write] [--code GW-...] [--choose N] [--revision SHA256] [--project DIR] [--json]"

// loadForFix is the front end Fix runs before and after each pass. Tests
// replace it to force a result that raises the error count.
var loadForFix = LoadBytes

// maxFixPasses bounds the passes of fix. A pass applies the edits of the
// diagnostics it sees; fixing one fault can show the next one.
const maxFixPasses = 8

type fixSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type fixApplied struct {
	Pass    int     `json:"pass"`
	Code    string  `json:"code"`
	Title   string  `json:"title"`
	Span    fixSpan `json:"span"`
	NewText string  `json:"new_text"`
}

type fixSkipped struct {
	Pass   int     `json:"pass"`
	Code   string  `json:"code"`
	Span   fixSpan `json:"span"`
	Reason string  `json:"reason"`
	// Alternatives lists the titles of the alternative edits, numbered from 1.
	Alternatives []string `json:"alternatives"`
}

type fixReport struct {
	Schema         string       `json:"schema"`
	File           string       `json:"file"`
	InputRevision  string       `json:"input_revision"`
	OutputRevision string       `json:"output_revision"`
	OK             bool         `json:"ok"`
	Changed        bool         `json:"changed"`
	Written        bool         `json:"written"`
	Refused        bool         `json:"refused"`
	Refusal        string       `json:"refusal,omitempty"`
	ErrorsBefore   int          `json:"errors_before"`
	ErrorsAfter    int          `json:"errors_after"`
	Applied        []fixApplied `json:"applied"`
	Skipped        []fixSkipped `json:"skipped"`
	Diff           string       `json:"diff"`
}

// revision is the SHA-256 of raw source bytes, the identity an edit is bound
// to.
func revision(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// Fix applies the edits that diagnostics carry. A diagnostic with one edit
// contributes it. A diagnostic with several alternative edits contributes
// none unless --choose N names one of them (numbered from 1). Edits that do
// not overlap are applied from the end of the file backwards. The result is
// checked again: if it has more errors than the source had, nothing is
// written and the status is 1. Without --write the command prints a unified
// diff.
func Fix(args []string, stdout, stderr io.Writer) int {
	p, err := parseArgs(args, flagSet{"write": false, "code": true, "choose": true, "revision": true, "project": true, "json": false})
	if err != nil || len(p.pos) != 1 {
		if err == nil {
			err = fmt.Errorf("one source file is needed")
		}
		return usageError(stderr, "fix", fixUsage, err)
	}
	choose := 0
	if p.has("choose") {
		choose, err = strconv.Atoi(p.get("choose"))
		if err != nil || choose < 1 {
			return usageError(stderr, "fix", fixUsage, fmt.Errorf("--choose needs a number from 1, got %q", p.get("choose")))
		}
	}
	codes := map[string]bool{}
	for _, c := range p.flags["code"] {
		if _, ok := diag.Lookup(c); !ok {
			return usageError(stderr, "fix", fixUsage, fmt.Errorf("%q is not a diagnostic code", c))
		}
		codes[c] = true
	}
	file := p.pos[0]
	orig, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(stderr, "greenware fix: %v\n", err)
		return ExitUsage
	}
	rep := fixReport{Schema: "greenware.fix/1", File: file, InputRevision: revision(orig), Applied: []fixApplied{}, Skipped: []fixSkipped{}}
	if want := p.get("revision"); want != "" {
		if strings.TrimPrefix(want, "sha256:") != strings.TrimPrefix(rep.InputRevision, "sha256:") {
			rep.Refused, rep.Refusal = true, fmt.Sprintf("the file's revision is %s, not %s", rep.InputRevision, want)
			rep.OutputRevision = rep.InputRevision
			return finishFix(p, rep, orig, orig, nil, stdout, stderr)
		}
	}

	cur := orig
	u := loadForFix(file, p.get("project"), cur)
	rep.ErrorsBefore = countErrors(u.Diags)
	prevErrors := rep.ErrorsBefore
	for pass := 1; pass <= maxFixPasses; pass++ {
		edits, _ := selectEdits(u.Diags, codes, choose, pass)
		if len(edits) == 0 {
			break
		}
		next := cur
		for i := len(edits) - 1; i >= 0; i-- {
			e := edits[i]
			var ok bool
			if next, ok = diag.Apply(next, diag.Edit{Span: diag.Span{Start: e.Span.Start, End: e.Span.End}, NewText: e.NewText}); !ok {
				rep.Refused, rep.Refusal = true, fmt.Sprintf("the edit %q for %s lies outside the file", e.Title, e.Code)
				return finishFix(p, rep, orig, orig, nil, stdout, stderr)
			}
		}
		nu := loadForFix(file, p.get("project"), next)
		if n := countErrors(nu.Diags); n > prevErrors {
			rep.Refused = true
			rep.Refusal = fmt.Sprintf("pass %d would raise the error count from %d to %d", pass, prevErrors, n)
			return finishFix(p, rep, orig, orig, nil, stdout, stderr)
		} else {
			prevErrors = n
		}
		rep.Applied = append(rep.Applied, edits...)
		cur, u = next, nu
	}
	// Report skips of the final state only: alternatives still waiting.
	_, finalSkipped := selectEdits(u.Diags, codes, choose, len(rep.Applied)+1)
	rep.Skipped = append(rep.Skipped, finalSkipped...)
	for i := range rep.Skipped {
		rep.Skipped[i].Pass = 0
	}
	return finishFix(p, rep, orig, cur, u, stdout, stderr)
}

// selectEdits picks the edits to apply from one pass of diagnostics, sorted
// by span with no two overlapping, and lists the diagnostics it left alone.
func selectEdits(ds []diag.Diagnostic, codes map[string]bool, choose, pass int) ([]fixApplied, []fixSkipped) {
	var picked []fixApplied
	var skipped []fixSkipped
	sorted := append([]diag.Diagnostic(nil), ds...)
	diag.Sort(sorted)
	for _, d := range sorted {
		if len(d.Edits) == 0 || (len(codes) > 0 && !codes[d.Code]) {
			continue
		}
		titles := make([]string, len(d.Edits))
		for i, e := range d.Edits {
			titles[i] = e.Title
		}
		skip := func(reason string) {
			skipped = append(skipped, fixSkipped{Pass: pass, Code: d.Code, Span: fixSpan{d.Span.Start, d.Span.End}, Reason: reason, Alternatives: titles})
		}
		var e diag.Edit
		switch {
		case len(d.Edits) == 1:
			e = d.Edits[0]
		case choose == 0:
			skip("alternatives: pass --choose N to take one")
			continue
		case choose > len(d.Edits):
			skip(fmt.Sprintf("--choose %d is beyond the %d alternatives", choose, len(d.Edits)))
			continue
		default:
			e = d.Edits[choose-1]
		}
		picked = append(picked, fixApplied{Pass: pass, Code: d.Code, Title: e.Title, Span: fixSpan{e.Span.Start, e.Span.End}, NewText: e.NewText})
	}
	sort.SliceStable(picked, func(i, j int) bool {
		a, b := picked[i], picked[j]
		if a.Span.Start != b.Span.Start {
			return a.Span.Start < b.Span.Start
		}
		return a.Span.End < b.Span.End
	})
	// Drop duplicates and overlaps, keeping the first of each.
	var out []fixApplied
	for _, e := range picked {
		if n := len(out); n > 0 {
			last := out[n-1]
			if e.Span == last.Span && e.NewText == last.NewText {
				continue
			}
			if e.Span.Start < last.Span.End || e.Span.Start == last.Span.Start {
				skipped = append(skipped, fixSkipped{Pass: pass, Code: e.Code, Span: e.Span, Reason: "overlaps an edit already chosen", Alternatives: []string{e.Title}})
				continue
			}
		}
		out = append(out, e)
	}
	return out, skipped
}

func finishFix(p *parsed, rep fixReport, orig, final []byte, after *Unit, stdout, stderr io.Writer) int {
	rep.OutputRevision = revision(final)
	rep.Changed = string(orig) != string(final)
	rep.ErrorsAfter = rep.ErrorsBefore
	if after != nil {
		rep.ErrorsAfter = countErrors(after.Diags)
	}
	rep.Diff = unifiedDiff("a/"+rep.File, "b/"+rep.File, orig, final)
	code := ExitOK
	if rep.Refused || rep.ErrorsAfter > 0 {
		code = ExitFindings
	}
	if rep.Changed && p.has("write") && !rep.Refused {
		if err := os.WriteFile(rep.File, final, 0o644); err != nil {
			fmt.Fprintf(stderr, "greenware fix: %v\n", err)
			return ExitUsage
		}
		rep.Written = true
	}
	rep.OK = code == ExitOK
	if p.has("json") {
		writeJSON(stdout, rep)
		return code
	}
	if rep.Refused {
		fmt.Fprintf(stderr, "%s: fix refused: %s\n", rep.File, rep.Refusal)
	}
	for _, s := range rep.Skipped {
		fmt.Fprintf(stderr, "%s: skipped %s at byte %d: %s\n", rep.File, s.Code, s.Span.Start, s.Reason)
		for i, t := range s.Alternatives {
			fmt.Fprintf(stderr, "  %d. %s\n", i+1, t)
		}
	}
	if after != nil && rep.ErrorsAfter > 0 {
		printDiags(stderr, rep.File, after.Diags, after.Loc)
	}
	switch {
	case rep.Written:
		fmt.Fprintf(stdout, "%s: applied %d edits\n", rep.File, len(rep.Applied))
	case rep.Changed:
		io.WriteString(stdout, rep.Diff)
	case !rep.Refused && rep.ErrorsAfter == 0:
		fmt.Fprintf(stdout, "%s: nothing to fix\n", rep.File)
	}
	return code
}
