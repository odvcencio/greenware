// Package ast is the typed declaration model of a Greenware file.
//
// Lower turns a parsed lang.Tree into a File: parameters, lets, checks,
// materials, the part tree, levels of detail, collision, a budget and
// outputs, each node with its source span. Lower checks the shape of the
// file against the registry (unknown fields, unsupported constructs, names
// that must be pairs). Values, units and name lookup are for package
// resolve.
package ast

import (
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/units"
)

// Ident is a name with its span.
type Ident struct {
	Name string
	Span diag.Span
}

// Expr is an expression node.
type Expr interface {
	Span() diag.Span
}

type base struct{ S diag.Span }

// Span returns the source span of the node.
func (b base) Span() diag.Span { return b.S }

// NumberLit is a number token such as 0.015, 6mm or 90deg.
type NumberLit struct {
	base
	Text  string
	Value units.Value
}

// ColorLit is #rrggbb or #rrggbbaa.
type ColorLit struct {
	base
	Value units.Color
}

// StringLit is a string token with its escapes decoded.
type StringLit struct {
	base
	Value string
}

// BoolLit is on or off.
type BoolLit struct {
	base
	Value bool
}

// Name is an identifier used as a value.
type Name struct {
	base
	Name string
}

// Qualified is a dotted name such as p.x.
type Qualified struct {
	base
	Parts []string
}

// Tuple is (a, b, ...) with at least two elements.
type Tuple struct {
	base
	Elems []Expr
}

// List is [a, b, ...].
type List struct {
	base
	Elems []Expr
}

// Unary is a prefix operator: -, + or !.
type Unary struct {
	base
	Op string
	X  Expr
}

// Binary is an infix operator.
type Binary struct {
	base
	Op   string
	L, R Expr
}

// If is `if c then a else b`.
type If struct {
	base
	Cond, Then, Else Expr
}

// Arg is a call or row argument, optionally named.
type Arg struct {
	Span     diag.Span
	Name     string
	NameSpan diag.Span
	Value    Expr
	Range    *Range
}

// Call is a function call.
type Call struct {
	base
	Func     string
	FuncSpan diag.Span
	Args     []Arg
}

// Index is v[i].
type Index struct {
	base
	X, I Expr
}

// Member is v.name on a computed value.
type Member struct {
	base
	X    Expr
	Name string
}

// Paren is a parenthesised expression. It keeps the source's grouping.
type Paren struct {
	base
	X Expr
}

// Bad stands for source that did not parse. Evaluating it adds no diagnostic.
type Bad struct{ base }

// Range is lo..hi. It appears only where the grammar names a range.
type Range struct {
	Span   diag.Span
	Lo, Hi Expr
}
