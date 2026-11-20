package resolve

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// This file reads `table name = json { path columns types }` (language
// reference 6.5): a JSON file of the project, a top-level array of objects,
// decoded against column types. A column's type is the unique type its uses
// require, or the one `types` gives. The rows are data: the file's bytes are
// pinned, nothing in them is evaluated, and no network is touched.

// ColType is the type of a table column.
type ColType struct {
	// Kind is string, color, enum, switch, number, integer, ratio, length,
	// angle or duration.
	Kind string
	Enum []string
}

func (c ColType) String() string {
	if c.Kind == "enum" {
		return "enum(" + strings.Join(c.Enum, ", ") + ")"
	}
	return c.Kind
}

// Table is a decoded table.
type Table struct {
	Name    string
	Path    string
	SHA256  string // of the file's bytes
	Columns []string
	Types   []ColType
	// Rows holds one value per column, in column order.
	Rows [][]units.Value
	// Keys is the unique name of each row: its `key` column when the table has
	// a string column of that name, else the decimal row number.
	Keys []string
}

// Col returns the index of a column, or -1.
func (t *Table) Col(name string) int {
	for i, c := range t.Columns {
		if c == name {
			return i
		}
	}
	return -1
}

// rowRef reads `row.column` as an expression.
func rowRef(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.Qualified:
		if len(x.Parts) == 2 && x.Parts[0] == "row" {
			return x.Parts[1], true
		}
	case *ast.Member:
		if n, ok := x.X.(*ast.Name); ok && n.Name == "row" {
			return x.Name, true
		}
	}
	return "", false
}

// forEachTable returns the table an output iterates, or "".
func forEachTable(o *ast.Output) (string, diag.Span) {
	s := ast.FindSetting(o.Settings, "for_each")
	if s == nil {
		return "", diag.Span{}
	}
	if n, ok := s.Value.(*ast.Name); ok {
		return n.Name, n.Span()
	}
	return "", s.Span
}

// templateColumns lists the `{row.column}` placeholders of a file name.
func templateColumns(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "{row.")
		if i < 0 {
			return out
		}
		j := strings.Index(s[i:], "}")
		if j < 0 {
			return out
		}
		out = append(out, s[i+len("{row."):i+j])
		s = s[i+j+1:]
	}
}

// columnTypeOfParam maps a parameter's declared type to a column type.
func columnTypeOfParam(p *ast.Param) (ColType, bool) {
	t, ok := registry.ParamType(p.Type.Name)
	if !ok {
		return ColType{}, false
	}
	switch t.Kind {
	case registry.TColor:
		return ColType{Kind: "color"}, true
	case registry.TSwitch:
		return ColType{Kind: "switch"}, true
	case registry.TInteger:
		return ColType{Kind: "integer"}, true
	case registry.TNumber:
		if p.Type.Name == "ratio" {
			return ColType{Kind: "ratio"}, true
		}
		return ColType{Kind: "number"}, true
	case registry.TLength:
		return ColType{Kind: "length"}, true
	case registry.TAngle:
		return ColType{Kind: "angle"}, true
	case registry.TDuration:
		return ColType{Kind: "duration"}, true
	case registry.TString:
		return ColType{Kind: "string"}, true
	case registry.TEnum:
		c := ColType{Kind: "enum"}
		for _, m := range p.Type.Enum {
			c.Enum = append(c.Enum, m.Name)
		}
		return c, true
	}
	return ColType{}, false
}

// explicitType reads a `types` entry: `string`, a parameter type name or
// `enum(a, b)`.
func explicitType(e ast.Expr) (ColType, bool) {
	switch x := e.(type) {
	case *ast.Name:
		if x.Name == "string" {
			return ColType{Kind: "string"}, true
		}
		return columnTypeOfParam(&ast.Param{Type: ast.ParamType{Name: x.Name}})
	case *ast.Call:
		if x.Func == "enum" {
			c := ColType{Kind: "enum"}
			for _, a := range x.Args {
				n, ok := a.Value.(*ast.Name)
				if !ok {
					return ColType{}, false
				}
				c.Enum = append(c.Enum, n.Name)
			}
			return c, len(c.Enum) > 0
		}
	}
	return ColType{}, false
}

