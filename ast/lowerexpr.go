package ast

import (
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/units"
)

func span(n *lang.Node) diag.Span {
	s := n.Span()
	return diag.Span{Start: s.Start, End: s.End}
}

func (l *lowerer) report(d diag.Diagnostic) { l.diags = append(l.diags, d) }

func bad(n *lang.Node) Expr { return &Bad{base{span(n)}} }

// binary operator precedence, as in the language reference, section 2.8.
func prec(op string) int {
	switch op {
	case "||":
		return 2
	case "&&":
		return 3
	case "==", "!=", "<", "<=", ">", ">=":
		return 4
	case "+", "-":
		return 5
	case "*", "/":
		return 6
	}
	return 0
}

// tok is one element of a flattened operator expression.
type tok struct {
	op      string // set for an operator
	prefix  bool
	operand Expr
	span    diag.Span
}

// flatten turns a chain of binary_expr and unary_expr nodes into a token
// list. The tree does not have to be shaped by precedence: the list is
// re-associated by precedence climbing, so the result is right even if the
// parser nests every operator to the right.
func (l *lowerer) flatten(n *lang.Node, out *[]tok) {
	switch n.Kind() {
	case "binary_expr":
		l.flatten(n.Field("left"), out)
		op := n.Field("operator")
		*out = append(*out, tok{op: op.Text(), span: span(op)})
		l.flatten(n.Field("right"), out)
	case "unary_expr":
		op := n.Field("operator")
		*out = append(*out, tok{op: op.Text(), prefix: true, span: span(op)})
		l.flatten(n.Field("operand"), out)
	default:
		*out = append(*out, tok{operand: l.expr(n)})
	}
}

type climber struct {
	toks []tok
	pos  int
}

func (c *climber) unary() Expr {
	if c.pos >= len(c.toks) {
		return nil
	}
	t := c.toks[c.pos]
	if t.prefix {
		c.pos++
		x := c.unary()
		if x == nil {
			return nil
		}
		return &Unary{base{diag.Span{Start: t.span.Start, End: x.Span().End}}, t.op, x}
	}
	c.pos++
	return t.operand
}

func (c *climber) binary(minPrec int) Expr {
	left := c.unary()
	if left == nil {
		return nil
	}
	for c.pos < len(c.toks) {
		t := c.toks[c.pos]
		p := prec(t.op)
		if t.prefix || t.operand != nil || p < minPrec || p == 0 {
			break
		}
		c.pos++
		right := c.binary(p + 1)
		if right == nil {
			return left
		}
		left = &Binary{base{diag.Span{Start: left.Span().Start, End: right.Span().End}}, t.op, left, right}
	}
	return left
}

// expr lowers an expression node.
func (l *lowerer) expr(n *lang.Node) Expr {
	if n == nil {
		return &Bad{}
	}
	if n.IsError() || n.IsMissing() {
		return bad(n)
	}
	s := span(n)
	switch n.Kind() {
	case "binary_expr", "unary_expr":
		var toks []tok
		l.flatten(n, &toks)
		c := &climber{toks: toks}
		if e := c.binary(1); e != nil {
			return e
		}
		return bad(n)
	case "number":
		v, err := units.ParseNumber(n.Text())
		if err != nil {
			l.report(diag.New(diag.CodeSyntax, s, "%v", err))
			return bad(n)
		}
		return &NumberLit{base{s}, n.Text(), v}
	case "integer":
		v, err := units.ParseNumber(n.Text())
		if err != nil {
			return bad(n)
		}
		return &NumberLit{base{s}, n.Text(), v}
	case "color":
		c, err := units.ParseColor(n.Text())
		if err != nil {
			l.report(diag.New(diag.CodeSyntax, s, "%v", err))
			return bad(n)
		}
		return &ColorLit{base{s}, c}
	case "string":
		v, msg := decodeString(n.Text())
		if msg != "" {
			l.report(diag.New(diag.CodeSyntax, s, "%s", msg))
		}
		return &StringLit{base{s}, v}
	case "boolean":
		return &BoolLit{base{s}, n.Text() == "on"}
	case "identifier":
		return &Name{base{s}, n.Text()}
	case "qualified_name":
		return &Qualified{base{s}, strings.Split(n.Text(), ".")}
	case "paren_expr":
		return &Paren{base{s}, l.expr(n.Field("value"))}
	case "tuple":
		return &Tuple{base{s}, l.exprs(n)}
	case "list":
		return &List{base{s}, l.exprs(n)}
	case "if_expr":
		return &If{base{s}, l.expr(n.Field("condition")), l.expr(n.Field("consequence")), l.expr(n.Field("alternative"))}
	case "call_expr":
		fn := n.Field("function")
		c := &Call{base: base{s}}
		if fn != nil {
			c.Func, c.FuncSpan = fn.Text(), span(fn)
		}
		c.Args = l.args(n)
		return c
	case "index_expr":
		return &Index{base{s}, l.expr(n.Field("object")), l.expr(n.Field("index"))}
	case "member_expr":
		m := n.Field("member")
		name := ""
		if m != nil {
			name = m.Text()
		}
		return &Member{base{s}, l.expr(n.Field("object")), name}
	}
	l.report(diag.New(diag.CodeSyntax, s, "unexpected %s in an expression", n.Kind()))
	return bad(n)
}

