package det

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/vectors.txt from the current implementation")

func TestSinCosAccuracy(t *testing.T) {
	worstS, worstC := 0.0, 0.0
	for i := -1000000; i <= 1000000; i++ {
		x := float64(i) * 0.001
		if d := math.Abs(Sin(x) - math.Sin(x)); d > worstS {
			worstS = d
		}
		if d := math.Abs(Cos(x) - math.Cos(x)); d > worstC {
			worstC = d
		}
	}
	t.Logf("worst abs error on [-1000,1000]: sin %.3g cos %.3g", worstS, worstC)
	if worstS > 1e-15 || worstC > 1e-15 {
		t.Fatalf("sin %.3g cos %.3g exceed 1e-15", worstS, worstC)
	}
}

func TestOtherAccuracy(t *testing.T) {
	for i := -2000; i <= 2000; i++ {
		x := float64(i) * 0.0123
		if math.Abs(math.Cos(x)) > 1e-3 {
			if d := math.Abs(Tan(x)-math.Tan(x)) / math.Max(1, math.Abs(math.Tan(x))); d > 1e-14 {
				t.Fatalf("Tan(%v) rel err %g", x, d)
			}
		}
		if d := math.Abs(Atan(x) - math.Atan(x)); d > 2e-15 {
			t.Fatalf("Atan(%v) err %g", x, d)
		}
		y := float64(i%97) * 0.37
		if d := math.Abs(Atan2(y, x) - math.Atan2(y, x)); d > 4e-15 {
			t.Fatalf("Atan2(%v,%v) err %g", y, x, d)
		}
		if d := math.Abs(Hypot(x, y)-math.Hypot(x, y)) / math.Max(1, math.Hypot(x, y)); d > 1e-15 {
			t.Fatalf("Hypot(%v,%v) err %g", x, y, d)
		}
	}
	for i := -1000; i <= 1000; i++ {
		x := float64(i) / 1000
		if d := math.Abs(Asin(x) - math.Asin(x)); d > 4e-15 {
			t.Fatalf("Asin(%v) err %g", x, d)
		}
		if d := math.Abs(Acos(x) - math.Acos(x)); d > 4e-15 {
			t.Fatalf("Acos(%v) err %g", x, d)
		}
	}
}

func TestSpecialCases(t *testing.T) {
	if !math.IsNaN(Sin(math.Inf(1))) || !math.IsNaN(Cos(math.NaN())) || !math.IsNaN(Asin(1.5)) {
		t.Fatal("NaN cases")
	}
	if math.Signbit(Sin(math.Copysign(0, -1))) == false {
		t.Fatal("Sin(-0) should keep its sign")
	}
	if Cos(0) != 1 || Sin(0) != 0 {
		t.Fatal("exact values at zero")
	}
	for _, c := range [][2]float64{{0, 1}, {0, -1}, {1, 0}, {-1, 0}, {1, 1}, {-1, -1}, {1, -1}, {-1, 1}, {math.Inf(1), math.Inf(-1)}} {
		got, want := Atan2(c[0], c[1]), math.Atan2(c[0], c[1])
		if math.Abs(got-want) > 4e-16 {
			t.Errorf("Atan2(%v,%v)=%v want %v", c[0], c[1], got, want)
		}
	}
	if Pow(2, 10) != 1024 || Pow(2, -2) != 0.25 || Pow(3, 0) != 1 || Pow(-2, 3) != -8 {
		t.Fatal("Pow")
	}
	if MulAdd(2, 3, 4) != 10 || Lerp(1, 3, 0) != 1 || Lerp(1, 3, 1) != 3 || Lerp(1, 3, 0.5) != 2 {
		t.Fatal("MulAdd or Lerp")
	}
	if Round32(0.1) != float32(0.1) || Sqrt(4) != 2 {
		t.Fatal("Round32 or Sqrt")
	}
}

