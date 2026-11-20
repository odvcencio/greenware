package lang

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gw "m31labs.dev/greenware/language"
)

var sampleNames = []string{"goalpost", "goalpost.accept", "helmet", "athlete"}

func readSample(t testing.TB, name string) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "testdata", "syntax", name+gw.Extension))
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// kinds lists every node kind in source order, anonymous tokens included.
func kinds(t *Tree) []string {
	var out []string
	t.Root().Walk(func(n *Node) bool {
		out = append(out, n.Kind())
		return true
	})
	return out
}

// sexp renders the named nodes with their field names, comments left out.
func sexp(n *Node) string {
	var b strings.Builder
	var write func(n *Node, depth int)
	write = func(n *Node, depth int) {
		b.WriteString(strings.Repeat("  ", depth))
		if n.FieldName() != "" {
			b.WriteString(n.FieldName() + ": ")
		}
		if n.IsError() {
			b.WriteString("ERROR")
		} else {
			b.WriteString(n.Kind())
		}
		kids := n.NamedChildren()
		if len(kids) == 0 {
			if n.IsNamed() {
				fmt.Fprintf(&b, " %q", n.Text())
			}
			b.WriteByte('\n')
			return
		}
		b.WriteByte('\n')
		for _, c := range n.Children() {
			if c.IsNamed() && !c.IsComment() || c.FieldName() != "" {
				write(c, depth+1)
			}
		}
	}
	write(n, 0)
	return b.String()
}

func mustParse(t testing.TB, src string) *Tree {
	t.Helper()
	tree := Parse([]byte(src))
	if tree.HasErrors() {
		t.Fatalf("parse %q: %v\n%s", src, tree.Diagnostics(), sexp(tree.Root()))
	}
	return tree
}

func TestSamplesParseWithoutErrors(t *testing.T) {
	for _, name := range sampleNames {
		t.Run(name, func(t *testing.T) {
			src := readSample(t, name)
			tree := Parse(src)
			if tree.HasErrors() {
				t.Fatalf("diagnostics: %v", tree.Diagnostics())
			}
			root := tree.Root()
			if root.Kind() != "source_file" {
				t.Fatalf("root kind %q", root.Kind())
			}
			if root.Span().End != len(src) && strings.TrimSpace(string(src[root.Span().End:])) != "" {
				t.Fatalf("parse stopped at byte %d of %d", root.Span().End, len(src))
			}
			tree.Root().Walk(func(n *Node) bool {
				if n.IsError() || n.IsMissing() {
					t.Errorf("%s node at %s", n.Kind(), n.Start())
				}
				return true
			})
		})
	}
}

func TestSourceRoundTripsByteForByte(t *testing.T) {
	for _, name := range sampleNames {
		t.Run(name, func(t *testing.T) {
			src := readSample(t, name)
			tree := Parse(src)
			if !bytes.Equal(tree.Source(), src) {
				t.Fatal("Source() differs from the input")
			}
			// Leaves and trivia together cover the source with no gap or overlap.
			var rebuilt []byte
			pos := 0
			leaves, trivia := tree.Leaves(), tree.Trivia()
			li, ti := 0, 0
			for pos < len(src) {
				switch {
				case li < len(leaves) && leaves[li].Span().Start == pos:
					rebuilt = append(rebuilt, leaves[li].Text()...)
					pos = leaves[li].Span().End
					li++
				case ti < len(trivia) && trivia[ti].Span.Start == pos:
					if !trivia[ti].Blank {
						t.Fatalf("skipped text at byte %d is not white space", pos)
					}
					rebuilt = append(rebuilt, src[trivia[ti].Span.Start:trivia[ti].Span.End]...)
					pos = trivia[ti].Span.End
					ti++
				default:
					t.Fatalf("no leaf or trivia starts at byte %d", pos)
				}
			}
			if !bytes.Equal(rebuilt, src) {
				t.Fatal("leaves and trivia do not rebuild the source")
			}
			if len(tree.Comments()) == 0 {
				t.Fatal("no comment nodes found")
			}
		})
	}
}

