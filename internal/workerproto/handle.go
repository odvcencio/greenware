package workerproto

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/resolve"
)

// Host holds what differs between the browser worker and a native host.
type Host struct {
	// AllowUnrestricted lets a request ask for runtime_safe "off". The browser
	// worker leaves it false: it always imposes `runtime_safe required`.
	AllowUnrestricted bool
	// Canceled, when not nil, is asked between the phases of a request (parse
	// and lower, admission, build, encode). The compiler has no phase boundary
	// inside BuildWith, so a build that has started runs to its end. In the
	// WebAssembly worker nothing can answer true while a build runs, because
	// the worker has one thread; a page cancels a running build by calling
	// Worker.terminate().
	Canceled func() bool
}

// Handle answers a request with the default host: the browser worker's.
func Handle(req Request) Response { return Host{}.Handle(req) }

// HandleBytes decodes a request document, answers it and encodes the answer.
func HandleBytes(b []byte) []byte { return Host{}.HandleBytes(b) }

// HandleBytes is the document form of Handle.
func (h Host) HandleBytes(b []byte) []byte {
	req, err := DecodeRequest(b)
	if err != nil {
		e := err.(*Error)
		return Encode(failure(req.ID, e.Code, e.Message))
	}
	return Encode(h.Handle(req))
}

// Handle runs one request. It never panics: a panic in the compiler becomes
// an internal error response.
func (h Host) Handle(req Request) (resp Response) {
	defer func() {
		if r := recover(); r != nil {
			resp = failure(req.ID, ErrInternal, fmt.Sprintf("the compiler stopped: %v", r))
		}
	}()
	if req.Schema != "" && req.Schema != RequestSchema {
		return failure(req.ID, ErrBadRequest, fmt.Sprintf("the request schema is %q; this worker reads %q", req.Schema, RequestSchema))
	}
	if err := validate(req); err != nil {
		e := err.(*Error)
		return failure(req.ID, e.Code, e.Message)
	}
	safe := req.Options.RuntimeSafe != SafeOff
	if !safe && !h.AllowUnrestricted {
		return failure(req.ID, ErrBadRequest, "this worker always imposes runtime_safe required")
	}
	cancel := func() *Response {
		if h.Canceled != nil && h.Canceled() {
			r := failure(req.ID, ErrCanceled, "the request was canceled between phases")
			return &r
		}
		return nil
	}

	// Phase 1: parse and lower, reading project files from the snapshot only.
	if c := cancel(); c != nil {
		return *c
	}
	src := req.Files[req.Root]
	tree := lang.Parse(src)
	dir := path.Dir(req.Root)
	if dir == "." {
		dir = ""
	}
	proj := &ast.Project{FS: snapshot(req.Files), Dir: dir}
	file, ds := ast.LowerWith(tree, ast.Options{Project: proj})
	loc := func(off int) (int, int) {
		p := tree.Position(off)
		return p.Line, p.Column
	}
	refuse := func(ds []diag.Diagnostic) Response {
		return Response{ID: req.ID, Diagnostics: diagnostics(ds, loc)}
	}
	if diag.HasErrors(ds) {
		return refuse(ds)
	}

	// Phase 2: worker admission, before any geometry is built.
	if c := cancel(); c != nil {
		return *c
	}
	if safe {
		if more := admit(file, req); len(more) > 0 {
			return refuse(diag.Dedup(append(ds, more...)))
		}
	}

	// Phase 3: the build, the same call `greenware build` makes.
	if c := cancel(); c != nil {
		return *c
	}
	opts := compiler.Options{Policy: compiler.Policy{MaxTotalWork: req.Options.MaxWork, RuntimePoints: req.Options.RuntimePoints}}
	p, _, more := compiler.BuildWith(file, req.Output, opts)
	ds = diag.Dedup(append(ds, more...))
	if p == nil || diag.HasErrors(ds) {
		return refuse(ds)
	}

	// Phase 4: encode the answer.
	if c := cancel(); c != nil {
		return *c
	}
	resp = Response{ID: req.ID, OK: true}
	if len(ds) > 0 {
		resp.Diagnostics = diagnostics(ds, loc)
	}
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			resp.Files = append(resp.Files, File{Output: o.Name, Name: f.Name, Level: f.Level, SHA256: f.SHA256, GLB: f.GLB})
		}
	}
	rec := p.Record()
	sum := sha256.Sum256(rec)
	resp.Record = &Record{Name: p.RecordName(), SHA256: hex.EncodeToString(sum[:]), Bytes: rec}
	return resp
}

