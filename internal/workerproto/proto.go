// Package workerproto is the message protocol of the Greenware browser worker
// and the one function that answers it. It is plain Go with no syscall/js, so
// native tests run the same code the WebAssembly worker runs.
//
// The worker is a thin shell around Handle. Handle calls the same
// ast.LowerWith and compiler.BuildWith as `greenware build`, so there is no
// second evaluator and the GLB bytes are the same.
//
// A request is one JSON document of at most MaxRequestBytes. A response is one
// JSON document of at most MaxResponseBytes. Every struct below fixes the key
// order of the JSON (encoding/json writes struct fields in declaration order
// and map keys sorted), so the same answer is the same bytes.
package workerproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"
)

// Schema names of the two documents.
const (
	RequestSchema  = "greenware.worker.request/1"
	ResponseSchema = "greenware.worker.response/1"
)

// Size caps. The snapshot caps are the `snapshot` ticket class of the cost
// schedule (at most 64 inputs, 4 MiB in all, 2 MiB per file).
const (
	MaxRequestBytes  = 8 << 20
	MaxResponseBytes = 16 << 20
	MaxFiles         = 64
	MaxSnapshotBytes = 4 << 20
	MaxFileBytes     = 2 << 20
)

// Values of Options.RuntimeSafe.
const (
	// SafeRequired imposes `runtime_safe required`. It is the default.
	SafeRequired = "required"
	// SafeOff builds with no worker admission. Only a host that sets
	// Host.AllowUnrestricted accepts it; the browser worker never does.
	SafeOff = "off"
)

// Error codes of Response.Error. A refused build is not an error: it is an
// ok=false response with Diagnostics.
const (
	ErrBadRequest = "bad_request"
	ErrTooLarge   = "too_large"
	ErrCanceled   = "canceled"
	ErrInternal   = "internal"
)

// Request is a sealed project snapshot and what to build from it.
type Request struct {
	Schema string `json:"schema"`
	// ID is echoed in the response so a page can match answers to requests.
	ID string `json:"id"`
	// Root is the source file to compile, a key of Files.
	Root string `json:"root"`
	// Output names one output. Empty builds every output.
	Output string `json:"output"`
	// Files maps a slash-separated path, relative to the project root, to the
	// bytes of the file. JSON carries the bytes as base64. A source's project
	// imports and tables are read from here and from nowhere else.
	Files   map[string][]byte `json:"files"`
	Options Options           `json:"options"`
}

// Options are the policy of one build.
type Options struct {
	// MaxWork is the total_work ceiling in work units. Zero means the default.
	// Under runtime_safe required it may lower the ceiling but not raise it.
	MaxWork uint64 `json:"max_work,omitempty"`
	// RuntimePoints is the `runtime_points` limit. Zero means the default.
	RuntimePoints int `json:"runtime_points,omitempty"`
	// RuntimeSafe is SafeRequired or SafeOff. Empty means SafeRequired.
	RuntimeSafe string `json:"runtime_safe,omitempty"`
}

// Response is the answer to one request.
type Response struct {
	Schema string `json:"schema"`
	ID     string `json:"id"`
	// OK is true when the build wrote every file.
	OK    bool   `json:"ok"`
	Error *Error `json:"error,omitempty"`
	// Files are the GLB files, every output in order, its levels in order.
	Files []File `json:"files,omitempty"`
	// Record is the build record, the same bytes `greenware build` writes.
	Record *Record `json:"record,omitempty"`
	// Diagnostics is a greenware.diagnostics/1 document. It is present when the
	// front end or the build reported anything, a warning included.
	Diagnostics json.RawMessage `json:"diagnostics,omitempty"`
}

// File is one built GLB.
type File struct {
	Output string `json:"output"`
	Name   string `json:"name"`
	Level  int    `json:"level"`
	SHA256 string `json:"sha256"`
	GLB    []byte `json:"glb"`
}

// Record is the build record file.
type Record struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  []byte `json:"bytes"`
}