func TestFormatSamples(t *testing.T) {
	for _, name := range sampleNames {
		t.Run(name, func(t *testing.T) {
			src := readSample(t, name)
			once, err := Format(src)
			if err != nil {
				t.Fatal(err)
			}
			twice, err := Format(once)
			if err != nil {
				t.Fatalf("formatted output does not parse: %v", err)
			}
			if !bytes.Equal(once, twice) {
				t.Fatalf("Format is not idempotent:\n%s", firstDifference(once, twice))
			}
			before, after := Parse(src), Parse(once)
			if got, want := strings.Join(kinds(after), " "), strings.Join(kinds(before), " "); got != want {
				t.Fatal("formatted file parses to different node kinds")
			}
			if got, want := commentTexts(after), commentTexts(before); got != want {
				t.Fatalf("comments changed:\n got %q\nwant %q", got, want)
			}
			if bytes.Contains(once, []byte("\n\n\n")) || bytes.HasSuffix(once, []byte("\n\n")) {
				t.Fatal("output has more than one blank line in a row")
			}
		})
	}
}

func commentTexts(t *Tree) string {
	var parts []string
	for _, c := range t.Comments() {
		parts = append(parts, strings.TrimRight(c.Text(), " \t\r"))
	}
	return strings.Join(parts, "\n")
}

func firstDifference(a, b []byte) string {
	al, bl := strings.Split(string(a), "\n"), strings.Split(string(b), "\n")
	for i := 0; i < len(al) && i < len(bl); i++ {
		if al[i] != bl[i] {
			return fmt.Sprintf("line %d:\n  %q\n  %q", i+1, al[i], bl[i])
		}
	}
	return fmt.Sprintf("lengths differ: %d and %d lines", len(al), len(bl))
}

func TestFormatGolden(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			"spacing and one item per line",
			"asset a{title=\"x\" up=y\nforward = -z}\n",
			"asset a {\n  title = \"x\"\n  up = y\n  forward = -z\n}\n",
		},
		{
			"one-line body stays on one line",
			"material m = pbr {  base=#f2c200   roughness=0.4 }\n",
			"material m = pbr { base = #f2c200 roughness = 0.4 }\n",
		},
		{
			"multi-line body stays multi-line",
			"material m = pbr {\n base=#f2c200 }\n",
			"material m = pbr {\n  base = #f2c200\n}\n",
		},
		{
			"empty bodies",
			"collision {\n}\nskin   {   }\n",
			"collision {}\nskin {}\n",
		},
		{
			"rows, calls, ranges and unary",
			"part p{ point(0,1 , -z) sample a in 0deg .. 90deg by 9->point(0, r*sin(a)) x = f( a ,b=2)[1] . y }\n",
			"part p { point (0, 1, -z) sample a in 0deg..90deg by 9 -> point (0, r * sin(a)) x = f(a, b = 2)[1].y }\n",
		},
		{
			"param with range, runtime and body",
			"param  h:length=3.05m in 2.5m .. 3.5m runtime{doc=\"d\"}\n",
			"param h : length = 3.05m in 2.5m..3.5m runtime { doc = \"d\" }\n",
		},
		{
			"painter and template signatures",
			"painter cloth ( base : color , folds : switch = off ) {\n}\ntemplate rail(y0:length){ }\n",
			"painter cloth(base: color, folds: switch = off) {}\ntemplate rail(y0: length) {}\n",
		},
		{
			"pair names and when/else",
			"part (a,b){ when x>1 { k = 1 } else when y { k = 2 } else { k = 3 } }\n",
			"part (a, b) { when x > 1 { k = 1 } else when y { k = 2 } else { k = 3 } }\n",
		},
		{
			"comments stay in place",
			"// head\n\n\n\nasset a {\n  x = 1   // one\n  yy = 2 // two\n\n\n  // own line\n  z = 3\n  // last\n}\n// tail\n",
			"// head\n\nasset a {\n  x = 1  // one\n  yy = 2  // two\n\n  // own line\n  z = 3\n  // last\n}\n// tail\n",
		},
		{
			"comment inside an argument list",
			"part p {\n  point (1, // first\n    2)\n}\n",
			"part p {\n  point (1,  // first\n    2)\n}\n",
		},
		{
			"blank lines at body edges go",
			"asset a {\n\n  x = 1\n\n}\n",
			"asset a {\n  x = 1\n}\n",
		},
		{
			"crlf becomes lf",
			"asset a {\r\n  x = 1 // c\r\n}\r\n",
			"asset a {\n  x = 1  // c\n}\n",
		},
		{"empty file", "", ""},
		{"only a comment", "// nothing here", "// nothing here\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Format([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, c.want)
			}
			again, err := Format(got)
			if err != nil || !bytes.Equal(again, got) {
				t.Fatalf("not idempotent: %v\n%s", err, again)
			}
		})
	}
}