func sameType(a, b ColType) bool {
	if a.Kind != b.Kind || len(a.Enum) != len(b.Enum) {
		return false
	}
	for i := range a.Enum {
		if a.Enum[i] != b.Enum[i] {
			return false
		}
	}
	return true
}

// nameList reads (a, b, c) or [a, b, c] as names.
func nameListOf(e ast.Expr) ([]string, bool) {
	var elems []ast.Expr
	switch x := e.(type) {
	case *ast.Tuple:
		elems = x.Elems
	case *ast.List:
		elems = x.Elems
	case *ast.Name:
		return []string{x.Name}, true
	default:
		return nil, false
	}
	var out []string
	for _, el := range elems {
		n, ok := el.(*ast.Name)
		if !ok {
			return nil, false
		}
		out = append(out, n.Name)
	}
	return out, len(out) > 0
}

// loadTables reads and decodes every table of the file. The result is empty
// without tables. Diagnostics are GW-TABLE-ROW for data and GW-MISSING for a
// file that cannot be read.
func loadTables(f *ast.File) (map[string]*Table, []diag.Diagnostic) {
	out := map[string]*Table{}
	var ds []diag.Diagnostic
	for _, td := range f.Tables {
		if _, dup := out[td.Name.Name]; dup {
			ds = append(ds, diag.New(diag.CodeDuplicate, td.Name.Span, "the table `%s` is declared twice", td.Name.Name))
			continue
		}
		t, more := decodeTable(f, td)
		ds = append(ds, more...)
		if t != nil {
			out[td.Name.Name] = t
		}
	}
	return out, ds
}

