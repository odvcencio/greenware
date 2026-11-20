package resolve

import (
	"math"
	"sort"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// A morph weight is a constant, or a function of one runtime parameter that a
// finite table states exactly (language reference 5.5). A numeric parameter
// gives ordered breakpoints over its full range with linear interpolation
// between them, including each change of slope; an enum gives a total lookup.
// The weight expression is not sampled: it is read as a piecewise-linear
// function, and anything else is refused.

// pl is a piecewise-linear function over a closed interval: strictly
// increasing xs, one y per x, linear between.
type pl struct{ xs, ys []float64 }

func plConst(lo, hi, c float64) pl { return pl{xs: []float64{lo, hi}, ys: []float64{c, c}} }

func (p pl) at(x float64) float64 {
	n := len(p.xs)
	if x <= p.xs[0] {
		return p.ys[0]
	}
	if x >= p.xs[n-1] {
		return p.ys[n-1]
	}
	i := sort.SearchFloat64s(p.xs, x)
	if p.xs[i] == x {
		return p.ys[i]
	}
	t := (x - p.xs[i-1]) / (p.xs[i] - p.xs[i-1])
	return det.Lerp(p.ys[i-1], p.ys[i], t)
}

func (p pl) isConst() (float64, bool) {
	for _, y := range p.ys {
		if y != p.ys[0] {
			return 0, false
		}
	}
	return p.ys[0], true
}

func (p pl) map1(f func(float64) float64) pl {
	out := pl{xs: p.xs, ys: make([]float64, len(p.ys))}
	for i, y := range p.ys {
		out.ys[i] = f(y)
	}
	return out
}

// combine applies op to two functions on the union of their breakpoints. When
// cross is set it adds the point where the two functions meet inside a
// segment, so a min or a max keeps its corner.
func combine(a, b pl, cross bool, op func(x, y float64) float64) pl {
	var xs []float64
	xs = append(xs, a.xs...)
	xs = append(xs, b.xs...)
	sort.Float64s(xs)
	var u []float64
	for _, x := range xs {
		if len(u) == 0 || x != u[len(u)-1] {
			u = append(u, x)
		}
	}
	if cross {
		var withX []float64
		for i, x := range u {
			withX = append(withX, x)
			if i+1 == len(u) {
				break
			}
			d0, d1 := a.at(x)-b.at(x), a.at(u[i+1])-b.at(u[i+1])
			if (d0 < 0 && d1 > 0) || (d0 > 0 && d1 < 0) {
				withX = append(withX, snap(det.Lerp(x, u[i+1], d0/(d0-d1))))
			}
		}
		u = withX
	}
	out := pl{xs: u, ys: make([]float64, len(u))}
	for i, x := range u {
		out.ys[i] = snap(op(a.at(x), b.at(x)))
	}
	return out.simplify()
}

// snap rounds away the error of a crossing: a value within 1e-12 of a multiple
// of 1e-12 becomes that multiple, so a corner that is exactly 1 or 0 in the
// source is exactly 1 or 0 in the table.
func snap(x float64) float64 { return math.Round(float64(x*1e12)) / 1e12 }

// simplify drops the breakpoints that sit on a straight line, so that a table
// lists the ends and each change of slope.
func (p pl) simplify() pl {
	if len(p.xs) <= 2 {
		return p
	}
	xs, ys := []float64{p.xs[0]}, []float64{p.ys[0]}
	for i := 1; i+1 < len(p.xs); i++ {
		x0, y0 := xs[len(xs)-1], ys[len(ys)-1]
		x2, y2 := p.xs[i+1], p.ys[i+1]
		want := det.Lerp(y0, y2, (p.xs[i]-x0)/(x2-x0))
		if math.Abs(want-p.ys[i]) <= 1e-12*math.Max(1, math.Abs(p.ys[i])) {
			continue
		}
		xs, ys = append(xs, p.xs[i]), append(ys, p.ys[i])
	}
	xs, ys = append(xs, p.xs[len(p.xs)-1]), append(ys, p.ys[len(p.ys)-1])
	return pl{xs: xs, ys: ys}
}

// weightPL reads an expression as a piecewise-linear function of the named
// parameter over [lo, hi]. ok is false after it reports why not.
func (r *run) weightPL(e ast.Expr, sc *scope, name string, lo, hi float64) (pl, bool) {
	e = unparen(e)
	// A subexpression that does not read a runtime parameter is a constant.
	v := r.quietEval(func() units.Value { return r.eval(e, sc) })
	if v.Valid() && !v.DependsOnRuntime() {
		c := r.scalar(v, units.Number, e.Span())
		if !c.Valid() {
			return pl{}, false
		}
		return plConst(lo, hi, c.Num), true
	}
	bad := func(msg string) (pl, bool) {
		r.report(diag.New(diag.CodeRuntime, e.Span(), "a morph weight must be a constant or a finite table of one runtime parameter, and %s", msg))
		return pl{}, false
	}
	switch n := e.(type) {
	case *ast.Name:
		if n.Name == name {
			return pl{xs: []float64{lo, hi}, ys: []float64{lo, hi}}, true
		}
		return bad("`" + n.Name + "` is a second runtime value")
	case *ast.Unary:
		x, ok := r.weightPL(n.X, sc, name, lo, hi)
		if !ok {
			return x, false
		}
		switch n.Op {
		case "-":
			return x.map1(func(y float64) float64 { return -y }), true
		case "+":
			return x, true
		}
		return bad("`" + n.Op + "` has no table form")
	case *ast.Binary:
		a, ok1 := r.weightPL(n.L, sc, name, lo, hi)
		b, ok2 := r.weightPL(n.R, sc, name, lo, hi)
		if !ok1 || !ok2 {
			return pl{}, false
		}
		switch n.Op {
		case "+":
			return combine(a, b, false, func(x, y float64) float64 { return x + y }), true
		case "-":
			return combine(a, b, false, func(x, y float64) float64 { return x - y }), true
		case "*":
			if c, ok := a.isConst(); ok {
				return b.map1(func(y float64) float64 { return float64(c * y) }).simplify(), true
			}
			if c, ok := b.isConst(); ok {
				return a.map1(func(y float64) float64 { return float64(c * y) }).simplify(), true
			}
			return bad("a product of two values that depend on the parameter is not piecewise linear")
		case "/":
			if c, ok := b.isConst(); ok && c != 0 {
				return a.map1(func(y float64) float64 { return y / c }).simplify(), true
			}
			return bad("a division by a value that depends on the parameter is not piecewise linear")
		}
		return bad("`" + n.Op + "` has no table form")
	case *ast.Call:
		args := make([]pl, len(n.Args))
		for i, a := range n.Args {
			if a.Value == nil || a.Name != "" {
				return bad("`" + n.Func + "` takes plain arguments")
			}
			x, ok := r.weightPL(a.Value, sc, name, lo, hi)
			if !ok {
				return pl{}, false
			}
			args[i] = x
		}
		mx := func(x, y float64) float64 { return math.Max(x, y) }
		mn := func(x, y float64) float64 { return math.Min(x, y) }
		switch {
		case n.Func == "clamp" && len(args) == 3:
			return combine(combine(args[0], args[1], true, mx), args[2], true, mn), true
		case n.Func == "max" && len(args) == 2:
			return combine(args[0], args[1], true, mx), true
		case n.Func == "min" && len(args) == 2:
			return combine(args[0], args[1], true, mn), true
		case n.Func == "abs" && len(args) == 1:
			neg := args[0].map1(func(y float64) float64 { return -y })
			return combine(args[0], neg, true, mx), true
		}
		return bad("`" + n.Func + "` of a value that depends on the parameter is not piecewise linear")
	}
	return bad("this expression has no table form")
}

// runtimeNames lists the runtime parameters an expression reads by name.
func (r *run) runtimeNames(e ast.Expr, sc *scope) (names []string, indirect bool) {
	seen := map[string]bool{}
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		switch n := e.(type) {
		case *ast.Name:
			b := sc.lookup(n.Name)
			switch {
			case b == nil:
			case b.param != nil && b.param.Runtime:
				if !seen[n.Name] {
					seen[n.Name] = true
					names = append(names, n.Name)
				}
			case b.let != nil && b.state == stDone && b.value.DependsOnRuntime():
				indirect = true
			}
		case *ast.Paren:
			walk(n.X)
		case *ast.Unary:
			walk(n.X)
		case *ast.Binary:
			walk(n.L)
			walk(n.R)
		case *ast.If:
			walk(n.Cond)
			walk(n.Then)
			walk(n.Else)
		case *ast.Call:
			for _, a := range n.Args {
				if a.Value != nil {
					walk(a.Value)
				}
			}
		case *ast.Tuple:
			for _, x := range n.Elems {
				walk(x)
			}
		}
	}
	walk(e)
	sort.Strings(names)
	return names, indirect
}

