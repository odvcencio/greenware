package tools

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const initUsage = "greenware init <dir> [--name NAME] [--author TEXT] [--license SPDX] [--dry-run] [--json]"

var identRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// InitFile is one file init creates.
type InitFile struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

type initReport struct {
	Schema  string     `json:"schema"`
	Project string     `json:"project"`
	Edition int        `json:"edition"`
	Dir     string     `json:"dir"`
	Files   []InitFile `json:"files"`
	DryRun  bool       `json:"dry_run"`
	OK      bool       `json:"ok"`
	// Conflicts lists the files that already exist; init writes nothing then.
	Conflicts []string `json:"conflicts"`
}

// sampleSource is the example asset: a marker post that passes check and
// build with no change. The asset name replaces the placeholder NAME.
const sampleSource = `greenware 1

// A marker post: a cylinder with a ball on top. Change a parameter, run
// ` + "`greenware check`" + `, then ` + "`greenware build`" + `.
%s
asset NAME {
  title = "NAME"
  root = NAME
  length = m
  angle = deg
  up = y
  forward = -z
}

param height : length = 1.2m in 0.3m..3m
param radius : length = 0.05m in 0.01m..0.3m

material paint = pbr { base = #d9482b roughness = 0.5 metallic = 0 }

part NAME {
  material = paint
  cylinder post {
    at = (0, height / 2, 0)
    radius = radius
    height = height
    segments = 16
    caps = on
  }
  sphere ball {
    at = (0, height, 0)
    radius = radius * 1.5
    segments = 16
  }
  socket top {
    kind = anchor
    at = (0, height + radius * 1.5, 0)
  }
}

output main {
  file = "NAME.glb"
  lods = files
}
`

func sampleFor(name, author, license string) string {
	meta := ""
	if author != "" && license != "" {
		meta = fmt.Sprintf("\nmeta {\n  author = %q\n  license = %q\n}\n", author, license)
	}
	return fmt.Sprintf(strings.ReplaceAll(sampleSource, "NAME", name), meta)
}

// Init creates a project: greenware.mod, one example asset that passes check
// and build, and greenware.sum. It never overwrites: if any of the files
// exists it writes nothing and exits 1. The licence of the artwork is not
// guessed: --author and --license add a meta block when both are given.
func Init(args []string, stdout, stderr io.Writer) int {
	p, err := parseArgs(args, flagSet{"name": true, "author": true, "license": true, "dry-run": false, "json": false})
	if err != nil || len(p.pos) != 1 {
		if err == nil {
			err = fmt.Errorf("one directory is needed")
		}
		return usageError(stderr, "init", initUsage, err)
	}
	dir := p.pos[0]
	name := p.get("name")
	if name == "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			fmt.Fprintf(stderr, "greenware init: %v\n", err)
			return ExitUsage
		}
		name = strings.ToLower(strings.NewReplacer("-", "_", " ", "_", ".", "_").Replace(filepath.Base(abs)))
	}
	if !identRE.MatchString(name) {
		return usageError(stderr, "init", initUsage, fmt.Errorf("the project name %q must start with a letter and hold only a-z, 0-9 and _; pass --name", name))
	}
	if (p.get("author") == "") != (p.get("license") == "") {
		return usageError(stderr, "init", initUsage, fmt.Errorf("--author and --license go together"))
	}
	rep := initReport{Schema: "greenware.init/1", Project: name, Edition: 1, Dir: dir, DryRun: p.has("dry-run"), Files: []InitFile{}, Conflicts: []string{}}
	files := []struct {
		name string
		data string
	}{
		{"greenware.mod", "project " + name + "\ngreenware 1\n"},
		{name + ".gw", sampleFor(name, p.get("author"), p.get("license"))},
		{SumFile, ""},
	}
	for _, f := range files {
		if _, err := os.Lstat(filepath.Join(dir, f.name)); err == nil {
			rep.Conflicts = append(rep.Conflicts, f.name)
		}
	}
	code := ExitOK
	if len(rep.Conflicts) > 0 {
		code = ExitFindings
	} else if !rep.DryRun {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "greenware init: %v\n", err)
			return ExitUsage
		}
		for _, f := range files[:2] {
			if err := writeNew(filepath.Join(dir, f.name), []byte(f.data)); err != nil {
				fmt.Fprintf(stderr, "greenware init: %v\n", err)
				return ExitUsage
			}
		}
		pins, err := computePins(dir)
		if err != nil {
			fmt.Fprintf(stderr, "greenware init: %v\n", err)
			return ExitUsage
		}
		if err := writeNew(filepath.Join(dir, SumFile), renderSum(pins)); err != nil {
			fmt.Fprintf(stderr, "greenware init: %v\n", err)
			return ExitUsage
		}
	}
	action := "create"
	if rep.DryRun {
		action = "would create"
	}
	for _, f := range files {
		a := action
		if len(rep.Conflicts) > 0 {
			a = "conflict"
			found := false
			for _, c := range rep.Conflicts {
				found = found || c == f.name
			}
			if !found {
				a = "skip"
			}
		}
		rep.Files = append(rep.Files, InitFile{Path: filepath.ToSlash(filepath.Join(dir, f.name)), Action: a})
	}
	rep.OK = code == ExitOK
	if p.has("json") {
		writeJSON(stdout, rep)
		return code
	}
	if code != ExitOK {
		for _, c := range rep.Conflicts {
			fmt.Fprintf(stderr, "greenware init: %s already exists; nothing was written\n", filepath.Join(dir, c))
		}
		return code
	}
	for _, f := range rep.Files {
		fmt.Fprintf(stdout, "%s %s\n", f.Action, f.Path)
	}
	return code
}

// writeNew creates a file and fails if it exists.
func writeNew(name string, data []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
