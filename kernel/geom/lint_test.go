package geom

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoUnwrappedMultiply fails when a multiply is an operand of an add or a
// subtract without a float64(...) conversion in between. On arm64 the
// compiler would fuse such a pair into one rounding step and change the bits.
func TestNoUnwrappedMultiply(t *testing.T) {
	noFusion(t, ".")
}

// noFusion type-checks the non-test files of dir and reports every float
// multiply that is a direct operand of +, -, += or -=. Integer arithmetic is
// ignored.
func noFusion(t *testing.T, dir string) {
	fset := token.NewFileSet()
	names, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	conf := types.Config{
		Importer: importer.ForCompiler(fset, "source", nil),
		Error:    func(error) {}, // a type we cannot resolve is treated as float below
	}
	conf.Check("p", fset, files, info)
	isFloat := func(e ast.Expr) bool {
		tv, ok := info.Types[e]
		if !ok || tv.Type == nil {
			return true
		}
		b, ok := tv.Type.Underlying().(*types.Basic)
		if !ok {
			return true
		}
		return b.Info()&(types.IsFloat|types.IsUntyped) != 0 && b.Info()&types.IsInteger == 0
	}
	isMul := func(e ast.Expr) bool {
		for {
			p, ok := e.(*ast.ParenExpr)
			if !ok {
				break
			}
			e = p.X
		}
		b, ok := e.(*ast.BinaryExpr)
		return ok && b.Op == token.MUL && isFloat(b)
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch b := n.(type) {
			case *ast.BinaryExpr:
				if (b.Op == token.ADD || b.Op == token.SUB) && (isMul(b.X) || isMul(b.Y)) {
					t.Errorf("%s: multiply feeds add or subtract without float64(...)", fset.Position(b.Pos()))
				}
			case *ast.AssignStmt:
				if b.Tok == token.ADD_ASSIGN || b.Tok == token.SUB_ASSIGN {
					for _, r := range b.Rhs {
						if isMul(r) {
							t.Errorf("%s: multiply feeds += or -= without float64(...)", fset.Position(b.Pos()))
						}
					}
				}
			}
			return true
		})
	}
}
