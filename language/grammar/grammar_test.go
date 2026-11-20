package grammar

import (
	"bytes"
	"testing"

	"github.com/odvcencio/gotreesitter/grammargen"
)

// Regenerating the blob gives the same bytes every time.
func TestBlobIsDeterministic(t *testing.T) {
	first, err := Blob()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Blob()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("two generations differ: %d and %d bytes", len(first), len(second))
	}
}

func TestEBNFIsDeterministic(t *testing.T) {
	first, err := EBNF()
	if err != nil {
		t.Fatal(err)
	}
	second, err := EBNF()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("two renderings differ")
	}
}

func TestGrammarValidates(t *testing.T) {
	if warnings := grammargen.Validate(Greenware()); len(warnings) > 0 {
		t.Fatalf("grammar warnings: %v", warnings)
	}
}

// The parser must stay small. Before the hidden choice rules were preserved,
// grammargen copied "_expression" into every rule that used it: 61,000
// productions and 28,000 states.
func TestGrammarStaysSmall(t *testing.T) {
	ng, err := grammargen.Normalize(Greenware())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ng.Productions); got > 400 {
		t.Errorf("%d productions, want at most 400", got)
	}
	report, err := grammargen.GenerateWithReport(Greenware())
	if err != nil {
		t.Fatal(err)
	}
	if report.StateCount > 3000 {
		t.Errorf("%d states, want at most 3000", report.StateCount)
	}
}
