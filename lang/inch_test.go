package lang

import (
	"strings"
	"testing"
)

func parseDiags(t *testing.T, src string) []Diagnostic {
	t.Helper()
	return Parse([]byte(src)).Diagnostics()
}

func TestInchParsesWhenItTouchesItsNumber(t *testing.T) {
	for name, src := range map[string]string{
		"param with range": "greenware 1\nasset a { root = a }\nparam p : length = 4in in 2in..6in\npart a { }\n",
		"field":            "greenware 1\nasset a { root = a }\npart a { box { size = (4in, 18.5in, 6in) } }\n",
		"let":              "greenware 1\nasset a { root = a }\nlet x = 4in\npart a { }\n",
		"plain range":      "greenware 1\nasset a { root = a }\nparam p : length = 4m in 2m..5m\npart a { }\n",
		"spaced range":     "greenware 1\nasset a { root = a }\nparam p : length = 4 in 2m..5m\npart a { }\n",
	} {
		for _, d := range parseDiags(t, src) {
			t.Errorf("%s: %s", name, d.Message)
		}
	}
}

func TestSpacedInchSaysWhatToWrite(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"greenware 1\nasset a { root = a }\nparam w : length = 4 in\npart a { }\n", "write the inch unit without a space: 4in"},
		{"greenware 1\nasset a { root = a }\npart a { box { size = (4 in, 1, 1) } }\n", "write the inch unit without a space: 4in"},
		{"greenware 1\nasset a { root = a }\npart a { cylinder { radius = 18.5 in height = 1 } }\n", "write the inch unit without a space: 18.5in"},
	} {
		ds := parseDiags(t, c.src)
		if len(ds) == 0 {
			t.Errorf("no diagnostic for %q", c.src)
			continue
		}
		if !strings.Contains(ds[0].Message, c.want) {
			t.Errorf("message %q does not say %q", ds[0].Message, c.want)
		}
		if got := c.src[ds[0].Span.Start:ds[0].Span.End]; got != "in" {
			t.Errorf("the diagnostic points at %q, want the word in", got)
		}
	}
}

func TestOtherErrorsKeepTheirMessage(t *testing.T) {
	// A missing brace, and an `in` that follows a name, are not inch errors.
	for _, src := range []string{
		"greenware 1\nasset a {\n  root = a\n\npart a { }\n",
		"greenware 1\nasset a { root = a }\npart a { box { size = (w in, 1, 1) } }\n",
	} {
		for _, d := range parseDiags(t, src) {
			if strings.Contains(d.Message, "inch") {
				t.Errorf("unexpected inch hint on %q: %s", src, d.Message)
			}
		}
	}
}
