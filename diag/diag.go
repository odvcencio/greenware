// Package diag holds the diagnostics that the Greenware front end reports.
//
// A diagnostic names one problem in one source file: a stable code, a
// severity, a message, a half-open byte span, and optionally edits that fix it
// and spans that explain it. The package knows nothing about the parser or the
// language: callers build diagnostics with New and the catalogue gives each
// code its severity, documentation and a failing fixture.
package diag

import (
	"fmt"
	"sort"
)

// Severity says whether a diagnostic stops a build.
type Severity uint8

const (
	// Error stops publication of a build.
	Error Severity = iota
	// Warning lets a build publish unless warnings are promoted.
	Warning
	// Info records a fact.
	Info
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	case Info:
		return "info"
	}
	return "unknown"
}

// Span is a half-open byte range [Start, End) of the source.
type Span struct {
	Start, End int
}

// Len returns the number of bytes in the span.
func (s Span) Len() int { return s.End - s.Start }

// Edit is one applicable fix: replace the bytes of Span with NewText. A
// diagnostic's Edits are independent alternatives, never a sequence. An edit
// with an empty span inserts text.
type Edit struct {
	Title   string
	Span    Span
	NewText string
}

// Related points at a second place that explains the diagnostic, such as the
// first of two duplicate declarations.
type Related struct {
	Span Span
	Note string
}

// Diagnostic is one finding.
type Diagnostic struct {
	Code     string
	Severity Severity
	Message  string
	Span     Span
	Edits    []Edit
	Related  []Related
}

// New builds a diagnostic with the severity the catalogue gives the code. It
// panics on a code that is not in the catalogue: that is a programming error,
// and the catalogue tests catch it.
func New(code string, span Span, format string, args ...any) Diagnostic {
	e, ok := Lookup(code)
	if !ok {
		panic("diag: code " + code + " is not in the catalogue")
	}
	return Diagnostic{Code: code, Severity: e.Severity, Message: fmt.Sprintf(format, args...), Span: span}
}

// WithEdit returns a copy of d with one more alternative fix.
func (d Diagnostic) WithEdit(title string, span Span, newText string) Diagnostic {
	d.Edits = append(append([]Edit(nil), d.Edits...), Edit{Title: title, Span: span, NewText: newText})
	return d
}

// WithRelated returns a copy of d with one more related span.
func (d Diagnostic) WithRelated(span Span, note string) Diagnostic {
	d.Related = append(append([]Related(nil), d.Related...), Related{Span: span, Note: note})
	return d
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%d-%d: %s %s: %s", d.Span.Start, d.Span.End, d.Severity, d.Code, d.Message)
}

// Less is the total order of diagnostics: span start, span end, code, message.
func Less(a, b Diagnostic) bool {
	if a.Span.Start != b.Span.Start {
		return a.Span.Start < b.Span.Start
	}
	if a.Span.End != b.Span.End {
		return a.Span.End < b.Span.End
	}
	if a.Code != b.Code {
		return a.Code < b.Code
	}
	return a.Message < b.Message
}

// Sort orders ds in place by Less. The order does not depend on the order in
// which the diagnostics were found.
func Sort(ds []Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool { return Less(ds[i], ds[j]) })
}

// Dedup sorts ds and removes diagnostics that have the same code, span and
// message. Evaluating one file several times (the defaults, then each output)
// finds the same fault several times; Dedup reports it once.
func Dedup(ds []Diagnostic) []Diagnostic {
	Sort(ds)
	out := ds[:0:0]
	for i, d := range ds {
		if i > 0 {
			p := ds[i-1]
			if p.Code == d.Code && p.Span == d.Span && p.Message == d.Message {
				continue
			}
		}
		out = append(out, d)
	}
	return out
}

// HasErrors reports whether any diagnostic has Error severity.
func HasErrors(ds []Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

// Codes returns the distinct codes in ds in sorted order.
func Codes(ds []Diagnostic) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range ds {
		if !seen[d.Code] {
			seen[d.Code] = true
			out = append(out, d.Code)
		}
	}
	sort.Strings(out)
	return out
}

// Apply applies one edit to src and returns the new source. It returns false
// when the edit lies outside src.
func Apply(src []byte, e Edit) ([]byte, bool) {
	if e.Span.Start < 0 || e.Span.End < e.Span.Start || e.Span.End > len(src) {
		return nil, false
	}
	out := make([]byte, 0, len(src)-e.Span.Len()+len(e.NewText))
	out = append(out, src[:e.Span.Start]...)
	out = append(out, e.NewText...)
	out = append(out, src[e.Span.End:]...)
	return out, true
}
