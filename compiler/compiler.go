package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

// Generator is written to asset.generator of every file.
const Generator = "greenware 0.1"

// File is one built GLB: one level of one output.
type File struct {
	Name   string         // base file name, e.g. goalpost-lod1.glb
	Level  int            // 0 is the source level
	Doc    *gltf.Document // the document that was encoded
	GLB    []byte         // the encoded bytes
	SHA256 string         // hex SHA-256 of GLB

	// Measured on the encoded file.
	Triangles int // triangles drawn in the default look
	Draws     int // draws in the default look: one per visible primitive
	Materials int // distinct materials written
	Vertices  int // written glTF vertices, every branch
	Size      int // len(GLB)
	// TrianglesWorst and DrawsWorst are the largest counts over every
	// reachable look of the runtime parameters; TrianglesStored counts every
	// branch of the file. Without runtime branches the three triangle counts
	// are equal, and so are Draws and DrawsWorst.
	TrianglesWorst  int
	TrianglesStored int
	DrawsWorst      int
	// Alternates is the number of roots of the alternates scene.
	Alternates int
}

// OutputProduct is the files of one output, level 0 first.
type OutputProduct struct {
	Name      string
	FirSHA256 string // the meaning hash written into every file of the output
	Files     []*File
}

// Product is the result of Build.
type Product struct {
	Asset        string // the asset's name, which names the record file
	SourceSHA256 string // hex SHA-256 of the source bytes
	InputSHA256  string // hex SHA-256 of the input record: source, selection, compiler
	Outputs      []*OutputProduct
	// Unsupported lists what the source asked for and the compiler did not
	// write, such as the GoSX manifest entry. None of it stops a build.
	Unsupported []string
	// Receipt lists the cost tickets the build reserved, in order. It is nil
	// for a Product that was not made by Build.
	Receipt *Receipt
}

// Build compiles one output, or every output when outputName is empty. It
// returns a nil product when any diagnostic is an error: nothing is
// published. The diagnostics include those of the front end for the chosen
// outputs. It runs under the default policy and plans no acceptance step; see
// BuildWith.
func Build(file *ast.File, outputName string) (*Product, []diag.Diagnostic) {
	p, _, ds := BuildWith(file, outputName, Options{})
	return p, ds
}

// arrayCapMessage is the start of the front end's message for an array above
// its copy cap. The front end reports it as GW-FIELD-RANGE; BuildWith reports
// it as GW-LIMIT on the `plan` ticket (compiler.md 3.1: an operand above a
// class bound is a count ceiling). TestLimitOversizedArray pins the text.
const arrayCapMessage = "an array makes at most"

