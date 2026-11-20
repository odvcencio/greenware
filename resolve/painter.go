package resolve

import (
	"fmt"
	"math"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/kernel/image"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// Painters (language reference 4.9). A painter is a pure function of one
// texel. Resolution turns its body into a tree of closures with every name,
// type and call bound, so a type or name fault is reported once, with a span,
// before any texel is painted, and painting runs no interpreter over source.
//
// Values are numbers (plain or angles), colours (sRGB-encoded RGBA as real
// components that are not clipped until storage) and switches. Arithmetic is
// binary64; every multiply that feeds an add is wrapped in float64(...), and
// the functions are those of package units, which use package det.

type ptype struct {
	k     byte // 'n' number, 'c' colour, 'b' switch
	angle bool
}

var (
	tNum  = ptype{k: 'n'}
	tAng  = ptype{k: 'n', angle: true}
	tCol  = ptype{k: 'c'}
	tBool = ptype{k: 'b'}
)

func (t ptype) String() string {
	switch {
	case t == tAng:
		return "angle"
	case t.k == 'n':
		return "number"
	case t.k == 'c':
		return "colour"
	}
	return "switch"
}

// pval is a painter value; the static type says which field is live.
type pval struct {
	n float64
	c image.Colour
	b bool
}

type penv struct {
	tex image.Texel
	err error
	// ops counts the operators and calls executed, for the tests that check
	// the static bound.
	ops int64
}

func (e *penv) fail(format string, args ...any) {
	if e.err == nil {
		e.err = fmt.Errorf(format, args...)
	}
}

type pfn func(e *penv, f []pval) pval

type pexpr struct {
	t   ptype
	fn  pfn
	ops int
}

// PainterParam is one formal of a painter.
type PainterParam struct {
	Name       string
	Type       string // colour, number or angle, switch
	t          ptype
	def        pval
	HasDefault bool
}

// Painter is a compiled painter.
type Painter struct {
	// Name is the name the source calls it by: "finish.gloss" or "flat".
	Name   string
	Params []PainterParam
	// Ops is the static count of scalar operations of one call: each operator,
	// function call and `if` counts one, both branches of an `if` are charged,
	// and a called painter adds its own count.
	Ops    int
	nslots int
	lets   []compiledLet
	color  pexpr
	decl   *ast.Painter
	lib    *library
}

type compiledLet struct {
	slot int
	e    pexpr
}

// call runs the painter on an argument list in a fresh frame.
func (p *Painter) call(e *penv, args []pval) pval {
	f := make([]pval, p.nslots)
	copy(f, args)
	for _, l := range p.lets {
		f[l.slot] = l.e.fn(e, f)
	}
	return p.color.fn(e, f)
}

// PaintCall is a bound painter call or a constant colour: what a `cell`'s
// `paint` or an atlas's `fill` holds.
type PaintCall struct {
	// Painter is nil for a constant colour.
	Painter *Painter
	Args    []pval
	Const   image.Colour
	// Text is the call as source writes it, for messages.
	Text string
	// Ops is the static scalar operation count of one texel.
	Ops int
	// Span locates the call in the source.
	Span diag.Span
}

// Func returns the call as an image painter.
func (pc *PaintCall) Func() image.Painter {
	if pc == nil {
		return nil
	}
	if pc.Painter == nil {
		c := pc.Const
		return func(image.Texel) (image.Colour, error) { return c, nil }
	}
	return func(t image.Texel) (image.Colour, error) {
		env := &penv{tex: t}
		out := pc.Painter.call(env, pc.Args)
		if env.err != nil {
			return image.Colour{}, fmt.Errorf("painter %s: %w", pc.Text, env.err)
		}
		return out.c, nil
	}
}

// ---- compile ---------------------------------------------------------------

type pstate int

const (
	psNew pstate = iota
	psBusy
	psDone
	psBad
)

type pentry struct {
	state pstate
	p     *Painter
}

type pslot struct {
	idx int
	t   ptype
}

// pctx is the compile context of one painter body.
type pctx struct {
	r      *run
	lib    *library // nil for the main file
	site   diag.Span
	names  map[string]pslot
	nslots int
	failed bool
}

func (c *pctx) report(code string, sp diag.Span, format string, args ...any) {
	c.failed = true
	msg := fmt.Sprintf(format, args...)
	if c.lib != nil {
		// A library's spans are in its own source; point at the call instead.
		c.r.report(diag.New(code, c.site, "in the library %s: %s", c.lib.path, msg))
		return
	}
	c.r.report(diag.New(code, sp, "%s", msg))
}

func (c *pctx) bad() pexpr { return pexpr{t: tNum, fn: func(*penv, []pval) pval { return pval{} }} }

// painterOf finds and compiles the painter a call names. owner is the library
// the call is written in (nil for the main file). site is where to report.
func (r *run) painterOf(owner *library, name string, site diag.Span) *Painter {
	var lib *library
	local := name
	if i := strings.Index(name, "."); i >= 0 {
		q := name[:i]
		local = name[i+1:]
		if owner != nil {
			r.report(diag.New(diag.CodeUnsupported, site, "in the library %s: a library painter cannot call a qualified painter", owner.path))
			return nil
		}
		lib = r.libs[q]
		if lib == nil {
			d := diag.New(diag.CodeUnknownName, site, "no import is qualified `%s`", q)
			var qs []string
			for k := range r.libs {
				qs = append(qs, k)
			}
			if s, ok := registry.Suggest(q, qs); ok {
				d = d.WithEdit("write "+s, site, s+name[i:])
			}
			r.report(d)
			return nil
		}
	} else {
		lib = owner
	}
	file := r.f
	if lib != nil {
		file = lib.file
	}
	var decl *ast.Painter
	for _, p := range file.Painters {
		if p.Name.Name == local {
			decl = p
			break
		}
	}
	if decl == nil {
		var names []string
		for _, p := range file.Painters {
			names = append(names, p.Name.Name)
		}
		where := "this file"
		if lib != nil {
			where = "the library " + lib.path
		}
		d := diag.New(diag.CodeUnknownName, site, "no painter is named `%s` in %s", name, where)
		if s, ok := registry.Suggest(local, names); ok && lib == nil {
			d = d.WithEdit("write "+s, site, s)
		}
		r.report(d)
		return nil
	}
	key := local
	if lib != nil {
		key = lib.path + "#" + local
	}
	ent := r.painters[key]
	if ent == nil {
		ent = &pentry{}
		r.painters[key] = ent
	}
	switch ent.state {
	case psDone:
		return ent.p
	case psBad:
		return nil
	case psBusy:
		sp := site
		if lib == nil {
			sp = decl.Name.Span
		}
		r.report(diag.New(diag.CodeCycle, sp, "the painter `%s` calls itself, directly or through other painters", name))
		ent.state = psBad
		return nil
	}
	ent.state = psBusy
	p := r.compilePainter(lib, decl, name, site)
	if p == nil {
		ent.state = psBad
		return nil
	}
	ent.state, ent.p = psDone, p
	return p
}

// compilePainter binds one painter body.
func (r *run) compilePainter(lib *library, decl *ast.Painter, name string, site diag.Span) *Painter {
	c := &pctx{r: r, lib: lib, site: site, names: map[string]pslot{}}
	p := &Painter{Name: name, decl: decl, lib: lib}
	if lib != nil {
		p.Name = name
	}
	for _, fp := range decl.Params {
		t, ok := painterParamType(fp.Type.Name)
		if !ok {
			c.report(diag.CodeType, fp.Type.Span, "a painter argument is a colour, number, ratio, integer, angle or switch, not `%s`", fp.Type.Name)
			continue
		}
		if _, dup := c.names[fp.Name.Name]; dup {
			c.report(diag.CodeDuplicate, fp.Name.Span, "the painter argument `%s` is declared twice", fp.Name.Name)
			continue
		}
		pp := PainterParam{Name: fp.Name.Name, Type: t.String(), t: t}
		if fp.Default != nil {
			d := c.compile(fp.Default)
			if d.t != t {
				c.report(diag.CodeType, fp.Default.Span(), "the default of `%s` is a %s, but the argument is a %s", fp.Name.Name, d.t, t)
			} else {
				pp.def, pp.HasDefault = d.fn(&penv{}, nil), true
			}
		}
		c.names[fp.Name.Name] = pslot{c.nslots, t}
		c.nslots++
		p.Params = append(p.Params, pp)
	}
	for _, l := range decl.Lets {
		e := c.compile(l.Value)
		if _, dup := c.names[l.Name.Name]; dup {
			c.report(diag.CodeDuplicate, l.Name.Span, "the name `%s` is declared twice in a painter", l.Name.Name)
			continue
		}
		slot := c.nslots
		c.nslots++
		c.names[l.Name.Name] = pslot{slot, e.t}
		p.lets = append(p.lets, compiledLet{slot, e})
		p.Ops += e.ops
	}
	if decl.Color == nil {
		c.report(diag.CodeMissing, decl.Name.Span, "the painter `%s` needs `color = ...`", decl.Name.Name)
	} else {
		col := c.compile(decl.Color)
		if col.t != tCol {
			c.report(diag.CodeType, decl.Color.Span(), "`color` must be a colour, found a %s", col.t)
		}
		p.color = col
		p.Ops += col.ops
	}
	p.nslots = c.nslots
	if c.failed {
		return nil
	}
	return p
}

func painterParamType(name string) (ptype, bool) {
	switch name {
	case "color":
		return tCol, true
	case "number", "ratio", "integer":
		return tNum, true
	case "angle":
		return tAng, true
	case "switch":
		return tBool, true
	}
	return ptype{}, false
}

func constExpr(t ptype, v pval) pexpr {
	return pexpr{t: t, fn: func(*penv, []pval) pval { return v }}
}

func (c *pctx) compile(x ast.Expr) pexpr {
	switch n := x.(type) {
	case nil, *ast.Bad:
		c.failed = true
		return c.bad()
	case *ast.NumberLit:
		v := n.Value
		switch {
		case v.Pending, v.Dim == units.Number:
			return constExpr(tNum, pval{n: v.Num})
		case v.Dim == units.Angle:
			return constExpr(tAng, pval{n: v.Num})
		}
		c.report(diag.CodeUnit, n.Span(), "a painter works with plain numbers and angles, not a %s", v.Dim)
		return c.bad()
	case *ast.ColorLit:
		return constExpr(tCol, pval{c: image.FromBytes([4]byte{n.Value.R, n.Value.G, n.Value.B, n.Value.A})})
	case *ast.BoolLit:
		return constExpr(tBool, pval{b: n.Value})
	case *ast.Paren:
		return c.compile(n.X)
	case *ast.Name:
		return c.name(n)
	case *ast.Unary:
		return c.unary(n)
	case *ast.Binary:
		return c.binary(n)
	case *ast.If:
		return c.ifExpr(n)
	case *ast.Call:
		return c.call(n)
	}
	c.report(diag.CodeType, x.Span(), "this expression is not allowed in a painter, which works with numbers, colours and switches")
	return c.bad()
}

func (c *pctx) name(n *ast.Name) pexpr {
	if s, ok := c.names[n.Name]; ok {
		idx := s.idx
		return pexpr{t: s.t, fn: func(_ *penv, f []pval) pval { return f[idx] }}
	}
	switch n.Name {
	case "px":
		return pexpr{t: tNum, fn: func(e *penv, _ []pval) pval { return pval{n: float64(e.tex.PX)} }}
	case "py":
		return pexpr{t: tNum, fn: func(e *penv, _ []pval) pval { return pval{n: float64(e.tex.PY)} }}
	case "cell":
		return pexpr{t: tNum, fn: func(e *penv, _ []pval) pval { return pval{n: float64(e.tex.Cell)} }}
	case "u":
		return pexpr{t: tNum, fn: func(e *penv, _ []pval) pval { return pval{n: e.tex.U} }}
	case "v":
		return pexpr{t: tNum, fn: func(e *penv, _ []pval) pval { return pval{n: e.tex.V} }}
	}
	// A bake parameter or top-level let of the document. A library sees none.
	if c.lib == nil {
		if b := c.r.globals.lookup(n.Name); b != nil {
			v := c.r.force(b)
			if !v.Valid() {
				c.failed = true
				return c.bad()
			}
			if v.DependsOnRuntime() {
				c.report(diag.CodeRuntime, n.Span(), "a painter cannot depend on the runtime parameter `%s`: a painted image is baked", n.Name)
				return c.bad()
			}
			switch v.Kind {
			case units.KNumber:
				v = c.r.d.Fix(v, units.Number)
				switch v.Dim {
				case units.Number:
					return constExpr(tNum, pval{n: v.Num})
				case units.Angle:
					return constExpr(tAng, pval{n: v.Num})
				}
			case units.KColor:
				return constExpr(tCol, pval{c: image.FromBytes([4]byte{v.Color.R, v.Color.G, v.Color.B, v.Color.A})})
			case units.KSwitch:
				return constExpr(tBool, pval{b: v.Bool})
			}
			c.report(diag.CodeType, n.Span(), "`%s` is a %s; a painter takes numbers, angles, colours and switches", n.Name, v.Describe())
			return c.bad()
		}
	}
	vis := []string{"px", "py", "u", "v", "cell"}
	for k := range c.names {
		vis = append(vis, k)
	}
	d := diag.New(diag.CodeUnknownName, n.Span(), "`%s` is not a painter argument, let, texel name (px, py, u, v, cell) or document value here", n.Name)
	if s, ok := registry.Suggest(n.Name, vis); ok && c.lib == nil {
		d = d.WithEdit("write "+s, n.Span(), s)
	}
	c.failed = true
	if c.lib != nil {
		d = diag.New(diag.CodeUnknownName, c.site, "in the library %s: `%s` is not a name a painter can use", c.lib.path, n.Name)
	}
	c.r.report(d)
	return c.bad()
}

func finite(e *penv, v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		e.fail("a result is not a finite number")
		return 0
	}
	return v
}

