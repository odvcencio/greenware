// Package depcheck checks the module boundary: the Facet core must not
// import GoSX, Kiln or cgo, directly or through any dependency.
package depcheck

import "strings"

// ForbiddenPrefixes are import-path prefixes the core closure must not contain.
var ForbiddenPrefixes = []string{
	"github.com/odvcencio/gosx",
	"github.com/odvcencio/kiln",
	"m31labs.dev/kiln",
	"m31labs.dev/facet/gosx",
	// The all-language grammar registry is generator-only.
	"github.com/odvcencio/gotreesitter/grammars",
}

// Violations returns every path in deps that breaks the boundary, in input order.
func Violations(deps []string) []string {
	var out []string
	for _, d := range deps {
		if d == "C" {
			out = append(out, d)
			continue
		}
		for _, p := range ForbiddenPrefixes {
			if d == p || strings.HasPrefix(d, p+"/") {
				out = append(out, d)
				break
			}
		}
	}
	return out
}
