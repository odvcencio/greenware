package lang

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	gts "github.com/odvcencio/gotreesitter"

	gw "m31labs.dev/greenware/language"
)

// CodeSyntax is the diagnostic code of every syntax error.
const CodeSyntax = gw.DiagnosticPrefix + "SYNTAX"

// Span is a half-open byte range [Start, End) of the source.
type Span struct {
	Start, End int
}

// Len returns the number of bytes the span covers.
func (s Span) Len() int { return s.End - s.Start }

// Position is a place in the source. Line and Column both start at 1. Column
// counts Unicode code points from the start of the line.
type Position struct {
	Line, Column int
}

func (p Position) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Column) }

// Diagnostic is one problem found in the source. Every diagnostic has a
// non-empty Span.
type Diagnostic struct {
	Code    string
	Message string
	Span    Span
	Start   Position
	End     Position
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%s: %s: %s", d.Start, d.Code, d.Message)
}

// Node is one node of the syntax tree. Leaf nodes are tokens: keywords,
// punctuation, names, numbers and comments.
type Node struct {
	tree     *Tree
	parent   *Node
	kind     string
	field    string
	span     Span
	children []*Node
	named    bool
	missing  bool
	isError  bool
}

// Tree is the result of Parse. It holds its own copy of the source.
type Tree struct {
	src    []byte
	root   *Node
	diags  []Diagnostic
	lines  []int // byte offset of the start of each line
	native *gts.Tree
	parser *gts.Parser
}

// Parse parses a Greenware source file. It never returns nil. Syntax errors
// appear in Diagnostics and as "ERROR" nodes or missing tokens in the tree.
// If the embedded parser cannot load, the tree has one diagnostic that says so.
func Parse(src []byte) *Tree {
	t := newTree(src)
	_, pool, err := load()
	if err != nil {
		return t.failed(fmt.Errorf("load grammar: %w", err))
	}
	native, err := pool.Parse(t.src)
	if err != nil {
		return t.failed(err)
	}
	t.adopt(native)
	return t
}

func newTree(src []byte) *Tree {
	t := &Tree{src: bytes.Clone(src)}
	t.lines = []int{0}
	for i, b := range t.src {
		if b == '\n' {
			t.lines = append(t.lines, i+1)
		}
	}
	return t
}

// failed builds a tree for a source the parser could not handle: one root
// node over the whole source and one diagnostic.
func (t *Tree) failed(err error) *Tree {
	t.root = &Node{tree: t, kind: "ERROR", span: Span{0, len(t.src)}, named: true, isError: true}
	t.diags = []Diagnostic{t.diagnostic(Span{0, len(t.src)}, err.Error())}
	return t
}

// adopt copies the native tree into Nodes and collects the diagnostics.
func (t *Tree) adopt(native *gts.Tree) {
	t.native = native
	lang := language
	var conv func(n *gts.Node, parent *Node, field string) *Node
	conv = func(n *gts.Node, parent *Node, field string) *Node {
		out := &Node{
			tree:    t,
			parent:  parent,
			kind:    n.Type(lang),
			field:   field,
			span:    Span{int(n.StartByte()), int(n.EndByte())},
			named:   n.IsNamed(),
			missing: n.IsMissing(),
			isError: n.IsError(),
		}
		count := n.ChildCount()
		if count > 0 {
			out.children = make([]*Node, 0, count)
		}
		for i := 0; i < count; i++ {
			c := n.Child(i)
			if c == nil {
				continue
			}
			out.children = append(out.children, conv(c, out, n.FieldNameForChild(i, lang)))
		}
		return out
	}
	t.root = conv(native.RootNode(), nil, "")
	t.collectDiagnostics(native.RootNode().HasError())
}

// collectDiagnostics turns ERROR nodes and missing tokens into diagnostics.
func (t *Tree) collectDiagnostics(hasError bool) {
	var visit func(n *Node)
	visit = func(n *Node) {
		switch {
		case n.isError:
			t.diags = append(t.diags, t.errorDiagnostic(n))
			return
		case n.missing:
			t.diags = append(t.diags, t.missingDiagnostic(n))
			return
		}
		for _, c := range n.children {
			visit(c)
		}
	}
	visit(t.root)
	if hasError && len(t.diags) == 0 {
		t.diags = append(t.diags, t.diagnostic(t.widen(Span{t.root.span.Start, t.root.span.Start}), "syntax error"))
	}
}

