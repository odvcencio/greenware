package ast

import "m31labs.dev/greenware/diag"

// File is one lowered source file.
type File struct {
	Source      []byte
	Edition     int
	EditionSpan diag.Span
	HasEdition  bool

	Asset   *Asset
	Meta    *Meta
	Imports []*Import
	// Mounts and Tables are the mount and table declarations, in source order.
	Mounts []*Mount
	Tables []*Table
	// Project is where this file's project imports and table data are read
	// from; nil when the file was lowered from bytes alone.
	Project   *Project
	Params    []*Param
	Lets      []*Let
	Checks    []*Check
	Materials []*Material
	// Parts are the top-level parts in source order. The asset's root names one;
	// the others are templates for instance.
	Parts     []*Part
	Lods      []*Lod
	Collision *Collision
	Budget    *Budget
	Outputs   []*Output
	Accepts   []*Accept
	// Templates are the row templates, in source order.
	Templates []*Template
	// Painters are the painters the file declares, in source order.
	Painters []*Painter
	// Textures are the texture declarations, in source order.
	Textures []*Texture
	// Plugs are the asset's plugs, in source order.
	Plugs []*Plug
	// Rigs are the skeleton, skin, morph and clip declarations, in source
	// order.
	Rigs []*RigDecl
	// Skipped lists the look declarations a geometry-only lowering left out,
	// each as a sentence for a person. It is empty otherwise.
	Skipped []string

	// Broken is true when the file had syntax errors. Evaluation of a broken
	// file reports nothing further, so a syntax error does not cascade.
	Broken bool
}

// Setting is `name = value`.
type Setting struct {
	Name     string
	NameSpan diag.Span
	Span     diag.Span
	// Value is the value expression; Range is set instead for `lo..hi`.
	Value Expr
	Range *Range
}

