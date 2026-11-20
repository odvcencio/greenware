package lsp

import (
	"encoding/json"
	"fmt"
	"strings"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// LSP completion item kinds.
const (
	itemFunction   = 3
	itemField      = 5
	itemVariable   = 6
	itemClass      = 7
	itemKeyword    = 14
	itemEnumMember = 20
	itemConstant   = 21
)

// topKeywords start a declaration at the top level of a file.
var topKeywords = []string{
	"asset", "meta", "import", "param", "let", "check", "material", "texture",
	"painter", "template", "table", "mount", "plug", "part", "lod", "collision",
	"budget", "output", "skeleton", "skin", "morph", "clip",
}

// scanned is what a lexical scan of the text before the cursor found.
type scanned struct {
	// stack holds the kind of each open block, outermost first.
	stack []string
	// segment is the text since the last brace or line break, up to the cursor.
	segment string
}

// scan reads the text before off and finds the blocks that are open there. It
// does not parse: it follows braces, skips comments and strings, and names a
// block by the words that come before its "{" on the same line. That keeps
// completion working inside text that does not parse yet.
func (d *document) scan(off int) scanned {
	var sc scanned
	text := d.text[:min(off, len(d.text))]
	var words []string // top-level words of the current line, since the last brace
	parens := 0
	hasEq := false
	segStart := 0
	endLine := func(i int) {
		words, parens, hasEq = nil, 0, false
		segStart = i
	}
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case c == '"':
			i++
			for i < len(text) && text[i] != '"' && text[i] != '\n' {
				if text[i] == '\\' {
					i++
				}
				i++
			}
			if i < len(text) && text[i] == '"' {
				i++
			}
		case c == '\n':
			i++
			endLine(i)
		case c == '{':
			sc.stack = append(sc.stack, headerKind(words, hasEq))
			i++
			endLine(i)
		case c == '}':
			if len(sc.stack) > 0 {
				sc.stack = sc.stack[:len(sc.stack)-1]
			}
			i++
			endLine(i)
		case c == '(' || c == '[':
			parens++
			i++
		case c == ')' || c == ']':
			if parens > 0 {
				parens--
			}
			i++
		case c == '=' && parens == 0:
			hasEq = true
			i++
		case isWordByte(c) && !(c >= '0' && c <= '9'):
			j := i
			for j < len(text) && isWordByte(text[j]) {
				j++
			}
			if parens == 0 {
				words = append(words, string(text[i:j]))
			}
			i = j
		default:
			i++
		}
	}
	sc.segment = string(text[segStart:])
	return sc
}

// headerKind names the block that a "{" opens, from the words before it.
func headerKind(words []string, hasEq bool) string {
	if len(words) == 0 {
		return ""
	}
	switch words[0] {
	case "material", "texture", "fallback":
		return words[len(words)-1]
	}
	return words[0]
}

// kindFields collects what a block kind accepts, across the registry entries
// that share its name.
func kindFields(name string) (fields []registry.Field, rows []string, blocks []string, found bool) {
	for _, k := range registry.Kinds() {
		if k.Name != name {
			continue
		}
		found = true
		fields = append(fields, k.Fields...)
		rows = append(rows, k.RowNames()...)
		blocks = append(blocks, k.Blocks...)
		if k.Contains {
			blocks = append(blocks, registry.KindNames(registry.CtxPart)...)
			blocks = append(blocks, "when", "let")
		}
	}
	switch name {
	case "template", "painter", "when":
		blocks = append(blocks, registry.KindNames(registry.CtxPart)...)
		blocks = append(blocks, "when", "let")
		found = true
	case "collision":
		blocks = append(blocks, registry.KindNames(registry.CtxCollision)...)
		blocks = append(blocks, "mirror")
		found = true
	case "skeleton", "skin", "morph", "clip", "joint":
		blocks = append(blocks, registry.KindNames(registry.CtxRig)...)
		found = true
	case "texture", "atlas":
		blocks = append(blocks, registry.KindNames(registry.CtxAtlas)...)
	}
	return
}

func (s *Server) completion(params json.RawMessage) (any, *respError) {
	var p textDocumentPosition
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	d := s.doc(p.TextDocument.URI)
	if d == nil {
		return CompletionList{Items: []CompletionItem{}}, nil
	}
	return d.complete(d.offset(p.Position)), nil
}

type completer struct {
	d      *document
	prefix diag.Span
	word   string
	items  []CompletionItem
	seen   map[string]bool
}

func (c *completer) add(it CompletionItem, insert string) {
	key := fmt.Sprintf("%d:%s", it.Kind, it.Label)
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	if insert == "" {
		insert = it.Label
	}
	it.TextEdit = &TextEdit{Range: c.d.rangeOf(c.prefix), NewText: insert}
	it.FilterText = it.Label
	it.SortText = fmt.Sprintf("%04d", len(c.items))
	c.items = append(c.items, it)
}

func doc(text string) *MarkupContent {
	if text == "" {
		return nil
	}
	return &MarkupContent{Kind: "markdown", Value: text}
}

