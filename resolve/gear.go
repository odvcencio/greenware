package resolve

import (
	"sort"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/registry"
	"m31labs.dev/greenware/units"
)

// This file reads the project imports, the mounts and the two checks that
// name them (`check runtime`, `check rig`). A project import brings in a
// whole asset, called gear when a mount uses it: its file is lowered, hashed
// and pinned, and the compiler builds the gear output a mount selects.

// GearImport is a project file that an import brought in.
type GearImport struct {
	Path      string    `json:"path"`
	Qualifier string    `json:"qualifier"`
	SHA256    string    `json:"sha256"`
	Pins      []ast.Pin `json:"pins"`
	File      *ast.File `json:"-"`
	Outputs   []string  `json:"outputs"`
	Resolved  *Resolved `json:"-"` // the gear at its own defaults
	span      diag.Span
}

// MountSet is one value a mount sets on its gear.
type MountSet struct {
	Name string
	// Expr is the value as written; it is read in the gear's scope, because
	// an enum member is a name of the gear.
	Expr ast.Expr `json:"-"`
	// Text is the source of Expr, the key two assignments are compared by.
	Text string
	Span diag.Span `json:"-"`
}

// Mount is a resolved mount.
type Mount struct {
	Name   string
	Gear   string // the import qualifier
	Socket string
	Mode   string // runtime or bake
	Set    []MountSet
	// HasFit says the mount carries a fit check; Clearance is in metres.
	HasFit    bool
	Clearance float64
	// SocketKind and SocketJoint describe the host socket the mount meets;
	// SocketJoint is empty for a socket on a part.
	SocketKind  string
	SocketJoint string
	// Plug is the gear's plug that meets the socket, with its position in the
	// gear's frame in metres.
	Plug     string
	PlugAt   [3]float64
	PlugKind string
	// Output is the gear output the mount selects, and the file it writes.
	Output     string
	OutputFile string
	// Overrides are the mount's set values, ready to apply to the gear: a
	// bare name stays an expression of the gear's scope.
	Overrides []Override `json:"-"`
	Span      diag.Span  `json:"-"`
	FitSpan   diag.Span  `json:"-"`
}

// GearRef names a gear output: `helmet.classic`.
type GearRef struct {
	Qualifier, Output string
}

// RuntimeCase is a named point of `check runtime`.
type RuntimeCase struct {
	Name string
	Set  []MountSet
	Gear *GearRef
	Span diag.Span `json:"-"`
}

// RuntimeCheck is `check runtime`.
type RuntimeCheck struct {
	Corners []string
	Each    []string
	Gear    []GearRef
	Pairs   [][2]string
	Cases   []RuntimeCase
	Span    diag.Span `json:"-"`
}

// RigCheck is `check rig`, with the edition's defaults for what the source
// leaves out.
type RigCheck struct {
	GroundLo, GroundHi float64 // metres
	Waist, Legs        [2]string
	HasWaist, HasLegs  bool
	WaistRatio         float64
	Torn, Open         float64
	BandsError         bool
	Span               diag.Span `json:"-"`
	WaistSpan          diag.Span `json:"-"`
	LegsSpan           diag.Span `json:"-"`
}

// exprText is the source of an expression.
func (r *run) exprText(e ast.Expr) string {
	sp := e.Span()
	if sp.End > len(r.f.Source) || sp.Start > sp.End {
		return ""
	}
	return strings.TrimSpace(string(r.f.Source[sp.Start:sp.End]))
}

