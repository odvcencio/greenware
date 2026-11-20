// Package resolve checks a lowered file and evaluates it.
//
// Evaluate binds every name, orders lets by their dependencies (a let may use
// one declared later; a cycle is an error), evaluates every constant
// expression at the parameter values in force, and returns concrete values:
// lengths in metres and angles in radians, as float64. It reports the
// diagnostics of the front end: unknown names and fields, duplicates, type
// and unit errors, parameter ranges, failed requirements and the budget
// limits that the source alone decides.
//
// Arrays and mirrors are not expanded here. A resolved array or mirror keeps
// its fields and its contents as written, evaluated for the first copy:
// `side` is 1 inside a mirror and an array's `index` is 0.
package resolve

import (
	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/units"
)

// Field is one evaluated field of a node.
type Field struct {
	Name  string      `json:"name"`
	Value units.Value `json:"value"`
	// Default is true when the source did not set the field and the value
	// is the registry default.
	Default bool `json:"default,omitempty"`
}

// Row is one evaluated row, such as a point of a path or a lathe profile.
type Row struct {
	Name   string        `json:"name"`
	Values []units.Value `json:"values"`
}

// Node is one evaluated declaration: a part, shape, socket, material, level
// of detail or collider.
type Node struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	// Pair is the reflected name of a declaration inside a mirror.
	Pair   string  `json:"pair,omitempty"`
	Fields []Field `json:"fields,omitempty"`
	Rows   []Row   `json:"rows,omitempty"`
	// Condition is set on a `when` node that depends on a runtime parameter:
	// its value at the defaults. A bake `when` leaves no node; only the
	// chosen branch remains.
	Condition *units.Value `json:"condition,omitempty"`
	// Test is the runtime condition as source writes it (`facemask == cage`);
	// Cond is the same in structured form. Both are set on a runtime `when`.
	Test     string  `json:"test,omitempty"`
	Cond     *Cond   `json:"-"`
	Children []*Node `json:"children,omitempty"`
	Else     []*Node `json:"else,omitempty"`
	// Loft carries the points of a loft whose rule is an author's `vertex`
	// expression, so a level can evaluate them at its own column count.
	Loft *LoftRule `json:"-"`
}

// Get returns a field's value.
func (n *Node) Get(name string) (units.Value, bool) {
	for _, f := range n.Fields {
		if f.Name == name {
			return f.Value, true
		}
	}
	return units.Bad(), false
}

// Param is an evaluated parameter.
type Param struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Members lists the members of an enum parameter, in declaration order.
	Members []string     `json:"members,omitempty"`
	Runtime bool         `json:"runtime,omitempty"`
	Value   units.Value  `json:"value"`
	Lo      *units.Value `json:"lo,omitempty"`
	Hi      *units.Value `json:"hi,omitempty"`
	Doc     string       `json:"doc,omitempty"`
	// Overridden is true when an override, not the default, gave the value.
	Overridden bool `json:"overridden,omitempty"`
}

// Let is an evaluated top-level let.
type Let struct {
	Name  string      `json:"name"`
	Value units.Value `json:"value"`
}

// Output describes an output declaration.
type Output struct {
	Name     string  `json:"name"`
	File     string  `json:"file"`
	LODs     string  `json:"lods"`
	Manifest bool    `json:"manifest"`
	Profile  string  `json:"profile"`
	Set      []Field `json:"set,omitempty"`
	Budget   []Field `json:"budget,omitempty"`
	// Mounts is "runtime" or "bake" when the output overrides its mounts' modes.
	Mounts string `json:"mounts,omitempty"`
	// ForEach names the table of a table-driven output.
	ForEach string `json:"for_each,omitempty"`
}

// Look counts what the source decides about the drawn result, without
// building geometry: the distinct materials in use and the draws that
// merging by exported node and material gives.
type Look struct {
	// Materials lists the materials reachable in any look, in declaration order.
	Materials      []string `json:"materials"`
	DrawCalls      int      `json:"draw_calls"`
	DrawCallsWorst int      `json:"draw_calls_worst"`
}

