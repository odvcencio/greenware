package datapkg

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf8"
)

// MaxFileBytes is the largest file Load reads. It equals the browser worker's
// per-file cap, so a package that builds natively also builds in the worker.
const MaxFileBytes = 2 << 20

const maxDepth = 32

type nodeKind uint8

const (
	nObject nodeKind = iota
	nArray
	nString
	nNumber
	nBool
	nNull
)

func (k nodeKind) String() string {
	switch k {
	case nObject:
		return "an object"
	case nArray:
		return "an array"
	case nString:
		return "a string"
	case nNumber:
		return "a number"
	case nBool:
		return "a boolean"
	}
	return "null"
}

// node is one parsed JSON value. For a string s holds the decoded text; for a
// number and for true, false and null it holds the token as written.
type node struct {
	kind    nodeKind
	s       string
	members []member
	elems   []*node
	off     int
}

type member struct {
	key    string
	val    *node
	keyOff int
}

func (n *node) get(key string) *node {
	for i := range n.members {
		if n.members[i].key == key {
			return n.members[i].val
		}
	}
	return nil
}

// parser is a strict JSON reader: it rejects duplicate keys (after escape
// decoding), invalid UTF-8, lone surrogates, a byte order mark, trailing text
// and nesting deeper than maxDepth.
type parser struct {
	b    []byte
	i    int
	file string
}

type syntaxError struct {
	off int
	msg string
}

func parseJSON(file string, b []byte) (n *node, err *Error) {
	if len(b) > MaxFileBytes {
		return nil, errf(KindData, file, "", "the file is %d bytes; the limit is %d", len(b), MaxFileBytes)
	}
	if bytes.HasPrefix(b, []byte("\xef\xbb\xbf")) {
		return nil, errf(KindData, file, "", "the file starts with a byte order mark; write plain UTF-8")
	}
	if !utf8.Valid(b) {
		return nil, errf(KindData, file, "", "the file is not valid UTF-8")
	}
	p := &parser{b: b, file: file}
	defer func() {
		if r := recover(); r != nil {
			se, ok := r.(syntaxError)
			if !ok {
				panic(r)
			}
			n, err = nil, p.at(se.off, se.msg)
		}
	}()
	p.ws()
	n = p.value(0)
	p.ws()
	if p.i < len(p.b) {
		p.fail(p.i, "unexpected text after the JSON value")
	}
	return n, nil
}

