package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/std"
)

const lockUsage = "greenware lock [<dir>] [--check] [--dry-run] [--json]"

// SumFile is the name of the lock file, beside greenware.mod.
const SumFile = "greenware.sum"

// A pin is one line of greenware.sum.
type pin struct {
	Kind, Name, Origin, Digest, License string
}

func (p pin) line() string {
	if p.Kind == "compiler" {
		return "compiler " + p.Name
	}
	return strings.Join([]string{p.Kind, quoteField(p.Name), p.Origin, p.Digest, quoteField(p.License)}, " ")
}

func (p pin) key() string { return p.Kind + " " + p.Name }

// quoteField quotes a field that is empty or holds white space or a quote.
func quoteField(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\"") {
		return fmt.Sprintf("%q", s)
	}
	return s
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// metaLicense returns the licence a lowered file declares in `meta`, or "-".
func metaLicense(f *ast.File) string {
	if f.Meta != nil {
		if st := ast.FindSetting(f.Meta.Settings, "license"); st != nil {
			if sl, ok := st.Value.(*ast.StringLit); ok && sl.Value != "" {
				return sl.Value
			}
		}
	}
	return "-"
}

// computePins lists the pins of the project at root: the compiler, every
// `.gw` file under root, every standard library file a source imports, and
// every table or Selena file a source names. It returns an error for a
// referenced file that is missing or leaves the project.
func computePins(root string) ([]pin, error) {
	pins := map[string]pin{}
	add := func(p pin) { pins[p.key()] = p }
	add(pin{Kind: "compiler", Name: compilerIdentity()})

	var sources []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".gw") && d.Type().IsRegular() {
			rel, _ := filepath.Rel(root, p)
			sources = append(sources, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(sources)
	var problems []string
	for _, rel := range sources {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		b, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		proj, _ := ast.OpenProject(abs, root)
		f, _ := ast.LowerWith(lang.Parse(b), ast.Options{Project: proj})
		add(pin{"source", rel, "project", sha(b), metaLicense(f)})
		for _, im := range f.Imports {
			if !strings.HasPrefix(im.Path, std.Prefix) {
				continue
			}
			lb, ok := std.Source(im.Path)
			if !ok {
				problems = append(problems, fmt.Sprintf("%s imports %q, which is not a shipped library", rel, im.Path))
				continue
			}
			lf, _ := ast.LowerWith(lang.Parse(lb), ast.Options{})
			add(pin{"lib", im.Path, "std", sha(lb), metaLicense(lf)})
		}
		dir := path.Dir(rel)
		ref := func(kind, name string) {
			joined := path.Join(dir, name)
			if name == "" || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") || joined == "." || strings.HasPrefix(joined, "..") {
				problems = append(problems, fmt.Sprintf("%s names %q, which is not a path inside the project", rel, name))
				return
			}
			rb, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(joined)))
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s names %q: %v", rel, name, err))
				return
			}
			add(pin{kind, joined, "project", sha(rb), "-"})
		}
		for _, t := range f.Tables {
			if st := ast.FindSetting(t.Settings, "path"); st != nil {
				if sl, ok := st.Value.(*ast.StringLit); ok {
					ref("table", sl.Value)
				}
			}
		}
		for _, m := range f.Materials {
			if m.Kind != "selena" {
				continue
			}
			if st := ast.FindSetting(m.Settings, "source"); st != nil {
				if sl, ok := st.Value.(*ast.StringLit); ok {
					ref("selena", sl.Value)
				}
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	out := make([]pin, 0, len(pins))
	for _, p := range pins {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		for _, pair := range [][2]string{{a.Kind, b.Kind}, {a.Name, b.Name}, {a.Origin, b.Origin}, {a.Digest, b.Digest}, {a.License, b.License}} {
			if pair[0] != pair[1] {
				return pair[0] < pair[1]
			}
		}
		return false
	})
	return out, nil
}

// compilerIdentity is the compiler line's value: the generator string that
// the compiler writes into every GLB, "greenware 0.1".
func compilerIdentity() string { return compiler.Generator }

func renderSum(pins []pin) []byte {
	var b bytes.Buffer
	for _, p := range pins {
		b.WriteString(p.line())
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// parseSum reads greenware.sum text into its lines keyed by "kind name".
func parseSum(b []byte) map[string]string {
	m := map[string]string{}
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "//") {
			continue
		}
		fields := strings.Fields(ln)
		if len(fields) < 2 {
			continue
		}
		name := fields[1]
		if fields[0] == "compiler" {
			name = strings.Join(fields[1:], " ")
			m["compiler"] = ln
			continue
		}
		m[fields[0]+" "+name] = ln
	}
	return m
}

// LockChange is one pin that differs from the lock file.
type LockChange struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

