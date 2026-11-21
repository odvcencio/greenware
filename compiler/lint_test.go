package compiler

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

// TestRigFilesWrapMultiplies fails when a float multiply is a direct operand of
// an add or a subtract in the rig files of this package (arm64 would fuse the
// pair into one rounding step and change the bits). The older files of the
// package build meshes with kernel calls and are not covered here.
func TestRigFilesWrapMultiplies(t *testing.T) {
	fset := token.NewFileSet()
	var files []*ast.File
	var all []*ast.File
	names, _ := filepath.Glob("*.go")
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, f)
		if strings.Contains(name, "rig") || strings.Contains(name, "solve") {
			files = append(files, f)
		}
	}
	if len(files) < 4 {
		t.Fatalf("found only %d rig files", len(files))
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil), Error: func(error) {}}
	conf.Check("compiler", fset, all, info)
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
