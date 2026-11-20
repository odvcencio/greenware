package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/lsp"
)

// lspUsage is the usage line of the lsp command.
const lspUsage = "  greenware lsp [--stdio] [--check-config] [--json]\n"

// lspConfig is what `lsp --check-config` reports. started is false: the check
// opens no stream and serves no session.
type lspConfig struct {
	Schema            string   `json:"schema"`
	Transport         string   `json:"transport"`
	PositionEncodings []string `json:"position_encodings"`
	SyncKind          string   `json:"sync_kind"`
	Capabilities      []string `json:"capabilities"`
	GrammarSHA256     string   `json:"grammar_sha256"`
	Started           bool     `json:"started"`
}

// runLSP serves one editor session on standard input and output, or with
// --check-config (implied by --json) reports the configuration and exits.
// Standard output belongs to the protocol while serving; the server's own
// notes go to standard error.
func runLSP(args []string, stdout, stderr io.Writer) int {
	check, asJSON := false, false
	for _, a := range args {
		switch a {
		case "--stdio":
		case "--check-config":
			check = true
		case "--json":
			check, asJSON = true, true
		default:
			if strings.HasPrefix(a, "-") {
				return usageFailure(stderr, "lsp", &unknownFlag{a}, lspUsage)
			}
			fmt.Fprintf(stderr, "greenware lsp: unknown argument %q\n%s", a, lspUsage)
			return 2
		}
	}
	if !check {
		return lsp.Serve(os.Stdin, stdout, stderr)
	}
	cfg := lspConfig{
		Schema:            "greenware.lsp/1",
		Transport:         "stdio",
		PositionEncodings: []string{"utf-16"},
		SyncKind:          "incremental",
		Capabilities:      lsp.Capabilities(),
		GrammarSHA256:     lang.GrammarSHA256(),
	}
	if asJSON {
		writeJSON(stdout, cfg)
		return 0
	}
	fmt.Fprintf(stdout, "transport: %s\nposition encodings: %v\nsync: %s\ncapabilities: %v\ngrammar sha256: %s\nstarted: false\n",
		cfg.Transport, cfg.PositionEncodings, cfg.SyncKind, cfg.Capabilities, cfg.GrammarSHA256)
	return 0
}