func decodeTable(f *ast.File, td *ast.Table) (*Table, []diag.Diagnostic) {
	bad := func(sp diag.Span, format string, args ...any) []diag.Diagnostic {
		return []diag.Diagnostic{diag.New(diag.CodeTableRow, sp, format, args...)}
	}
	ps, cs := ast.FindSetting(td.Settings, "path"), ast.FindSetting(td.Settings, "columns")
	if ps == nil || cs == nil {
		return nil, []diag.Diagnostic{diag.New(diag.CodeMissing, td.Name.Span, "the table `%s` needs `path` and `columns`", td.Name.Name)}
	}
	lit, ok := ps.Value.(*ast.StringLit)
	if !ok {
		return nil, []diag.Diagnostic{diag.New(diag.CodeType, ps.Span, "`path` must be a string")}
	}
	cols, ok := nameListOf(cs.Value)
	if !ok {
		return nil, []diag.Diagnostic{diag.New(diag.CodeType, cs.Span, "`columns` must be a nonempty tuple or list of names")}
	}
	seen := map[string]bool{}
	for _, c := range cols {
		if seen[c] {
			return nil, []diag.Diagnostic{diag.New(diag.CodeDuplicate, cs.Span, "the column `%s` is listed twice", c)}
		}
		seen[c] = true
	}
	t := &Table{Name: td.Name.Name, Path: lit.Value, Columns: cols}
	types := make([]ColType, len(cols))
	have := make([]bool, len(cols))
	// Explicit types first.
	for _, s := range td.Types {
		i := t.Col(s.Name)
		if i < 0 {
			return nil, []diag.Diagnostic{diag.New(diag.CodeTableRow, s.NameSpan, "`types` names `%s`, which is not a column of `%s`", s.Name, t.Name)}
		}
		ct, ok := explicitType(s.Value)
		if !ok {
			return nil, []diag.Diagnostic{diag.New(diag.CodeType, s.Span, "`%s` is not a column type: write string, a parameter type or enum(a, b)", s.Name)}
		}
		types[i], have[i] = ct, true
	}
	// Types from uses, which must agree with each other and with `types`.
	params := map[string]*ast.Param{}
	for _, p := range f.Params {
		params[p.Name.Name] = p
	}
	use := func(col string, ct ColType, sp diag.Span, what string) []diag.Diagnostic {
		i := t.Col(col)
		if i < 0 {
			return []diag.Diagnostic{diag.New(diag.CodeTableRow, sp, "%s reads `row.%s`, which is not a column of `%s`", what, col, t.Name)}
		}
		if have[i] && !sameType(types[i], ct) {
			return []diag.Diagnostic{diag.New(diag.CodeTableRow, sp, "column `%s` of `%s` is %s, but %s needs %s", col, t.Name, types[i], what, ct)}
		}
		types[i], have[i] = ct, true
		return nil
	}
	for _, o := range f.Outputs {
		name, _ := forEachTable(o)
		if name != t.Name {
			continue
		}
		if fs := ast.FindSetting(o.Settings, "file"); fs != nil {
			if fl, ok := fs.Value.(*ast.StringLit); ok {
				for _, c := range templateColumns(fl.Value) {
					if d := use(c, ColType{Kind: "string"}, fs.Span, "the file name"); d != nil {
						return nil, d
					}
				}
			}
		}
		for _, s := range o.Set {
			col, ok := rowRef(s.Value)
			if !ok {
				continue
			}
			p := params[s.Name]
			if p == nil {
				continue // reported as an unknown parameter by the output
			}
			ct, ok := columnTypeOfParam(p)
			if !ok {
				continue
			}
			if d := use(col, ct, s.Span, "`"+s.Name+"`"); d != nil {
				return nil, d
			}
		}
	}
	for i, c := range cols {
		if !have[i] {
			return nil, bad(cs.Span, "the column `%s` of table `%s` is used nowhere and has no type: write types { %s = string }", c, t.Name, c)
		}
	}
	t.Types = types

	b, logical, err := f.Project.Read(lit.Value)
	if err != nil {
		return nil, []diag.Diagnostic{diag.New(diag.CodeMissing, ps.Span, "cannot read the table file: %v", err)}
	}
	t.Path = logical
	sum := sha256.Sum256(b)
	t.SHA256 = hex.EncodeToString(sum[:])
	rows, derr := decodeRows(b, t)
	if derr != nil {
		return nil, bad(ps.Span, "table %s, %v", t.Name, derr)
	}
	t.Rows = rows
	// Row keys.
	ki := t.Col("key")
	keys := map[string]bool{}
	for r := range rows {
		k := strconv.Itoa(r)
		if ki >= 0 && t.Types[ki].Kind == "string" {
			k = rows[r][ki].Str
			if k == "" || k == "." || k == ".." || strings.ContainsAny(k, "/\\") || strings.Contains(k, "..") || strings.HasPrefix(k, ".") {
				return nil, bad(ps.Span, "table %s, row %d, column key: %q is not a safe file name component", t.Name, r, k)
			}
		}
		if keys[k] {
			return nil, bad(ps.Span, "table %s, row %d, column key: %q repeats an earlier row", t.Name, r, k)
		}
		keys[k] = true
		t.Keys = append(t.Keys, k)
	}
	return t, nil
}

// decodeRows reads the top-level array of flat objects from raw tokens.
func decodeRows(b []byte, t *Table) ([][]units.Value, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('[') {
		return nil, fmt.Errorf("the top-level JSON value must be an array of objects")
	}
	var rows [][]units.Value
	for r := 0; dec.More(); r++ {
		tok, err := dec.Token()
		if err != nil || tok != json.Delim('{') {
			return nil, fmt.Errorf("row %d is not an object", r)
		}
		vals := make([]units.Value, len(t.Columns))
		got := make([]bool, len(t.Columns))
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, fmt.Errorf("row %d: %v", r, err)
			}
			key, _ := kt.(string)
			ci := t.Col(key)
			if ci < 0 {
				return nil, fmt.Errorf("row %d, column %s: not a declared column", r, key)
			}
			if got[ci] {
				return nil, fmt.Errorf("row %d, column %s: repeated", r, key)
			}
			vt, err := dec.Token()
			if err != nil {
				return nil, fmt.Errorf("row %d, column %s: %v", r, key, err)
			}
			v, err := decodeCell(vt, t.Types[ci])
			if err != nil {
				return nil, fmt.Errorf("row %d, column %s: %v", r, key, err)
			}
			vals[ci], got[ci] = v, true
		}
		if _, err := dec.Token(); err != nil {
			return nil, fmt.Errorf("row %d: %v", r, err)
		}
		for ci, ok := range got {
			if !ok {
				return nil, fmt.Errorf("row %d, column %s: missing", r, t.Columns[ci])
			}
		}
		rows = append(rows, vals)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("the file has data after the array")
	}
	return rows, nil
}