func (t *Tree) errorDiagnostic(n *Node) Diagnostic {
	if in, num, ok := t.inchHint(n); ok {
		return t.diagnostic(in, inchMessage(num))
	}
	span := n.span
	// An error node can swallow the rest of the file. Point at its first
	// token, which is where the parser gave up.
	if first := firstLeaf(n); first != nil && first.span.Len() > 0 {
		span = first.span
	}
	span = t.widen(span)
	return t.diagnostic(span, fmt.Sprintf("unexpected %s", t.describe(span)))
}

func (t *Tree) missingDiagnostic(n *Node) Diagnostic {
	span := t.widen(Span{n.span.Start, n.span.Start})
	return t.diagnostic(span, fmt.Sprintf("missing %s", quoteKind(n.kind)))
}

func quoteKind(kind string) string {
	if kind == "" {
		return "token"
	}
	return fmt.Sprintf("%q", kind)
}

// describe quotes the source text of a span, cut to a readable length.
func (t *Tree) describe(s Span) string {
	text := string(t.src[s.Start:s.End])
	if len(text) > 24 {
		cut := 24
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "..."
	}
	return fmt.Sprintf("%q", text)
}

// widen makes an empty span cover one rune, so a diagnostic always points at
// something: the rune after the span, or the rune before it at the end of the
// file. An empty source has no rune; its span stays empty.
func (t *Tree) widen(s Span) Span {
	if s.Len() > 0 || len(t.src) == 0 {
		return s
	}
	if s.Start < len(t.src) {
		_, size := utf8.DecodeRune(t.src[s.Start:])
		return Span{s.Start, s.Start + size}
	}
	_, size := utf8.DecodeLastRune(t.src)
	return Span{len(t.src) - size, len(t.src)}
}

func (t *Tree) diagnostic(s Span, msg string) Diagnostic {
	return Diagnostic{
		Code:    CodeSyntax,
		Message: msg,
		Span:    s,
		Start:   t.Position(s.Start),
		End:     t.Position(s.End),
	}
}

func firstLeaf(n *Node) *Node {
	if len(n.children) == 0 {
		return n
	}
	for _, c := range n.children {
		if l := firstLeaf(c); l != nil && l.span.Len() > 0 {
			return l
		}
	}
	return nil
}

// Source returns the source text. The tree owns the slice; do not change it.
// It is byte-for-byte the input that was parsed.
func (t *Tree) Source() []byte { return t.src }

// Root returns the root node, of kind "source_file".
func (t *Tree) Root() *Node { return t.root }

// Diagnostics returns the syntax errors in source order. A file with no
// errors has none.
func (t *Tree) Diagnostics() []Diagnostic { return t.diags }

// HasErrors reports whether the file has any syntax error.
func (t *Tree) HasErrors() bool { return len(t.diags) > 0 }

// Position converts a byte offset to a line and column. An offset past the
// end of the source gives the position of the end.
func (t *Tree) Position(offset int) Position {
	if offset < 0 {
		offset = 0
	}
	if offset > len(t.src) {
		offset = len(t.src)
	}
	lo, hi := 0, len(t.lines)
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if t.lines[mid] <= offset {
			lo = mid
		} else {
			hi = mid
		}
	}
	col := utf8.RuneCount(t.src[t.lines[lo]:offset]) + 1
	return Position{Line: lo + 1, Column: col}
}

// Leaves returns every token in source order, comments included. Together
// with Trivia it covers the source with no gap and no overlap.
func (t *Tree) Leaves() []*Node {
	var out []*Node
	t.root.Walk(func(n *Node) bool {
		if len(n.children) == 0 && n.span.Len() > 0 {
			out = append(out, n)
		}
		return true
	})
	return out
}

// Comments returns the comment nodes in source order.
func (t *Tree) Comments() []*Node {
	var out []*Node
	for _, l := range t.Leaves() {
		if l.kind == "comment" {
			out = append(out, l)
		}
	}
	return out
}

// Trivia is source text that is not a token: white space, or text the parser
// skipped without making a node. Comments are tokens, not trivia.
type Trivia struct {
	Span Span
	// Blank is true when the text is only spaces, tabs and line breaks.
	Blank bool
}

// Trivia returns the gaps between tokens in source order. Interleaving the
// gaps with Leaves rebuilds the source exactly.
func (t *Tree) Trivia() []Trivia {
	var out []Trivia
	pos := 0
	add := func(end int) {
		if end > pos {
			out = append(out, Trivia{Span{pos, end}, isBlank(t.src[pos:end])})
		}
	}
	for _, l := range t.Leaves() {
		add(l.span.Start)
		pos = l.span.End
	}
	add(len(t.src))
	return out
}

func isBlank(b []byte) bool {
	for _, c := range b {
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			return false
		}
	}
	return true
}

