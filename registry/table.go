package registry

import (
	"sort"

	"m31labs.dev/greenware/units"
)

// This file is the whole registry for the subset the first compiler reads.
// Add a kind or a field here and lowering, evaluation and the reference
// follow. Defaults are written as values in canonical units.

func fp(x float64) *float64         { return &x }
func dv(v units.Value) *units.Value { return &v }

var (
	tLength   = Type{Kind: TLength}
	tAngle    = Type{Kind: TAngle}
	tNumber   = Type{Kind: TNumber}
	tInteger  = Type{Kind: TInteger}
	tSwitch   = Type{Kind: TSwitch}
	tColor    = Type{Kind: TColor}
	tString   = Type{Kind: TString}
	tAxis     = Type{Kind: TAxis}
	tLabel    = Type{Kind: TLabel}
	tPoint    = Type{Kind: TPoint}
	tExtent   = Type{Kind: TExtent}
	tRotation = Type{Kind: TRotation}
	tBytes    = Type{Kind: TBytes}
)

func enum(vs ...string) Type { return Type{Kind: TEnum, Enum: vs} }
func ref(ns Namespace) Type  { return Type{Kind: TRef, Ns: ns} }
func refs(ns Namespace) Type { return Type{Kind: TRefList, Ns: ns} }

func origin() *units.Value {
	return dv(units.Tuple(units.Len(0), units.Len(0), units.Len(0)))
}

func unsupported(name string, t Type, why string) Field {
	return Field{Name: name, Type: t, Unsupported: why}
}

// shapeFields are the fields every shape shares, followed by extra.
func shapeFields(extra ...Field) []Field {
	common := []Field{
		{Name: "at", Type: tPoint, Default: origin(), Doc: "Translation of the shape in its part's frame."},
		{Name: "rot", Type: tRotation, Default: dv(units.Tuple(units.Ang(0), units.Ang(0), units.Ang(0))), Doc: "Rotation, applied before the translation."},
		{Name: "material", Type: ref(NsMaterial), Doc: "Material; absent inherits from the part."},
		{Name: "uv", Type: enum("auto", "planar", "box", "cylindrical", "spherical"), Default: dv(units.Label("auto")), Doc: "UV projection."},
		unsupported("normals", tLabel, "normal rules"),
		unsupported("joint", tLabel, "skeletons"),
		unsupported("cell", tLabel, "atlas cells"),
	}
	return append(append([]Field(nil), extra...), common...)
}

func segments(def float64, least float64) Field {
	return Field{Name: "segments", Type: tInteger, Default: dv(units.Num(def)), Min: fp(least), Doc: "Divisions around the axis."}
}

var pointRow3 = []Column{{"x", tLength, false}, {"y", tLength, false}, {"z", tLength, false}}

