package accept

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/units"
)

// Exit statuses of Command.
const (
	ExitPass        = 0
	ExitFail        = 1
	ExitUsage       = 2
	ExitUnavailable = 3
)

const usage = `usage: greenware accept <source.gw> <accept.gw> --glb-dir DIR [--json] [--lod0-error LEN] [--geometry-only]

Runs the acceptance file on the decoded GLB files that the source's output
blocks name, in DIR, at every level. It compiles nothing.

  --glb-dir DIR      folder holding the GLB files (required)
  --json             write the report as JSON on stdout
  --lod0-error LEN   level 0's lod_error, such as 2.99mm, when the GLB does not record it
  --geometry-only    read the source as greenware build --geometry-only does

exit status: 0 all pass, 1 any fail or refused, 2 usage or unreadable input,
3 none failed but some could not be measured (for example outline)
`

// Command implements `greenware accept`. It returns the exit status.
func Command(args []string, stdout, stderr io.Writer) int {
	var pos []string
	var glbDir, lod0 string
	var asJSON, haveDir, geomOnly bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func(name string) (string, bool) {
			if strings.HasPrefix(a, name+"=") {
				return a[len(name)+1:], true
			}
			if a == name && i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case a == "--json":
			asJSON = true
		case a == "--geometry-only":
			geomOnly = true
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, usage)
			return ExitPass
		case a == "--glb-dir" || strings.HasPrefix(a, "--glb-dir="):
			v, ok := value("--glb-dir")
			if !ok {
				fmt.Fprintf(stderr, "greenware accept: --glb-dir needs a folder\n\n%s", usage)
				return ExitUsage
			}
			glbDir, haveDir = v, true
		case a == "--lod0-error" || strings.HasPrefix(a, "--lod0-error="):
			v, ok := value("--lod0-error")
			if !ok {
				fmt.Fprintf(stderr, "greenware accept: --lod0-error needs a length\n\n%s", usage)
				return ExitUsage
			}
			lod0 = v
		case strings.HasPrefix(a, "-") && a != "-":
			fmt.Fprintf(stderr, "greenware accept: unknown flag %s\n\n%s", a, usage)
			return ExitUsage
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) != 2 || !haveDir {
		fmt.Fprint(stderr, usage)
		return ExitUsage
	}
	var lod0Err *float64
	if lod0 != "" {
		v, err := units.ParseNumber(lod0)
		if err != nil || v.Kind != units.KNumber || (!v.Pending && v.Dim != units.Length) {
			fmt.Fprintf(stderr, "greenware accept: --lod0-error %q is not a length\n", lod0)
			return ExitUsage
		}
		x := v.Num // a bare number is metres
		lod0Err = &x
	}

	srcPath, accPath := pos[0], pos[1]
	srcText, ok := loadFile(srcPath, stderr)
	if !ok {
		return ExitUsage
	}
	accText, ok := loadFile(accPath, stderr)
	if !ok {
		return ExitUsage
	}
	srcAST, srcDiags := ast.LowerWith(lang.Parse(srcText), ast.Options{GeometryOnly: geomOnly})
	if srcAST.Broken || diag.HasErrors(srcDiags) {
		printDiags(stderr, srcPath, srcText, srcDiags)
		return ExitUsage
	}
	accAST, accDiags := ast.Lower(lang.Parse(accText))
	if accAST.Broken || diag.HasErrors(accDiags) {
		printDiags(stderr, accPath, accText, accDiags)
		return ExitUsage
	}
	if len(accAST.Accepts) == 0 {
		fmt.Fprintf(stderr, "greenware accept: %s has no accept block\n", accPath)
		return ExitUsage
	}

	plan := stagedFiles(srcAST)
	var reports []*Report
	exit := ExitPass
	for _, acc := range accAST.Accepts {
		inputs := loadInputs(acc, plan, glbDir, lod0Err)
		rep, ds := Run(acc, inputs)
		reports = append(reports, rep)
		if !asJSON {
			writeText(stdout, rep)
		}
		printDiags(stderr, accPath, accText, ds)
		switch c := rep.ExitCode(); {
		case c == ExitFail:
			exit = ExitFail
		case c == ExitUnavailable && exit == ExitPass:
			exit = ExitUnavailable
		}
	}
	if asJSON {
		var b []byte
		var err error
		if len(reports) == 1 {
			b, err = reports[0].JSON()
		} else {
			b, err = json.MarshalIndent(reports, "", "  ")
			b = append(b, '\n')
		}
		if err != nil {
			fmt.Fprintf(stderr, "greenware accept: %v\n", err)
			return ExitUsage
		}
		stdout.Write(b)
	}
	return exit
}

