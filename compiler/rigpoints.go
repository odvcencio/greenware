package compiler

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

// Runtime points (language reference 6.3). A check runs at the default point,
// at the corners of the numeric runtime parameters, at every member of the
// discrete ones, at each selected gear output of each mount, and at the named
// cases. The Cartesian product of the axes is the full set; above the
// `runtime_points` limit the audit keeps the default, every case, one axis at
// a time and the declared pairs, and says what it left out.

// DefaultRuntimePoints is the edition's `runtime_points` limit.
const DefaultRuntimePoints = 256

// runtimePoint is one assignment of every runtime parameter and of every
// mount's gear output.
type runtimePoint struct {
	label string
	vals  map[string]units.Value
	gear  map[string]string // mount name -> gear output
}

func (p runtimePoint) key() string {
	var ks []string
	for k := range p.vals {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	var b strings.Builder
	for _, k := range ks {
		fmt.Fprintf(&b, "%s=%s;", k, p.vals[k].Format())
	}
	var gs []string
	for k := range p.gear {
		gs = append(gs, k)
	}
	sort.Strings(gs)
	for _, k := range gs {
		fmt.Fprintf(&b, "gear %s=%s;", k, p.gear[k])
	}
	return b.String()
}

func (p runtimePoint) clone() runtimePoint {
	q := runtimePoint{vals: map[string]units.Value{}, gear: map[string]string{}}
	for k, v := range p.vals {
		q.vals[k] = v
	}
	for k, v := range p.gear {
		q.gear[k] = v
	}
	return q
}

// axis is one thing the plan varies: a runtime parameter's values, or a
// mount's gear outputs.
type axis struct {
	name  string // parameter, or "gear <mount>"
	param string
	mount string
	vals  []units.Value
	gears []string
	text  []string
}

// pointPlan is the result of planning the runtime points of an output.
type pointPlan struct {
	points []runtimePoint
	// axes and full are the axes and the size of their full product.
	axes []axis
	full uint64
	// partial is set when the full product exceeded the limit; omitted is the
	// number of full-product points the plan left out.
	partial bool
	omitted uint64
	limit   int
}

// paramValueText is a short text of a value for a point label.
func paramValueText(v units.Value) string {
	switch v.Kind {
	case units.KSwitch:
		if v.Bool {
			return "on"
		}
		return "off"
	case units.KLabel:
		return v.Str
	}
	return v.Format()
}

// valueOf reads a value written in a case: a bare name is an enum member, a
// number or switch is itself.
func valueOf(e ast.Expr) (units.Value, bool) {
	switch x := e.(type) {
	case *ast.Name:
		return units.Label(x.Name), true
	case *ast.NumberLit:
		return units.Num(x.Value.Num), true
	case *ast.BoolLit:
		return units.Switch(x.Value), true
	case *ast.Unary:
		if v, ok := valueOf(x.X); ok && x.Op == "-" && v.Kind == units.KNumber {
			return units.Num(-v.Num), true
		}
	case *ast.Paren:
		return valueOf(x.X)
	}
	return units.Bad(), false
}

// planPoints lists the runtime points the output's checks run at.
func (e *elab) planPoints(limit int) *pointPlan {
	if limit <= 0 {
		limit = DefaultRuntimePoints
	}
	r := e.r
	pl := &pointPlan{limit: limit}
	def := runtimePoint{label: "default", vals: map[string]units.Value{}, gear: map[string]string{}}
	var runtimeParams []resolve.Param
	for _, p := range r.Params {
		if p.Runtime {
			def.vals[p.Name] = p.Value
			runtimeParams = append(runtimeParams, p)
		}
	}
	for _, g := range e.mounts {
		def.gear[g.m.Name] = g.m.Output
	}
	rc := r.RuntimeCheck
	var corners, each []string
	if rc != nil {
		corners, each = rc.Corners, rc.Each
	} else {
		for _, p := range runtimeParams {
			switch {
			case p.Lo != nil && p.Hi != nil && p.Value.Kind == units.KNumber:
				corners = append(corners, p.Name)
			case p.Type == "enum" || p.Type == "switch":
				each = append(each, p.Name)
			}
		}
	}
	byName := map[string]resolve.Param{}
	for _, p := range runtimeParams {
		byName[p.Name] = p
	}
	var axes []axis
	for _, n := range corners {
		p := byName[n]
		a := axis{name: n, param: n, vals: []units.Value{*p.Lo}}
		if p.Hi.Num != p.Lo.Num {
			a.vals = append(a.vals, *p.Hi)
		}
		axes = append(axes, a)
	}
	for _, n := range each {
		p := byName[n]
		a := axis{name: n, param: n}
		switch {
		case p.Type == "switch":
			a.vals = []units.Value{units.Switch(false), units.Switch(true)}
		default:
			for _, m := range p.Members {
				a.vals = append(a.vals, units.Label(m))
			}
		}
		axes = append(axes, a)
	}
	for _, g := range e.mounts {
		if g.mode == "bake" {
			continue // a baked gear is fixed by the mount's `set`
		}
		var outs []string
		if rc != nil && len(rc.Gear) > 0 {
			for _, ref := range rc.Gear {
				if ref.Qualifier == g.m.Gear {
					outs = append(outs, ref.Output)
				}
			}
		} else {
			outs = append(outs, g.gear.Outputs...)
		}
		if len(outs) > 1 {
			axes = append(axes, axis{name: "gear " + g.m.Name, mount: g.m.Name, gears: outs})
		}
	}
	for i := range axes {
		for _, v := range axes[i].vals {
			axes[i].text = append(axes[i].text, paramValueText(v))
		}
		axes[i].text = append(axes[i].text, axes[i].gears...)
	}
	pl.axes = axes
	pl.full = 1
	for _, a := range axes {
		n := uint64(len(a.vals) + len(a.gears))
		if n == 0 {
			continue
		}
		if pl.full > math.MaxUint32/n {
			pl.full = math.MaxUint32
		} else {
			pl.full *= n
		}
	}
	label := func(p runtimePoint, chosen []int) string {
		var parts []string
		for i, a := range axes {
			if chosen[i] >= 0 {
				parts = append(parts, a.name+"="+a.text[chosen[i]])
			}
		}
		if len(parts) == 0 {
			return "default"
		}
		return strings.Join(parts, " ")
	}
	apply := func(p *runtimePoint, i, c int) {
		a := axes[i]
		if a.param != "" {
			p.vals[a.param] = a.vals[c]
		} else {
			p.gear[a.mount] = a.gears[c]
		}
	}
	seen := map[string]bool{}
	add := func(p runtimePoint) {
		if k := p.key(); !seen[k] {
			seen[k] = true
			pl.points = append(pl.points, p)
		}
	}
	add(def)
	cases := func() {
		if rc == nil {
			return
		}
		for _, c := range rc.Cases {
			p := def.clone()
			p.label = "case " + c.Name
			for _, s := range c.Set {
				if v, ok := valueOf(s.Expr); ok {
					p.vals[s.Name] = v
				}
			}
			if c.Gear != nil {
				for _, g := range e.mounts {
					if g.m.Gear == c.Gear.Qualifier {
						p.gear[g.m.Name] = c.Gear.Output
					}
				}
			}
			// A named case keeps its label even when another point equals it.
			k := p.key()
			if seen[k] {
				for i := range pl.points {
					if pl.points[i].key() == k {
						pl.points[i].label += "; case " + c.Name
					}
				}
				continue
			}
			add(p)
		}
	}
	if pl.full <= uint64(limit) {
		chosen := make([]int, len(axes))
		var rec func(i int, p runtimePoint)
		rec = func(i int, p runtimePoint) {
			if i == len(axes) {
				q := p.clone()
				q.label = label(q, chosen)
				add(q)
				return
			}
			n := len(axes[i].vals) + len(axes[i].gears)
			for c := 0; c < n; c++ {
				chosen[i] = c
				q := p.clone()
				apply(&q, i, c)
				rec(i+1, q)
			}
		}
		if len(axes) > 0 {
			rec(0, def)
		}
		cases()
		return pl
	}
	// Partial: the default, every case, one axis at a time and the pairs.
	pl.partial = true
	cases()
	for i, a := range axes {
		n := len(a.vals) + len(a.gears)
		for c := 0; c < n; c++ {
			q := def.clone()
			apply(&q, i, c)
			chosen := make([]int, len(axes))
			for k := range chosen {
				chosen[k] = -1
			}
			chosen[i] = c
			q.label = label(q, chosen)
			add(q)
		}
	}
	if rc != nil {
		for _, pr := range rc.Pairs {
			ia, ib := -1, -1
			for i, a := range axes {
				if a.param == pr[0] {
					ia = i
				}
				if a.param == pr[1] {
					ib = i
				}
			}
			if ia < 0 || ib < 0 {
				continue
			}
			for ca := 0; ca < len(axes[ia].vals); ca++ {
				for cb := 0; cb < len(axes[ib].vals); cb++ {
					q := def.clone()
					apply(&q, ia, ca)
					apply(&q, ib, cb)
					chosen := make([]int, len(axes))
					for k := range chosen {
						chosen[k] = -1
					}
					chosen[ia], chosen[ib] = ca, cb
					q.label = label(q, chosen)
					add(q)
				}
			}
		}
	}
	// Points of the full product that the plan did not keep: enumerate the
	// product (it is small enough to list, or the count says "at least").
	if pl.full <= 1<<20 {
		chosen := make([]int, len(axes))
		var rec func(i int, p runtimePoint)
		rec = func(i int, p runtimePoint) {
			if i == len(axes) {
				if !seen[p.key()] {
					pl.omitted++
				}
				return
			}
			n := len(axes[i].vals) + len(axes[i].gears)
			for c := 0; c < n; c++ {
				chosen[i] = c
				q := p.clone()
				apply(&q, i, c)
				rec(i+1, q)
			}
		}
		rec(0, def)
	} else {
		pl.omitted = pl.full - uint64(len(pl.points))
	}
	return pl
}

// partialMessage describes what a partial plan left out.
func (pl *pointPlan) partialMessage() string {
	var axes []string
	for _, a := range pl.axes {
		axes = append(axes, fmt.Sprintf("%s{%s}", a.name, strings.Join(a.text, ",")))
	}
	return fmt.Sprintf("the full product of %s is %d points, above the limit of %d; the audit runs %d points (the default, every case, one axis at a time and the declared pairs) and leaves out %d combinations of the axes",
		strings.Join(axes, " x "), pl.full, pl.limit, len(pl.points), pl.omitted)
}

// weightAt is a morph's weight at a runtime point.
func weightAt(w resolve.RigWeight, vals map[string]units.Value) float64 {
	if w.Param == "" {
		return w.Const
	}
	v, ok := vals[w.Param]
	if !ok {
		return w.Default
	}
	if len(w.Breaks) > 0 {
		x := v.Num
		bs := w.Breaks
		if x <= bs[0].X {
			return bs[0].Y
		}
		for i := 1; i < len(bs); i++ {
			if x <= bs[i].X {
				t := (x - bs[i-1].X) / (bs[i].X - bs[i-1].X)
				return bs[i-1].Y + float64(t*(bs[i].Y-bs[i-1].Y))
			}
		}
		return bs[len(bs)-1].Y
	}
	for _, m := range w.Members {
		if m.Name == v.Str {
			return m.W
		}
	}
	return w.Default
}
