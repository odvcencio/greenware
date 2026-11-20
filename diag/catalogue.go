package diag

// Codes of the diagnostics the front end reports. Emission sites use these
// constants; the catalogue tests reject a code that is not listed below.
const (
	CodeSyntax       = "GW-SYNTAX"
	CodeEdition      = "GW-EDITION"
	CodeUnknownName  = "GW-UNKNOWN-NAME"
	CodeUnknownField = "GW-UNKNOWN-FIELD"
	CodeDuplicate    = "GW-DUPLICATE"
	CodeType         = "GW-TYPE"
	CodeUnit         = "GW-UNIT"
	CodeParamRange   = "GW-PARAM-RANGE"
	CodeFieldRange   = "GW-FIELD-RANGE"
	CodeRequire      = "GW-REQUIRE"
	CodeUnsupported  = "GW-UNSUPPORTED"
	CodeBudget       = "GW-BUDGET"
	CodeMissing      = "GW-MISSING"
	CodeCycle        = "GW-CYCLE"
	CodeDomain       = "GW-DOMAIN"
	CodeRuntime      = "GW-RUNTIME"
	CodeMirrorName   = "GW-MIRROR-NAME"
	CodeAccept       = "GW-ACCEPT"
	CodeLimit        = "GW-LIMIT"
	CodeUVCell       = "GW-UV-CELL"
	CodeMatParam     = "GW-MAT-PARAM"
	CodeRigUnbound   = "GW-RIG-UNBOUND"
	CodeRigStructure = "GW-RIG-STRUCTURE"
	CodeClipKey      = "GW-CLIP-KEY"
	CodeRigLimit     = "GW-RIG-LIMIT"
	CodeRigBand      = "GW-RIG-BAND"
	CodeMount        = "GW-MOUNT"
	CodeTableRow     = "GW-TABLE-ROW"
	CodeRigGround    = "GW-RIG-GROUND"
	CodeRuntimePart  = "GW-RUNTIME-PARTIAL"
	CodeRigTorn      = "GW-RIG-TORN"
	CodePrint        = "GW-PRINT"
)

// Entry documents one code.
type Entry struct {
	Code     string
	Severity Severity
	// Doc says when the code is reported and what to do about it.
	Doc string
	// Fixture is a source file, relative to the repository root, that makes
	// the compiler report the code. The tests run every fixture.
	Fixture string
	// Fix says whether the code can carry an applicable edit.
	Fix bool
}

