package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"m31labs.dev/greenware/ast"
)

const watchUsage = "  greenware watch <file> [--output NAME] [--out-dir DIR | -o DIR] [--project DIR]\n"

// A snapshot records failures too, so adding a previously missing import
// triggers a build without another edit to the entry file.
type fileSnapshot struct {
	sum     [sha256.Size]byte
	problem string
}

func captureFile(data []byte, err error) fileSnapshot {
	if err != nil {
		return fileSnapshot{problem: err.Error()}
	}
	return fileSnapshot{sum: sha256.Sum256(data)}
}

type capturedFile struct {
	state fileSnapshot
	data  []byte
	err   error
}

type buildReads struct {
	fs.FS
	files map[string]capturedFile
}

func (r *buildReads) ReadFile(name string) ([]byte, error) {
	file, seen := r.files[name]
	if !seen {
		data, err := fs.ReadFile(r.FS, name)
		file = capturedFile{state: captureFile(data, err), data: data, err: err}
		r.files[name] = file
	}
	return bytes.Clone(file.data), file.err
}

func (r *buildReads) changed() bool {
	if r == nil {
		return false
	}
	for name, before := range r.files {
		data, err := fs.ReadFile(r.FS, name)
		if captureFile(data, err) != before.state {
			return true
		}
	}
	return false
}

func runWatch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	allowed := map[string]bool{"output": true, "out-dir": true, "o": true, "project": true}
	o, err := parse(args, allowed)
	if err != nil {
		return usageFailure(stderr, "watch", err, "usage:\n"+watchUsage)
	}
	if !o.hasOut {
		o.outDir = filepath.Join("out", "preview")
	}
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	watch(ctx, ticker.C, o, stdout, stderr)
	return 0
}

func watch(ctx context.Context, ticks <-chan time.Time, o options, stdout, stderr io.Writer) {
	fmt.Fprintln(stdout, "watch: preview builds; acceptance and independent validation not run")
	watchInputs(ctx, ticks, o, func(o options, src []byte, readErr error) {
		if readErr != nil {
			fmt.Fprintf(stderr, "watch: %v; preview not updated\n", readErr)
			return
		}
		switch runBuild(o, src, stdout, stderr) {
		case 1:
			fmt.Fprintln(stderr, "watch: build rejected; preview not updated")
		case 2:
			fmt.Fprintln(stderr, "watch: preview could not be published")
		}
	})
}

func watchInputs(ctx context.Context, ticks <-chan time.Time, o options, build func(options, []byte, error)) {
	var lastSource fileSnapshot
	var reads *buildReads
	first := true
	for {
		if ctx.Err() != nil {
			return
		}
		src, readErr := os.ReadFile(o.file)
		now := captureFile(src, readErr)
		if first || now != lastSource || reads.changed() {
			first = false
			lastSource = now
			reads = nil
			o.observeProject = func(p *ast.Project) {
				reads = &buildReads{FS: p.FS, files: make(map[string]capturedFile)}
				p.FS = reads
			}
			build(o, src, readErr)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}
