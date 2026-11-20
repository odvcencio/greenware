package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const worldFixture = "../../testdata/world"

// copyWorld copies the data-package fixture project into a new folder.
func copyWorld(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"greenware.mod", "example/crate.gw", "example/world.lock", "example/world/schema-v1.json", "example/world/standard.json"} {
		copyFile(t, filepath.Join(worldFixture, filepath.FromSlash(f)), filepath.Join(dir, filepath.FromSlash(f)))
	}
	return dir
}

func editFile(t *testing.T, name, old, new string) {
	t.Helper()
	s := readFile(t, name)
	if !strings.Contains(s, old) {
		t.Fatalf("%s has no %q", name, old)
	}
	if err := os.WriteFile(name, []byte(strings.Replace(s, old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A stale pin fails the build with GW-LOCK and nothing is written; repinning
// with `lock --data` lets the build through.
func TestBuildRefusesAStalePinAndWritesNothing(t *testing.T) {
	dir := copyWorld(t)
	out := filepath.Join(t.TempDir(), "out")
	crate := filepath.Join(dir, "example", "crate.gw")
	if code, _, errOut := exec(t, "build", crate, "--out-dir", out); code != 0 {
		t.Fatalf("clean build: %d %s", code, errOut)
	}
	if err := os.RemoveAll(out); err != nil {
		t.Fatal(err)
	}
	editFile(t, filepath.Join(dir, "example/world/standard.json"), `"24"`, `"25"`)
	code, stdout, errOut := exec(t, "build", crate, "--out-dir", out)
	if code != 1 || stdout != "" || !strings.Contains(errOut, "GW-LOCK") || !strings.Contains(errOut, "expected sha256 6a972715") {
		t.Fatalf("stale build: %d\n%s%s", code, stdout, errOut)
	}
	if !strings.Contains(errOut, "crate.gw:8:8:") {
		t.Errorf("the diagnostic is not at the import: %s", errOut)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("the failed build created the output folder")
	}
	// check refuses it too.
	if code, _, errOut := exec(t, "check", crate); code != 1 || !strings.Contains(errOut, "GW-LOCK") {
		t.Errorf("check: %d %s", code, errOut)
	}
	// Repin explicitly, then build.
	if code, so, e := exec(t, "lock", "--data", "example/world@1", dir); code != 0 || !strings.Contains(so, "written") {
		t.Fatalf("lock --data: %d %s%s", code, so, e)
	}
	if code, _, errOut := exec(t, "build", crate, "--out-dir", out); code != 0 {
		t.Fatalf("build after repin: %d %s", code, errOut)
	}
}

func TestLockDataCheckDryRunAndRefusals(t *testing.T) {
	dir := copyWorld(t)
	lockFile := filepath.Join(dir, "example", "world.lock")
	before := readFile(t, lockFile)
	if code, out, _ := exec(t, "lock", "--data", "example/world@1", dir, "--check"); code != 0 || !strings.Contains(out, "up to date") {
		t.Errorf("check on a good lock: %d %s", code, out)
	}
	editFile(t, filepath.Join(dir, "example/world/standard.json"), `"24"`, `"25"`)
	code, out, _ := exec(t, "lock", "--data", "example/world@1", dir, "--check")
	if code != 1 || !strings.Contains(out, "~ data example/world@1 sha256:") || readFile(t, lockFile) != before {
		t.Errorf("check on a stale lock: %d %s", code, out)
	}
	if code, _, _ := exec(t, "lock", "--data", "example/world@1", dir, "--dry-run"); code != 0 || readFile(t, lockFile) != before {
		t.Error("dry run wrote or failed")
	}
	// A data file that does not hold together is refused and the lock stays.
	editFile(t, filepath.Join(dir, "example/world/standard.json"), `"unit": "in"`, `"unit": "deg"`)
	code, _, errOut := exec(t, "lock", "--data", "example/world@1", dir)
	if code != 1 || !strings.Contains(errOut, "$.values.crate.width.unit") || readFile(t, lockFile) != before {
		t.Errorf("lock of a bad package: %d %s", code, errOut)
	}
	// Usage errors.
	if code, _, _ := exec(t, "lock", "--data", "example/world", dir); code != 2 {
		t.Errorf("a name without a major: %d", code)
	}
	if code, _, errOut := exec(t, "lock", "--data", "example/other@1", dir); code != 2 || !strings.Contains(errOut, "--schema-file") {
		t.Errorf("a first lock without files: %d %s", code, errOut)
	}
	if code, _, _ := exec(t, "lock", dir, "--schema-file", "x.json"); code != 2 {
		t.Errorf("--schema-file without --data: %d", code)
	}
}

// The first lock of a package names the files; the project lock then records
// the data line, and a repin shows as drift.
func TestLockRecordsDataPinsAndFlagsDrift(t *testing.T) {
	dir := copyWorld(t)
	if err := os.Remove(filepath.Join(dir, "example", "world.lock")); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := exec(t, "lock", dir); code != 1 || !strings.Contains(errOut, "no lock file") {
		t.Fatalf("project lock with no package lock: %d %s", code, errOut)
	}
	args := []string{"lock", "--data", "example/world@1", dir, "--schema-file", "example/world/schema-v1.json", "--data-file", "example/world/standard.json"}
	if code, out, errOut := exec(t, args...); code != 0 || !strings.Contains(out, "+ data example/world@1") {
		t.Fatalf("first lock: %d %s%s", code, out, errOut)
	}
	if got, want := readFile(t, filepath.Join(dir, "example", "world.lock")), readFile(t, filepath.Join(worldFixture, "example", "world.lock")); got != want {
		t.Errorf("the lock differs from the fixture:\n%s", got)
	}
	if code, _, errOut := exec(t, "lock", dir); code != 0 {
		t.Fatalf("project lock: %d %s", code, errOut)
	}
	sum := readFile(t, filepath.Join(dir, "greenware.sum"))
	want := "data example/world@1 contract example/world@1 sha256:6a972715adc467fefb3e4c892958ee75a1cdb752a407b817fbd2af606e9beab5\n"
	if !strings.Contains(sum, want) {
		t.Errorf("sum:\n%s", sum)
	}
	if code, _, _ := exec(t, "lock", dir, "--check"); code != 0 {
		t.Error("check right after lock")
	}
	// Edit the data and repin the package: the project lock is behind.
	editFile(t, filepath.Join(dir, "example/world/standard.json"), `"24"`, `"26"`)
	if code, _, errOut := exec(t, "lock", dir, "--check"); code != 1 || !strings.Contains(errOut, "GW-LOCK") && !strings.Contains(errOut, "do not match the pin") {
		t.Errorf("check with a stale package pin: %d %s", code, errOut)
	}
	exec(t, "lock", "--data", "example/world@1", dir)
	code, out, _ := exec(t, "lock", dir, "--check")
	if code != 1 || !strings.Contains(out, "~ data example/world@1") {
		t.Errorf("check after a repin: %d %s", code, out)
	}
}

func TestDescribeShowsDataMeaning(t *testing.T) {
	dir := copyWorld(t)
	code, out, errOut := exec(t, "describe", filepath.Join(dir, "example", "crate.gw"))
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	for _, want := range []string{
		"data example/world@1 as world: profile standard, sha256 6a972715",
		"world.crate.width : length = 24 in  distance between the two outer side faces of the crate, along x",
		"world.crate.tilt : angle = 15 deg  angle of the lid about the x axis when the crate is open",
		"world.crate.slat_count : number = 6",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("describe lacks %q:\n%s", want, out)
		}
	}
}