func TestFormatRefusesBrokenSource(t *testing.T) {
	out, err := Format([]byte("asset a {\n  x = \n"))
	var fe *FormatError
	if err == nil || out != nil {
		t.Fatalf("got %q, %v", out, err)
	}
	if !asFormatError(err, &fe) || len(fe.Diagnostics) == 0 {
		t.Fatalf("error %v is not a FormatError with diagnostics", err)
	}
}

func asFormatError(err error, target **FormatError) bool {
	fe, ok := err.(*FormatError)
	if ok {
		*target = fe
	}
	return ok
}

// Each case here is a parser fixture for a rule in the grammar header.
func TestContextualKeywords(t *testing.T) {
	findKinds := func(tree *Tree, kind string) []*Node {
		var out []*Node
		tree.Root().Walk(func(n *Node) bool {
			if n.Kind() == kind {
				out = append(out, n)
			}
			return true
		})
		return out
	}
	t.Run("by is a field name", func(t *testing.T) {
		tree := mustParse(t, "part p { inflate { by = 14% } }")
		s := findKinds(tree, "setting")
		if len(s) != 1 || s[0].Field("name").Text() != "by" || s[0].Field("value").Text() != "14%" {
			t.Fatalf("got %s", sexp(tree.Root()))
		}
	})
	t.Run("by is a field name after another setting", func(t *testing.T) {
		tree := mustParse(t, "part p { inflate { amount = 2\n by = 14% } }")
		if got := len(findKinds(tree, "setting")); got != 2 {
			t.Fatalf("got %d settings\n%s", got, sexp(tree.Root()))
		}
	})
	t.Run("by introduces a count in a sampled row", func(t *testing.T) {
		tree := mustParse(t, "part p { path { sample a in 0deg..90deg by 9 -> point (0, a, 1) } }")
		rows := findKinds(tree, "sample_row")
		if len(rows) != 1 || rows[0].Field("count").Text() != "9" || rows[0].Field("row").Field("name").Text() != "point" {
			t.Fatalf("got %s", sexp(tree.Root()))
		}
	})
	t.Run("runtime is a value", func(t *testing.T) {
		tree := mustParse(t, "mount hand { mode = runtime }")
		s := findKinds(tree, "setting")
		if len(s) != 1 || s[0].Field("value").Kind() != "identifier" || s[0].Field("value").Text() != "runtime" {
			t.Fatalf("got %s", sexp(tree.Root()))
		}
	})
	t.Run("runtime is a field name", func(t *testing.T) {
		tree := mustParse(t, "mount hand { a = 5\n runtime = 3 }")
		if got := len(findKinds(tree, "setting")); got != 2 {
			t.Fatalf("got %d settings\n%s", got, sexp(tree.Root()))
		}
	})
	t.Run("runtime is a param modifier", func(t *testing.T) {
		tree := mustParse(t, "param c : color = #1d4e89 runtime { doc = \"x\" }\nparam d : switch = on runtime")
		p := findKinds(tree, "param_decl")
		if len(p) != 2 || p[0].Field("modifier") == nil || p[1].Field("modifier") == nil || p[0].Field("body") == nil {
			t.Fatalf("got %s", sexp(tree.Root()))
		}
	})
	t.Run("a param without runtime has none", func(t *testing.T) {
		tree := mustParse(t, "param c : length = 1m in 0m..2m\nparam d : switch = on")
		for _, p := range findKinds(tree, "param_decl") {
			if p.Field("modifier") != nil {
				t.Fatalf("unexpected modifier in %s", sexp(p))
			}
		}
	})
	t.Run("top-level keywords are names inside bodies", func(t *testing.T) {
		tree := mustParse(t, "output o { material = steel\n mesh = x\n lod = 2\n asset = a\n param = 1\n budget = 3 }")
		if got := len(findKinds(tree, "setting")); got != 6 {
			t.Fatalf("got %d settings\n%s", got, sexp(tree.Root()))
		}
	})
	t.Run("top-level keywords are blocks and rows inside bodies", func(t *testing.T) {
		tree := mustParse(t, "part p { material m { a = 1 } skin (1, 2) lod lo { } }")
		if len(findKinds(tree, "block")) != 2 || len(findKinds(tree, "row")) != 1 {
			t.Fatalf("got %s", sexp(tree.Root()))
		}
	})
	t.Run("in and as are names inside bodies", func(t *testing.T) {
		tree := mustParse(t, "part p { in = 1\n as = 2\n then_x = 3 }")
		if got := len(findKinds(tree, "setting")); got != 3 {
			t.Fatalf("got %d settings\n%s", got, sexp(tree.Root()))
		}
	})
	t.Run("on and off are booleans in a value", func(t *testing.T) {
		tree := mustParse(t, "part p { a = on\n b = off }")
		if got := len(findKinds(tree, "boolean")); got != 2 {
			t.Fatalf("got %s", sexp(tree.Root()))
		}
	})
	t.Run("a setting then a row with no line break", func(t *testing.T) {
		tree := mustParse(t, "part p { material = steel point (0, 1, 0) }")
		if len(findKinds(tree, "setting")) != 1 || len(findKinds(tree, "row")) != 1 || len(findKinds(tree, "call_expr")) != 0 {
			t.Fatalf("got %s", sexp(tree.Root()))
		}
	})
	t.Run("a pair-named block and a row share a prefix", func(t *testing.T) {
		tree := mustParse(t, "part p { mirror { joint (arml, armr) { a = 1 }\n joint (arml, armr)\n joint (1, 2) } }")
		if len(findKinds(tree, "block")) != 2 || len(findKinds(tree, "row")) != 2 || len(findKinds(tree, "name_pair")) != 1 {
			t.Fatalf("got %s", sexp(tree.Root()))
		}
	})
	t.Run("a call and a member on a computed value", func(t *testing.T) {
		tree := mustParse(t, "part p { a = bounds().max.y\n b = position(socket(x)).y\n c = finish.gloss() }")
		if len(findKinds(tree, "member_expr")) != 3 || len(findKinds(tree, "qualified_name")) != 1 {
			t.Fatalf("got %s", sexp(tree.Root()))
		}
	})
	t.Run("a qualified row name", func(t *testing.T) {
		tree := mustParse(t, "part p { lettering.word (\"HOME\", height = 0.42m, at = (0, 4m, 0.3m)) }")
		rows := findKinds(tree, "row")
		if len(rows) != 1 || rows[0].Field("name").Kind() != "qualified_name" {
			t.Fatalf("got %s", sexp(tree.Root()))
		}
	})
}

