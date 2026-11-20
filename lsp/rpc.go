package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// maxMessage is the largest body the reader accepts. A header that asks for
// more is a transport failure, so a bad length cannot make the server
// allocate without bound.
const maxMessage = 64 << 20

// JSON-RPC and LSP error codes.
const (
	codeParseError           = -32700
	codeInvalidRequest       = -32600
	codeMethodNotFound       = -32601
	codeInvalidParams        = -32602
	codeInternalError        = -32603
	codeServerNotInitialized = -32002
	codeRequestFailed        = -32803
)

// message is one JSON-RPC 2.0 message, request, notification or response.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *respError      `json:"error,omitempty"`
}

// respError is the error object of a response.
type respError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *respError) Error() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }

// response is a message the server sends back. The id is raw so a number or
// a string goes back as it came; a null id answers a message that had none.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *respError      `json:"error,omitempty"`
}

// notification is a message the server sends with no id.
type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// errFraming marks input that is not a framed message: a bad or missing
// header. The stream cannot be trusted after it.
var errFraming = errors.New("lsp: bad message framing")

// reader reads Content-Length framed messages.
type reader struct {
	r *bufio.Reader
}

func newReader(r io.Reader) *reader { return &reader{r: bufio.NewReaderSize(r, 64<<10)} }

// next returns the body of the next message. It returns io.EOF when the
// stream ends between messages, and an error wrapping errFraming for a
// malformed header block.
func (rd *reader) next() ([]byte, error) {
	length := -1
	sawHeader := false
	for {
		line, err := rd.r.ReadString('\n')
		if err != nil {
			if err == io.EOF && !sawHeader && line == "" {
				return nil, io.EOF
			}
			if err == io.EOF {
				return nil, fmt.Errorf("%w: input ended inside a header", errFraming)
			}
			return nil, err
		}
		sawHeader = true
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, val, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("%w: header %q has no colon", errFraming, trunc(line))
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, perr := strconv.Atoi(strings.TrimSpace(val))
			if perr != nil || n < 0 {
				return nil, fmt.Errorf("%w: Content-Length %q", errFraming, trunc(val))
			}
			if n > maxMessage {
				return nil, fmt.Errorf("%w: Content-Length %d is over the %d byte limit", errFraming, n, maxMessage)
			}
			length = n
		}
		if len(line) > 4096 {
			return nil, fmt.Errorf("%w: header line is too long", errFraming)
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("%w: no Content-Length header", errFraming)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(rd.r, body); err != nil {
		return nil, fmt.Errorf("%w: input ended inside a body: %v", errFraming, err)
	}
	return body, nil
}

func trunc(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}

// writer writes Content-Length framed messages.
type writer struct {
	w io.Writer
}

func (wr *writer) send(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(body))
	buf.Write(body)
	_, err = wr.w.Write(buf.Bytes())
	return err
}
