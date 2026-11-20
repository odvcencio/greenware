package accept

import (
	"bytes"
	"encoding/json"

	"m31labs.dev/greenware/diag"
)

// Schema names the layout of the JSON that Report.JSON writes.
const Schema = "greenware.accept/1"

// CodeAccept is the diagnostic code of a failed, refused or unavailable
// expectation. It is not in package diag's catalogue yet, so this package
// builds its diagnostics as values and does not call diag.New.
const CodeAccept = "GW-ACCEPT"

// Verdict is the result of one expect at one output and level.
type Verdict string

const (
	// Pass: the condition held.
	Pass Verdict = "pass"
	// Fail: the condition was false, or a level's file was missing.
	Fail Verdict = "fail"
	// Refused: a measurement could not be taken (a ray missed, a node was
	// absent, an expression was ill-typed). The spec treats a failed
	// measurement as a failed expectation, so Refused counts as a failure.
	Refused Verdict = "refused"
	// Unavailable: this evaluator cannot take the measurement at all (for
	// example outline, which needs a reference mask). It never counts as a
	// pass and it has its own exit status.
	Unavailable Verdict = "unavailable"
)

// Measure is one number or fact read from the decoded mesh while evaluating an
// expect. Value is text with the unit ("5.64m", "true", "12 edges"). Number is
// set for numeric values, in metres or plain units, with nine significant
// digits.
type Measure struct {
	Name   string      `json:"name"`
	Value  string      `json:"value"`
	Number json.Number `json:"number,omitempty"`
}

// Span is a half-open byte range of the acceptance source.
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// ExpectReport is the result of one expect at one output and level.
type ExpectReport struct {
	Span     Span      `json:"span"`
	Message  string    `json:"message"`
	Verdict  Verdict   `json:"verdict"`
	Detail   string    `json:"detail,omitempty"`
	Measures []Measure `json:"measures"`
}

// LevelReport holds the expects of one output at one level. Problem is set
// instead of Expects when the level's decoded file is not available.
type LevelReport struct {
	Output   string         `json:"output"`
	LOD      int            `json:"lod"`
	File     string         `json:"file,omitempty"`
	LODError json.Number    `json:"lod_error,omitempty"`
	Distance json.Number    `json:"distance,omitempty"`
	Problem  string         `json:"problem,omitempty"`
	Expects  []ExpectReport `json:"expects"`
}

// Totals counts verdicts. Missing counts levels with no usable file.
type Totals struct {
	Pass        int `json:"pass"`
	Fail        int `json:"fail"`
	Refused     int `json:"refused"`
	Unavailable int `json:"unavailable"`
	Missing     int `json:"missing"`
}

// Report is the outcome of one acceptance document.
type Report struct {
	Schema string        `json:"schema"`
	Accept string        `json:"accept"`
	Of     string        `json:"of,omitempty"`
	Totals Totals        `json:"totals"`
	Levels []LevelReport `json:"levels"`
}

func (r *Report) tally() {
	r.Totals = Totals{}
	for _, l := range r.Levels {
		if l.Problem != "" {
			r.Totals.Missing++
		}
		for _, e := range l.Expects {
			switch e.Verdict {
			case Pass:
				r.Totals.Pass++
			case Fail:
				r.Totals.Fail++
			case Refused:
				r.Totals.Refused++
			case Unavailable:
				r.Totals.Unavailable++
			}
		}
	}
}

// ExitCode is 0 when every expect passed, 1 when any failed, was refused or
// had no file, 3 when none failed but some were unavailable.
func (r *Report) ExitCode() int {
	t := r.Totals
	switch {
	case t.Fail > 0 || t.Refused > 0 || t.Missing > 0:
		return 1
	case t.Unavailable > 0:
		return 3
	}
	return 0
}

// JSON encodes the report with a fixed key order (struct order), two-space
// indent and a trailing newline. The same report gives the same bytes.
func (r *Report) JSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func newDiag(sev diag.Severity, sp Span, msg string) diag.Diagnostic {
	return diag.Diagnostic{Code: CodeAccept, Severity: sev, Message: msg, Span: diag.Span{Start: sp.Start, End: sp.End}}
}

func jsonNum(x float64) json.Number { return json.Number(fmtNum(x)) }
