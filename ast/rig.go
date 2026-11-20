package ast

import "m31labs.dev/greenware/diag"

// RigItem is an item of a rig declaration's body: *RigBlock, *Row,
// *SampleRow or *Let, in source order. Order matters for skin steps, joints
// and keys, so a body keeps it.
type RigItem interface{ ItemSpan() diag.Span }

// ItemSpan implements RigItem.
func (r *Row) ItemSpan() diag.Span { return r.Span }

// ItemSpan implements RigItem.
func (r *SampleRow) ItemSpan() diag.Span { return r.Span }

// ItemSpan implements RigItem.
func (b *RigBlock) ItemSpan() diag.Span { return b.Span }

// RigBlock is a block inside a rig declaration: joint, socket, limit, mirror,
// lengthen, blend, inflate, stretch, reshape, offset or pose. Settings are
// checked against the registry kind of the block; which blocks a parent may
// hold is the resolver's rule.
type RigBlock struct {
	Kind     string
	KindSpan diag.Span
	Name     Ident
	Pair     Ident
	Span     diag.Span
	Settings []*Setting
	Items    []RigItem
}

// RigDecl is a top-level rig declaration: skeleton, skin, morph or clip.
type RigDecl struct {
	Kind     string
	KindSpan diag.Span
	Name     Ident
	Span     diag.Span
	Settings []*Setting
	Items    []RigItem
}
