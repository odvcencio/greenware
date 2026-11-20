package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"m31labs.dev/greenware/units"
)

const describeUsage = "greenware describe <file> [--project DIR] [--json]"

type describeReport struct {
	Schema string `json:"schema"`
	File   string `json:"file"`
	OK     bool   `json:"ok"`
	*Summary
	Diagnostics json.RawMessage `json:"diagnostics"`
}

// Describe prints a structured summary of a file: the asset block,
// parameters, outputs and levels, the part tree, materials, sockets, plugs,
// mounts, clips and budgets. It runs the front end only. It exits 1 when the
// file has an error diagnostic; the summary is still printed.
func Describe(args []string, stdout, stderr io.Writer) int {
	p, err := parseArgs(args, flagSet{"project": true, "json": false})
	if err != nil || len(p.pos) != 1 {
		if err == nil {
			err = fmt.Errorf("one source file is needed")
		}
		return usageError(stderr, "describe", describeUsage, err)
	}
	u, err := Load(p.pos[0], p.get("project"))
	if err != nil {
		fmt.Fprintf(stderr, "greenware describe: %v\n", err)
		return ExitUsage
	}
	sum := Summarize(u)
	code := ExitOK
	if countErrors(u.Diags) > 0 {
		code = ExitFindings
	}
	if p.has("json") {
		writeJSON(stdout, describeReport{Schema: "greenware.describe/1", File: p.pos[0], OK: code == 0, Summary: sum, Diagnostics: diagJSON(u.Diags, u.Loc)})
		return code
	}
	printDiags(stderr, p.pos[0], u.Diags, u.Loc)
	renderSummary(stdout, p.pos[0], sum)
	return code
}

func settingList(ss []Setting) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = s.Name + " = " + s.Value
	}
	return strings.Join(parts, "; ")
}

func valueText(v *units.Value) string {
	if v == nil {
		return ""
	}
	b, err := v.MarshalJSON()
	if err != nil {
		return ""
	}
	return string(b)
}

func renderSummary(w io.Writer, file string, s *Summary) {
	name := settingValue(s.Asset, "title")
	fmt.Fprintf(w, "asset %s (%s)\n", file, strings.Trim(firstNonEmpty(name, settingValue(s.Asset, "root")), `"`))
	for _, a := range s.Asset {
		fmt.Fprintf(w, "  %s = %s\n", a.Name, a.Value)
	}
	if len(s.Meta) > 0 {
		fmt.Fprintf(w, "meta: %s\n", settingList(s.Meta))
	}
	if len(s.Imports) > 0 {
		fmt.Fprintln(w, "imports:")
		for _, im := range s.Imports {
			fmt.Fprintf(w, "  %q as %s\n", im.Path, im.Qualifier)
		}
	}
	fmt.Fprintf(w, "parameters (%d):\n", len(s.Params))
	for _, p := range s.Params {
		rng := ""
		if p.Lo != "" || p.Hi != "" {
			rng = fmt.Sprintf(" in %s..%s", p.Lo, p.Hi)
		}
		unit := ""
		if p.Unit != "" {
			unit = " [" + p.Unit + "]"
		}
		fmt.Fprintf(w, "  %s : %s%s = %s%s  %s\n", p.Name, p.Type, unit, p.Default, rng, p.Binding)
	}
	fmt.Fprintf(w, "outputs (%d):\n", len(s.Outputs))
	for _, o := range s.Outputs {
		fmt.Fprintf(w, "  %s file=%s lods=%s levels=%v", o.Name, o.File, firstNonEmpty(o.Lods, "-"), o.Levels)
		if len(o.Set) > 0 {
			fmt.Fprintf(w, " set{%s}", settingList(o.Set))
		}
		if len(o.Budget) > 0 {
			fmt.Fprintf(w, " budget{%s}", settingList(o.Budget))
		}
		fmt.Fprintln(w)
	}
	for _, l := range s.Levels {
		fmt.Fprintf(w, "level %s: %s\n", l.Name, settingList(l.Settings))
	}
	fmt.Fprintln(w, "parts:")
	for _, p := range s.Parts {
		renderPart(w, p, 1)
	}
	fmt.Fprintf(w, "materials (%d):\n", len(s.Materials))
	for _, m := range s.Materials {
		fmt.Fprintf(w, "  %s = %s { %s }\n", m.Name, m.Kind, settingList(m.Settings))
	}
	if len(s.Sockets) > 0 {
		fmt.Fprintln(w, "sockets:")
		for _, k := range s.Sockets {
			n := k.Name
			if k.Pair != "" {
				n = "(" + k.Name + ", " + k.Pair + ")"
			}
			fmt.Fprintf(w, "  %s on %s kind=%s at=%s\n", n, k.Part, firstNonEmpty(k.Kind, "-"), firstNonEmpty(k.At, "-"))
		}
	}
	for _, group := range []struct {
		title string
		items []NamedInfo
	}{{"plugs", s.Plugs}, {"mounts", s.Mounts}, {"colliders", s.Colliders}, {"tables", s.Tables}} {
		if len(group.items) == 0 {
			continue
		}
		fmt.Fprintf(w, "%s:\n", group.title)
		for _, it := range group.items {
			fmt.Fprintf(w, "  %s %s { %s }\n", firstNonEmpty(it.Kind, "-"), it.Name, settingList(it.Settings))
		}
	}
	if len(s.Rigs) > 0 {
		fmt.Fprintln(w, "rig:")
		for _, r := range s.Rigs {
			fmt.Fprintf(w, "  %s %s", r.Kind, r.Name)
			if len(r.Joints) > 0 {
				fmt.Fprintf(w, " joints=%s", strings.Join(r.Joints, ","))
			}
			fmt.Fprintln(w)
		}
	}
	if len(s.Budget) > 0 {
		fmt.Fprintf(w, "budget: %s\n", settingList(s.Budget))
	}
}

func renderPart(w io.Writer, p PartInfo, depth int) {
	ind := strings.Repeat("  ", depth)
	n := p.Name
	if p.Pair != "" {
		n = "(" + p.Name + ", " + p.Pair + ")"
	}
	extra := ""
	if p.Condition != "" {
		extra += " when " + p.Condition
	}
	if p.Mirror != "" {
		extra += " mirror " + p.Mirror
	}
	var shapes []string
	for _, s := range p.Shapes {
		shapes = append(shapes, strings.TrimSpace(s.Kind+" "+s.Name))
	}
	fmt.Fprintf(w, "%s%s [%s]%s", ind, n, p.Role, extra)
	if len(shapes) > 0 {
		fmt.Fprintf(w, " shapes: %s", strings.Join(shapes, ", "))
	}
	fmt.Fprintln(w)
	for _, c := range p.Children {
		renderPart(w, c, depth+1)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
