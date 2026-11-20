package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

// Setting is one `name = value` pair, the value as source text with layout
// and comments removed.
type Setting struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ParamInfo describes one parameter.
type ParamInfo struct {
	Name string `json:"name"`
	Line int    `json:"line"`
	Type string `json:"type"`
	// Unit is the SI unit of the evaluated value ("m", "rad", "s", "B"), or
	// "" for a type with no unit.
	Unit string `json:"unit"`
	// Default is the default as source text; Value is it evaluated, in SI
	// units; both are empty when the default does not evaluate.
	Default string       `json:"default"`
	Value   *units.Value `json:"value,omitempty"`
	Lo      string       `json:"lo,omitempty"`
	Hi      string       `json:"hi,omitempty"`
	LoValue *units.Value `json:"lo_value,omitempty"`
	HiValue *units.Value `json:"hi_value,omitempty"`
	// Binding is "bake" (fixed when the file compiles) or "runtime".
	Binding string `json:"binding"`
	Doc     string `json:"doc,omitempty"`
}

// ShapeInfo names a shape of a part.
type ShapeInfo struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}

// PartInfo is a node of the part tree.
type PartInfo struct {
	Name string `json:"name"`
	Pair string `json:"pair,omitempty"`
	Line int    `json:"line"`
	// Role is "root" for the asset's root part, "template" for another
	// top-level part, and "part" for a nested one.
	Role string `json:"role"`
	// Condition is the `when` condition (or "else <condition>") that makes
	// the part present; Mirror is the mirror plane of a mirrored pair.
	Condition string      `json:"condition,omitempty"`
	Mirror    string      `json:"mirror,omitempty"`
	Settings  []Setting   `json:"settings"`
	Shapes    []ShapeInfo `json:"shapes"`
	// Body is the first 12 hex digits of the SHA-256 of the part's own text
	// (children and name left out, layout and comments removed).
	Body     string     `json:"body"`
	Children []PartInfo `json:"children"`
}

// MaterialInfo describes one material.
type MaterialInfo struct {
	Name     string    `json:"name"`
	Line     int       `json:"line"`
	Kind     string    `json:"kind"`
	Settings []Setting `json:"settings"`
	Fallback []Setting `json:"fallback,omitempty"`
}

// SocketInfo is a socket of a part.
type SocketInfo struct {
	Name string `json:"name"`
	Pair string `json:"pair,omitempty"`
	Line int    `json:"line"`
	Part string `json:"part"`
	Kind string `json:"kind,omitempty"`
	At   string `json:"at,omitempty"`
}

// NamedInfo is a named declaration with its settings: plugs, mounts, tables,
// colliders, textures, templates, painters and levels.
type NamedInfo struct {
	Name     string    `json:"name"`
	Line     int       `json:"line"`
	Kind     string    `json:"kind,omitempty"`
	Settings []Setting `json:"settings"`
}

// OutputInfo describes one output.
type OutputInfo struct {
	Name string `json:"name"`
	Line int    `json:"line"`
	File string `json:"file,omitempty"`
	// Lods is the `lods` setting (files, combined, none); Levels lists the
	// level numbers the output has: 0 and every declared level.
	Lods     string    `json:"lods,omitempty"`
	Levels   []int     `json:"levels"`
	Settings []Setting `json:"settings"`
	Set      []Setting `json:"set"`
	Budget   []Setting `json:"budget"`
}

// RigInfo describes a skeleton, skin, morph or clip declaration.
type RigInfo struct {
	Kind     string    `json:"kind"`
	Name     string    `json:"name"`
	Line     int       `json:"line"`
	Settings []Setting `json:"settings"`
	Joints   []string  `json:"joints,omitempty"`
	Body     string    `json:"body"`
}

// ImportInfo is an import.
type ImportInfo struct {
	Path      string `json:"path"`
	Qualifier string `json:"qualifier"`
	Line      int    `json:"line"`
}

