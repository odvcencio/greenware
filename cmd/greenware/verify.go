package main

import (
	"errors"
	"fmt"
	"io"

	"m31labs.dev/greenware/buildrecord"
)

const verifyUsage = "  greenware verify <asset.build.json> [--strict] [--json]\n"

func runVerify(args []string, stdout, stderr io.Writer) int {
	o, err := parse(args, map[string]bool{"strict": true, "json": true})
	if err != nil {
		return usageFailure(stderr, "verify", err, "usage:\n"+verifyUsage)
	}
	report, err := buildrecord.Verify(o.file, buildrecord.Options{Strict: o.strict})
	if err != nil {
		fmt.Fprintf(stderr, "greenware verify: %s: %v\n", o.file, err)
		if errors.Is(err, buildrecord.ErrInvalidRecord) {
			return 1
		}
		return 2
	}
	if o.json {
		writeJSON(stdout, report)
	} else {
		for _, file := range report.Files {
			fmt.Fprintf(stdout, "%s: %s", file.Name, file.Status)
			if file.Detail != "" {
				fmt.Fprintf(stdout, " (%s)", file.Detail)
			}
			fmt.Fprintln(stdout)
		}
		for _, name := range report.Unexpected {
			fmt.Fprintf(stdout, "%s: unlisted GLB\n", name)
		}
		if report.RecordChanged {
			fmt.Fprintln(stdout, "Build record changed during verification.")
		}
	}
	if !report.OK {
		return 1
	}
	return 0
}
