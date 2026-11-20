package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestLSPCheckConfig(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"lsp", "--json"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var got lspConfig
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != "greenware.lsp/1" || got.Started || got.SyncKind != "incremental" || len(got.GrammarSHA256) != 64 {
		t.Errorf("config: %+v", got)
	}
	if len(got.PositionEncodings) != 1 || got.PositionEncodings[0] != "utf-16" {
		t.Errorf("encodings: %v", got.PositionEncodings)
	}
	out.Reset()
	if code := run([]string{"lsp", "--check-config"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "started: false") {
		t.Errorf("--check-config: %d %q", code, out.String())
	}
	if code := run([]string{"lsp", "--bogus"}, &out, &errb); code != 2 {
		t.Errorf("unknown flag: %d, want 2", code)
	}
}