// Summary is what describe reports and diff compares.
type Summary struct {
	Asset      []Setting      `json:"asset"`
	Meta       []Setting      `json:"meta"`
	Imports    []ImportInfo   `json:"imports"`
	Params     []ParamInfo    `json:"params"`
	Outputs    []OutputInfo   `json:"outputs"`
	Levels     []NamedInfo    `json:"levels"`
	Parts      []PartInfo     `json:"parts"`
	Materials  []MaterialInfo `json:"materials"`
	Sockets    []SocketInfo   `json:"sockets"`
	Plugs      []NamedInfo    `json:"plugs"`
	Mounts     []NamedInfo    `json:"mounts"`
	Colliders  []NamedInfo    `json:"colliders"`
	Rigs       []RigInfo      `json:"rigs"`
	Tables     []NamedInfo    `json:"tables"`
	Templates  []NamedInfo    `json:"templates"`
	Painters   []NamedInfo    `json:"painters"`
	Textures   []NamedInfo    `json:"textures"`
	Budget     []Setting      `json:"budget"`
	Evaluated  bool           `json:"evaluated"`
	Unresolved []string       `json:"unresolved"`
}

type summarizer struct {
	u    *Unit
	src  []byte
	sum  *Summary
	root string
}

func (s *summarizer) line(sp diag.Span) int {
	l, _ := s.u.Loc(sp.Start)
	return l
}

func (s *summarizer) settings(ss []*ast.Setting) []Setting {
	out := []Setting{}
	for _, st := range ss {
		out = append(out, Setting{st.Name, s.settingText(st)})
	}
	return out
}

func (s *summarizer) settingText(st *ast.Setting) string {
	switch {
	case st.Value != nil:
		return norm(srcText(s.src, st.Value.Span()))
	case st.Range != nil:
		return norm(srcText(s.src, st.Range.Span))
	}
	return ""
}

func (s *summarizer) text(e ast.Expr) string {
	if e == nil {
		return ""
	}
	return norm(srcText(s.src, e.Span()))
}

func settingValue(ss []Setting, name string) string {
	for _, s := range ss {
		if s.Name == name {
			return s.Value
		}
	}
	return ""
}

var siUnit = map[string]string{"length": "m", "angle": "rad", "duration": "s", "bytes": "B"}

