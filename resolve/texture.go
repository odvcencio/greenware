package resolve

import (
	"fmt"
	"sort"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/kernel/image"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// Limits of an atlas (language reference 4.1, limit I and P).
const (
	maxAtlasCells = 4096
	maxAtlasSide  = 2048
	maxDecoded    = 4194304 // decoded texels summed over atlases
)

// Atlas is a resolved atlas texture in a form that can be baked.
type Atlas struct {
	Name   string
	Layout image.Layout
	Seed   int64
	Mode   image.Mode
	Cells  []AtlasCell
	Fill   *PaintCall
	// Slots lists the slot bytes the baked mask uses, in increasing order: the
	// slots of its cells, and slot 0 when some cell is left to `fill`. It is
	// empty for a colour atlas.
	Slots []int
	// Ops is the static count of scalar painter operations to bake the atlas:
	// every interior texel of every cell, each charged the painter's count.
	Ops  int64
	Span diag.Span
}

// AtlasCell is one painted cell with its allocated index.
type AtlasCell struct {
	Name  string
	Index int
	Slot  int
	Paint *PaintCall
	Span  diag.Span
}

// Spec returns what image.Bake needs.
func (a *Atlas) Spec() image.Spec {
	s := image.Spec{Layout: a.Layout, Seed: a.Seed, Mode: a.Mode, Fill: a.Fill.Func()}
	for _, c := range a.Cells {
		s.Cells = append(s.Cells, image.Cell{Index: c.Index, Slot: c.Slot, Paint: c.Paint.Func()})
	}
	return s
}

// CellIndex returns the physical index of a named cell.
func (a *Atlas) CellIndex(name string) (int, bool) {
	for _, c := range a.Cells {
		if c.Name == name {
			return c.Index, true
		}
	}
	return 0, false
}

// Atlas returns the named resolved atlas.
func (r *Resolved) Atlas(name string) *Atlas {
	for _, a := range r.Atlases {
		if a.Name == name {
			return a
		}
	}
	return nil
}

// PaintRef is the reference value of `paint(atlas)`: a texture reference whose
// text is the call as source writes it.
func PaintRef(atlas string) string { return "paint(" + atlas + ")" }

// ParsePaintRef reads a reference value as `paint(atlas)`.
func ParsePaintRef(s string) (atlas string, ok bool) {
	rest, ok := strings.CutPrefix(s, "paint(")
	if !ok || !strings.HasSuffix(rest, ")") {
		return "", false
	}
	return strings.TrimSuffix(rest, ")"), true
}

// textureValue evaluates a `base_texture`: a texture name, or paint(atlas).
func (r *run) textureValue(e ast.Expr) units.Value {
	switch n := e.(type) {
	case *ast.Name:
		if r.syms.lookup(registry.NsTexture, n.Name) == nil {
			r.unknownTexture(n.Name, n.Span())
			return units.Bad()
		}
		return units.Ref(n.Name)
	case *ast.Call:
		if n.Func == "paint" {
			if len(n.Args) != 1 || n.Args[0].Name != "" || n.Args[0].Value == nil {
				r.report(diag.New(diag.CodeType, n.Span(), "`paint` takes one atlas: paint(name)"))
				return units.Bad()
			}
			nm, ok := n.Args[0].Value.(*ast.Name)
			if !ok {
				r.report(diag.New(diag.CodeType, n.Args[0].Value.Span(), "`paint` takes the name of an atlas texture"))
				return units.Bad()
			}
			if r.syms.lookup(registry.NsTexture, nm.Name) == nil {
				r.unknownTexture(nm.Name, nm.Span())
				return units.Bad()
			}
			return units.Ref(PaintRef(nm.Name))
		}
	}
	r.report(diag.New(diag.CodeType, e.Span(), "`base_texture` takes a texture name or paint(atlas)"))
	return units.Bad()
}

func (r *run) unknownTexture(name string, sp diag.Span) {
	d := diag.New(diag.CodeUnknownName, sp, "no texture is named `%s`", name)
	if s, ok := registry.Suggest(name, r.syms.names(registry.NsTexture)); ok {
		d = d.WithEdit("write "+s, sp, s)
	}
	r.report(d)
}

// textures resolves every atlas declaration.
func (r *run) textures() {
	total := 0
	for _, t := range r.f.Textures {
		if d := r.syms.lookup(registry.NsTexture, t.Name.Name); d == nil || d.span != t.Name.Span {
			continue // a duplicate: already reported
		}
		a, n := r.atlas(t)
		if n != nil {
			r.resolved.Textures = append(r.resolved.Textures, n)
		}
		if a == nil {
			continue
		}
		total += a.Layout.Width() * a.Layout.Height()
		if total > maxDecoded {
			r.report(diag.New(diag.CodeFieldRange, t.Name.Span, "the atlases decode to more than %d texels in all; the limit is %d", total, maxDecoded))
		}
		r.resolved.Atlases = append(r.resolved.Atlases, a)
	}
}

func (r *run) atlas(t *ast.Texture) (*Atlas, *Node) {
	k := docKind("atlas")
	n := &Node{Kind: "atlas", Name: t.Name.Name}
	n.Fields = r.fields(k, t.Settings, r.globals, t.KindSpan)
	a := &Atlas{Name: t.Name.Name, Span: t.Name.Span}
	get := func(name string) units.Value {
		v, _ := field(n.Fields, name)
		return v
	}
	explicit := ast.FindSetting(t.Settings, "size") != nil
	grid, size, inset, side := get("grid"), get("cell_size"), get("inset"), get("size")
	switch {
	case explicit:
		if !side.Valid() {
			return nil, n
		}
		for _, bad := range []string{"grid", "cell_size", "inset"} {
			if s := ast.FindSetting(t.Settings, bad); s != nil {
				r.report(diag.New(diag.CodeType, s.NameSpan, "an atlas of rectangles (`size`) does not take `%s`: every cell gives its own rectangle", bad))
				return nil, n
			}
		}
	case ast.FindSetting(t.Settings, "grid") == nil || ast.FindSetting(t.Settings, "cell_size") == nil:
		r.report(diag.New(diag.CodeMissing, t.KindSpan, "an atlas needs `grid` and `cell_size`, or `size` with a rectangle in every cell"))
		return nil, n
	case !grid.Valid() || !size.Valid() || !inset.Valid():
		return nil, n
	}
	for _, f := range n.Fields {
		if f.Value.Valid() && f.Value.DependsOnRuntime() {
			r.report(diag.New(diag.CodeRuntime, t.Name.Span, "`%s` depends on a runtime parameter, but an atlas is baked", f.Name))
			return nil, n
		}
	}
	a.Seed = int64(get("seed").Num)
	if m := get("mode"); m.Valid() && m.Str == "palette" {
		a.Mode = image.ModePalette
	}
	ok := true
	lo, hi := 0, -1
	if explicit {
		if int(side.Num) > maxAtlasSide {
			r.report(diag.New(diag.CodeFieldRange, settingSpanOf(t.Settings, "size", t.KindSpan), "the image is %d x %d texels; the limit is %d on either side", int(side.Num), int(side.Num), maxAtlasSide))
			return nil, n
		}
		a.Layout = image.Layout{Size: int(side.Num)}
		if tv := get("texels"); tv.Valid() && ast.FindSetting(t.Settings, "texels") != nil {
			if int(tv.Num) > a.Layout.Size || a.Layout.Size%int(tv.Num) != 0 {
				r.report(diag.New(diag.CodeFieldRange, settingSpanOf(t.Settings, "texels", t.KindSpan), "`texels` must divide `size` (%d), found %d", a.Layout.Size, int(tv.Num)))
				return nil, n
			}
			a.Layout.Texels = int(tv.Num)
		}
	} else {
		a.Layout = image.Layout{Cols: int(grid.Elems[0].Num), Rows: int(grid.Elems[1].Num), CellSize: int(size.Num), Inset: inset.Num}
		if a.Layout.Cells() > maxAtlasCells {
			r.report(diag.New(diag.CodeFieldRange, settingSpanOf(t.Settings, "grid", t.KindSpan), "an atlas has at most %d cells, found %d", maxAtlasCells, a.Layout.Cells()))
			ok = false
		}
		if a.Layout.Width() > maxAtlasSide || a.Layout.Height() > maxAtlasSide {
			r.report(diag.New(diag.CodeFieldRange, settingSpanOf(t.Settings, "cell_size", t.KindSpan), "the image is %d x %d texels; the limit is %d on either side", a.Layout.Width(), a.Layout.Height(), maxAtlasSide))
			ok = false
		}
		if !(inset.Num >= 0 && inset.Num < 0.5) {
			r.report(diag.New(diag.CodeFieldRange, settingSpanOf(t.Settings, "inset", t.KindSpan), "`inset` is a ratio from 0 up to but not including 0.5, found %s", inset.Format()))
			ok = false
		}
		if !ok {
			return nil, n
		}
		var err error
		lo, hi, err = a.Layout.Interior()
		if err != nil {
			r.report(diag.New(diag.CodeFieldRange, settingSpanOf(t.Settings, "inset", t.KindSpan), "a %d texel cell with inset %s has no texel centre inside the inset", a.Layout.CellSize, inset.Format()))
			return nil, n
		}
	}
	if fs := ast.FindSetting(t.Settings, "fill"); fs != nil && fs.Value != nil {
		a.Fill = r.paintCall(fs.Value, r.globals)
		if a.Fill == nil {
			ok = false
		}
	}
	// The cells: names, indices, slots and painters.
	type pending struct {
		cell  *ast.Cell
		index int
		hasIx bool
		slot  int
		paint *PaintCall
		rect  image.Rect
	}
	var cells []*pending
	names := map[string]diag.Span{}
	ck, _ := registry.Lookup(registry.CtxAtlas, "cell")
	for _, c := range t.Cells {
		if prev, dup := names[c.Name.Name]; dup {
			r.report(diag.New(diag.CodeDuplicate, c.Name.Span, "the cell `%s` is declared twice in this atlas", c.Name.Name).WithRelated(prev, "first declaration"))
			ok = false
			continue
		}
		names[c.Name.Name] = c.Name.Span
		fs := r.fields(ck, c.Settings, r.globals, c.Name.Span)
		p := &pending{cell: c}
		if v, has := field(fs, "index"); has && v.Valid() && !isDefault(fs, "index") {
			p.index, p.hasIx = int(v.Num), true
		} else if has && !v.Valid() {
			ok = false
		}
		if explicit {
			for _, need := range []string{"index", "x", "y", "w", "h"} {
				if ast.FindSetting(c.Settings, need) == nil {
					r.report(diag.New(diag.CodeMissing, c.Name.Span, "the cell `%s` of an atlas of rectangles needs `%s`", c.Name.Name, need))
					ok = false
				}
			}
			geti := func(name string) int { v, _ := field(fs, name); return int(v.Num) }
			p.rect = image.Rect{X: geti("x"), Y: geti("y"), W: geti("w"), H: geti("h"), Margin: geti("margin")}
		}
		slotSet := ast.FindSetting(c.Settings, "slot")
		switch {
		case a.Mode == image.ModePalette && slotSet == nil:
			r.report(diag.New(diag.CodeMissing, c.Name.Span, "the cell `%s` of a palette atlas needs `slot = 0..255`", c.Name.Name))
			ok = false
		case a.Mode != image.ModePalette && slotSet != nil:
			r.report(diag.New(diag.CodeType, slotSet.NameSpan, "`slot` belongs to a palette atlas; this atlas stores colour"))
			ok = false
		case slotSet != nil:
			if v, has := field(fs, "slot"); has && v.Valid() {
				p.slot = int(v.Num)
			} else {
				ok = false
			}
		}
		ps := ast.FindSetting(c.Settings, "paint")
		switch {
		case ps != nil && ps.Value != nil:
			if p.paint = r.paintCall(ps.Value, r.globals); p.paint == nil {
				ok = false
			}
		case ps == nil && a.Fill == nil:
			r.report(diag.New(diag.CodeMissing, c.Name.Span, "the cell `%s` needs `paint`, because the atlas has no `fill`", c.Name.Name))
			ok = false
		case ps == nil:
			p.paint = a.Fill
		}
		cells = append(cells, p)
	}
	if !ok {
		return nil, n
	}
	// Allocation: explicit indices first, then the unindexed cells in name
	// order take the lowest free indices.
	used := map[int]string{}
	for _, p := range cells {
		if !p.hasIx {
			continue
		}
		if !explicit && p.index >= a.Layout.Cells() {
			r.report(diag.New(diag.CodeFieldRange, p.cell.Name.Span, "the cell `%s` has index %d, but the grid has %d cells (0..%d)", p.cell.Name.Name, p.index, a.Layout.Cells(), a.Layout.Cells()-1))
			ok = false
			continue
		}
		if other, dup := used[p.index]; dup {
			r.report(diag.New(diag.CodeDuplicate, p.cell.Name.Span, "the cells `%s` and `%s` both have index %d", other, p.cell.Name.Name, p.index))
			ok = false
			continue
		}
		used[p.index] = p.cell.Name.Name
	}
	var rest []*pending
	for _, p := range cells {
		if !p.hasIx {
			rest = append(rest, p)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].cell.Name.Name < rest[j].cell.Name.Name })
	next := 0
	for _, p := range rest {
		for {
			if _, taken := used[next]; !taken {
				break
			}
			next++
		}
		if !explicit && next >= a.Layout.Cells() {
			r.report(diag.New(diag.CodeFieldRange, p.cell.Name.Span, "the atlas has %d cells and %d named cells: the grid is full", a.Layout.Cells(), len(cells)))
			ok = false
			break
		}
		p.index = next
		used[next] = p.cell.Name.Name
	}
	if !ok {
		return nil, n
	}
	sort.SliceStable(cells, func(i, j int) bool { return cells[i].index < cells[j].index })
	if explicit {
		top := 0
		for _, p := range cells {
			top = max(top, p.index+1)
		}
		a.Layout.Rects = make([]image.Rect, top)
		for _, p := range cells {
			rc := p.rect
			if rc.X+rc.W > a.Layout.Size || rc.Y+rc.H > a.Layout.Size || 2*rc.Margin >= rc.W || 2*rc.Margin >= rc.H {
				r.report(diag.New(diag.CodeFieldRange, p.cell.Name.Span, "the rectangle of the cell `%s` does not fit the %d texel image, or its margin leaves no interior", p.cell.Name.Name, a.Layout.Size))
				return nil, n
			}
			a.Layout.Rects[p.index] = rc
		}
	}
	interior := int64(hi-lo+1) * int64(hi-lo+1)
	slotSet := map[int]bool{}
	for _, p := range cells {
		ac := AtlasCell{Name: p.cell.Name.Name, Index: p.index, Slot: p.slot, Paint: p.paint, Span: p.cell.Name.Span}
		a.Cells = append(a.Cells, ac)
		cellTexels := interior
		if explicit {
			f := int64(a.Layout.Size / a.Layout.Width())
			cellTexels = int64(p.rect.W-2*p.rect.Margin) / f * (int64(p.rect.H-2*p.rect.Margin) / f)
		}
		a.Ops += cellTexels * int64(p.paint.Ops+1)
		cn := &Node{Kind: "cell", Name: ac.Name, Fields: []Field{{Name: "index", Value: units.Num(float64(ac.Index))}}}
		if explicit {
			for _, kv := range []struct {
				k string
				v int
			}{{"x", p.rect.X}, {"y", p.rect.Y}, {"w", p.rect.W}, {"h", p.rect.H}, {"margin", p.rect.Margin}} {
				cn.Fields = append(cn.Fields, Field{Name: kv.k, Value: units.Num(float64(kv.v))})
			}
		}
		if a.Mode == image.ModePalette {
			cn.Fields = append(cn.Fields, Field{Name: "slot", Value: units.Num(float64(ac.Slot))})
			slotSet[ac.Slot] = true
		}
		cn.Fields = append(cn.Fields, Field{Name: "paint", Value: units.Str(p.paint.Text)})
		n.Children = append(n.Children, cn)
	}
	if rem := a.Layout.Cells() - len(cells); rem > 0 && !explicit {
		fillOps := 1
		if a.Fill != nil {
			fillOps = a.Fill.Ops + 1
		}
		a.Ops += int64(rem) * interior * int64(fillOps)
		if a.Mode == image.ModePalette {
			slotSet[0] = true
		}
	}
	for s := range slotSet {
		a.Slots = append(a.Slots, s)
	}
	sort.Ints(a.Slots)
	if a.Ops > maxPainterOps {
		r.report(diag.New(diag.CodeFieldRange, t.Name.Span, "painting the atlas needs %d scalar operations; the limit is %d", a.Ops, int64(maxPainterOps)))
		return nil, n
	}
	return a, n
}