// loadGear reads one project import.
func (r *run) loadGear(im *ast.Import) *GearImport {
	if r.f.Project == nil || r.f.Project.FS == nil {
		r.report(diag.New(diag.CodeUnsupported, im.PathSpan, "importing the project file %q needs a project directory, and this source was read without one; only `std/` libraries are read", im.Path))
		return nil
	}
	g, ds := r.f.Project.Import(im.Path, im.PathSpan)
	for _, d := range ds {
		r.report(d)
	}
	if g == nil {
		return nil
	}
	if diag.HasErrors(g.Diagnostics) {
		r.report(diag.New(diag.CodeSyntax, im.PathSpan, "the project file %q does not read cleanly: %s", im.Path, g.Diagnostics[0].Message))
		return nil
	}
	gi := &GearImport{Path: g.Path, Qualifier: im.Qualifier, SHA256: g.SHA256, Pins: g.Pins, File: g.File, span: im.Span}
	res, gds := Evaluate(g.File, nil)
	if diag.HasErrors(gds) || res == nil {
		msg := "it does not evaluate"
		if len(gds) > 0 {
			msg = gds[0].Message
		}
		r.report(diag.New(diag.CodeSyntax, im.PathSpan, "the project file %q does not evaluate at its defaults: %s", im.Path, msg))
		return nil
	}
	gi.Resolved = res
	for _, o := range g.File.Outputs {
		gi.Outputs = append(gi.Outputs, o.Name.Name)
	}
	return gi
}

// gearOverrides makes overrides from a mount's assignments. A bare name is an
// enum member of the gear and is read in the gear's scope; any other
// expression is evaluated here, in the host's scope.
func (r *run) gearOverrides(sets []MountSet) []Override {
	var out []Override
	for _, s := range sets {
		if _, isName := s.Expr.(*ast.Name); isName {
			out = append(out, Override{Name: s.Name, Expr: s.Expr, Span: s.Span, NameSpan: s.Span})
			continue
		}
		v := r.eval(s.Expr, r.globals)
		if !v.Valid() {
			continue
		}
		out = append(out, Override{Name: s.Name, Value: &v, Span: s.Span, NameSpan: s.Span})
	}
	return out
}

func (r *run) mountSets(ss []*ast.Setting) []MountSet {
	var out []MountSet
	for _, s := range ss {
		if s.Value == nil {
			continue
		}
		out = append(out, MountSet{Name: s.Name, Expr: s.Value, Text: r.exprText(s.Value), Span: s.Span})
	}
	return out
}

// hostSocket finds a socket of the host by name: a joint socket of the
// skeleton, then a socket node of the part tree.
func (r *run) hostSocket(name string) (kind, joint string, ok bool) {
	if rg := r.resolved.Rig; rg != nil && rg.Skeleton != nil {
		for _, s := range rg.Skeleton.Sockets {
			if s.Name == name {
				return s.Kind, s.Joint, true
			}
		}
	}
	var walk func(n *Node) bool
	walk = func(n *Node) bool {
		if n == nil {
			return false
		}
		if n.Kind == "socket" && n.Name == name {
			if v, has := n.Get("kind"); has {
				kind = v.Str
			}
			return true
		}
		for _, c := range n.Children {
			if walk(c) {
				return true
			}
		}
		return false
	}
	if walk(r.resolved.Root) {
		return kind, "", true
	}
	for _, d := range r.resolved.Definitions {
		if walk(d) {
			return kind, "", true
		}
	}
	return "", "", false
}

