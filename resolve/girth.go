package resolve

import (
	"fmt"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/units"
)

// GirthRule evaluates the three amounts of a `girth` morph operation at a
// height. Each amount is an expression of `y`, the height of a position in the
// asset's length unit as a plain number; it gives the fractional widening of
// the ring at that height. An absent amount is 0.
type GirthRule struct {
	run               *run
	scope             *scope
	side, front, back ast.Expr
}

// At returns the side, front and back amounts at height y (in the asset's
// length unit). The error names the expression that could not be evaluated.
func (g *GirthRule) At(y float64) (side, front, back float64, err error) {
	for _, a := range []struct {
		name string
		e    ast.Expr
		out  *float64
	}{{"side", g.side, &side}, {"front", g.front, &front}, {"back", g.back, &back}} {
		if a.e == nil {
			continue
		}
		v, err := g.eval(a.e, y, false)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("`%s` at y = %v: %w", a.name, y, err)
		}
		*a.out = v
	}
	return side, front, back, nil
}

// eval evaluates one amount. With report set, a fault is reported at the
// expression's own span; otherwise it comes back as an error and no
// diagnostic is left behind.
func (g *GirthRule) eval(e ast.Expr, y float64, report bool) (float64, error) {
	r := g.run
	sc := newScope(g.scope)
	sc.add(&binding{name: "y", state: stDone, value: units.Num(y)})
	before := len(r.diags)
	quiet := r.quiet
	r.quiet = 0
	v := r.scalar(r.eval(e, sc), units.Number, e.Span())
	r.quiet = quiet
	if len(r.diags) > before {
		msg := r.diags[before].Message
		if !report {
			r.diags = r.diags[:before]
		}
		return 0, fmt.Errorf("%s", msg)
	}
	if v.DependsOnRuntime() {
		if report {
			r.report(diag.New(diag.CodeRuntime, e.Span(), "a girth amount depends on a runtime parameter, but a morph's shape must be known when the file compiles: put the parameter in the morph's `weight`"))
		}
		return 0, fmt.Errorf("the amount depends on a runtime parameter")
	}
	if !v.Valid() {
		return 0, fmt.Errorf("the amount is not a number")
	}
	return v.Num, nil
}

// girth reads a `girth` operation's amounts. Each is an expression of `y`, so
// it is kept as written and evaluated per position by the compiler.
func (r *run) girth(b *ast.RigBlock, sc *scope) *GirthRule {
	g := &GirthRule{run: r, scope: sc}
	for _, a := range []struct {
		name string
		dst  *ast.Expr
	}{{"side", &g.side}, {"front", &g.front}, {"back", &g.back}} {
		if s := ast.FindSetting(b.Settings, a.name); s != nil && s.Value != nil {
			*a.dst = s.Value
		}
	}
	if g.side == nil && g.front == nil && g.back == nil {
		r.report(diag.New(diag.CodeMissing, b.KindSpan, "a `girth` needs at least one of `side`, `front` and `back`"))
		return nil
	}
	// Check each amount once, at the bottom of the figure, so that a fault is
	// reported where the expression is written.
	for _, e := range []ast.Expr{g.side, g.front, g.back} {
		if e == nil {
			continue
		}
		if _, err := g.eval(e, 0, true); err != nil {
			return nil
		}
	}
	return g
}
