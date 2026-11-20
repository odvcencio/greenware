package ast

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"m31labs.dev/greenware/datapkg"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/registry"
)

// Project is the set of files a source may read besides itself: the
// project-file imports of language reference 7.4 and the JSON of a table.
// FS is rooted at the project root. A path in a document is relative to the
// document's own directory, Dir (slash-separated, relative to the root, ""
// for the root itself). A path may climb with `..` as long as the result
// stays inside the root; an absolute path, a backslash or a result outside
// the root is refused before anything is read. A file lowered without a
// Project reads nothing from disk.
type Project struct {
	FS  fs.FS
	Dir string
	// chain lists the project files whose imports are being read, outermost
	// first, so that an import cycle is found.
	chain []string
}

// Pin is one project file a build read, with the SHA-256 of its bytes. The
// build's input record lists the pins.
type Pin struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Gear is a project file that an `import "file.gw" as name` brought in: the
// lowered file, its bytes' hash and the files it imports in turn.
type Gear struct {
	Path   string
	File   *File
	SHA256 string
	// Pins lists this file and every project file it imports, depth first.
	Pins []Pin
	// Diagnostics are those of the file's own lowering.
	Diagnostics []diag.Diagnostic
}

// Resolve turns a path written in the document into a root-relative logical
// name, or says why it is not a path inside the project.
func (p *Project) Resolve(name string) (string, error) {
	if p == nil || p.FS == nil {
		return "", fmt.Errorf("no project directory is known")
	}
	if name == "" || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") || (len(name) > 1 && name[1] == ':') {
		return "", fmt.Errorf("%q is not a relative, slash-separated path", name)
	}
	r := path.Join(p.Dir, name)
	if r == "." || !fs.ValidPath(r) {
		return "", fmt.Errorf("%q leaves the project", name)
	}
	return r, nil
}

// ReadFile reads a file of the project, named as the document writes it.
func (p *Project) ReadFile(name string) ([]byte, error) {
	b, _, err := p.Read(name)
	return b, err
}

// Read is ReadFile that also returns the root-relative name.
func (p *Project) Read(name string) ([]byte, string, error) {
	r, err := p.Resolve(name)
	if err != nil {
		return nil, "", err
	}
	b, err := fs.ReadFile(p.FS, r)
	return b, r, err
}

