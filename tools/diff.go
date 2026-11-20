package tools

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"m31labs.dev/greenware/units"
)

const diffUsage = "greenware diff <old.gw> <new.gw> [--fail-on breaking|change] [--project DIR] [--json]"

// field is one compared value. Show is what a reader sees; Key is what the
// comparison uses, so that 3000mm and 3m are the same default.
type field struct{ Name, Show, Key string }

type ent struct {
	Kind, Name string
	Fields     []field
	Param      *ParamInfo
}

// FieldChange is one field that differs between the two sides.
type FieldChange struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

// Change is one semantic difference.
type Change struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Change is "added", "removed", "changed" or "renamed". A renamed entity
	// carries its old name in From.
	Change   string        `json:"change"`
	From     string        `json:"from,omitempty"`
	Fields   []FieldChange `json:"fields"`
	Breaking bool          `json:"breaking"`
	Reason   string        `json:"reason,omitempty"`
}

type diffReport struct {
	Schema   string   `json:"schema"`
	Old      string   `json:"old"`
	New      string   `json:"new"`
	Changed  bool     `json:"changed"`
	Breaking bool     `json:"breaking"`
	FailOn   string   `json:"fail_on,omitempty"`
	Failed   bool     `json:"failed"`
	Changes  []Change `json:"changes"`
}

var kindOrder = []string{"asset", "meta", "import", "param", "material", "part", "socket", "plug", "mount", "collider",
	"skeleton", "joint", "skin", "morph", "clip", "level", "budget", "output", "table", "template", "painter", "texture"}

func kindRank(k string) int {
	for i, o := range kindOrder {
		if o == k {
			return i
		}
	}
	return len(kindOrder)
}

func settingFields(prefix string, ss []Setting) []field {
	var out []field
	for _, s := range ss {
		out = append(out, field{prefix + s.Name, s.Value, s.Value})
	}
	return out
}

func entities(s *Summary) []ent {
	var out []ent
	add := func(kind, name string, fs []field) { out = append(out, ent{Kind: kind, Name: name, Fields: fs}) }
	add("asset", "asset", settingFields("", s.Asset))
	add("meta", "meta", settingFields("", s.Meta))
	add("budget", "budget", settingFields("", s.Budget))
	for _, im := range s.Imports {
		add("import", im.Qualifier, []field{{"path", im.Path, im.Path}})
	}
	for i := range s.Params {
		p := &s.Params[i]
		def := field{"default", p.Default, p.Default}
		if p.Value != nil {
			def.Key = valueText(p.Value)
		}
		rng := field{"range", "", ""}
		if p.Lo != "" || p.Hi != "" {
			rng.Show = p.Lo + ".." + p.Hi
			rng.Key = rng.Show
			if p.LoValue != nil && p.HiValue != nil {
				rng.Key = valueText(p.LoValue) + ".." + valueText(p.HiValue)
			}
		}
		out = append(out, ent{Kind: "param", Name: p.Name, Param: p, Fields: []field{
			{"type", p.Type, p.Type}, def, rng, {"binding", p.Binding, p.Binding}, {"doc", p.Doc, p.Doc}}})
	}
	for _, m := range s.Materials {
		fs := []field{{"kind", m.Kind, m.Kind}}
		fs = append(fs, settingFields("", m.Settings)...)
		fs = append(fs, settingFields("fallback.", m.Fallback)...)
		add("material", m.Name, fs)
	}
	var walk func(ps []PartInfo, parent string)
	walk = func(ps []PartInfo, parent string) {
		for _, p := range ps {
			name := p.Name
			if p.Pair != "" {
				name = p.Name + "/" + p.Pair
			}
			var shapes []string
			for _, sh := range p.Shapes {
				shapes = append(shapes, strings.TrimSpace(sh.Kind+" "+sh.Name))
			}
			fs := []field{{"parent", parent, parent}, {"condition", p.Condition, p.Condition}, {"mirror", p.Mirror, p.Mirror},
				{"shapes", strings.Join(shapes, ", "), strings.Join(shapes, ", ")}}
			fs = append(fs, settingFields("", p.Settings)...)
			fs = append(fs, field{"body", p.Body, p.Body})
			add("part", name, fs)
			walk(p.Children, p.Name)
		}
	}
	walk(s.Parts, "")
	for _, k := range s.Sockets {
		name := k.Name
		if k.Pair != "" {
			name = k.Name + "/" + k.Pair
		}
		add("socket", name, []field{{"part", k.Part, k.Part}, {"kind", k.Kind, k.Kind}, {"at", k.At, k.At}})
	}
	named := func(kind string, items []NamedInfo) {
		for _, it := range items {
			fs := []field{{"kind", it.Kind, it.Kind}}
			fs = append(fs, settingFields("", it.Settings)...)
			add(kind, it.Name, fs)
		}
	}
	named("plug", s.Plugs)
	named("mount", s.Mounts)
	named("collider", s.Colliders)
	named("level", s.Levels)
	named("table", s.Tables)
	named("template", s.Templates)
	named("painter", s.Painters)
	named("texture", s.Textures)
	for _, r := range s.Rigs {
		fs := settingFields("", r.Settings)
		fs = append(fs, field{"body", r.Body, r.Body})
		add(r.Kind, r.Name, fs)
		for _, j := range r.Joints {
			add("joint", r.Name+"/"+j, nil)
		}
	}
	for _, o := range s.Outputs {
		fs := settingFields("", o.Settings)
		fs = append(fs, settingFields("set.", o.Set)...)
		fs = append(fs, settingFields("budget.", o.Budget)...)
		fs = append(fs, field{"levels", fmt.Sprint(o.Levels), fmt.Sprint(o.Levels)})
		add("output", o.Name, fs)
	}
	return out
}

