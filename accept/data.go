package accept

import (
	"sort"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/datapkg"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

// An acceptance file may import a typed data package with the same line an
// asset uses, `import "football/world@1" as world`, and read its fields in an
// expect as dimensioned values: `world.goal_post.inside_width` is a length in
// metres (docs/reference/data-package.md). The package is found from the
// project root and its pin is verified exactly as for an asset. This file
// loads the imports and checks every reference before any expect runs.

// Data is the loaded data imports of one acceptance file.
type Data struct {
	pkgs map[string]*datapkg.Package // by qualifier
	// Inputs records each package the file imported, sorted by contract,
	// profile and digest. The report carries it.
	Inputs []datapkg.Input
}

// LoadData loads and verifies every data import of an acceptance file that was
// lowered with its project (ast.LowerWith with Options.Project). It returns
// nil when the file imports nothing. A stale pin, a missing or malformed file,
// a contract mismatch, a wrong unit in the data, a repeated qualifier or an
// import that is not a data package is an error diagnostic at the import; when
// there is any, nothing should be evaluated.
func LoadData(f *ast.File) (*Data, []diag.Diagnostic) {
	if len(f.Imports) == 0 {
		return nil, nil
	}
	var ds []diag.Diagnostic
	d := &Data{pkgs: map[string]*datapkg.Package{}}
	seen := map[string]diag.Span{}
	for _, im := range f.Imports {
		if !datapkg.IsImport(im.Path) {
			ds = append(ds, diag.New(diag.CodeUnsupported, im.PathSpan, "an acceptance file imports data packages such as \"football/world@1\"; %q is not one", im.Path))
			continue
		}
		if prev, dup := seen[im.Qualifier]; dup {
			ds = append(ds, diag.New(diag.CodeDuplicate, im.QualSpan, "the import qualifier `%s` is used twice", im.Qualifier).WithRelated(prev, "first import"))
			continue
		}
		seen[im.Qualifier] = im.Span
		if !isIdent(im.Qualifier) {
			ds = append(ds, diag.New(diag.CodeType, im.QualSpan, "`%s` is not a name a qualifier can have; write `as name`", im.Qualifier))
			continue
		}
		pkg, dg := f.Project.ImportData(im.Path, im.PathSpan)
		if dg != nil {
			ds = append(ds, *dg)
			continue
		}
		d.pkgs[im.Qualifier] = pkg
		d.Inputs = append(d.Inputs, datapkg.Input{Contract: pkg.Contract, Profile: pkg.Profile, SHA256: pkg.Digest})
	}
	sort.Slice(d.Inputs, func(i, j int) bool {
		a, b := d.Inputs[i], d.Inputs[j]
		if a.Contract != b.Contract {
			return a.Contract < b.Contract
		}
		if a.Profile != b.Profile {
			return a.Profile < b.Profile
		}
		return a.SHA256 < b.SHA256
	})
	return d, ds
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// Check finds every reference to an imported package in the acceptance
// document and reports the ones that name no field (GW-UNKNOWN-NAME, with the
// nearest name as the fix) or a group of fields (GW-TYPE), at the span of the
// name in the acceptance file. A document with any is not evaluated.
func (d *Data) Check(acc *ast.Accept) []diag.Diagnostic {
	if d == nil {
		return nil
	}
	var ds []diag.Diagnostic
	var expr func(ast.Expr)
	expr = func(e ast.Expr) {
		switch x := e.(type) {
		case *ast.Qualified:
			if pkg, ok := d.pkgs[x.Parts[0]]; ok {
				if _, _, dg := resolve.LookupData(x, pkg); dg != nil {
					ds = append(ds, *dg)
				}
			}
		case *ast.Paren:
			expr(x.X)
		case *ast.Tuple:
			for _, el := range x.Elems {
				expr(el)
			}
		case *ast.List:
			for _, el := range x.Elems {
				expr(el)
			}
		case *ast.Unary:
			expr(x.X)
		case *ast.Binary:
			expr(x.L)
			expr(x.R)
		case *ast.If:
			expr(x.Cond)
			expr(x.Then)
			expr(x.Else)
		case *ast.Call:
			for _, a := range x.Args {
				expr(a.Value)
			}
		case *ast.Index:
			expr(x.X)
			expr(x.I)
		case *ast.Member:
			expr(x.X)
		}
	}
	var items func([]ast.AcceptItem)
	items = func(list []ast.AcceptItem) {
		for _, it := range list {
			switch x := it.(type) {
			case *ast.Expect:
				expr(x.Cond)
				expr(x.Message)
			case *ast.AcceptWhen:
				expr(x.Cond)
				items(x.Then)
				items(x.Else)
			}
		}
	}
	for _, s := range acc.Settings {
		expr(s.Value)
	}
	items(acc.Items)
	return ds
}

// value reads a qualified name as a data field. ok is false when the first
// part is not an import's qualifier, so the evaluator reads the name as it
// always has.
func (d *Data) value(n *ast.Qualified) (v units.Value, rest []string, ok bool) {
	if d == nil {
		return units.Value{}, nil, false
	}
	pkg, ok := d.pkgs[n.Parts[0]]
	if !ok {
		return units.Value{}, nil, false
	}
	v, used, dg := resolve.LookupData(n, pkg)
	if dg != nil {
		// Check reports this before evaluation; an evaluation that skipped
		// the check reads the name as a label like any unknown name.
		return units.Value{}, nil, false
	}
	return v, n.Parts[1+used:], true
}
