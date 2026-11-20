package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// client drives a Server in-process over pipes.
type client struct {
	t     *testing.T
	in    io.WriteCloser
	msgs  chan json.RawMessage
	exit  chan int
	nextI int
}

func startServer(t *testing.T) *client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &client{t: t, in: inW, msgs: make(chan json.RawMessage, 1024), exit: make(chan int, 1)}
	go func() {
		code := Serve(inR, outW, io.Discard)
		outW.Close()
		c.exit <- code
	}()
	go func() {
		rd := newReader(outR)
		for {
			body, err := rd.next()
			if err != nil {
				close(c.msgs)
				return
			}
			c.msgs <- body
		}
	}()
	t.Cleanup(func() { inW.Close() })
	return c
}

func (c *client) write(v any) {
	c.t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	c.writeRaw(body)
}

func (c *client) writeRaw(body []byte) {
	c.t.Helper()
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(body))
	buf.Write(body)
	if _, err := c.in.Write(buf.Bytes()); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) recv() json.RawMessage {
	c.t.Helper()
	select {
	case m, ok := <-c.msgs:
		if !ok {
			c.t.Fatal("the server closed its output")
		}
		return m
	case <-time.After(30 * time.Second):
		c.t.Fatal("timed out waiting for the server")
	}
	return nil
}

// request sends a request and returns its response, with the notifications
// that came before it.
func (c *client) request(method string, params any) (result json.RawMessage, rerr *respError, notes []json.RawMessage) {
	c.t.Helper()
	c.nextI++
	id := c.nextI
	c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	for {
		m := c.recv()
		var r struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *respError      `json:"error"`
		}
		if err := json.Unmarshal(m, &r); err != nil {
			c.t.Fatal(err)
		}
		if r.ID != nil && *r.ID == id && r.Method == "" {
			return r.Result, r.Error, notes
		}
		notes = append(notes, m)
	}
}

// call is request that fails the test on an error response and decodes into out.
func (c *client) call(method string, params any, out any) {
	c.t.Helper()
	res, rerr, _ := c.request(method, params)
	if rerr != nil {
		c.t.Fatalf("%s: %v", method, rerr)
	}
	if out != nil {
		if err := json.Unmarshal(res, out); err != nil {
			c.t.Fatalf("%s: %v\n%s", method, err, res)
		}
	}
}

func (c *client) notify(method string, params any) {
	c.t.Helper()
	c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

type published struct {
	URI         string       `json:"uri"`
	Version     int          `json:"version"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// diagnostics reads the next publishDiagnostics notification.
func (c *client) diagnostics() published {
	c.t.Helper()
	var m struct {
		Method string    `json:"method"`
		Params published `json:"params"`
	}
	if err := json.Unmarshal(c.recv(), &m); err != nil {
		c.t.Fatal(err)
	}
	if m.Method != "textDocument/publishDiagnostics" {
		c.t.Fatalf("got %s, want publishDiagnostics", m.Method)
	}
	return m.Params
}

func (c *client) initialize() {
	c.t.Helper()
	c.call("initialize", map[string]any{"processId": nil, "rootUri": nil, "capabilities": map[string]any{}}, nil)
	c.notify("initialized", map[string]any{})
}

func (c *client) open(uri, text string) published {
	c.t.Helper()
	c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "greenware", "version": 1, "text": text}})
	return c.diagnostics()
}

func (c *client) shutdown() int {
	c.t.Helper()
	c.call("shutdown", nil, nil)
	c.notify("exit", nil)
	select {
	case code := <-c.exit:
		return code
	case <-time.After(10 * time.Second):
		c.t.Fatal("the server did not exit")
	}
	return -1
}

// ---- recorded exchanges ------------------------------------------------------

// A transcript is a list of steps. Each step has a message to send and the
// messages the server sent back, recorded. A request's step ends at its
// response; a didOpen, didChange or didClose step has one message (the
// diagnostics); any other notification has none. An object {"$file": "path"}
// in a message stands for the text of that file, read from the repository
// root.
type step struct {
	Send   json.RawMessage   `json:"send"`
	Expect []json.RawMessage `json:"expect"`
}

type transcript struct {
	Steps []step `json:"steps"`
}

const transcriptDir = "../testdata/lsp"

func expandFiles(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if p, ok := x["$file"].(string); ok && len(x) == 1 {
			b, err := os.ReadFile(filepath.Join("..", p))
			if err != nil {
				panic(err)
			}
			return string(b)
		}
		for k, e := range x {
			x[k] = expandFiles(e)
		}
	case []any:
		for i, e := range x {
			x[i] = expandFiles(e)
		}
	}
	return v
}

func canon(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("bad JSON %s: %v", raw, err)
	}
	return v
}

// runTranscript replays the sends of a transcript and compares the server's
// messages with the recorded ones. With -update it records them instead.
func runTranscript(t *testing.T, name string, update bool) {
	path := filepath.Join(transcriptDir, name+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tr transcript
	if err := json.Unmarshal(b, &tr); err != nil {
		t.Fatal(err)
	}
	c := startServer(t)
	for i, st := range tr.Steps {
		var send any
		if err := json.Unmarshal(st.Send, &send); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		send = expandFiles(send)
		m := send.(map[string]any)
		method, _ := m["method"].(string)
		var got []json.RawMessage
		if id, isReq := m["id"]; isReq {
			c.write(send)
			for {
				msg := c.recv()
				got = append(got, msg)
				var r struct {
					ID     any    `json:"id"`
					Method string `json:"method"`
				}
				json.Unmarshal(msg, &r)
				if r.Method == "" && fmt.Sprint(r.ID) == fmt.Sprint(id) {
					break
				}
			}
		} else {
			c.write(send)
			if method == "textDocument/didOpen" || method == "textDocument/didChange" || method == "textDocument/didClose" {
				got = append(got, c.recv())
			}
		}
		if update {
			tr.Steps[i].Expect = got
			continue
		}
		if len(got) != len(st.Expect) {
			t.Fatalf("step %d (%s): got %d messages, recorded %d\n%s", i, method, len(got), len(st.Expect), got)
		}
		for j := range got {
			a, e := canon(t, got[j]), canon(t, st.Expect[j])
			if fmt.Sprint(a) != fmt.Sprint(e) { // maps print in key order
				t.Errorf("step %d (%s) message %d differs\n got: %s\nwant: %s", i, method, j, got[j], st.Expect[j])
			}
		}
	}
	if update {
		var out strings.Builder
		out.WriteString("{\"steps\": [\n")
		for i, st := range tr.Steps {
			out.WriteString("  {\"send\": " + compact(st.Send) + ",\n   \"expect\": [")
			for j, e := range st.Expect {
				if j > 0 {
					out.WriteString(",\n     ")
				} else {
					out.WriteString("\n     ")
				}
				out.WriteString(compact(e))
			}
			if len(st.Expect) > 0 {
				out.WriteString("\n   ")
			}
			out.WriteString("]}")
			if i < len(tr.Steps)-1 {
				out.WriteString(",")
			}
			out.WriteString("\n")
		}
		out.WriteString("]}\n")
		if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func compact(raw []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}
