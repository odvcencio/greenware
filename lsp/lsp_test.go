package lsp

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"m31labs.dev/greenware/lang"
)

var update = flag.Bool("update", false, "record the server's replies into testdata/lsp")

func TestTranscripts(t *testing.T) {
	for _, name := range []string{"goalpost", "misspelled", "complete", "incremental", "utf16", "format"} {
		t.Run(name, func(t *testing.T) { runTranscript(t, name, *update) })
	}
}

// ---- behaviour ---------------------------------------------------------------

func readGoalpost(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../examples/goalpost/goalpost.gw")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// applyEdits applies edits (all against the same text) to text.
func applyEdits(t *testing.T, text string, edits []TextEdit) string {
	t.Helper()
	d := newDocument("file:///x.gw", 1, []byte(text))
	type span struct {
		s, e int
		n    string
	}
	var sp []span
	for _, e := range edits {
		sp = append(sp, span{d.offset(e.Range.Start), d.offset(e.Range.End), e.NewText})
	}
	sort.Slice(sp, func(i, j int) bool { return sp[i].s > sp[j].s })
	out := text
	for _, x := range sp {
		out = out[:x.s] + x.n + out[x.e:]
	}
	return out
}

func TestGoalpostHasNoDiagnostics(t *testing.T) {
	c := startServer(t)
	c.initialize()
	if pub := c.open("file:///work/goalpost.gw", readGoalpost(t)); len(pub.Diagnostics) != 0 {
		t.Fatalf("goalpost.gw: %d diagnostics, first %+v", len(pub.Diagnostics), pub.Diagnostics[0])
	}
	if code := c.shutdown(); code != 0 {
		t.Errorf("exit status %d, want 0", code)
	}
}

func TestQuickFixApplies(t *testing.T) {
	src, err := os.ReadFile("../testdata/diag/GW-UNKNOWN-FIELD.gw")
	if err != nil {
		t.Fatal(err)
	}
	uri := "file:///work/f.gw"
	c := startServer(t)
	c.initialize()
	pub := c.open(uri, string(src))
	if len(pub.Diagnostics) != 1 || pub.Diagnostics[0].Code != "GW-UNKNOWN-FIELD" {
		t.Fatalf("diagnostics: %+v", pub.Diagnostics)
	}
	var actions []CodeAction
	c.call("textDocument/codeAction", map[string]any{
		"textDocument": textDocumentIdentifier{URI: uri},
		"range":        pub.Diagnostics[0].Range,
		"context":      map[string]any{"diagnostics": pub.Diagnostics},
	}, &actions)
	if len(actions) != 1 || actions[0].Kind != "quickfix" {
		t.Fatalf("actions: %+v", actions)
	}
	fixed := applyEdits(t, string(src), actions[0].Edit.Changes[uri])
	if strings.Contains(fixed, "raduis") {
		t.Fatalf("the edit left the misspelling:\n%s", fixed)
	}
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": 2},
		"contentChanges": []any{map[string]any{"text": fixed}},
	})
	// radius is now set twice, so the fix swaps one problem for another: the
	// duplicate. The point is that the edit is applicable and well formed.
	pub = c.diagnostics()
	for _, d := range pub.Diagnostics {
		if d.Code == "GW-UNKNOWN-FIELD" {
			t.Errorf("the fix did not clear GW-UNKNOWN-FIELD: %+v", d)
		}
	}
}

func TestCodeActionOnePerAlternative(t *testing.T) {
	// A bare angle argument to sin offers deg and rad as alternative edits.
	src := "greenware 1\n\nasset a { root = a }\n\nlet q = sin(30)\n\npart a {\n  sphere { radius = 1m }\n}\n"
	uri := "file:///u.gw"
	d := newDocument(uri, 1, []byte(src))
	want := 0
	for _, x := range d.diags {
		want += len(x.Edits)
	}
	if want < 2 {
		t.Fatalf("the source carries %d edits in %v", want, d.diags)
	}
	c := startServer(t)
	c.initialize()
	c.open(uri, src)
	var actions []CodeAction
	whole := Range{Start: Position{0, 0}, End: Position{Line: 10000, Character: 0}}
	c.call("textDocument/codeAction", map[string]any{"textDocument": textDocumentIdentifier{URI: uri}, "range": whole, "context": map[string]any{"diagnostics": []any{}}}, &actions)
	if len(actions) != want {
		t.Errorf("%d actions for %d edits", len(actions), want)
	}
	for _, a := range actions {
		if a.IsPreferred {
			t.Errorf("%q is preferred, but there are alternatives", a.Title)
		}
	}
}