// BuildWith is Build under an explicit policy and with an optional planned
// acceptance step. It reserves every ticket of the run, in schedule order,
// before it builds anything (see the package's cost ledger). The receipt is
// returned even when the build is refused, holding the tickets reserved so far
// and the refusal. It is nil only when the build stopped before the ledger
// began.
func BuildWith(file *ast.File, outputName string, opts Options) (*Product, *Receipt, []diag.Diagnostic) {
	if file == nil || file.Broken {
		return nil, nil, []diag.Diagnostic{diag.New(diag.CodeSyntax, diag.Span{}, "the file has syntax errors; nothing was built")}
	}
	var names []string
	for _, o := range file.Outputs {
		if outputName == "" || o.Name.Name == outputName {
			names = append(names, o.Name.Name)
		}
	}
	if len(names) == 0 {
		if outputName != "" {
			return nil, nil, []diag.Diagnostic{diag.New(diag.CodeUnknownName, diag.Span{}, "the file has no output named %q", outputName)}
		}
		return nil, nil, []diag.Diagnostic{diag.New(diag.CodeMissing, diag.Span{}, "the file declares no output to build")}
	}
	srcSum := sha256.Sum256(file.Source)
	p := &Product{SourceSHA256: hex.EncodeToString(srcSum[:])}
	in := sha256.New()
	fmt.Fprintf(in, "greenware.input/1\n%s\nsource %s\noutput %s\n", Generator, p.SourceSHA256, outputName)
	p.InputSHA256 = hex.EncodeToString(in.Sum(nil))
	if file.Asset != nil {
		p.Asset = file.Asset.Name.Name
	}

	// Front end for every chosen output, before any ticket: a different error
	// is not a cost receipt.
	var all []diag.Diagnostic
	var resolved []resolvedOutput
	byName := map[string]resolvedOutput{}
	for _, name := range names {
		r, ds := resolve.EvaluateOutput(file, name)
		for i := range ds {
			if ds[i].Code == diag.CodeFieldRange && strings.HasPrefix(ds[i].Message, arrayCapMessage) {
				ds[i].Code = diag.CodeLimit
				ds[i].Severity = diag.Error
				ds[i].Message = fmt.Sprintf("the `%s` ticket (%s %s) cannot take this operand: %s (the class allows %d copies per array)",
					TicketPlan, TicketPlan, name, ds[i].Message, maxPlanItems)
			}
		}
		all = append(all, ds...)
		if diag.HasErrors(ds) || r == nil {
			continue
		}
		ro := resolvedOutput{name: name, r: r, levels: len(r.Lods) + 1}
		resolved = append(resolved, ro)
		byName[name] = ro
	}

	// The ledger: reserve the whole planned run before running any of it.
	var rc *Receipt
	if !diag.HasErrors(all) {
		levels := map[string]int{}
		for _, o := range resolved {
			levels[o.name] = o.levels
		}
		var plan []acceptLevel
		var inputs = []int{len(file.Source)}
		if opts.Accept != nil {
			plan = planAccept(opts.Accept, levels)
			inputs = append(inputs, len(opts.Accept.Source))
		}
		accByKey := map[accKey]acceptLevel{}
		for _, a := range plan {
			k := accKey{a.output, a.level}
			cur := accByKey[k]
			cur.output, cur.level = a.output, a.level
			cur.nonImage += a.nonImage
			accByKey[k] = cur
		}
		lg := newLedger(opts.Policy.limit())
		rc = lg.rc
		for _, c := range schedule(resolved, plan) {
			// The class bound comes first: a ticket whose operand does not fit
			// its class is never priced.
			var ref *Refusal
			if b := classBound(c, byName, inputs, accByKey); b != "" {
				ref = &Refusal{Charge: c, Bound: b}
			} else {
				ref = lg.reserve(c)
			}
			if ref != nil {
				lg.rc.Refused = ref
				return nil, rc, diag.Dedup(append(all, limitDiag(outputSpan(file, c.Output), ref, lg.rc.Limit)))
			}
		}
		p.Receipt = rc
	}

	files := map[string]string{} // output file name -> output name; lookup only
	for _, ro := range resolved {
		name := ro.name
		op, ds, notes := buildOutput(file, ro.r, name)
		all = append(all, ds...)
		if op == nil || diag.HasErrors(ds) {
			continue
		}
		if ref := classBoundFiles(op, usesBranchClass(ro.r)); ref != nil && rc != nil {
			rc.Refused = ref
			all = append(all, limitDiag(outputSpan(file, name), ref, rc.Limit))
			continue
		}
		for _, f := range op.Files {
			if prev, dup := files[f.Name]; dup {
				all = append(all, diag.New(diag.CodeDuplicate, outputSpan(file, name), "the file name %q is written by outputs %s and %s", f.Name, prev, name))
			}
			files[f.Name] = name
		}
		p.Outputs = append(p.Outputs, op)
		p.Unsupported = appendUnique(p.Unsupported, notes...)
	}
	all = diag.Dedup(all)
	if diag.HasErrors(all) {
		return nil, rc, all
	}
	return p, rc, all
}

// classBoundFiles checks the class bounds that only the built files show: the
// triangles of a level (the `surface` ticket) and the size of a file (the
// `encode` ticket). It returns the first breach in level order.
func classBoundFiles(op *OutputProduct, branches bool) *Refusal {
	for _, f := range op.Files {
		lv := f.Level
		limit, ticket := maxSurfaceTris, TicketSurface
		if branches {
			limit, ticket = maxSurfaceBranchTris, TicketSurfaceBranches
		}
		if f.TrianglesStored > limit {
			return &Refusal{Charge: Charge{Ticket: ticket, Output: op.Name, Level: &lv},
				Bound: fmt.Sprintf("output %s level %d holds %d triangles; the class allows %d", op.Name, lv, f.TrianglesStored, limit)}
		}
		if f.Size > maxEncodeBytes {
			return &Refusal{Charge: Charge{Ticket: TicketEncode, Output: op.Name, Level: &lv},
				Bound: fmt.Sprintf("%s is %d bytes; the class allows %d", f.Name, f.Size, maxEncodeBytes)}
		}
	}
	return nil
}

func appendUnique(dst []string, add ...string) []string {
	for _, a := range add {
		found := false
		for _, d := range dst {
			if d == a {
				found = true
			}
		}
		if !found {
			dst = append(dst, a)
		}
	}
	return dst
}

func outputSpan(f *ast.File, name string) diag.Span {
	for _, o := range f.Outputs {
		if o.Name.Name == name {
			return o.Name.Span
		}
	}
	return diag.Span{}
}

// levelSpec is one level of detail as the compiler reads it.
type levelSpec struct {
	n      int
	maxErr float64
	drop   []string
}

