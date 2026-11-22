// Package nofusion checks the floating-point rounding barriers in build code.
package nofusion

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type listedPackage struct {
	ImportPath string
	Dir        string
	Export     string
	GoFiles    []string
	DepOnly    bool
}

// Check uses the active Go command's selected files and dependency export
// data. The source importer can lose its GOROOT in a trimpath test binary,
// which leaves expressions untyped and can misclassify integer arithmetic.
func Check(t *testing.T) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", "-export", "-json", ".")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("list packages for rounding check: %v\n%s", err, stderr.String())
	}
	exports := make(map[string]string)
	var target listedPackage
	list := json.NewDecoder(bytes.NewReader(data))
	for {
		var pkg listedPackage
		if err := list.Decode(&pkg); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		exports[pkg.ImportPath] = pkg.Export
		if !pkg.DepOnly {
			target = pkg
		}
	}
	if len(target.GoFiles) == 0 {
		t.Fatal("go list returned no source files for the rounding check")
	}
	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(target.GoFiles))
	for _, name := range target.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(target.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	lookup := func(path string) (io.ReadCloser, error) { return os.Open(exports[path]) }
	conf := types.Config{Importer: importer.ForCompiler(fset, "gc", lookup)}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	if _, err := conf.Check(target.ImportPath, fset, files, info); err != nil {
		t.Fatalf("type-check rounding rules: %v", err)
	}
	for _, violation := range unwrappedMultiplies(files, info) {
		t.Errorf("%s: multiply feeds add or subtract without an explicit float conversion", fset.Position(violation))
	}
}

func unwrappedMultiplies(files []*ast.File, info *types.Info) []token.Pos {
	var positions []token.Pos
	check := func(expr ast.Expr) {
		mul, ok := ast.Unparen(expr).(*ast.BinaryExpr)
		if !ok || mul.Op != token.MUL {
			return
		}
		if b, ok := info.TypeOf(mul).Underlying().(*types.Basic); ok && b.Info()&types.IsFloat != 0 {
			positions = append(positions, mul.Pos())
		}
	}
	for _, f := range files {
		ast.Inspect(f, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.BinaryExpr:
				if n.Op == token.ADD || n.Op == token.SUB {
					check(n.X)
					check(n.Y)
				}
			case *ast.AssignStmt:
				if n.Tok == token.ADD_ASSIGN || n.Tok == token.SUB_ASSIGN {
					for _, expr := range n.Rhs {
						check(expr)
					}
				}
			}
			return true
		})
	}
	return positions
}
