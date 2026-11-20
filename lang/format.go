package lang

import (
	"bytes"
	"fmt"
	"strings"
)

// Format rewrites a Greenware source file in the canonical style:
//
//   - two-space indentation, one setting, row or block per line;
//   - single spaces around "=", "in", "by", "->" and binary operators, none
//     inside brackets or around "..", "." and a call's "(";
//   - a body that sits on one line in the source stays on one line
//     ("{ base = #f2c200 roughness = 0.4 }"); every other body gets a line per
//     item;
//   - comments stay where they are: a comment after code stays on that line,
//     a comment on its own line stays on its own line; a trailing comment
//     follows its code after exactly two spaces, with no column alignment;
//   - blank lines collapse to at most one, and none are kept at the start or
//     end of a body or file;
//   - line endings become "\n", and the file ends with one.
//
// Format is idempotent, and the formatted file parses to the same kinds of
// nodes in the same order. It returns an error, and no output, when the source
// has syntax errors: the error lists them.
func Format(src []byte) ([]byte, error) {
	t := Parse(src)
	if t.HasErrors() {
		return nil, &FormatError{Diagnostics: t.Diagnostics()}
	}
	f := &formatter{src: t.src, comments: t.Comments()}
	f.sourceFile(t.root)
	return f.render(), nil
}

// FormatError is returned by Format for a source with syntax errors.
type FormatError struct {
	Diagnostics []Diagnostic
}

func (e *FormatError) Error() string {
	if len(e.Diagnostics) == 0 {
		return "cannot format: syntax error"
	}
	msg := fmt.Sprintf("cannot format: %d syntax error(s); first: %s", len(e.Diagnostics), e.Diagnostics[0])
	return msg
}

// outLine is one line of output.
type outLine struct {
	indent  int
	text    string
	comment string
	blank   bool // a blank line comes before this line
}

type formatter struct {
	src      []byte
	comments []*Node
	next     int // index of the next comment not yet written

	lines []outLine
	cur   *outLine

	indent       int  // indent level of the item being written
	inItem       bool // between the first and last token of an item
	prevEnd      int  // source offset just after the last token or comment written
	afterOpen    bool // the last thing written was a body's "{"
	pendingSpace bool
}

// tightParen lists the node kinds whose "(" follows the name with no space.
// A row's "(" is spaced from its name: "point (0, 1, 0)".
var tightParen = map[string]bool{
	"call_expr": true, "param_type": true, "painter_decl": true, "template_decl": true,
}

func (f *formatter) sourceFile(root *Node) {
	for _, c := range root.children {
		if c.kind == "comment" {
			continue
		}
		f.item(c)
	}
	f.flushComments(len(f.src))
	f.endLine()
}

// item writes one declaration or body item on its own line.
func (f *formatter) item(n *Node) {
	f.flushComments(n.span.Start)
	f.beginLine(n.span.Start, true)
	f.inItem = true
	f.node(n)
	f.inItem = false
}

// node writes a node and its descendants on the current line.
func (f *formatter) node(n *Node) {
	if n.kind == "body" {
		f.body(n)
		return
	}
	if len(n.children) == 0 {
		f.token(n)
		return
	}
	var prev *Node
	for _, c := range n.children {
		if c.kind == "comment" {
			continue
		}
		if prev != nil {
			f.pendingSpace = spaceBetween(n, prev, c)
		}
		f.node(c)
		prev = c
	}
}

// spaceBetween decides whether a space separates two neighbouring children.
func spaceBetween(parent, prev, next *Node) bool {
	nextFirst := leafText(firstLeaf(next))
	switch nextFirst {
	case ",", ")", "]", "..", ".":
		return false
	}
	switch leafText(lastLeaf(prev)) {
	case "(", "[", ".", "..":
		return false
	}
	if parent.kind == "unary_expr" {
		return false
	}
	if len(next.children) == 0 {
		switch next.kind {
		case "(":
			return !tightParen[parent.kind]
		case "[":
			return parent.kind != "index_expr"
		case ":":
			return parent.kind != "typed_param"
		}
	}
	return true
}

func leafText(n *Node) string {
	if n == nil {
		return ""
	}
	return n.Text()
}

func lastLeaf(n *Node) *Node {
	for i := len(n.children) - 1; i >= 0; i-- {
		if l := lastLeaf(n.children[i]); l != nil && l.span.Len() > 0 {
			return l
		}
	}
	if len(n.children) == 0 {
		return n
	}
	return nil
}

