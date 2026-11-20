package depcheck

import (
	"os/exec"
	"strings"
	"testing"
)

// The negative control: a closure with forbidden imports must be caught.
func TestViolationsCatchesForbidden(t *testing.T) {
	deps := []string{"fmt", "github.com/odvcencio/gosx/scene", "C", "m31labs.dev/greenware/det", "github.com/odvcencio/gotreesitter/grammars/go"}
	got := Violations(deps)
	if len(got) != 3 {
		t.Fatalf("want 3 violations, got %v", got)
	}
	if v := Violations([]string{"fmt", "github.com/odvcencio/gotreesitter", "m31labs.dev/greenware/gltf"}); len(v) != 0 {
		t.Fatalf("clean closure flagged: %v", v)
	}
}

// The real closure of the core packages and commands must be clean.
func TestCoreClosureIsClean(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "./...")
	cmd.Dir = "../.."
	cmd.Env = append(cmd.Environ(), "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	deps := strings.Fields(string(out))
	if v := Violations(deps); len(v) != 0 {
		t.Fatalf("forbidden imports in the core closure: %v", v)
	}
}
