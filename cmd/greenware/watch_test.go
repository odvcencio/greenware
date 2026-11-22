package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type watchEvents chan string

func (events watchEvents) Write(b []byte) (int, error) {
	line := string(b)
	if strings.HasSuffix(line, "crate.build.json\n") || strings.Contains(line, "preview not updated") {
		events <- line
	}
	return len(b), nil
}

func TestWatchRebuildsPinnedDataAndKeepsLastGoodOnRejection(t *testing.T) {
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("../../testdata/world")); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "example", "crate.gw")
	data := filepath.Join(root, "example", "world", "standard.json")
	out := filepath.Join(root, "preview")
	file := filepath.Join(out, "crate.glb")
	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	events := make(watchEvents, 10)
	ticks := make(chan time.Time, 1)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		watch(ctx, ticks, options{file: src, outDir: out}, events, events)
	}()
	defer func() {
		cancel()
		<-done
	}()
	wait := func(want string) {
		t.Helper()
		select {
		case line := <-events:
			if !strings.Contains(line, want) {
				t.Fatalf("got %q, want %q", line, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("watch did not report %q", want)
		}
	}
	wait("crate.build.json")
	first := read(file)
	payload := read(data)
	newPayload := bytes.Replace(payload, []byte("\"24\""), []byte("\"25\""), 1)
	if bytes.Equal(payload, newPayload) {
		t.Fatal("fixture width did not change")
	}
	if err := os.WriteFile(data, newPayload, 0o644); err != nil {
		t.Fatal(err)
	}
	ticks <- time.Now()
	wait("build rejected")
	if !bytes.Equal(first, read(file)) {
		t.Fatal("rejected build replaced the preview")
	}
	code, _, stderr := exec(t, "lock", "--data", "example/world@1", root)
	if code != 0 {
		t.Fatalf("repin: exit %d: %s", code, stderr)
	}
	ticks <- time.Now()
	wait("crate.build.json")
	if bytes.Equal(first, read(file)) {
		t.Fatal("repinned data did not update the preview")
	}
}