// mounts resolves every mount: the gear's plug and output, the set values and
// the fit clearance.
func (r *run) mounts() {
	seen := map[string]diag.Span{}
	for _, md := range r.f.Mounts {
		if prev, dup := seen[md.Name.Name]; dup {
			r.report(diag.New(diag.CodeDuplicate, md.Name.Span, "the mount `%s` is declared twice", md.Name.Name).WithRelated(prev, "first mount"))
			continue
		}
		seen[md.Name.Name] = md.Name.Span
		m := Mount{Name: md.Name.Name, Mode: "runtime", Span: md.Name.Span}
		as, ss := ast.FindSetting(md.Settings, "asset"), ast.FindSetting(md.Settings, "socket")
		if as == nil || ss == nil {
			r.report(diag.New(diag.CodeMissing, md.Name.Span, "the mount `%s` needs `asset` and `socket`", m.Name))
			continue
		}
		an, aok := as.Value.(*ast.Name)
		sn, sok := ss.Value.(*ast.Name)
		if !aok || !sok {
			r.report(diag.New(diag.CodeType, md.Name.Span, "`asset` and `socket` of a mount are names"))
			continue
		}
		if ms := ast.FindSetting(md.Settings, "mode"); ms != nil {
			if n, ok := ms.Value.(*ast.Name); ok && (n.Name == "runtime" || n.Name == "bake") {
				m.Mode = n.Name
			} else {
				r.report(diag.New(diag.CodeType, ms.Span, "`mode` is runtime or bake"))
				continue
			}
		}
		m.Gear, m.Socket = an.Name, sn.Name
		gear := r.gears[m.Gear]
		if gear == nil {
			d := diag.New(diag.CodeUnknownName, an.Span(), "`%s` is not an imported project asset", m.Gear)
			if s, ok := registry.Suggest(m.Gear, r.gearNames()); ok {
				d = d.WithEdit("write "+s, an.Span(), s)
			}
			r.report(d)
			continue
		}
		kind, joint, ok := r.hostSocket(m.Socket)
		if !ok {
			r.report(diag.New(diag.CodeMount, sn.Span(), "the mount `%s` names the socket `%s`, which this asset does not have", m.Name, m.Socket))
			continue
		}
		m.SocketKind, m.SocketJoint = kind, joint
		// The plug: the gear's plug of the socket's kind; among several, the
		// one named like the socket.
		var plugs []*Node
		for _, p := range gear.Resolved.Plugs {
			if v, has := p.Get("kind"); has && v.Str == kind {
				plugs = append(plugs, p)
			}
		}
		var plug *Node
		switch {
		case len(plugs) == 1:
			plug = plugs[0]
		case len(plugs) > 1:
			for _, p := range plugs {
				if p.Name == m.Socket {
					plug = p
				}
			}
			if plug == nil {
				r.report(diag.New(diag.CodeMount, md.Name.Span, "the mount `%s`: %d plugs of %s have the kind `%s` and none is named `%s`", m.Name, len(plugs), gear.Path, kind, m.Socket))
				continue
			}
		default:
			r.report(diag.New(diag.CodeMount, md.Name.Span, "the mount `%s`: %s has no plug of the kind `%s` that the socket `%s` meets", m.Name, gear.Path, kind, m.Socket))
			continue
		}
		m.Plug, m.PlugKind = plug.Name, kind
		if v, has := plug.Get("at"); has && v.Valid() && len(v.Elems) == 3 {
			m.PlugAt = [3]float64{v.Elems[0].Num, v.Elems[1].Num, v.Elems[2].Num}
		}
		m.Set = r.mountSets(md.Set)
		m.Overrides = r.gearOverrides(m.Set)
		bad := false
		for _, s := range m.Set {
			if !gearHasParam(gear, s.Name) {
				r.report(diag.New(diag.CodeUnknownName, s.Span, "`%s` is not a parameter of %s", s.Name, gear.Path))
				bad = true
			}
		}
		if bad {
			continue
		}
		out, ok := r.selectGearOutput(m, gear, md)
		if !ok {
			continue
		}
		m.Output = out.Name.Name
		if fs := ast.FindSetting(out.Settings, "file"); fs != nil {
			if lit, ok := fs.Value.(*ast.StringLit); ok {
				m.OutputFile = lit.Value
			}
		}
		if md.HasFit {
			m.HasFit, m.FitSpan = true, md.FitSpan
			if cs := ast.FindSetting(md.Fit, "clearance"); cs != nil {
				v := r.coerce(r.eval(cs.Value, r.globals), registry.Type{Kind: registry.TLength}, cs.Value.Span())
				if v.Valid() {
					if v.Num < 0 {
						r.report(diag.New(diag.CodeFieldRange, cs.Value.Span(), "the clearance must not be negative"))
					}
					m.Clearance = v.Num
				}
			} else {
				r.report(diag.New(diag.CodeMissing, md.FitSpan, "`fit` needs `clearance`"))
			}
		}
		r.resolved.Mounts = append(r.resolved.Mounts, m)
	}
}

func (r *run) gearNames() []string {
	var out []string
	for q := range r.gears {
		out = append(out, q)
	}
	sort.Strings(out)
	return out
}

