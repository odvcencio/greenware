package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

func frame(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

// FuzzFraming feeds arbitrary bytes as the input stream. The server must end,
// and no handler may panic (a recovered panic is logged, and fails the test).
func FuzzFraming(f *testing.F) {
	f.Add([]byte(frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)))
	f.Add([]byte("Content-Length: 5\r\n\r\n{}"))
	f.Add([]byte("Content-Length: -1\r\n\r\n"))
	f.Add([]byte("Content-Length 5\r\n\r\n"))
	f.Add([]byte("\r\n\r\n"))
	f.Add([]byte(frame(`[]`) + frame(`null`) + frame(`{"id":[1],"method":3}`)))
	f.Fuzz(func(t *testing.T, data []byte) {
		var log bytes.Buffer
		Serve(bytes.NewReader(data), io.Discard, &log)
		if strings.Contains(log.String(), "panic in") {
			t.Fatalf("a handler panicked:\n%s", log.String())
		}
	})
}

var fuzzMethods = []string{
	"initialize", "shutdown", "textDocument/didOpen", "textDocument/didChange", "textDocument/didClose",
	"textDocument/hover", "textDocument/definition", "textDocument/references", "textDocument/documentSymbol",
	"textDocument/completion", "textDocument/formatting", "textDocument/codeAction", "textDocument/rename", "exit",
}

const fuzzSource = "greenware 1\n\nasset a { root = a }\n\nparam h : length = 1m in 0m..2m\nlet k = h * 2\n\npart a {\n  cylinder c {\n    radius = k\n    raduis = 1m\n  }\n}\n"

// FuzzRequests sends one request or notification with arbitrary params to a
// session that has a document open.
func FuzzRequests(f *testing.F) {
	pos := `{"textDocument":{"uri":"file:///f.gw"},"position":{"line":9,"character":12}}`
	f.Add(uint8(5), pos)
	f.Add(uint8(7), `{"textDocument":{"uri":"file:///f.gw"},"position":{"line":-5,"character":99999999999}}`)
	f.Add(uint8(2), `{"textDocument":{"uri":"file:///f.gw","text":"é😀"}}`)
	f.Add(uint8(3), `{"textDocument":{"uri":"file:///f.gw","version":2},"contentChanges":[{"range":{"start":{"line":9,"character":4},"end":{"line":3,"character":2}},"text":"x"},{"text":"{"}]}`)
	f.Add(uint8(11), `{"textDocument":{"uri":"file:///f.gw"},"range":{"start":{"line":0,"character":0},"end":{"line":99,"character":0}}}`)
	f.Add(uint8(9), pos)
	f.Add(uint8(10), `{"textDocument":{"uri":"file:///f.gw"}}`)
	f.Fuzz(func(t *testing.T, which uint8, params string) {
		method := fuzzMethods[int(which)%len(fuzzMethods)]
		var in strings.Builder
		in.WriteString(frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
		open, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
			"textDocument": map[string]any{"uri": "file:///f.gw", "version": 1, "text": fuzzSource}}})
		in.WriteString(frame(string(open)))
		if json.Valid([]byte(params)) {
			in.WriteString(frame(fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":%q,"params":%s}`, method, params)))
			in.WriteString(frame(fmt.Sprintf(`{"jsonrpc":"2.0","method":%q,"params":%s}`, method, params)))
		} else {
			in.WriteString(frame(params))
		}
		in.WriteString(frame(`{"jsonrpc":"2.0","id":3,"method":"textDocument/documentSymbol","params":{"textDocument":{"uri":"file:///f.gw"}}}`))
		var log bytes.Buffer
		Serve(strings.NewReader(in.String()), io.Discard, &log)
		if strings.Contains(log.String(), "panic in") {
			t.Fatalf("a handler panicked:\n%s", log.String())
		}
	})
}

// FuzzEdits applies one edit of arbitrary range and text, then checks that the
// incremental result equals a fresh parse and that every feature answers at
// the edit position.
func FuzzEdits(f *testing.F) {
	f.Add(9, 4, 9, 10, "radius")
	f.Add(0, 0, 100, 100, "")
	f.Add(2, 8, 2, 9, "😀\n{")
	f.Add(-1, -1, 3, 3, "\r\n")
	f.Fuzz(func(t *testing.T, sl, sc, el, ec int, text string) {
		d := newDocument("file:///f.gw", 1, []byte(fuzzSource))
		d.apply([]change{{Range: &Range{Start: Position{sl, sc}, End: Position{el, ec}}, Text: text}})
		fresh := newDocument("file:///f.gw", 1, d.text)
		if fmt.Sprint(d.diags) != fmt.Sprint(fresh.diags) {
			t.Fatalf("incremental diagnostics differ from a fresh parse\nsource %q\n inc: %v\nnew: %v", d.text, d.diags, fresh.diags)
		}
		off := d.offset(Position{sl, sc})
		d.hoverAt(off)
		d.complete(off)
		d.outline()
		d.targetAt(off)
		if out, err := formatForTest(d.text); err == nil {
			d.lineDiff(out)
		}
		for _, x := range d.diagnostics() {
			if x.Range.End.Line < x.Range.Start.Line {
				t.Fatalf("inverted range %v", x.Range)
			}
		}
	})
}
