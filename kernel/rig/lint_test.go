package rig

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/greenware/internal/nofusion"
)

func TestNoUnwrappedMultiply(t *testing.T) { nofusion.Check(t) }

// TestSolveIsOrderedAndPure keeps the weight solver out of the places where
// the bits could depend on the machine or the run: no map type (a map's order
// is random), no goroutine, no clock, no random numbers, and no float
// function that is not exact or in package det. The solver reaches vertices,
// joints, edges and segments by index only.
func TestSolveIsOrderedAndPure(t *testing.T) {
	names, _ := filepath.Glob("solve*.go")
	n := 0
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		n++
		solveFileIsOrderedAndPure(t, name)
	}
	if n == 0 {
		t.Fatal("found no solver file")
	}
}

func solveFileIsOrderedAndPure(t *testing.T, name string) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		switch path := strings.Trim(imp.Path.Value, `"`); path {
		case "time", "math/rand", "math/rand/v2", "sync", "runtime", "os", "reflect", "strconv":
			t.Errorf("%s imports %s", name, path)
		}
	}
	// math functions that are exact (they pick or flip a value) or are
	// correctly rounded by the language: nothing that is a series.
	exact := map[string]bool{"Min": true, "Max": true, "Abs": true, "IsInf": true, "IsNaN": true, "Inf": true, "RoundToEven": true, "Trunc": true}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.MapType:
			t.Errorf("%s: a map type in the solver", fset.Position(x.Pos()))
		case *ast.GoStmt:
			t.Errorf("%s: a goroutine in the solver", fset.Position(x.Pos()))
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && id.Name == "math" && !exact[x.Sel.Name] && x.Sel.Name != "Pi" {
				t.Errorf("%s: math.%s in the solver; use package det", fset.Position(x.Pos()), x.Sel.Name)
			}
		}
		return true
	})
}