func TestHoverOnCrossbarHeight(t *testing.T) {
	c := startServer(t)
	c.initialize()
	uri := "file:///work/goalpost.gw"
	c.open(uri, readGoalpost(t))
	var h Hover
	c.call("textDocument/hover", textDocumentPosition{TextDocument: textDocumentIdentifier{URI: uri}, Position: Position{Line: 30, Character: 8}}, &h)
	for _, want := range []string{"length", "metre", "3.05m", "2.5m..3.5m", "bake"} {
		if !strings.Contains(h.Contents.Value, want) {
			t.Errorf("hover lacks %q:\n%s", want, h.Contents.Value)
		}
	}
	// The runtime parameter says so, and carries its doc.
	var h2 Hover
	line := strings.Split(readGoalpost(t), "\n")
	for i, l := range line {
		if strings.HasPrefix(l, "param pad_color") {
			c.call("textDocument/hover", textDocumentPosition{TextDocument: textDocumentIdentifier{URI: uri}, Position: Position{Line: i, Character: 8}}, &h2)
		}
	}
	for _, want := range []string{"runtime", "The home school's colour on the pad"} {
		if !strings.Contains(h2.Contents.Value, want) {
			t.Errorf("hover on pad_color lacks %q:\n%s", want, h2.Contents.Value)
		}
	}
}

func TestDefinitionOfLet(t *testing.T) {
	c := startServer(t)
	c.initialize()
	uri := "file:///work/goalpost.gw"
	src := readGoalpost(t)
	c.open(uri, src)
	lines := strings.Split(src, "\n")
	var use, decl int
	for i, l := range lines {
		if strings.HasPrefix(l, "let half_span") {
			decl = i
		}
		if strings.Contains(l, "height = 2 * half_span") {
			use = i
		}
	}
	col := strings.Index(lines[use], "half_span") + 2
	var loc Location
	c.call("textDocument/definition", textDocumentPosition{TextDocument: textDocumentIdentifier{URI: uri}, Position: Position{Line: use, Character: col}}, &loc)
	if loc.URI != uri || loc.Range.Start.Line != decl || loc.Range.Start.Character != len("let ") {
		t.Errorf("definition = %+v, want line %d column 4", loc, decl)
	}
	// A name with no declaration answers null.
	res, rerr, _ := c.request("textDocument/definition", textDocumentPosition{TextDocument: textDocumentIdentifier{URI: uri}, Position: Position{Line: 0, Character: 2}})
	if rerr != nil || string(res) != "null" {
		t.Errorf("definition of the edition header: %s %v", res, rerr)
	}
}

