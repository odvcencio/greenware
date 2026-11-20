package tools

import (
	"fmt"
	"strings"
)

// unifiedDiff returns a unified diff of two texts with three lines of
// context, or "" when they are equal. The diff is line based: it trims the
// common head and tail, then finds a longest common subsequence of the rest.
func unifiedDiff(oldName, newName string, oldText, newText []byte) string {
	if string(oldText) == string(newText) {
		return ""
	}
	a, b := splitLines(string(oldText)), splitLines(string(newText))
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	midA, midB := a[head:len(a)-tail], b[head:len(b)-tail]

	// Edit script over the middle: ' ' keep, '-' delete, '+' insert.
	type op struct {
		kind byte
		text string
	}
	var script []op
	for _, l := range a[:head] {
		script = append(script, op{' ', l})
	}
	n, m := len(midA), len(midB)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if midA[i] == midB[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && midA[i] == midB[j]:
			script = append(script, op{' ', midA[i]})
			i++
			j++
		case i < n && (j >= m || lcs[i+1][j] >= lcs[i][j+1]):
			script = append(script, op{'-', midA[i]})
			i++
		default:
			script = append(script, op{'+', midB[j]})
			j++
		}
	}
	for _, l := range a[len(a)-tail:] {
		script = append(script, op{' ', l})
	}

	const context = 3
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", oldName, newName)
	// Line numbers (1-based) of each script entry in old and new.
	oldNo, newNo := make([]int, len(script)+1), make([]int, len(script)+1)
	o, nw := 1, 1
	for k, e := range script {
		oldNo[k], newNo[k] = o, nw
		if e.kind != '+' {
			o++
		}
		if e.kind != '-' {
			nw++
		}
	}
	oldNo[len(script)], newNo[len(script)] = o, nw
	k := 0
	for k < len(script) {
		if script[k].kind == ' ' {
			k++
			continue
		}
		start := k - context
		if start < 0 {
			start = 0
		}
		// Extend the hunk while changes are within 2*context lines.
		end := k
		lastChange := k
		for end < len(script) {
			if script[end].kind != ' ' {
				lastChange = end
			}
			if end-lastChange > 2*context {
				break
			}
			end++
		}
		stop := lastChange + context + 1
		if stop > len(script) {
			stop = len(script)
		}
		oldCount, newCount := 0, 0
		for _, e := range script[start:stop] {
			if e.kind != '+' {
				oldCount++
			}
			if e.kind != '-' {
				newCount++
			}
		}
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", hunkRange(oldNo[start], oldCount), hunkRange(newNo[start], newCount))
		for _, e := range script[start:stop] {
			out.WriteByte(e.kind)
			out.WriteString(strings.TrimSuffix(e.text, "\n"))
			out.WriteByte('\n')
			if !strings.HasSuffix(e.text, "\n") {
				out.WriteString("\\ No newline at end of file\n")
			}
		}
		k = stop
	}
	return out.String()
}

func hunkRange(start, count int) string {
	if count == 1 {
		return fmt.Sprint(start)
	}
	if count == 0 {
		start--
	}
	return fmt.Sprintf("%d,%d", start, count)
}

// splitLines splits text into lines that keep their newline.
func splitLines(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}
