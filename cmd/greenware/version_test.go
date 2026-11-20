package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/greenware/compiler"
)

func TestVersionText(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		code, out, errOut := exec(t, args...)
		if code != 0 || errOut != "" {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
		}
		first := strings.SplitN(out, "\n", 2)[0]
		if !strings.HasPrefix(first, "greenware ") || !strings.Contains(first, compiler.Generator) || !strings.Contains(first, "(go") {
			t.Errorf("%v: first line %q", args, first)
		}
	}
}

func TestVersionJSONKeysAndOrder(t *testing.T) {
	code, out, _ := exec(t, "version", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	if tok, _ := dec.Token(); tok != json.Delim('{') {
		t.Fatalf("not an object: %s", out)
	}
	var keys []string
	for dec.More() {
		k, _ := dec.Token()
		keys = append(keys, k.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"schema", "name", "version", "vcs_revision", "vcs_modified", "go", "os", "arch", "module"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Errorf("keys %v, want %v", keys, want)
	}
	v := oneJSON(t, out)
	if v["schema"] != "greenware.version/1" || v["name"] != "greenware" || v["version"] != compiler.Generator {
		t.Errorf("report: %v", v)
	}
	if mod, ok := v["module"].(map[string]any); !ok || mod["path"] == nil || mod["version"] == nil {
		t.Errorf("module: %v", v["module"])
	}
	if v["os"] == "" || v["arch"] == "" || !strings.HasPrefix(v["go"].(string), "go") {
		t.Errorf("platform: %v", v)
	}
}

func TestVersionRefusesAnArgument(t *testing.T) {
	code, _, errOut := exec(t, "version", "--foo")
	if code != 2 || strings.Count(errOut, "\n") != 1 {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestUnknownFlagIsOneLine(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"build", goalpost, "--foo"}, "greenware build: unknown flag --foo (see greenware help build)\n"},
		{[]string{"build", "--foo=3", goalpost}, "greenware build: unknown flag --foo (see greenware help build)\n"},
		{[]string{"check", goalpost, "-x"}, "greenware check: unknown flag -x (see greenware help check)\n"},
		{[]string{"fmt", goalpost, "--bad"}, "greenware fmt: unknown flag --bad (see greenware help fmt)\n"},
		{[]string{"print", goalpost, "--bad"}, "greenware print: unknown flag --bad (see greenware help print)\n"},
		{[]string{"lsp", "--bad"}, "greenware lsp: unknown flag --bad (see greenware help lsp)\n"},
	} {
		code, out, errOut := exec(t, c.args...)
		if code != 2 || errOut != c.want || out != "" {
			t.Errorf("%v: exit %d, stdout %q, stderr %q, want %q", c.args, code, out, errOut, c.want)
		}
	}
}

func TestHelpNamesOneCommand(t *testing.T) {
	code, out, _ := exec(t, "help", "build")
	if code != 0 || !strings.HasPrefix(out, "usage:\n  greenware build ") || strings.Count(out, "\n") != 2 {
		t.Errorf("exit %d\n%s", code, out)
	}
	if code, _, errOut := exec(t, "help", "nope"); code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if code, out, _ := exec(t, "help"); code != 0 || !strings.Contains(out, "greenware version") {
		t.Errorf("full usage lacks version: %d %q", code, out)
	}
}

func TestBuildShortOutDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "short")
	code, _, errOut := exec(t, "build", goalpost, "--output", "stadium", "-o", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "goalpost.glb")); err != nil {
		t.Errorf("-o did not set the folder: %v", err)
	}
	// The two spellings agree.
	dir2 := filepath.Join(t.TempDir(), "long")
	if code, _, errOut := exec(t, "build", goalpost, "--output", "stadium", "--out-dir", dir2); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	a, _ := os.ReadFile(filepath.Join(dir, "goalpost.glb"))
	b, _ := os.ReadFile(filepath.Join(dir2, "goalpost.glb"))
	if string(a) != string(b) || len(a) == 0 {
		t.Error("-o and --out-dir wrote different files")
	}
}
