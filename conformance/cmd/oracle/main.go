// Command oracle checks a GLB file with the independent decoder and geometry
// measurements. Usage:
//
//	oracle check [--strict] [--node NAME]... [--gap NODE:AXIS:X,Y,Z]... file.glb
//
// It prints one JSON report. Exit status: 0 no findings, 1 findings (errors;
// with --strict also warnings), 2 usage or unreadable file.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const usage = "usage: oracle check [--strict] [--node NAME]... [--gap NODE:AXIS:X,Y,Z]... file.glb\n"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var opts options
	var file string
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		take := func(name string) (string, bool) {
			if v, ok := strings.CutPrefix(a, name+"="); ok {
				return v, true
			}
			if a == name && i+1 < len(rest) {
				i++
				return rest[i], true
			}
			return "", false
		}
		switch {
		case a == "--strict":
			opts.strict = true
		case a == "--node" || strings.HasPrefix(a, "--node="):
			v, ok := take("--node")
			if !ok {
				fmt.Fprint(stderr, usage)
				return 2
			}
			opts.nodes = append(opts.nodes, v)
		case a == "--gap" || strings.HasPrefix(a, "--gap="):
			v, ok := take("--gap")
			if !ok {
				fmt.Fprint(stderr, usage)
				return 2
			}
			g, err := parseGap(v)
			if err != nil {
				fmt.Fprintf(stderr, "oracle: %v\n", err)
				return 2
			}
			opts.gaps = append(opts.gaps, g)
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "oracle: unknown flag %s\n%s", a, usage)
			return 2
		case file == "":
			file = a
		default:
			fmt.Fprint(stderr, usage)
			return 2
		}
	}
	if file == "" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(stderr, "oracle: %v\n", err)
		return 2
	}
	opts.dir = filepath.Dir(file)
	rep := check(file, data, opts)
	if err := writeJSON(stdout, rep); err != nil {
		fmt.Fprintf(stderr, "oracle: %v\n", err)
		return 2
	}
	if rep.Counts.Errors > 0 || (opts.strict && rep.Counts.Warnings > 0) {
		return 1
	}
	return 0
}