// body writes "{ ... }". A body that is on one line in the source stays on
// one line. Any other body gets one item per line.
func (f *formatter) body(n *Node) {
	var open, close *Node
	var items []*Node
	for _, c := range n.children {
		switch {
		case c.kind == "{" && open == nil:
			open = c
		case c.kind == "}":
			close = c
		case c.kind != "comment":
			items = append(items, c)
		}
	}
	oneLine := !bytes.Contains(f.src[n.span.Start:n.span.End], []byte{'\n'})
	if len(items) == 0 && !f.hasCommentIn(open.span.End, close.span.Start) {
		oneLine = true
	}
	if oneLine {
		f.token(open)
		for _, it := range items {
			f.pendingSpace = true
			f.node(it)
		}
		f.pendingSpace = len(items) > 0
		f.token(close)
		return
	}
	f.token(open)
	f.afterOpen = true
	wasInItem := f.inItem
	f.inItem = false
	f.indent++
	for _, it := range items {
		f.item(it)
	}
	f.flushComments(close.span.Start)
	f.indent--
	f.inItem = wasInItem
	f.endLine()
	f.cur = &outLine{indent: f.indent}
	f.pendingSpace = false
	f.token(close)
}

func (f *formatter) hasCommentIn(start, end int) bool {
	for _, c := range f.comments[f.next:] {
		if c.span.Start >= end {
			return false
		}
		if c.span.Start >= start {
			return true
		}
	}
	return false
}

// token writes a leaf node, after any comments that come before it.
func (f *formatter) token(n *Node) {
	f.flushComments(n.span.Start)
	f.write(n.Text())
	f.prevEnd = n.span.End
	f.afterOpen = n.kind == "{"
}

// write adds text to the current line. A pending space applies only when the
// line already has text.
func (f *formatter) write(s string) {
	if f.cur == nil {
		f.cur = &outLine{indent: f.lineIndent()}
	}
	if f.pendingSpace && f.cur.text != "" {
		f.cur.text += " "
	}
	f.pendingSpace = false
	f.cur.text += s
}

// lineIndent is the indent of a new line: one level deeper for the lines that
// continue an item after a comment.
func (f *formatter) lineIndent() int {
	if f.inItem {
		return f.indent + 1
	}
	return f.indent
}

func (f *formatter) endLine() {
	if f.cur != nil && (f.cur.text != "" || f.cur.comment != "") {
		f.lines = append(f.lines, *f.cur)
	}
	f.cur = nil
}

// beginLine starts a new line for source offset start. Blank lines before it
// in the source are kept as one, if allowBlank is true.
func (f *formatter) beginLine(start int, allowBlank bool) {
	f.endLine()
	blank := allowBlank && !f.afterOpen && len(f.lines) > 0 &&
		bytes.Count(f.src[f.prevEnd:start], []byte{'\n'}) >= 2
	f.cur = &outLine{indent: f.indent, blank: blank}
	f.afterOpen = false
}

// flushComments writes every comment that starts before offset.
func (f *formatter) flushComments(offset int) {
	for f.next < len(f.comments) && f.comments[f.next].span.Start < offset {
		c := f.comments[f.next]
		f.next++
		text := strings.TrimRight(c.Text(), " \t\r")
		sameLine := f.cur != nil && f.cur.text != "" &&
			!bytes.Contains(f.src[f.prevEnd:c.span.Start], []byte{'\n'})
		if sameLine {
			f.cur.comment = text
			f.endLine()
		} else {
			f.endLine()
			blank := !f.afterOpen && len(f.lines) > 0 &&
				bytes.Count(f.src[f.prevEnd:c.span.Start], []byte{'\n'}) >= 2
			f.lines = append(f.lines, outLine{indent: f.lineIndent(), text: "", comment: text, blank: blank})
		}
		f.prevEnd = c.span.End
		f.afterOpen = false
		f.pendingSpace = false
	}
}

// render joins the lines. A trailing comment follows its code after exactly two
// spaces; the formatter does not line comments up in a column, so editing one
// line never moves the comments on its neighbours.
func (f *formatter) render() []byte {
	var out bytes.Buffer
	for _, l := range f.lines {
		if l.blank {
			out.WriteByte('\n')
		}
		out.WriteString(strings.Repeat("  ", l.indent))
		out.WriteString(l.text)
		if l.comment != "" {
			if l.text != "" {
				out.WriteString("  ")
			}
			out.WriteString(l.comment)
		}
		out.WriteByte('\n')
	}
	return out.Bytes()
}
