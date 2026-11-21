package athleteparity

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/lang"
)

// BuildOptions say what to build.
type BuildOptions struct {
	// Source is the path of the athlete's .gw file.
	Source string
	// Root is the project root that the source's imports are read inside. When
	// empty, the nearest enclosing directory with a greenware.mod; when the
	// source has none above it, the examples directory of the repository
	// (RepoRoot/examples), with the source served inside it, so a copy of the
	// athlete outside examples can still import "../helmet/helmet.gw".
	Root string
	// RepoRoot is the directory with go.mod; used to find the examples.
	RepoRoot string
	// Output names the output to build. The athlete's "match" file is the
	// runtime-mount body that the game's ledger counts; when the source has no
	// output of that name, the first output is built.
	Output string
	// Aliases map a shape name to the game's shell name.
	Aliases map[string]string
}

// Built is a compiled athlete.
type Built struct {
	Model    *Model
	Output   string
	File     string
	Warnings []string
}

// Build compiles the source's level-0 file of the chosen output and decodes
// it. It returns an error when the source does not compile.
func Build(o BuildOptions) (*Built, error) {
	src, err := os.ReadFile(o.Source)
	if err != nil {
		return nil, err
	}
	proj, err := openProject(o)
	if err != nil {
		return nil, err
	}
	f, ds := ast.LowerWith(lang.Parse(src), ast.Options{Project: proj})
	if diag.HasErrors(ds) {
		return nil, fmt.Errorf("the source does not compile:\n%s", join(ds))
	}
	name := o.Output
	if name == "" {
		name = "match"
	}
	p, ds2 := compiler.Build(f, name)
	if p == nil && name != "" && hasCode(ds2, diag.CodeUnknownName) {
		name = ""
		p, ds2 = compiler.Build(f, name)
	}
	if p == nil || len(p.Outputs) == 0 {
		return nil, fmt.Errorf("the build failed:\n%s", join(append(ds, ds2...)))
	}
	op := p.Outputs[0]
	file := op.Files[0]
	doc, err := gltf.Decode(file.GLB)
	if err != nil {
		return nil, fmt.Errorf("the built file does not decode: %w", err)
	}
	m, err := NewModel(file.Name, doc, file.Parts, o.Aliases)
	if err != nil {
		return nil, err
	}
	b := &Built{Model: m, Output: op.Name, File: file.Name}
	for _, d := range append(ds, ds2...) {
		if !diag.HasErrors([]diag.Diagnostic{d}) {
			b.Warnings = append(b.Warnings, d.String())
		}
	}
	return b, nil
}

func hasCode(ds []diag.Diagnostic, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

func join(ds []diag.Diagnostic) string {
	var s []string
	for _, d := range ds {
		s = append(s, "  "+d.String())
	}
	return strings.Join(s, "\n")
}

// virtualDir is where a source outside the project root is served.
const virtualDir = "_athleteparity"

func openProject(o BuildOptions) (*ast.Project, error) {
	if o.Root != "" {
		return projectIn(o.Source, o.Root)
	}
	abs, err := filepath.Abs(o.Source)
	if err != nil {
		return nil, err
	}
	for d := filepath.Dir(abs); ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "greenware.mod")); err == nil {
			return ast.OpenProject(o.Source, d)
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	if o.RepoRoot != "" {
		ex := filepath.Join(o.RepoRoot, "examples")
		if _, err := os.Stat(filepath.Join(ex, "greenware.mod")); err == nil {
			return projectIn(o.Source, ex)
		}
	}
	return ast.OpenProject(o.Source, "")
}

// projectIn opens the project at root; a source outside root is served from
// memory as root/_athleteparity/<name>, one level below the root.
func projectIn(source, root string) (*ast.Project, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absSrc, err := filepath.Abs(source)
	if err != nil {
		return nil, err
	}
	if rel, err := filepath.Rel(absRoot, absSrc); err == nil && !strings.HasPrefix(rel, "..") {
		return ast.OpenProject(source, absRoot)
	}
	data, err := os.ReadFile(absSrc)
	if err != nil {
		return nil, err
	}
	return &ast.Project{FS: overlayFS{root: os.DirFS(absRoot), name: path.Join(virtualDir, filepath.Base(absSrc)), data: data}, Dir: virtualDir}, nil
}

// overlayFS is a project directory with one more file: the source, held in
// memory at a name under the root.
type overlayFS struct {
	root fs.FS
	name string
	data []byte
}

func (o overlayFS) Open(name string) (fs.File, error) {
	if name == o.name {
		return &memFile{name: path.Base(name), data: o.data}, nil
	}
	return o.root.Open(name)
}

func (o overlayFS) ReadFile(name string) ([]byte, error) {
	if name == o.name {
		return append([]byte(nil), o.data...), nil
	}
	return fs.ReadFile(o.root, name)
}

type memFile struct {
	name string
	data []byte
	off  int
}

func (f *memFile) Stat() (fs.FileInfo, error) { return memInfo{f}, nil }
func (f *memFile) Close() error               { return nil }
func (f *memFile) Read(b []byte) (int, error) {
	if f.off >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(b, f.data[f.off:])
	f.off += n
	return n, nil
}

type memInfo struct{ f *memFile }

func (i memInfo) Name() string       { return i.f.name }
func (i memInfo) Size() int64        { return int64(len(i.f.data)) }
func (i memInfo) Mode() fs.FileMode  { return 0o444 }
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }

// FindRepoRoot walks up from dir to the directory with a go.mod.
func FindRepoRoot(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("no go.mod above %s", dir)
		}
		d = filepath.Dir(d)
	}
}