var kinds = []*Kind{
	{
		Name: "asset", Context: CtxDocument, Doc: "The one asset a file describes.",
		Fields: []Field{
			{Name: "title", Type: tString, Doc: "Name for people."},
			{Name: "root", Type: ref(NsGeometry), Required: true, Doc: "The top-level part at the top of the hierarchy."},
			{Name: "length", Type: enum(units.LengthUnits...), Default: dv(units.Label("m")), Doc: "Unit of a bare literal in a length slot."},
			{Name: "angle", Type: enum(units.AngleUnits...), Default: dv(units.Label("deg")), Doc: "Unit of a bare literal in an angle slot."},
			{Name: "up", Type: tAxis, Default: dv(units.Axis("+y")), Doc: "The up axis of the authored asset."},
			{Name: "forward", Type: tAxis, Default: dv(units.Axis("+z")), Doc: "The forward axis; perpendicular to up."},
			unsupported("idle", tLabel, "clips"),
			{Name: "generator", Type: tString, Doc: "Workshop that made the file; a record field."},
			{Name: "mesh_name", Type: tString, Doc: "Display name of the root's mesh."},
			unsupported("wrappers", Type{Kind: TStringList}, "wrapper nodes"),
			unsupported("bounds", tPoint, "declared bounds"),
		},
		Names: NameRequired,
	},
	{
		Name: "meta", Context: CtxDocument, Doc: "Metadata about the file.",
		Fields: []Field{
			{Name: "author", Type: tString, Doc: "Rights holder."},
			{Name: "license", Type: tString, Doc: "SPDX licence identifier."},
			{Name: "source", Type: tString, Doc: "Provenance."},
			{Name: "intent", Type: tString, Doc: "What the asset is for."},
			{Name: "notes", Type: tString, Doc: "Free notes."},
		},
	},
	{
		Name: "param", Context: CtxDocument, Doc: "The body of a parameter declaration.",
		Fields: []Field{{Name: "doc", Type: tString, Doc: "Text for a person or tool."}},
	},
	{
		Name: "pbr", Context: CtxDocument, Doc: "A core metallic-roughness material.",
		Fields: []Field{
			{Name: "base", Type: tColor, Default: dv(units.Col(units.Color{R: 255, G: 255, B: 255, A: 255})), Runtime: true, Doc: "Base colour factor."},
			unsupported("base_texture", tLabel, "textures"),
			{Name: "roughness", Type: tNumber, Default: dv(units.Num(1)), Min: fp(0), Max: fp(1), Runtime: true, Doc: "0 is smoothest."},
			{Name: "metallic", Type: tNumber, Default: dv(units.Num(0)), Min: fp(0), Max: fp(1), Runtime: true, Doc: "Write the factor even when zero."},
			unsupported("normal_texture", tLabel, "textures"),
			unsupported("occlusion", tLabel, "textures"),
			{Name: "emissive", Type: tColor, Default: dv(units.Col(units.Color{A: 255})), Runtime: true, Doc: "Emitted colour."},
			{Name: "emissive_strength", Type: tNumber, Default: dv(units.Num(1)), Min: fp(0), Doc: "Multiplies the emissive colour."},
			{Name: "alpha", Type: tNumber, Default: dv(units.Num(1)), Min: fp(0), Max: fp(1), Runtime: true, Doc: "Opacity; blending needs `blend = on`."},
			{Name: "blend", Type: tSwitch, Default: dv(units.Switch(false)), Doc: "Alpha blending instead of opaque."},
			{Name: "double_sided", Type: tSwitch, Default: dv(units.Switch(false)), Doc: "Draw both windings."},
			{Name: "clearcoat", Type: tNumber, Default: dv(units.Num(0)), Min: fp(0), Max: fp(1), Doc: "Clear top layer factor."},
			{Name: "clearcoat_roughness", Type: tNumber, Default: dv(units.Num(0)), Min: fp(0), Max: fp(1), Doc: "Roughness of the top layer."},
			{Name: "filter", Type: enum("nearest", "linear", "trilinear"), Default: dv(units.Label("trilinear")), Doc: "Texture filtering."},
			{Name: "wrap", Type: enum("repeat", "clamp", "mirror"), Default: dv(units.Label("repeat")), Doc: "Texture wrapping."},
			{Name: "name", Type: tString, Doc: "Display name; defaults to the declaration name."},
		},
	},
	{
		Name: "selena", Context: CtxDocument, Doc: "A Selena material with a PBR fallback. Descriptor parameters are open: the first compiler cannot check them without the artifact.",
		Fields: []Field{
			{Name: "source", Type: tString, Required: true, Doc: "Project-relative .sel path."},
			{Name: "name", Type: tString, Required: true, Doc: "Material name in the artifact."},
			{Name: "fallback", Type: tLabel, Required: true, Doc: "Inline `pbr { ... }`, written as the glTF core material."},
		},
		Open: true,
	},
	{
		Name: "part", Context: CtxDocument, Doc: "A group of shapes, child parts and sockets. Top-level parts other than the root are templates for `instance`.",
		Fields: []Field{
			{Name: "at", Type: tPoint, Default: origin(), Doc: "Origin in the parent's frame."},
			{Name: "pivot", Type: tPoint, Default: origin(), Doc: "Point the rotation and scale keep fixed."},
			{Name: "rot", Type: tRotation, Default: dv(units.Tuple(units.Ang(0), units.Ang(0), units.Ang(0))), Doc: "Rotation."},
			{Name: "scale", Type: Type{Kind: TScale}, Default: dv(units.Tuple(units.Num(1), units.Num(1), units.Num(1))), Doc: "Positive scale factors."},
			{Name: "export", Type: tSwitch, Default: dv(units.Switch(false)), Doc: "Keep this part as its own node."},
			{Name: "material", Type: ref(NsMaterial), Doc: "Material the shapes inherit."},
			unsupported("normals", tLabel, "normal rules"),
			unsupported("cell", tLabel, "atlas cells"),
			unsupported("joint", tLabel, "skeletons"),
			unsupported("uv_shared", tSwitch, "UV overlap control"),
			unsupported("watertight", tSwitch, "watertight checks"),
			unsupported("instancing", tSwitch, "GPU instancing"),
		},
		Contains: true, Names: NameRequired, Ns: NsGeometry,
	},
	// Shapes.
	{
		Name: "cylinder", Context: CtxPart, IsShape: true, Doc: "A cylinder along an axis.",
		Fields: shapeFields(
			Field{Name: "radius", Type: tLength, Required: true, Positive: true, Doc: "Radius."},
			Field{Name: "height", Type: tLength, Required: true, Positive: true, Doc: "Height along the axis."},
			Field{Name: "axis", Type: tAxis, Default: dv(units.Axis("+y")), Doc: "Axis of the cylinder."},
			segments(16, 3),
			Field{Name: "caps", Type: tSwitch, Default: dv(units.Switch(true)), Doc: "Close the ends."},
		),
		Blocks: []string{"bevel"}, Names: NameOptional, Ns: NsGeometry,
	},
	{
		Name: "cone", Context: CtxPart, IsShape: true, Doc: "A cone or frustum along an axis.",
		Fields: shapeFields(
			Field{Name: "radius", Type: tLength, Required: true, Positive: true, Doc: "Radius at the -axis end."},
			Field{Name: "top_radius", Type: tLength, Default: dv(units.Len(0)), NonNegative: true, Doc: "Radius at the +axis end."},
			Field{Name: "height", Type: tLength, Required: true, Positive: true, Doc: "Height along the axis."},
			Field{Name: "axis", Type: tAxis, Default: dv(units.Axis("+y")), Doc: "Axis of the cone."},
			segments(16, 3),
			Field{Name: "caps", Type: tSwitch, Default: dv(units.Switch(true)), Doc: "Close the ends."},
		),
		Blocks: []string{"bevel"}, Names: NameOptional, Ns: NsGeometry,
	},
	{
		Name: "box", Context: CtxPart, IsShape: true, Doc: "A box.",
		Fields: shapeFields(
			Field{Name: "size", Type: tExtent, Required: true, Doc: "Full size along x, y and z."},
			segments(1, 1),
		),
		Blocks: []string{"bevel"}, Names: NameOptional, Ns: NsGeometry,
	},
	{
		Name: "sphere", Context: CtxPart, IsShape: true, Doc: "A sphere.",
		Fields: shapeFields(
			Field{Name: "radius", Type: tLength, Required: true, Positive: true, Doc: "Radius."},
			segments(16, 3),
			Field{Name: "rings", Type: tInteger, Min: fp(2), Derived: "half the segments, rounded up", Doc: "Latitude divisions."},
		),
		Names: NameOptional, Ns: NsGeometry,
	},
	{
		Name: "lathe", Context: CtxPart, IsShape: true, Doc: "A profile turned about an axis.",
		Fields: shapeFields(
			segments(24, 3),
			Field{Name: "closed", Type: tSwitch, Default: dv(units.Switch(false)), Doc: "Join the profile's ends."},
			Field{Name: "axis", Type: tAxis, Default: dv(units.Axis("+y")), Doc: "Axis of revolution."},
			Field{Name: "arc", Type: Type{Kind: TAngleRange}, Default: dv(units.Pair(units.Ang(0), units.Ang(units.TwoPi))), Doc: "Angular span, written lo..hi."},
		),
		Rows:  []RowSpec{{Name: "point", MinRows: 2, Columns: []Column{{"radius", tLength, true}, {"height", tLength, false}}}},
		Names: NameOptional, Ns: NsGeometry,
	},
	{
		Name: "sweep", Context: CtxPart, IsShape: true, Doc: "A profile swept along a path.",
		Fields: shapeFields(
			Field{Name: "profile", Type: Type{Kind: TProfile}, Required: true, Doc: "Closed 2D profile, such as circle(r, n)."},
			Field{Name: "twist", Type: tAngle, Default: dv(units.Ang(0)), Doc: "Total twist along the path."},
			Field{Name: "end_scale", Type: tNumber, Default: dv(units.Num(1)), Positive: true, Doc: "Scale of the profile at the end."},
			Field{Name: "caps", Type: enum("off", "both", "start", "end"), Default: dv(units.Label("off")), Doc: "Which ends are closed."},
			Field{Name: "frame", Type: enum("minimal", "tube"), Default: dv(units.Label("minimal")), Doc: "How the profile turns along the path."},
		),
		Blocks: []string{"path"}, Names: NameOptional, Ns: NsGeometry,
	},
	{
		Name: "ellipsoid", Context: CtxPart, IsShape: true, Doc: "An ellipsoid with its own radii. The end rings keep a small floor, so each end is a small capped polygon.",
		Fields: shapeFields(
			Field{Name: "centre", Type: tPoint, Default: origin(), Doc: "Centre of the ellipsoid; `at` then translates the result."},
			Field{Name: "radii", Type: tExtent, Required: true, Doc: "Half-extents along x, y and z; each must be positive."},
			segments(12, 3),
			Field{Name: "rings", Type: tInteger, Default: dv(units.Num(7)), Min: fp(2), Doc: "Latitude rows including both ends."},
		),
		Names: NameOptional, Ns: NsGeometry,
	},
	{
		Name: "loft", Context: CtxPart, IsShape: true, Doc: "Rings joined by bands. Each `ring` row gives the values of one ring; the default columns are (x, y, z, rx, rz) and the ring is an ellipse.",
		Fields: shapeFields(
			segments(14, 3),
			Field{Name: "caps", Type: enum("both", "bottom", "top", "off"), Default: dv(units.Label("both")), Doc: "Which end rings are closed. `off` is required on an open arc."},
			Field{Name: "columns", Type: Type{Kind: TNames}, Doc: "Names of the ring row's columns; the default is (x, y, z, rx, rz)."},
			Field{Name: "vertex", Type: Type{Kind: TExpr}, Doc: "The point at a ring and column, as (x, y, z), from the column names, the ring angle `a` and the whole numbers `j` (column) and `k` (ring)."},
			Field{Name: "arc", Type: Type{Kind: TAngleRange}, Default: dv(units.Pair(units.Ang(0), units.Ang(units.TwoPi))), Doc: "Angular span, written lo..hi. A partial span is an open sheet."},
		),
		Rows: []RowSpec{{Name: "ring", MinRows: 2, Dynamic: true,
			Columns: []Column{{"x", tLength, false}, {"y", tLength, false}, {"z", tLength, false}, {"rx", tLength, true}, {"rz", tLength, true}}}},
		Names: NameOptional, Ns: NsGeometry,
	},
	{
		Name: "tube", Context: CtxPart, IsShape: true, Doc: "A circle carried through the `point` rows, with its frame recomputed at every point. Open ends are not capped.",
		Fields: shapeFields(
			Field{Name: "radius", Type: tLength, Required: true, Positive: true, Doc: "Radius."},
			segments(7, 3),
			Field{Name: "closed", Type: tSwitch, Default: dv(units.Switch(false)), Doc: "Join the last point back to the first; do not repeat the first point."},
		),
		Rows:  []RowSpec{{Name: "point", MinRows: 2, Columns: pointRow3}},
		Names: NameOptional, Ns: NsGeometry,
	},
	// Blocks inside shapes.
	{
		Name: "bevel", Context: CtxShape, Doc: "Rounds selected edges of a box, cylinder or cone.",
		Fields: []Field{
			{Name: "width", Type: tLength, Required: true, Positive: true, Doc: "Setback on the adjacent faces."},
			{Name: "segments", Type: tInteger, Default: dv(units.Num(1)), Min: fp(1), Doc: "1 is a chamfer."},
			{Name: "edges", Type: enum("all", "caps", "top", "bottom"), Derived: "all for a box, caps for a cylinder or cone", Doc: "Which edges."},
		},
	},
	{
		Name: "path", Context: CtxShape, Doc: "The ordered points a sweep follows.",
		Rows: []RowSpec{{Name: "point", MinRows: 2, Columns: pointRow3}},
	},
	// Part items.
	{
		Name: "array", Context: CtxPart, Doc: "Repeats its contents.",
		Fields: []Field{
			{Name: "mode", Type: enum("linear", "radial", "grid"), Default: dv(units.Label("linear")), Doc: "How copies are placed."},
			{Name: "count", Type: Type{Kind: TCount}, Required: true, Doc: "Copies; three counts for a grid. The product is at most 4096."},
			{Name: "step", Type: tPoint, Default: origin(), Doc: "Displacement per copy (linear, grid)."},
			{Name: "axis", Type: tAxis, Default: dv(units.Axis("+y")), Doc: "Axis of a radial array."},
			{Name: "centre", Type: tPoint, Default: origin(), Doc: "Centre of a radial array."},
			{Name: "radius", Type: tLength, NonNegative: true, Doc: "Placement radius; required in radial mode."},
			{Name: "index", Type: tLabel, Doc: "Names the zero-based copy number inside the array."},
		},
		Contains: true, Names: NameOptional, Ns: NsGeometry,
	},
	{
		Name: "instance", Context: CtxPart, Doc: "Places a top-level part definition.",
		Fields: []Field{
			{Name: "of", Type: ref(NsGeometry), Required: true, Doc: "The top-level part to place."},
			{Name: "at", Type: tPoint, Default: origin(), Doc: "Origin of the placement."},
			{Name: "rot", Type: tRotation, Default: dv(units.Tuple(units.Ang(0), units.Ang(0), units.Ang(0))), Doc: "Rotation."},
			{Name: "scale", Type: Type{Kind: TScale}, Default: dv(units.Tuple(units.Num(1), units.Num(1), units.Num(1))), Doc: "Positive scale factors."},
		},
		Names: NameOptional, Ns: NsGeometry,
	},
	{
		Name: "mirror", Context: CtxPart, Doc: "Keeps its contents and adds one reflected copy. Declared names inside are pairs (left, right); `side` is 1 in the original.",
		Fields:   []Field{{Name: "plane", Type: enum("x", "y", "z"), Required: true, Doc: "Plane through the origin, perpendicular to this axis."}},
		Contains: true,
	},
	{
		Name: "socket", Context: CtxPart, Doc: "A named point other code reads or attaches to.",
		Fields: []Field{
			{Name: "kind", Type: tLabel, Required: true, Doc: "What the socket is for, such as anchor or ribbon."},
			{Name: "at", Type: tPoint, Default: origin(), Doc: "Position."},
			{Name: "rot", Type: tRotation, Default: dv(units.Tuple(units.Ang(0), units.Ang(0), units.Ang(0))), Doc: "Frame."},
		},
		Names: NameRequired, Ns: NsSocket,
	},
	// Levels of detail, collision, budget, outputs.
	{
		Name: "lod", Context: CtxDocument, Doc: "A level of detail. Level 0 is the source and is not declared.",
		Fields: []Field{
			{Name: "distance", Type: tLength, Required: true, Positive: true, Doc: "Switch distance from the asset origin."},
			unsupported("view", tLabel, "screen-error switching"),
			unsupported("screen_error", tLabel, "screen-error switching"),
			{Name: "max_error", Type: tLength, Required: true, Positive: true, Doc: "Tessellation error target."},
			{Name: "segments", Type: tNumber, Positive: true, Max: fp(1), Doc: "Overrides automatic segment choice; a share up to 100%."},
			{Name: "keep", Type: refs(NsGeometry), Doc: "Geometry that keeps its source detail."},
			{Name: "drop", Type: refs(NsGeometry), Doc: "Geometry removed at this level."},
			unsupported("simplify", tNumber, "mesh simplification"),
		},
	},
	{
		Name: "collision", Context: CtxDocument, Doc: "Game collision volumes, written once in model space.",
		Contains: true,
	},
	{
		Name: "capsule", Context: CtxCollision, Doc: "A capsule collider.",
		Fields: []Field{
			{Name: "from", Type: tPoint, Required: true, Doc: "One cap centre."},
			{Name: "to", Type: tPoint, Required: true, Doc: "The other cap centre."},
			{Name: "radius", Type: tLength, Required: true, Positive: true, Doc: "Radius."},
			unsupported("joint", tLabel, "skeletons"),
		},
		Names: NameRequired, Ns: NsCollider,
	},
	{
		Name: "box", Context: CtxCollision, Doc: "A box collider.",
		Fields: []Field{
			{Name: "size", Type: tExtent, Required: true, Doc: "Full size."},
			{Name: "at", Type: tPoint, Default: origin(), Doc: "Centre."},
			{Name: "rot", Type: tRotation, Default: dv(units.Tuple(units.Ang(0), units.Ang(0), units.Ang(0))), Doc: "Rotation."},
			unsupported("joint", tLabel, "skeletons"),
		},
		Names: NameRequired, Ns: NsCollider,
	},
	{
		Name: "sphere", Context: CtxCollision, Doc: "A sphere collider.",
		Fields: []Field{
			{Name: "at", Type: tPoint, Default: origin(), Doc: "Centre."},
			{Name: "radius", Type: tLength, Required: true, Positive: true, Doc: "Radius."},
			unsupported("joint", tLabel, "skeletons"),
		},
		Names: NameRequired, Ns: NsCollider,
	},
	{
		Name: "mirror", Context: CtxCollision, Doc: "Reflects the colliders it holds.",
		Fields:   []Field{{Name: "plane", Type: enum("x", "y", "z"), Required: true, Doc: "Plane through the origin, perpendicular to this axis."}},
		Contains: true,
	},
	{
		Name: "budget", Context: CtxDocument, Doc: "Inclusive limits checked against each output.",
		Fields: []Field{
			{Name: "triangles_stored", Type: tInteger, Min: fp(1), Doc: "Triangles stored in the file."},
			{Name: "triangles", Type: tInteger, Min: fp(1), Doc: "Triangles drawn in the default look."},
			{Name: "triangles_worst", Type: tInteger, Min: fp(1), Doc: "Most triangles drawn in any reachable look."},
			{Name: "draw_calls", Type: tInteger, Min: fp(1), Doc: "Draws in the default look."},
			{Name: "draw_calls_worst", Type: tInteger, Min: fp(1), Doc: "Most draws in any reachable look."},
			{Name: "vertices", Type: tInteger, Min: fp(1), Doc: "Written vertices."},
			{Name: "bones", Type: tInteger, Min: fp(1), Doc: "Joints."},
			{Name: "influences", Type: tInteger, Min: fp(1), Max: fp(4), Doc: "Most weights on one vertex."},
			{Name: "materials", Type: tInteger, Min: fp(1), Doc: "Distinct materials stored."},
			{Name: "texture_bytes", Type: tBytes, Positive: true, Doc: "Decoded texture bytes."},
			{Name: "file_size", Type: tBytes, Positive: true, Doc: "Bytes of each file."},
			{Name: "morphs", Type: tInteger, Min: fp(0), Doc: "Morph targets on a mesh."},
			{Name: "texels_per_m", Type: tNumber, Positive: true, Doc: "Texel density."},
		},
	},
	{
		Name: "output", Context: CtxDocument, Doc: "A file family built from the asset at one choice of parameters.",
		Fields: []Field{
			{Name: "file", Type: tString, Required: true, Doc: "Relative destination."},
			{Name: "lods", Type: enum("files", "msft_lod"), Default: dv(units.Label("files")), Doc: "How levels of detail are delivered."},
			{Name: "manifest", Type: tSwitch, Default: dv(units.Switch(false)), Doc: "Write the asset manifest entry."},
			{Name: "profile", Type: enum("source", "delivery"), Default: dv(units.Label("source")), Doc: "Encoding profile."},
			unsupported("variant", tLabel, "variants"),
			unsupported("for_each", tLabel, "tables"),
			unsupported("mounts", tLabel, "mounts"),
			unsupported("buffer", tString, "split glTF"),
			unsupported("image", tLabel, "image encodings"),
		},
		Blocks: []string{"set", "budget"}, Names: NameRequired, Ns: NsOutput,
	},
	{
		Name: "set", Context: CtxDocument, Doc: "Parameter values an output overrides: `name = value` for each parameter.",
		Open: true,
	},
	{
		Name: "accept", Context: CtxDocument, Doc: "An owner's acceptance tests. They run on decoded files, not here; this compiler keeps the expressions unevaluated.",
		Open: true, Names: NameRequired,
	},
}

