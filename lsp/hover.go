package lsp

import (
	"encoding/json"
	"fmt"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

// index returns the symbol index of the current text, building it on first use.
func (d *document) index() *index {
	if d.ix == nil && d.file != nil {
		d.ix = buildIndex(d.file)
	}
	return d.ix
}

// nodeTable returns the evaluated nodes by name, built on first use.
func (d *document) nodeTable() map[string]*resolve.Node {
	if d.nodes != nil || d.resolved == nil {
		return d.nodes
	}
	d.nodes = map[string]*resolve.Node{}
	add := func(prefix string, n *resolve.Node) {
		if n == nil {
			return
		}
		for _, name := range []string{n.Name, n.Pair} {
			if name == "" {
				continue
			}
			if _, dup := d.nodes[prefix+name]; !dup {
				d.nodes[prefix+name] = n
			}
		}
	}
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		if n == nil {
			return
		}
		add("", n)
		for _, c := range n.Children {
			walk(c)
		}
		for _, c := range n.Else {
			walk(c)
		}
	}
	r := d.resolved
	walk(r.Root)
	for _, n := range r.Definitions {
		walk(n)
	}
	for _, n := range r.Collision {
		walk(n)
	}
	for _, n := range r.Plugs {
		walk(n)
	}
	for _, n := range r.Materials {
		add("material:", n)
	}
	return d.nodes
}

func (s *Server) hover(params json.RawMessage) (any, *respError) {
	var p textDocumentPosition
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	d := s.doc(p.TextDocument.URI)
	if d == nil || d.file == nil {
		return nil, nil
	}
	text, sp := d.hoverAt(d.offset(p.Position))
	if text == "" {
		return nil, nil
	}
	r := d.rangeOf(sp)
	return Hover{Contents: MarkupContent{Kind: "markdown", Value: text}, Range: &r}, nil
}

// hoverAt builds the hover text for an offset and the span it describes.
func (d *document) hoverAt(off int) (string, diag.Span) {
	var parts []string
	sp := diag.Span{Start: off, End: off}
	ix := d.index()
	if ix != nil {
		if t, tsp := d.dataFieldHover(ix, off); t != "" {
			sp = tsp
			parts = append(parts, t)
		} else if i := ix.refAt(off); i >= 0 {
			r := ix.refs[i]
			sp = r.Span
			if r.Target >= 0 {
				parts = append(parts, d.describe(ix.decls[r.Target]))
			} else if t := d.describeBuiltin(r); t != "" {
				parts = append(parts, t)
			}
		} else if i := ix.declAt(off); i >= 0 {
			sp = ix.decls[i].Span
			parts = append(parts, d.describe(ix.decls[i]))
		} else if f, ok := ix.fieldAt(off); ok {
			if t := fieldHover(f.Kind, f.Name); t != "" {
				sp = f.Span
				parts = append(parts, t)
			}
		}
	}
	for _, x := range d.diags {
		if off >= x.Span.Start && (off < x.Span.End || off == x.Span.Start && x.Span.Len() == 0) {
			if len(parts) == 0 {
				sp = x.Span
			}
			parts = append(parts, codeDoc(x.Code, d.severityName(x)))
		}
	}
	if len(parts) == 0 {
		if code, csp := d.codeAt(off); code != "" {
			if e, ok := diag.Lookup(code); ok {
				sp = csp
				parts = append(parts, codeDoc(code, e.Severity.String()))
			}
		}
	}
	return strings.Join(parts, "\n\n---\n\n"), sp
}

func (d *document) severityName(x diag.Diagnostic) string { return x.Severity.String() }

func codeDoc(code, severity string) string {
	doc := "A Greenware diagnostic."
	if e, ok := diag.Lookup(code); ok {
		doc = e.Doc
	}
	return fmt.Sprintf("**%s** (%s)\n\n%s", code, severity, doc)
}

