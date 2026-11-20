package gltf

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"sort"
	"strconv"
)

// canonicalJSON re-writes raw as compact JSON with sorted object keys and
// number text kept as written. It returns nil for empty input and for null.
func canonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, io.ErrUnexpectedEOF
	}
	if v == nil {
		return nil, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

func canonicalExt(m map[string]json.RawMessage, path string) (map[string]json.RawMessage, error) {
	if len(m) == 0 {
		return nil, nil
	}
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		c, err := canonicalJSON(v)
		if err != nil {
			return nil, errf(path+"."+k, "invalid JSON: %v", err)
		}
		if c == nil {
			c = json.RawMessage("null")
		}
		out[k] = c
	}
	return out, nil
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func jstr(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}

// jnum formats a float32 as the shortest JSON number that reads back as the
// same float32. Negative zero becomes 0.
func jnum(f float32) string {
	if f == 0 {
		return "0"
	}
	return strconv.FormatFloat(float64(f), 'g', -1, 32)
}

func jnums(fs []float32) string {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, f := range fs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jnum(f))
	}
	b.WriteByte(']')
	return b.String()
}

func jints(is []int) string {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, v := range is {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(v))
	}
	b.WriteByte(']')
	return b.String()
}

// fields builds a JSON object with its members in the order they are added.
type fields []string

func (f *fields) add(key, value string) { *f = append(*f, jstr(key)+":"+value) }
func (f *fields) String() string {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, s := range *f {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s)
	}
	b.WriteByte('}')
	return b.String()
}

func jarr(items []string) string {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, s := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s)
	}
	b.WriteByte(']')
	return b.String()
}

func finite32(f float32) bool {
	return !math.IsNaN(float64(f)) && !math.IsInf(float64(f), 0)
}