func (p *parser) at(off int, msg string) *Error {
	line, col := 1, 1
	for _, c := range p.b[:off] {
		if c == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return &Error{Kind: KindData, File: p.file, Line: line, Col: col, Msg: msg}
}

func (p *parser) fail(off int, format string, args ...any) {
	panic(syntaxError{off, fmt.Sprintf(format, args...)})
}

func (p *parser) ws() {
	for p.i < len(p.b) {
		switch p.b[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) value(depth int) *node {
	if depth > maxDepth {
		p.fail(p.i, "the JSON nests deeper than %d levels", maxDepth)
	}
	if p.i >= len(p.b) {
		p.fail(p.i, "the JSON ends before a value")
	}
	start := p.i
	switch c := p.b[p.i]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"':
		s := p.str()
		return &node{kind: nString, s: s, off: start}
	case c == '-' || (c >= '0' && c <= '9'):
		return &node{kind: nNumber, s: p.number(), off: start}
	case c == 't':
		p.lit("true")
		return &node{kind: nBool, s: "true", off: start}
	case c == 'f':
		p.lit("false")
		return &node{kind: nBool, s: "false", off: start}
	case c == 'n':
		p.lit("null")
		return &node{kind: nNull, s: "null", off: start}
	}
	p.fail(p.i, "unexpected character %q", rune(p.b[p.i]))
	return nil
}

func (p *parser) lit(w string) {
	if !bytes.HasPrefix(p.b[p.i:], []byte(w)) {
		p.fail(p.i, "expected %s", w)
	}
	p.i += len(w)
}

func (p *parser) object(depth int) *node {
	n := &node{kind: nObject, off: p.i}
	p.i++
	p.ws()
	if p.i < len(p.b) && p.b[p.i] == '}' {
		p.i++
		return n
	}
	seen := map[string]bool{}
	for {
		p.ws()
		if p.i >= len(p.b) || p.b[p.i] != '"' {
			p.fail(p.i, "expected a string key")
		}
		keyOff := p.i
		k := p.str()
		if seen[k] {
			p.fail(keyOff, "duplicate key %q", k)
		}
		seen[k] = true
		p.ws()
		if p.i >= len(p.b) || p.b[p.i] != ':' {
			p.fail(p.i, "expected ':' after the key %q", k)
		}
		p.i++
		p.ws()
		v := p.value(depth + 1)
		n.members = append(n.members, member{k, v, keyOff})
		p.ws()
		if p.i >= len(p.b) {
			p.fail(p.i, "the object is not closed")
		}
		switch p.b[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			return n
		default:
			p.fail(p.i, "expected ',' or '}'")
		}
	}
}

func (p *parser) array(depth int) *node {
	n := &node{kind: nArray, off: p.i}
	p.i++
	p.ws()
	if p.i < len(p.b) && p.b[p.i] == ']' {
		p.i++
		return n
	}
	for {
		p.ws()
		n.elems = append(n.elems, p.value(depth+1))
		p.ws()
		if p.i >= len(p.b) {
			p.fail(p.i, "the array is not closed")
		}
		switch p.b[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			return n
		default:
			p.fail(p.i, "expected ',' or ']'")
		}
	}
}

func hex4(b []byte) (rune, bool) {
	if len(b) < 4 {
		return 0, false
	}
	var r rune
	for _, c := range b[:4] {
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}

func (p *parser) str() string {
	p.i++ // opening quote
	var out []byte
	for {
		if p.i >= len(p.b) {
			p.fail(p.i, "the string is not closed")
		}
		c := p.b[p.i]
		switch {
		case c == '"':
			p.i++
			return string(out)
		case c < 0x20:
			p.fail(p.i, "a raw control character is not allowed in a string")
		case c == '\\':
			p.i++
			if p.i >= len(p.b) {
				p.fail(p.i, "the string is not closed")
			}
			e := p.b[p.i]
			p.i++
			switch e {
			case '"', '\\', '/':
				out = append(out, e)
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'u':
				r, ok := hex4(p.b[p.i:])
				if !ok {
					p.fail(p.i-2, "a \\u escape needs four hex digits")
				}
				p.i += 4
				if r >= 0xD800 && r < 0xDC00 {
					if p.i+1 < len(p.b) && p.b[p.i] == '\\' && p.b[p.i+1] == 'u' {
						if lo, ok := hex4(p.b[p.i+2:]); ok && lo >= 0xDC00 && lo < 0xE000 {
							p.i += 6
							r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
							out = utf8.AppendRune(out, r)
							continue
						}
					}
					p.fail(p.i-6, "a high surrogate escape has no low surrogate after it")
				}
				if r >= 0xDC00 && r < 0xE000 {
					p.fail(p.i-6, "a low surrogate escape has no high surrogate before it")
				}
				out = utf8.AppendRune(out, r)
			default:
				p.fail(p.i-2, "unknown escape \\%c", rune(e))
			}
		default:
			out = append(out, c)
			p.i++
		}
	}
}

// number reads a token of the JSON number grammar and returns it as written.
func (p *parser) number() string {
	start := p.i
	if p.b[p.i] == '-' {
		p.i++
	}
	digits := func() int {
		n := 0
		for p.i < len(p.b) && p.b[p.i] >= '0' && p.b[p.i] <= '9' {
			p.i++
			n++
		}
		return n
	}
	if p.i < len(p.b) && p.b[p.i] == '0' {
		p.i++
	} else if digits() == 0 {
		p.fail(start, "a number needs digits")
	}
	if p.i < len(p.b) && p.b[p.i] == '.' {
		p.i++
		if digits() == 0 {
			p.fail(start, "a number needs digits after the decimal point")
		}
	}
	if p.i < len(p.b) && (p.b[p.i] == 'e' || p.b[p.i] == 'E') {
		p.i++
		if p.i < len(p.b) && (p.b[p.i] == '+' || p.b[p.i] == '-') {
			p.i++
		}
		if digits() == 0 {
			p.fail(start, "a number needs digits in its exponent")
		}
	}
	return string(p.b[start:p.i])
}

// writeCanonical writes n in canonical form: object keys sorted by their
// UTF-8 bytes, no whitespace, numbers and literals as written, strings with
// only the escapes that are required.
func writeCanonical(buf *bytes.Buffer, n *node) {
	switch n.kind {
	case nObject:
		idx := make([]int, len(n.members))
		for i := range idx {
			idx[i] = i
		}
		sort.Slice(idx, func(a, b int) bool { return n.members[idx[a]].key < n.members[idx[b]].key })
		buf.WriteByte('{')
		for j, i := range idx {
			if j > 0 {
				buf.WriteByte(',')
			}
			writeString(buf, n.members[i].key)
			buf.WriteByte(':')
			writeCanonical(buf, n.members[i].val)
		}
		buf.WriteByte('}')
	case nArray:
		buf.WriteByte('[')
		for i, e := range n.elems {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonical(buf, e)
		}
		buf.WriteByte(']')
	case nString:
		writeString(buf, n.s)
	default:
		buf.WriteString(n.s)
	}
}

// writeString writes a JSON string. Only the quote, the backslash and the
// bytes below 0x20 are escaped: the quote as \", the backslash as \\ and a
// control byte as \u00xx with lower-case hex digits. Every other character is
// written as UTF-8, so "<", ">", "&" and U+2028 stay as they are.
func writeString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			buf.WriteString(`\"`)
		case c == '\\':
			buf.WriteString(`\\`)
		case c < 0x20:
			buf.WriteString(`\u00`)
			buf.WriteByte("0123456789abcdef"[c>>4])
			buf.WriteByte("0123456789abcdef"[c&15])
		default:
			buf.WriteByte(c)
		}
	}
	buf.WriteByte('"')
}

// Canonical returns the canonical form of a JSON document: object keys sorted
// by UTF-8 bytes, no insignificant whitespace, numbers as written, strings
// escaped as little as possible (see writeString). A document with a
// duplicate key or invalid text is an error. The file name is used in the
// error only.
func Canonical(file string, b []byte) ([]byte, error) {
	n, err := parseJSON(file, b)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	writeCanonical(&buf, n)
	return buf.Bytes(), nil
}

// quote is strconv.Quote for messages.
func quote(s string) string { return strconv.Quote(s) }
