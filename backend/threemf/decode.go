package threemf

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// Decode reads a 3MF package that Encode wrote, or one of the same shape: a
// stored ZIP with [Content_Types].xml, _rels/.rels and the model part. It
// parses the XML with encoding/xml and checks the CRC of every part, the
// content type of the model and the relationship that points to it. It does
// not call Validate; call that on the result. It exists for tests and
// acceptance, and reads nothing the writer cannot produce (no components, no
// transforms, no extensions).
func Decode(b []byte) (*Model, error) {
	parts, err := readZip(b)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", ModelPath} {
		if _, ok := parts[name]; !ok {
			return nil, fmt.Errorf("threemf: the package has no part %q", name)
		}
	}
	var ct struct {
		XMLName  xml.Name `xml:"Types"`
		Defaults []struct {
			Ext  string `xml:"Extension,attr"`
			Type string `xml:"ContentType,attr"`
		} `xml:"Default"`
	}
	if err := xml.Unmarshal(parts["[Content_Types].xml"], &ct); err != nil {
		return nil, fmt.Errorf("threemf: [Content_Types].xml: %w", err)
	}
	if ct.XMLName.Space != typesNamespace {
		return nil, errors.New("threemf: [Content_Types].xml has the wrong namespace")
	}
	hasModel := false
	for _, d := range ct.Defaults {
		if d.Ext == "model" && d.Type == modelType {
			hasModel = true
		}
	}
	if !hasModel {
		return nil, errors.New("threemf: [Content_Types].xml does not declare the model content type")
	}
	var rels struct {
		XMLName xml.Name `xml:"Relationships"`
		Rels    []struct {
			Target string `xml:"Target,attr"`
			Type   string `xml:"Type,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.Unmarshal(parts["_rels/.rels"], &rels); err != nil {
		return nil, fmt.Errorf("threemf: _rels/.rels: %w", err)
	}
	pointed := false
	for _, r := range rels.Rels {
		if r.Type == modelRelType && r.Target == "/"+ModelPath {
			pointed = true
		}
	}
	if !pointed {
		return nil, errors.New("threemf: _rels/.rels has no relationship to the model part")
	}
	return decodeModel(parts[ModelPath])
}

func decodeModel(data []byte) (*Model, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	m := &Model{}
	var cur *Object
	var path []string
	var metaName string
	var text bytes.Buffer
	attr := func(se xml.StartElement, name string) (string, bool) {
		for _, a := range se.Attr {
			if a.Name.Local == name && a.Name.Space == "" {
				return a.Value, true
			}
		}
		return "", false
	}
	num := func(se xml.StartElement, name string) (float64, error) {
		s, ok := attr(se, name)
		if !ok {
			return 0, fmt.Errorf("threemf: <%s> has no %s", se.Name.Local, name)
		}
		return strconv.ParseFloat(s, 64)
	}
	idx := func(se xml.StartElement, name string) (int, error) {
		s, ok := attr(se, name)
		if !ok {
			return 0, fmt.Errorf("threemf: <%s> has no %s", se.Name.Local, name)
		}
		return strconv.Atoi(s)
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("threemf: %s: %w", ModelPath, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			path = append(path, t.Name.Local)
			if t.Name.Space != CoreNamespace {
				return nil, fmt.Errorf("threemf: element <%s> is not in the 3MF core namespace", t.Name.Local)
			}
			switch t.Name.Local {
			case "model":
				if len(path) != 1 {
					return nil, errors.New("threemf: <model> is not the root")
				}
				m.Unit, _ = attr(t, "unit")
				if m.Unit == "" {
					m.Unit = "millimeter" // the 3MF default
				}
			case "metadata":
				metaName, _ = attr(t, "name")
				text.Reset()
			case "object":
				id, err := idx(t, "id")
				if err != nil {
					return nil, err
				}
				name, _ := attr(t, "name")
				m.Objects = append(m.Objects, Object{ID: id, Name: name})
				cur = &m.Objects[len(m.Objects)-1]
			case "vertex":
				if cur == nil {
					return nil, errors.New("threemf: <vertex> outside an object")
				}
				var p Vec3
				for i, n := range []string{"x", "y", "z"} {
					if p[i], err = num(t, n); err != nil {
						return nil, fmt.Errorf("threemf: vertex %d: %w", len(cur.Vertices), err)
					}
				}
				cur.Vertices = append(cur.Vertices, p)
			case "triangle":
				if cur == nil {
					return nil, errors.New("threemf: <triangle> outside an object")
				}
				var tr Tri
				for i, n := range []string{"v1", "v2", "v3"} {
					if tr[i], err = idx(t, n); err != nil {
						return nil, fmt.Errorf("threemf: triangle %d: %w", len(cur.Triangles), err)
					}
				}
				cur.Triangles = append(cur.Triangles, tr)
			case "item":
				id, err := idx(t, "objectid")
				if err != nil {
					return nil, err
				}
				m.Items = append(m.Items, Item{ObjectID: id})
			}
		case xml.CharData:
			if len(path) > 0 && path[len(path)-1] == "metadata" {
				text.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "metadata":
				if metaName == "Title" {
					m.Title = text.String()
				}
			case "object":
				cur = nil
			}
			path = path[:len(path)-1]
		}
	}
	if len(path) != 0 || m.Unit == "" {
		return nil, errors.New("threemf: the model is empty or truncated")
	}
	return m, nil
}
