package units

import (
	"bytes"
	"encoding/json"
)

// MarshalJSON writes a value in a fixed, readable shape:
//
//	number    {"dim":"length","value":3.05}   (metres, radians, seconds, bytes)
//	switch    true or false
//	string    "text"
//	colour    {"color":"#1d4e89ff"}
//	axis      {"axis":"+y"}
//	label     {"label":"radial"}
//	reference {"ref":"steel"}
//	tuple     {"tuple":[...]}  list {"list":[...]}  range {"range":[lo,hi]}
//	profile   {"profile":"circle","args":[...]}
//
// A value that depends on a runtime parameter adds "runtime":true.
func (v Value) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := func(x any) {
		b, _ := json.Marshal(x)
		buf.Write(b)
	}
	elems := func(key string) {
		buf.WriteString(`{"` + key + `":[`)
		for i, e := range v.Elems {
			if i > 0 {
				buf.WriteByte(',')
			}
			b, err := e.MarshalJSON()
			if err != nil {
				return
			}
			buf.Write(b)
		}
		buf.WriteString(`]`)
	}
	switch v.Kind {
	case KSwitch:
		enc(v.Bool)
		return buf.Bytes(), nil
	case KString:
		enc(v.Str)
		return buf.Bytes(), nil
	case KNumber:
		dim := v.Dim.String()
		if v.Pending {
			dim = "pending"
		}
		buf.WriteString(`{"dim":`)
		enc(dim)
		buf.WriteString(`,"value":`)
		enc(v.Num)
	case KColor:
		buf.WriteString(`{"color":`)
		enc(v.Color.Hex())
	case KAxis:
		buf.WriteString(`{"axis":`)
		enc(v.Str)
	case KLabel:
		buf.WriteString(`{"label":`)
		enc(v.Str)
	case KRef:
		buf.WriteString(`{"ref":`)
		enc(v.Str)
	case KTuple:
		elems("tuple")
	case KList:
		elems("list")
	case KRange:
		elems("range")
	case KProfile:
		buf.WriteString(`{"profile":`)
		enc(v.Str)
		buf.WriteString(`,"args":[`)
		for i, e := range v.Elems {
			if i > 0 {
				buf.WriteByte(',')
			}
			b, _ := e.MarshalJSON()
			buf.Write(b)
		}
		buf.WriteString(`]`)
	default:
		return []byte("null"), nil
	}
	if v.Runtime {
		buf.WriteString(`,"runtime":true`)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
