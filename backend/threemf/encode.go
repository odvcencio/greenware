package threemf

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"strconv"
)

const xmlHeader = `<?xml version="1.0" encoding="UTF-8"?>` + "\n"

const contentTypes = xmlHeader +
	`<Types xmlns="` + typesNamespace + `">` +
	`<Default Extension="rels" ContentType="` + relsType + `"/>` +
	`<Default Extension="model" ContentType="` + modelType + `"/>` +
	`</Types>` + "\n"

const rootRels = xmlHeader +
	`<Relationships xmlns="` + relsNamespace + `">` +
	`<Relationship Target="/` + ModelPath + `" Id="rel0" Type="` + modelRelType + `"/>` +
	`</Relationships>` + "\n"

// Encode writes the model as a 3MF package. The same model gives the same
// bytes on every run and architecture. It refuses a model that is not in
// millimetres, has a coordinate that is not finite, or has object ids that do
// not strictly increase from 1 up; it does not check that meshes are solids
// (see Validate).
func Encode(m *Model) ([]byte, error) {
	if m.Unit != Millimeter {
		return nil, fmt.Errorf("threemf: unit %q: only %q is written", m.Unit, Millimeter)
	}
	prev := 0
	for i := range m.Objects {
		o := &m.Objects[i]
		if o.ID <= prev {
			return nil, fmt.Errorf("threemf: object id %d follows %d; ids must strictly increase from 1", o.ID, prev)
		}
		prev = o.ID
		for _, p := range o.Vertices {
			for _, v := range p {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					return nil, ErrNotFinite
				}
			}
		}
	}
	var b bytes.Buffer
	b.WriteString(xmlHeader)
	b.WriteString(`<model unit="` + m.Unit + `" xml:lang="en-US" xmlns="` + CoreNamespace + `">` + "\n")
	if m.Title != "" {
		b.WriteString(` <metadata name="Title">`)
		escape(&b, m.Title)
		b.WriteString("</metadata>\n")
	}
	b.WriteString(` <metadata name="Application">greenware</metadata>` + "\n")
	b.WriteString(" <resources>\n")
	for i := range m.Objects {
		o := &m.Objects[i]
		b.WriteString(`  <object id="` + strconv.Itoa(o.ID) + `" name="`)
		escape(&b, o.Name)
		b.WriteString(`" type="model">` + "\n")
		b.WriteString("   <mesh>\n    <vertices>\n")
		for _, p := range o.Vertices {
			b.WriteString(`     <vertex x="` + FormatMM(p[0]) + `" y="` + FormatMM(p[1]) + `" z="` + FormatMM(p[2]) + `"/>` + "\n")
		}
		b.WriteString("    </vertices>\n    <triangles>\n")
		for _, t := range o.Triangles {
			b.WriteString(`     <triangle v1="` + strconv.Itoa(t[0]) + `" v2="` + strconv.Itoa(t[1]) + `" v3="` + strconv.Itoa(t[2]) + `"/>` + "\n")
		}
		b.WriteString("    </triangles>\n   </mesh>\n  </object>\n")
	}
	b.WriteString(" </resources>\n <build>\n")
	for _, it := range m.Items {
		b.WriteString(`  <item objectid="` + strconv.Itoa(it.ObjectID) + `"/>` + "\n")
	}
	b.WriteString(" </build>\n</model>\n")
	return writeZip([]zipEntry{
		{Name: "[Content_Types].xml", Data: []byte(contentTypes)},
		{Name: "_rels/.rels", Data: []byte(rootRels)},
		{Name: ModelPath, Data: b.Bytes()},
	})
}

// escape writes s as XML attribute or text content. It also escapes the
// whitespace controls that an attribute would otherwise normalise away.
func escape(b *bytes.Buffer, s string) {
	var t bytes.Buffer
	_ = xml.EscapeText(&t, []byte(s)) // a bytes.Buffer does not fail
	b.Write(t.Bytes())
}