// decodeCell converts one JSON scalar to a value of the column type.
func decodeCell(tok json.Token, ct ColType) (units.Value, error) {
	switch tok.(type) {
	case nil:
		return units.Bad(), fmt.Errorf("null is not a value")
	case json.Delim:
		return units.Bad(), fmt.Errorf("an array or object is not a value")
	}
	str, isStr := tok.(string)
	num, isNum := tok.(json.Number)
	b, isBool := tok.(bool)
	switch ct.Kind {
	case "string":
		if !isStr {
			return units.Bad(), fmt.Errorf("expected a string")
		}
		return units.Str(str), nil
	case "color":
		if !isStr {
			return units.Bad(), fmt.Errorf("expected a colour string such as \"#165348\"")
		}
		c, err := units.ParseColor(str)
		if err != nil {
			return units.Bad(), fmt.Errorf("%q is not a colour: %v", str, err)
		}
		return units.Col(c), nil
	case "enum":
		if !isStr {
			return units.Bad(), fmt.Errorf("expected one of %s", strings.Join(ct.Enum, ", "))
		}
		for _, m := range ct.Enum {
			if m == str {
				return units.Label(str), nil
			}
		}
		return units.Bad(), fmt.Errorf("%q is not a member of %s", str, ct)
	case "switch":
		if !isBool {
			return units.Bad(), fmt.Errorf("expected true or false")
		}
		return units.Switch(b), nil
	case "number", "integer", "ratio":
		if isStr && ct.Kind == "ratio" && strings.HasSuffix(str, "%") {
			f, err := strconv.ParseFloat(strings.TrimSuffix(str, "%"), 64)
			if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
				return units.Bad(), fmt.Errorf("%q is not a percentage", str)
			}
			return units.Num(f / 100), nil
		}
		if !isNum {
			return units.Bad(), fmt.Errorf("expected a JSON number")
		}
		f, err := strconv.ParseFloat(string(num), 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return units.Bad(), fmt.Errorf("%s is not a finite number", num)
		}
		if ct.Kind == "integer" && !units.IsIntegral(f) {
			return units.Bad(), fmt.Errorf("%s is not a whole number", num)
		}
		return units.Num(f), nil
	case "length", "angle", "duration":
		if !isStr {
			return units.Bad(), fmt.Errorf("expected a string with a unit, such as \"0.3m\"")
		}
		neg := strings.HasPrefix(str, "-")
		v, err := units.ParseNumber(strings.TrimPrefix(strings.TrimPrefix(str, "-"), "+"))
		dim := map[string]units.Dim{"length": units.Length, "angle": units.Angle, "duration": units.Duration}[ct.Kind]
		if err != nil || v.Pending || v.Dim != dim {
			return units.Bad(), fmt.Errorf("%q is not a %s with a unit", str, ct.Kind)
		}
		if neg {
			v.Num = -v.Num
		}
		return v, nil
	}
	return units.Bad(), fmt.Errorf("unknown column type %s", ct.Kind)
}

// ---- output rows ------------------------------------------------------------

// OutputRef is one concrete output of a file: a declared output, or one row
// of a table-driven output.
type OutputRef struct {
	// Name is the output's name: the declared name, or `name[key]` for a row.
	Name string
	// Base is the declared output.
	Base *ast.Output
	// Table and Row are set for a table-driven output.
	Table *Table
	Row   int
}

