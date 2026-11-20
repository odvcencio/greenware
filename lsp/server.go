// Package lsp is the Greenware language server. It speaks the Language Server
// Protocol (JSON-RPC 2.0 with Content-Length framing) over a pair of streams
// and uses nothing outside the standard library and the front end.
//
// The server keeps the text of each open document, the syntax tree and the
// front end's findings. It builds no index across files, listens on no
// network and ends when its input closes. Requests are served one at a time,
// in the order they arrive, so every answer matches the text the client last
// sent. See docs/reference/lsp.md for the methods and their limits.
package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
)

// Server holds the state of one editor session.
type Server struct {
	rd    *reader
	wr    *writer
	log   io.Writer
	docs  map[string]*document
	state sessionState
	// exitCode is set by the exit notification.
	exitCode int
	done     bool
}

type sessionState int

const (
	stateNew sessionState = iota
	stateReady
	stateShutdown
)

// Serve runs one session: it reads framed messages from in and writes framed
// messages to out until in closes or the client sends exit. Diagnostics of the
// server itself go to log, which may be nil. It returns the process exit
// status: 0 when the session closed normally, 1 when exit came with no
// shutdown before it, 2 when the input was not valid framing or a stream
// failed.
func Serve(in io.Reader, out io.Writer, log io.Writer) int {
	s := &Server{
		rd:   newReader(in),
		wr:   &writer{w: out},
		log:  log,
		docs: map[string]*document{},
	}
	for !s.done {
		body, err := s.rd.next()
		if err == io.EOF {
			return 0
		}
		if err != nil {
			s.logf("transport: %v", err)
			return 2
		}
		if err := s.handle(body); err != nil {
			s.logf("write: %v", err)
			return 2
		}
	}
	return s.exitCode
}

func (s *Server) logf(format string, args ...any) {
	if s.log != nil {
		fmt.Fprintf(s.log, "greenware lsp: "+format+"\n", args...)
	}
}

// handler answers a request: a result to marshal, or an error.
type handler func(s *Server, params json.RawMessage) (any, *respError)

var requests = map[string]handler{
	"initialize":                  (*Server).initialize,
	"shutdown":                    (*Server).shutdown,
	"textDocument/hover":          (*Server).hover,
	"textDocument/definition":     (*Server).definition,
	"textDocument/references":     (*Server).references,
	"textDocument/documentSymbol": (*Server).documentSymbol,
	"textDocument/completion":     (*Server).completion,
	"textDocument/formatting":     (*Server).formatting,
	"textDocument/codeAction":     (*Server).codeAction,
}

var notifications = map[string]func(s *Server, params json.RawMessage) error{
	"initialized":            func(*Server, json.RawMessage) error { return nil },
	"textDocument/didOpen":   (*Server).didOpen,
	"textDocument/didChange": (*Server).didChange,
	"textDocument/didClose":  (*Server).didClose,
	"exit":                   (*Server).exit,
}

// handle reads one message body and answers it. It returns only a write
// error. A panic in a handler is answered with InternalError, and the server
// keeps serving.
func (s *Server) handle(body []byte) (werr error) {
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		return s.reply(nil, nil, &respError{codeParseError, "parse error: " + err.Error()})
	}
	isRequest := len(m.ID) > 0
	if m.Method == "" {
		if isRequest && m.Result == nil && m.Error == nil {
			return s.reply(m.ID, nil, &respError{codeInvalidRequest, "a request needs a method"})
		}
		return nil // a response to a request we never sent
	}
	defer func() {
		if r := recover(); r != nil {
			s.logf("panic in %s: %v\n%s", m.Method, r, debug.Stack())
			if isRequest {
				werr = s.reply(m.ID, nil, &respError{codeInternalError, fmt.Sprintf("internal error in %s", m.Method)})
			}
		}
	}()
	if !isRequest {
		return s.notify(m)
	}
	h, ok := requests[m.Method]
	switch {
	case !ok:
		return s.reply(m.ID, nil, &respError{codeMethodNotFound, "method not supported: " + m.Method})
	case s.state == stateNew && m.Method != "initialize":
		return s.reply(m.ID, nil, &respError{codeServerNotInitialized, "the server is not initialized"})
	case s.state == stateShutdown:
		return s.reply(m.ID, nil, &respError{codeInvalidRequest, "the server is shut down"})
	}
	result, rerr := h(s, m.Params)
	return s.reply(m.ID, result, rerr)
}