// maxPainterOps is the limit P puts on scalar painter operations (language
// reference 4.1).
const maxPainterOps = 20_000_000

func isDefault(fs []Field, name string) bool {
	for _, f := range fs {
		if f.Name == name {
			return f.Default
		}
	}
	return false
}

func settingSpanOf(ss []*ast.Setting, name string, fallback diag.Span) diag.Span {
	if s := ast.FindSetting(ss, name); s != nil {
		if s.Value != nil {
			return s.Value.Span()
		}
		return s.Span
	}
	return fallback
}

// ---- materials ----------------------------------------------------------------

// checkMaterialTextures checks the texture uses of one resolved material: a
// raw palette mask is not a base texture, and paint(palette) needs a colour
// for every slot the atlas uses in the Selena material that contains it.
func (r *run) checkMaterialTextures(m *ast.Material, n *Node, parent *Node) {
	s := ast.FindSetting(m.Settings, "base_texture")
	if s == nil || s.Value == nil {
		return
	}
	v, ok := field(n.Fields, "base_texture")
	if !ok || !v.Valid() {
		return
	}
	sp := s.Value.Span()
	if atlas, painted := ParsePaintRef(v.Str); painted {
		a := r.resolved.Atlas(atlas)
		if a == nil || a.Mode != image.ModePalette {
			return // a colour atlas is its own colour image
		}
		if parent == nil {
			r.report(diag.New(diag.CodeMatParam, sp, "`paint(%s)` paints a palette atlas with the default slot colours of a Selena material, but this material is not the fallback of one", atlas))
			return
		}
		for _, slot := range a.Slots {
			name := fmt.Sprintf("slot%d", slot)
			sv, has := field(parent.Fields, name)
			if !has || !sv.Valid() || sv.Kind != units.KColor {
				r.report(diag.New(diag.CodeMatParam, sp, "`paint(%s)` uses palette slot %d, but the Selena material has no colour `%s`", atlas, slot, name))
			}
		}
		return
	}
	if a := r.resolved.Atlas(v.Str); a != nil && a.Mode == image.ModePalette {
		r.report(diag.New(diag.CodeMatParam, sp, "`%s` is a palette mask, which holds data, not colour: use paint(%s) for the base texture", v.Str, v.Str))
	}
}

// ---- plugs --------------------------------------------------------------------

func (r *run) plugs() {
	k := docKind("plug")
	for _, p := range r.f.Plugs {
		if d := r.syms.lookup(registry.NsSocket, p.Name.Name); d == nil || d.span != p.Name.Span {
			continue
		}
		n := &Node{Kind: "plug", Name: p.Name.Name}
		n.Fields = r.fields(k, p.Settings, r.globals, p.Name.Span)
		r.resolved.Plugs = append(r.resolved.Plugs, n)
	}
}
