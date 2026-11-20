package compiler

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
)

const tableHostSrc = `greenware 1
asset a { root = a }
param shirt : color = #123456
param grain : ratio = 0.5 in 0..1
param crest : enum(none, star) = none
param trim : switch = off
table schools = json {
  path = "teams.json"
  columns = (key, main, grain, crest, trim)
}
material cloth = pbr { base = shirt roughness = grain }
part a { sphere ball { radius = 0.1m material = cloth } when crest == star { sphere star { radius = 0.02m at = (0, 0.1, 0) material = cloth } } }
output team {
  for_each = schools
  file = "team-{row.key}.glb"
  set { shirt = row.main grain = row.grain crest = row.crest trim = row.trim }
}
output plain { file = "plain.glb" }
`

const tableRows = `[
  {"key": "oak", "main": "#165348", "grain": 0.25, "crest": "star", "trim": true},
  {"key": "elm", "main": "#f1f2ed", "grain": "50%", "crest": "none", "trim": false}
]`

func tableProject(t testing.TB, host, rows string) (*ast.File, []diag.Diagnostic) {
	t.Helper()
	fsys := fstest.MapFS{"teams.json": {Data: []byte(rows)}}
	return ast.LowerWith(lang.Parse([]byte(host)), ast.Options{Project: &ast.Project{FS: fsys}})
}

// A table drives one output per row: file names come from the row, assignments
// from the row's typed values.
func TestTableDrivenOutputs(t *testing.T) {
	f, ds := tableProject(t, tableHostSrc, tableRows)
	if diag.HasErrors(ds) {
		t.Fatal(ds)
	}
	p, _, ds := BuildWith(f, "", Options{})
	if p == nil {
		t.Fatalf("%v", ds)
	}
	var names, files []string
	for _, o := range p.Outputs {
		names = append(names, o.Name)
		for _, fl := range o.Files {
			files = append(files, fl.Name)
		}
	}
	if got := strings.Join(names, " "); got != "team[oak] team[elm] plain" {
		t.Errorf("outputs %q", got)
	}
	if got := strings.Join(files, " "); got != "team-oak.glb team-elm.glb plain.glb" {
		t.Errorf("files %q", got)
	}
	oak, elm := p.Outputs[0].Files[0], p.Outputs[1].Files[0]
	if oak.SHA256 == elm.SHA256 {
		t.Error("two rows gave the same file")
	}
	// The row's colour is the material's base colour, and its crest decides
	// the geometry: the star is a second sphere.
	if got := oak.Doc.Materials[0].BaseColor; got[1] < got[0] && got[2] < got[1] && oak.Triangles <= elm.Triangles {
		t.Errorf("oak %v tris %d, elm tris %d", got, oak.Triangles, elm.Triangles)
	}
	if oak.Triangles == elm.Triangles {
		t.Errorf("the crest column did not change the geometry: %d triangles each", oak.Triangles)
	}
	if elm.Doc.Materials[0].Roughness != 0.5 || oak.Doc.Materials[0].Roughness != 0.25 {
		t.Errorf("roughness %v %v: a ratio is a number or a percent string", oak.Doc.Materials[0].Roughness, elm.Doc.Materials[0].Roughness)
	}
	// One output by name, or one row by its expanded name.
	one, _, ds := BuildWith(f, "team[elm]", Options{})
	if one == nil || len(one.Outputs) != 1 || one.Outputs[0].Files[0].SHA256 != elm.SHA256 {
		t.Errorf("team[elm] alone: %v", ds)
	}
	all, _, _ := BuildWith(f, "team", Options{})
	if all == nil || len(all.Outputs) != 2 {
		t.Errorf("the output name builds every row: %v", all)
	}
	// The table's bytes are pinned in the input record.
	f2, _ := tableProject(t, tableHostSrc, strings.Replace(tableRows, "#f1f2ed", "#f1f2ee", 1))
	q, _, _ := BuildWith(f2, "", Options{})
	if q.InputSHA256 == p.InputSHA256 {
		t.Error("the table's bytes are not part of the input record")
	}
	// The receipt names the row.
	found := false
	for _, c := range p.Receipt.Charges {
		if c.Output == "team[oak]" && c.Ticket == TicketPlan {
			found = true
		}
	}
	if !found {
		t.Error("no plan ticket for team[oak]")
	}
	// The record names each row's output.
	var rec struct{ Outputs []struct{ Name string } }
	if err := json.Unmarshal(p.Record(), &rec); err != nil || len(rec.Outputs) != 3 || rec.Outputs[0].Name != "team[oak]" {
		t.Errorf("record %v %v", rec, err)
	}
}

