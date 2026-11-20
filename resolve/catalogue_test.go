package resolve_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/resolve"
)

// diagnose runs the whole front end on a source.
func diagnose(src string) []diag.Diagnostic {
	return diagnoseIn(src, nil)
}

// diagnoseIn is diagnose with a project, for a source that imports a project
// file or reads a table.
func diagnoseIn(src string, proj *ast.Project) []diag.Diagnostic {
	f, ds := ast.LowerWith(lang.Parse([]byte(src)), ast.Options{Project: proj})
	_, more := resolve.EvaluateAll(f)
	return diag.Dedup(append(ds, more...))
}

// Every code in the catalogue has a documentation line and a fixture that
// makes the compiler report that code and no other.
func TestCatalogueFixtures(t *testing.T) {
	for _, e := range diag.Catalogue() {
		t.Run(e.Code, func(t *testing.T) {
			if strings.TrimSpace(e.Doc) == "" {
				t.Fatal("no doc line")
			}
			// GW-ACCEPT comes from running an acceptance file on built
			// files, not from the front end; accept/fixture_test.go checks it.
			// GW-PRINT comes from the print backend on evaluated meshes;
			// compiler/print_test.go checks its fixture.
			if e.Code == diag.CodeAccept || e.Code == diag.CodeLimit || e.Code == diag.CodePrint {
				return
			}
			// GW-RIG-LIMIT and GW-RIG-BAND measure poses, which only the
			// compiler does; compiler/rig_test.go checks their fixtures.
			// So do GW-RIG-GROUND, GW-RIG-TORN and GW-RUNTIME-PARTIAL, which the
			// compiler's audit reports.
			if e.Code == diag.CodeRigLimit || e.Code == diag.CodeRigBand || e.Code == diag.CodeRigGround ||
				e.Code == diag.CodeRigTorn || e.Code == diag.CodeRuntimePart {
				if _, err := os.Stat(filepath.Join("..", e.Fixture)); err != nil {
					t.Fatalf("fixture: %v", err)
				}
				return
			}
			src, err := os.ReadFile(filepath.Join("..", e.Fixture))
			if err != nil {
				t.Fatalf("fixture: %v", err)
			}
			// The fixtures of GW-MOUNT and GW-TABLE-ROW read a neighbouring file.
			proj, err := ast.OpenProject(filepath.Join("..", e.Fixture), "")
			if err != nil {
				t.Fatal(err)
			}
			ds := diagnoseIn(string(src), proj)
			found := false
			for _, d := range ds {
				if d.Code == e.Code {
					found = true
					if d.Span.End < d.Span.Start || d.Span.End > len(src) || (d.Span.Len() == 0 && len(src) > 0 && e.Code != diag.CodeEdition) {
						t.Errorf("bad span %+v for %s", d.Span, d)
					}
				} else {
					t.Errorf("fixture for %s also reports %s", e.Code, d)
				}
			}
			if !found {
				t.Errorf("fixture does not report %s; got %v", e.Code, ds)
			}
		})
	}
}

// Emission sites must use the diag constants: a code written as a string
// literal could drift from the catalogue.
func TestNoCodeLiteralsOutsideCatalogue(t *testing.T) {
	re := regexp.MustCompile(`"GW-[A-Z-]+"`)
	for _, dir := range []string{"../ast", "../resolve", "../units", "../registry"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, _ := os.ReadFile(f)
			if m := re.Find(b); m != nil {
				t.Errorf("%s writes the code literal %s: use the constant in package diag", f, m)
			}
		}
	}
}