func gearHasParam(g *GearImport, name string) bool {
	for _, p := range g.Resolved.Params {
		if p.Name == name {
			return true
		}
	}
	return false
}

// selectGearOutput finds the one gear output whose bake parameters agree with
// the mount's `set`.
func (r *run) selectGearOutput(m Mount, g *GearImport, md *ast.Mount) (*ast.Output, bool) {
	ovs := r.gearOverrides(m.Set)
	want, wds := Evaluate(g.File, ovs)
	if diag.HasErrors(wds) || want == nil {
		for _, d := range wds {
			if d.Severity == diag.Error {
				r.report(diag.New(diag.CodeMount, md.Name.Span, "the mount `%s`: its `set` does not suit %s: %s", m.Name, g.Path, d.Message))
				return nil, false
			}
		}
		return nil, false
	}
	var cands []*ast.Output
	for _, o := range g.File.Outputs {
		res, ds := EvaluateOutput(g.File, o.Name.Name)
		if res == nil || diag.HasErrors(ds) {
			continue
		}
		ok := true
		for _, s := range m.Set {
			p := paramOf(res.Params, s.Name)
			w := paramOf(want.Params, s.Name)
			if p == nil || w == nil || p.Runtime {
				continue
			}
			if !units.Equal(p.Value, w.Value) {
				ok = false
			}
		}
		if ok {
			cands = append(cands, o)
		}
	}
	if len(cands) != 1 {
		var names []string
		for _, c := range cands {
			names = append(names, c.Name.Name)
		}
		what := "selects no output of " + g.Path
		if len(cands) > 1 {
			what = "selects " + strings.Join(names, " and ") + " of " + g.Path + " alike; set the bake parameter that tells them apart"
		}
		r.report(diag.New(diag.CodeMount, md.Name.Span, "the mount `%s`: its `set` %s", m.Name, what))
		return nil, false
	}
	return cands[0], true
}

func paramOf(ps []Param, name string) *Param {
	for i := range ps {
		if ps[i].Name == name {
			return &ps[i]
		}
	}
	return nil
}

// ---- the checks that name runtime points and the audit ---------------------------

// gearRef reads `helmet.classic`.
func gearRefOf(e ast.Expr) (GearRef, bool) {
	switch x := e.(type) {
	case *ast.Qualified:
		if len(x.Parts) == 2 {
			return GearRef{x.Parts[0], x.Parts[1]}, true
		}
	case *ast.Member:
		if n, ok := x.X.(*ast.Name); ok {
			return GearRef{n.Name, x.Name}, true
		}
	}
	return GearRef{}, false
}

func listOf(e ast.Expr) []ast.Expr {
	switch x := e.(type) {
	case *ast.List:
		return x.Elems
	case *ast.Tuple:
		return x.Elems
	}
	return []ast.Expr{e}
}

// auditChecks reads `check rig` and `check runtime`.
func (r *run) auditChecks() {
	for _, c := range r.f.Checks {
		switch c.Name.Name {
		case "rig":
			if r.resolved.RigCheck != nil {
				r.report(diag.New(diag.CodeDuplicate, c.Name.Span, "a file has one `check rig`"))
				continue
			}
			r.resolved.RigCheck = r.rigCheck(c)
		case "runtime":
			if r.resolved.RuntimeCheck != nil {
				r.report(diag.New(diag.CodeDuplicate, c.Name.Span, "a file has one `check runtime`"))
				continue
			}
			r.resolved.RuntimeCheck = r.runtimeCheck(c)
		}
	}
}