func loadFile(path string, stderr io.Writer) ([]byte, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "greenware accept: %v\n", err)
		return nil, false
	}
	return b, true
}

// stagedLevel is one GLB the source says it writes.
type stagedLevel struct {
	lod      int
	file     string
	maxError float64 // metres; meaningful for lod > 0
	known    bool
	distance float64
}

// stagedFiles lists, for each output of the source, the GLB file names its
// levels are written to: the declared file for level 0, and for `lods = files`
// the same name with `-lod<N>` before the extension.
func stagedFiles(f *ast.File) map[string][]stagedLevel {
	lengthScale := 1.0
	if f.Asset != nil {
		if s := ast.FindSetting(f.Asset.Settings, "length"); s != nil {
			if n, ok := s.Value.(*ast.Name); ok {
				if sc, ok := units.LengthUnit(n.Name); ok {
					lengthScale = sc
				}
			}
		}
	}
	lengthOf := func(s *ast.Setting) (float64, bool) {
		if s == nil {
			return 0, false
		}
		if n, ok := s.Value.(*ast.NumberLit); ok && n.Value.Kind == units.KNumber {
			if n.Value.Pending {
				return float64(n.Value.Num * lengthScale), true
			}
			if n.Value.Dim == units.Length {
				return n.Value.Num, true
			}
		}
		return 0, false
	}
	plan := map[string][]stagedLevel{}
	for _, o := range f.Outputs {
		fileSet := ast.FindSetting(o.Settings, "file")
		lit, ok := (*ast.StringLit)(nil), false
		if fileSet != nil {
			lit, ok = fileSet.Value.(*ast.StringLit)
		}
		if !ok {
			continue
		}
		levels := []stagedLevel{{lod: 0, file: lit.Value}}
		if s := ast.FindSetting(o.Settings, "lods"); s != nil {
			if n, ok := s.Value.(*ast.Name); ok && n.Name == "files" {
				ext := filepath.Ext(lit.Value)
				stem := strings.TrimSuffix(lit.Value, ext)
				lods := append([]*ast.Lod(nil), f.Lods...)
				sort.Slice(lods, func(i, j int) bool { return lods[i].Level < lods[j].Level })
				for _, l := range lods {
					sl := stagedLevel{lod: l.Level, file: fmt.Sprintf("%s-lod%d%s", stem, l.Level, ext)}
					sl.maxError, sl.known = lengthOf(ast.FindSetting(l.Settings, "max_error"))
					sl.distance, _ = lengthOf(ast.FindSetting(l.Settings, "distance"))
					levels = append(levels, sl)
				}
			}
		}
		plan[o.Name.Name] = levels
	}
	return plan
}