func (d *document) complete(off int) CompletionList {
	if off > len(d.text) {
		off = len(d.text)
	}
	// The word being typed ends at the cursor.
	start := off
	for start > 0 && isWordByte(d.text[start-1]) {
		start--
	}
	c := &completer{d: d, prefix: diag.Span{Start: start, End: off}, word: string(d.text[start:off]), seen: map[string]bool{}}
	sc := d.scan(start)
	before := strings.TrimRight(sc.segment, " \t")

	switch {
	case len(sc.stack) == 0 && strings.TrimSpace(before) == "":
		for _, k := range topKeywords {
			c.add(CompletionItem{Label: k, Kind: itemKeyword}, "")
		}
	case len(sc.stack) == 0 && strings.HasSuffix(before, ":"):
		for _, n := range registry.ParamTypeNames {
			c.add(CompletionItem{Label: n, Kind: itemEnumMember, Detail: "parameter type"}, "")
		}
	case len(sc.stack) == 0 && strings.HasSuffix(before, "=") && (strings.HasPrefix(before, "material") || strings.HasPrefix(before, "texture")):
		names := []string{"pbr", "selena"}
		if strings.HasPrefix(before, "texture") {
			names = []string{"atlas"}
		}
		for _, n := range names {
			c.add(CompletionItem{Label: n, Kind: itemClass}, "")
		}
	case valuePosition(before):
		c.values(before, sc, off)
	case len(sc.stack) > 0:
		c.items0(sc.stack[len(sc.stack)-1])
	default:
		c.values(before, sc, off)
	}
	if c.items == nil {
		c.items = []CompletionItem{}
	}
	return CompletionList{Items: c.items}
}

// valuePosition reports whether the text before the cursor ends where a value
// goes: after "=", an operator, an opening bracket or a comma.
func valuePosition(before string) bool {
	if before == "" {
		return false
	}
	for _, suf := range []string{"=", "(", "[", ",", "+", "-", "*", "/", "..", "->", "<", ">", "&&", "||", "!", " in", " by", " then", " else", " if"} {
		if strings.HasSuffix(before, suf) {
			return true
		}
	}
	return strings.HasPrefix(before, "when") && !strings.Contains(before, "{")
}

// items0 offers what a block of the given kind holds at statement start.
func (c *completer) items0(kind string) {
	fields, rows, blocks, found := kindFields(kind)
	if !found {
		return
	}
	for i := range fields {
		f := &fields[i]
		c.add(CompletionItem{Label: f.Name, Kind: itemField, Detail: f.Type.Name(), Documentation: doc(fieldDoc(f))}, f.Name+" = ")
	}
	for _, r := range rows {
		c.add(CompletionItem{Label: r, Kind: itemField, Detail: "row"}, "")
	}
	seen := map[string]bool{}
	for _, b := range blocks {
		if seen[b] {
			continue
		}
		seen[b] = true
		c.add(CompletionItem{Label: b, Kind: itemClass, Detail: "block"}, "")
	}
}

func fieldDoc(f *registry.Field) string {
	s := f.Doc
	if f.Required {
		s += " Required."
	} else if f.Default != nil {
		s += " Default: `" + f.Default.Format() + "`."
	}
	return strings.TrimSpace(s)
}

// values offers the members of an enum slot, then the names in scope.
func (c *completer) values(before string, sc scanned, off int) {
	if len(sc.stack) > 0 {
		if field := lastSetting(before); field != "" {
			c.slotValues(sc.stack[len(sc.stack)-1], field)
		}
	}
	ix := c.d.index()
	if ix != nil {
		kinds := map[string]int{"variable": itemVariable, "index": itemVariable, "tparam": itemVariable, "let": itemConstant, "param": itemVariable}
		for _, kind := range valueKinds {
			for _, x := range ix.decls {
				if x.Kind != kind || off < x.Scope.Start || off > x.Scope.End || x.Span.Start >= c.prefix.Start && x.Span.End <= c.prefix.End {
					continue
				}
				c.add(CompletionItem{Label: x.Name, Kind: kinds[kind], Detail: x.Kind}, "")
			}
		}
	}
	for _, f := range units.Funcs() {
		c.add(CompletionItem{Label: f.Name, Kind: itemFunction, Detail: f.Signature}, "")
	}
}

// slotValues offers the legal values of one field: enum members, on and off,
// axes, and the declared names of a reference slot.
func (c *completer) slotValues(kind, field string) {
	var f *registry.Field
	for _, k := range registry.Kinds() {
		if k.Name == kind {
			if ff, ok := k.Field(field); ok {
				f = ff
				break
			}
		}
	}
	if f == nil {
		return
	}
	switch f.Type.Kind {
	case registry.TEnum:
		for _, m := range f.Type.Enum {
			c.add(CompletionItem{Label: m, Kind: itemEnumMember, Detail: kind + "." + field}, "")
		}
	case registry.TSwitch:
		for _, m := range []string{"on", "off"} {
			c.add(CompletionItem{Label: m, Kind: itemEnumMember, Detail: "switch"}, "")
		}
	case registry.TAxis:
		for _, m := range []string{"x", "y", "z", "-x", "-y", "-z"} {
			c.add(CompletionItem{Label: m, Kind: itemEnumMember, Detail: "axis"}, "")
		}
	case registry.TRef, registry.TRefList:
		ix := c.d.index()
		if ix == nil {
			return
		}
		var kinds []string
		switch f.Type.Ns {
		case registry.NsMaterial:
			kinds = []string{"material"}
		case registry.NsGeometry:
			kinds = []string{"part"}
		}
		for _, x := range ix.decls {
			for _, k := range kinds {
				if x.Kind == k {
					c.add(CompletionItem{Label: x.Name, Kind: itemClass, Detail: x.Kind}, "")
				}
			}
		}
	}
}

// lastSetting returns the name of the setting whose value the cursor is in:
// the word before the last "=" of the segment.
func lastSetting(before string) string {
	i := strings.LastIndexByte(before, '=')
	if i < 0 {
		return ""
	}
	j := strings.TrimRight(before[:i], " \t")
	k := len(j)
	for k > 0 && isWordByte(j[k-1]) {
		k--
	}
	return j[k:]
}
