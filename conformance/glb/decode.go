package glb

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	glbMagic     = 0x46546C67 // "glTF"
	chunkJSON    = 0x4E4F534A // "JSON"
	chunkBIN     = 0x004E4942 // "BIN\0"
	maxElemCount = 1 << 26    // refuse to materialise larger accessors
)

// File is a decoded GLB. Doc is nil when the JSON chunk could not be parsed.
type File struct {
	Doc      *Document
	JSONText []byte
	Bin      []byte
	HasBin   bool

	buffers [][]byte // resolved bytes per buffer, nil when unresolved
	accs    []accInfo
	parent  []int // parent node per node, -1 for roots; set by validateNodes
	cache   map[int][]float64
}

// Options controls Decode.
type Options struct {
	// Resolve loads an external buffer or image URI. It is called only for
	// non-data URIs. Nil means external resources are reported as unresolved.
	Resolve func(uri string) ([]byte, error)
}

// Decode parses and validates a GLB. It never panics: an internal fault is
// reported as a finding with code ORACLE-INTERNAL. The returned File may be
// non-nil even when findings exist.
func Decode(data []byte, opts ...Options) (f *File, findings []Finding) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	c := &collector{}
	defer func() {
		if r := recover(); r != nil {
			c.errf("ORACLE-INTERNAL", "", "validator fault: %v", r)
			findings = c.fs
		}
	}()
	f = decode(data, o, c)
	return f, c.fs
}

func decode(data []byte, o Options, c *collector) *File {
	f := &File{cache: map[int][]float64{}}
	jsonChunk, ok := f.splitContainer(data, c)
	if !ok {
		return f
	}
	f.JSONText = jsonChunk
	dec := json.NewDecoder(bytes.NewReader(jsonChunk))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		c.errf("JSON-SYNTAX", "glb/chunks/0", "the JSON chunk does not parse: %v", err)
		return f
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		c.errf("JSON-TRAILING", "glb/chunks/0", "unexpected data after the JSON value (the chunk must be padded with spaces)")
	}
	f.Doc = parseDocument(root, c)
	if f.Doc == nil {
		return f
	}
	f.resolveBuffers(o, c)
	f.validate(c)
	return f
}

// splitContainer checks the 12-byte header and the chunk list.
func (f *File) splitContainer(data []byte, c *collector) ([]byte, bool) {
	if len(data) < 12 {
		c.errf("GLB-HEADER", "glb/header", "file has %d bytes; the header needs 12", len(data))
		return nil, false
	}
	le := binary.LittleEndian
	if m := le.Uint32(data[0:]); m != glbMagic {
		c.errf("GLB-MAGIC", "glb/header/magic", "magic is %#08x; expected 0x46546C67 (\"glTF\")", m)
		return nil, false
	}
	if v := le.Uint32(data[4:]); v != 2 {
		c.errf("GLB-VERSION", "glb/header/version", "container version is %d; expected 2", v)
		return nil, false
	}
	total := uint64(le.Uint32(data[8:]))
	end := uint64(len(data))
	if total != end {
		c.errf("GLB-LENGTH", "glb/header/length", "header length is %d but the file has %d bytes", total, end)
		if total < end {
			end = total
		}
	}
	if end < 12 {
		return nil, false
	}
	var jsonChunk []byte
	haveJSON := false
	off := uint64(12)
	idx := 0
	for off < end {
		if end-off < 8 {
			c.errf("GLB-CHUNK", fmt.Sprintf("glb/chunks/%d", idx), "%d trailing bytes cannot hold a chunk header", end-off)
			break
		}
		clen := uint64(le.Uint32(data[off:]))
		ctype := le.Uint32(data[off+4:])
		path := fmt.Sprintf("glb/chunks/%d", idx)
		if off+8+clen > end {
			c.errf("GLB-CHUNK-LENGTH", path, "chunk length %d runs past the end of the file (%d bytes remain)", clen, end-off-8)
			break
		}
		body := data[off+8 : off+8+clen]
		if clen%4 != 0 {
			c.errf("GLB-CHUNK-ALIGN", path, "chunk length %d is not a multiple of 4", clen)
		}
		switch {
		case idx == 0 && ctype != chunkJSON:
			c.errf("GLB-CHUNK-ORDER", path, "the first chunk has type %#08x; it must be JSON (0x4E4F534A)", ctype)
		case ctype == chunkJSON:
			if haveJSON {
				c.errf("GLB-CHUNK-DUP", path, "more than one JSON chunk")
			} else {
				haveJSON, jsonChunk = true, body
				if clen == 0 {
					c.errf("GLB-JSON-EMPTY", path, "the JSON chunk is empty")
				}
			}
		case ctype == chunkBIN:
			switch {
			case idx != 1:
				c.errf("GLB-CHUNK-ORDER", path, "the BIN chunk must be the second chunk")
			case f.HasBin:
				c.errf("GLB-CHUNK-DUP", path, "more than one BIN chunk")
			default:
				f.HasBin, f.Bin = true, body
			}
		default:
			c.warnf("GLB-CHUNK-UNKNOWN", path, "unknown chunk type %#08x ignored", ctype)
		}
		off += 8 + clen
		idx++
	}
	if !haveJSON {
		c.errf("GLB-NO-JSON", "glb/chunks", "no JSON chunk found")
		return nil, false
	}
	return jsonChunk, true
}

