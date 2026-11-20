package std

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// finishDigest pins std/finish. Editing finish.gw changes every product that
// imports it, so the edit must change this line on purpose, in a reviewed
// commit. The build's input hash records the same digest.
const finishDigest = "09bc7367a35a3f76a2d01d7945f314c634041c204f1b5efcb380b62d0b3e661c"

func TestFinishIsPinned(t *testing.T) {
	d, ok := Digest("std/finish")
	if !ok || d != finishDigest {
		t.Fatalf("std/finish digest is %s, pinned %s: a changed library must be re-pinned on purpose", d, finishDigest)
	}
	b, _ := Source("std/finish")
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != d {
		t.Error("the digest is not the SHA-256 of the source bytes")
	}
}

func TestSourceLookup(t *testing.T) {
	if got := Names(); len(got) != 1 || got[0] != "finish" {
		t.Errorf("names %v", got)
	}
	for _, bad := range []string{"finish", "std/", "std/finish.gw", "std/a/b", "std/../finish", "lib/finish", ""} {
		if _, ok := Source(bad); ok {
			t.Errorf("Source(%q) found a file", bad)
		}
	}
	b, ok := Source("std/finish")
	if !ok || !bytes.HasPrefix(b, []byte("greenware 1")) {
		t.Errorf("std/finish does not start with the edition header")
	}
	if strings.Contains(string(b), "\r") {
		t.Error("the library has CR bytes: its digest would depend on checkout line endings")
	}
	for _, p := range []string{"std/hardware", "std/cloth", "std/lettering", "std/kinds"} {
		if !IsPlanned(p) {
			t.Errorf("%s is reserved", p)
		}
		if _, ok := Source(p); ok {
			t.Errorf("%s is not shipped", p)
		}
	}
	if IsPlanned("std/finish") || IsPlanned("x/hardware") {
		t.Error("IsPlanned is too wide")
	}
}