// Kinds returns every kind in a fixed order.
func Kinds() []*Kind { return kinds }

// Lookup finds a kind by context and name.
func Lookup(ctx Context, name string) (*Kind, bool) {
	for _, k := range kinds {
		if k.Context == ctx && k.Name == name {
			return k, true
		}
	}
	return nil, false
}

// KindNames returns the kind names for a context, sorted.
func KindNames(ctx Context) []string {
	var out []string
	for _, k := range kinds {
		if k.Context == ctx {
			out = append(out, k.Name)
		}
	}
	sort.Strings(out)
	return out
}

// knownUnsupported lists block kinds the language defines that this compiler
// does not read, by context.
var knownUnsupported = map[Context][]string{
	CtxPart:      {"capsule", "torus", "extrude", "panel", "subdivide", "boolean", "plug", "mount", "skeleton", "skin", "joint"},
	CtxShape:     {"subdivide"},
	CtxCollision: {"hull", "trimesh"},
}

// IsKnownUnsupported reports whether name is a block kind of the language that
// this compiler does not read in the given context.
func IsKnownUnsupported(ctx Context, name string) bool {
	for _, n := range knownUnsupported[ctx] {
		if n == name {
			return true
		}
	}
	return false
}

// ParamType maps a parameter type name to a slot type.
func ParamType(name string) (Type, bool) {
	switch name {
	case "length":
		return tLength, true
	case "angle":
		return tAngle, true
	case "duration":
		return Type{Kind: TDuration}, true
	case "number", "ratio":
		return tNumber, true
	case "integer":
		return tInteger, true
	case "color":
		return tColor, true
	case "switch":
		return tSwitch, true
	case "enum":
		return Type{Kind: TEnum}, true
	}
	return Type{}, false
}

// ParamTypeNames lists the parameter type names.
var ParamTypeNames = []string{"angle", "color", "duration", "enum", "integer", "length", "number", "ratio", "switch"}