// Resolved is the result of evaluating a file at one choice of parameters.
type Resolved struct {
	// Output is the output the values were chosen for; empty for the defaults.
	Output    string  `json:"output,omitempty"`
	Asset     []Field `json:"asset"`
	Meta      []Field `json:"meta,omitempty"`
	Params    []Param `json:"params"`
	Lets      []Let   `json:"lets"`
	Materials []*Node `json:"materials"`
	// Imports are the libraries the file imports, with their pins.
	Imports []Import `json:"imports,omitempty"`
	// Textures are the atlas declarations, each with its cells as children.
	Textures []*Node `json:"textures,omitempty"`
	// Atlases are the atlases in a form that can be baked.
	Atlases []*Atlas `json:"-"`
	// Plugs are the asset's plugs.
	Plugs       []*Node `json:"plugs,omitempty"`
	Root        *Node   `json:"root"`
	Definitions []*Node `json:"definitions,omitempty"`
	Lods        []*Node `json:"lods,omitempty"`
	Collision   []*Node `json:"collision,omitempty"`
	// Rig is the evaluated skeleton, skin, morphs and clips; nil when the
	// file declares none.
	Rig *Rig `json:"rig,omitempty"`
	// Gears are the project files the file imports; Mounts the resolved mounts.
	Gears  []GearImport `json:"gears,omitempty"`
	Mounts []Mount      `json:"mounts,omitempty"`
	// RigCheck and RuntimeCheck are `check rig` and `check runtime`.
	RigCheck     *RigCheck     `json:"rig_check,omitempty"`
	RuntimeCheck *RuntimeCheck `json:"runtime_check,omitempty"`
	// Pins are the project files and table files read, sorted by path.
	Pins []ast.Pin `json:"pins,omitempty"`
	// Budget is the effective budget: the document's, with the output's
	// fields replacing the matching ones.
	Budget  []Field  `json:"budget,omitempty"`
	Look    Look     `json:"look"`
	Outputs []Output `json:"outputs"`
}

// Param returns the named parameter's value.
func (r *Resolved) Param(name string) (units.Value, bool) {
	for _, p := range r.Params {
		if p.Name == name {
			return p.Value, true
		}
	}
	return units.Bad(), false
}

// Let returns the named let's value.
func (r *Resolved) Let(name string) (units.Value, bool) {
	for _, l := range r.Lets {
		if l.Name == name {
			return l.Value, true
		}
	}
	return units.Bad(), false
}

// Override replaces a parameter's value. Give Expr (evaluated in the
// document scope at the defaults) or Value. NameSpan and Span locate
// diagnostics: they may be zero for an override that does not come from
// source.
type Override struct {
	Name     string
	NameSpan diag.Span
	Expr     ast.Expr
	Value    *units.Value
	Span     diag.Span
}

// Evaluate evaluates the file with the overrides applied to the parameters.
// With no overrides it gives the default values. It also checks the `set`
// blocks of the outputs for known names, types and ranges, without building
// each output's full result; EvaluateAll does that.
func Evaluate(f *ast.File, overrides []Override) (*Resolved, []diag.Diagnostic) {
	return evaluate(f, "", overrides, true, nil)
}

// EvaluateOutput evaluates the file with the named output's `set` block as
// overrides and its own budget applied. A table-driven output is named
// `output[key]`, one per row (see Outputs).
func EvaluateOutput(f *ast.File, name string) (*Resolved, []diag.Diagnostic) {
	ref, ds := lookupOutput(f, name)
	if ref == nil {
		return nil, append(ds, diag.New(diag.CodeUnknownName, diag.Span{}, "the file has no output named %q", name))
	}
	return evaluateRef(f, ref, false, ds)
}

func evaluateRef(f *ast.File, ref *OutputRef, shallow bool, pre []diag.Diagnostic) (*Resolved, []diag.Diagnostic) {
	ovs, ds := ref.overrides()
	r, more := evaluate(f, ref.Name, ovs, shallow, ref)
	return r, diag.Dedup(append(append(pre, ds...), more...))
}

// EvaluateAll evaluates the defaults and then every output. The first result
// is the defaults; the rest follow the order of the outputs, a table-driven
// output once per row. The diagnostics are merged, sorted and free of repeats.
func EvaluateAll(f *ast.File) ([]*Resolved, []diag.Diagnostic) {
	var out []*Resolved
	var all []diag.Diagnostic
	d, ds := Evaluate(f, nil)
	out, all = append(out, d), append(all, ds...)
	refs, ds := Outputs(f)
	all = append(all, ds...)
	for i := range refs {
		r, ds := evaluateRef(f, &refs[i], false, nil)
		out, all = append(out, r), append(all, ds...)
	}
	return out, diag.Dedup(all)
}