// TestNoUnwrappedMultiply fails when a multiply is an operand of an add or a
// subtract without a float64(...) conversion in between.
func TestNoUnwrappedMultiply(t *testing.T) {
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
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
			return ok && b.Op == token.MUL
		}
		ast.Inspect(f, func(n ast.Node) bool {
			b, ok := n.(*ast.BinaryExpr)
			if ok && (b.Op == token.ADD || b.Op == token.SUB) && (isMul(b.X) || isMul(b.Y)) {
				t.Errorf("%s: multiply feeds add or subtract without float64(...)", fset.Position(b.Pos()))
			}
			return true
		})
	}
}

type vec struct {
	fn   string
	args []float64
}

func (v vec) eval() float64 {
	switch v.fn {
	case "sin":
		return Sin(v.args[0])
	case "cos":
		return Cos(v.args[0])
	case "tan":
		return Tan(v.args[0])
	case "atan2":
		return Atan2(v.args[0], v.args[1])
	case "acos":
		return Acos(v.args[0])
	case "asin":
		return Asin(v.args[0])
	case "hypot":
		return Hypot(v.args[0], v.args[1])
	case "pow":
		return Pow(v.args[0], int(v.args[1]))
	}
	panic("unknown function " + v.fn)
}

func vectorInputs() []vec {
	var vs []vec
	xs := []float64{0.1, 0.5, 1, 2, 3.14159, 10, -7.25, 100.5, 999.9, -0.0001, 31.4}
	for _, fn := range []string{"sin", "cos"} {
		for _, x := range xs {
			vs = append(vs, vec{fn, []float64{x}})
		}
	}
	for _, x := range []float64{0.1, 0.7, 1.2, -0.4, 2.5} {
		vs = append(vs, vec{"tan", []float64{x}})
	}
	for _, p := range [][2]float64{{1, 1}, {-1, 1}, {1, -1}, {-1, -1}, {0.25, 3}, {5, -0.5}, {0, -1}} {
		vs = append(vs, vec{"atan2", []float64{p[0], p[1]}})
	}
	for _, x := range []float64{-0.9, -0.3, 0, 0.5, 0.99} {
		vs = append(vs, vec{"acos", []float64{x}}, vec{"asin", []float64{x}})
	}
	vs = append(vs, vec{"hypot", []float64{3, 4}}, vec{"hypot", []float64{1e200, 1e200}}, vec{"hypot", []float64{0.1, 0.2}})
	vs = append(vs, vec{"pow", []float64{1.1, 7}}, vec{"pow", []float64{3, -4}}, vec{"pow", []float64{0.9, 20}})
	return vs
}

func hex(f float64) string { return fmt.Sprintf("%016x", math.Float64bits(f)) }

func TestVectors(t *testing.T) {
	path := filepath.Join("testdata", "vectors.txt")
	if *update {
		var sb strings.Builder
		sb.WriteString("# fn arg-bits... result-bits (IEEE-754 binary64, hex). Regenerate: go test -update\n")
		for _, v := range vectorInputs() {
			sb.WriteString(v.fn)
			for _, a := range v.args {
				sb.WriteString(" " + hex(a))
			}
			sb.WriteString(" " + hex(v.eval()) + "\n")
		}
		if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		fields := strings.Fields(line)
		v := vec{fn: fields[0]}
		for _, h := range fields[1 : len(fields)-1] {
			b, err := strconv.ParseUint(h, 16, 64)
			if err != nil {
				t.Fatalf("%q: %v", line, err)
			}
			v.args = append(v.args, math.Float64frombits(b))
		}
		want, err := strconv.ParseUint(fields[len(fields)-1], 16, 64)
		if err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		if got := math.Float64bits(v.eval()); got != want {
			t.Errorf("%s: got %016x want %016x", line, got, want)
		}
		n++
	}
	if n < 50 {
		t.Fatalf("only %d vectors", n)
	}
	t.Logf("%d vectors checked", n)
}