func TestDocumentSymbolOfGoalpost(t *testing.T) {
	c := startServer(t)
	c.initialize()
	uri := "file:///work/goalpost.gw"
	c.open(uri, readGoalpost(t))
	var syms []DocumentSymbol
	c.call("textDocument/documentSymbol", map[string]any{"textDocument": textDocumentIdentifier{URI: uri}}, &syms)
	top := map[string]DocumentSymbol{}
	var names []string
	for _, s := range syms {
		top[s.Name] = s
		names = append(names, s.Name)
	}
	for _, want := range []string{"goalpost", "crossbar_height", "pad_color", "paint", "steel", "pad_cover", "bolt", "stadium", "practice"} {
		if _, ok := top[want]; !ok {
			t.Errorf("outline lacks %q; has %v", want, names)
		}
	}
	// The root part holds base, post, crossbar, the mirrored uprights and pad.
	var root DocumentSymbol
	for _, s := range syms {
		if s.Name == "goalpost" && s.Detail == "part" {
			root = s
		}
	}
	var kids []string
	for _, k := range root.Children {
		kids = append(kids, k.Name)
	}
	for _, want := range []string{"base", "post", "crossbar", "upright_l, upright_r", "pad"} {
		if !slices.Contains(kids, want) {
			t.Errorf("root part lacks child %q; has %v", want, kids)
		}
	}
	var check func(s DocumentSymbol)
	check = func(s DocumentSymbol) {
		if less(s.SelectionRange.Start, s.Range.Start) || less(s.Range.End, s.SelectionRange.End) {
			t.Errorf("%s: selection %v is outside range %v", s.Name, s.SelectionRange, s.Range)
		}
		for _, k := range s.Children {
			check(k)
		}
	}
	for _, s := range syms {
		check(s)
	}
}

func less(a, b Position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
}

func TestCompletionInCylinder(t *testing.T) {
	c := startServer(t)
	c.initialize()
	uri := "file:///work/c.gw"
	c.open(uri, "greenware 1\n\nasset a { root = a }\n\npart a {\n  cylinder {\n    \n  }\n}\n")
	var list CompletionList
	c.call("textDocument/completion", textDocumentPosition{TextDocument: textDocumentIdentifier{URI: uri}, Position: Position{Line: 6, Character: 4}}, &list)
	got := map[string]bool{}
	for _, it := range list.Items {
		got[it.Label] = true
	}
	for _, f := range []string{"radius", "height", "axis", "segments", "caps", "at", "rot", "material"} {
		if !got[f] {
			t.Errorf("no completion %q", f)
		}
	}
	if got["sphere"] || got["size"] {
		t.Error("completion offers a field or block that a cylinder does not take")
	}
}

func TestIncrementalMatchesFresh(t *testing.T) {
	src := readGoalpost(t)
	d := newDocument("file:///g.gw", 1, []byte(src))
	edits := []change{
		{Range: &Range{Start: Position{30, 6}, End: Position{30, 21}}, Text: "crossbar_hight"},
		{Range: &Range{Start: Position{40, 0}, End: Position{40, 3}}, Text: "lett"},
		{Range: &Range{Start: Position{40, 0}, End: Position{40, 4}}, Text: "let"},
		{Range: &Range{Start: Position{72, 4}, End: Position{73, 0}}, Text: ""},
	}
	for i, e := range edits {
		d.apply([]change{e})
		fresh := newDocument("file:///g.gw", 1, d.text)
		if fmt.Sprint(d.diags) != fmt.Sprint(fresh.diags) {
			t.Errorf("edit %d: incremental diagnostics differ from a fresh parse\n inc: %v\nnew: %v", i, d.diags, fresh.diags)
		}
	}
}

