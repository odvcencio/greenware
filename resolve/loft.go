package resolve

import (
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// defaultLoftColumns is the layout of a ring row when `columns` is absent.
var defaultLoftColumns = []string{"x", "y", "z", "rx", "rz"}

// LoftRule evaluates the points of a loft whose `vertex` is an author's
// expression. A level that changes the number of columns asks again for its
// own column count; the rows, with their values and indices, never change.
type LoftRule struct {
	run     *run
	scope   *scope
	vertex  ast.Expr
	columns []string
	rings   [][]units.Value
}

// Points evaluates every ring at every column. A full turn with n segments has
// n columns, with a = 360deg * j / n; an arc with n intervals has n+1, with
// a = lo + (hi - lo) * j / n. The points are in metres. The error names the
// first point that could not be evaluated.
func (l *LoftRule) Points(n int, arc bool, lo, hi float64) ([][][3]float64, error) {
	cols := n
	if arc {
		cols = n + 1
	}
	out := make([][][3]float64, len(l.rings))
	for k := range l.rings {
		out[k] = make([][3]float64, cols)
		for j := 0; j < cols; j++ {
			p, err := l.point(k, j, n, arc, lo, hi, false)
			if err != nil {
				return nil, err
			}
			out[k][j] = p
		}
	}
	return out, nil
}

// point evaluates one vertex. With report set, a fault is reported as a
// diagnostic at its own span (the front end's check); otherwise it comes back
// as an error and no diagnostic is left behind.
func (l *LoftRule) point(k, j, n int, arc bool, lo, hi float64, report bool) ([3]float64, error) {
	r := l.run
	sc := newScope(l.scope)
	for i, name := range l.columns {
		sc.add(&binding{name: name, state: stDone, value: l.rings[k][i]})
	}
	var a float64
	if arc {
		a = lo + float64(float64(hi-lo)*float64(j))/float64(n)
	} else {
		a = float64(units.TwoPi*float64(j)) / float64(n)
	}
	sc.add(&binding{name: "a", state: stDone, value: units.Ang(a)})
	sc.add(&binding{name: "j", state: stDone, value: units.Num(float64(j))})
	sc.add(&binding{name: "k", state: stDone, value: units.Num(float64(k))})
	before := len(r.diags)
	quiet := r.quiet
	r.quiet = 0
	v := r.triple(r.eval(l.vertex, sc), units.Length, l.vertex.Span(), "three lengths")
	r.quiet = quiet
	if len(r.diags) > before {
		msg := r.diags[before].Message
		if !report {
			r.diags = r.diags[:before]
		}
		return [3]float64{}, fmt.Errorf("ring %d, column %d: %s", k, j, msg)
	}
	if v.DependsOnRuntime() {
		if report {
			r.report(diag.New(diag.CodeRuntime, l.vertex.Span(), "the vertex rule depends on a runtime parameter, but a loft's points must be known when the file compiles"))
		}
		return [3]float64{}, fmt.Errorf("the vertex of ring %d depends on a runtime parameter", k)
	}
	p, ok := v.Vec3()
	if !ok {
		return [3]float64{}, fmt.Errorf("ring %d, column %d: the vertex is not three lengths", k, j)
	}
	return p, nil
}

// names reads `columns = (a, b, c)`: distinct names, none of them the loft's
// own context values.
func (r *run) names(e ast.Expr) units.Value {
	var elems []ast.Expr
	switch x := e.(type) {
	case *ast.Tuple:
		elems = x.Elems
	case *ast.Name:
		elems = []ast.Expr{x}
	case *ast.Paren:
		elems = []ast.Expr{x.X}
	default:
		r.report(diag.New(diag.CodeType, e.Span(), "`columns` takes a tuple of names, such as (y, rx, rz)"))
		return units.Bad()
	}
	seen := map[string]bool{}
	var out []units.Value
	for _, el := range elems {
		nm, sp, ok := nameOf(el)
		switch {
		case !ok:
			r.report(diag.New(diag.CodeType, el.Span(), "a column is a plain name"))
			return units.Bad()
		case nm == "a" || nm == "j" || nm == "k":
			r.report(diag.New(diag.CodeFieldRange, sp, "`%s` is the loft's own angle or index; name the column something else", nm))
			return units.Bad()
		case seen[nm]:
			r.report(diag.New(diag.CodeDuplicate, sp, "the column `%s` is listed twice", nm))
			return units.Bad()
		}
		seen[nm] = true
		out = append(out, units.Label(nm))
	}
	return units.List(out...)
}

// loft evaluates what is special about a loft: its column layout, its rows and
// its vertex rule.
func (r *run) loft(n *Node, s *ast.Shape, k *registry.Kind, sc *scope) {
	cols := defaultLoftColumns
	if v, ok := field(n.Fields, "columns"); ok && v.Valid() {
		cols = nil
		for _, e := range v.Elems {
			cols = append(cols, e.Str)
		}
	}
	vs := ast.FindSetting(s.Settings, "vertex")
	haveVertex := vs != nil && vs.Value != nil
	if !haveVertex {
		for _, need := range defaultLoftColumns {
			if !contains(cols, need) {
				r.report(diag.New(diag.CodeMissing, s.KindSpan, "a loft whose columns are (%s) needs a `vertex` rule, because it does not name `%s`", strings.Join(cols, ", "), need))
				break
			}
		}
	}
	// Every column is a length: the rows are bare numbers in the asset's unit
	// unless they carry one.
	spec := registry.RowSpec{Name: "ring", MinRows: 2}
	for _, c := range cols {
		spec.Columns = append(spec.Columns, registry.Column{Name: c, Type: registry.Type{Kind: registry.TLength}, NonNegative: (c == "rx" || c == "rz") && !haveVertex})
	}
	kind := &registry.Kind{Name: "loft", Rows: []registry.RowSpec{spec}}
	rows, rowsOK := r.rows(s.Rows, kind, sc)
	n.Rows = rows
	if rowsOK && len(rows) < 2 {
		r.report(diag.New(diag.CodeMissing, s.KindSpan, "`loft` needs at least 2 `ring` rows, found %d", len(rows)))
	}
	// A partial arc is an open sheet: caps must be off.
	arc, hasArc := field(n.Fields, "arc")
	partial := hasArc && arc.Valid() && len(arc.Elems) == 2 && !(arc.Elems[0].Num == 0 && arc.Elems[1].Num == units.TwoPi)
	if partial {
		if span := arc.Elems[1].Num - arc.Elems[0].Num; span > units.TwoPi {
			r.report(diag.New(diag.CodeFieldRange, settingSpan(s, "arc"), "an arc spans at most one turn, found %s", units.Ang(span).Format()))
		}
		if caps, ok := field(n.Fields, "caps"); ok && caps.Valid() && caps.Str != "off" {
			r.report(diag.New(diag.CodeFieldRange, settingSpan(s, "caps"), "an open arc is a sheet: write `caps = off`, not `caps = %s`", caps.Str))
		}
	}
	if !haveVertex || !rowsOK {
		return
	}
	rule := &LoftRule{run: r, scope: sc, vertex: vs.Value, columns: cols}
	for _, row := range rows {
		rule.rings = append(rule.rings, row.Values)
	}
	n.Loft = rule
	// Check the rule once at the source's own columns so that a fault is found
	// by `check` and reported where the expression is written.
	seg, ok := field(n.Fields, "segments")
	if !ok || !seg.Valid() {
		return
	}
	segs := int(seg.Num)
	lo, hi := 0.0, units.TwoPi
	if hasArc && arc.Valid() && len(arc.Elems) == 2 {
		lo, hi = arc.Elems[0].Num, arc.Elems[1].Num
	}
	for kk := range rule.rings {
		cols := segs
		if partial {
			cols = segs + 1
		}
		for j := 0; j < cols; j++ {
			if _, err := rule.point(kk, j, segs, partial, lo, hi, true); err != nil {
				return // one fault is reported once; the rest would repeat it
			}
		}
	}
}

func settingSpan(s *ast.Shape, name string) diag.Span {
	if st := ast.FindSetting(s.Settings, name); st != nil {
		return st.Span
	}
	return s.Span
}

// exprText writes an expression in one canonical form, with every operator
// parenthesised and every number as written, for the meaning hash.
func exprText(e ast.Expr) string {
	switch x := e.(type) {
	case nil:
		return ""
	case *ast.NumberLit:
		return x.Text
	case *ast.ColorLit:
		return x.Value.Hex()
	case *ast.StringLit:
		return strconv.Quote(x.Value)
	case *ast.BoolLit:
		if x.Value {
			return "on"
		}
		return "off"
	case *ast.Name:
		return x.Name
	case *ast.Qualified:
		return strings.Join(x.Parts, ".")
	case *ast.Paren:
		return exprText(x.X)
	case *ast.Tuple:
		return "(" + joinExprs(x.Elems) + ")"
	case *ast.List:
		return "[" + joinExprs(x.Elems) + "]"
	case *ast.Unary:
		return "(" + x.Op + exprText(x.X) + ")"
	case *ast.Binary:
		return "(" + exprText(x.L) + " " + x.Op + " " + exprText(x.R) + ")"
	case *ast.If:
		return "(if " + exprText(x.Cond) + " then " + exprText(x.Then) + " else " + exprText(x.Else) + ")"
	case *ast.Index:
		return exprText(x.X) + "[" + exprText(x.I) + "]"
	case *ast.Member:
		return exprText(x.X) + "." + x.Name
	case *ast.Call:
		var parts []string
		for _, a := range x.Args {
			t := exprText(a.Value)
			if a.Range != nil {
				t = exprText(a.Range.Lo) + ".." + exprText(a.Range.Hi)
			}
			if a.Name != "" {
				t = a.Name + "=" + t
			}
			parts = append(parts, t)
		}
		return x.Func + "(" + strings.Join(parts, ", ") + ")"
	}
	return "?"
}

func joinExprs(es []ast.Expr) string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = exprText(e)
	}
	return strings.Join(parts, ", ")
}