func TestExpressionShape(t *testing.T) {
	expr := func(t *testing.T, src string) string {
		tree := mustParse(t, "part p { x = "+src+" }")
		var value *Node
		tree.Root().Walk(func(n *Node) bool {
			if n.Kind() == "setting" {
				value = n.Field("value")
			}
			return true
		})
		return paren(value)
	}
	cases := map[string]string{
		"1 + 2 * 3":              "(1 + (2 * 3))",
		"1 * 2 + 3":              "((1 * 2) + 3)",
		"1 - 2 - 3":              "((1 - 2) - 3)",
		"a || b && c":            "(a || (b && c))",
		"a && b || c":            "((a && b) || c)",
		"a + 1 < b * 2":          "((a + 1) < (b * 2))",
		"-a * b":                 "((-a) * b)",
		"-a.b":                   "(-a.b)",
		"!f(x) && y":             "((!f(x)) && y)",
		"if a then 1 else 2 + 3": "(if a then 1 else (2 + 3))",
		"a[1].b":                 "((a[1]).b)",
		"(1 + 2) * 3":            "(((1 + 2)) * 3)",
	}
	for src, want := range cases {
		if got := expr(t, src); got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
	// Comparison is non-associative: a < b < c is an error.
	if tree := Parse([]byte("part p { x = a < b < c }")); !tree.HasErrors() {
		t.Error("a < b < c should be a syntax error")
	}
}

// paren renders an expression with explicit brackets for operators.
func paren(n *Node) string {
	switch n.Kind() {
	case "binary_expr":
		return "(" + paren(n.Field("left")) + " " + n.Field("operator").Text() + " " + paren(n.Field("right")) + ")"
	case "unary_expr":
		return "(" + n.Field("operator").Text() + paren(n.Field("operand")) + ")"
	case "if_expr":
		return "(if " + paren(n.Field("condition")) + " then " + paren(n.Field("consequence")) + " else " + paren(n.Field("alternative")) + ")"
	case "index_expr":
		return "(" + paren(n.Field("object")) + "[" + paren(n.Field("index")) + "])"
	case "member_expr":
		return "(" + paren(n.Field("object")) + "." + n.Field("member").Text() + ")"
	case "paren_expr":
		return "(" + paren(n.Field("value")) + ")"
	case "call_expr":
		return n.Text()
	}
	return n.Text()
}

func TestNodeAPI(t *testing.T) {
	tree := mustParse(t, "greenware 1\n\nparam h : length = 3.05m in 2.5m..3.5m runtime\nasset a {\n  title = \"x\" // note\n}\n")
	root := tree.Root()
	if ed := root.Children()[0]; ed.Kind() != "edition_decl" || ed.Field("edition").Text() != "1" {
		t.Fatalf("edition: %s", sexp(root))
	}
	var param, setting *Node
	root.Walk(func(n *Node) bool {
		switch n.Kind() {
		case "param_decl":
			param = n
		case "setting":
			setting = n
		}
		return true
	})
	if param.Field("name").Text() != "h" || param.Field("type").Text() != "length" ||
		param.Field("default").Text() != "3.05m" || param.Field("range").Kind() != "range" ||
		param.Field("range").Field("start").Text() != "2.5m" || param.Field("modifier").Text() != "runtime" {
		t.Fatalf("param fields: %s", sexp(param))
	}
	if got := param.Span(); got.Start != 13 || got.Len() != len("param h : length = 3.05m in 2.5m..3.5m runtime") {
		t.Fatalf("param span %+v", got)
	}
	if p := param.Start(); p != (Position{3, 1}) {
		t.Fatalf("param start %v", p)
	}
	if setting.Field("name").FieldName() != "name" || setting.Field("value").Parent() != setting {
		t.Fatal("field name or parent wrong")
	}
	if got := setting.Start(); got != (Position{5, 3}) {
		t.Fatalf("setting start %v", got)
	}
	if body := setting.Parent(); body.Kind() != "body" || len(body.NamedChildren()) != 1 {
		t.Fatalf("body has %d named children", len(body.NamedChildren()))
	}
	if cs := tree.Comments(); len(cs) != 1 || cs[0].Text() != "// note" {
		t.Fatalf("comments: %v", cs)
	}
}

func TestPositionCountsCodePoints(t *testing.T) {
	tree := Parse([]byte("// héllo\nasset a {}\n"))
	if got := tree.Position(len("// héllo\n") + 6); got != (Position{2, 7}) {
		t.Fatalf("got %v", got)
	}
	if got := tree.Position(len("// héllo")); got != (Position{1, 9}) {
		t.Fatalf("got %v", got)
	}
	if got := tree.Position(1 << 20); got.Line != 3 {
		t.Fatalf("past the end: %v", got)
	}
}

func TestMalformedInput(t *testing.T) {
	cases := map[string]string{
		"unterminated block":        "asset a {\n  x = 1\n",
		"unterminated nested block": "asset a {\n  part p {\n    x = 1\n}\n",
		"stray token":               "asset a {\n  x = 1\n  ) y = 2\n}\n",
		"stray token at top level":  "asset a {}\n}\nmeta {}\n",
		"missing value":             "asset a { x = }\n",
		"unterminated string":       "asset a { x = \"abc }\n",
		"bad character":             "asset a { x = 1 @ 2 }\n",
		"incomplete row":            "part p { point (1, 2 }\n",
		"empty argument list":       "part p { point () }\n",
		"only a brace":              "{",
		"edition without number":    "greenware\nasset a {}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			done := make(chan *Tree, 1)
			go func() { done <- Parse([]byte(src)) }()
			var tree *Tree
			select {
			case tree = <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("parse did not finish")
			}
			if tree == nil || tree.Root() == nil {
				t.Fatal("no tree")
			}
			if !bytes.Equal(tree.Source(), []byte(src)) {
				t.Fatal("source changed")
			}
			ds := tree.Diagnostics()
			if len(ds) == 0 {
				t.Fatalf("no diagnostic\n%s", sexp(tree.Root()))
			}
			for _, d := range ds {
				if d.Code != CodeSyntax {
					t.Errorf("code %q", d.Code)
				}
				if d.Span.Len() == 0 || d.Span.Start < 0 || d.Span.End > len(src) {
					t.Errorf("diagnostic %v has span %+v in a %d-byte source", d, d.Span, len(src))
				}
				if d.Start.Line < 1 || d.End.Line < d.Start.Line {
					t.Errorf("diagnostic %v has bad positions %v..%v", d, d.Start, d.End)
				}
				if d.Message == "" {
					t.Error("empty message")
				}
			}
			// Leaves and trivia still cover the source.
			covered := 0
			for _, l := range tree.Leaves() {
				covered += l.Span().Len()
			}
			for _, tv := range tree.Trivia() {
				covered += tv.Span.Len()
			}
			if covered != len(src) {
				t.Errorf("leaves and trivia cover %d of %d bytes", covered, len(src))
			}
		})
	}
}