func TestUTF16Positions(t *testing.T) {
	text := "// größe 😀 pâté\nx = \"😀é\" y\n"
	d := newDocument("file:///u.gw", 1, []byte(text))
	cases := []struct {
		pos  Position
		want string // the text from that position to the end of the line
	}{
		{Position{0, 0}, "// größe 😀 pâté"},
		{Position{0, 9}, "😀 pâté"},
		{Position{0, 11}, " pâté"}, // the emoji is two UTF-16 units
		{Position{0, 12}, "pâté"},
		{Position{1, 5}, "😀é\" y"},
		{Position{1, 7}, "é\" y"},
		{Position{1, 8}, "\" y"},
		{Position{0, 500}, ""}, // past the end of the line: clamped
	}
	for _, c := range cases {
		off := d.offset(c.pos)
		end := d.lineEnd(c.pos.Line)
		if got := text[off:end]; got != c.want {
			t.Errorf("offset(%v): text from there is %q, want %q", c.pos, got, c.want)
		}
		// Converting back gives the position, except where it was clamped.
		if c.pos.Character < 100 {
			if back := d.position(off); back != c.pos {
				t.Errorf("position(offset(%v)) = %v", c.pos, back)
			}
		}
	}
	// A position in the middle of the emoji rounds to the end of it.
	if off := d.offset(Position{0, 10}); text[off:off+1] != " " {
		t.Errorf("middle of a surrogate pair gave offset %d", off)
	}
	// An edit after the emoji on the same line lands on the right bytes.
	d.apply([]change{{Range: &Range{Start: Position{1, 8}, End: Position{1, 9}}, Text: "'"}})
	if got := string(d.text); got != "// größe 😀 pâté\nx = \"😀é' y\n" {
		t.Errorf("after the edit: %q", got)
	}
	// Positions of a diagnostic after non-ASCII text are in UTF-16 units.
	e := newDocument("file:///e.gw", 1, []byte("greenware 1\n\n// größe 😀\nasset a { title = \"😀é\" root = a bogus = 1 }\n"))
	var found bool
	for _, x := range e.diagnostics() {
		if x.Code == "GW-UNKNOWN-FIELD" {
			found = true
			if x.Range.Start != (Position{3, 33}) || x.Range.End != (Position{3, 38}) {
				t.Errorf("range %v, want 3:33-3:38", x.Range)
			}
		}
	}
	if !found {
		t.Error("no GW-UNKNOWN-FIELD")
	}
}

func TestCRLFLines(t *testing.T) {
	d := newDocument("file:///c.gw", 1, []byte("greenware 1\r\nasset a { root = a }\r\n"))
	if got := d.offset(Position{0, 99}); got != len("greenware 1") {
		t.Errorf("a character past a CRLF line end gives %d, want %d", got, len("greenware 1"))
	}
}

func TestFormattingMakesNoEditWhenFormatted(t *testing.T) {
	c := startServer(t)
	c.initialize()
	uri := "file:///work/f.gw"
	c.open(uri, "greenware 1\n\nasset a { root = a }\n\npart a {\n  sphere { radius = 1m }\n}\n")
	var edits []TextEdit
	c.call("textDocument/formatting", map[string]any{"textDocument": textDocumentIdentifier{URI: uri}, "options": map[string]any{}}, &edits)
	if len(edits) != 0 {
		t.Errorf("edits for formatted text: %+v", edits)
	}
}

