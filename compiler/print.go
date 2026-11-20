package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/backend/threemf"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/resolve"
)

// The print backend writes one level of one output as a 3MF file of solids.
//
// What a print file holds. Each shell of each shape is its own object, in the
// order the walk meets them, with its own vertices. Shapes that touch or
// overlap are never merged: the specification (compiler.md 8.3) says a
// renderable closed shell is not necessarily a printable assembly, and it
// defines no rule that merges shells, so there is none here. The painted
// frame of the goal post is therefore several bodies, and the product notes
// list the pairs whose bounding boxes meet.
//
// Frame and units. The assets are in metres with +y up. A 3MF is in
// millimetres with +z up. The conversion happens once, here, when a vertex
// leaves the evaluated mesh: (x, y, z) metres become (x, -z, y) times
// 1000 times Scale millimetres (a rotation of 90 degrees about x, which keeps
// the handedness and the winding). Nothing is moved to the origin: the ground
// stays at z = 0 for an asset that stands on y = 0.
//
// Refusal. BuildPrint writes nothing and reports GW-PRINT when an object is
// not a closed manifold shell with outward winding, or when the output cannot
// be printed at all. The check runs on the decoded bytes of the 3MF, after the
// 6-decimal rounding of the coordinates, which is what a slicer reads.

// PrintOptions are the parameters of BuildPrintWith.
type PrintOptions struct {
	// Scale multiplies every dimension. Zero means 1. A 12 m goal post needs a
	// scale below 1 to fit a printer.
	Scale float64
}

// PrintObject describes one object of the 3MF.
type PrintObject struct {
	ID       int
	Name     string // for example goalpost/crossbar
	Material string // the render material of the shape; not written to the file
	Vertices int
	// Triangles is the triangle count.
	Triangles int
	// Closed is true when the object is a closed manifold shell. BuildPrint
	// returns a product only when every object is closed, so it is true there.
	Closed bool
	// VolumeMM3 is the enclosed volume in cubic millimetres, as read back from
	// the file.
	VolumeMM3 float64
	// Lo and Hi are the corners of the object's bounds in millimetres, as read
	// back from the file.
	Lo, Hi [3]float64
	// Span is the declaration the object comes from.
	Span diag.Span
}

// PrintProduct is the result of BuildPrint.
type PrintProduct struct {
	Output string  // the output name
	Level  int     // the level of detail printed
	Scale  float64 // the scale applied to the dimensions
	Name   string  // the file name, for example goalpost.3mf
	Bytes  []byte  // the 3MF package
	SHA256 string  // hex SHA-256 of Bytes
	Model  *threemf.Model
	// Objects lists the objects in file order.
	Objects []PrintObject
	// Lo and Hi are the corners of the bounds of every object, in millimetres.
	Lo, Hi    [3]float64
	Triangles int // total over objects
	// Notes lists what the source or the specification asks for and the print
	// file does not carry or check. None of it stops the build.
	Notes []string
}

// BuildPrint is BuildPrintWith at scale 1.
func BuildPrint(file *ast.File, outputName string, level int) (*PrintProduct, []diag.Diagnostic) {
	return BuildPrintWith(file, outputName, level, PrintOptions{})
}

type printMeta struct {
	shape string
	chain []string
	mat   string
}

