package nofusion

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

func TestFlagsFloatsAndLeavesIntegerArithmeticAlone(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "control.go", `package check
type Sample float64
func floatOps(x, y, z Sample) Sample {
	z += (x * y)
	return z - (x * y)
}
func integerOps(x, y, z int) int { return z + x*y }
func rounded(x, y, z float64) float64 { return z + float64(x*y) }
`, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	files := []*ast.File{f}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	var conf types.Config
	if _, err := conf.Check("check", fset, files, info); err != nil {
		t.Fatal(err)
	}
	got := unwrappedMultiplies(files, info)
	if len(got) != 2 || fset.Position(got[0]).Line != 4 || fset.Position(got[1]).Line != 5 {
		t.Fatalf("wrong rounding violations: %v", got)
	}
}
