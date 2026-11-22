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
	for _, k := range sortedKeys(m) {
		c, err := canonicalJSON(m[k])
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

// mergeTargetNames returns extras with a "targetNames" member added. It is an
// error when extras is not a JSON object or already has that member.
func mergeTargetNames(extras json.RawMessage, names []string, path string) (json.RawMessage, error) {
	if len(names) == 0 {
		return extras, nil
	}
	obj := map[string]any{}
	c, err := canonicalJSON(extras)
	if err != nil {
		return nil, errf(path, "invalid JSON: %v", err)
	}
	if c != nil {
		dec := json.NewDecoder(bytes.NewReader(c))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, errf(path, "invalid JSON: %v", err)
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, errf(path, "TargetNames needs extras to be a JSON object")
		}
		obj = m
	}
	if _, dup := obj["targetNames"]; dup {
		return nil, errf(path+".targetNames", "set TargetNames or the extras member, not both")
	}
	list := make([]any, len(names))
	for i, n := range names {
		list[i] = n
	}
	obj["targetNames"] = list
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, errf(path, "invalid JSON: %v", err)
	}
	return out, nil
}

// splitTargetNames takes a "targetNames" member out of canonical extras when it
// is a non-empty list of exactly n strings. Otherwise it returns extras
// unchanged and nil names.
func splitTargetNames(extras json.RawMessage, n int) (json.RawMessage, []string) {
	if n == 0 || len(extras) == 0 {
		return extras, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(extras, &obj); err != nil {
		return extras, nil
	}
	raw, ok := obj["targetNames"]
	if !ok {
		return extras, nil
	}
	var list []any
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != n {
		return extras, nil
	}
	names := make([]string, n)
	for i, v := range list {
		s, ok := v.(string)
		if !ok {
			return extras, nil
		}
		names[i] = s
	}
	delete(obj, "targetNames")
	if len(obj) == 0 {
		return nil, names
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return extras, nil
	}
	rest, err := canonicalJSON(b)
	if err != nil {
		return extras, nil
	}
	return rest, names
}
