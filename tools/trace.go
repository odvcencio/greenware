package tools

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

const traceUsage = "greenware trace <file> <name> [--project DIR] [--json]"

// TraceUse is one place that uses the traced name.
type TraceUse struct {
	Line   int    `json:"line"`
	Column int    `json:"column"`
	In     site   `json:"in"`
	Field  string `json:"field,omitempty"`
	// Via lists the lets and parameters between the traced name and this use:
	// empty for a direct use.
	Via  []string `json:"via"`
	Text string   `json:"text"`
}

// TraceDef is the declaration of the traced name.
type TraceDef struct {
	Kind   string `json:"kind"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Text   string `json:"text"`
}

type traceReport struct {
	Schema     string     `json:"schema"`
	File       string     `json:"file"`
	Name       string     `json:"name"`
	Definition *TraceDef  `json:"definition"`
	Uses       []TraceUse `json:"uses"`
	// Reaches lists the declarations that end the chains: the parts, shapes,
	// outputs and other holders that use the name, directly or through lets.
	Reaches []site `json:"reaches"`
}

var traceableKinds = map[string]int{"param": 0, "let": 1, "material": 2, "part": 3, "template": 4, "painter": 5, "texture": 6, "output": 7, "socket": 8}

// Trace shows where a name is used: a parameter, let, material, part or
// other declaration. A use inside a let or parameter default leads on to the
// uses of that let, so the trace follows a value to the parts and outputs
// that read it. Scope is not tracked: a local let or a sample variable with
// the same name counts as a use. Exit 1 when the file declares no such name.
func Trace(args []string, stdout, stderr io.Writer) int {
	p, err := parseArgs(args, flagSet{"project": true, "json": false})
	if err != nil || len(p.pos) != 2 {
		if err == nil {
			err = fmt.Errorf("a source file and a name are needed")
		}
		return usageError(stderr, "trace", traceUsage, err)
	}
	file, name := p.pos[0], p.pos[1]
	u, err := Load(file, p.get("project"))
	if err != nil {
		fmt.Fprintf(stderr, "greenware trace: %v\n", err)
		return ExitUsage
	}
	ix := buildIndex(u.File)
	var def *declared
	for i := range ix.decls {
		d := &ix.decls[i]
		if d.Name != name {
			continue
		}
		if _, ok := traceableKinds[d.Kind]; !ok {
			continue
		}
		if def == nil || (d.Top && !def.Top) {
			def = d
		}
	}
	if def == nil {
		fmt.Fprintf(stderr, "greenware trace: %s declares no parameter, let, material, part or other value named %q\n", file, name)
		return ExitFindings
	}
	lineText := func(off int) string {
		start := off
		for start > 0 && u.Src[start-1] != '\n' {
			start--
		}
		end := off
		for end < len(u.Src) && u.Src[end] != '\n' {
			end++
		}
		return strings.TrimSpace(string(u.Src[start:end]))
	}
	dl, dc := u.Loc(def.Span.Start)
	rep := traceReport{Schema: "greenware.trace/1", File: file, Name: name, Uses: []TraceUse{}, Reaches: []site{},
		Definition: &TraceDef{Kind: def.Kind, Line: dl, Column: dc, Text: lineText(def.Span.Start)}}

	type level struct {
		name string
		via  []string
	}
	visited := map[string]bool{name: true}
	queue := []level{{name, nil}}
	reach := map[site]bool{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		var found []reference
		for _, r := range ix.refs {
			if r.Name == cur.name {
				found = append(found, r)
			}
		}
		sort.SliceStable(found, func(i, j int) bool { return found[i].Span.Start < found[j].Span.Start })
		for _, r := range found {
			l, c := u.Loc(r.Span.Start)
			via := cur.via
			if via == nil {
				via = []string{}
			}
			rep.Uses = append(rep.Uses, TraceUse{Line: l, Column: c, In: r.In, Field: r.Field, Via: via, Text: lineText(r.Span.Start)})
			if r.In.Kind == "let" || r.In.Kind == "param" {
				if !visited[r.In.Name] {
					visited[r.In.Name] = true
					queue = append(queue, level{r.In.Name, append(append([]string(nil), cur.via...), r.In.Name)})
				}
			} else {
				reach[r.In] = true
			}
		}
	}
	for s := range reach {
		rep.Reaches = append(rep.Reaches, s)
	}
	sort.Slice(rep.Reaches, func(i, j int) bool {
		a, b := rep.Reaches[i], rep.Reaches[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
	if p.has("json") {
		writeJSON(stdout, rep)
		return ExitOK
	}
	fmt.Fprintf(stdout, "%s %s  %s:%d:%d\n", def.Kind, name, file, dl, dc)
	for _, use := range rep.Uses {
		via := ""
		if len(use.Via) > 0 {
			via = " via " + strings.Join(use.Via, " > ")
		}
		fmt.Fprintf(stdout, "  %s:%d:%d in %s %s%s: %s\n", file, use.Line, use.Column, use.In.Kind, use.In.Name, via, use.Text)
	}
	if len(rep.Uses) == 0 {
		fmt.Fprintln(stdout, "  not used")
	}
	return ExitOK
}
