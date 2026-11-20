// Package tools holds the authoring commands of the greenware command:
// fix, diff, describe, ir, init, lock, explain, search and trace.
//
// Each command is a function from argument list and two writers to an exit
// status, so a test runs it exactly as a shell does. All of them use the front
// end only (parse, lower, resolve); none builds geometry. With --json a
// command writes exactly one JSON value to standard output, with a fixed key
// order and a "schema" key.
//
// Exit status: 0 success, 1 findings (an error diagnostic, a drift, a
// refusal, a gate that failed), 2 a usage or read error.
package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/resolve"
)

// Exit statuses shared by every command.
const (
	ExitOK       = 0
	ExitFindings = 1
	ExitUsage    = 2
)

// flagSet says which flags a command takes: true when the flag takes a value.
type flagSet map[string]bool

// parsed is a command line split into operands and flags.
type parsed struct {
	pos   []string
	flags map[string][]string
}

func (p *parsed) has(name string) bool { _, ok := p.flags[name]; return ok }

// get returns the last value of a flag, or "".
func (p *parsed) get(name string) string {
	v := p.flags[name]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

// parseArgs reads flags in any position; "--" ends them. A flag that takes a
// value accepts "--name value" and "--name=value". A repeated flag keeps
// every value.
func parseArgs(args []string, fs flagSet) (*parsed, error) {
	p := &parsed{flags: map[string][]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			p.pos = append(p.pos, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			p.pos = append(p.pos, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		takes, ok := fs[name]
		if !ok {
			return nil, fmt.Errorf("unknown flag %q", a)
		}
		if !takes {
			if hasVal {
				return nil, fmt.Errorf("flag --%s takes no value", name)
			}
			p.flags[name] = append(p.flags[name], "")
			continue
		}
		if !hasVal {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag --%s needs a value", name)
			}
			i++
			val = args[i]
		}
		p.flags[name] = append(p.flags[name], val)
	}
	return p, nil
}

// usageError writes the usage line for a command and returns exit status 2.
func usageError(stderr io.Writer, cmd, usage string, err error) int {
	if err != nil {
		fmt.Fprintf(stderr, "greenware %s: %v\n", cmd, err)
	}
	fmt.Fprintf(stderr, "usage: %s\n", usage)
	return ExitUsage
}

// writeJSON writes v as one indented JSON value with a trailing newline.
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

// Unit is one source file after the front end: its bytes, its lowered form
// and its diagnostics.
type Unit struct {
	Path string
	Src  []byte
	Tree *lang.Tree
	File *ast.File
	// Lowered are the diagnostics of parsing and lowering; Diags adds those of
	// evaluating the defaults and every output.
	Lowered []diag.Diagnostic
	Diags   []diag.Diagnostic
	Loc     diag.Locator
}

// Load reads a source file and runs the front end on it. project, when not
// empty, names the project root; otherwise the nearest greenware.mod decides.
func Load(path, project string) (*Unit, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadBytes(path, project, src), nil
}

// LoadBytes runs the front end on bytes that stand for the file at path. The
// path decides where project imports are read from.
func LoadBytes(path, project string, src []byte) *Unit {
	tree := lang.Parse(src)
	var proj *ast.Project
	var projErr error
	if path != "" {
		proj, projErr = ast.OpenProject(path, project)
	}
	f, ds := ast.LowerWith(tree, ast.Options{Project: proj})
	if projErr != nil && project != "" {
		ds = append(ds, diag.New(diag.CodeMissing, diag.Span{}, "%v", projErr))
	}
	u := &Unit{Path: path, Src: src, Tree: tree, File: f, Lowered: ds}
	u.Loc = func(off int) (int, int) {
		p := tree.Position(off)
		return p.Line, p.Column
	}
	all := append([]diag.Diagnostic(nil), ds...)
	if !f.Broken {
		_, more := resolve.EvaluateAll(f)
		all = append(all, more...)
	}
	u.Diags = diag.Dedup(all)
	return u
}

// countErrors returns the number of error diagnostics.
func countErrors(ds []diag.Diagnostic) int {
	n := 0
	for _, d := range ds {
		if d.Severity == diag.Error {
			n++
		}
	}
	return n
}

// printDiags writes diagnostics as "file:line:col: severity CODE: message".
func printDiags(w io.Writer, file string, ds []diag.Diagnostic, loc diag.Locator) {
	sorted := append([]diag.Diagnostic(nil), ds...)
	diag.Sort(sorted)
	for _, d := range sorted {
		l, c := loc(d.Span.Start)
		fmt.Fprintf(w, "%s:%d:%d: %s %s: %s\n", file, l, c, d.Severity, d.Code, d.Message)
	}
}

// diagJSON encodes diagnostics with the shared diag layout, for embedding.
func diagJSON(ds []diag.Diagnostic, loc diag.Locator) json.RawMessage {
	b, err := diag.Marshal(ds, loc)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return bytes.TrimSpace(b)
}

// srcText returns the source text of a span, or "" when the span is outside.
func srcText(src []byte, sp diag.Span) string {
	if sp.Start < 0 || sp.End > len(src) || sp.End < sp.Start {
		return ""
	}
	return string(src[sp.Start:sp.End])
}

// norm reduces source text to a form that ignores layout and comments: `//`
// comments are removed outside string literals, and white space is kept (as
// one space) only between two word characters, so `a = b` and `a=b` agree.
func norm(s string) string {
	var b strings.Builder
	inStr, space := false, false
	last := byte(0)
	emit := func(c byte) {
		if space && isWordByte(last) && isWordByte(c) {
			b.WriteByte(' ')
		}
		space = false
		b.WriteByte(c)
		last = c
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			b.WriteByte(c)
			if c == '\\' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			} else if c == '"' {
				inStr = false
				last = c
			}
			continue
		}
		switch {
		case c == '"':
			emit(c)
			inStr = true
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			space = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			space = true
		default:
			emit(c)
		}
	}
	return b.String()
}

func isWordByte(c byte) bool {
	return c >= 0x80 || c == '_' || c == '.' || c == '#' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
