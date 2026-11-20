// Command greenware compiles Greenware sources (.gw files).
//
//	greenware build <file> [--output NAME] [--out-dir DIR] [--json]
//	greenware check <file> [--json]
//	greenware fmt <file> [--check] [--write] [--json]
//	greenware accept ...   (not built yet)
//
// With --json the command writes exactly one JSON value to standard output.
// Exit status: 0 on success, 1 when the diagnostics hold an error (or fmt
// --check finds a file that is not formatted), 2 on a usage or read error.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/resolve"
)

const usage = `usage:
  greenware build <file> [--output NAME] [--out-dir DIR] [--json]
  greenware check <file> [--json]
  greenware fmt <file> [--check] [--write] [--json]
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// options holds the parsed command line after the subcommand.
type options struct {
	file    string
	output  string
	outDir  string
	json    bool
	check   bool
	write   bool
	hasOut  bool
	hasName bool
}

var errUsage = errors.New("usage")

// parse reads flags in any position. A flag that takes a value accepts
// "--name value" and "--name=value".
func parse(args []string, allowed map[string]bool) (options, error) {
	o := options{outDir: "out"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			if o.file != "" {
				return o, fmt.Errorf("unexpected argument %q", a)
			}
			o.file = a
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !allowed[name] {
			return o, fmt.Errorf("unknown flag %q", a)
		}
		takes := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag --%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		var err error
		switch name {
		case "json":
			o.json = true
		case "check":
			o.check = true
		case "write":
			o.write = true
		case "output":
			o.output, err = takes()
			o.hasName = true
		case "out-dir":
			o.outDir, err = takes()
			o.hasOut = true
		}
		if err != nil {
			return o, err
		}
	}
	if o.file == "" {
		return o, errors.New("a source file is needed")
	}
	return o, nil
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	var allowed map[string]bool
	switch cmd {
	case "build":
		allowed = map[string]bool{"output": true, "out-dir": true, "json": true}
	case "check":
		allowed = map[string]bool{"json": true}
	case "fmt":
		allowed = map[string]bool{"check": true, "write": true, "json": true}
	case "accept":
		// The acceptance evaluator is wired here later.
		fmt.Fprintln(stderr, "greenware accept: not built yet")
		return 2
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "greenware: unknown command %q\n%s", cmd, usage)
		return 2
	}
	o, err := parse(rest, allowed)
	if err != nil {
		fmt.Fprintf(stderr, "greenware %s: %v\n%s", cmd, err, usage)
		return 2
	}
	src, err := os.ReadFile(o.file)
	if err != nil {
		fmt.Fprintf(stderr, "greenware %s: %v\n", cmd, err)
		return 2
	}
	switch cmd {
	case "build":
		return runBuild(o, src, stdout, stderr)
	case "check":
		return runCheck(o, src, stdout, stderr)
	default:
		return runFmt(o, src, stdout, stderr)
	}
}

// front lowers a source and returns its diagnostics and a locator.
func front(src []byte) (*ast.File, []diag.Diagnostic, diag.Locator) {
	tree := lang.Parse(src)
	f, ds := ast.Lower(tree)
	loc := func(off int) (int, int) {
		p := tree.Position(off)
		return p.Line, p.Column
	}
	return f, ds, loc
}

// printText writes diagnostics as "file:line:col: severity CODE: message".
func printText(w io.Writer, file string, ds []diag.Diagnostic, loc diag.Locator) {
	sorted := append([]diag.Diagnostic(nil), ds...)
	diag.Sort(sorted)
	for _, d := range sorted {
		l, c := loc(d.Span.Start)
		fmt.Fprintf(w, "%s:%d:%d: %s %s: %s\n", file, l, c, d.Severity, d.Code, d.Message)
	}
}

func writeJSON(w io.Writer, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "greenware:", err)
		return
	}
	w.Write(buf.Bytes())
}

func status(ds []diag.Diagnostic) int {
	if diag.HasErrors(ds) {
		return 1
	}
	return 0
}

// ---- check -------------------------------------------------------------------

func checkAll(f *ast.File, lowered []diag.Diagnostic) []diag.Diagnostic {
	all := append([]diag.Diagnostic(nil), lowered...)
	if !f.Broken {
		_, ds := resolve.EvaluateAll(f)
		all = append(all, ds...)
	}
	return diag.Dedup(all)
}

func runCheck(o options, src []byte, stdout, stderr io.Writer) int {
	f, lowered, loc := front(src)
	ds := checkAll(f, lowered)
	if o.json {
		b, err := diag.Marshal(ds, loc)
		if err != nil {
			fmt.Fprintln(stderr, "greenware check:", err)
			return 2
		}
		stdout.Write(b)
		return status(ds)
	}
	printText(stderr, o.file, ds, loc)
	if status(ds) == 0 {
		fmt.Fprintf(stdout, "%s: ok\n", o.file)
	}
	return status(ds)
}

// ---- fmt ---------------------------------------------------------------------

func runFmt(o options, src []byte, stdout, stderr io.Writer) int {
	out, err := lang.Format(src)
	if err != nil {
		var fe *lang.FormatError
		if errors.As(err, &fe) && o.json {
			_, lowered, loc := front(src)
			b, _ := diag.Marshal(lowered, loc)
			stdout.Write(b)
			return 1
		}
		fmt.Fprintf(stderr, "%s: %v\n", o.file, err)
		return 1
	}
	changed := !bytes.Equal(out, src)
	if o.write && changed {
		if err := os.WriteFile(o.file, out, 0o644); err != nil {
			fmt.Fprintf(stderr, "greenware fmt: %v\n", err)
			return 2
		}
	}
	switch {
	case o.json:
		writeJSON(stdout, struct {
			Schema  string `json:"schema"`
			Changed bool   `json:"changed"`
			Source  string `json:"source"`
		}{"greenware.fmt/1", changed, string(out)})
	case o.check:
		if changed {
			fmt.Fprintf(stderr, "%s: not formatted\n", o.file)
		}
	case o.write:
	default:
		stdout.Write(out)
	}
	if o.check && changed {
		return 1
	}
	return 0
}

// ---- build -------------------------------------------------------------------

type buildFile struct {
	Name      string `json:"name"`
	Level     int    `json:"level"`
	Bytes     int    `json:"bytes"`
	SHA256    string `json:"sha256"`
	Triangles int    `json:"triangles"`
	Draws     int    `json:"draws"`
	Materials int    `json:"materials"`
	Vertices  int    `json:"vertices"`
}

type buildOutput struct {
	Name      string      `json:"name"`
	FirSHA256 string      `json:"fir_sha256"`
	Files     []buildFile `json:"files"`
}

type buildReport struct {
	Schema      string          `json:"schema"`
	OK          bool            `json:"ok"`
	Written     bool            `json:"written"`
	OutDir      string          `json:"out_dir,omitempty"`
	Record      string          `json:"record,omitempty"`
	InputSHA256 string          `json:"input_sha256,omitempty"`
	Outputs     []buildOutput   `json:"outputs"`
	Unsupported []string        `json:"unsupported"`
	Diagnostics json.RawMessage `json:"diagnostics"`
}

func runBuild(o options, src []byte, stdout, stderr io.Writer) int {
	f, ds, loc := front(src)
	var p *compiler.Product
	if !diag.HasErrors(ds) {
		var more []diag.Diagnostic
		p, more = compiler.Build(f, o.output)
		ds = diag.Dedup(append(ds, more...))
	}
	rep := buildReport{Schema: "greenware.build/1", Outputs: []buildOutput{}, Unsupported: []string{}}
	code := status(ds)
	var writeErr error
	if p != nil && code == 0 {
		if writeErr = compiler.Write(p, o.outDir); writeErr == nil {
			rep.Written = true
			rep.OutDir = o.outDir
			rep.Record = p.RecordName()
		}
		rep.InputSHA256 = p.InputSHA256
		rep.Unsupported = append(rep.Unsupported, p.Unsupported...)
		for _, op := range p.Outputs {
			bo := buildOutput{Name: op.Name, FirSHA256: op.FirSHA256, Files: []buildFile{}}
			for _, fl := range op.Files {
				bo.Files = append(bo.Files, buildFile{fl.Name, fl.Level, fl.Size, fl.SHA256, fl.Triangles, fl.Draws, fl.Materials, fl.Vertices})
			}
			rep.Outputs = append(rep.Outputs, bo)
		}
	}
	rep.OK = code == 0 && writeErr == nil
	if writeErr != nil {
		code = 2
	}
	if o.json {
		b, err := diag.Marshal(ds, loc)
		if err != nil {
			fmt.Fprintln(stderr, "greenware build:", err)
			return 2
		}
		rep.Diagnostics = bytes.TrimSpace(b)
		writeJSON(stdout, rep)
		if writeErr != nil {
			fmt.Fprintln(stderr, "greenware build:", writeErr)
		}
		return code
	}
	printText(stderr, o.file, ds, loc)
	if writeErr != nil {
		fmt.Fprintln(stderr, "greenware build:", writeErr)
	}
	if rep.OK {
		for _, bo := range rep.Outputs {
			for _, fl := range bo.Files {
				fmt.Fprintf(stdout, "%s/%s  %d triangles, %d draws, %d bytes\n", o.outDir, fl.Name, fl.Triangles, fl.Draws, fl.Bytes)
			}
		}
		fmt.Fprintf(stdout, "%s/%s\n", o.outDir, rep.Record)
		for _, n := range rep.Unsupported {
			fmt.Fprintf(stderr, "note: %s\n", n)
		}
	}
	return code
}