// morphWeight reads a morph's `weight` setting.
func (r *run) morphWeight(e ast.Expr, sc *scope) RigWeight {
	// The ordinary evaluation reports names, units and member typos once.
	v := r.eval(e, sc)
	if !v.Valid() {
		return RigWeight{}
	}
	names, indirect := r.runtimeNames(e, sc)
	if indirect {
		r.report(diag.New(diag.CodeUnsupported, e.Span(), "a morph weight that reads a runtime parameter through a `let` is valid in the language but outside this compiler's subset: write the parameter in the weight"))
		return RigWeight{}
	}
	if len(names) > 1 {
		r.report(diag.New(diag.CodeRuntime, e.Span(), "a morph weight depends on one runtime parameter, and this one reads %d: %v", len(names), names))
		return RigWeight{}
	}
	if len(names) == 0 {
		c := r.scalar(v, units.Number, e.Span())
		if !c.Valid() {
			return RigWeight{}
		}
		return r.checkWeight(RigWeight{Const: c.Num, Default: c.Num}, e)
	}
	b := sc.lookup(names[0])
	var p Param
	for _, q := range r.resolved.Params {
		if q.Name == names[0] {
			p = q
		}
	}
	w := RigWeight{Param: names[0]}
	switch {
	case p.Type == "enum":
		for _, m := range p.Members {
			child := newScope(sc)
			child.add(&binding{name: names[0], state: stDone, value: units.Label(m)})
			x := r.quietEval(func() units.Value { return r.eval(e, child) })
			c := r.scalar(x, units.Number, e.Span())
			if !c.Valid() {
				return RigWeight{}
			}
			w.Members = append(w.Members, RigMember{Name: m, W: c.Num})
			if p.Value.Str == m {
				w.Default = c.Num
			}
		}
	case p.Lo != nil && p.Hi != nil && b != nil && b.param != nil:
		t, _ := registry.ParamType(b.param.Type.Name)
		if t.Kind != registry.TNumber {
			r.report(diag.New(diag.CodeRuntime, e.Span(), "a morph weight can depend on a number or an enum parameter; `%s` is a %s", names[0], p.Type))
			return RigWeight{}
		}
		f, ok := r.weightPL(e, sc, names[0], p.Lo.Num, p.Hi.Num)
		if !ok {
			return RigWeight{}
		}
		for i := range f.xs {
			w.Breaks = append(w.Breaks, RigBreak{X: f.xs[i], Y: f.ys[i]})
		}
		w.Default = f.at(p.Value.Num)
	default:
		r.report(diag.New(diag.CodeRuntime, e.Span(), "a morph weight needs the full range of `%s` to write its table: declare the parameter with `in lo..hi`", names[0]))
		return RigWeight{}
	}
	return r.checkWeight(w, e)
}

// checkWeight requires finite weights.
func (r *run) checkWeight(w RigWeight, e ast.Expr) RigWeight {
	bad := func(x float64) bool { return math.IsNaN(x) || math.IsInf(x, 0) }
	if bad(w.Const) || bad(w.Default) {
		r.report(diag.New(diag.CodeDomain, e.Span(), "a morph weight is not finite"))
	}
	for _, b := range w.Breaks {
		if bad(b.Y) {
			r.report(diag.New(diag.CodeDomain, e.Span(), "a morph weight is not finite"))
		}
	}
	return w
}
