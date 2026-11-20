package lang

import (
	"bytes"
	"fmt"
)

// inchHint finds a `4 in` written with a space inside a syntax error. `in` is
// the range word (`param p : length = 4m in 2m..5m`) and also the inch unit
// when it touches its number (`4in`), so a spaced `in` after a bare number
// that no `..` follows on the line is almost surely an inch written wrongly.
// It returns the span of the `in` and the number as the author wrote it.
func (t *Tree) inchHint(n *Node) (Span, string, bool) {
	var found bool
	var span Span
	var num string
	var walk func(n *Node)
	walk = func(n *Node) {
		if found {
			return
		}
		if len(n.children) == 0 {
			if n.span.Len() == 2 && string(t.src[n.span.Start:n.span.End]) == "in" {
				if s, ok := t.spacedInch(n.span); ok {
					found, span, num = true, n.span, s
				}
			}
			return
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(n)
	return span, num, found
}

// spacedInch reports whether the `in` at in follows a bare number and spaces
// with no `..` after it on its line, and returns the number's text.
func (t *Tree) spacedInch(in Span) (string, bool) {
	end := in.End
	if end < len(t.src) && isWordByte(t.src[end]) {
		return "", false // `inner`, `index`: not the word `in`
	}
	rest := t.src[end:]
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	if bytes.Contains(rest, []byte("..")) {
		return "", false // `in lo..hi` is the range
	}
	i := in.Start
	gap := 0
	for i > 0 && (t.src[i-1] == ' ' || t.src[i-1] == '\t') {
		i--
		gap++
	}
	if gap == 0 {
		return "", false
	}
	j := i
	for j > 0 && t.src[j-1] >= '0' && t.src[j-1] <= '9' {
		j--
	}
	if j == i {
		return "", false // no digits: `4m in`, a name, a bracket
	}
	if j > 1 && t.src[j-1] == '.' && t.src[j-2] >= '0' && t.src[j-2] <= '9' {
		j -= 2
		for j > 0 && t.src[j-1] >= '0' && t.src[j-1] <= '9' {
			j--
		}
	}
	if j > 0 && (isWordByte(t.src[j-1]) || t.src[j-1] == '.') {
		return "", false // part of a name or a longer token
	}
	return string(t.src[j:i]), true
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// inchMessage is the text of the diagnostic for a spaced inch.
func inchMessage(num string) string {
	return fmt.Sprintf("unexpected \"in\": write the inch unit without a space: %sin", num)
}
