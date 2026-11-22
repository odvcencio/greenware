// Package socketmeta owns the metadata shared by Greenware GLB producers and readers.
package socketmeta

import (
	"encoding/json"
	"fmt"
	"strings"
)

const Prefix = "socket."

const format = "greenware.socket/1"

type Value struct {
	Name, Kind, Joint string
}

func Encode(v Value) json.RawMessage {
	payload := struct {
		Greenware struct {
			Format string `json:"format"`
			Name   string `json:"name"`
			Kind   string `json:"kind"`
			Joint  string `json:"joint,omitempty"`
		} `json:"greenware"`
	}{}
	payload.Greenware.Format = format
	payload.Greenware.Name = v.Name
	payload.Greenware.Kind = v.Kind
	payload.Greenware.Joint = v.Joint
	// The payload contains only strings and can always be encoded.
	raw, _ := json.Marshal(payload)
	return raw
}

func Decode(extras json.RawMessage) (Value, bool, error) {
	var envelope struct{ Greenware json.RawMessage }
	var schema struct{ Format string }
	// glTF extras can carry arbitrary JSON. Only interpret a socket schema.
	if json.Unmarshal(extras, &envelope) != nil ||
		json.Unmarshal(envelope.Greenware, &schema) != nil ||
		!strings.HasPrefix(schema.Format, "greenware.socket/") {
		return Value{}, false, nil
	}
	if schema.Format != format {
		return Value{}, false, fmt.Errorf("unsupported socket metadata format %q", schema.Format)
	}
	var meta Value
	if err := json.Unmarshal(envelope.Greenware, &meta); err != nil {
		return Value{}, false, fmt.Errorf("socket metadata: %w", err)
	}
	return meta, true, nil
}
