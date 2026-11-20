// Package glb is an independent GLB (glTF 2.0 binary) decoder and structural
// validator. It is written from the glTF 2.0 specification and shares no code
// with the Greenware compiler. It is NOT the Khronos glTF validator; every report
// that uses it must say so (see Label).
package glb

import "fmt"

// Label must appear in every report produced from this package.
const Label = "independent structural validator, not the Khronos validator"

// Severity of a finding.
type Severity string

// Severities.
const (
	Error   Severity = "error"
	Warning Severity = "warning"
)

// Finding is one problem found in a file. Path is a JSON pointer into the
// glTF JSON (for example "/accessors/3/min") or "glb/..." for container-level
// problems that have no JSON location.
type Finding struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Path     string   `json:"path"`
	Message  string   `json:"message"`
}

func (f Finding) String() string {
	return fmt.Sprintf("%s %s at %s: %s", f.Severity, f.Code, f.Path, f.Message)
}

// HasErrors reports whether any finding has error severity.
func HasErrors(fs []Finding) bool {
	for _, f := range fs {
		if f.Severity == Error {
			return true
		}
	}
	return false
}

type collector struct{ fs []Finding }

func (c *collector) add(sev Severity, code, path, format string, a ...any) {
	if len(c.fs) >= maxFindings {
		return
	}
	c.fs = append(c.fs, Finding{sev, code, path, fmt.Sprintf(format, a...)})
}

func (c *collector) errf(code, path, format string, a ...any) {
	c.add(Error, code, path, format, a...)
}

func (c *collector) warnf(code, path, format string, a ...any) {
	c.add(Warning, code, path, format, a...)
}

// maxFindings bounds the report size on hostile input.
const maxFindings = 500
