package tools

import (
	"encoding/json"
	"fmt"
	"io"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/resolve"
)

const irUsage = "greenware ir <file> [--output NAME] [--project DIR] [--json]"

type irReport struct {
	Schema      string            `json:"schema"`
	Stage       string            `json:"stage"`
	File        string            `json:"file"`
	Output      string            `json:"output"`
	OK          bool              `json:"ok"`
	Model       *resolve.Resolved `json:"model"`
	Diagnostics json.RawMessage   `json:"diagnostics"`
}

// IR dumps the resolved model: every value after evaluation, for the file's
// defaults or for one output. The bare form (no --json) prints the model
// alone, indented, as resolve.Resolved marshals. With --json the model sits
// inside an envelope that carries the schema, the output and the
// diagnostics. A file with an error diagnostic has no model (null) and the
// status is 1.
func IR(args []string, stdout, stderr io.Writer) int {
	p, err := parseArgs(args, flagSet{"output": true, "project": true, "json": false})
	if err != nil || len(p.pos) != 1 {
		if err == nil {
			err = fmt.Errorf("one source file is needed")
		}
		return usageError(stderr, "ir", irUsage, err)
	}
	u, err := Load(p.pos[0], p.get("project"))
	if err != nil {
		fmt.Fprintf(stderr, "greenware ir: %v\n", err)
		return ExitUsage
	}
	ds := append([]diag.Diagnostic(nil), u.Lowered...)
	var model *resolve.Resolved
	if !u.File.Broken {
		var more []diag.Diagnostic
		if name := p.get("output"); name != "" {
			model, more = resolve.EvaluateOutput(u.File, name)
		} else {
			model, more = resolve.Evaluate(u.File, nil)
		}
		ds = diag.Dedup(append(ds, more...))
	}
	code := ExitOK
	if diag.HasErrors(ds) {
		code, model = ExitFindings, nil
	}
	if p.has("json") {
		writeJSON(stdout, irReport{Schema: "greenware.ir/1", Stage: "evaluated", File: p.pos[0], Output: p.get("output"), OK: code == 0, Model: model, Diagnostics: diagJSON(ds, u.Loc)})
		return code
	}
	printDiags(stderr, p.pos[0], ds, u.Loc)
	if model != nil {
		b, err := json.MarshalIndent(model, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "greenware ir: %v\n", err)
			return ExitUsage
		}
		stdout.Write(append(b, '\n'))
	}
	return code
}
