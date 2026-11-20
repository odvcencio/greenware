package tools

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const searchUsage = "greenware search <query> [dir] [--kind KIND] [--json]"

// Match is one search result.
type Match struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	// Parent is the part that holds the declaration, when there is one.
	Parent string `json:"parent,omitempty"`
}

type searchReport struct {
	Schema  string  `json:"schema"`
	Query   string  `json:"query"`
	Dir     string  `json:"dir"`
	Kind    string  `json:"kind,omitempty"`
	Files   int     `json:"files"`
	Matches []Match `json:"matches"`
}

// Search finds names in the .gw files under a directory. A name matches when
// it contains the query, ignoring case. It searches the names of parameters,
// lets, materials, parts, shapes, sockets, outputs, clips and the other
// declarations of the syntax tree, and the codes of the diagnostics a file
// reports (kind "diagnostic"). Zero matches is a success. Results are sorted
// by path, line, column, kind and name.
func Search(args []string, stdout, stderr io.Writer) int {
	p, err := parseArgs(args, flagSet{"kind": true, "json": false})
	if err != nil || len(p.pos) < 1 || len(p.pos) > 2 {
		if err == nil {
			err = fmt.Errorf("a query and at most one directory are needed")
		}
		return usageError(stderr, "search", searchUsage, err)
	}
	query, dir := p.pos[0], "."
	if len(p.pos) == 2 {
		dir = p.pos[1]
	}
	if query == "" {
		return usageError(stderr, "search", searchUsage, fmt.Errorf("the query is empty"))
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		fmt.Fprintf(stderr, "greenware search: %s is not a directory\n", dir)
		return ExitUsage
	}
	kind := p.get("kind")
	q := strings.ToLower(query)
	var files []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".gw") && d.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(stderr, "greenware search: %v\n", err)
		return ExitUsage
	}
	sort.Strings(files)
	rep := searchReport{Schema: "greenware.search/1", Query: query, Dir: dir, Kind: kind, Matches: []Match{}}
	for _, file := range files {
		u, err := Load(file, "")
		if err != nil {
			fmt.Fprintf(stderr, "greenware search: %v\n", err)
			continue
		}
		rep.Files++
		for _, d := range buildIndex(u.File).decls {
			if (kind != "" && d.Kind != kind) || !strings.Contains(strings.ToLower(d.Name), q) {
				continue
			}
			l, c := u.Loc(d.Span.Start)
			rep.Matches = append(rep.Matches, Match{Path: file, Line: l, Column: c, Kind: d.Kind, Name: d.Name, Parent: d.Parent})
		}
		if kind == "" || kind == "diagnostic" {
			for _, d := range u.Diags {
				if strings.Contains(strings.ToLower(d.Code), q) {
					l, c := u.Loc(d.Span.Start)
					rep.Matches = append(rep.Matches, Match{Path: file, Line: l, Column: c, Kind: "diagnostic", Name: d.Code})
				}
			}
		}
	}
	sort.SliceStable(rep.Matches, func(i, j int) bool {
		a, b := rep.Matches[i], rep.Matches[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
	if p.has("json") {
		writeJSON(stdout, rep)
		return ExitOK
	}
	for _, m := range rep.Matches {
		fmt.Fprintf(stdout, "%s:%d:%d %s %s\n", m.Path, m.Line, m.Column, m.Kind, m.Name)
	}
	return ExitOK
}