// Edit describes a change to the source: the bytes in [Start, End) of the old
// source become Text.
type Edit struct {
	Start, End int
	Text       []byte
}

// Reparse applies an edit and returns the tree of the new source. It reuses
// the old parse where it can. It spends the receiver's incremental state: the
// receiver stays readable, but a second Reparse on it parses from scratch.
func (t *Tree) Reparse(e Edit) (*Tree, error) {
	if e.Start < 0 || e.End < e.Start || e.End > len(t.src) {
		return nil, fmt.Errorf("edit span [%d, %d) is outside the source (%d bytes)", e.Start, e.End, len(t.src))
	}
	next := make([]byte, 0, len(t.src)-(e.End-e.Start)+len(e.Text))
	next = append(next, t.src[:e.Start]...)
	next = append(next, e.Text...)
	next = append(next, t.src[e.End:]...)
	old := t.native
	if old == nil {
		return Parse(next), nil
	}
	t.native = nil
	lang, _, err := load()
	if err != nil {
		return nil, fmt.Errorf("load grammar: %w", err)
	}
	startPoint := t.nativePoint(e.Start)
	oldEndPoint := t.nativePoint(e.End)
	out := newTree(next)
	newEnd := e.Start + len(e.Text)
	old.Edit(gts.InputEdit{
		StartByte:   uint32(e.Start),
		OldEndByte:  uint32(e.End),
		NewEndByte:  uint32(newEnd),
		StartPoint:  startPoint,
		OldEndPoint: oldEndPoint,
		NewEndPoint: out.nativePoint(newEnd),
	})
	native, err := gts.NewParser(lang).ParseIncremental(out.src, old)
	if err != nil {
		return nil, err
	}
	out.adopt(native)
	return out, nil
}

// nativePoint returns the native row and byte column of an offset.
func (t *Tree) nativePoint(offset int) gts.Point {
	p := t.Position(offset)
	return gts.Point{Row: uint32(p.Line - 1), Column: uint32(offset - t.lines[p.Line-1])}
}

// Kind returns the node kind: a rule name such as "setting", a keyword or
// punctuation such as "{", "comment", or "ERROR". Kind names are stable.
func (n *Node) Kind() string { return n.kind }

// IsNamed reports whether the node comes from a named rule. Keywords and
// punctuation are not named. Comments are.
func (n *Node) IsNamed() bool { return n.named }

// IsComment reports whether the node is a comment.
func (n *Node) IsComment() bool { return n.kind == "comment" }

// IsError reports whether the node is an "ERROR" node, which holds source the
// parser could not place.
func (n *Node) IsError() bool { return n.isError }

// IsMissing reports whether the parser added the node to repair the tree. A
// missing node has an empty span.
func (n *Node) IsMissing() bool { return n.missing }

// Span returns the half-open byte range of the node.
func (n *Node) Span() Span { return n.span }

// Start returns the line and column where the node starts.
func (n *Node) Start() Position { return n.tree.Position(n.span.Start) }

// End returns the line and column just after the node.
func (n *Node) End() Position { return n.tree.Position(n.span.End) }

// Text returns the source text of the node, comments and white space inside
// it included.
func (n *Node) Text() string { return string(n.tree.src[n.span.Start:n.span.End]) }

// Parent returns the parent node, or nil for the root.
func (n *Node) Parent() *Node { return n.parent }

// FieldName returns the name this node has in its parent, such as "name",
// "value" or "body", or "" when it has none.
func (n *Node) FieldName() string { return n.field }

// Children returns all children in source order: tokens, comments and nodes.
func (n *Node) Children() []*Node { return n.children }

// NamedChildren returns the children that come from named rules, without
// comments. For a "setting" that is the name and the value.
func (n *Node) NamedChildren() []*Node {
	var out []*Node
	for _, c := range n.children {
		if c.named && c.kind != "comment" {
			out = append(out, c)
		}
	}
	return out
}

// Field returns the first child with the given field name, or nil.
func (n *Node) Field(name string) *Node {
	for _, c := range n.children {
		if c.field == name {
			return c
		}
	}
	return nil
}

// Fields returns every child with the given field name, in source order.
func (n *Node) Fields(name string) []*Node {
	var out []*Node
	for _, c := range n.children {
		if c.field == name {
			out = append(out, c)
		}
	}
	return out
}

// Walk visits the node and its descendants in source order. If fn returns
// false, Walk skips that node's children.
func (n *Node) Walk(fn func(*Node) bool) {
	if !fn(n) {
		return
	}
	for _, c := range n.children {
		c.Walk(fn)
	}
}