func (r *run) rigCheck(c *ast.Check) *RigCheck {
	rc := &RigCheck{GroundLo: -0.005, GroundHi: 0.020, WaistRatio: 0.95, Torn: 0.01, Open: 0.02, Span: c.Name.Span}
	number := func(s *ast.Setting) (float64, bool) {
		v := r.coerce(r.eval(s.Value, r.globals), registry.Type{Kind: registry.TNumber}, s.Value.Span())
		return v.Num, v.Valid()
	}
	for _, s := range c.Settings {
		switch s.Name {
		case "ground":
			if s.Range == nil {
				r.report(diag.New(diag.CodeType, s.Span, "`ground` is a length range, such as -5mm..20mm"))
				continue
			}
			lo := r.coerce(r.eval(s.Range.Lo, r.globals), registry.Type{Kind: registry.TLength}, s.Range.Lo.Span())
			hi := r.coerce(r.eval(s.Range.Hi, r.globals), registry.Type{Kind: registry.TLength}, s.Range.Hi.Span())
			if lo.Valid() && hi.Valid() {
				if lo.Num > hi.Num {
					r.report(diag.New(diag.CodeFieldRange, s.Span, "the ground band is empty"))
					continue
				}
				rc.GroundLo, rc.GroundHi = lo.Num, hi.Num
			}
		case "waist", "legs":
			names, ok := nameListOf(s.Value)
			if !ok || len(names) != 2 {
				r.report(diag.New(diag.CodeType, s.Span, "`%s` is a pair of joints: (lower, upper)", s.Name))
				continue
			}
			for _, n := range names {
				if !r.jointExists(n) {
					r.report(diag.New(diag.CodeUnknownName, s.Value.Span(), "`%s` is not a joint", n))
				}
			}
			if s.Name == "waist" {
				rc.Waist, rc.HasWaist, rc.WaistSpan = [2]string{names[0], names[1]}, true, s.Span
			} else {
				rc.Legs, rc.HasLegs, rc.LegsSpan = [2]string{names[0], names[1]}, true, s.Span
			}
		case "waist_ratio":
			if v, ok := number(s); ok {
				if v <= 0 || v > 1 {
					r.report(diag.New(diag.CodeFieldRange, s.Span, "`waist_ratio` is above 0 and at most 1"))
					continue
				}
				rc.WaistRatio = v
			}
		case "torn", "open":
			if v, ok := number(s); ok {
				if v < 0 || v > 1 {
					r.report(diag.New(diag.CodeFieldRange, s.Span, "`%s` is a share from 0%% to 100%%", s.Name))
					continue
				}
				if s.Name == "torn" {
					rc.Torn = v
				} else {
					rc.Open = v
				}
			}
		case "bands":
			n, ok := s.Value.(*ast.Name)
			if !ok || (n.Name != "warning" && n.Name != "error") {
				r.report(diag.New(diag.CodeType, s.Span, "`bands` is warning or error"))
				continue
			}
			rc.BandsError = n.Name == "error"
		}
	}
	return rc
}

func (r *run) jointExists(name string) bool {
	if r.resolved.Rig == nil || r.resolved.Rig.Skeleton == nil {
		return false
	}
	for _, j := range r.resolved.Rig.Skeleton.Joints {
		if j.Name == name {
			return true
		}
	}
	return false
}