func (c *pctx) unary(n *ast.Unary) pexpr {
	x := c.compile(n.X)
	switch n.Op {
	case "-":
		if x.t.k == 'n' {
			f := x.fn
			return pexpr{t: x.t, ops: x.ops + 1, fn: func(e *penv, fr []pval) pval {
				e.ops++
				return pval{n: -f(e, fr).n}
			}}
		}
	case "+":
		if x.t.k == 'n' {
			return x
		}
	case "!":
		if x.t == tBool {
			f := x.fn
			return pexpr{t: tBool, ops: x.ops + 1, fn: func(e *penv, fr []pval) pval {
				e.ops++
				return pval{b: !f(e, fr).b}
			}}
		}
	}
	c.report(diag.CodeType, n.Span(), "`%s` does not apply to a %s", n.Op, x.t)
	return c.bad()
}

func (c *pctx) binary(n *ast.Binary) pexpr {
	l, r := c.compile(n.L), c.compile(n.R)
	ops := l.ops + r.ops + 1
	lf, rf := l.fn, r.fn
	mk := func(t ptype, fn func(e *penv, a, b pval) pval) pexpr {
		return pexpr{t: t, ops: ops, fn: func(e *penv, fr []pval) pval {
			e.ops++
			return fn(e, lf(e, fr), rf(e, fr))
		}}
	}
	mismatch := func() pexpr {
		c.report(diag.CodeType, n.Span(), "`%s` does not apply to a %s and a %s", n.Op, l.t, r.t)
		return c.bad()
	}
	num := func(a, b ptype) bool { return a.k == 'n' && b.k == 'n' }
	switch n.Op {
	case "&&", "||":
		if l.t != tBool || r.t != tBool {
			return mismatch()
		}
		and := n.Op == "&&"
		return mk(tBool, func(_ *penv, a, b pval) pval {
			if and {
				return pval{b: a.b && b.b}
			}
			return pval{b: a.b || b.b}
		})
	case "==", "!=", "<", "<=", ">", ">=":
		if l.t != r.t || l.t == tCol {
			return mismatch()
		}
		if l.t == tBool && n.Op != "==" && n.Op != "!=" {
			return mismatch()
		}
		op := n.Op
		isBool := l.t == tBool
		return mk(tBool, func(_ *penv, a, b pval) pval {
			x, y := a.n, b.n
			if isBool {
				x, y = b2f(a.b), b2f(b.b)
			}
			switch op {
			case "==":
				return pval{b: x == y}
			case "!=":
				return pval{b: x != y}
			case "<":
				return pval{b: x < y}
			case "<=":
				return pval{b: x <= y}
			case ">":
				return pval{b: x > y}
			}
			return pval{b: x >= y}
		})
	case "+", "-":
		sub := n.Op == "-"
		switch {
		case num(l.t, r.t) && l.t == r.t:
			return mk(l.t, func(e *penv, a, b pval) pval {
				if sub {
					return pval{n: finite(e, a.n-b.n)}
				}
				return pval{n: finite(e, a.n+b.n)}
			})
		case l.t == tCol && r.t == tNum:
			return mk(tCol, func(e *penv, a, b pval) pval {
				d := b.n
				if sub {
					d = -d
				}
				return pval{c: image.Colour{finite(e, a.c[0]+d), finite(e, a.c[1]+d), finite(e, a.c[2]+d), a.c[3]}}
			})
		case l.t == tNum && r.t == tCol && !sub:
			return mk(tCol, func(e *penv, a, b pval) pval {
				d := a.n
				return pval{c: image.Colour{finite(e, b.c[0]+d), finite(e, b.c[1]+d), finite(e, b.c[2]+d), b.c[3]}}
			})
		}
		return mismatch()
	case "*":
		switch {
		case num(l.t, r.t) && !(l.t == tAng && r.t == tAng):
			t := tNum
			if l.t == tAng || r.t == tAng {
				t = tAng
			}
			return mk(t, func(e *penv, a, b pval) pval { return pval{n: finite(e, float64(a.n*b.n))} })
		case l.t == tCol && r.t == tNum:
			return mk(tCol, func(e *penv, a, b pval) pval { return pval{c: scaleRGB(e, a.c, b.n)} })
		case l.t == tNum && r.t == tCol:
			return mk(tCol, func(e *penv, a, b pval) pval { return pval{c: scaleRGB(e, b.c, a.n)} })
		}
		return mismatch()
	case "/":
		switch {
		case num(l.t, r.t) && (r.t == tNum || l.t == tAng):
			t := l.t
			if l.t == tAng && r.t == tAng {
				t = tNum
			}
			return mk(t, func(e *penv, a, b pval) pval {
				if b.n == 0 {
					e.fail("division by zero")
					return pval{}
				}
				return pval{n: finite(e, a.n/b.n)}
			})
		case l.t == tCol && r.t == tNum:
			return mk(tCol, func(e *penv, a, b pval) pval {
				if b.n == 0 {
					e.fail("division by zero")
					return pval{}
				}
				return pval{c: image.Colour{finite(e, a.c[0]/b.n), finite(e, a.c[1]/b.n), finite(e, a.c[2]/b.n), a.c[3]}}
			})
		}
		return mismatch()
	}
	return mismatch()
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func scaleRGB(e *penv, c image.Colour, k float64) image.Colour {
	return image.Colour{finite(e, float64(c[0]*k)), finite(e, float64(c[1]*k)), finite(e, float64(c[2]*k)), c[3]}
}

func (c *pctx) ifExpr(n *ast.If) pexpr {
	cond, a, b := c.compile(n.Cond), c.compile(n.Then), c.compile(n.Else)
	if cond.t != tBool {
		c.report(diag.CodeType, n.Cond.Span(), "an `if` condition must be a switch, found a %s", cond.t)
		return c.bad()
	}
	if a.t != b.t {
		c.report(diag.CodeType, n.Span(), "the two branches of an `if` must have one type, found a %s and a %s", a.t, b.t)
		return c.bad()
	}
	cf, af, bf := cond.fn, a.fn, b.fn
	return pexpr{t: a.t, ops: cond.ops + max(a.ops, b.ops) + 1, fn: func(e *penv, fr []pval) pval {
		e.ops++
		if cf(e, fr).b {
			return af(e, fr)
		}
		return bf(e, fr)
	}}
}

// callArgs compiles the arguments of a builtin call, which are positional.
func (c *pctx) callArgs(n *ast.Call) ([]pexpr, bool) {
	var out []pexpr
	for _, a := range n.Args {
		if a.Name != "" || a.Value == nil {
			c.report(diag.CodeType, a.Span, "`%s` takes plain positional arguments", n.Func)
			return nil, false
		}
		out = append(out, c.compile(a.Value))
	}
	return out, true
}

func (c *pctx) call(n *ast.Call) pexpr {
	switch n.Func {
	case "grain", "noise":
		return c.randomCall(n)
	case "abs", "clamp", "cos", "floor", "lerp", "max", "min", "mod", "pow", "round", "sin", "smoothstep", "sqrt", "tan":
		return c.mathCall(n)
	}
	// A painter call.
	p := c.r.painterOf(c.lib, n.Func, n.FuncSpan)
	if p == nil {
		c.failed = true
		return c.bad()
	}
	args, ok := c.bindCall(p, n)
	if !ok {
		return c.bad()
	}
	ops := p.Ops + 1
	for _, a := range args {
		ops += a.ops
	}
	return pexpr{t: tCol, ops: ops, fn: func(e *penv, fr []pval) pval {
		e.ops++
		vals := make([]pval, len(args))
		for i, a := range args {
			vals[i] = a.fn(e, fr)
		}
		return p.call(e, vals)
	}}
}

// bindCall matches the arguments of a painter call to its formals: positional
// first, then named, each once; a formal with a default may be left out.
func (c *pctx) bindCall(p *Painter, n *ast.Call) ([]pexpr, bool) {
	args := make([]*pexpr, len(p.Params))
	seenNamed := false
	pos := 0
	good := true
	for _, a := range n.Args {
		if a.Value == nil {
			c.report(diag.CodeType, a.Span, "`%s` takes values, not ranges", n.Func)
			return nil, false
		}
		var idx int
		if a.Name == "" {
			if seenNamed {
				c.report(diag.CodeType, a.Span, "a positional argument cannot follow a named one")
				good = false
				continue
			}
			if pos >= len(p.Params) {
				c.report(diag.CodeType, a.Span, "`%s` takes %d argument(s)", n.Func, len(p.Params))
				good = false
				continue
			}
			idx = pos
			pos++
		} else {
			seenNamed = true
			idx = -1
			for i, fp := range p.Params {
				if fp.Name == a.Name {
					idx = i
				}
			}
			if idx < 0 {
				c.report(diag.CodeUnknownField, a.NameSpan, "`%s` has no argument `%s`", n.Func, a.Name)
				good = false
				continue
			}
			if args[idx] != nil {
				c.report(diag.CodeDuplicate, a.NameSpan, "the argument `%s` is given twice", a.Name)
				good = false
				continue
			}
		}
		e := c.compile(a.Value)
		if e.t != p.Params[idx].t {
			c.report(diag.CodeType, a.Value.Span(), "the argument `%s` of `%s` is a %s, found a %s", p.Params[idx].Name, n.Func, p.Params[idx].t, e.t)
			good = false
			continue
		}
		args[idx] = &e
	}
	out := make([]pexpr, len(p.Params))
	for i, fp := range p.Params {
		switch {
		case args[i] != nil:
			out[i] = *args[i]
		case fp.HasDefault:
			out[i] = constExpr(fp.t, fp.def)
		default:
			c.report(diag.CodeMissing, n.Span(), "`%s` needs the argument `%s`", n.Func, fp.Name)
			good = false
		}
	}
	return out, good
}

func (c *pctx) randomCall(n *ast.Call) pexpr {
	args, ok := c.callArgs(n)
	if !ok {
		return c.bad()
	}
	if len(args) != 1 || args[0].t != tNum {
		c.report(diag.CodeType, n.Span(), "`%s` takes one plain number", n.Func)
		return c.bad()
	}
	af := args[0].fn
	ops := args[0].ops + 1
	if n.Func == "grain" {
		return pexpr{t: tNum, ops: ops, fn: func(e *penv, fr []pval) pval {
			e.ops++
			s := af(e, fr).n
			if !(s >= 0) {
				e.fail("grain needs a sigma of at least 0, found %g", s)
				return pval{}
			}
			t := e.tex
			return pval{n: finite(e, image.Grain(t.Seed, int64(t.Cell), t.PX, t.PY, s))}
		}}
	}
	return pexpr{t: tNum, ops: ops, fn: func(e *penv, fr []pval) pval {
		e.ops++
		s := af(e, fr).n
		if !(s >= 0) {
			e.fail("noise needs a scale of at least 0, found %g", s)
			return pval{}
		}
		t := e.tex
		v, err := image.Noise(t.Seed, int64(t.Cell), t.U, t.V, s)
		if err != nil {
			e.fail("%v", err)
		}
		return pval{n: v}
	}}
}

func (c *pctx) mathCall(n *ast.Call) pexpr {
	args, ok := c.callArgs(n)
	if !ok {
		return c.bad()
	}
	f, _ := units.LookupFunc(n.Func)
	if len(args) != f.Arity {
		c.report(diag.CodeType, n.Span(), "`%s` takes %d argument(s), found %d", n.Func, f.Arity, len(args))
		return c.bad()
	}
	ops := 1
	for _, a := range args {
		ops += a.ops
	}
	sig := func() string {
		s := make([]string, len(args))
		for i, a := range args {
			s[i] = a.t.String()
		}
		return strings.Join(s, ", ")
	}
	// result type and the units.Value dimension each argument is passed with.
	var rt ptype
	allNum := true
	for _, a := range args {
		allNum = allNum && a.t.k == 'n'
	}
	like := allNum
	for _, a := range args {
		like = like && a.t == args[0].t
	}
	switch n.Func {
	case "sin", "cos", "tan":
		if args[0].t != tAng {
			c.report(diag.CodeUnit, n.Span(), "`%s` needs an angle with a unit: write the argument with deg or rad (found %s)", n.Func, sig())
			return c.bad()
		}
		rt = tNum
	case "abs":
		if !allNum {
			c.report(diag.CodeType, n.Span(), "`abs` needs a number or an angle, found a %s", sig())
			return c.bad()
		}
		rt = args[0].t
	case "min", "max", "mod":
		if !like {
			c.report(diag.CodeType, n.Span(), "`%s` needs two numbers or two angles, found %s", n.Func, sig())
			return c.bad()
		}
		rt = args[0].t
	case "clamp":
		if !like {
			c.report(diag.CodeType, n.Span(), "`clamp` needs three numbers or three angles, found %s", sig())
			return c.bad()
		}
		rt = args[0].t
	case "smoothstep":
		if !like {
			c.report(diag.CodeType, n.Span(), "`smoothstep` needs three numbers or three angles, found %s", sig())
			return c.bad()
		}
		rt = tNum
	case "floor", "round", "sqrt", "pow":
		for _, a := range args {
			if a.t != tNum {
				c.report(diag.CodeType, n.Span(), "`%s` needs plain numbers, found %s", n.Func, sig())
				return c.bad()
			}
		}
		rt = tNum
	case "lerp":
		if args[2].t != tNum || args[0].t != args[1].t || args[0].t == tBool {
			c.report(diag.CodeType, n.Span(), "`lerp` needs two like values and a plain number, found %s", sig())
			return c.bad()
		}
		rt = args[0].t
	}
	name := n.Func
	toUnit := func(t ptype, v float64) units.Value {
		if t == tAng {
			return units.Ang(v)
		}
		return units.Num(v)
	}
	d := c.r.d
	if name == "lerp" && rt == tCol {
		af, bf, tf := args[0].fn, args[1].fn, args[2].fn
		return pexpr{t: tCol, ops: ops, fn: func(e *penv, fr []pval) pval {
			e.ops++
			a, b, t := af(e, fr).c, bf(e, fr).c, tf(e, fr).n
			var o image.Colour
			for k := range o {
				o[k] = finite(e, a[k]+float64((b[k]-a[k])*t))
			}
			return pval{c: o}
		}}
	}
	fns := make([]pfn, len(args))
	ts := make([]ptype, len(args))
	for i, a := range args {
		fns[i], ts[i] = a.fn, a.t
	}
	return pexpr{t: rt, ops: ops, fn: func(e *penv, fr []pval) pval {
		e.ops++
		vals := make([]units.Value, len(fns))
		for i, f := range fns {
			vals[i] = toUnit(ts[i], f(e, fr).n)
		}
		out, err := units.Call(name, vals, d)
		if err != nil {
			e.fail("%s: %s", name, err.Msg)
			return pval{}
		}
		return pval{n: out.Num}
	}}
}

// ---- binding a call from the document ------------------------------------------

// paintCall binds a `paint` or `fill` value of an atlas: a constant colour or
// a call of a painter, evaluated in the document's scope.
func (r *run) paintCall(x ast.Expr, sc *scope) *PaintCall {
	sp := x.Span()
	text := exprText(x)
	switch n := x.(type) {
	case *ast.Call:
		p := r.painterOf(nil, n.Func, n.FuncSpan)
		if p == nil {
			return nil
		}
		args, ok := r.bindDocumentCall(p, n, sc)
		if !ok {
			return nil
		}
		return &PaintCall{Painter: p, Args: args, Text: text, Ops: p.Ops, Span: sp}
	case *ast.Name, *ast.Qualified, *ast.ColorLit, *ast.Paren:
		v := r.eval(x, sc)
		if !v.Valid() {
			return nil
		}
		if v.DependsOnRuntime() {
			r.report(diag.New(diag.CodeRuntime, sp, "a painted image is baked: its colour cannot depend on a runtime parameter"))
			return nil
		}
		if v.Kind != units.KColor {
			r.report(diag.New(diag.CodeType, sp, "`paint` takes a painter call or a colour, found a %s", v.Describe()))
			return nil
		}
		return &PaintCall{Const: image.FromBytes([4]byte{v.Color.R, v.Color.G, v.Color.B, v.Color.A}), Text: text, Span: sp}
	}
	r.report(diag.New(diag.CodeType, sp, "`paint` takes a painter call such as finish.gloss(), or a colour"))
	return nil
}

// bindDocumentCall evaluates the arguments of a painter call written in the
// document, where names are parameters and lets.
func (r *run) bindDocumentCall(p *Painter, n *ast.Call, sc *scope) ([]pval, bool) {
	vals := make([]pval, len(p.Params))
	set := make([]bool, len(p.Params))
	seenNamed := false
	pos := 0
	good := true
	for _, a := range n.Args {
		if a.Value == nil {
			r.report(diag.New(diag.CodeType, a.Span, "`%s` takes values, not ranges", n.Func))
			return nil, false
		}
		idx := -1
		if a.Name == "" {
			if seenNamed {
				r.report(diag.New(diag.CodeType, a.Span, "a positional argument cannot follow a named one"))
				good = false
				continue
			}
			if pos >= len(p.Params) {
				r.report(diag.New(diag.CodeType, a.Span, "`%s` takes %d argument(s)", n.Func, len(p.Params)))
				good = false
				continue
			}
			idx = pos
			pos++
		} else {
			seenNamed = true
			for i, fp := range p.Params {
				if fp.Name == a.Name {
					idx = i
				}
			}
			if idx < 0 {
				r.report(diag.New(diag.CodeUnknownField, a.NameSpan, "`%s` has no argument `%s`", n.Func, a.Name))
				good = false
				continue
			}
			if set[idx] {
				r.report(diag.New(diag.CodeDuplicate, a.NameSpan, "the argument `%s` is given twice", a.Name))
				good = false
				continue
			}
		}
		v := r.eval(a.Value, sc)
		if !v.Valid() {
			good = false
			continue
		}
		if v.DependsOnRuntime() {
			r.report(diag.New(diag.CodeRuntime, a.Value.Span(), "a painted image is baked: the argument `%s` cannot depend on a runtime parameter", p.Params[idx].Name))
			good = false
			continue
		}
		pv, ok := r.painterArg(v, p.Params[idx], a.Value.Span())
		if !ok {
			good = false
			continue
		}
		vals[idx], set[idx] = pv, true
	}
	for i, fp := range p.Params {
		if set[i] {
			continue
		}
		if fp.HasDefault {
			vals[i] = fp.def
			continue
		}
		r.report(diag.New(diag.CodeMissing, n.Span(), "`%s` needs the argument `%s`", n.Func, fp.Name))
		good = false
	}
	return vals, good
}

func (r *run) painterArg(v units.Value, fp PainterParam, sp diag.Span) (pval, bool) {
	switch fp.t {
	case tCol:
		if v.Kind == units.KColor {
			return pval{c: image.FromBytes([4]byte{v.Color.R, v.Color.G, v.Color.B, v.Color.A})}, true
		}
	case tBool:
		if v.Kind == units.KSwitch {
			return pval{b: v.Bool}, true
		}
	case tNum, tAng:
		if v.Kind == units.KNumber {
			dim := units.Number
			if fp.t == tAng {
				dim = units.Angle
			}
			v = r.d.Fix(v, dim)
			if v.Dim == dim {
				return pval{n: v.Num}, true
			}
		}
	}
	r.report(diag.New(diag.CodeType, sp, "the argument `%s` is a %s, found a %s", fp.Name, fp.t, v.Describe()))
	return pval{}, false
}

// checkPainters compiles every painter the main file declares, so an unused
// one is still checked.
func (r *run) checkPainters() {
	seen := map[string]diag.Span{}
	for _, p := range r.f.Painters {
		if prev, dup := seen[p.Name.Name]; dup {
			r.report(diag.New(diag.CodeDuplicate, p.Name.Span, "the painter name `%s` is declared twice", p.Name.Name).WithRelated(prev, "first declaration"))
			continue
		}
		seen[p.Name.Name] = p.Name.Span
		r.painterOf(nil, p.Name.Name, p.Name.Span)
	}
}