// loadInputs reads and decodes the GLB of every staged level of the outputs
// the acceptance file selects.
func loadInputs(acc *ast.Accept, plan map[string][]stagedLevel, dir string, lod0 *float64) Inputs {
	inputs := Inputs{}
	names := selectOutputs(acc, nil)
	if len(names) == 0 {
		for n := range plan {
			names = append(names, n)
		}
		sort.Strings(names)
	}
	for _, name := range names {
		levels, ok := plan[name]
		if !ok {
			inputs[Key{name, 0}] = Product{Problem: fmt.Sprintf("the source declares no output %q", name)}
			continue
		}
		for _, l := range levels {
			p := Product{File: l.file, Distance: l.distance}
			b, err := os.ReadFile(filepath.Join(dir, l.file))
			if err != nil {
				p.Problem = fmt.Sprintf("cannot read %s: %v", l.file, err)
				inputs[Key{name, l.lod}] = p
				continue
			}
			doc, err := gltf.Decode(b)
			if err != nil {
				p.Problem = fmt.Sprintf("cannot decode %s: %v", l.file, err)
				inputs[Key{name, l.lod}] = p
				continue
			}
			p.Doc = doc
			switch {
			case l.lod > 0 && l.known:
				p.LODError = l.maxError
			case lod0 != nil && l.lod == 0:
				p.LODError = *lod0
			default:
				if e, ok := recordedLODError(doc); ok && l.lod == 0 {
					p.LODError = e
				} else {
					p.LODErrorUnknown = true
				}
			}
			inputs[Key{name, l.lod}] = p
		}
	}
	return inputs
}

// recordedLODError reads the level's lod_error (metres) from the GLB's
// top-level extras: {"greenware": {"lod_error": 0.00299}}. The spec says the
// compiler records the level-0 value but does not say where; this is the
// place this evaluator looks, and --lod0-error overrides it.
func recordedLODError(doc *gltf.Document) (float64, bool) {
	if len(doc.Extras) == 0 {
		return 0, false
	}
	var ex struct {
		Greenware struct {
			LODError *float64 `json:"lod_error"`
		} `json:"greenware"`
	}
	if json.Unmarshal(doc.Extras, &ex) != nil || ex.Greenware.LODError == nil {
		return 0, false
	}
	return *ex.Greenware.LODError, true
}

// writeText prints one line per expect, grouped by output and level.
func writeText(w io.Writer, r *Report) {
	fmt.Fprintf(w, "accept %s\n", r.Accept)
	for _, l := range r.Levels {
		head := fmt.Sprintf("output %s lod %d", l.Output, l.LOD)
		if l.File != "" {
			head += " (" + l.File + ")"
		}
		fmt.Fprintln(w, head)
		if l.Problem != "" {
			fmt.Fprintf(w, "  MISSING  %s\n", l.Problem)
			continue
		}
		for _, e := range l.Expects {
			fmt.Fprintf(w, "  %-11s %s\n", strings.ToUpper(string(e.Verdict)), e.Message)
			if e.Verdict != Pass {
				if e.Detail != "" {
					fmt.Fprintf(w, "              %s\n", e.Detail)
				}
				for _, m := range e.Measures {
					fmt.Fprintf(w, "              %s = %s\n", m.Name, m.Value)
				}
			}
		}
	}
	t := r.Totals
	fmt.Fprintf(w, "pass %d, fail %d, refused %d, unavailable %d, missing %d\n", t.Pass, t.Fail, t.Refused, t.Unavailable, t.Missing)
}

// printDiags writes diagnostics as `file:line:col: severity CODE: message`.
func printDiags(w io.Writer, path string, src []byte, ds []diag.Diagnostic) {
	sorted := append([]diag.Diagnostic(nil), ds...)
	diag.Sort(sorted)
	for _, d := range sorted {
		line, col := lineCol(src, d.Span.Start)
		fmt.Fprintf(w, "%s:%d:%d: %s %s: %s\n", path, line, col, d.Severity, d.Code, d.Message)
	}
}

func lineCol(src []byte, off int) (int, int) {
	if off > len(src) {
		off = len(src)
	}
	line, start := 1, 0
	for i := 0; i < off; i++ {
		if src[i] == '\n' {
			line++
			start = i + 1
		}
	}
	return line, off - start + 1
}