// notify runs a notification. Notifications get no reply, even on error.
func (s *Server) notify(m message) error {
	h, ok := notifications[m.Method]
	if !ok {
		return nil // an unknown notification, such as $/cancelRequest, is ignored
	}
	if s.state == stateNew && m.Method != "exit" {
		return nil
	}
	if s.state == stateShutdown && m.Method != "exit" {
		return nil
	}
	if err := h(s, m.Params); err != nil {
		s.logf("%s: %v", m.Method, err)
	}
	return nil
}

func (s *Server) reply(id json.RawMessage, result any, rerr *respError) error {
	if id == nil {
		id = json.RawMessage("null")
	}
	resp := response{JSONRPC: "2.0", ID: id}
	if rerr != nil {
		resp.Error = rerr
	} else {
		b, err := json.Marshal(result)
		if err != nil {
			resp.Error = &respError{codeInternalError, "cannot encode the result: " + err.Error()}
		} else {
			resp.Result = b
		}
	}
	if resp.Error == nil && len(resp.Result) == 0 {
		resp.Result = json.RawMessage("null")
	}
	return s.wr.send(resp)
}

func (s *Server) publish(d *document) error {
	return s.wr.send(notification{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics", Params: map[string]any{
		"uri":         d.uri,
		"version":     d.version,
		"diagnostics": d.diagnostics(),
	}})
}

func invalidParams(err error) *respError {
	return &respError{codeInvalidParams, "invalid params: " + err.Error()}
}

// ---- lifecycle ---------------------------------------------------------------

func (s *Server) initialize(params json.RawMessage) (any, *respError) {
	if s.state != stateNew {
		return nil, &respError{codeInvalidRequest, "the server is already initialized"}
	}
	s.state = stateReady
	return map[string]any{
		"capabilities": map[string]any{
			"positionEncoding":           "utf-16",
			"textDocumentSync":           map[string]any{"openClose": true, "change": 2},
			"hoverProvider":              true,
			"definitionProvider":         true,
			"referencesProvider":         true,
			"documentSymbolProvider":     true,
			"documentFormattingProvider": true,
			"codeActionProvider":         map[string]any{"codeActionKinds": []string{"quickfix"}},
			"completionProvider":         map[string]any{"resolveProvider": false},
		},
		"serverInfo": map[string]any{"name": "greenware"},
	}, nil
}

func (s *Server) shutdown(json.RawMessage) (any, *respError) {
	s.state = stateShutdown
	return nil, nil
}

func (s *Server) exit(json.RawMessage) error {
	s.done = true
	if s.state != stateShutdown {
		s.exitCode = 1
	}
	return nil
}

// ---- documents ---------------------------------------------------------------

func (s *Server) didOpen(params json.RawMessage) error {
	var p struct {
		TextDocument struct {
			URI     string `json:"uri"`
			Version int    `json:"version"`
			Text    string `json:"text"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return err
	}
	d := newDocument(p.TextDocument.URI, p.TextDocument.Version, []byte(p.TextDocument.Text))
	s.docs[d.uri] = d
	return s.publish(d)
}

func (s *Server) didChange(params json.RawMessage) error {
	var p struct {
		TextDocument struct {
			URI     string `json:"uri"`
			Version int    `json:"version"`
		} `json:"textDocument"`
		ContentChanges []change `json:"contentChanges"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return err
	}
	d := s.docs[p.TextDocument.URI]
	if d == nil {
		return fmt.Errorf("didChange for %s, which is not open", p.TextDocument.URI)
	}
	d.version = p.TextDocument.Version
	d.apply(p.ContentChanges)
	return s.publish(d)
}

func (s *Server) didClose(params json.RawMessage) error {
	var p struct {
		TextDocument textDocumentIdentifier `json:"textDocument"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return err
	}
	delete(s.docs, p.TextDocument.URI)
	return s.wr.send(notification{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics", Params: map[string]any{
		"uri": p.TextDocument.URI, "diagnostics": []Diagnostic{},
	}})
}

// doc returns the open document of a request, or nil.
func (s *Server) doc(uri string) *document { return s.docs[uri] }

// Capabilities lists the methods the server answers, for `greenware lsp
// --check-config`.
func Capabilities() []string {
	return []string{
		"textDocument/didOpen", "textDocument/didChange", "textDocument/didClose",
		"textDocument/publishDiagnostics", "textDocument/hover", "textDocument/definition",
		"textDocument/references", "textDocument/documentSymbol", "textDocument/completion",
		"textDocument/formatting", "textDocument/codeAction",
	}
}