func (e *elab) levelSpecs() []levelSpec {
	var out []levelSpec
	for _, l := range e.r.Lods {
		spec := levelSpec{}
		fmt.Sscan(l.Name, &spec.n)
		for _, f := range l.Fields {
			switch f.Name {
			case "max_error":
				spec.maxErr = f.Value.Num
			case "drop":
				for _, d := range f.Value.Elems {
					spec.drop = append(spec.drop, d.Str)
				}
			case "keep":
				if !f.Default && len(f.Value.Elems) > 0 {
					e.report(diag.CodeUnsupported, lodSpan(e.file, spec.n), "`keep` on level %d is not supported yet", spec.n)
				}
			case "segments":
				if !f.Default {
					e.report(diag.CodeUnsupported, lodSpan(e.file, spec.n), "`segments` on level %d is not supported yet", spec.n)
				}
			}
		}
		out = append(out, spec)
	}
	return out
}

func lodSpan(f *ast.File, n int) diag.Span {
	for _, l := range f.Lods {
		if l.Level == n {
			return l.LevelSpan
		}
	}
	return diag.Span{}
}

// splitFile checks an output's file name and returns its stem and extension.
func splitFile(name string) (stem, ext string, ok bool) {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".glb") {
		return "", "", false
	}
	return strings.TrimSuffix(name, ".glb"), ".glb", true
}

func buildOutput(file *ast.File, r *resolve.Resolved, oname string) (*OutputProduct, []diag.Diagnostic, []string) {
	e := &elab{file: file, r: r, defs: map[string]*resolve.Node{}}
	var notes []string
	var out resolve.Output
	for _, o := range r.Outputs {
		if o.Name == oname {
			out = o
		}
	}
	osp := outputSpan(file, oname)
	stem, ext, ok := splitFile(out.File)
	if !ok {
		e.report(diag.CodeUnsupported, osp, "the output file %q must be a plain name ending in .glb", out.File)
		return nil, e.diags, nil
	}
	if out.LODs != "files" {
		e.report(diag.CodeUnsupported, osp, "`lods = %s` is not supported yet; the compiler writes one file per level", out.LODs)
	}
	if out.Profile != "source" {
		e.report(diag.CodeUnsupported, osp, "`profile = %s` is not supported yet", out.Profile)
	}
	if out.Manifest {
		notes = append(notes, fmt.Sprintf("output %s: manifest = on writes no GoSX manifest entry; the specification does not define its layout", oname))
	}
	if r.Root == nil {
		e.report(diag.CodeMissing, osp, "the asset has no root part to build")
		return nil, e.diags, nil
	}
	for _, d := range r.Definitions {
		e.defs[d.Name] = d
	}
	specs := e.levelSpecs()
	for _, m := range r.Materials {
		if m.Kind == "selena" {
			notes = appendUnique(notes, "selena materials: the artifact is not read or pinned; the pbr fallback is written")
		}
	}
	if e.failed() {
		return nil, e.diags, nil
	}
	fir := firHash(r)
	cols := e.colliders()
	op := &OutputProduct{Name: oname, FirSHA256: fir}

	dropped := map[string]bool{}
	for lv := 0; lv <= len(specs); lv++ {
		e.maxErr = 0
		e.lodErr = 0
		if lv > 0 {
			s := specs[lv-1]
			e.maxErr = s.maxErr
			for _, d := range s.drop {
				dropped[d] = true
			}
		}
		e.drop = dropped
		root := &snode{name: r.Root.Name}
		e.part(r.Root, wctx{node: root, a: mesh.IdentityAffine}, true)
		if e.failed() {
			return nil, diag.Dedup(e.diags), nil
		}
		l, ok := e.assemble(root, cols, lv, fir)
		if !ok || e.failed() {
			return nil, diag.Dedup(e.diags), nil
		}
		glb, err := gltf.Encode(l.doc)
		if err != nil {
			e.report(diag.CodeDomain, osp, "level %d could not be written as glTF: %v", lv, err)
			return nil, e.diags, nil
		}
		sum := sha256.Sum256(glb)
		name := out.File
		if lv > 0 {
			name = stem + "-lod" + strconv.Itoa(lv) + ext
		}
		op.Files = append(op.Files, &File{
			Name: name, Level: lv, Doc: l.doc, GLB: glb, SHA256: hex.EncodeToString(sum[:]),
			Triangles: l.tris, Draws: l.draws, Materials: l.mats, Vertices: l.verts, Size: len(glb),
			TrianglesWorst: l.trisWorst, TrianglesStored: l.trisStored, DrawsWorst: l.drawsWorst, Alternates: l.alternates,
		})
	}
	e.checkBudget(op, oname)
	if e.failed() {
		return nil, diag.Dedup(e.diags), nil
	}
	return op, diag.Dedup(e.diags), notes
}

