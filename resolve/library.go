package resolve

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/datapkg"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/std"
)

// library is an imported library, read like project source.
type library struct {
	path      string
	qualifier string
	file      *ast.File
	sha256    string
	spanOf    diag.Span
}

// Import is one resolved import: the path, the qualifier its references use
// and the SHA-256 of the library's bytes, the pin that the build's input hash
// records.
type Import struct {
	Path      string `json:"path"`
	Qualifier string `json:"qualifier"`
	SHA256    string `json:"sha256"`
}

// imports binds every import before any reference is resolved, so a forward
// reference works. A duplicate qualifier fails.
func (r *run) imports() {
	r.libs = map[string]*library{}
	for _, im := range r.f.Imports {
		if datapkg.IsImport(im.Path) {
			continue // read by dataImports, before the lets
		}
		if _, dup := r.data[im.Qualifier]; dup {
			r.report(diag.New(diag.CodeDuplicate, im.QualSpan, "the import qualifier `%s` is used twice", im.Qualifier).WithRelated(r.data[im.Qualifier].span, "first import"))
			continue
		}
		if prev, dup := r.libs[im.Qualifier]; dup {
			r.report(diag.New(diag.CodeDuplicate, im.QualSpan, "the import qualifier `%s` is used twice", im.Qualifier).WithRelated(prev.span(), "first import"))
			continue
		}
		switch {
		case im.Qualifier == "" || !isName(im.Qualifier):
			r.report(diag.New(diag.CodeType, im.QualSpan, "`%s` is not a name a qualifier can have; write `as name`", im.Qualifier))
			continue
		}
		if !strings.HasPrefix(im.Path, std.Prefix) && !std.IsPlanned(im.Path) {
			if g := r.loadGear(im); g != nil {
				r.gears[im.Qualifier] = g
				r.resolved.Gears = append(r.resolved.Gears, *g)
			}
			continue
		}
		lib := r.loadLibrary(im)
		if lib == nil {
			continue
		}
		lib.qualifier = im.Qualifier
		r.libs[im.Qualifier] = lib
		r.resolved.Imports = append(r.resolved.Imports, Import{Path: im.Path, Qualifier: im.Qualifier, SHA256: lib.sha256})
	}
	sort.SliceStable(r.resolved.Imports, func(i, j int) bool { return r.resolved.Imports[i].Path < r.resolved.Imports[j].Path })
}

func (l *library) span() diag.Span { return l.spanOf }

func isName(s string) bool {
	for i, c := range s {
		switch {
		case c == '_' || (c >= 'a' && c <= 'z'):
		case i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return s != ""
}

func (r *run) loadLibrary(im *ast.Import) *library {
	switch {
	case std.IsPlanned(im.Path):
		r.report(diag.New(diag.CodeUnsupported, im.PathSpan, "the library `%s` is valid in the language but is not shipped by this compiler; std ships %v", im.Path, std.Names()))
		return nil
	case len(im.Path) < len(std.Prefix) || im.Path[:len(std.Prefix)] != std.Prefix:
		r.report(diag.New(diag.CodeUnsupported, im.PathSpan, "importing the project file %q is valid in the language but outside this compiler's subset; only `std/` libraries are read", im.Path))
		return nil
	}
	f, ds, ok := ast.Library(im.Path)
	if !ok {
		d := diag.New(diag.CodeUnknownName, im.PathSpan, "no library is named %q", im.Path)
		var cands []string
		for _, n := range std.Names() {
			cands = append(cands, std.Prefix+n)
		}
		if s, ok := registry.Suggest(im.Path, cands); ok {
			d = d.WithEdit("write "+s, im.PathSpan, `"`+s+`"`)
		}
		r.report(d)
		return nil
	}
	if diag.HasErrors(ds) {
		r.report(diag.New(diag.CodeSyntax, im.PathSpan, "the library %q does not read cleanly: %s", im.Path, ds[0].Message))
		return nil
	}
	b, _ := std.Source(im.Path)
	sum := sha256.Sum256(b)
	return &library{path: im.Path, file: f, sha256: hex.EncodeToString(sum[:]), spanOf: im.Span}
}