func (f *File) resolveBuffers(o Options, c *collector) {
	d := f.Doc
	f.buffers = make([][]byte, len(d.Buffers))
	for i, b := range d.Buffers {
		path := fmt.Sprintf("/buffers/%d", i)
		switch {
		case !b.HasURI:
			if i != 0 {
				c.errf("BUFFER-NO-URI", path, "only buffer 0 may omit uri (it is the GLB binary chunk)")
				continue
			}
			if !f.HasBin {
				c.errf("BUFFER-NO-BIN", path, "buffer 0 has no uri and the file has no BIN chunk")
				continue
			}
			if b.ByteLength > len(f.Bin) {
				c.errf("BUFFER-LENGTH", path+"/byteLength", "byteLength %d exceeds the BIN chunk length %d", b.ByteLength, len(f.Bin))
				continue
			}
			if len(f.Bin)-b.ByteLength > 3 {
				c.errf("BUFFER-PADDING", path+"/byteLength", "the BIN chunk (%d bytes) is more than 3 bytes longer than byteLength %d", len(f.Bin), b.ByteLength)
			}
			f.buffers[i] = f.Bin[:b.ByteLength]
		case strings.HasPrefix(b.URI, "data:"):
			raw, err := decodeDataURI(b.URI)
			if err != nil {
				c.errf("BUFFER-URI", path+"/uri", "%v", err)
				continue
			}
			if len(raw) < b.ByteLength {
				c.errf("BUFFER-LENGTH", path+"/byteLength", "byteLength %d exceeds the %d decoded bytes", b.ByteLength, len(raw))
				continue
			}
			f.buffers[i] = raw[:b.ByteLength]
		default:
			if o.Resolve == nil {
				c.warnf("BUFFER-EXTERNAL", path+"/uri", "external resource %q was not loaded; accessors that use it are not checked", b.URI)
				continue
			}
			raw, err := o.Resolve(b.URI)
			if err != nil {
				c.errf("BUFFER-UNRESOLVED", path+"/uri", "cannot load %q: %v", b.URI, err)
				continue
			}
			if len(raw) < b.ByteLength {
				c.errf("BUFFER-LENGTH", path+"/byteLength", "byteLength %d exceeds the %d bytes loaded from %q", b.ByteLength, len(raw), b.URI)
				continue
			}
			f.buffers[i] = raw[:b.ByteLength]
		}
	}
}

func decodeDataURI(uri string) ([]byte, error) {
	i := strings.IndexByte(uri, ',')
	if i < 0 {
		return nil, fmt.Errorf("data URI has no comma")
	}
	if !strings.HasSuffix(uri[:i], ";base64") {
		return nil, fmt.Errorf("data URI is not base64")
	}
	return base64.StdEncoding.DecodeString(uri[i+1:])
}