// OpenProject opens the project of a source file on disk: the root is the
// nearest enclosing directory that holds a `greenware.mod` file, or the
// file's own directory when there is none. root, when not empty, names the
// root explicitly and must contain the file.
func OpenProject(file, root string) (*Project, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(abs)
	if root != "" {
		if root, err = filepath.Abs(root); err != nil {
			return nil, err
		}
	} else {
		root = dir
		for d := dir; ; d = filepath.Dir(d) {
			if _, err := os.Stat(filepath.Join(d, "greenware.mod")); err == nil {
				root = d
				break
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%s is not inside the project root %s", file, root)
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		rel = ""
	}
	return &Project{FS: os.DirFS(root), Dir: rel}, nil
}

// Import reads, hashes and lowers the project file an import names. It
// follows the file's own project imports, so a cycle is found here: the error
// is a GW-CYCLE diagnostic that names the chain. A missing file or a path
// outside the project is a GW-MISSING diagnostic.
func (p *Project) Import(importPath string, sp diag.Span) (*Gear, []diag.Diagnostic) {
	if !strings.HasSuffix(importPath, ".gw") {
		return nil, []diag.Diagnostic{diag.New(diag.CodeUnknownName, sp, "the import %q is neither a `std/` library, a project file ending in .gw nor a data package such as \"football/world@1\"", importPath)}
	}
	b, clean, err := p.Read(importPath)
	if err != nil {
		return nil, []diag.Diagnostic{diag.New(diag.CodeMissing, sp, "cannot import the project file %q: %v", importPath, err)}
	}
	for i, c := range p.chain {
		if c == clean {
			cycle := append(append([]string(nil), p.chain[i:]...), clean)
			return nil, []diag.Diagnostic{diag.New(diag.CodeCycle, sp, "project files import each other in a cycle: %s", strings.Join(cycle, " -> "))}
		}
	}
	child := &Project{FS: p.FS, Dir: path.Dir(clean), chain: append(append([]string(nil), p.chain...), clean)}
	if child.Dir == "." {
		child.Dir = ""
	}
	f, ds := LowerWith(lang.Parse(b), Options{Project: child})
	sum := sha256.Sum256(b)
	g := &Gear{Path: clean, File: f, SHA256: hex.EncodeToString(sum[:]), Diagnostics: ds}
	g.Pins = []Pin{{Path: clean, SHA256: g.SHA256}}
	if diag.HasErrors(ds) {
		return g, nil
	}
	for _, im := range f.Imports {
		if strings.HasPrefix(im.Path, "std/") || datapkg.IsImport(im.Path) {
			continue
		}
		sub, more := child.Import(im.Path, im.PathSpan)
		if len(more) > 0 {
			// A problem inside the imported file is reported at this import.
			out := make([]diag.Diagnostic, len(more))
			for i, m := range more {
				out[i] = diag.New(m.Code, sp, "in %s: %s", clean, m.Message)
				if m.Code == diag.CodeCycle {
					out[i] = diag.New(m.Code, sp, "%s", m.Message)
				}
			}
			return nil, out
		}
		g.Pins = append(g.Pins, sub.Pins...)
	}
	return g, nil
}

// ImportData loads the data package an `import "football/world@1"` names. The
// lock is found from the project root, not from the importing file's folder:
// `football/world@1` is `football/world.lock` under the root. The pin is
// verified. A failure is a diagnostic at sp, by the kind of the error:
// GW-IMPORT-DATA, GW-UNIT, GW-IMPORT-CONTRACT or GW-LOCK. Nothing is fetched.
func (p *Project) ImportData(logical string, sp diag.Span) (*datapkg.Package, *diag.Diagnostic) {
	if p == nil || p.FS == nil {
		d := diag.New(diag.CodeImportData, sp, "importing the data package %q needs a project directory, and this source was read without one", logical)
		return nil, &d
	}
	pkg, err := datapkg.LoadFS(p.FS, logical)
	if err == nil {
		return pkg, nil
	}
	d := DataDiagnostic(err, sp)
	return nil, &d
}

// DataDiagnostic turns a datapkg error into the diagnostic of its kind.
func DataDiagnostic(err error, sp diag.Span) diag.Diagnostic {
	code := diag.CodeImportData
	var de *datapkg.Error
	if errors.As(err, &de) {
		switch de.Kind {
		case datapkg.KindUnit:
			code = diag.CodeUnit
		case datapkg.KindContract:
			code = diag.CodeImportContract
		case datapkg.KindLock:
			code = diag.CodeLock
		}
	}
	return diag.New(code, sp, "%s", err.Error())
}

// Mount is `mount name { asset = ... socket = ... mode = ... set { } fit { } }`.
type Mount struct {
	Name     Ident
	Span     diag.Span
	Settings []*Setting
	// Set holds the gear parameter values, in source order.
	Set     []*Setting
	SetSpan diag.Span
	// Fit holds the fit block's settings; nil when there is none.
	Fit     []*Setting
	FitSpan diag.Span
	HasFit  bool
}

// Table is `table name = json { path = ... columns = (...) types { } }`.
type Table struct {
	Name     Ident
	Kind     Ident
	Span     diag.Span
	Settings []*Setting
	// Types holds the explicit column types.
	Types []*Setting
}

// CheckCase is a named runtime point of `check runtime`.
type CheckCase struct {
	Name     Ident
	Span     diag.Span
	Settings []*Setting
	Set      []*Setting
}

func (l *lowerer) mount(n *lang.Node) *Mount {
	k, _ := registry.Lookup(registry.CtxDocument, "mount")
	m := &Mount{Name: ident(n.Field("name")), Span: span(n)}
	settings, rest := l.settings(bodyItems(n.Field("body")), k)
	m.Settings = settings
	for _, it := range rest {
		if it.Kind() != "block" {
			l.notAllowed(it, k)
			continue
		}
		switch it.Field("kind").Text() {
		case "set":
			if m.SetSpan.Len() > 0 {
				l.report(diag.New(diag.CodeDuplicate, span(it), "a mount has one `set` block").WithRelated(m.SetSpan, "first set block"))
				continue
			}
			sk, _ := registry.Lookup(registry.CtxDocument, "set")
			m.SetSpan = span(it)
			m.Set = l.settingsOnly(it.Field("body"), sk)
		case "fit":
			if m.HasFit {
				l.report(diag.New(diag.CodeDuplicate, span(it), "a mount has one `fit` block").WithRelated(m.FitSpan, "first fit block"))
				continue
			}
			fk, _ := registry.Lookup(registry.CtxDocument, "fit")
			m.HasFit, m.FitSpan = true, span(it)
			m.Fit = l.settingsOnly(it.Field("body"), fk)
		default:
			l.notAllowed(it, k)
		}
	}
	return m
}

func (l *lowerer) table(n *lang.Node) *Table {
	k, _ := registry.Lookup(registry.CtxDocument, "table")
	t := &Table{Name: ident(n.Field("name")), Kind: ident(n.Field("row_kind")), Span: span(n)}
	if t.Kind.Name != "json" {
		l.report(diag.New(diag.CodeUnsupported, t.Kind.Span, "a `%s` table is valid in the language but outside this compiler's subset; only `json` tables are read", t.Kind.Name))
	}
	settings, rest := l.settings(bodyItems(n.Field("body")), k)
	t.Settings = settings
	for _, it := range rest {
		if it.Kind() == "block" && it.Field("kind").Text() == "types" {
			if t.Types != nil {
				l.report(diag.New(diag.CodeDuplicate, span(it), "a table has one `types` block"))
				continue
			}
			tk, _ := registry.Lookup(registry.CtxDocument, "types")
			t.Types = l.settingsOnly(it.Field("body"), tk)
			if t.Types == nil {
				t.Types = []*Setting{}
			}
			continue
		}
		l.notAllowed(it, k)
	}
	return t
}

// checkBody lowers the body of `check rig` or `check runtime`.
func (l *lowerer) checkBody(c *Check, body *lang.Node, kind string) {
	k, _ := registry.Lookup(registry.CtxDocument, kind)
	settings, rest := l.settings(bodyItems(body), k)
	c.Settings = settings
	for _, it := range rest {
		if kind != "check runtime" || it.Kind() != "block" || it.Field("kind").Text() != "case" {
			l.notAllowed(it, k)
			continue
		}
		ck, _ := registry.Lookup(registry.CtxDocument, "case")
		cc := &CheckCase{Name: ident(it.Field("name")), Span: span(it)}
		if cc.Name.Name == "" {
			l.report(diag.New(diag.CodeMissing, span(it.Field("kind")), "`case` needs a name"))
			continue
		}
		s2, r2 := l.settings(bodyItems(it.Field("body")), ck)
		cc.Settings = s2
		for _, x := range r2 {
			if x.Kind() == "block" && x.Field("kind").Text() == "set" {
				sk, _ := registry.Lookup(registry.CtxDocument, "set")
				cc.Set = l.settingsOnly(x.Field("body"), sk)
				continue
			}
			l.notAllowed(x, ck)
		}
		c.Cases = append(c.Cases, cc)
	}
}
