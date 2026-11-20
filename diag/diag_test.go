package diag_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/greenware/diag"
)

func TestCatalogueIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range diag.Catalogue() {
		if !strings.HasPrefix(e.Code, "GW-") || seen[e.Code] {
			t.Errorf("bad or repeated code %q", e.Code)
		}
		seen[e.Code] = true
		if strings.TrimSpace(e.Doc) == "" {
			t.Errorf("%s has no doc line", e.Code)
		}
		if e.Fixture != "testdata/diag/"+e.Code+".gw" {
			t.Errorf("%s fixture is %q", e.Code, e.Fixture)
		}
		if _, err := os.Stat(filepath.Join("..", e.Fixture)); err != nil {
			t.Errorf("%s: %v", e.Code, err)
		}
	}
	for _, code := range []string{diag.CodeSyntax, diag.CodeUnknownName, diag.CodeUnknownField, diag.CodeDuplicate, diag.CodeType, diag.CodeUnit,
		diag.CodeParamRange, diag.CodeRequire, diag.CodeUnsupported, diag.CodeEdition, diag.CodeBudget} {
		if !seen[code] {
			t.Errorf("%s missing from the catalogue", code)
		}
	}
}

func TestNewUsesCatalogueSeverity(t *testing.T) {
	d := diag.New(diag.CodeUnit, diag.Span{Start: 1, End: 3}, "bad %d", 7)
	if d.Severity != diag.Error || d.Message != "bad 7" {
		t.Errorf("%+v", d)
	}
	defer func() {
		if recover() == nil {
			t.Error("an unknown code must panic")
		}
	}()
	diag.New("GW-NOPE", diag.Span{}, "x")
}

func TestSortIsTotalAndStable(t *testing.T) {
	mk := func(code string, a, b int, msg string) diag.Diagnostic {
		return diag.Diagnostic{Code: code, Span: diag.Span{Start: a, End: b}, Message: msg}
	}
	in := []diag.Diagnostic{
		mk("GW-UNIT", 5, 9, "b"), mk("GW-TYPE", 5, 9, "a"), mk("GW-UNIT", 2, 4, "z"),
		mk("GW-TYPE", 5, 7, "m"), mk("GW-TYPE", 5, 9, "a"),
	}
	want := []string{"GW-UNIT 2", "GW-TYPE 5-7", "GW-TYPE 5-9", "GW-UNIT 5-9"}
	got := diag.Dedup(append([]diag.Diagnostic(nil), in...))
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	// The order must not depend on the input order.
	rev := make([]diag.Diagnostic, len(in))
	for i := range in {
		rev[len(in)-1-i] = in[i]
	}
	got2 := diag.Dedup(rev)
	for i := range got {
		if got[i].Code != got2[i].Code || got[i].Span != got2[i].Span || got[i].Message != got2[i].Message {
			t.Errorf("order differs at %d: %v vs %v", i, got[i], got2[i])
		}
	}
	if got[0].Span.Start != 2 || got[1].Span.End != 7 || got[2].Code != "GW-TYPE" || got[3].Code != "GW-UNIT" {
		t.Errorf("order: %v", got)
	}
}

func TestMarshalStableKeyOrder(t *testing.T) {
	d := diag.New(diag.CodeUnknownName, diag.Span{Start: 4, End: 10}, "no name `hieght`").
		WithEdit("write height", diag.Span{Start: 4, End: 10}, "height").
		WithRelated(diag.Span{Start: 0, End: 3}, "declared here")
	loc := func(off int) (int, int) { return 1, off + 1 }
	b, err := diag.Marshal([]diag.Diagnostic{d}, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"greenware.diagnostics/1","ok":false,"diagnostics":[{"code":"GW-UNKNOWN-NAME","severity":"error","message":"no name ` + "`hieght`" + `","span":{"start":{"byte":4,"line":1,"column":5},"end":{"byte":10,"line":1,"column":11}},"edits":[{"title":"write height","span":{"start":{"byte":4,"line":1,"column":5},"end":{"byte":10,"line":1,"column":11}},"new_text":"height"}],"related":[{"span":{"start":{"byte":0,"line":1,"column":1},"end":{"byte":3,"line":1,"column":4}},"note":"declared here"}]}]}` + "\n"
	if string(b) != want {
		t.Errorf("got\n%s\nwant\n%s", b, want)
	}
	empty, _ := diag.Marshal(nil, nil)
	if string(empty) != `{"schema":"greenware.diagnostics/1","ok":true,"diagnostics":[]}`+"\n" {
		t.Errorf("empty: %s", empty)
	}
}

func TestApply(t *testing.T) {
	out, ok := diag.Apply([]byte("let x = hieght"), diag.Edit{Span: diag.Span{Start: 8, End: 14}, NewText: "height"})
	if !ok || string(out) != "let x = height" {
		t.Errorf("%q %v", out, ok)
	}
	if _, ok := diag.Apply([]byte("ab"), diag.Edit{Span: diag.Span{Start: 1, End: 9}}); ok {
		t.Error("an edit past the end must be refused")
	}
}