// Setting finds a setting by name.
func FindSetting(ss []*Setting, name string) *Setting {
	for _, s := range ss {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// Row is a row such as point (0, 1, 2).
type Row struct {
	Name     string
	NameSpan diag.Span
	Span     diag.Span
	Args     []Arg
}

// SampleRow is `sample v in lo..hi by n -> row`.
type SampleRow struct {
	Span  diag.Span
	Var   Ident
	Range *Range
	Count Expr
	Row   *Row
}

// RowItem is a Row or a SampleRow.
type RowItem interface{ rowItem() }

func (*Row) rowItem()       {}
func (*SampleRow) rowItem() {}

// Block is a nested block of a shape: bevel or path.
type Block struct {
	Kind     string
	KindSpan diag.Span
	Span     diag.Span
	Settings []*Setting
	Rows     []RowItem
}

// Asset is the asset declaration.
type Asset struct {
	Name     Ident
	Span     diag.Span
	Settings []*Setting
	// UseClips are the `use_clips { of = lib }` blocks, in source order.
	UseClips []*UseClips
}

// UseClips is `use_clips { of = qualifier skeleton = name }` inside an asset:
// it installs the clips of an imported clip library (a project file with
// `clip` declarations and no asset).
type UseClips struct {
	Span     diag.Span
	Settings []*Setting
}

// Meta is the meta declaration.
type Meta struct {
	Span     diag.Span
	Settings []*Setting
}

// ParamType is the type written after a parameter's colon.
type ParamType struct {
	Name string
	Span diag.Span
	// Enum lists the members of enum(a, b, ...).
	Enum []Ident
}

// Param is `param name : type = default [in lo..hi] [runtime] [{ doc = ... }]`.
type Param struct {
	Name     Ident
	Span     diag.Span
	Type     ParamType
	Default  Expr
	Range    *Range
	Runtime  bool
	Settings []*Setting
}

// Let is `let name = value`, at the top level or inside a body.
type Let struct {
	Name  Ident
	Span  diag.Span
	Value Expr
}

// Require is one `require (condition, "message")` row.
type Require struct {
	Span    diag.Span
	Cond    Expr
	Message Expr
}

// Check is a check block: `check params` holds require rows; `check rig` and
// `check runtime` hold settings, and `check runtime` named cases.
type Check struct {
	Name     Ident
	Span     diag.Span
	Requires []*Require
	Settings []*Setting
	Cases    []*CheckCase
}

// Material is a pbr or selena material. A selena material carries its
// inline pbr fallback.
type Material struct {
	Name     Ident
	Span     diag.Span
	Kind     string
	KindSpan diag.Span
	Settings []*Setting
	Fallback *Material
}

// Item is something a part, array, mirror or when body holds: *Part, *Shape,
// *Array, *Instance, *Mirror, *Socket, *When or *Let.
type Item interface{ ItemSpan() diag.Span }

// Part groups items. A name pair is only valid inside a mirror.
type Part struct {
	Name     Ident
	Pair     Ident
	Span     diag.Span
	Settings []*Setting
	Items    []Item
	TopLevel bool
}

// Shape is a shape such as cylinder, sphere, sweep or lathe.
type Shape struct {
	Kind     string
	KindSpan diag.Span
	Name     Ident
	Pair     Ident
	Span     diag.Span
	Settings []*Setting
	Rows     []RowItem
	Blocks   []*Block
}

// Array repeats its items.
type Array struct {
	Name     Ident
	Pair     Ident
	Span     diag.Span
	Settings []*Setting
	Items    []Item
}

// Instance places a top-level part.
type Instance struct {
	Name     Ident
	Pair     Ident
	Span     diag.Span
	Settings []*Setting
}

// Mirror keeps its items and adds a reflected copy.
type Mirror struct {
	Span     diag.Span
	Settings []*Setting
	Items    []Item
}

// Socket is a named point.
type Socket struct {
	Name     Ident
	Pair     Ident
	Span     diag.Span
	Settings []*Setting
}

// When includes Then if its condition holds, Else if it does not.
type When struct {
	Span diag.Span
	Cond Expr
	Then []Item
	Else []Item
}

// TemplateParam is one typed formal of a template: `name: type [= default]`.
type TemplateParam struct {
	Name    Ident
	Type    ParamType
	Default Expr
}

// Template is `template name(formals) { items }`: a reusable block that
// expands where a row calls it and makes no node of its own.
type Template struct {
	Name   Ident
	Span   diag.Span
	Params []TemplateParam
	Items  []Item
}

// TemplateCall is a row in a part body that calls a template:
// `rail (1.785, 0.198)`.
type TemplateCall struct {
	Name     string
	NameSpan diag.Span
	Span     diag.Span
	Args     []Arg
}

// Collider is a capsule, box or sphere in collision.
type Collider struct {
	Kind     string
	KindSpan diag.Span
	Name     Ident
	Pair     Ident
	Span     diag.Span
	Settings []*Setting
}

func (i *Part) ItemSpan() diag.Span         { return i.Span }
func (i *Shape) ItemSpan() diag.Span        { return i.Span }
func (i *Array) ItemSpan() diag.Span        { return i.Span }
func (i *Instance) ItemSpan() diag.Span     { return i.Span }
func (i *Mirror) ItemSpan() diag.Span       { return i.Span }
func (i *Socket) ItemSpan() diag.Span       { return i.Span }
func (i *When) ItemSpan() diag.Span         { return i.Span }
func (i *Let) ItemSpan() diag.Span          { return i.Span }
func (i *TemplateCall) ItemSpan() diag.Span { return i.Span }
func (i *Collider) ItemSpan() diag.Span     { return i.Span }

// Lod is a level of detail.
type Lod struct {
	Level     int
	LevelSpan diag.Span
	Span      diag.Span
	Settings  []*Setting
}

// Collision holds colliders and mirrors of colliders.
type Collision struct {
	Span  diag.Span
	Items []Item
}

// Budget is a set of limits.
type Budget struct {
	Span     diag.Span
	Settings []*Setting
}

// Output is an output declaration.
type Output struct {
	Name     Ident
	Span     diag.Span
	Settings []*Setting
	// Set holds the output's parameter overrides, in source order.
	Set     []*Setting
	SetSpan diag.Span
	Budget  *Budget
}

// Expect is one `expect (condition, "message")` row of an acceptance file.
type Expect struct {
	Span    diag.Span
	Cond    Expr
	Message Expr
}

// AcceptWhen is a `when` block of an acceptance file.
type AcceptWhen struct {
	Span diag.Span
	Cond Expr
	Then []AcceptItem
	Else []AcceptItem
}

// AcceptItem is an *Expect or an *AcceptWhen.
type AcceptItem interface{ acceptItem() }

func (*Expect) acceptItem()     {}
func (*AcceptWhen) acceptItem() {}

// Accept is an acceptance document. Its expressions are kept as written; they
// run against decoded files in another package.
type Accept struct {
	Name     Ident
	Span     diag.Span
	Settings []*Setting
	Items    []AcceptItem
}

// Import is `import "path" [as name]`. Qualifier is the name the file's
// qualified references use: the last component of the path unless `as` gives
// another.
type Import struct {
	Path      string
	PathSpan  diag.Span
	Qualifier string
	QualSpan  diag.Span
	Span      diag.Span
	Explicit  bool // the qualifier came from `as`
}

// Painter is `painter name(formals) { let ... color = ... }`: a pure typed
// function sampled at a texel (language reference 4.9).
type Painter struct {
	Name   Ident
	Span   diag.Span
	Params []TemplateParam
	Lets   []*Let
	// Color is the expression of the `color` setting; ColorSpan is its span.
	// Color is nil when the body sets none.
	Color     Expr
	ColorSpan diag.Span
}

// Texture is `texture name = kind { ... }`. Only the atlas kind is read.
type Texture struct {
	Name     Ident
	Span     diag.Span
	Kind     string
	KindSpan diag.Span
	Settings []*Setting
	Cells    []*Cell
}

// Cell is `cell name { index = ... slot = ... paint = ... }` in an atlas.
type Cell struct {
	Name     Ident
	Span     diag.Span
	Settings []*Setting
}

// Plug is `plug name { kind = ... at = ... }`: the frame on this asset that
// meets a host's socket.
type Plug struct {
	Name     Ident
	Span     diag.Span
	Settings []*Setting
}