func TestFormattingEditApplies(t *testing.T) {
	text := "greenware 1\nasset a{root=a}\n\n\n\npart a {\n sphere {radius=1m}   // ball\n}\n"
	want, err := lang.Format([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	d := newDocument("file:///f.gw", 1, []byte(text))
	if got := applyEdits(t, text, d.lineDiff(want)); got != string(want) {
		t.Errorf("applying the edit gives\n%q\nwant\n%q", got, want)
	}
}

// ---- protocol edges ----------------------------------------------------------

func TestUnsupportedMethodIsMethodNotFound(t *testing.T) {
	c := startServer(t)
	c.initialize()
	_, rerr, _ := c.request("textDocument/rename", map[string]any{})
	if rerr == nil || rerr.Code != codeMethodNotFound {
		t.Errorf("rename: %v", rerr)
	}
	// An unknown notification is ignored and the session goes on.
	c.notify("$/cancelRequest", map[string]any{"id": 1})
	c.notify("workspace/didChangeConfiguration", map[string]any{})
	if _, rerr, _ := c.request("shutdown", nil); rerr != nil {
		t.Errorf("shutdown after unknown notifications: %v", rerr)
	}
}

func TestRequestBeforeInitialize(t *testing.T) {
	c := startServer(t)
	_, rerr, _ := c.request("textDocument/hover", map[string]any{})
	if rerr == nil || rerr.Code != codeServerNotInitialized {
		t.Errorf("hover before initialize: %v", rerr)
	}
	c.initialize()
	if _, rerr, _ := c.request("initialize", map[string]any{}); rerr == nil {
		t.Error("a second initialize succeeded")
	}
}

func TestRequestAfterShutdown(t *testing.T) {
	c := startServer(t)
	c.initialize()
	c.call("shutdown", nil, nil)
	_, rerr, _ := c.request("textDocument/hover", map[string]any{})
	if rerr == nil || rerr.Code != codeInvalidRequest {
		t.Errorf("hover after shutdown: %v", rerr)
	}
}

func TestMalformedInputKeepsServing(t *testing.T) {
	c := startServer(t)
	c.initialize()
	c.writeRaw([]byte(`{"jsonrpc":"2.0","id":`)) // truncated JSON
	var m struct {
		ID    json.RawMessage `json:"id"`
		Error *respError      `json:"error"`
	}
	if err := json.Unmarshal(c.recv(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Error == nil || m.Error.Code != codeParseError || string(m.ID) != "null" {
		t.Errorf("truncated JSON: %+v id %s", m.Error, m.ID)
	}
	// Params of the wrong shape.
	_, rerr, _ := c.request("textDocument/hover", "not an object")
	if rerr == nil || rerr.Code != codeInvalidParams {
		t.Errorf("bad params: %v", rerr)
	}
	// A request for a document that is not open.
	res, rerr, _ := c.request("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": "file:///none.gw"}, "position": map[string]any{}})
	if rerr != nil || string(res) != "null" {
		t.Errorf("hover on a document that is not open: %s %v", res, rerr)
	}
	// A request with no method.
	c.writeRaw([]byte(`{"jsonrpc":"2.0","id":77}`))
	if err := json.Unmarshal(c.recv(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Error == nil || m.Error.Code != codeInvalidRequest {
		t.Errorf("no method: %+v", m.Error)
	}
	// String ids come back as strings.
	c.writeRaw([]byte(`{"jsonrpc":"2.0","id":"abc","method":"shutdown"}`))
	if err := json.Unmarshal(c.recv(), &m); err != nil || string(m.ID) != `"abc"` {
		t.Errorf("string id: %s %v", m.ID, err)
	}
}

func TestPanicIsInternalError(t *testing.T) {
	requests["test/panic"] = func(*Server, json.RawMessage) (any, *respError) { panic("boom") }
	t.Cleanup(func() { delete(requests, "test/panic") })
	c := startServer(t)
	c.initialize()
	_, rerr, _ := c.request("test/panic", nil)
	if rerr == nil || rerr.Code != codeInternalError {
		t.Fatalf("panic answered with %v", rerr)
	}
	if _, rerr, _ := c.request("shutdown", nil); rerr != nil {
		t.Errorf("the server did not keep serving: %v", rerr)
	}
}

func TestExitCodes(t *testing.T) {
	c := startServer(t)
	c.initialize()
	if code := c.shutdown(); code != 0 {
		t.Errorf("shutdown then exit: %d", code)
	}
	c = startServer(t)
	c.initialize()
	c.notify("exit", nil)
	if code := <-c.exit; code != 1 {
		t.Errorf("exit without shutdown: %d, want 1", code)
	}
	c = startServer(t)
	c.initialize()
	c.in.Close() // stdin closes
	if code := <-c.exit; code != 0 {
		t.Errorf("end of input: %d, want 0", code)
	}
	if code := Serve(strings.NewReader("Content-Length: nope\r\n\r\n"), io.Discard, nil); code != 2 {
		t.Errorf("bad framing: %d, want 2", code)
	}
	if code := Serve(strings.NewReader("Content-Length: 99999999999\r\n\r\n"), io.Discard, nil); code != 2 {
		t.Errorf("huge length: %d, want 2", code)
	}
	if code := Serve(strings.NewReader("Content-Length: 50\r\n\r\n{}"), io.Discard, nil); code != 2 {
		t.Errorf("short body: %d, want 2", code)
	}
}

func TestFramingAcceptsExtraHeaders(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	in := "content-length: " + fmt.Sprint(len(body)) + "\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n" + body
	var out bytes.Buffer
	if code := Serve(strings.NewReader(in), &out, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), `"hoverProvider":true`) {
		t.Errorf("no initialize result: %s", out.String())
	}
}

func formatForTest(b []byte) ([]byte, error) { return lang.Format(b) }
