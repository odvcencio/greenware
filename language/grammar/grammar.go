// Package grammar defines the Greenware grammar in the grammargen Go DSL. It is
// the source of lang/greenware.bin and docs/reference/grammar.ebnf: run
// `go generate ./lang` after a change.
//
// Only the generator and tests import this package. The parser loads the
// generated blob, so programs that parse Greenware do not link grammargen.
package grammar

import (
	"strings"

	"github.com/odvcencio/gotreesitter/grammargen"

	"m31labs.dev/greenware/language"
)

// The DSL reads like tree-sitter's grammar.js.
var (
	seq       = grammargen.Seq
	choice    = grammargen.Choice
	repeat    = grammargen.Repeat
	optional  = grammargen.Optional
	str       = grammargen.Str
	sym       = grammargen.Sym
	pat       = grammargen.Pat
	field     = grammargen.Field
	token     = grammargen.Token
	prec      = grammargen.Prec
	precLeft  = grammargen.PrecLeft
	precRight = grammargen.PrecRight
)

// commaList is a comma-separated list of one or more items.
func commaList(item *grammargen.Rule) *grammargen.Rule {
	return seq(item, repeat(seq(str(","), item)))
}

// Greenware returns the grammar for Greenware edition 1 documents. Rule order fixes
// symbol ids, so reordering rules changes the generated blob.
func Greenware() *grammargen.Grammar {
	g := grammargen.NewGrammar(language.Name)

	g.Define("source_file", seq(optional(sym("edition_decl")), repeat(sym("_declaration"))))
	g.Define("edition_decl", seq(str(language.HeaderWord), field("edition", sym("integer"))))
	g.Define("_declaration", choice(
		sym("import_decl"), sym("asset_decl"), sym("param_decl"), sym("let_decl"), sym("table_decl"),
		sym("material_decl"), sym("texture_decl"), sym("mesh_decl"), sym("view_decl"), sym("painter_decl"),
		sym("template_decl"), sym("part_decl"), sym("plug_decl"), sym("mount_decl"), sym("skeleton_decl"),
		sym("skin_decl"), sym("morph_decl"), sym("clip_decl"), sym("lod_decl"), sym("collision_decl"),
		sym("budget_decl"), sym("variant_decl"), sym("output_decl"), sym("meta_decl"), sym("check_decl"),
		sym("accept_decl"),
	))

	// Documents and libraries.
	g.Define("import_decl", seq(str("import"), field("path", sym("string")), optional(seq(str("as"), field("alias", sym("identifier"))))))
	g.Define("asset_decl", seq(str("asset"), field("name", sym("identifier")), field("body", sym("body"))))
	g.Define("meta_decl", seq(str("meta"), field("body", sym("body"))))

	// Parameters and values.
	g.Define("param_decl", seq(
		str("param"), field("name", sym("identifier")), str(":"), field("type", sym("param_type")),
		str("="), field("default", sym("_expression")),
		optional(seq(str("in"), field("range", sym("range")))),
		optional(field("modifier", str("runtime"))),
		optional(field("body", sym("body"))),
	))
	g.Define("param_type", seq(
		field("name", sym("identifier")),
		optional(seq(str("("), field("argument", commaList(sym("identifier"))), str(")"))),
	))
	g.Define("let_decl", seq(str("let"), field("name", sym("identifier")), str("="), field("value", sym("_expression"))))
	g.Define("table_decl", seq(str("table"), field("name", sym("identifier")), str("="), field("row_kind", sym("identifier")), field("body", sym("body"))))

	// Looks and inputs.
	g.Define("material_decl", seq(str("material"), field("name", sym("identifier")), str("="), field("value", sym("source_value"))))
	g.Define("texture_decl", seq(str("texture"), field("name", sym("identifier")), str("="), field("value", sym("source_value"))))
	g.Define("mesh_decl", seq(str("mesh"), field("name", sym("identifier")), str("="), field("value", sym("source_value"))))
	g.Define("source_value", seq(field("kind", sym("identifier")), field("body", sym("body"))))
	g.Define("painter_decl", seq(
		str("painter"), field("name", sym("identifier")),
		str("("), optional(field("parameters", commaList(sym("typed_param")))), str(")"),
		field("body", sym("body")),
	))
	g.Define("typed_param", seq(
		field("name", sym("identifier")), str(":"), field("type", sym("param_type")),
		optional(seq(str("="), field("default", sym("_expression")))),
	))

	// Shape, rig and motion.
	g.Define("part_decl", seq(str("part"), field("name", sym("_declared_name")), field("body", sym("body"))))
	g.Define("template_decl", seq(
		str("template"), field("name", sym("identifier")),
		str("("), optional(field("parameters", commaList(sym("typed_param")))), str(")"),
		field("body", sym("body")),
	))
	g.Define("plug_decl", seq(str("plug"), field("name", sym("identifier")), field("body", sym("body"))))
	g.Define("mount_decl", seq(str("mount"), field("name", sym("identifier")), field("body", sym("body"))))
	g.Define("skeleton_decl", seq(str("skeleton"), optional(field("name", sym("identifier"))), field("body", sym("body"))))
	g.Define("skin_decl", seq(str("skin"), field("body", sym("body"))))
	g.Define("morph_decl", seq(str("morph"), field("name", sym("identifier")), field("body", sym("body"))))
	g.Define("clip_decl", seq(str("clip"), field("name", sym("identifier")), field("body", sym("body"))))

	// Delivery, checks and acceptance.
	g.Define("lod_decl", seq(str("lod"), field("level", sym("integer")), field("body", sym("body"))))
	g.Define("collision_decl", seq(str("collision"), field("body", sym("body"))))
	g.Define("budget_decl", seq(str("budget"), field("body", sym("body"))))
	g.Define("variant_decl", seq(str("variant"), field("name", sym("identifier")), field("body", sym("body"))))
	g.Define("output_decl", seq(str("output"), field("name", sym("identifier")), field("body", sym("body"))))
	g.Define("view_decl", seq(str("view"), field("name", sym("identifier")), field("body", sym("body"))))
	g.Define("check_decl", seq(str("check"), field("name", sym("identifier")), field("body", sym("body"))))
	g.Define("accept_decl", seq(str("accept"), field("name", sym("identifier")), field("body", sym("body"))))

	// Bodies: the generic part of the language.
	g.Define("body", seq(str("{"), repeat(sym("_item")), str("}")))
	g.Define("_item", choice(
		sym("setting"), sym("row"), sym("sample_row"), sym("block"), sym("when_block"),
		sym("let_decl"), sym("part_decl"), sym("waiver"),
	))
	g.Define("setting", seq(field("name", sym("_name")), str("="), field("value", sym("_value"))))
	g.Define("_value", choice(sym("_expression"), sym("range"), sym("source_value")))
	g.Define("row", seq(field("name", sym("_name")), str("("), field("arguments", commaList(sym("argument"))), str(")")))
	g.Define("sample_row", seq(
		str("sample"), field("variable", sym("identifier")), str("in"), field("range", sym("range")),
		str("by"), field("count", sym("_expression")), str("->"), field("row", sym("row")),
	))
	g.Define("block", seq(field("kind", sym("identifier")), optional(field("name", sym("_declared_name"))), field("body", sym("body"))))
	g.Define("_declared_name", choice(sym("identifier"), sym("name_pair")))
	g.Define("name_pair", seq(str("("), field("left", sym("identifier")), str(","), field("right", sym("identifier")), str(")")))
	g.Define("when_block", seq(
		str("when"), field("condition", sym("_expression")), field("body", sym("body")),
		optional(seq(str("else"), field("alternative", choice(sym("body"), sym("when_block"))))),
	))
	g.Define("waiver", seq(str("waive"), field("code", sym("diagnostic_code")), field("reason", sym("string"))))
	g.Define("range", seq(field("start", sym("_expression")), str(".."), field("end", sym("_expression"))))

	// Expressions. Larger precedence numbers bind more tightly. Each operator is
	// its own production, so the shift of an operator token carries the
	// precedence the conflict resolver compares. Comparison has no
	// associativity, so "a < b < c" is a syntax error.
	g.Define("_expression", choice(
		sym("binary_expr"), sym("unary_expr"), sym("if_expr"), sym("call_expr"),
		sym("index_expr"), sym("member_expr"), sym("_primary"),
	))
	var binaries []*grammargen.Rule
	for _, lv := range []struct {
		assoc func(int, *grammargen.Rule) *grammargen.Rule
		n     int
		ops   []string
	}{
		{precLeft, 2, []string{"||"}},
		{precLeft, 3, []string{"&&"}},
		{prec, 4, []string{"==", "!=", "<", "<=", ">", ">="}},
		{precLeft, 5, []string{"+", "-"}},
		{precLeft, 6, []string{"*", "/"}},
	} {
		for _, op := range lv.ops {
			binaries = append(binaries, lv.assoc(lv.n, seq(field("left", sym("_expression")), field("operator", str(op)), field("right", sym("_expression")))))
		}
	}
	g.Define("binary_expr", choice(binaries...))
	g.Define("unary_expr", choice(
		prec(7, seq(field("operator", str("-")), field("operand", sym("_expression")))),
		prec(7, seq(field("operator", str("+")), field("operand", sym("_expression")))),
		prec(7, seq(field("operator", str("!")), field("operand", sym("_expression")))),
	))
	g.Define("if_expr", precRight(1, seq(
		str("if"), field("condition", sym("_expression")), str("then"), field("consequence", sym("_expression")),
		str("else"), field("alternative", sym("_expression")),
	)))
	g.Define("call_expr", prec(8, seq(
		field("function", sym("_name")), str("("), optional(field("arguments", commaList(sym("argument")))), str(")"),
	)))
	g.Define("index_expr", prec(8, seq(field("object", sym("_expression")), str("["), field("index", sym("_expression")), str("]"))))
	g.Define("member_expr", precLeft(8, seq(field("object", sym("_expression")), str("."), field("member", sym("identifier")))))
	g.Define("argument", seq(
		optional(seq(field("name", sym("identifier")), str("="))),
		field("value", choice(sym("_expression"), sym("range"))),
	))
	g.Define("_primary", choice(
		sym("number"), sym("color"), sym("string"), sym("boolean"), sym("_name"),
		sym("tuple"), sym("list"), sym("paren_expr"),
	))
	g.Define("paren_expr", seq(str("("), field("value", sym("_expression")), str(")")))
	g.Define("tuple", seq(str("("), sym("_expression"), str(","), sym("_expression"), repeat(seq(str(","), sym("_expression"))), str(")")))
	g.Define("list", seq(str("["), optional(seq(sym("_expression"), repeat(seq(str(","), sym("_expression"))), optional(str(",")))), str("]")))
	g.Define("boolean", choice(str("on"), str("off")))

	// Tokens. A qualified name is one token: a dotted run of identifiers with
	// no space, so "finish.gloss" never competes with member access.
	g.Define("_name", choice(sym("identifier"), sym("qualified_name")))
	g.Define("qualified_name", token(pat(`[a-z_][a-z0-9_]*(\.[a-z_][a-z0-9_]*)+`)))
	g.Define("number", token(pat(`[0-9]+(\.[0-9]+)?(mm|cm|km|m|yd|ft|in|deg|rad|ms|s|px|%|KiB|MiB|GiB|B)?`)))
	g.Define("integer", token(pat(`[0-9]+`)))
	g.Define("color", token(pat(`#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?`)))
	g.Define("string", token(pat(`"([^"\\]|\\.)*"`)))
	g.Define("identifier", pat(`[a-z_][a-z0-9_]*`))
	g.Define("diagnostic_code", token(pat(language.DiagnosticPrefix+`[A-Z0-9]+(-[A-Z0-9]+)*`)))
	g.Define("comment", token(pat(`//[^\n]*`)))

	g.SetExtras(pat(`[ \t\r\n]+`), sym("comment"))
	g.SetWord("identifier")

	// A row and a pair-named block share a prefix, "joint (a, b)": the "{" after
	// the ")" decides. The parser keeps both readings until then.
	g.SetConflicts([]string{"_name", "name_pair"})

	// Without this, grammargen copies the alternatives of a hidden choice rule
	// into every rule that uses it. "_expression" appears three times in "tuple",
	// so that rule alone grew to 26,000 productions and the parser to 28,000
	// states. Each hidden choice stays a rule of its own.
	for _, name := range g.RuleOrder {
		if strings.HasPrefix(name, "_") && g.Rules[name].Kind == grammargen.RuleChoice {
			g.PreserveHiddenChoicePassthrough = append(g.PreserveHiddenChoicePassthrough, name)
		}
	}

	return g
}

// Blob compiles the grammar to the serialized parser that lang embeds. The
// output depends only on the grammar, so two calls return the same bytes.
func Blob() ([]byte, error) {
	return grammargen.Generate(Greenware())
}