func fieldMap(fs []field) map[string]field {
	m := map[string]field{}
	for _, f := range fs {
		m[f.Name] = f
	}
	return m
}

func sameFields(a, b []field) bool {
	if len(a) != len(b) {
		return false
	}
	bm := fieldMap(b)
	for _, f := range a {
		g, ok := bm[f.Name]
		if !ok || g.Key != f.Key {
			return false
		}
	}
	return true
}

func diffFields(a, b []field) []FieldChange {
	am, bm := fieldMap(a), fieldMap(b)
	names := map[string]bool{}
	for n := range am {
		names[n] = true
	}
	for n := range bm {
		names[n] = true
	}
	var sorted []string
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	out := []FieldChange{}
	for _, n := range sorted {
		x, xok := am[n]
		y, yok := bm[n]
		if xok && yok && x.Key == y.Key {
			continue
		}
		out = append(out, FieldChange{Field: n, Old: x.Show, New: y.Show})
	}
	return out
}

// breakingKind says whether removing or renaming an entity of the kind breaks
// the code that uses the asset.
func breakingKind(e ent) bool {
	switch e.Kind {
	case "socket", "joint", "output", "clip":
		return true
	case "param":
		return e.Param != nil && e.Param.Binding == "runtime"
	}
	return false
}

// narrowed reports whether the new range of a parameter holds less than the
// old one, comparing evaluated bounds.
func narrowed(a, b *ParamInfo) bool {
	less := func(x, y *units.Value) bool {
		return x != nil && y != nil && x.Kind == units.KNumber && y.Kind == units.KNumber && x.Num < y.Num
	}
	return less(a.LoValue, b.LoValue) || less(b.HiValue, a.HiValue)
}

// diffSummaries compares two Summaries by name.
func diffSummaries(oldS, newS *Summary) []Change {
	olds, news := entities(oldS), entities(newS)
	key := func(e ent) string { return e.Kind + "\x00" + e.Name }
	om, nm := map[string]ent{}, map[string]ent{}
	for _, e := range olds {
		om[key(e)] = e
	}
	for _, e := range news {
		nm[key(e)] = e
	}
	var changes []Change
	var removed, added []ent
	for _, e := range olds {
		n, ok := nm[key(e)]
		if !ok {
			removed = append(removed, e)
			continue
		}
		if fc := diffFields(e.Fields, n.Fields); len(fc) > 0 {
			c := Change{Kind: e.Kind, Name: e.Name, Change: "changed", Fields: fc}
			if e.Kind == "param" && e.Param != nil && n.Param != nil {
				for _, f := range fc {
					switch {
					case f.Field == "type":
						c.Breaking, c.Reason = true, "the parameter's type changed"
					case f.Field == "binding" && e.Param.Binding == "runtime":
						c.Breaking, c.Reason = true, "a runtime parameter became a bake parameter"
					case f.Field == "range" && narrowed(e.Param, n.Param):
						c.Breaking, c.Reason = true, "the parameter's range narrowed"
					}
				}
			}
			changes = append(changes, c)
		}
	}
	for _, e := range news {
		if _, ok := om[key(e)]; !ok {
			added = append(added, e)
		}
	}
	// A removed and an added entity of one kind with the same content (not
	// counting the parent) are one rename. Pair them in name order.
	usedAdd := map[int]bool{}
	var stillRemoved []ent
	for _, r := range removed {
		matched := false
		for i, a := range added {
			if usedAdd[i] || a.Kind != r.Kind || len(r.Fields) == 0 || !sameFields(dropParent(r.Fields), dropParent(a.Fields)) {
				continue
			}
			usedAdd[i] = true
			matched = true
			c := Change{Kind: r.Kind, Name: a.Name, Change: "renamed", From: r.Name, Fields: []FieldChange{}}
			if breakingKind(r) {
				c.Breaking, c.Reason = true, "a "+r.Kind+" was renamed; code that names it breaks"
			}
			changes = append(changes, c)
			break
		}
		if !matched {
			stillRemoved = append(stillRemoved, r)
		}
	}
	for _, r := range stillRemoved {
		c := Change{Kind: r.Kind, Name: r.Name, Change: "removed", Fields: []FieldChange{}}
		if breakingKind(r) {
			c.Breaking, c.Reason = true, "a "+r.Kind+" was removed; code that names it breaks"
		}
		changes = append(changes, c)
	}
	for i, a := range added {
		if !usedAdd[i] {
			changes = append(changes, Change{Kind: a.Kind, Name: a.Name, Change: "added", Fields: []FieldChange{}})
		}
	}
	sort.SliceStable(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if ra, rb := kindRank(a.Kind), kindRank(b.Kind); ra != rb {
			return ra < rb
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Change < b.Change
	})
	if changes == nil {
		changes = []Change{}
	}
	return changes
}