// Summarize runs the front end's declarations into a Summary. It reads
// declarations and the evaluated defaults; it builds no geometry.
func Summarize(u *Unit) *Summary {
	f := u.File
	s := &summarizer{u: u, src: u.Src, sum: &Summary{}}
	sum := s.sum
	sum.Asset, sum.Meta, sum.Budget = []Setting{}, []Setting{}, []Setting{}
	sum.Imports, sum.Params, sum.Outputs, sum.Levels = []ImportInfo{}, []ParamInfo{}, []OutputInfo{}, []NamedInfo{}
	sum.Parts, sum.Materials, sum.Sockets, sum.Plugs = []PartInfo{}, []MaterialInfo{}, []SocketInfo{}, []NamedInfo{}
	sum.Mounts, sum.Colliders, sum.Rigs, sum.Tables = []NamedInfo{}, []NamedInfo{}, []RigInfo{}, []NamedInfo{}
	sum.Templates, sum.Painters, sum.Textures, sum.Unresolved = []NamedInfo{}, []NamedInfo{}, []NamedInfo{}, []string{}
	if f.Asset != nil {
		sum.Asset = s.settings(f.Asset.Settings)
		s.root = settingValue(sum.Asset, "root")
	}
	if f.Meta != nil {
		sum.Meta = s.settings(f.Meta.Settings)
	}
	if f.Budget != nil {
		sum.Budget = s.settings(f.Budget.Settings)
	}
	for _, im := range f.Imports {
		sum.Imports = append(sum.Imports, ImportInfo{im.Path, im.Qualifier, s.line(im.Span)})
	}

	var r *resolve.Resolved
	if !f.Broken {
		if res, ds := resolve.Evaluate(f, nil); res != nil && !diag.HasErrors(ds) {
			r = res
			sum.Evaluated = true
		}
	}
	for _, p := range f.Params {
		pi := ParamInfo{Name: p.Name.Name, Line: s.line(p.Span), Type: paramType(p.Type), Default: s.text(p.Default), Binding: "bake"}
		pi.Unit = siUnit[p.Type.Name]
		if p.Runtime {
			pi.Binding = "runtime"
		}
		if p.Range != nil {
			pi.Lo, pi.Hi = s.text(p.Range.Lo), s.text(p.Range.Hi)
		}
		if d := ast.FindSetting(p.Settings, "doc"); d != nil {
			if sl, ok := d.Value.(*ast.StringLit); ok {
				pi.Doc = sl.Value
			}
		}
		if r != nil {
			for _, rp := range r.Params {
				if rp.Name == p.Name.Name {
					v := rp.Value
					pi.Value = &v
					pi.LoValue, pi.HiValue = rp.Lo, rp.Hi
				}
			}
		}
		sum.Params = append(sum.Params, pi)
	}
	for _, m := range f.Materials {
		mi := MaterialInfo{Name: m.Name.Name, Line: s.line(m.Span), Kind: m.Kind, Settings: s.settings(m.Settings)}
		if m.Fallback != nil {
			mi.Fallback = s.settings(m.Fallback.Settings)
		}
		sum.Materials = append(sum.Materials, mi)
	}
	for _, p := range f.Parts {
		role := "template"
		if p.Name.Name == s.root {
			role = "root"
		}
		sum.Parts = append(sum.Parts, s.part(p, role, "", "", ""))
	}
	for _, l := range f.Lods {
		sum.Levels = append(sum.Levels, NamedInfo{Name: strconv.Itoa(l.Level), Line: s.line(l.Span), Settings: s.settings(l.Settings)})
	}
	levels := []int{0}
	for _, l := range f.Lods {
		levels = append(levels, l.Level)
	}
	sort.Ints(levels)
	for _, o := range f.Outputs {
		oi := OutputInfo{Name: o.Name.Name, Line: s.line(o.Span), Settings: s.settings(o.Settings), Set: s.settings(o.Set), Budget: []Setting{}, Levels: levels}
		oi.File = trimQuote(settingValue(oi.Settings, "file"))
		oi.Lods = settingValue(oi.Settings, "lods")
		if o.Budget != nil {
			oi.Budget = s.settings(o.Budget.Settings)
		}
		sum.Outputs = append(sum.Outputs, oi)
	}
	for _, p := range f.Plugs {
		sum.Plugs = append(sum.Plugs, NamedInfo{Name: p.Name.Name, Line: s.line(p.Span), Settings: s.settings(p.Settings)})
	}
	for _, m := range f.Mounts {
		ss := s.settings(m.Settings)
		for _, st := range s.settings(m.Set) {
			ss = append(ss, Setting{"set." + st.Name, st.Value})
		}
		sum.Mounts = append(sum.Mounts, NamedInfo{Name: m.Name.Name, Line: s.line(m.Span), Settings: ss})
	}
	if f.Collision != nil {
		s.colliders(f.Collision.Items)
	}
	for _, rd := range f.Rigs {
		ri := RigInfo{Kind: rd.Kind, Name: rd.Name.Name, Line: s.line(rd.Span), Settings: s.settings(rd.Settings), Body: bodyHash(norm(srcText(s.src, rd.Span)))}
		s.joints(rd.Items, &ri)
		sum.Rigs = append(sum.Rigs, ri)
	}
	for _, t := range f.Tables {
		sum.Tables = append(sum.Tables, NamedInfo{Name: t.Name.Name, Kind: t.Kind.Name, Line: s.line(t.Span), Settings: s.settings(t.Settings)})
	}
	for _, t := range f.Templates {
		sum.Templates = append(sum.Templates, NamedInfo{Name: t.Name.Name, Line: s.line(t.Span), Settings: []Setting{{"body", bodyHash(norm(srcText(s.src, t.Span)))}}})
	}
	for _, p := range f.Painters {
		sum.Painters = append(sum.Painters, NamedInfo{Name: p.Name.Name, Line: s.line(p.Span), Settings: []Setting{{"body", bodyHash(norm(srcText(s.src, p.Span)))}}})
	}
	for _, t := range f.Textures {
		sum.Textures = append(sum.Textures, NamedInfo{Name: t.Name.Name, Kind: t.Kind, Line: s.line(t.Span), Settings: s.settings(t.Settings)})
	}
	if !sum.Evaluated {
		sum.Unresolved = append(sum.Unresolved, "the defaults do not evaluate; values are source text only")
	}
	return sum
}

