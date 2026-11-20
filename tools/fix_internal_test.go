package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/greenware/diag"
)

// A fix whose result has more errors than the source is refused and nothing
// is written, even with --write.
func TestFixRefusesWhenErrorsRise(t *testing.T) {
	src, err := os.ReadFile("../testdata/diag/GW-UNKNOWN-NAME.gw")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "a.gw")
	if err := os.WriteFile(file, src, 0o644); err != nil {
		t.Fatal(err)
	}
	orig := loadForFix
	defer func() { loadForFix = orig }()
	calls := 0
	loadForFix = func(path, project string, b []byte) *Unit {
		u := orig(path, project, b)
		calls++
		if calls > 1 {
			// The checked result: pretend the edit broke two more things.
			u.Diags = append(u.Diags, diag.New(diag.CodeType, diag.Span{}, "invented"), diag.New(diag.CodeType, diag.Span{Start: 1, End: 2}, "invented too"))
		}
		return u
	}
	var out, errb bytes.Buffer
	code := Fix([]string{file, "--write", "--json"}, &out, &errb)
	if code != ExitFindings {
		t.Fatalf("exit %d, want 1\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"refused": true`) || !strings.Contains(out.String(), `"written": false`) {
		t.Errorf("report does not say refused and unwritten:\n%s", out.String())
	}
	got, _ := os.ReadFile(file)
	if !bytes.Equal(got, src) {
		t.Errorf("a refused fix changed the file")
	}
}

func TestUnifiedDiff(t *testing.T) {
	a := []byte("one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n")
	b := []byte("one\ntwo\nTHREE\nfour\nfive\nsix\nseven\neight\nnine\nten\nextra\n")
	want := "--- a/x\n+++ b/x\n@@ -1,6 +1,6 @@\n one\n two\n-three\n+THREE\n four\n five\n six\n@@ -8,3 +8,4 @@\n eight\n nine\n ten\n+extra\n"
	if got := unifiedDiff("a/x", "b/x", a, b); got != want {
		t.Errorf("diff:\n%s\nwant:\n%s", got, want)
	}
	if got := unifiedDiff("a", "b", a, a); got != "" {
		t.Errorf("equal inputs gave %q", got)
	}
}

func TestNorm(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a  =  b // note\n", "a=b"},
		{"(0, height / 2,\n 0)", "(0,height/2,0)"},
		{`"a  b" // x`, `"a  b"`},
		{"on  off", "on off"},
	} {
		if got := norm(c.in); got != c.want {
			t.Errorf("norm(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