func dropParent(fs []field) []field {
	var out []field
	for _, f := range fs {
		if f.Name != "parent" {
			out = append(out, f)
		}
	}
	return out
}

// DiffCommand compares two sources by meaning: parameters, parts, materials,
// outputs, budgets and the rest, by name. Layout and comments do not count.
// It exits 0 unless --fail-on asks for a gate: "breaking" fails (1) when a
// breaking change is present, "change" when any change is.
func DiffCommand(args []string, stdout, stderr io.Writer) int {
	p, err := parseArgs(args, flagSet{"fail-on": true, "project": true, "json": false})
	if err != nil || len(p.pos) != 2 {
		if err == nil {
			err = fmt.Errorf("two source files are needed")
		}
		return usageError(stderr, "diff", diffUsage, err)
	}
	failOn := p.get("fail-on")
	if failOn != "" && failOn != "breaking" && failOn != "change" {
		return usageError(stderr, "diff", diffUsage, fmt.Errorf("--fail-on takes breaking or change, got %q", failOn))
	}
	var us [2]*Unit
	for i := range us {
		u, err := Load(p.pos[i], p.get("project"))
		if err != nil {
			fmt.Fprintf(stderr, "greenware diff: %v\n", err)
			return ExitUsage
		}
		if u.File.Broken {
			printDiags(stderr, p.pos[i], u.Lowered, u.Loc)
			fmt.Fprintf(stderr, "greenware diff: %s does not parse, so it cannot be compared\n", p.pos[i])
			return ExitFindings
		}
		us[i] = u
	}
	changes := diffSummaries(Summarize(us[0]), Summarize(us[1]))
	rep := diffReport{Schema: "greenware.diff/1", Old: p.pos[0], New: p.pos[1], Changed: len(changes) > 0, FailOn: failOn, Changes: changes}
	for _, c := range changes {
		if c.Breaking {
			rep.Breaking = true
		}
	}
	rep.Failed = (failOn == "breaking" && rep.Breaking) || (failOn == "change" && rep.Changed)
	code := ExitOK
	if rep.Failed {
		code = ExitFindings
	}
	if p.has("json") {
		writeJSON(stdout, rep)
		return code
	}
	if !rep.Changed {
		fmt.Fprintln(stdout, "no semantic changes")
		return code
	}
	for _, c := range changes {
		mark := map[string]string{"added": "+", "removed": "-", "changed": "~", "renamed": ">"}[c.Change]
		name := c.Name
		if c.Change == "renamed" {
			name = c.From + " -> " + c.Name
		}
		if c.Name == c.Kind {
			name = ""
		}
		fmt.Fprintf(stdout, "%s", strings.TrimSpace(mark+" "+c.Kind+" "+name))
		if c.Breaking {
			fmt.Fprint(stdout, "  BREAKING")
		}
		fmt.Fprintln(stdout)
		for _, f := range c.Fields {
			fmt.Fprintf(stdout, "    %s: %s -> %s\n", f.Field, orDash(f.Old), orDash(f.New))
		}
	}
	return code
}

func orDash(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
