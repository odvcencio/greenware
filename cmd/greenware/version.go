package main

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"

	"m31labs.dev/greenware/compiler"
)

const versionUsage = "  greenware version [--json]\n"

// versionReport is `greenware version --json` (schema greenware.version/1).
// The keys keep this order. vcs_revision and vcs_modified are null when the
// binary carries no version control stamp (a build outside a checkout, or with
// -buildvcs=false).
type versionReport struct {
	Schema      string        `json:"schema"`
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	VCSRevision *string       `json:"vcs_revision"`
	VCSModified *bool         `json:"vcs_modified"`
	Go          string        `json:"go"`
	OS          string        `json:"os"`
	Arch        string        `json:"arch"`
	Module      versionModule `json:"module"`
}

type versionModule struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

// readVersion collects what the binary knows about itself. Version is the
// compiler's generator string (compiler.Generator, `greenware 0.1`), which is
// also written into every GLB.
func readVersion() versionReport {
	r := versionReport{
		Schema: "greenware.version/1", Name: "greenware", Version: compiler.Generator,
		Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		r.Module = versionModule{Path: bi.Main.Path, Version: bi.Main.Version}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev := s.Value
				r.VCSRevision = &rev
			case "vcs.modified":
				mod := s.Value == "true"
				r.VCSModified = &mod
			}
		}
	}
	return r
}

// runVersion prints the version: one line, `greenware <generator version>
// (<go version>, <os>/<arch>)`, then a revision line when the binary has one.
func runVersion(args []string, stdout, stderr io.Writer) int {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		default:
			if len(a) > 0 && a[0] == '-' {
				fmt.Fprintf(stderr, "greenware version: unknown flag %s (see greenware help version)\n", a)
			} else {
				fmt.Fprintf(stderr, "greenware version: unexpected argument %q (see greenware help version)\n", a)
			}
			return 2
		}
	}
	r := readVersion()
	if asJSON {
		writeJSON(stdout, r)
		return 0
	}
	fmt.Fprintf(stdout, "%s (%s, %s/%s)\n", r.Version, r.Go, r.OS, r.Arch)
	if r.VCSRevision != nil {
		line := "revision " + *r.VCSRevision
		if r.VCSModified != nil && *r.VCSModified {
			line += " (modified)"
		}
		fmt.Fprintln(stdout, line)
	}
	return 0
}
