package ast_test

import (
	"os"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
)

func lower(t *testing.T, src string) (*ast.File, []diag.Diagnostic) {
	t.Helper()
	return ast.Lower(lang.Parse([]byte(src)))
}

func TestGoalPostLowers(t *testing.T) {
	src, err := os.ReadFile("../testdata/syntax/goalpost.gw")
	if err != nil {
		t.Fatal(err)
	}
	f, ds := ast.Lower(lang.Parse(src))
	for _, d := range ds {
		t.Errorf("%v", d)
	}
	if f.Asset == nil || f.Asset.Name.Name != "goalpost" {
		t.Fatalf("asset = %+v", f.Asset)
	}
	if len(f.Params) != 6 || len(f.Lets) != 5 || len(f.Materials) != 3 || len(f.Parts) != 2 ||
		len(f.Lods) != 2 || len(f.Outputs) != 2 || f.Collision == nil || f.Budget == nil {
		t.Errorf("counts: params %d lets %d materials %d parts %d lods %d outputs %d", len(f.Params), len(f.Lets), len(f.Materials), len(f.Parts), len(f.Lods), len(f.Outputs))
	}
	if f.Materials[0].Fallback == nil {
		t.Error("selena material lost its fallback")
	}
}

func TestAcceptLowers(t *testing.T) {
	src, err := os.ReadFile("../testdata/syntax/goalpost.accept.gw")
	if err != nil {
		t.Fatal(err)
	}
	f, ds := ast.Lower(lang.Parse(src))
	for _, d := range ds {
		t.Errorf("%v", d)
	}
	if len(f.Accepts) != 1 || len(f.Accepts[0].Items) != 11 {
		t.Errorf("accept items = %d", len(f.Accepts[0].Items))
	}
}

func letExpr(t *testing.T, expr string) ast.Expr {
	t.Helper()
	f, ds := lower(t, "greenware 1\n\nasset a { root = a }\nlet x = "+expr+"\npart a { }\n")
	if len(ds) != 0 {
		t.Fatalf("%v", ds)
	}
	return f.Lets[0].Value
}

// shape renders an expression with explicit grouping.
func shape(e ast.Expr) string {
	switch n := e.(type) {
	case *ast.Binary:
		return "(" + shape(n.L) + " " + n.Op + " " + shape(n.R) + ")"
	case *ast.Unary:
		return "(" + n.Op + shape(n.X) + ")"
	case *ast.NumberLit:
		return n.Text
	case *ast.Name:
		return n.Name
	case *ast.Paren:
		return "[" + shape(n.X) + "]"
	case *ast.Call:
		return n.Func + "()"
	}
	return "?"
}

// The parser nests every operator to the right. Lowering must rebuild the
// tree by precedence and associativity.
func TestPrecedence(t *testing.T) {
	cases := map[string]string{
		"a / 2 + b":      "((a / 2) + b)",
		"a + b * c":      "(a + (b * c))",
		"a - b - c":      "((a - b) - c)",
		"a * b / c":      "((a * b) / c)",
		"-a * b":         "((-a) * b)",
		"a + b >= c - d": "((a + b) >= (c - d))",
		"a && b || c":    "((a && b) || c)",
		"a || b && c":    "(a || (b && c))",
		"(a + b) * c":    "([(a + b)] * c)",
		"a + -b * c":     "(a + ((-b) * c))",
		"1 + 2 * 3 - 4":  "((1 + (2 * 3)) - 4)",
		"- - a":          "(-(-a))",
	}
	for src, want := range cases {
		if got := shape(letExpr(t, src)); got != want {
			t.Errorf("%s parsed as %s, want %s", src, got, want)
		}
	}
	// A binary node spans from its left operand to its right.
	b := letExpr(t, "a / 2 + b").(*ast.Binary)
	if b.Span().End-b.Span().Start != len("a / 2 + b") {
		t.Errorf("span %+v", b.Span())
	}
}

func TestStringEscapes(t *testing.T) {
	f, ds := lower(t, "greenware 1\n\nasset a { root = a title = \"A\\u00e9\\n\\\"q\\\" \\/\" }\npart a { }\n")
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	got := f.Asset.Settings[1].Value.(*ast.StringLit).Value
	if got != "Aé\n\"q\" /" {
		t.Errorf("decoded %q", got)
	}
	if _, ds := lower(t, "greenware 1\n\nasset a { root = a title = \"bad \\q\" }\npart a { }\n"); len(ds) != 1 || ds[0].Code != diag.CodeSyntax {
		t.Errorf("unknown escape: %v", ds)
	}
}

func TestSyntaxErrorsDoNotCascade(t *testing.T) {
	f, ds := lower(t, "greenware 1\n\nasset a { root = a\npart a { cylinder { radius = } }\n")
	if !f.Broken || len(ds) == 0 {
		t.Fatal("expected syntax errors")
	}
	for _, d := range ds {
		if d.Code != diag.CodeSyntax {
			t.Errorf("a syntax error cascaded into %v", d)
		}
	}
}

func TestLoweringStructure(t *testing.T) {
	src, _ := os.ReadFile("../testdata/syntax/goalpost.gw")
	f, _ := ast.Lower(lang.Parse(src))
	root := f.Parts[1]
	if root.Name.Name != "goalpost" || !root.TopLevel {
		t.Fatalf("root = %+v", root.Name)
	}
	var mirror *ast.Mirror
	for _, it := range root.Items {
		if m, ok := it.(*ast.Mirror); ok {
			mirror = m
		}
	}
	if mirror == nil {
		t.Fatal("no mirror")
	}
	up := mirror.Items[0].(*ast.Part)
	if up.Name.Name != "upright_l" || up.Pair.Name != "upright_r" {
		t.Errorf("pair = %q, %q", up.Name.Name, up.Pair.Name)
	}
	var sockets int
	for _, it := range up.Items {
		if s, ok := it.(*ast.Socket); ok && s.Pair.Name != "" {
			sockets++
		}
	}
	if sockets != 2 {
		t.Errorf("paired sockets = %d", sockets)
	}
	if f.Params[5].Runtime != true || f.Params[0].Range == nil || f.Params[0].Runtime {
		t.Error("param flags")
	}
	if f.Outputs[1].Budget == nil || len(f.Outputs[1].Set) != 2 {
		t.Error("practice output")
	}
	if when := root.Items[len(root.Items)-1].(*ast.When); when == nil || len(when.Then) != 1 {
		t.Error("when padding")
	}
	// Every node keeps a span inside the source.
	for _, p := range f.Params {
		if p.Span.End > len(f.Source) || p.Span.Len() == 0 || p.Name.Span.Len() == 0 {
			t.Errorf("param span %+v", p.Span)
		}
	}
}

func TestEditionDiagnostics(t *testing.T) {
	if _, ds := lower(t, "asset a { root = a }\npart a { }\n"); len(ds) != 1 || ds[0].Code != diag.CodeEdition || len(ds[0].Edits) != 1 {
		t.Errorf("missing header: %v", ds)
	}
	if _, ds := lower(t, "facet 1\n\nasset a { root = a }\npart a { }\n"); len(ds) == 0 || ds[0].Code != diag.CodeEdition {
		t.Errorf("wrong header word: %v", ds)
	}
	if _, ds := lower(t, "greenware 2\n\nasset a { root = a }\npart a { }\n"); len(ds) != 1 || ds[0].Code != diag.CodeEdition {
		t.Errorf("wrong edition: %v", ds)
	}
}