var catalogue = []Entry{
	{CodeSyntax, Error, "The source does not match the grammar. The span marks the first token the parser could not place, or the place where a token is missing.", "testdata/diag/GW-SYNTAX.gw", false},
	{CodeEdition, Error, "The file does not start with the edition header `greenware 1`, or names an edition this compiler does not read. The fix inserts or corrects the header.", "testdata/diag/GW-EDITION.gw", true},
	{CodeUnknownName, Error, "A name does not resolve in the namespace its slot asks for: a parameter, let, part, material, parameter type, function or label. The fix offers the nearest known name.", "testdata/diag/GW-UNKNOWN-NAME.gw", true},
	{CodeUnknownField, Error, "A field, row or block is not part of the kind that holds it. The fix offers the nearest field the kind accepts.", "testdata/diag/GW-UNKNOWN-FIELD.gw", true},
	{CodeDuplicate, Error, "A name or field is declared twice in one scope. The first declaration is the related span.", "testdata/diag/GW-DUPLICATE.gw", false},
	{CodeType, Error, "A value has the wrong type for its slot (for example a switch where a length is needed), or a declaration has the wrong shape. The unit of a number is a separate code, GW-UNIT.", "testdata/diag/GW-TYPE.gw", true},
	{CodeUnit, Error, "Two numbers have dimensions that cannot combine (a length plus an angle), a slot got a number of another dimension, or a trigonometric function got a number with no angle unit. For a bare angle argument the edits offer deg and rad as alternatives.", "testdata/diag/GW-UNIT.gw", true},
	{CodeParamRange, Error, "A parameter's default, or a value supplied for it, lies outside its declared `in lo..hi` range, or the range is empty. The fix offers the nearest bound.", "testdata/diag/GW-PARAM-RANGE.gw", true},
	{CodeFieldRange, Error, "A well-typed value is outside what its field allows: a zero radius, fewer than three segments, a roughness above 1, up and forward that are not perpendicular.", "testdata/diag/GW-FIELD-RANGE.gw", false},
	{CodeRequire, Error, "A `require` row of `check params` is false at the evaluated parameter values. The message lists the values the condition reads.", "testdata/diag/GW-REQUIRE.gw", false},
	{CodeUnsupported, Error, "The construct is valid in the language but outside the subset this compiler reads. Nothing is substituted for it.", "testdata/diag/GW-UNSUPPORTED.gw", false},
	{CodeBudget, Error, "A budget limit that the source alone decides is broken: more distinct materials or draws than the limit allows, or a worst-case limit below its default-look limit.", "testdata/diag/GW-BUDGET.gw", false},
	{CodeMissing, Error, "A required field, name or block is absent.", "testdata/diag/GW-MISSING.gw", false},
	{CodeCycle, Error, "Names depend on each other in a closed chain: lets and parameters that read each other, parts that instance each other, or templates that call each other. The related spans give the chain.", "testdata/diag/GW-CYCLE.gw", false},
	{CodeDomain, Error, "An operation is undefined for its operands: division by zero, the square root of a negative number, an index out of range, a non-finite result.", "testdata/diag/GW-DOMAIN.gw", false},
	{CodeRuntime, Error, "A value that depends on a runtime parameter reaches a slot that must be known when the file compiles: a length, a count, a require, a range bound.", "testdata/diag/GW-RUNTIME.gw", false},
	{CodeMirrorName, Error, "A named declaration inside a mirror is not a pair `(left, right)`, or a pair is written outside a mirror.", "testdata/diag/GW-MIRROR-NAME.gw", false},
	{CodeAccept, Error, "An `expect` of an acceptance file failed or was refused on a decoded GLB at one output and level. The message names the expect, the output, the level and the measured values. An expect that cannot be measured (for example `outline`, which needs a rendered image) is reported with the same code as a warning and does not fail the run. Only `greenware accept` reports this code; the front end never does.", "testdata/diag/GW-ACCEPT.gw", false},
	{CodeLimit, Error, "A build needs more logical work than the `total_work` ceiling leaves, or an operand is larger than the class its cost ticket admits (an array of more than 4096 copies, more than 4096 expanded items in an output, a level of more than 2048 triangles, a file over 96 KiB). The message names the ticket and, for a ceiling, the work it requested and the work that remained. The build stops before the ticket's operation and writes nothing; an earlier build record stays as it was. Raise `--max-work` only for a class the schedule prices; a larger operand needs a new schedule version, not a larger ceiling.", "testdata/diag/GW-LIMIT.gw", false},
	{CodeUVCell, Error, "A shape's UVs cannot address the atlas cell it names: two atlases sampled by one material give the cell different rectangles, or the shape's own UVs leave the unit square so that the cell's inset rectangle cannot hold them. Use an aligned chart or another projection; no triangle is added to make the check pass.", "testdata/diag/GW-UV-CELL.gw", false},
	{CodeMatParam, Error, "A material parameter does not fit its use: a palette mask used directly as a base texture (use paint(mask)), `paint(atlas)` of a palette atlas where the containing Selena material has no colour for a slot the atlas uses, or a texture argument that is not a texture.", "testdata/diag/GW-MAT-PARAM.gw", false},
	{CodeRigUnbound, Error, "A rendered shape of a skinned asset has no joint. Bind it with `joint = name` on the shape or on a part that holds it; every shape starts rigid on one joint with weight 1.", "testdata/diag/GW-RIG-UNBOUND.gw", false},
	{CodeRigStructure, Error, "The skeleton is malformed: no root or more than one root, a `joint_order` that does not list every joint exactly once, a joint a skin step cannot use (a `lengthen` joint that has not exactly one child, a `blend` child that is not the immediate child of its parent), or an unusable skin: a zero blend axis, a weight sum that is not 1, more influences than `max_influences`.", "testdata/diag/GW-RIG-STRUCTURE.gw", false},
	{CodeClipKey, Error, "A clip's keys cannot be written: a key time that is negative, repeated or reversed, a key later than the clip's duration, a `key` row mixing translations and rotations, or times that float32 rounding makes equal. Nothing is sorted, merged or repaired.", "testdata/diag/GW-CLIP-KEY.gw", false},
	{CodeRigLimit, Error, "A pose of a clip leaves a joint's hard limit (a `limit` block), or the test is undefined at the pose (a pure 180 degree swing). The compiler measures swing and twist at every key and never clamps a pose.", "testdata/diag/GW-RIG-LIMIT.gw", false},
	{CodeRigBand, Warning, "A pose of a clip leaves a joint's natural band (the ranges written on the joint). It is a warning: the pose is written as authored.", "testdata/diag/GW-RIG-BAND.gw", false},
	{CodeMount, Error, "A mount cannot be made: the host has no socket of that name, the gear asset has no plug of the socket's kind (or none named like the socket among several), the mount's `set` selects no single gear output or more than one, or the mounted gear penetrates the host or comes closer than the `fit` clearance. The message names the mount, the gear output and, for a fit failure, the clip, key, runtime point and the measured and required clearance.", "testdata/diag/GW-MOUNT.gw", false},
	{CodeTableRow, Error, "A row of a table cannot be decoded against its column types: a missing, repeated or undeclared key, a null, array or nested value, a value of the wrong JSON type, an unsafe `{row.key}` file component, or a column with no type. The message names the table, the zero-based row and the column.", "testdata/diag/GW-TABLE-ROW.gw", false},
	{CodeRigGround, Error, "The lowest skinned vertex of a non-airborne clip key lies outside the `ground` band of `check rig` (default -5 mm to 20 mm) at some runtime point. The message names the output, level, clip, key time, runtime point and the measured height.", "testdata/diag/GW-RIG-GROUND.gw", false},
	{CodeRuntimePart, Warning, "The runtime points `check runtime` asks for are more than the `runtime_points` limit allows, so the audit ran the default, every named case and one axis at a time. The message lists the omitted combinations.", "testdata/diag/GW-RUNTIME-PARTIAL.gw", false},
	{CodeRigTorn, Error, "More than the `torn` share of the geometric edges of the posed skin are longer than three times or shorter than a quarter of their bind length at some key. The message names the output, level, clip, key time, runtime point and the share.", "testdata/diag/GW-RIG-TORN.gw", false},
	{CodePrint, Error, "An output cannot be written as a print file. A solid of the output is not a closed manifold shell (an open edge, an edge shared by three triangles or more, a degenerate triangle) or its triangles wind inward (non-positive volume), or the output cannot be printed at all (a rigged asset, a mount, no visible shape, a bad scale). The message names the shape, the fault and the first example in millimetres. Only `BuildPrint` reports this code; a GLB of the same source still builds. Touching or overlapping solids are not refused: they print as separate bodies and the product notes list them.", "testdata/diag/GW-PRINT.gw", false},
}

// Catalogue returns every code in a fixed order. The caller must not change
// the entries.
func Catalogue() []Entry { return catalogue }

// Lookup returns the entry for a code.
func Lookup(code string) (Entry, bool) {
	for _, e := range catalogue {
		if e.Code == code {
			return e, true
		}
	}
	return Entry{}, false
}
