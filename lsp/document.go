package lsp

import (
	"net/url"
	"path/filepath"
	"sort"
	"unicode/utf8"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/resolve"
)

// document is one open file: its text, its tree and what the front end found.
//
// The text is the tree's own source, which nothing writes to. Analysis runs
// on every change, so the diagnostics always match the text. The symbol index
// and the node table are built on first use after a change and kept.
type document struct {
	uri     string
	path    string
	version int
	text    []byte
	lines   []int // byte offset of the start of each line
	tree    *lang.Tree
	proj    *ast.Project

	file     *ast.File
	diags    []diag.Diagnostic
	resolved *resolve.Resolved

	ix    *index
	nodes map[string]*resolve.Node
}

func newDocument(uri string, version int, text []byte) *document {
	d := &document{uri: uri, version: version, path: uriPath(uri)}
	if d.path != "" {
		if p, err := ast.OpenProject(d.path, ""); err == nil {
			d.proj = p
		}
	}
	d.setTree(lang.Parse(text))
	return d
}

// uriPath returns the file path of a file: URI, or "" for any other URI.
func uriPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	p := u.Path
	if len(p) > 2 && p[0] == '/' && p[2] == ':' { // file:///C:/x on Windows
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

func (d *document) setTree(t *lang.Tree) {
	d.tree = t
	d.text = t.Source()
	d.lines = d.lines[:0]
	d.lines = append(d.lines, 0)
	for i := 0; i < len(d.text); i++ {
		if d.text[i] == '\n' {
			d.lines = append(d.lines, i+1)
		}
	}
	d.analyze()
}

// change is one content change of a didChange notification.
type change struct {
	Range *Range `json:"range"`
	Text  string `json:"text"`
}

// apply applies the changes in order. Each range is read against the text as
// it stands after the changes before it. A range change goes through
// Tree.Reparse; if that fails the whole text is parsed again. Analysis runs
// once, after the last change.
func (d *document) apply(changes []change) {
	for i, c := range changes {
		last := i == len(changes)-1
		if c.Range == nil {
			d.install(lang.Parse([]byte(c.Text)), last)
			continue
		}
		start := d.offset(c.Range.Start)
		end := d.offset(c.Range.End)
		if end < start {
			start, end = end, start
		}
		next, err := d.tree.Reparse(lang.Edit{Start: start, End: end, Text: []byte(c.Text)})
		if err != nil {
			text := make([]byte, 0, len(d.text)-(end-start)+len(c.Text))
			text = append(text, d.text[:start]...)
			text = append(text, c.Text...)
			text = append(text, d.text[end:]...)
			next = lang.Parse(text)
		}
		d.install(next, last)
	}
}

// install makes t the document's tree. Only the last tree of a batch is
// analysed.
func (d *document) install(t *lang.Tree, analyse bool) {
	if analyse {
		d.setTree(t)
		return
	}
	d.tree = t
	d.text = t.Source()
	d.lines = d.lines[:0]
	d.lines = append(d.lines, 0)
	for i := 0; i < len(d.text); i++ {
		if d.text[i] == '\n' {
			d.lines = append(d.lines, i+1)
		}
	}
}

// analyze runs the front end on the current tree: syntax errors and lowering
// diagnostics, then the evaluator's. The index and node table are dropped and
// rebuilt on demand.
func (d *document) analyze() {
	d.ix, d.nodes, d.file, d.resolved, d.diags = nil, nil, nil, nil, nil
	f, ds := ast.LowerWith(d.tree, ast.Options{Project: d.proj})
	d.file = f
	if !f.Broken {
		r, rds := resolve.Evaluate(f, nil)
		d.resolved = r
		ds = append(ds, rds...)
	}
	d.diags = diag.Dedup(ds)
}

// lineEnd returns the byte offset where line's text ends, before its line
// break.
func (d *document) lineEnd(line int) int {
	end := len(d.text)
	if line+1 < len(d.lines) {
		end = d.lines[line+1] - 1 // the "\n"
	}
	if end > d.lines[line] && d.text[end-1] == '\r' {
		end--
	}
	if end < d.lines[line] {
		end = d.lines[line]
	}
	return end
}

// offset converts a position to a byte offset. A line past the end gives the
// end of the text; a character past the end of its line gives the line's end.
// A character inside a surrogate pair rounds up to the end of that character.
func (d *document) offset(p Position) int {
	if p.Line < 0 {
		return 0
	}
	if p.Line >= len(d.lines) {
		return len(d.text)
	}
	i, end := d.lines[p.Line], d.lineEnd(p.Line)
	units := 0
	for i < end && units < p.Character {
		r, size := utf8.DecodeRune(d.text[i:end])
		units += utf16Len(r)
		i += size
	}
	return i
}

// position converts a byte offset to a position. An offset inside a UTF-8
// sequence counts the whole sequence.
func (d *document) position(off int) Position {
	if off < 0 {
		off = 0
	}
	if off > len(d.text) {
		off = len(d.text)
	}
	line := sort.Search(len(d.lines), func(i int) bool { return d.lines[i] > off }) - 1
	if line < 0 {
		line = 0
	}
	units := 0
	for i := d.lines[line]; i < off; {
		r, size := utf8.DecodeRune(d.text[i:])
		units += utf16Len(r)
		i += size
	}
	return Position{Line: line, Character: units}
}

func utf16Len(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

func (d *document) rangeOf(sp diag.Span) Range {
	if sp.End < sp.Start {
		sp.End = sp.Start
	}
	return Range{Start: d.position(sp.Start), End: d.position(sp.End)}
}

func (d *document) location(sp diag.Span) Location {
	return Location{URI: d.uri, Range: d.rangeOf(sp)}
}

func (d *document) severity(s diag.Severity) int {
	switch s {
	case diag.Warning:
		return sevWarning
	case diag.Info:
		return sevInfo
	}
	return sevError
}

// lspDiagnostic converts one front-end diagnostic.
func (d *document) lspDiagnostic(x diag.Diagnostic) Diagnostic {
	out := Diagnostic{
		Range:    d.rangeOf(x.Span),
		Severity: d.severity(x.Severity),
		Code:     x.Code,
		Source:   "greenware",
		Message:  x.Message,
	}
	for _, r := range x.Related {
		out.RelatedInformation = append(out.RelatedInformation, relatedInformation{Location: d.location(r.Span), Message: r.Note})
	}
	return out
}

func (d *document) diagnostics() []Diagnostic {
	out := make([]Diagnostic, 0, len(d.diags))
	for _, x := range d.diags {
		out = append(out, d.lspDiagnostic(x))
	}
	return out
}

// wordAt returns the span of the identifier characters around offset.
func (d *document) wordAt(off int) (diag.Span, string) {
	if off > len(d.text) {
		off = len(d.text)
	}
	s, e := off, off
	for s > 0 && isWordByte(d.text[s-1]) {
		s--
	}
	for e < len(d.text) && isWordByte(d.text[e]) {
		e++
	}
	return diag.Span{Start: s, End: e}, string(d.text[s:e])
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

// lineText returns the text of the line that holds offset, up to offset.
func (d *document) linePrefix(off int) string {
	p := d.position(off)
	return string(d.text[d.lines[p.Line]:min(off, len(d.text))])
}
