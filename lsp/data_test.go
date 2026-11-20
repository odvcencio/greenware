package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Hovering a field of an imported data package shows the schema's meaning, the
// dimension and the value as written, and the package pin.
func TestHoverOnADataPackageField(t *testing.T) {
	abs, err := filepath.Abs("../testdata/world/example/crate.gw")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	c := startServer(t)
	c.initialize()
	uri := "file://" + filepath.ToSlash(abs)
	c.open(uri, string(src))
	for i, l := range strings.Split(string(src), "\n") {
		col := strings.Index(l, "world.crate.width")
		if !strings.HasPrefix(l, "let width") || col < 0 {
			continue
		}
		// On the last name of the path, not on the qualifier.
		var h Hover
		c.call("textDocument/hover", textDocumentPosition{TextDocument: textDocumentIdentifier{URI: uri}, Position: Position{Line: i, Character: col + len("world.crate.wi")}}, &h)
		for _, want := range []string{
			"world.crate.width", "length", "24 in",
			"distance between the two outer side faces of the crate, along x",
			"example/world@1", "profile `standard`", "6a972715adc467fefb3e4c892958ee75a1cdb752a407b817fbd2af606e9beab5",
		} {
			if !strings.Contains(h.Contents.Value, want) {
				t.Errorf("hover lacks %q:\n%s", want, h.Contents.Value)
			}
		}
		if h.Range == nil || h.Range.Start.Character != col || h.Range.End.Character != col+len("world.crate.width") {
			t.Errorf("hover range %+v", h.Range)
		}
		return
	}
	t.Fatal("the fixture has no `let width` line")
}