func TestStrayTokenPointsAtTheToken(t *testing.T) {
	src := "asset a {\n  x = 1\n  ) y = 2\n}\n"
	ds := Parse([]byte(src)).Diagnostics()
	if len(ds) == 0 {
		t.Fatal("no diagnostic")
	}
	at := strings.Index(src, ")")
	if ds[0].Span.Start > at || ds[0].Span.End <= at {
		t.Fatalf("first diagnostic %+v does not cover the stray ')' at %d", ds[0], at)
	}
	if ds[0].Start != (Position{3, 3}) {
		t.Fatalf("start %v, want 3:3", ds[0].Start)
	}
}

func TestUnterminatedBlockPointsAtTheEnd(t *testing.T) {
	src := "asset a {\n  x = 1\n"
	ds := Parse([]byte(src)).Diagnostics()
	if len(ds) == 0 {
		t.Fatal("no diagnostic")
	}
	last := ds[len(ds)-1]
	if last.Span.End != len(src) && ds[0].Span.End > len(src) {
		t.Fatalf("diagnostic %+v", ds)
	}
}

// Hostile input must finish in bounded time and memory.
func TestHostileInputTerminates(t *testing.T) {
	inputs := map[string]string{
		"brace flood":    strings.Repeat("{", 20000),
		"close flood":    strings.Repeat("}", 20000),
		"deep nesting":   strings.Repeat("a {", 3000) + strings.Repeat("}", 3000),
		"paren flood":    "part p { x = " + strings.Repeat("(", 5000) + " }",
		"unary flood":    "part p { x = " + strings.Repeat("-", 5000) + "1 }",
		"garbage":        strings.Repeat("@#$ ) ] = , .. -> ", 2000),
		"long expr":      "part p { x = " + strings.Repeat("1 + ", 20000) + "1 }",
		"many errors":    strings.Repeat("part p { x = } ", 2000),
		"non utf8":       "asset a { x = \xff\xfe\xfd }",
		"null bytes":     "asset a { x = 1\x00 }",
		"long comment":   "// " + strings.Repeat("x", 200000),
		"unclosed quote": "part p { x = \"" + strings.Repeat("a", 100000),
	}
	for name, src := range inputs {
		t.Run(name, func(t *testing.T) {
			done := make(chan *Tree, 1)
			go func() { done <- Parse([]byte(src)) }()
			select {
			case tree := <-done:
				if !bytes.Equal(tree.Source(), []byte(src)) {
					t.Fatal("source changed")
				}
				for _, d := range tree.Diagnostics() {
					if d.Span.Start < 0 || d.Span.End > len(src) {
						t.Fatalf("span %+v outside the source", d.Span)
					}
				}
			case <-time.After(60 * time.Second):
				t.Fatal("parse did not finish in 60 seconds")
			}
		})
	}
}