// codeAt finds a diagnostic code such as GW-UNKNOWN-FIELD in the text around
// the offset, for a code that sits in a comment or a message.
func (d *document) codeAt(off int) (string, diag.Span) {
	isCode := func(c byte) bool { return c == '-' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' }
	s, e := off, off
	for s > 0 && isCode(d.text[s-1]) {
		s--
	}
	for e < len(d.text) && isCode(d.text[e]) {
		e++
	}
	word := string(d.text[s:e])
	if strings.HasPrefix(word, "GW-") {
		return word, diag.Span{Start: s, End: e}
	}
	return "", diag.Span{}
}

func (d *document) describeBuiltin(r ref) string {
	if r.Field != "func" {
		return ""
	}
	if f, ok := units.LookupFunc(r.Name); ok {
		return "**function** `" + f.Signature + "`"
	}
	return ""
}

// sourceLine returns the first line of the source in a span, trimmed.
func (d *document) sourceLine(sp diag.Span) string {
	if sp.Start < 0 || sp.End > len(d.text) || sp.Start > sp.End {
		return ""
	}
	s := string(d.text[sp.Start:sp.End])
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func (d *document) describe(x decl) string {
	switch x.Kind {
	case "param":
		return d.describeParam(x)
	case "let":
		return d.describeLet(x)
	case "template":
		return d.describeTemplate(x)
	case "output":
		return d.describeOutput(x)
	case "part", "material", "shape", "socket", "collider", "array":
		if t := d.describeNode(x); t != "" {
			return t
		}
	}
	line := d.sourceLine(x.Whole)
	if len(line) > 120 {
		line = line[:120] + "..."
	}
	return fmt.Sprintf("**%s** `%s`\n\n```greenware\n%s\n```", x.Kind, x.Name, line)
}

func unitName(dim units.Dim) string {
	switch dim {
	case units.Length:
		return "metre (m)"
	case units.Angle:
		return "degree (deg); the compiler keeps radians"
	case units.Duration:
		return "second (s)"
	case units.Bytes:
		return "byte (B)"
	case units.Number:
		return "none"
	}
	return dim.String()
}

func (d *document) describeParam(x decl) string {
	var ap *ast.Param
	for _, p := range d.file.Params {
		if p.Name.Name == x.Name {
			ap = p
			break
		}
	}
	var rp *resolve.Param
	if d.resolved != nil {
		for i := range d.resolved.Params {
			if d.resolved.Params[i].Name == x.Name {
				rp = &d.resolved.Params[i]
				break
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**param** `%s`\n\n", x.Name)
	typeName := ""
	if ap != nil {
		typeName = ap.Type.Name
	} else if rp != nil {
		typeName = rp.Type
	}
	if typeName != "" {
		line := "- **Type:** " + typeName
		if t, ok := registry.ParamType(typeName); ok {
			if dim, ok := t.Dim(); ok {
				line += "; unit: " + unitName(dim)
			}
		}
		if rp != nil && len(rp.Members) > 0 {
			line = "- **Type:** enum; members: " + strings.Join(rp.Members, ", ")
		}
		b.WriteString(line + "\n")
	}
	if rp != nil {
		if rp.Value.Valid() {
			fmt.Fprintf(&b, "- **Default:** `%s`\n", rp.Value.Format())
		}
		if rp.Lo != nil && rp.Hi != nil {
			fmt.Fprintf(&b, "- **Range:** `%s..%s`\n", rp.Lo.Format(), rp.Hi.Format())
		}
		if rp.Runtime {
			b.WriteString("- **Binding:** runtime (the game can change it per instance)\n")
		} else {
			b.WriteString("- **Binding:** bake (fixed when the file compiles)\n")
		}
		if rp.Doc != "" {
			fmt.Fprintf(&b, "\n%s\n", rp.Doc)
		}
	} else if ap != nil {
		fmt.Fprintf(&b, "- **Binding:** %s\n", map[bool]string{true: "runtime", false: "bake"}[ap.Runtime])
		fmt.Fprintf(&b, "\n```greenware\n%s\n```\n", d.sourceLine(ap.Span))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (d *document) describeLet(x decl) string {
	src := ""
	for _, l := range d.file.Lets {
		if l.Name.Name == x.Name && l.Name.Span == x.Span && l.Value != nil {
			sp := l.Value.Span()
			if sp.End <= len(d.text) && sp.Start <= sp.End {
				src = strings.TrimSpace(string(d.text[sp.Start:sp.End]))
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**let** `%s`", x.Name)
	if src != "" {
		fmt.Fprintf(&b, " = `%s`", src)
	}
	if x.Top && d.resolved != nil {
		if v, ok := d.resolved.Let(x.Name); ok && v.Valid() {
			fmt.Fprintf(&b, "\n\n- **Value at the defaults:** `%s`", v.Format())
			if v.DependsOnRuntime() {
				b.WriteString("\n- **Binding:** depends on a runtime parameter")
			} else {
				b.WriteString("\n- **Binding:** bake")
			}
		}
	}
	return b.String()
}

func (d *document) describeTemplate(x decl) string {
	for _, t := range d.file.Templates {
		if t.Name.Name != x.Name {
			continue
		}
		var ps []string
		for _, p := range t.Params {
			s := p.Name.Name
			if p.Type.Name != "" {
				s += ": " + p.Type.Name
			}
			if p.Default != nil {
				sp := p.Default.Span()
				if sp.End <= len(d.text) && sp.Start <= sp.End {
					s += " = " + strings.TrimSpace(string(d.text[sp.Start:sp.End]))
				}
			}
			ps = append(ps, s)
		}
		return fmt.Sprintf("**template** `%s(%s)`\n\nA row template: it expands where a row calls it and makes no node of its own.", x.Name, strings.Join(ps, ", "))
	}
	return "**template** `" + x.Name + "`"
}

func (d *document) describeOutput(x decl) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**output** `%s`", x.Name)
	if d.resolved != nil {
		for _, o := range d.resolved.Outputs {
			if o.Name != x.Name {
				continue
			}
			fmt.Fprintf(&b, "\n\n- **File:** `%s`\n- **Levels of detail:** %s", o.File, o.LODs)
			for _, f := range o.Set {
				fmt.Fprintf(&b, "\n- **set** `%s` = `%s`", f.Name, f.Value.Format())
			}
			break
		}
	}
	return b.String()
}

func (d *document) describeNode(x decl) string {
	table := d.nodeTable()
	var n *resolve.Node
	if x.Kind == "material" {
		n = table["material:"+x.Name]
	} else if c := table[x.Name]; c != nil {
		ok := false
		switch x.Kind {
		case "part", "socket", "array":
			ok = c.Kind == x.Kind
		default:
			ok = c.Kind != "part" && c.Kind != "socket" && c.Kind != "array"
		}
		if ok {
			n = c
		}
	}
	if n == nil {
		return ""
	}
	var b strings.Builder
	kind := n.Kind
	if x.Kind == "material" {
		fmt.Fprintf(&b, "**material** `%s` (%s)", x.Name, kind)
	} else if kind == x.Kind {
		fmt.Fprintf(&b, "**%s** `%s`", kind, x.Name)
	} else {
		fmt.Fprintf(&b, "**%s** `%s` (%s)", x.Kind, x.Name, kind)
	}
	const limit = 24
	for i, f := range n.Fields {
		if i == limit {
			fmt.Fprintf(&b, "\n- ... %d more fields", len(n.Fields)-limit)
			break
		}
		def := ""
		if f.Default {
			def = " (default)"
		}
		fmt.Fprintf(&b, "\n- `%s` = `%s`%s", f.Name, f.Value.Format(), def)
	}
	if n.Test != "" {
		fmt.Fprintf(&b, "\n- **when** `%s` (runtime)", n.Test)
	}
	if len(n.Children) > 0 {
		fmt.Fprintf(&b, "\n- %d child node(s)", len(n.Children))
	}
	return b.String()
}

// fieldHover describes a field of a block kind from the registry.
func fieldHover(kind, name string) string {
	for _, k := range registry.Kinds() {
		if k.Name != kind {
			continue
		}
		if f, ok := k.Field(name); ok {
			return describeField(k.Name, f)
		}
	}
	return ""
}

func describeField(kind string, f *registry.Field) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s.%s**: %s", kind, f.Name, f.Type.Name())
	if f.Doc != "" {
		fmt.Fprintf(&b, "\n\n%s", f.Doc)
	}
	switch {
	case f.Required:
		b.WriteString("\n\n- Required")
	case f.Default != nil:
		fmt.Fprintf(&b, "\n\n- Default: `%s`", f.Default.Format())
	case f.Derived != "":
		fmt.Fprintf(&b, "\n\n- Default: %s", f.Derived)
	}
	if f.Positive {
		b.WriteString("\n- Must be greater than zero")
	} else if f.NonNegative {
		b.WriteString("\n- Must be zero or more")
	}
	if f.Min != nil || f.Max != nil {
		lo, hi := "", ""
		if f.Min != nil {
			lo = fmt.Sprint(*f.Min)
		}
		if f.Max != nil {
			hi = fmt.Sprint(*f.Max)
		}
		fmt.Fprintf(&b, "\n- Range: %s..%s", lo, hi)
	}
	if f.Runtime {
		b.WriteString("\n- May depend on a runtime parameter")
	}
	if f.Unsupported != "" {
		fmt.Fprintf(&b, "\n- Not supported by this compiler: %s", f.Unsupported)
	}
	return b.String()
}

// dataFieldHover describes a field of a data package that the offset names:
// its dimension and value as written, the meaning the schema gives it, and the
// package, profile and pin.
func (d *document) dataFieldHover(ix *index, off int) (string, diag.Span) {
	q, ok := ix.qualifiedAt(off)
	if !ok || d.resolved == nil {
		return "", diag.Span{}
	}
	for _, di := range d.resolved.Data {
		if di.Qualifier != q.Parts[0] || di.Pkg == nil {
			continue
		}
		v, ok := di.Pkg.Value(q.Parts[1:]...)
		if !ok {
			return "", diag.Span{}
		}
		written := v.Text
		if v.Unit != "" {
			written += " " + v.Unit
		}
		text := fmt.Sprintf("**%s** : %s = %s\n\n%s\n\ndata package `%s`, profile `%s`, sha256 `%s`", strings.Join(q.Parts, "."), v.Dimension, written, v.Meaning, di.Contract, di.Profile, di.SHA256)
		return text, q.Span
	}
	return "", diag.Span{}
}