// exprs lowers the expression children of a tuple or list.
func (l *lowerer) exprs(n *lang.Node) []Expr {
	var out []Expr
	for _, c := range n.NamedChildren() {
		out = append(out, l.expr(c))
	}
	return out
}

func (l *lowerer) rng(n *lang.Node) *Range {
	return &Range{Span: span(n), Lo: l.expr(n.Field("start")), Hi: l.expr(n.Field("end"))}
}

// args lowers the `argument` children of a call or row.
func (l *lowerer) args(n *lang.Node) []Arg {
	var out []Arg
	for _, c := range n.Children() {
		if c.Kind() != "argument" {
			continue
		}
		a := Arg{Span: span(c)}
		if nm := c.Field("name"); nm != nil {
			a.Name, a.NameSpan = nm.Text(), span(nm)
		}
		if v := c.Field("value"); v != nil {
			if v.Kind() == "range" {
				a.Range = l.rng(v)
			} else {
				a.Value = l.expr(v)
			}
		}
		out = append(out, a)
	}
	return out
}

// decodeString decodes a string token with the JSON escape vocabulary. It
// returns a message for an invalid escape.
func decodeString(tok string) (string, string) {
	if len(tok) < 2 {
		return "", "unterminated string"
	}
	s := tok[1 : len(tok)-1]
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x20 {
			return b.String(), "a string cannot hold a raw control character"
		}
		if c != '\\' {
			r, size := utf8.DecodeRuneInString(s[i:])
			b.WriteRune(r)
			i += size
			continue
		}
		if i+1 >= len(s) {
			return b.String(), "incomplete escape in a string"
		}
		e := s[i+1]
		i += 2
		switch e {
		case '"', '\\', '/':
			b.WriteByte(e)
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'u':
			r, ok := hex4(s, i)
			if !ok {
				return b.String(), "\\u needs four hex digits"
			}
			i += 4
			if utf16.IsSurrogate(r) {
				if r < 0xDC00 && i+6 <= len(s) && s[i] == '\\' && s[i+1] == 'u' {
					if lo, ok := hex4(s, i+2); ok {
						if dec := utf16.DecodeRune(r, lo); dec != utf8.RuneError {
							b.WriteRune(dec)
							i += 6
							continue
						}
					}
				}
				return b.String(), "unpaired surrogate in a string escape"
			}
			b.WriteRune(r)
		default:
			return b.String(), fmt.Sprintf("unknown escape \\%c in a string", e)
		}
	}
	return b.String(), ""
}

func hex4(s string, i int) (rune, bool) {
	if i+4 > len(s) {
		return 0, false
	}
	var r rune
	for _, c := range s[i : i+4] {
		var d rune
		switch {
		case c >= '0' && c <= '9':
			d = c - '0'
		case c >= 'a' && c <= 'f':
			d = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, false
		}
		r = r<<4 | d
	}
	return r, true
}