// checkBudget compares each file with the output's effective budget.
func (e *elab) checkBudget(op *OutputProduct, oname string) {
	for _, bf := range e.r.Budget {
		for _, f := range op.Files {
			var got int
			switch bf.Name {
			case "triangles":
				got = f.Triangles
			case "triangles_worst":
				got = f.TrianglesWorst
			case "triangles_stored":
				got = f.TrianglesStored
			case "draw_calls":
				got = f.Draws
			case "draw_calls_worst":
				got = f.DrawsWorst
			case "vertices":
				got = f.Vertices
			case "materials":
				got = f.Materials
			case "file_size":
				got = f.Size
			case "bones", "influences", "morphs", "texture_bytes":
				got = 0
			default:
				continue
			}
			if float64(got) > bf.Value.Num {
				unit := ""
				if bf.Value.Dim == units.Bytes {
					unit = " bytes"
				}
				e.report(diag.CodeBudget, e.budgetSpan(oname, bf.Name),
					"output %s, level %d (%s): `%s` measures %d%s but the budget allows %s",
					oname, f.Level, f.Name, bf.Name, got, unit, bf.Value.Format())
			}
		}
	}
}

// budgetSpan finds the source of an effective budget limit: the output's own
// budget block first, then the document's.
func (e *elab) budgetSpan(oname, field string) diag.Span {
	for _, o := range e.file.Outputs {
		if o.Name.Name == oname && o.Budget != nil {
			if s := ast.FindSetting(o.Budget.Settings, field); s != nil {
				return s.Value.Span()
			}
		}
	}
	if e.file.Budget != nil {
		if s := ast.FindSetting(e.file.Budget.Settings, field); s != nil {
			return s.Value.Span()
		}
	}
	return outputSpan(e.file, oname)
}

// ---- the meaning hash --------------------------------------------------------

// firHash hashes the product-bearing part of a resolved output: the part
// tree, the templates, the materials, the runtime parameters, the level
// settings that change geometry, the collision shapes and the rights holder.
// It leaves out record fields (budgets, meta text, level distances, doc
// strings) and the Default flags, so spelling a default does not change it.
func firHash(r *resolve.Resolved) string {
	type param struct {
		Name  string       `json:"name"`
		Type  string       `json:"type"`
		Value units.Value  `json:"value"`
		Lo    *units.Value `json:"lo,omitempty"`
		Hi    *units.Value `json:"hi,omitempty"`
		// Members of an enum are part of what a binding can test.
		Members []string `json:"members,omitempty"`
	}
	doc := struct {
		Format      string          `json:"format"`
		Copyright   string          `json:"copyright"`
		Runtime     []param         `json:"runtime"`
		Materials   []*resolve.Node `json:"materials"`
		Root        *resolve.Node   `json:"root"`
		Definitions []*resolve.Node `json:"definitions"`
		Lods        []*resolve.Node `json:"lods"`
		Collision   []*resolve.Node `json:"collision"`
	}{
		Format:      "greenware.fir.product/1",
		Copyright:   metaString(r, "author"),
		Root:        canon(r.Root, nil),
		Definitions: canonAll(r.Definitions, nil),
		Materials:   canonAll(r.Materials, nil),
		Collision:   canonAll(r.Collision, nil),
		Lods:        canonAll(r.Lods, map[string]bool{"max_error": true, "drop": true, "keep": true, "segments": true}),
	}
	for _, p := range r.Params {
		if p.Runtime {
			doc.Runtime = append(doc.Runtime, param{Name: p.Name, Type: p.Type, Value: p.Value, Lo: p.Lo, Hi: p.Hi, Members: p.Members})
		}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		panic("compiler: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func canonAll(ns []*resolve.Node, only map[string]bool) []*resolve.Node {
	out := make([]*resolve.Node, 0, len(ns))
	for _, n := range ns {
		out = append(out, canon(n, only))
	}
	return out
}

// canon copies a node without the Default flags. With only set, it keeps just
// the named fields.
func canon(n *resolve.Node, only map[string]bool) *resolve.Node {
	if n == nil {
		return nil
	}
	c := &resolve.Node{Kind: n.Kind, Name: n.Name, Pair: n.Pair, Rows: n.Rows, Condition: n.Condition, Test: n.Test}
	for _, f := range n.Fields {
		if only != nil && !only[f.Name] {
			continue
		}
		c.Fields = append(c.Fields, resolve.Field{Name: f.Name, Value: f.Value})
	}
	if n.Kind == "lod" {
		c.Name = n.Name
	}
	for _, ch := range n.Children {
		c.Children = append(c.Children, canon(ch, only))
	}
	for _, ch := range n.Else {
		c.Else = append(c.Else, canon(ch, only))
	}
	return c
}