// Rows are checked against their schema; the diagnostic names the table, the
// zero-based row and the column.
func TestTableRowErrors(t *testing.T) {
	good := `{"key": "oak", "main": "#165348", "grain": 0.25, "crest": "star", "trim": true}`
	cases := []struct {
		name, rows string
		code, frag string
	}{
		{"colour", `[` + good + `, {"key": "elm", "main": "green", "grain": 0.5, "crest": "none", "trim": false}]`, diag.CodeTableRow, "row 1, column main"},
		{"missing column", `[{"key": "oak", "main": "#165348", "grain": 0.25, "crest": "star"}]`, diag.CodeTableRow, "row 0, column trim: missing"},
		{"undeclared column", `[{"key": "oak", "main": "#165348", "grain": 0.25, "crest": "star", "trim": true, "extra": 1}]`, diag.CodeTableRow, "column extra: not a declared column"},
		{"repeated key in a row", `[{"key": "oak", "key": "elm", "main": "#165348", "grain": 0.25, "crest": "star", "trim": true}]`, diag.CodeTableRow, "column key: repeated"},
		{"null", `[{"key": "oak", "main": null, "grain": 0.25, "crest": "star", "trim": true}]`, diag.CodeTableRow, "row 0, column main: null"},
		{"nested", `[{"key": "oak", "main": "#165348", "grain": [1], "crest": "star", "trim": true}]`, diag.CodeTableRow, "row 0, column grain: an array or object"},
		{"enum member", `[{"key": "oak", "main": "#165348", "grain": 0.25, "crest": "Star", "trim": true}]`, diag.CodeTableRow, "row 0, column crest"},
		{"switch as string", `[{"key": "oak", "main": "#165348", "grain": 0.25, "crest": "star", "trim": "yes"}]`, diag.CodeTableRow, "column trim: expected true or false"},
		{"number as string", `[{"key": "oak", "main": "#165348", "grain": "0.25", "crest": "star", "trim": true}]`, diag.CodeTableRow, "column grain: expected a JSON number"},
		{"unsafe key", `[{"key": "../oak", "main": "#165348", "grain": 0.25, "crest": "star", "trim": true}]`, diag.CodeTableRow, "not a safe file name component"},
		{"repeated key row", `[` + good + `,` + good + `]`, diag.CodeTableRow, "repeats an earlier row"},
		{"not an array", `{"key": "oak"}`, diag.CodeTableRow, "must be an array of objects"},
		{"trailing data", `[] []`, diag.CodeTableRow, "data after the array"},
		{"out of range", `[{"key": "oak", "main": "#165348", "grain": 4, "crest": "star", "trim": true}]`, diag.CodeParamRange, "outside the range"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, ds := tableProject(t, tableHostSrc, c.rows)
			_, _, more := BuildWith(f, "", Options{})
			for _, d := range append(ds, more...) {
				if d.Code == c.code && strings.Contains(d.Message, c.frag) {
					if c.code == diag.CodeTableRow && !strings.Contains(d.Message, "schools") {
						t.Errorf("the diagnostic does not name the table: %s", d)
					}
					return
				}
			}
			t.Errorf("want %s mentioning %q, got %v %v", c.code, c.frag, ds, more)
		})
	}
}

// Column types come from their uses, or from `types`; an unused column with no
// type is an error, and a use that disagrees with `types` is too.
func TestTableColumnTypes(t *testing.T) {
	host := `greenware 1
asset a { root = a }
param shirt : color = #123456
table schools = json {
  path = "teams.json"
  columns = [key, main, note]
  types { note = string }
}
part a { sphere ball { radius = 0.1m } }
output team { for_each = schools file = "t-{row.key}.glb" set { shirt = row.main } }
`
	rows := `[{"key": "oak", "main": "#165348", "note": "a free string"}]`
	f, ds := tableProject(t, host, rows)
	if p, _, more := BuildWith(f, "", Options{}); p == nil {
		t.Fatalf("explicit types: %v %v", ds, more)
	}
	bad := strings.Replace(host, "  types { note = string }\n", "", 1)
	f, _ = tableProject(t, bad, rows)
	_, _, ds = BuildWith(f, "", Options{})
	if len(ds) == 0 || !strings.Contains(ds[0].Message, "used nowhere and has no type") {
		t.Errorf("an unused column: %v", ds)
	}
	clash := strings.Replace(host, "types { note = string }", "types { main = string }", 1)
	f, _ = tableProject(t, clash, rows)
	_, _, ds = BuildWith(f, "", Options{})
	found := false
	for _, d := range ds {
		if strings.Contains(d.Message, "needs color") {
			found = true
		}
	}
	if !found {
		t.Errorf("types against a use: %v", ds)
	}
	// An empty table still types its columns from their uses, and builds no
	// row output.
	f, _ = tableProject(t, host, `[]`)
	_, _, ds = BuildWith(f, "", Options{})
	if len(ds) == 0 || ds[0].Code != diag.CodeMissing {
		t.Errorf("an empty table gives no output: %v", ds)
	}
	// A table with no project is reported, not guessed.
	f2, ds2 := ast.LowerWith(lang.Parse([]byte(host)), ast.Options{})
	_, _, ds = BuildWith(f2, "", Options{})
	if len(append(ds2, ds...)) == 0 {
		t.Error("a table without a project reads nothing and says so")
	}
}