// BuildPrintWith evaluates one output at one level, in the default look, and
// encodes its solids as a 3MF. outputName must name exactly one output. It
// returns a nil product when any diagnostic is an error. It does not reserve
// cost tickets: the level is bounded by the same triangle class as its GLB,
// which Build checks.
func BuildPrintWith(file *ast.File, outputName string, level int, opts PrintOptions) (*PrintProduct, []diag.Diagnostic) {
	if file == nil || file.Broken {
		return nil, []diag.Diagnostic{diag.New(diag.CodeSyntax, diag.Span{}, "the file has syntax errors; nothing was built")}
	}
	scale := opts.Scale
	if scale == 0 {
		scale = 1
	}
	if math.IsNaN(scale) || math.IsInf(scale, 0) || scale <= 0 || scale > 1e6 {
		return nil, []diag.Diagnostic{diag.New(diag.CodePrint, diag.Span{}, "the print scale %v is not a positive number up to 1e6", scale)}
	}
	refs, tds := resolve.Outputs(file)
	if diag.HasErrors(tds) {
		return nil, tds
	}
	var names []string
	for _, o := range refs {
		if o.Name == outputName || o.Base.Name.Name == outputName {
			names = append(names, o.Name)
		}
	}
	if outputName == "" || len(names) == 0 {
		return nil, []diag.Diagnostic{diag.New(diag.CodeUnknownName, diag.Span{}, "the file has no output named %q", outputName)}
	}
	if len(names) > 1 {
		return nil, []diag.Diagnostic{diag.New(diag.CodeUnsupported, outputSpan(file, names[0]), "%q names %d outputs; a print file is built from one", outputName, len(names))}
	}
	oname := names[0]
	r, ds := resolve.EvaluateOutput(file, oname)
	if diag.HasErrors(ds) || r == nil {
		return nil, ds
	}
	osp := outputSpan(file, oname)
	refuse := func(sp diag.Span, format string, args ...any) (*PrintProduct, []diag.Diagnostic) {
		return nil, diag.Dedup(append(ds, diag.New(diag.CodePrint, sp, format, args...)))
	}

	e := &elab{file: file, r: r, defs: map[string]*resolve.Node{}, oname: oname}
	var out resolve.Output
	for _, o := range r.Outputs {
		if o.Name == oname {
			out = o
		}
	}
	stem, _, ok := splitFile(out.File)
	if !ok {
		e.report(diag.CodeUnsupported, osp, "the output file %q must be a plain name ending in .glb", out.File)
		return nil, e.diags
	}
	if r.Root == nil {
		e.report(diag.CodeMissing, osp, "the asset has no root part to build")
		return nil, e.diags
	}
	if e.rigged() {
		return refuse(osp, "output %s is a rigged asset; a print file holds static solids and no skin or pose is printed", oname)
	}
	if len(r.Mounts) > 0 {
		return refuse(osp, "output %s has mounts; the print backend does not build mounted gear", oname)
	}
	upright := true // +y up: rotate to +z up
	if v, ok := fieldOf(r.Asset, "up"); ok && v.Valid() {
		switch v.Str {
		case "y", "+y":
		case "z", "+z":
			upright = false
		default:
			return refuse(osp, "the asset's up axis %q is not +y or +z; the print backend maps only those to the 3MF's +z", v.Str)
		}
	}
	for _, d := range r.Definitions {
		e.defs[d.Name] = d
	}
	specs := e.levelSpecs()
	if level < 0 || level > len(specs) {
		return nil, append(ds, diag.New(diag.CodeMissing, osp, "output %s has levels 0 to %d, not %d", oname, len(specs), level))
	}
	if e.failed() {
		return nil, e.diags
	}
	if _, ok := e.bakeLook(); !ok || e.failed() {
		return nil, diag.Dedup(e.diags)
	}

	metas := map[*mesh.Mesh]printMeta{} // lookup only
	e.printHook = func(c wctx, mat string, m *mesh.Mesh) {
		metas[m] = printMeta{shape: c.shape, chain: c.chain, mat: mat}
	}
	dropped := map[string]bool{}
	var root *snode
	for lv := 0; lv <= level; lv++ {
		e.maxErr, e.lodErr = 0, 0
		if lv > 0 {
			s := specs[lv-1]
			e.maxErr = s.maxErr
			for _, d := range s.drop {
				dropped[d] = true
			}
		}
		e.drop = dropped
		e.keep = map[string]bool{}
		if lv > 0 {
			for _, k := range specs[lv-1].keep {
				e.keep[k] = true
			}
		}
		if lv < level {
			continue // the walk of a lower level changes no state the next reads
		}
		root = &snode{name: r.Root.Name}
		e.part(r.Root, wctx{node: root, a: mesh.IdentityAffine}, true)
		if e.failed() {
			return nil, diag.Dedup(e.diags)
		}
	}

	k := float64(1000 * scale)
	defaults := resolve.Defaults(r.Params)
	var objects []threemf.Object
	var info []PrintObject
	used := map[string]int{} // lookup only
	hidden := 0
	var walk func(n *snode, off mesh.Vec3)
	walk = func(n *snode, off mesh.Vec3) {
		if n.cond != nil && !n.cond.Holds(defaults) {
			hidden++
			return
		}
		off = mesh.Add(off, n.t)
		for _, p := range n.pieces {
			meta := metas[p.m]
			base := strings.Join(append(append([]string(nil), meta.chain...), meta.shape), "/")
			shells := printShells(p.m)
			for si, sh := range shells {
				name := base
				if len(shells) > 1 {
					name = fmt.Sprintf("%s.%d", base, si+1)
				}
				used[name]++
				if used[name] > 1 {
					name = fmt.Sprintf("%s~%d", name, used[name])
				}
				o := objectOf(p.m, sh, off, k, upright)
				o.ID = len(objects) + 1
				o.Name = name
				objects = append(objects, o)
				info = append(info, PrintObject{ID: o.ID, Name: name, Material: meta.mat, Span: printSpan(e, meta)})
			}
		}
		for _, kid := range n.kids {
			walk(kid, off)
		}
	}
	walk(root, mesh.Vec3{})
	if len(objects) == 0 {
		return refuse(osp, "output %s level %d has no visible shape to print", oname, level)
	}

	model := threemf.NewModel(r.Root.Name, objects)
	b, err := threemf.Encode(model)
	if err != nil {
		return refuse(osp, "output %s level %d could not be written as 3MF: %v", oname, level, err)
	}
	// The gate reads the bytes back, as a slicer would.
	dec, err := threemf.Decode(b)
	if err != nil {
		return refuse(osp, "the 3MF of output %s level %d does not read back: %v", oname, level, err)
	}
	fs := threemf.Validate(dec)
	if len(fs) > 0 {
		byID := map[int]PrintObject{}
		for _, o := range info {
			byID[o.ID] = o
		}
		out := append([]diag.Diagnostic(nil), ds...)
		for _, f := range fs {
			sp := osp
			who := "the model"
			if o, ok := byID[f.Object]; ok {
				sp = o.Span
				who = fmt.Sprintf("the solid %q", o.Name)
			}
			out = append(out, diag.New(diag.CodePrint, sp, "%s of output %s level %d is not printable: %s", who, oname, level, f))
		}
		return nil, diag.Dedup(out)
	}

	p := &PrintProduct{Output: oname, Level: level, Scale: scale, Bytes: b, Model: dec}
	name := stem
	if level > 0 {
		name = fmt.Sprintf("%s-lod%d", stem, level)
	}
	p.Name = name + ".3mf"
	sum := sha256.Sum256(b)
	p.SHA256 = hex.EncodeToString(sum[:])
	first := true
	for i := range dec.Objects {
		o := &dec.Objects[i]
		po := info[i]
		po.Vertices, po.Triangles, po.Closed = len(o.Vertices), len(o.Triangles), true
		po.VolumeMM3 = threemf.Volume(o)
		lo, hi, _ := threemf.Bounds(o)
		po.Lo, po.Hi = [3]float64(lo), [3]float64(hi)
		info[i] = po
		p.Triangles += po.Triangles
		for a := 0; a < 3; a++ {
			if first || po.Lo[a] < p.Lo[a] {
				p.Lo[a] = po.Lo[a]
			}
			if first || po.Hi[a] > p.Hi[a] {
				p.Hi[a] = po.Hi[a]
			}
		}
		first = false
	}
	p.Objects = info
	p.Notes = printNotes(p, hidden, level)
	return p, diag.Dedup(ds)
}

