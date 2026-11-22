package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSocketsCommand(t *testing.T) {
	out := t.TempDir()
	if code, stdout, stderr := exec(t, "build", exampleSrc, "-o", out); code != 0 {
		t.Fatalf("build exit %d: %s %s", code, stdout, stderr)
	}
	path := filepath.Join(out, "goalpost.glb")
	code, stdout, stderr := exec(t, "sockets", path, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("sockets exit %d: %s %s", code, stdout, stderr)
	}
	var report socketReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatal(err)
	}
	if report.Schema != "greenware.sockets/1" || len(report.Sockets) != 5 {
		t.Fatalf("unexpected report %+v", report)
	}
	if ribbon := report.Sockets[3]; ribbon.Name != "ribbon_l" || ribbon.Kind != "ribbon" || ribbon.Translation[1] < 12 {
		t.Errorf("ribbon_l = %+v", ribbon)
	}
	if code, _, _ := exec(t, "sockets", filepath.Join(out, "absent.glb")); code != 2 {
		t.Fatalf("missing file exited %d", code)
	}
	corrupt := filepath.Join(out, "corrupt.glb")
	if err := os.WriteFile(corrupt, []byte("bad GLB"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := exec(t, "sockets", corrupt, "--json"); code != 1 || stdout != "" || !strings.Contains(stderr, "corrupt.glb") {
		t.Fatalf("invalid file exit %d: %s %s", code, stdout, stderr)
	}
}
