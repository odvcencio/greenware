package resolve

import (
	"sort"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/datapkg"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// A data import, `import "football/world@1" as world`, brings in a typed data
// package (decision 0020, docs/reference/data-package.md). Its fields are
// read as qualified names, `world.goal_post.inside_width`, and evaluate to
// dimensioned values: a length in metres, an angle in radians, a ratio or a
// number. The package is loaded, checked and pinned before any parameter or
// let is evaluated.

// DataImport is one resolved data import.
type DataImport struct {
	Path       string `json:"path"`
	Qualifier  string `json:"qualifier"`
	Contract   string `json:"contract"`
	Profile    string `json:"profile"`
	SHA256     string `json:"sha256"`
	LockFile   string `json:"lock_file"`
	SchemaFile string `json:"schema_file"`
	DataFile   string `json:"data_file"`
	// Pkg is the loaded package, for tools that show a field's meaning.
	Pkg *datapkg.Package `json:"-"`
}

// Input is the record of the import that a built file and the build record carry.
func (d DataImport) Input() datapkg.Input {
	return datapkg.Input{Contract: d.Contract, Profile: d.Profile, SHA256: d.SHA256}
}

// DataInputs lists the data packages this evaluation read, those of the file
// and those of the project files it imports, once each, sorted by contract
// (then profile and digest).
func (r *Resolved) DataInputs() []datapkg.Input {
	if r == nil {
		return nil
	}
	seen := map[datapkg.Input]bool{}
	var out []datapkg.Input
	var walk func(*Resolved)
	walk = func(x *Resolved) {
		if x == nil {
			return
		}
		for _, d := range x.Data {
			if in := d.Input(); !seen[in] {
				seen[in] = true
				out = append(out, in)
			}
		}
		for i := range x.Gears {
			walk(x.Gears[i].Resolved)
		}
	}
	walk(r)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Contract != b.Contract {
			return a.Contract < b.Contract
		}
		if a.Profile != b.Profile {
			return a.Profile < b.Profile
		}
		return a.SHA256 < b.SHA256
	})
	return out
}

// dataImport is a data import while the run is reading it. pkg is nil when
// the import failed: its references stay silent, so one fault gives one
// diagnostic.
type dataImport struct {
	pkg  *datapkg.Package
	span diag.Span
}

// dataImports binds every data import. It runs before parameters and lets,
// which may read a field.
func (r *run) dataImports() {
	r.data = map[string]*dataImport{}
	for _, im := range r.f.Imports {
		if !datapkg.IsImport(im.Path) {
			continue
		}
		if prev, dup := r.data[im.Qualifier]; dup {
			r.report(diag.New(diag.CodeDuplicate, im.QualSpan, "the import qualifier `%s` is used twice", im.Qualifier).WithRelated(prev.span, "first import"))
			continue
		}
		if im.Qualifier == "" || !isName(im.Qualifier) {
			r.report(diag.New(diag.CodeType, im.QualSpan, "`%s` is not a name a qualifier can have; write `as name`", im.Qualifier))
			continue
		}
		di := &dataImport{span: im.Span}
		r.data[im.Qualifier] = di
		pkg, d := r.f.Project.ImportData(im.Path, im.PathSpan)
		if d != nil {
			r.report(*d)
			continue
		}
		di.pkg = pkg
		r.resolved.Data = append(r.resolved.Data, DataImport{
			Path: im.Path, Qualifier: im.Qualifier, Contract: pkg.Contract, Profile: pkg.Profile, SHA256: pkg.Digest,
			LockFile: pkg.LockFile, SchemaFile: pkg.SchemaFile, DataFile: pkg.DataFile, Pkg: pkg,
		})
	}
	sort.SliceStable(r.resolved.Data, func(i, j int) bool { return r.resolved.Data[i].Path < r.resolved.Data[j].Path })
}

// DataValue converts a package value to a language value: a length in metres,
// an angle in radians, a ratio or number as a plain number. The one rounding
// to float64 is in datapkg.Value.Float64.
func DataValue(v datapkg.Value) units.Value {
	f := v.Float64()
	switch v.Dimension {
	case datapkg.Length:
		return units.Len(f)
	case datapkg.Angle:
		return units.Ang(f)
	}
	return units.Num(f)
}

// dataRef evaluates `world.goal_post.inside_width`: the qualifier, then the
// path of a field. A name that is not a field is GW-UNKNOWN-NAME at that name,
// with the nearest name at that level as the fix; a group of fields is
// GW-TYPE.
func (r *run) dataRef(n *ast.Qualified, di *dataImport) units.Value {
	if di.pkg == nil {
		return units.Bad()
	}
	out, used, d := LookupData(n, di.pkg)
	if d != nil {
		r.report(*d)
		return units.Bad()
	}
	for j := used; j < len(n.Parts)-1; j++ {
		out = r.member(out, n.Parts[j+1], partSpan(n, j+1))
	}
	return out
}

// partSpan is the span of the i-th dotted part of a qualified name.
func partSpan(n *ast.Qualified, i int) diag.Span {
	at := n.Span().Start
	for _, p := range n.Parts[:i] {
		at += len(p) + 1
	}
	return diag.Span{Start: at, End: at + len(n.Parts[i])}
}

// LookupData reads the field a qualified name `q.a.b` names in pkg, where q is
// the import's qualifier. It returns the value and used, the number of path
// parts after the qualifier that the field took (a longer name is a member
// read on the value, which the caller decides). A name that is not a field is
// a GW-UNKNOWN-NAME diagnostic at that part with the nearest name as the fix;
// a group of fields is GW-TYPE. The compiler and the accept evaluator share it.
func LookupData(n *ast.Qualified, pkg *datapkg.Package) (units.Value, int, *diag.Diagnostic) {
	path := n.Parts[1:]
	for i := range path {
		prefix := path[:i+1]
		if v, ok := pkg.Value(prefix...); ok {
			return DataValue(v), i + 1, nil
		}
		if _, group := pkg.Group(prefix...); group {
			continue
		}
		sp := partSpan(n, i+1)
		parent := path[:i]
		kids, _ := pkg.Group(parent...)
		where := n.Parts[0]
		if i > 0 {
			where += "." + strings.Join(parent, ".")
		}
		d := diag.New(diag.CodeUnknownName, sp, "`%s` is not a field of `%s` in the data package %s", path[i], where, pkg.Logical)
		if s, ok := registry.Suggest(path[i], kids); ok {
			d = d.WithEdit("write "+s, sp, s)
		} else if len(kids) > 0 {
			d.Message += "; it has " + strings.Join(kids, ", ")
		}
		return units.Bad(), 0, &d
	}
	kids, _ := pkg.Group(path...)
	d := diag.New(diag.CodeType, n.Span(), "`%s` is a group of fields in the data package %s, not a value; name one of %s", strings.Join(n.Parts, "."), pkg.Logical, strings.Join(kids, ", "))
	return units.Bad(), 0, &d
}