// printShells returns the shell ids of a mesh in increasing order.
func printShells(m *mesh.Mesh) []int32 {
	seen := map[int32]bool{} // lookup only
	var out []int32
	for _, s := range m.Shell {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// objectOf makes the object of one shell. Vertices that are at one position
// in the evaluated mesh are one vertex, numbered by first use in triangle
// order. Each leaves the metre frame here: translated by the node offset,
// turned to +z up when upright, and scaled by k (1000 times the print scale).
func objectOf(m *mesh.Mesh, shell int32, off mesh.Vec3, k float64, upright bool) threemf.Object {
	type key [3]uint64
	var o threemf.Object
	ids := map[key]int{} // lookup only
	vertex := func(ci int32) int {
		p := m.Positions[m.Corners[ci].Pos]
		var kk key
		for j, v := range p {
			if v == 0 {
				v = 0 // folds -0 into +0
			}
			kk[j] = math.Float64bits(v)
		}
		if id, ok := ids[kk]; ok {
			return id
		}
		w := mesh.Add(p, off)
		if upright {
			w = mesh.Vec3{w[0], -w[2], w[1]}
		}
		id := len(o.Vertices)
		ids[kk] = id
		o.Vertices = append(o.Vertices, threemf.Vec3{float64(w[0] * k), float64(w[1] * k), float64(w[2] * k)})
		return id
	}
	for i, t := range m.Tris {
		if m.Shell[i] != shell {
			continue
		}
		a, b, c := vertex(t[0]), vertex(t[1]), vertex(t[2])
		o.Triangles = append(o.Triangles, threemf.Tri{a, b, c})
	}
	return o
}

// printSpan finds the declaration of a shape: its own name, else the part that
// holds it, innermost first. It falls back to the root part.
func printSpan(e *elab, m printMeta) diag.Span {
	fallback := e.spanOf("")
	for _, cand := range append([]string{m.shape}, reverse(m.chain)...) {
		if sp := e.spanOf(cand); cand != "" && sp != fallback {
			return sp
		}
	}
	return fallback
}

func reverse(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}

// printNotes lists what the product does not do, and which objects meet.
func printNotes(p *PrintProduct, hidden, level int) []string {
	var notes []string
	notes = append(notes,
		fmt.Sprintf("units and frame: millimetres, +z up; metres were converted once as (x, -z, y) x 1000 x %g", p.Scale),
		"minimum wall thickness: not checked; the specification names the check (FACET-PRINT-WALL, compiler.md 8.3) and gives no minimum thickness or method, and every object is written as a solid body",
		"materials and colours: not written; the 3MF material subset is not implemented, so the file carries geometry only",
		"intersection: each object is checked alone; two solids that cut into each other, or one that cuts itself, are not detected",
	)
	if hidden > 0 {
		notes = append(notes, fmt.Sprintf("runtime branches: %d node(s) hidden at the default parameter values are not printed", hidden))
	}
	if level > 0 {
		notes = append(notes, fmt.Sprintf("level %d: printed from the level's own tessellation, with its drops applied", level))
	}
	type pair struct{ a, b int }
	var meets []pair
	for i := range p.Objects {
		for j := i + 1; j < len(p.Objects); j++ {
			a, b := p.Objects[i], p.Objects[j]
			if a.Lo[0] <= b.Hi[0] && b.Lo[0] <= a.Hi[0] && a.Lo[1] <= b.Hi[1] && b.Lo[1] <= a.Hi[1] && a.Lo[2] <= b.Hi[2] && b.Lo[2] <= a.Hi[2] {
				meets = append(meets, pair{i, j})
			}
		}
	}
	if len(meets) > 0 {
		var parts []string
		for i, m := range meets {
			if i == 12 {
				parts = append(parts, fmt.Sprintf("and %d more", len(meets)-12))
				break
			}
			parts = append(parts, fmt.Sprintf("%s + %s", p.Objects[m.a].Name, p.Objects[m.b].Name))
		}
		notes = append(notes, fmt.Sprintf("separate bodies: %d pair(s) of objects have bounding boxes that meet, so they may touch or overlap; they are written as separate solids, not fused into one (compiler.md 8.3): %s", len(meets), strings.Join(parts, "; ")))
	}
	return notes
}
