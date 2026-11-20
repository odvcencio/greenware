package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
)

const goalpostPath = "../testdata/syntax/goalpost.gw"

func lowerSource(t testing.TB, src []byte) *ast.File {
	t.Helper()
	f, ds := ast.Lower(lang.Parse(src))
	if diag.HasErrors(ds) {
		t.Fatalf("lower: %v", ds)
	}
	return f
}

func loadFile(t testing.TB, path string) *ast.File {
	t.Helper()
	src, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	return lowerSource(t, src)
}

func buildGoalPost(t testing.TB) *Product {
	t.Helper()
	p, ds := Build(loadFile(t, goalpostPath), "")
	if p == nil {
		t.Fatalf("build failed: %v", ds)
	}
	return p
}

// goalpostSource returns the sample's text with one replacement applied.
func goalpostSource(t testing.TB, old, new string) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.FromSlash(goalpostPath))
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, old) {
		t.Fatalf("sample does not contain %q", old)
	}
	return []byte(strings.Replace(s, old, new, 1))
}