func paramType(t ast.ParamType) string {
	if len(t.Enum) == 0 {
		return t.Name
	}
	names := make([]string, len(t.Enum))
	for i, e := range t.Enum {
		names[i] = e.Name
	}
	return t.Name + "(" + strings.Join(names, ", ") + ")"
}

func trimQuote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func bodyHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:12]
}

func (s *summarizer) colliders(items []ast.Item) {
	for _, it := range items {
		switch n := it.(type) {
		case *ast.Collider:
			s.sum.Colliders = append(s.sum.Colliders, NamedInfo{Name: n.Name.Name, Kind: n.Kind, Line: s.line(n.Span), Settings: s.settings(n.Settings)})
		case *ast.Mirror:
			s.colliders(n.Items)
		}
	}
}

func (s *summarizer) joints(items []ast.RigItem, ri *RigInfo) {
	for _, it := range items {
		if b, ok := it.(*ast.RigBlock); ok {
			if b.Kind == "joint" && b.Name.Name != "" {
				ri.Joints = append(ri.Joints, b.Name.Name)
			}
			s.joints(b.Items, ri)
		}
	}
}

// part builds the node of a part; condition and mirror come from the
// containers it was found in.
func (s *summarizer) part(p *ast.Part, role, parent, condition, mirror string) PartInfo {
	pi := PartInfo{Name: p.Name.Name, Pair: p.Pair.Name, Line: s.line(p.Span), Role: role, Condition: condition, Mirror: mirror,
		Settings: s.settings(p.Settings), Shapes: []ShapeInfo{}, Children: []PartInfo{}}
	cut := []diag.Span{p.Name.Span}
	if p.Pair.Name != "" {
		cut = append(cut, p.Pair.Span)
	}
	s.partItems(p, p.Items, &pi, &cut, "", "")
	pi.Body = bodyHash(norm(excise(s.src, p.Span, cut)))
	return pi
}

func (s *summarizer) partItems(p *ast.Part, items []ast.Item, pi *PartInfo, cut *[]diag.Span, cond, mirror string) {
	for _, it := range items {
		switch n := it.(type) {
		case *ast.Part:
			*cut = append(*cut, n.Span)
			pi.Children = append(pi.Children, s.part(n, "part", p.Name.Name, cond, mirror))
		case *ast.Shape:
			pi.Shapes = append(pi.Shapes, ShapeInfo{Kind: n.Kind, Name: n.Name.Name})
		case *ast.Array:
			s.partItems(p, n.Items, pi, cut, cond, mirror)
		case *ast.Mirror:
			plane := ""
			for _, st := range s.settings(n.Settings) {
				if st.Name == "plane" {
					plane = st.Value
				}
			}
			s.partItems(p, n.Items, pi, cut, cond, plane)
		case *ast.When:
			c := s.text(n.Cond)
			s.partItems(p, n.Then, pi, cut, c, mirror)
			s.partItems(p, n.Else, pi, cut, "else "+c, mirror)
		case *ast.Socket:
			ss := s.settings(n.Settings)
			s.sum.Sockets = append(s.sum.Sockets, SocketInfo{Name: n.Name.Name, Pair: n.Pair.Name, Line: s.line(n.Span), Part: p.Name.Name,
				Kind: settingValue(ss, "kind"), At: settingValue(ss, "at")})
		}
	}
}

// excise returns the text of sp with the cut spans removed.
func excise(src []byte, sp diag.Span, cut []diag.Span) string {
	cs := append([]diag.Span(nil), cut...)
	sort.Slice(cs, func(i, j int) bool { return cs[i].Start < cs[j].Start })
	var b strings.Builder
	pos := sp.Start
	for _, c := range cs {
		if c.Start < pos || c.End > sp.End {
			continue
		}
		b.WriteString(srcText(src, diag.Span{Start: pos, End: c.Start}))
		b.WriteByte(' ')
		pos = c.End
	}
	b.WriteString(srcText(src, diag.Span{Start: pos, End: sp.End}))
	return b.String()
}
