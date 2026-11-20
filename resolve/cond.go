package resolve

import (
	"sort"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/units"
)

// Cond is the condition of a runtime `when`, in the one shape a binding table
// can hold (language.md section 3.3): a switch parameter, a negated switch, or
// an enum parameter compared with one of its members.
type Cond struct {
	Param string `json:"param"`
	// Op is "is" (the switch is on), "not" (the switch is off), "eq" or "ne"
	// (the enum equals, or differs from, Value).
	Op    string `json:"op"`
	Value string `json:"value,omitempty"`
}

// String writes the condition as source reads it.
func (c Cond) String() string {
	switch c.Op {
	case "is":
		return c.Param
	case "not":
		return "!" + c.Param
	case "eq":
		return c.Param + " == " + c.Value
	}
	return c.Param + " != " + c.Value
}

// Holds evaluates the condition under an assignment of parameters to values:
// "on" or "off" for a switch, the member name for an enum.
func (c Cond) Holds(assign map[string]string) bool {
	v := assign[c.Param]
	switch c.Op {
	case "is":
		return v == "on"
	case "not":
		return v != "on"
	case "eq":
		return v == c.Value
	}
	return v != c.Value
}

// Domain lists the values a runtime parameter can take in a condition: "off"
// and "on" for a switch, the members of an enum. It is nil for other types,
// which a condition cannot test.
func (p Param) Domain() []string {
	switch {
	case p.Type == "switch":
		return []string{"off", "on"}
	case p.Type == "enum":
		return append([]string(nil), p.Members...)
	}
	return nil
}

// Assigned is the parameter's value as an assignment holds it.
func (p Param) Assigned() string {
	switch p.Value.Kind {
	case units.KSwitch:
		if p.Value.Bool {
			return "on"
		}
		return "off"
	case units.KLabel:
		return p.Value.Str
	}
	return ""
}

// VisibilityWhen returns the runtime `when` that makes a part a node of its
// own: a part whose whole body is one runtime `when` (language.md section 3.3
// rule 3). It returns nil for any other part.
func VisibilityWhen(part *Node) *Node {
	if part == nil || part.Kind != "part" || len(part.Children) != 1 {
		return nil
	}
	if w := part.Children[0]; w.Kind == "when" && w.Cond != nil {
		return w
	}
	return nil
}

// condOf reads a runtime condition from a `when` expression.
func (r *run) condOf(e ast.Expr, sc *scope) (Cond, bool) {
	switch x := e.(type) {
	case *ast.Paren:
		return r.condOf(x.X, sc)
	case *ast.Name:
		if p := r.runtimeParam(x.Name, sc); p != nil && p.Type.Name == "switch" {
			return Cond{Param: x.Name, Op: "is"}, true
		}
	case *ast.Unary:
		if n, ok := x.X.(*ast.Name); ok && x.Op == "!" {
			if p := r.runtimeParam(n.Name, sc); p != nil && p.Type.Name == "switch" {
				return Cond{Param: n.Name, Op: "not"}, true
			}
		}
	case *ast.Binary:
		if x.Op != "==" && x.Op != "!=" {
			break
		}
		l, lok := x.L.(*ast.Name)
		rr, rok := x.R.(*ast.Name)
		if !lok || !rok {
			break
		}
		param, member := l.Name, rr.Name
		if r.runtimeParam(param, sc) == nil {
			param, member = rr.Name, l.Name
		}
		if p := r.runtimeParam(param, sc); p != nil && p.Type.Name == "enum" && sc.lookup(member) == nil {
			op := "eq"
			if x.Op == "!=" {
				op = "ne"
			}
			return Cond{Param: param, Op: op, Value: member}, true
		}
	}
	return Cond{}, false
}

// runtimeParam returns the runtime parameter a name is bound to, or nil.
func (r *run) runtimeParam(name string, sc *scope) *ast.Param {
	if b := sc.lookup(name); b != nil && b.param != nil && b.param.Runtime {
		return b.param
	}
	return nil
}

// condParams lists the parameters the conditions under n test, sorted.
func condParams(n *Node, into map[string]bool) {
	if n == nil {
		return
	}
	if n.Cond != nil {
		into[n.Cond.Param] = true
	}
	for _, c := range n.Children {
		condParams(c, into)
	}
	for _, c := range n.Else {
		condParams(c, into)
	}
}

// maxAssignments is the most parameter assignments the draw count enumerates.
const maxAssignments = 4096

// Assignments lists every assignment of the named runtime parameters to their
// domains, in a fixed order (parameters by name, values in domain order). It
// returns false when there are more than maxAssignments.
func Assignments(params []Param, names []string) ([]map[string]string, bool) {
	sort.Strings(names)
	by := map[string]Param{} // lookup only
	for _, p := range params {
		by[p.Name] = p
	}
	out := []map[string]string{{}}
	for _, nm := range names {
		dom := by[nm].Domain()
		if len(dom) == 0 {
			continue
		}
		if len(out)*len(dom) > maxAssignments {
			return nil, false
		}
		var next []map[string]string
		for _, a := range out {
			for _, v := range dom {
				b := map[string]string{}
				for k, x := range a {
					b[k] = x
				}
				b[nm] = v
				next = append(next, b)
			}
		}
		out = next
	}
	return out, true
}

// Defaults returns the assignment of every runtime parameter's current value.
func Defaults(params []Param) map[string]string {
	out := map[string]string{}
	for _, p := range params {
		if p.Runtime {
			if v := p.Assigned(); v != "" {
				out[p.Name] = v
			}
		}
	}
	return out
}

// CondParams lists the runtime parameters that the conditions under root test.
func CondParams(root *Node, defs []*Node) []string {
	set := map[string]bool{}
	condParams(root, set)
	for _, d := range defs {
		condParams(d, set)
	}
	var out []string
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
