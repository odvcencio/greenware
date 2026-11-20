package resolve_test

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

// worldProject is the fixture package example/world@1 in memory, with source
// files beside it.
func worldProject(t *testing.T, files map[string]string) *ast.Project {
	t.Helper()
	m := fstest.MapFS{}
	for _, f := range []string{"example/world.lock", "example/world/schema-v1.json", "example/world/standard.json"} {
		b, err := os.ReadFile(filepath.Join("..", "testdata", "world", filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		m[f] = &fstest.MapFile{Data: b}
	}
	for name, text := range files {
		m[name] = &fstest.MapFile{Data: []byte(text)}
	}
	return &ast.Project{FS: m}
}

func dataAsset(body string) string {
	return "greenware 1\nimport \"example/world@1\" as world\nasset a { root = a }\n" + body + "\npart a { }\n"
}

func codeList(ds []diag.Diagnostic) string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Code)
	}
	return strings.Join(out, " ")
}

func evalWorld(t *testing.T, src string) (*resolve.Resolved, []diag.Diagnostic) {
	t.Helper()
	f, ds := ast.LowerWith(lang.Parse([]byte(src)), ast.Options{Project: worldProject(t, nil)})
	if diag.HasErrors(ds) {
		t.Fatalf("lower: %v", ds)
	}
	return resolve.Evaluate(f, nil)
}

// A field evaluates to the value the literal in the same unit evaluates to,
// bit for bit: lengths in metres, angles in radians, ratios and numbers plain.
func TestDataReferencesEqualTheirLiterals(t *testing.T) {
	r, ds := evalWorld(t, dataAsset(`
let w = world.crate.width
let h = world.crate.height
let g = world.crate.slat_gap
let n = world.crate.slat_count
let a = world.crate.tilt
let same = world.crate.width + 0m`))
	if diag.HasErrors(ds) {
		t.Fatal(ds)
	}
	for name, lit := range map[string]string{"w": "24in", "h": "2ft", "g": "12.5%", "n": "6", "a": "15deg"} {
		got, ok := r.Let(name)
		want, err := units.ParseNumber(lit)
		if err != nil || !ok {
			t.Fatalf("%s: %v", name, err)
		}
		want = units.Finish(want)
		if math.Float64bits(got.Num) != math.Float64bits(want.Num) || got.Dim != want.Dim || got.Pending {
			t.Errorf("%s = %+v, literal %s = %+v", name, got, lit, want)
		}
	}
	if len(r.Data) != 1 || r.Data[0].Contract != "example/world@1" || r.Data[0].Profile != "standard" || r.Data[0].Qualifier != "world" {
		t.Errorf("data %+v", r.Data)
	}
	if in := r.DataInputs(); len(in) != 1 || in[0].SHA256 != "6a972715adc467fefb3e4c892958ee75a1cdb752a407b817fbd2af606e9beab5" {
		t.Errorf("inputs %+v", in)
	}
}

func TestDataImportWithoutAs(t *testing.T) {
	r, ds := evalWorld(t, "greenware 1\nimport \"example/world@1\"\nasset a { root = a }\nlet w = world.crate.depth\npart a { }\n")
	if diag.HasErrors(ds) {
		t.Fatal(ds)
	}
	if v, _ := r.Let("w"); math.Abs(v.Num-0.4572) > 1e-12 {
		t.Errorf("depth %v", v.Num)
	}
}

func TestDataReferenceErrors(t *testing.T) {
	for _, c := range []struct {
		name, body, code, msg string
		wrong                 string // the text the fix replaces
	}{
		{"typo in the field", "let w = world.crate.widht", diag.CodeUnknownName, "`widht` is not a field of `world.crate`", "widht"},
		{"typo in the group", "let w = world.crat.width", diag.CodeUnknownName, "`crat` is not a field of `world`", "crat"},
		{"no near name", "let w = world.crate.zzzzzzzz", diag.CodeUnknownName, "it has depth, height", ""},
		{"a group is not a value", "let w = world.crate", diag.CodeType, "is a group of fields", ""},
		{"past a leaf", "let w = world.crate.width.q", diag.CodeType, "no member `q`", ""},
		{"length in an angle slot", "param p : angle = world.crate.width", diag.CodeUnit, "", ""},
		{"angle plus length", "let w = world.crate.tilt + world.crate.width", diag.CodeUnit, "", ""},
		{"duplicate qualifier", "import \"example/world@1\" as world", diag.CodeDuplicate, "used twice", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := dataAsset(c.body)
			f, ds := ast.LowerWith(lang.Parse([]byte(src)), ast.Options{Project: worldProject(t, nil)})
			_, more := resolve.Evaluate(f, nil)
			all := diag.Dedup(append(ds, more...))
			if len(all) != 1 || all[0].Code != c.code {
				t.Fatalf("got %s: %v", codeList(all), all)
			}
			if !strings.Contains(all[0].Message, c.msg) {
				t.Errorf("message %q lacks %q", all[0].Message, c.msg)
			}
			if c.wrong != "" {
				if len(all[0].Edits) == 0 {
					t.Fatalf("no edit on %v", all[0])
				}
				// The diagnostic covers only the wrong name.
				if sp := all[0].Span; src[sp.Start:sp.End] != c.wrong {
					t.Errorf("span covers %q, want %q", src[sp.Start:sp.End], c.wrong)
				}
			}
		})
	}
}

// A failed import gives one diagnostic, not one for every reference.
func TestFailedDataImportIsReportedOnce(t *testing.T) {
	p := worldProject(t, nil)
	delete(p.FS.(fstest.MapFS), "example/world/standard.json")
	src := dataAsset("let w = world.crate.width\nlet d = world.crate.depth + w")
	f, ds := ast.LowerWith(lang.Parse([]byte(src)), ast.Options{Project: p})
	_, more := resolve.Evaluate(f, nil)
	all := diag.Dedup(append(ds, more...))
	if len(all) != 1 || all[0].Code != diag.CodeImportData || !strings.Contains(all[0].Message, "example/world/standard.json") {
		t.Fatalf("%v", all)
	}
	at := strings.Index(src, `"example/world@1"`)
	if all[0].Span.Start != at {
		t.Errorf("span %v, want the import path at %d", all[0].Span, at)
	}
}

func TestDataImportNeedsAProject(t *testing.T) {
	f, ds := ast.LowerWith(lang.Parse([]byte(dataAsset("let w = world.crate.width"))), ast.Options{})
	_, more := resolve.Evaluate(f, nil)
	all := diag.Dedup(append(ds, more...))
	if len(all) != 1 || all[0].Code != diag.CodeImportData || !strings.Contains(all[0].Message, "needs a project directory") {
		t.Fatalf("%v", all)
	}
}

// A project file that imports a package passes its data to the host.
func TestGearDataReachesTheHost(t *testing.T) {
	gear := dataAsset("let w = world.crate.width") + "output o { file = \"g.glb\" }\n"
	host := "greenware 1\nimport \"gear.gw\" as gear\nasset h { root = h }\npart h { }\noutput o { file = \"h.glb\" }\n"
	p := worldProject(t, map[string]string{"gear.gw": gear})
	f, ds := ast.LowerWith(lang.Parse([]byte(host)), ast.Options{Project: p})
	r, more := resolve.Evaluate(f, nil)
	if diag.HasErrors(append(ds, more...)) {
		t.Fatalf("%v %v", ds, more)
	}
	if in := r.DataInputs(); len(in) != 1 || in[0].Contract != "example/world@1" {
		t.Errorf("inputs %+v", in)
	}
}