type lockReport struct {
	Schema       string       `json:"schema"`
	Dir          string       `json:"dir"`
	Mode         string       `json:"mode"`
	BeforeSHA256 string       `json:"before_sha256"`
	AfterSHA256  string       `json:"after_sha256"`
	Changes      []LockChange `json:"changes"`
	Drift        bool         `json:"drift"`
	Written      bool         `json:"written"`
	OK           bool         `json:"ok"`
}

// pinChanges compares the pins with the current lock file text.
func pinChanges(pins []pin, current []byte) []LockChange {
	old := parseSum(current)
	want := map[string]string{}
	var order []string
	for _, p := range pins {
		k := p.key()
		if p.Kind == "compiler" {
			k = "compiler"
		}
		want[k] = p.line()
		order = append(order, k)
	}
	changes := []LockChange{}
	kindName := func(k string) (string, string) {
		kind, name, _ := strings.Cut(k, " ")
		return kind, name
	}
	for _, k := range order {
		kind, name := kindName(k)
		if o, ok := old[k]; !ok {
			changes = append(changes, LockChange{kind, name, "", want[k]})
		} else if o != want[k] {
			changes = append(changes, LockChange{kind, name, o, want[k]})
		}
	}
	var gone []string
	for k := range old {
		if _, ok := want[k]; !ok {
			gone = append(gone, k)
		}
	}
	sort.Strings(gone)
	for _, k := range gone {
		kind, name := kindName(k)
		changes = append(changes, LockChange{kind, name, old[k], ""})
	}
	return changes
}

// Lock writes greenware.sum, the pins of every input of a project: the
// compiler identity, each .gw file, each standard library file that a source
// imports, and each table and Selena file a source names. Each pin is one
// line `kind name origin sha256:digest license`, sorted. With --check the
// command writes nothing and exits 1 when the file is missing or differs.
func Lock(args []string, stdout, stderr io.Writer) int {
	p, err := parseArgs(args, flagSet{"check": false, "dry-run": false, "json": false})
	if err != nil || len(p.pos) > 1 {
		if err == nil {
			err = fmt.Errorf("at most one directory is needed")
		}
		return usageError(stderr, "lock", lockUsage, err)
	}
	dir := "."
	if len(p.pos) == 1 {
		dir = p.pos[0]
	}
	if _, err := os.Stat(filepath.Join(dir, "greenware.mod")); err != nil {
		fmt.Fprintf(stderr, "greenware lock: %s has no greenware.mod\n", dir)
		return ExitUsage
	}
	pins, err := computePins(dir)
	if err != nil {
		fmt.Fprintf(stderr, "greenware lock: %v\n", err)
		return ExitFindings
	}
	next := renderSum(pins)
	sumPath := filepath.Join(dir, SumFile)
	cur, readErr := os.ReadFile(sumPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		fmt.Fprintf(stderr, "greenware lock: %v\n", readErr)
		return ExitUsage
	}
	rep := lockReport{Schema: "greenware.lock/1", Dir: dir, Mode: "write", BeforeSHA256: sha(cur), AfterSHA256: sha(next), Changes: pinChanges(pins, cur)}
	rep.Drift = !bytes.Equal(cur, next)
	switch {
	case p.has("check"):
		rep.Mode = "check"
	case p.has("dry-run"):
		rep.Mode = "dry-run"
	}
	code := ExitOK
	if rep.Mode == "check" && rep.Drift {
		code = ExitFindings
	}
	if rep.Mode == "write" && rep.Drift {
		if err := writeAtomic(sumPath, next); err != nil {
			fmt.Fprintf(stderr, "greenware lock: %v\n", err)
			return ExitUsage
		}
		rep.Written = true
	}
	rep.OK = code == ExitOK
	if p.has("json") {
		writeJSON(stdout, rep)
		return code
	}
	for _, c := range rep.Changes {
		mark := "~"
		switch {
		case c.Old == "":
			mark = "+"
		case c.New == "":
			mark = "-"
		}
		fmt.Fprintf(stdout, "%s\n", strings.TrimSpace(mark+" "+c.Kind+" "+c.Name))
	}
	switch {
	case rep.Mode == "check" && rep.Drift:
		fmt.Fprintf(stderr, "%s: out of date; run greenware lock\n", sumPath)
	case !rep.Drift:
		fmt.Fprintf(stdout, "%s: up to date\n", sumPath)
	case rep.Mode == "dry-run":
		fmt.Fprintf(stdout, "%s: would be written (%d pins)\n", sumPath, len(pins))
	case rep.Written:
		fmt.Fprintf(stdout, "%s: written (%d pins)\n", sumPath, len(pins))
	}
	return code
}

// writeAtomic writes a file by a temporary file and a rename.
func writeAtomic(name string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(name), ".greenware-sum-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), name)
}
