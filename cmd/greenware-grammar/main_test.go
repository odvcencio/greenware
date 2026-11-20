package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The committed blob and EBNF reference are the output of the grammar in
// language/grammar. If this fails, run `go generate ./lang`.
func TestCommittedFilesMatchTheGrammar(t *testing.T) {
	dir := t.TempDir()
	bin, ebnf := filepath.Join(dir, "greenware.bin"), filepath.Join(dir, "grammar.ebnf")
	if err := run(bin, ebnf, true); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ got, committed string }{
		{bin, filepath.Join("..", "..", "lang", "greenware.bin")},
		{ebnf, filepath.Join("..", "..", "docs", "reference", "grammar.ebnf")},
	} {
		got, err := os.ReadFile(c.got)
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(c.committed)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from the grammar's output (%d and %d bytes); run go generate ./lang", c.committed, len(want), len(got))
		}
	}
}
