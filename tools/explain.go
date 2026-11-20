package tools

import (
	"fmt"
	"io"
	"strings"

	"m31labs.dev/greenware/diag"
)

const explainUsage = "greenware explain <GW-CODE> [--json]"

type explainReport struct {
	Schema   string `json:"schema"`
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Doc      string `json:"doc"`
	Fixture  string `json:"fixture"`
	// Fix is true when diagnostics of the code can carry an edit.
	Fix bool `json:"fix"`
}

// Explain prints the catalogue entry of one diagnostic code: its severity,
// its documentation, the fixture that triggers it and whether it can carry
// an edit. An unknown code exits 2.
func Explain(args []string, stdout, stderr io.Writer) int {
	p, err := parseArgs(args, flagSet{"json": false})
	if err != nil || len(p.pos) != 1 {
		if err == nil {
			err = fmt.Errorf("one diagnostic code is needed")
		}
		return usageError(stderr, "explain", explainUsage, err)
	}
	code := strings.ToUpper(p.pos[0])
	e, ok := diag.Lookup(code)
	if !ok {
		fmt.Fprintf(stderr, "greenware explain: %q is not a diagnostic code; run `greenware explain` with one of:\n", p.pos[0])
		for _, c := range diag.Catalogue() {
			fmt.Fprintf(stderr, "  %s\n", c.Code)
		}
		return ExitUsage
	}
	rep := explainReport{Schema: "greenware.explain/1", Code: e.Code, Severity: e.Severity.String(), Doc: e.Doc, Fixture: e.Fixture, Fix: e.Fix}
	if p.has("json") {
		writeJSON(stdout, rep)
		return ExitOK
	}
	fmt.Fprintf(stdout, "%s (%s)\n%s\nfixture: %s\n", e.Code, e.Severity, e.Doc, e.Fixture)
	if e.Fix {
		fmt.Fprintln(stdout, "fix: a diagnostic of this code can carry an edit (greenware fix)")
	}
	return ExitOK
}
