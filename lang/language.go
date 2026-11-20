// Package lang parses and formats Greenware source files (.gw).
//
// Parse returns a source-backed concrete syntax tree: every node has a
// half-open byte span, comments are nodes, and Tree.Source returns the input
// byte for byte. Syntax errors are diagnostics with spans, and the tree is
// still returned so tools can work on a file that is half written. Format
// rewrites a valid file in the canonical style.
//
// The parser is a gotreesitter blob, generated from the Go DSL in
// language/grammar by cmd/greenware-grammar. The grammar is generic inside bodies:
// it accepts settings, rows, sampled rows, blocks, "when", "let", "part" and
// waivers. Which fields a kind accepts is for the later lowering step to
// check, not for the parser.
package lang

import (
	_ "embed"
	"sync"

	gts "github.com/odvcencio/gotreesitter"
)

//go:generate go run ../cmd/greenware-grammar -q -bin greenware.bin -ebnf ../docs/reference/grammar.ebnf

// greenware.bin is generated from the grammargen DSL in language/grammar.
//
//go:embed greenware.bin
var grammarBlob []byte

var (
	loadOnce sync.Once
	language *gts.Language
	pool     *gts.ParserPool
	loadErr  error
)

// load decodes the embedded blob once. A decode failure means the binary was
// built with a bad blob, which the drift test in cmd/greenware-grammar catches.
func load() (*gts.Language, *gts.ParserPool, error) {
	loadOnce.Do(func() {
		language, loadErr = gts.LoadLanguage(grammarBlob)
		if loadErr == nil {
			pool = gts.NewParserPool(language)
		}
	})
	return language, pool, loadErr
}