// Error is a failure of the request itself, not of the source.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// DecodeRequest reads one request document. It refuses a document above
// MaxRequestBytes, an unknown key, a different schema and trailing data.
func DecodeRequest(b []byte) (Request, error) {
	var req Request
	if len(b) > MaxRequestBytes {
		return req, &Error{ErrTooLarge, fmt.Sprintf("the request is %d bytes; the limit is %d", len(b), MaxRequestBytes)}
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, &Error{ErrBadRequest, "the request is not a valid request document: " + err.Error()}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return req, &Error{ErrBadRequest, "the request has data after its document"}
	}
	if req.Schema != RequestSchema {
		return req, &Error{ErrBadRequest, fmt.Sprintf("the request schema is %q; this worker reads %q", req.Schema, RequestSchema)}
	}
	return req, nil
}

// Encode writes a response as JSON with no indentation and no trailing
// newline. A response above MaxResponseBytes becomes a too_large error that
// keeps the ID.
func Encode(resp Response) []byte {
	resp.Schema = ResponseSchema
	b := marshal(resp)
	if len(b) > MaxResponseBytes {
		b = marshal(failure(resp.ID, ErrTooLarge, fmt.Sprintf("the response is %d bytes; the limit is %d", len(b), MaxResponseBytes)))
	}
	return b
}

func marshal(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic("workerproto: " + err.Error())
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

func failure(id, code, msg string) Response {
	return Response{Schema: ResponseSchema, ID: id, Error: &Error{code, msg}}
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// snapshot is the sealed project: an fs.FS over the request's bytes. It reads
// nothing else, so a source cannot reach a file the page did not send.
type snapshot map[string][]byte

func (s snapshot) Open(name string) (fs.File, error) {
	if b, ok := s[name]; ok {
		return &snapFile{Reader: bytes.NewReader(b), name: name, size: int64(len(b))}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (s snapshot) ReadFile(name string) ([]byte, error) {
	if b, ok := s[name]; ok {
		return b, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

type snapFile struct {
	*bytes.Reader
	name string
	size int64
}

func (f *snapFile) Stat() (fs.FileInfo, error) { return f, nil }
func (f *snapFile) Close() error               { return nil }
func (f *snapFile) Name() string               { return f.name[strings.LastIndex(f.name, "/")+1:] }
func (f *snapFile) Size() int64                { return f.size }
func (f *snapFile) Mode() fs.FileMode          { return 0o444 }
func (f *snapFile) ModTime() time.Time         { return time.Time{} }
func (f *snapFile) IsDir() bool                { return false }
func (f *snapFile) Sys() any                   { return nil }

// validate checks the snapshot and the root before anything is parsed.
func validate(req Request) error {
	if len(req.Files) == 0 {
		return &Error{ErrBadRequest, "the snapshot has no files"}
	}
	if len(req.Files) > MaxFiles {
		return &Error{ErrTooLarge, fmt.Sprintf("the snapshot has %d files; the limit is %d", len(req.Files), MaxFiles)}
	}
	paths := make([]string, 0, len(req.Files))
	for p := range req.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	total := 0
	for _, p := range paths {
		if !fs.ValidPath(p) || p == "." || strings.Contains(p, `\`) {
			return &Error{ErrBadRequest, fmt.Sprintf("%q is not a relative, slash-separated file path inside the project", p)}
		}
		n := len(req.Files[p])
		if n > MaxFileBytes {
			return &Error{ErrTooLarge, fmt.Sprintf("%s is %d bytes; the limit is %d per file", p, n, MaxFileBytes)}
		}
		total += n
	}
	if total > MaxSnapshotBytes {
		return &Error{ErrTooLarge, fmt.Sprintf("the snapshot is %d bytes; the limit is %d", total, MaxSnapshotBytes)}
	}
	if _, ok := req.Files[req.Root]; !ok || !strings.HasSuffix(req.Root, ".gw") {
		return &Error{ErrBadRequest, fmt.Sprintf("the root %q is not a .gw file of the snapshot", req.Root)}
	}
	switch req.Options.RuntimeSafe {
	case "", SafeRequired, SafeOff:
	default:
		return &Error{ErrBadRequest, fmt.Sprintf("runtime_safe is %q; it is %q or %q", req.Options.RuntimeSafe, SafeRequired, SafeOff)}
	}
	return nil
}

// Refuse encodes an error response for a request the shell could not pass to
// Handle, such as a message above MaxRequestBytes.
func Refuse(id, code, msg string) []byte { return Encode(failure(id, code, msg)) }