// Outputs lists the concrete outputs of a file in source order, a
// table-driven output once per row in row order. A table that cannot be read
// gives diagnostics and no rows.
func Outputs(f *ast.File) ([]OutputRef, []diag.Diagnostic) {
	tables, ds := loadTables(f)
	var out []OutputRef
	for _, o := range f.Outputs {
		tn, sp := forEachTable(o)
		if tn == "" {
			if ast.FindSetting(o.Settings, "for_each") != nil {
				ds = append(ds, diag.New(diag.CodeType, sp, "`for_each` names a table"))
			}
			out = append(out, OutputRef{Name: o.Name.Name, Base: o})
			continue
		}
		t := tables[tn]
		if t == nil {
			if !hasTable(f, tn) {
				ds = append(ds, diag.New(diag.CodeUnknownName, sp, "`for_each` names `%s`, which is not a table", tn))
			}
			continue
		}
		for i := range t.Rows {
			out = append(out, OutputRef{Name: o.Name.Name + "[" + t.Keys[i] + "]", Base: o, Table: t, Row: i})
		}
	}
	return out, diag.Dedup(ds)
}

func hasTable(f *ast.File, name string) bool {
	for _, t := range f.Tables {
		if t.Name.Name == name {
			return true
		}
	}
	return false
}

// lookupOutput finds a concrete output by name.
func lookupOutput(f *ast.File, name string) (*OutputRef, []diag.Diagnostic) {
	// A declared output without rows needs no table.
	base, key := name, ""
	if i := strings.Index(name, "["); i > 0 && strings.HasSuffix(name, "]") {
		base, key = name[:i], name[i+1:len(name)-1]
	}
	for _, o := range f.Outputs {
		if o.Name.Name != base {
			continue
		}
		tn, sp := forEachTable(o)
		if tn == "" {
			if key != "" {
				return nil, nil
			}
			return &OutputRef{Name: name, Base: o}, nil
		}
		tables, ds := loadTables(f)
		t := tables[tn]
		if t == nil {
			if !hasTable(f, tn) {
				ds = append(ds, diag.New(diag.CodeUnknownName, sp, "`for_each` names `%s`, which is not a table", tn))
			}
			return nil, ds
		}
		for i, k := range t.Keys {
			if k == key {
				return &OutputRef{Name: name, Base: o, Table: t, Row: i}, ds
			}
		}
		return nil, ds
	}
	return nil, nil
}

// rowOverrides is the overrides of an output's `set` block for one row: an
// assignment of `row.column` becomes the column's decoded value.
func (ref *OutputRef) overrides() ([]Override, []diag.Diagnostic) {
	var out []Override
	var ds []diag.Diagnostic
	for _, s := range ref.Base.Set {
		if col, ok := rowRef(s.Value); ok {
			if ref.Table == nil {
				ds = append(ds, diag.New(diag.CodeUnknownName, s.Value.Span(), "`row` is only known in an output with `for_each`"))
				continue
			}
			ci := ref.Table.Col(col)
			if ci < 0 {
				ds = append(ds, diag.New(diag.CodeTableRow, s.Value.Span(), "`row.%s`: `%s` is not a column of `%s`", col, col, ref.Table.Name))
				continue
			}
			v := ref.Table.Rows[ref.Row][ci]
			out = append(out, Override{Name: s.Name, NameSpan: s.NameSpan, Value: &v, Span: s.Span})
			continue
		}
		out = append(out, Override{Name: s.Name, NameSpan: s.NameSpan, Expr: s.Value, Span: s.Span})
	}
	return out, ds
}

// fileName substitutes `{row.column}` in an output's file name.
func (ref *OutputRef) fileName(file string) (string, []diag.Diagnostic) {
	if ref.Table == nil {
		return file, nil
	}
	cols := templateColumns(file)
	sort.Strings(cols)
	for _, c := range cols {
		ci := ref.Table.Col(c)
		if ci < 0 {
			return file, []diag.Diagnostic{diag.New(diag.CodeTableRow, ref.Base.Name.Span, "the file name reads `{row.%s}`, which is not a column of `%s`", c, ref.Table.Name)}
		}
		file = strings.ReplaceAll(file, "{row."+c+"}", ref.Table.Rows[ref.Row][ci].Str)
	}
	return file, nil
}
