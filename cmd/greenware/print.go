package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/resolve"
)

const printUsage = `  greenware print <file> [--output NAME] [--level N] [--scale S] [--out FILE] [--force] [--project DIR] [--json]
`

type printOptions struct {
	file, output, out, project string
	level                      int
	scale                      float64
	force, json                bool
}

func parsePrint(args []string) (printOptions, error) {
	o := printOptions{scale: 1}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			if o.file != "" {
				return o, fmt.Errorf("unexpected argument %q", a)
			}
			o.file = a
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		takes := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag --%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		var v string
		var err error
		switch name {
		case "json":
			o.json = true
		case "force":
			o.force = true
		case "output":
			o.output, err = takes()
		case "out":
			o.out, err = takes()
		case "project":
			o.project, err = takes()
		case "level":
			if v, err = takes(); err == nil {
				n, perr := strconv.Atoi(v)
				if perr != nil || n < 0 {
					return o, fmt.Errorf("flag --level needs a whole number, zero or more, got %q", v)
				}
				o.level = n
			}
		case "scale":
			if v, err = takes(); err == nil {
				s, perr := strconv.ParseFloat(v, 64)
				if perr != nil || !(s > 0) {
					return o, fmt.Errorf("flag --scale needs a positive number, got %q", v)
				}
				o.scale = s
			}
		default:
			return o, fmt.Errorf("unknown flag %q", a)
		}
		if err != nil {
			return o, err
		}
	}
	if o.file == "" {
		return o, errors.New("a source file is needed")
	}
	return o, nil
}

type printObjectJSON struct {
	Name      string     `json:"name"`
	Triangles int        `json:"triangles"`
	Closed    bool       `json:"closed"`
	VolumeCM3 float64    `json:"volume_cm3"`
	LoMM      [3]float64 `json:"lo_mm"`
	HiMM      [3]float64 `json:"hi_mm"`
}

type printReport struct {
	Schema      string            `json:"schema"`
	OK          bool              `json:"ok"`
	Written     bool              `json:"written"`
	Output      string            `json:"output,omitempty"`
	Level       int               `json:"level"`
	Scale       float64           `json:"scale"`
	Out         string            `json:"out,omitempty"`
	Bytes       int               `json:"bytes,omitempty"`
	SHA256      string            `json:"sha256,omitempty"`
	Triangles   int               `json:"triangles,omitempty"`
	Objects     []printObjectJSON `json:"objects"`
	Notes       []string          `json:"notes"`
	Diagnostics json.RawMessage   `json:"diagnostics"`
}

func runPrint(args []string, stdout, stderr io.Writer) int {
	o, err := parsePrint(args)
	if err != nil {
		fmt.Fprintf(stderr, "greenware print: %v\n%s", err, printUsage)
		return 2
	}
	src, err := os.ReadFile(o.file)
	if err != nil {
		fmt.Fprintf(stderr, "greenware print: %v\n", err)
		return 2
	}
	f, ds, loc := front(o.file, o.project, src, false)
	rep := printReport{Schema: "greenware.print/1", Level: o.level, Scale: o.scale, Objects: []printObjectJSON{}, Notes: []string{}}
	var p *compiler.PrintProduct
	if !diag.HasErrors(ds) {
		output := o.output
		if output == "" && !f.Broken {
			if refs, _ := resolve.Outputs(f); len(refs) > 0 {
				output = refs[0].Name
			}
		}
		var more []diag.Diagnostic
		p, more = compiler.BuildPrintWith(f, output, o.level, compiler.PrintOptions{Scale: o.scale})
		ds = diag.Dedup(append(ds, more...))
		rep.Output = output
	}
	code := status(ds)
	var writeErr error
	if p != nil && code == 0 {
		out := o.out
		if out == "" {
			out = p.Name
		}
		writeErr = writeNew(out, p.Bytes, o.force)
		if writeErr == nil {
			rep.Written, rep.Out = true, out
		}
		rep.Bytes, rep.SHA256, rep.Triangles = len(p.Bytes), p.SHA256, p.Triangles
		rep.Notes = append(rep.Notes, p.Notes...)
		for _, ob := range p.Objects {
			rep.Objects = append(rep.Objects, printObjectJSON{ob.Name, ob.Triangles, ob.Closed, ob.VolumeMM3 / 1000, ob.Lo, ob.Hi})
		}
	}
	rep.OK = code == 0 && writeErr == nil
	if writeErr != nil {
		code = 2
	}
	if o.json {
		b, err := diag.Marshal(ds, loc)
		if err != nil {
			fmt.Fprintln(stderr, "greenware print:", err)
			return 2
		}
		rep.Diagnostics = bytes.TrimSpace(b)
		writeJSON(stdout, rep)
		if writeErr != nil {
			fmt.Fprintln(stderr, "greenware print:", writeErr)
		}
		return code
	}
	printText(stderr, o.file, ds, loc)
	if writeErr != nil {
		fmt.Fprintln(stderr, "greenware print:", writeErr)
	}
	if rep.OK {
		fmt.Fprintf(stdout, "%s  %d bytes, %d objects, %d triangles, sha256 %s\n", rep.Out, rep.Bytes, len(rep.Objects), rep.Triangles, rep.SHA256)
		for _, ob := range rep.Objects {
			fmt.Fprintf(stdout, "%s  %d triangles, closed %t, %.3f cm3, bounds (%.2f, %.2f, %.2f) to (%.2f, %.2f, %.2f) mm\n",
				ob.Name, ob.Triangles, ob.Closed, ob.VolumeCM3, ob.LoMM[0], ob.LoMM[1], ob.LoMM[2], ob.HiMM[0], ob.HiMM[1], ob.HiMM[2])
		}
		for _, n := range rep.Notes {
			fmt.Fprintf(stdout, "note: %s\n", n)
		}
	}
	return code
}

// writeNew writes data to name. Unless force is set it refuses to replace a
// file that exists.
func writeNew(name string, data []byte, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	fh, err := os.OpenFile(name, flags, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s exists; use --force to replace it", name)
		}
		return err
	}
	if _, err := fh.Write(data); err != nil {
		fh.Close()
		return err
	}
	return fh.Close()
}
