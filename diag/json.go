package diag

import (
	"bytes"
	"encoding/json"
)

// Schema names the layout of the JSON that Marshal writes.
const Schema = "greenware.diagnostics/1"

// Locator converts a byte offset to a one-based line and column. It is
// optional: Marshal leaves the line and column out when it is nil.
type Locator func(offset int) (line, column int)

type jsonPoint struct {
	Byte   int `json:"byte"`
	Line   int `json:"line,omitempty"`
	Column int `json:"column,omitempty"`
}

type jsonSpan struct {
	Start jsonPoint `json:"start"`
	End   jsonPoint `json:"end"`
}

type jsonEdit struct {
	Title   string   `json:"title"`
	Span    jsonSpan `json:"span"`
	NewText string   `json:"new_text"`
}

type jsonRelated struct {
	Span jsonSpan `json:"span"`
	Note string   `json:"note"`
}

type jsonDiagnostic struct {
	Code     string        `json:"code"`
	Severity string        `json:"severity"`
	Message  string        `json:"message"`
	Span     jsonSpan      `json:"span"`
	Edits    []jsonEdit    `json:"edits"`
	Related  []jsonRelated `json:"related"`
}

type jsonReport struct {
	Schema      string           `json:"schema"`
	OK          bool             `json:"ok"`
	Diagnostics []jsonDiagnostic `json:"diagnostics"`
}

// Marshal encodes ds as one JSON document, sorted by Less, with a fixed key
// order and a trailing newline. Edits and related are always arrays.
func Marshal(ds []Diagnostic, loc Locator) ([]byte, error) {
	sorted := append([]Diagnostic(nil), ds...)
	Sort(sorted)
	point := func(off int) jsonPoint {
		p := jsonPoint{Byte: off}
		if loc != nil {
			p.Line, p.Column = loc(off)
		}
		return p
	}
	span := func(s Span) jsonSpan { return jsonSpan{point(s.Start), point(s.End)} }
	rep := jsonReport{Schema: Schema, OK: !HasErrors(sorted), Diagnostics: []jsonDiagnostic{}}
	for _, d := range sorted {
		j := jsonDiagnostic{
			Code: d.Code, Severity: d.Severity.String(), Message: d.Message, Span: span(d.Span),
			Edits: []jsonEdit{}, Related: []jsonRelated{},
		}
		for _, e := range d.Edits {
			j.Edits = append(j.Edits, jsonEdit{Title: e.Title, Span: span(e.Span), NewText: e.NewText})
		}
		for _, r := range d.Related {
			j.Related = append(j.Related, jsonRelated{Span: span(r.Span), Note: r.Note})
		}
		rep.Diagnostics = append(rep.Diagnostics, j)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rep); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
