// Command greenware compiles Greenware sources (.gw files).
//
//	greenware build <file> [--output NAME] [--out-dir DIR] [--max-work N] [--accept FILE] [--project DIR] [--runtime-points N] [--geometry-only] [--json]
//	greenware check <file> [--project DIR] [--geometry-only] [--json]
//	greenware fmt <file> [--check] [--write] [--json]
//	greenware accept <source.gw> <accept.gw> --glb-dir DIR [--json]
//	greenware print <file> [--output NAME] [--level N] [--scale S] [--out FILE] [--force] [--project DIR] [--json]
//	greenware fix | diff | describe | ir | init | lock | explain | search | trace (package tools; see docs/reference/commands.md)
//
// With --json the command writes exactly one JSON value to standard output.
// Exit status: 0 on success, 1 when the diagnostics hold an error (or fmt
// --check finds a file that is not formatted), 2 on a usage or read error. accept exits 0 when every expect passes, 1 when
// one fails or is refused, 3 when none failed but some could not be measured.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"m31labs.dev/greenware/accept"
	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/resolve"
)

const usage = `usage:
  greenware build <file> [--output NAME] [--out-dir DIR] [--max-work N] [--accept FILE] [--project DIR] [--runtime-points N] [--geometry-only] [--json]
  greenware check <file> [--project DIR] [--geometry-only] [--json]
  greenware fmt <file> [--check] [--write] [--json]
  greenware accept <source.gw> <accept.gw> --glb-dir DIR [--json]
` + printUsage + toolsUsage

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
	// maxWork is the total_work ceiling; zero means the default.
	maxWork uint64
	// runtimePoints is the `runtime_points` limit; zero means the default.
	runtimePoints int
	// project is the project root; empty finds the nearest greenware.mod.
	project string
	// accept is the acceptance file whose decode, measure and outline tickets
	// the build reserves.
	accept string
	// geometryOnly leaves out the look declarations the compiler does not read
	// yet (imports, plugs, textures, cells); every material is a plain grey pbr.
	geometryOnly bool
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
		case "geometry-only":
			o.geometryOnly = true
		case "write":
			o.write = true
		case "output":
			o.output, err = takes()
			o.hasName = true
		case "out-dir":
			o.outDir, err = takes()
			o.hasOut = true
		case "max-work":
			var v string
			if v, err = takes(); err == nil {
				n, perr := strconv.ParseUint(v, 10, 64)
				if perr != nil || n == 0 {
					return o, fmt.Errorf("flag --max-work needs a whole number of work units above zero, got %q", v)
				}
				o.maxWork = n
			}
		case "accept":
			o.accept, err = takes()
		case "project":
			o.project, err = takes()
		case "runtime-points":
			var v string
			if v, err = takes(); err == nil {
				n, perr := strconv.Atoi(v)
				if perr != nil || n < 1 {
					return o, fmt.Errorf("flag --runtime-points needs a whole number above zero, got %q", v)
				}
				o.runtimePoints = n
			}
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
	if h, ok := toolCommands[cmd]; ok {
		return h(rest, stdout, stderr)
	}
	var allowed map[string]bool
	switch cmd {
	case "build":
		allowed = map[string]bool{"output": true, "out-dir": true, "json": true, "max-work": true, "accept": true, "geometry-only": true, "project": true, "runtime-points": true}
	case "check":
		allowed = map[string]bool{"json": true, "geometry-only": true, "project": true}
	case "fmt":
		allowed = map[string]bool{"check": true, "write": true, "json": true}
	case "print":
		return runPrint(rest, stdout, stderr)
	case "accept":
		return accept.Command(rest, stdout, stderr)
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

// front lowers a source and returns its diagnostics and a locator. file and
// root say where the project is; an unreadable project leaves the source
// without one, and a project import then reports that.
func front(file, root string, src []byte, geometryOnly bool) (*ast.File, []diag.Diagnostic, diag.Locator) {
	tree := lang.Parse(src)
	var proj *ast.Project
	var projErr error
	if file != "" {
		proj, projErr = ast.OpenProject(file, root)
	}
	f, ds := ast.LowerWith(tree, ast.Options{GeometryOnly: geometryOnly, Project: proj})
	if projErr != nil && root != "" {
		ds = append(ds, diag.New(diag.CodeMissing, diag.Span{}, "%v", projErr))
	}
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
	f, lowered, loc := front(o.file, o.project, src, o.geometryOnly)
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
	for _, sk := range f.Skipped {
		fmt.Fprintf(stderr, "note: geometry only: skipped %s\n", sk)
	}
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
			_, lowered, loc := front(o.file, o.project, src, false)
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
	// The worst look and the stored counts, written only when a runtime branch
	// makes them differ from the default look.
	TrianglesWorst  int `json:"triangles_worst,omitempty"`
	TrianglesStored int `json:"triangles_stored,omitempty"`
	DrawsWorst      int `json:"draws_worst,omitempty"`
	// TextureBytes and Images describe the embedded images, when there are any.
	TextureBytes int                  `json:"texture_bytes,omitempty"`
	Images       []compiler.ImageInfo `json:"images,omitempty"`
}

type buildOutput struct {
	Name      string      `json:"name"`
	FirSHA256 string      `json:"fir_sha256"`
	Files     []buildFile `json:"files"`
}

type buildReport struct {
	Schema      string        `json:"schema"`
	OK          bool          `json:"ok"`
	Written     bool          `json:"written"`
	OutDir      string        `json:"out_dir,omitempty"`
	Record      string        `json:"record,omitempty"`
	InputSHA256 string        `json:"input_sha256,omitempty"`
	Outputs     []buildOutput `json:"outputs"`
	// Census holds the measured figures of every output and level.
	Census      []compiler.Census `json:"census"`
	Unsupported []string          `json:"unsupported"`
	// Cost is the ticket receipt, present on a refusal as well.
	Cost        *compiler.Receipt `json:"cost,omitempty"`
	Diagnostics json.RawMessage   `json:"diagnostics"`
}

func runBuild(o options, src []byte, stdout, stderr io.Writer) int {
	f, ds, loc := front(o.file, o.project, src, o.geometryOnly)
	opts := compiler.Options{Policy: compiler.Policy{MaxTotalWork: o.maxWork, RuntimePoints: o.runtimePoints}}
	if o.accept != "" {
		accSrc, err := os.ReadFile(o.accept)
		if err != nil {
			fmt.Fprintf(stderr, "greenware build: %v\n", err)
			return 2
		}
		accAST, accDs, accLoc := front(o.accept, o.project, accSrc, false)
		if accAST.Broken || diag.HasErrors(accDs) {
			printText(stderr, o.accept, accDs, accLoc)
			return 1
		}
		opts.Accept = accAST
	}
	var p *compiler.Product
	var receipt *compiler.Receipt
	if !diag.HasErrors(ds) {
		var more []diag.Diagnostic
		p, receipt, more = compiler.BuildWith(f, o.output, opts)
		ds = diag.Dedup(append(ds, more...))
	}
	rep := buildReport{Schema: "greenware.build/1", Outputs: []buildOutput{}, Census: []compiler.Census{}, Unsupported: []string{}, Cost: receipt}
	code := status(ds)
	var writeErr error
	if p != nil && code == 0 {
		if writeErr = compiler.Write(p, o.outDir); writeErr == nil {
			rep.Written = true
			rep.OutDir = o.outDir
			rep.Record = p.RecordName()
		}
		rep.InputSHA256 = p.InputSHA256
		rep.Census = p.Census()
		rep.Unsupported = append(rep.Unsupported, p.Unsupported...)
		for _, sk := range f.Skipped {
			rep.Unsupported = append(rep.Unsupported, "geometry only: skipped "+sk)
		}
		for _, op := range p.Outputs {
			bo := buildOutput{Name: op.Name, FirSHA256: op.FirSHA256, Files: []buildFile{}}
			for _, fl := range op.Files {
				bf := buildFile{Name: fl.Name, Level: fl.Level, Bytes: fl.Size, SHA256: fl.SHA256, Triangles: fl.Triangles, Draws: fl.Draws, Materials: fl.Materials, Vertices: fl.Vertices}
				if fl.TrianglesWorst != fl.Triangles || fl.DrawsWorst != fl.Draws || fl.TrianglesStored != fl.Triangles {
					bf.TrianglesWorst, bf.TrianglesStored, bf.DrawsWorst = fl.TrianglesWorst, fl.TrianglesStored, fl.DrawsWorst
				}
				bf.TextureBytes, bf.Images = fl.TextureBytes, fl.Images
				bo.Files = append(bo.Files, bf)
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
				extra := ""
				if fl.TrianglesWorst != 0 {
					extra = fmt.Sprintf(" (worst look %d triangles, %d draws; stored %d triangles)", fl.TrianglesWorst, fl.DrawsWorst, fl.TrianglesStored)
				}
				if fl.TextureBytes != 0 {
					extra += fmt.Sprintf(" (%d images, %d bytes decoded)", len(fl.Images), fl.TextureBytes)
				}
				fmt.Fprintf(stdout, "%s/%s  %d triangles, %d draws, %d bytes%s\n", o.outDir, fl.Name, fl.Triangles, fl.Draws, fl.Bytes, extra)
			}
		}
		fmt.Fprintf(stdout, "%s/%s\n", o.outDir, rep.Record)
		for _, n := range rep.Unsupported {
			fmt.Fprintf(stderr, "note: %s\n", n)
		}
	}
	return code
}
