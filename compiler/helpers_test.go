package compiler

import (
	"io/fs"
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

// lowerIn lowers a source that reads a project: fsys is the project root and
// dir the directory of the source inside it.
func lowerIn(t testing.TB, src string, fsys fs.FS, dir string) *ast.File {
	t.Helper()
	f, ds := ast.LowerWith(lang.Parse([]byte(src)), ast.Options{Project: &ast.Project{FS: fsys, Dir: dir}})
	if diag.HasErrors(ds) {
		t.Fatalf("lower: %v", ds)
	}
	return f
}

// loadProject lowers a source file on disk with its project (the nearest
// greenware.mod).
func loadProject(t testing.TB, path string) *ast.File {
	t.Helper()
	src, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	return lowerProjectSource(t, path, src)
}

func lowerProjectSource(t testing.TB, path string, src []byte) *ast.File {
	t.Helper()
	proj, err := ast.OpenProject(filepath.FromSlash(path), "")
	if err != nil {
		t.Fatal(err)
	}
	f, ds := ast.LowerWith(lang.Parse(src), ast.Options{Project: proj})
	if diag.HasErrors(ds) {
		t.Fatalf("lower: %v", ds)
	}
	return f
}
