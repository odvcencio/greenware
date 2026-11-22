package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/greenware/buildrecord"
)

func TestVerifyCommandChecksPublishedBytes(t *testing.T) {
	dir := t.TempDir()
	if code, out, errOut := exec(t, "build", exampleSrc, "-o", dir); code != 0 {
		t.Fatalf("build exit %d: %s %s", code, out, errOut)
	}
	record := filepath.Join(dir, "goalpost.build.json")
	getReport := func(want int, args ...string) buildrecord.Report {
		t.Helper()
		args = append([]string{"verify", record, "--json"}, args...)
		code, out, errOut := exec(t, args...)
		if code != want || errOut != "" {
			t.Fatalf("verify exit %d: %s %s", code, out, errOut)
		}
		var report buildrecord.Report
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	write := func(name string, b []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pass := getReport(0, "--strict")
	if !pass.OK || pass.Schema != buildrecord.VerificationSchema || len(pass.Files) != 6 {
		t.Fatalf("clean output did not pass: %+v", pass)
	}
	b, err := os.ReadFile(filepath.Join(dir, "goalpost.glb"))
	if err != nil {
		t.Fatal(err)
	}
	write("extra.glb", b)
	if got := getReport(0).Unexpected; len(got) != 0 {
		t.Fatalf("default inventory flagged extras: %v", got)
	}
	b[len(b)-1] ^= 1
	write("goalpost.glb", b)
	write("goalpost-lod2.glb", b[:1])
	if err := os.Remove(filepath.Join(dir, "goalpost-lod1.glb")); err != nil {
		t.Fatal(err)
	}
	failed := getReport(1, "--strict")
	if failed.OK || len(failed.Unexpected) != 1 || failed.Unexpected[0] != "extra.glb" {
		t.Fatalf("strict inventory failed: %+v", failed)
	}
	got := make(map[string]string)
	for _, row := range failed.Files {
		got[row.Name] = row.Status
	}
	for name, status := range map[string]string{
		"goalpost.glb": "digest_mismatch", "goalpost-lod1.glb": "missing",
		"goalpost-lod2.glb": "size_mismatch", "goalpost-practice.glb": "ok",
	} {
		if got[name] != status {
			t.Errorf("%s = %q, want %q", name, got[name], status)
		}
	}
}

func TestVerifyCommandRefusesInvalidInventory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.build.json")
	if code, _, _ := exec(t, "verify", path); code != 2 {
		t.Fatalf("missing record exited %d", code)
	}
	data := `{"schema":"greenware.build/1","outputs":[{"name":"main","files":[{
		"name":"../missing.glb","level":0,"bytes":128,"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := exec(t, "verify", path, "--json")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "is not a GLB basename") {
		t.Fatalf("invalid record exit %d: %s %s", code, stdout, stderr)
	}
}
