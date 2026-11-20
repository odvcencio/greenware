package accept

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/lang"
)

// GW-ACCEPT comes from the acceptance run, not from the front end, so its
// fixture (testdata/diag/GW-ACCEPT.gw) is checked here. The fixture holds a
// source and its acceptance block. The synthetic goal post of build_test.go stands in for
// the compiler's output, because accept must not import the compiler.
func TestAcceptFixtureReportsTheCode(t *testing.T) {
	e, ok := diag.Lookup(diag.CodeAccept)
	if !ok || e.Severity != diag.Error {
		t.Fatalf("catalogue entry: %+v %v", e, ok)
	}
	path := filepath.Join("..", filepath.FromSlash(e.Fixture))
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ds := ast.Lower(lang.Parse(src)); len(ds) != 0 {
		t.Fatalf("the front end reports %v for the fixture", ds)
	}
	dir := t.TempDir()
	b, err := gltf.Encode(defaultModel().document())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.glb"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := Command([]string{path, path, "--glb-dir", dir}, &out, &errOut); code != ExitFail {
		t.Errorf("exit %d\n%s\n%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "error GW-ACCEPT:") {
		t.Errorf("stderr lacks the code:\n%s", errOut.String())
	}
}