func diagnostics(ds []diag.Diagnostic, loc diag.Locator) []byte {
	b, err := diag.Marshal(ds, loc)
	if err != nil {
		panic("workerproto: " + err.Error())
	}
	return []byte(strings.TrimSpace(string(b)))
}

// admit is the `runtime_safe required` admission of compiler.md 3 and 9.4. It
// runs before BuildWith and builds nothing. It refuses:
//
//   - a total_work ceiling above the edition default, which is the worker's
//     ceiling (GW-LIMIT): a request may lower the ceiling, not raise it;
//   - a `check rig` audit, a `check runtime` audit and a mount with a fit
//     check (GW-RUNTIME-UNSAFE): the full athlete audit goes to the native
//     build or the service, and these have no certified worker charge.
//
// Everything else the compiler builds today is a bounded, ticketed operation
// (the cost schedule): metered primitives, sweeps, lofts, arrays, painted
// atlases, rig weights, morphs and clips. The compiler has no boolean,
// subdivision, remeshing, mesh import, weight solving, sculpt or unwrap to
// refuse; a source that asks for one gets GW-UNSUPPORTED from the front end.
func admit(file *ast.File, req Request) []diag.Diagnostic {
	var out []diag.Diagnostic
	if req.Options.MaxWork > compiler.DefaultMaxTotalWork {
		out = append(out, diag.New(diag.CodeLimit, diag.Span{},
			"the request asks for %d work units; the worker ceiling under runtime_safe required is %d",
			req.Options.MaxWork, compiler.DefaultMaxTotalWork))
	}
	refs, ds := resolve.Outputs(file)
	if diag.HasErrors(ds) {
		return append(out, ds...)
	}
	var names []string
	for _, o := range refs {
		if req.Output == "" || o.Name == req.Output || o.Base.Name.Name == req.Output {
			names = append(names, o.Name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		r, _ := resolve.EvaluateOutput(file, name)
		if r == nil {
			continue
		}
		if r.RigCheck != nil {
			out = append(out, diag.New(diag.CodeRuntimeUnsafe, r.RigCheck.Span,
				"output %s asks for a `check rig` audit, which has no certified worker charge; build it with the native greenware or use its prebuilt GLB", name))
		}
		if r.RuntimeCheck != nil {
			out = append(out, diag.New(diag.CodeRuntimeUnsafe, r.RuntimeCheck.Span,
				"output %s asks for a `check runtime` audit, which has no certified worker charge; build it with the native greenware or use its prebuilt GLB", name))
		}
		if r.Rig != nil && r.Rig.Skin != nil {
			for _, st := range r.Rig.Skin.Steps {
				if st.Solve != nil {
					out = append(out, diag.New(diag.CodeRuntimeUnsafe, st.Solve.Span,
						"output %s has a `solve` step in its skin, which is not runtime safe and has no certified worker charge; build it with the native greenware or use its prebuilt GLB", name))
				}
			}
		}
		for _, m := range r.Mounts {
			if m.HasFit {
				out = append(out, diag.New(diag.CodeRuntimeUnsafe, m.FitSpan,
					"output %s mount %s has a fit check, which has no certified worker charge; build it with the native greenware or use its prebuilt GLB", name, m.Name))
			}
		}
	}
	return out
}