func TestReparseMatchesFreshParse(t *testing.T) {
	src := readSample(t, "goalpost")
	edits := []Edit{
		{Start: bytes.Index(src, []byte("3.05m")), End: bytes.Index(src, []byte("3.05m")) + 5, Text: []byte("3.1m")},
		{Start: bytes.Index(src, []byte("segments = 24")), End: bytes.Index(src, []byte("segments = 24")), Text: []byte("// inserted\n      ")},
		{Start: bytes.Index(src, []byte("lod 1")), End: bytes.Index(src, []byte("lod 1")) + 5, Text: []byte("lod 9")},
		{Start: 0, End: 0, Text: []byte("// first line\n")},
		{Start: len(src), End: len(src), Text: []byte("\nmeta { a = 1 }\n")},
	}
	tree := Parse(src)
	for i, e := range edits {
		next, err := tree.Reparse(e)
		if err != nil {
			t.Fatalf("edit %d: %v", i, err)
		}
		fresh := Parse(next.Source())
		if sexp(next.Root()) != sexp(fresh.Root()) || strings.Join(kinds(next), " ") != strings.Join(kinds(fresh), " ") {
			t.Fatalf("edit %d: edited tree differs from a fresh parse", i)
		}
		if len(next.Diagnostics()) != len(fresh.Diagnostics()) {
			t.Fatalf("edit %d: diagnostics differ: %v and %v", i, next.Diagnostics(), fresh.Diagnostics())
		}
		tree = next
	}
	// An edit that breaks the file and one that repairs it.
	broken, err := tree.Reparse(Edit{Start: bytes.Index(tree.Source(), []byte("collision {")) + len("collision "), End: bytes.Index(tree.Source(), []byte("collision {")) + len("collision {"), Text: nil})
	if err != nil {
		t.Fatal(err)
	}
	if !broken.HasErrors() {
		t.Fatal("removing a brace should break the file")
	}
	if fresh := Parse(broken.Source()); sexp(broken.Root()) != sexp(fresh.Root()) {
		t.Fatal("edited broken tree differs from a fresh parse")
	}
	if _, err := tree.Reparse(Edit{Start: 5, End: 2}); err == nil {
		t.Fatal("bad edit accepted")
	}
}

func BenchmarkParseAthlete(b *testing.B) {
	src := readSample(b, "athlete")
	b.SetBytes(int64(len(src)))
	for i := 0; i < b.N; i++ {
		Parse(src)
	}
}

// The parser table must stay small and quick to load and run. A grammar change
// that multiplies the productions (see language/grammar) shows up here first.
func TestParserSizeAndSpeed(t *testing.T) {
	if len(grammarBlob) > 100<<10 {
		t.Errorf("greenware.bin is %d bytes, want at most %d", len(grammarBlob), 100<<10)
	}
	src := readSample(t, "goalpost")
	start := time.Now()
	tree := Parse(src)
	elapsed := time.Since(start)
	if tree.HasErrors() {
		t.Fatalf("diagnostics: %v", tree.Diagnostics())
	}
	if elapsed > 2*time.Second {
		t.Errorf("parsing goalpost.gw took %v, want under 2s (includes loading the table)", elapsed)
	}
}
