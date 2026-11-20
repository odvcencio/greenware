// Command greenware-grammar generates the Greenware parser blob and the EBNF
// reference from the grammargen Go DSL in language/grammar, and reports on the
// grammar: table sizes, resolved conflicts and embedded tests.
//
// Run it from the repository root, or let `go generate ./lang` do so:
//
//	go run ./cmd/greenware-grammar -bin lang/greenware.bin -ebnf docs/reference/grammar.ebnf
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/odvcencio/gotreesitter/grammargen"

	"m31labs.dev/greenware/language/grammar"
)

func main() {
	bin := flag.String("bin", "", "write the parser blob to this path")
	ebnf := flag.String("ebnf", "", "write the EBNF reference to this path")
	quiet := flag.Bool("q", false, "do not print the report")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: greenware-grammar [-bin lang/greenware.bin] [-ebnf docs/reference/grammar.ebnf] [-q]")
		os.Exit(2)
	}
	if err := run(*bin, *ebnf, *quiet); err != nil {
		fmt.Fprintln(os.Stderr, "greenware-grammar:", err)
		os.Exit(1)
	}
}

// run validates the grammar, then writes the blob and the EBNF reference to
// the paths that are not empty.
func run(bin, ebnf string, quiet bool) error {
	g := grammar.Greenware()
	if warnings := grammargen.Validate(g); len(warnings) > 0 {
		return fmt.Errorf("grammar warnings: %v", warnings)
	}
	report, err := grammargen.GenerateWithReport(g)
	if err != nil {
		return err
	}
	blob := report.Blob
	if err := grammargen.RunTests(g); err != nil {
		return err
	}
	if !quiet {
		fmt.Printf("Grammar:   %s\n", g.Name)
		fmt.Printf("Rules:     %d\n", len(g.RuleOrder))
		fmt.Printf("States:    %d\n", report.StateCount)
		fmt.Printf("Tokens:    %d\n", report.TokenCount)
		fmt.Printf("Blob:      %d bytes\n", len(blob))
		fmt.Printf("Conflicts: %d resolved\n", len(report.Conflicts))
		fmt.Printf("Tests:     %d passed\n", len(g.Tests))
	}
	if bin != "" {
		if err := writeFile(bin, blob); err != nil {
			return err
		}
	}
	if ebnf != "" {
		text, err := grammar.EBNF()
		if err != nil {
			return err
		}
		if err := writeFile(ebnf, []byte(text)); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
