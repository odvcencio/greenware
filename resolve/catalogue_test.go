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
	f, ds := ast.Lower(lang.Parse([]byte(src)))
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
			if e.Code == diag.CodeAccept || e.Code == diag.CodeLimit {
				return
			}
			src, err := os.ReadFile(filepath.Join("..", e.Fixture))
			if err != nil {
				t.Fatalf("fixture: %v", err)
			}
			ds := diagnose(string(src))
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