func (r *run) runtimeCheck(c *ast.Check) *RuntimeCheck {
	rc := &RuntimeCheck{Span: c.Name.Span}
	runtimeParam := func(e ast.Expr, what string) (string, bool) {
		n, ok := e.(*ast.Name)
		if !ok {
			r.report(diag.New(diag.CodeType, e.Span(), "%s names runtime parameters", what))
			return "", false
		}
		p := paramOf(r.resolved.Params, n.Name)
		if p == nil {
			r.report(diag.New(diag.CodeUnknownName, n.Span(), "`%s` is not a parameter", n.Name))
			return "", false
		}
		if !p.Runtime {
			r.report(diag.New(diag.CodeType, n.Span(), "`%s` is not a runtime parameter", n.Name))
			return "", false
		}
		return n.Name, true
	}
	gearRef := func(e ast.Expr) (GearRef, bool) {
		g, ok := gearRefOf(e)
		if !ok {
			r.report(diag.New(diag.CodeType, e.Span(), "a gear is written `import_name.output`, such as helmet.aero"))
			return g, false
		}
		gi := r.gears[g.Qualifier]
		if gi == nil {
			r.report(diag.New(diag.CodeUnknownName, e.Span(), "`%s` is not an imported project asset", g.Qualifier))
			return g, false
		}
		for _, o := range gi.Outputs {
			if o == g.Output {
				return g, true
			}
		}
		r.report(diag.New(diag.CodeUnknownName, e.Span(), "%s has no output named `%s`", gi.Path, g.Output))
		return g, false
	}
	for _, s := range c.Settings {
		switch s.Name {
		case "corners":
			for _, e := range listOf(s.Value) {
				if n, ok := runtimeParam(e, "`corners`"); ok {
					p := paramOf(r.resolved.Params, n)
					if p.Lo == nil || p.Hi == nil {
						r.report(diag.New(diag.CodeType, e.Span(), "`%s` has no finite range, so it has no corners", n))
						continue
					}
					rc.Corners = append(rc.Corners, n)
				}
			}
		case "each":
			for _, e := range listOf(s.Value) {
				if n, ok := runtimeParam(e, "`each`"); ok {
					rc.Each = append(rc.Each, n)
				}
			}
		case "gear":
			for _, e := range listOf(s.Value) {
				if g, ok := gearRef(e); ok {
					rc.Gear = append(rc.Gear, g)
				}
			}
		case "pairs":
			for _, e := range listOf(s.Value) {
				ps := listOf(e)
				if len(ps) != 2 {
					r.report(diag.New(diag.CodeType, e.Span(), "a pair is (a, b)"))
					continue
				}
				a, aok := runtimeParam(ps[0], "`pairs`")
				b, bok := runtimeParam(ps[1], "`pairs`")
				if aok && bok {
					rc.Pairs = append(rc.Pairs, [2]string{a, b})
				}
			}
		}
	}
	names := map[string]bool{}
	for _, cs := range c.Cases {
		if names[cs.Name.Name] {
			r.report(diag.New(diag.CodeDuplicate, cs.Name.Span, "the case `%s` is declared twice", cs.Name.Name))
			continue
		}
		names[cs.Name.Name] = true
		rcase := RuntimeCase{Name: cs.Name.Name, Span: cs.Name.Span, Set: r.mountSets(cs.Set)}
		for _, s := range rcase.Set {
			if p := paramOf(r.resolved.Params, s.Name); p == nil || !p.Runtime {
				r.report(diag.New(diag.CodeUnknownName, s.Span, "`%s` is not a runtime parameter", s.Name))
			}
		}
		if g := ast.FindSetting(cs.Settings, "gear"); g != nil {
			if ref, ok := gearRef(g.Value); ok {
				rcase.Gear = &ref
			}
		}
		rc.Cases = append(rc.Cases, rcase)
	}
	return rc
}

// pins lists the project files and table files this evaluation read, sorted by
// path, once each.
func (r *run) pins() {
	seen := map[string]string{}
	for _, g := range r.resolved.Gears {
		for _, p := range g.Pins {
			seen[p.Path] = p.SHA256
		}
	}
	if len(r.f.Tables) > 0 {
		tables, _ := loadTables(r.f)
		for _, t := range tables {
			seen[t.Path] = t.SHA256
		}
	}
	var out []ast.Pin
	for p, h := range seen {
		out = append(out, ast.Pin{Path: p, SHA256: h})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	r.resolved.Pins = out
}

// EvaluateGear evaluates one output of an imported project file with extra
// overrides laid over the output's own `set`: the way a mount, or a runtime
// point of `check runtime`, chooses the gear it fits. An extra value replaces
// the output's value for the same parameter.
func EvaluateGear(g *GearImport, output string, extra []Override) (*Resolved, []diag.Diagnostic) {
	ref, ds := lookupOutput(g.File, output)
	if ref == nil {
		return nil, append(ds, diag.New(diag.CodeUnknownName, diag.Span{}, "%s has no output named %q", g.Path, output))
	}
	ovs, ods := ref.overrides()
	replaced := map[string]bool{}
	for _, e := range extra {
		replaced[e.Name] = true
	}
	var all []Override
	for _, o := range ovs {
		if !replaced[o.Name] {
			all = append(all, o)
		}
	}
	all = append(all, extra...)
	res, more := evaluate(g.File, ref.Name, all, false, ref)
	return res, diag.Dedup(append(append(ds, ods...), more...))
}
