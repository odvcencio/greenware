package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	printGoalpost  = "../../examples/goalpost/goalpost.gw"
	printCandidate = "../../testdata/print/goalpost-stadium.candidate.sha256"
	printOpenPipe  = "../../testdata/diag/GW-PRINT.gw"
)

func candidateSHA(t *testing.T) string {
	t.Helper()
	for _, ln := range strings.Split(readFile(t, printCandidate), "\n") {
		if ln != "" && !strings.HasPrefix(ln, "#") {
			return strings.Fields(ln)[0]
		}
	}
	t.Fatal("no candidate digest")
	return ""
}

func TestPrintGoalPostWritesCandidate(t *testing.T) {
	out := filepath.Join(t.TempDir(), "goalpost.3mf")
	code, stdout, errOut := exec(t, "print", printGoalpost, "--output", "stadium", "--level", "0", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, errOut)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != candidateSHA(t) {
		t.Errorf("sha256 %s, candidate %s", got, candidateSHA(t))
	}
	if !strings.Contains(stdout, "goalpost/crossbar/cylinder") || !strings.Contains(stdout, "closed true") || !strings.Contains(stdout, "cm3") {
		t.Errorf("summary:\n%s", stdout)
	}
	// The default output and level give the same bytes.
	out2 := filepath.Join(t.TempDir(), "again.3mf")
	if code, o, e := exec(t, "print", printGoalpost, "--out", out2); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, o, e)
	}
	if readFile(t, out2) != string(b) {
		t.Error("the default output differs from stadium level 0")
	}
}

func TestPrintRefusesToOverwrite(t *testing.T) {
	out := filepath.Join(t.TempDir(), "post.3mf")
	if code, o, e := exec(t, "print", printGoalpost, "--out", out); code != 0 {
		t.Fatalf("first run: exit %d\n%s%s", code, o, e)
	}
	first := readFile(t, out)
	code, _, errOut := exec(t, "print", printGoalpost, "--out", out)
	if code != 2 || !strings.Contains(errOut, "--force") {
		t.Errorf("second run: exit %d, stderr %q", code, errOut)
	}
	if readFile(t, out) != first {
		t.Error("the second run changed the file")
	}
	if code, o, e := exec(t, "print", printGoalpost, "--out", out, "--force"); code != 0 {
		t.Errorf("--force: exit %d\n%s%s", code, o, e)
	}
}

func TestPrintOpenPipeExits1(t *testing.T) {
	out := filepath.Join(t.TempDir(), "pipe.3mf")
	code, stdout, errOut := exec(t, "print", printOpenPipe, "--output", "demo", "--out", out)
	if code != 1 || stdout != "" || !strings.Contains(errOut, "GW-PRINT") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, errOut)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a refused print wrote a file")
	}
}

func TestPrintJSON(t *testing.T) {
	out := filepath.Join(t.TempDir(), "post.3mf")
	code, stdout, errOut := exec(t, "print", printGoalpost, "--out", out, "--scale", "0.01", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, errOut)
	}
	keys := keyOrder(t, stdout)
	if keys[0] != "schema" {
		t.Errorf("keys %v", keys)
	}
	rep := oneJSON(t, stdout)
	if rep["schema"] != "greenware.print/1" || rep["ok"] != true || rep["written"] != true || rep["scale"] != 0.01 {
		t.Errorf("report %v", rep)
	}
	code, stdout, _ = exec(t, "print", printOpenPipe, "--output", "demo", "--out", out+"x", "--json")
	if code != 1 || oneJSON(t, stdout)["ok"] != false {
		t.Errorf("refusal: exit %d\n%s", code, stdout)
	}
}

func TestPrintUsage(t *testing.T) {
	for _, args := range [][]string{{"print"}, {"print", printGoalpost, "--level", "x"}, {"print", printGoalpost, "--scale", "0"}, {"print", printGoalpost, "--bogus"}, {"print", "missing.gw"}} {
		if code, _, _ := exec(t, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
