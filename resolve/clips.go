package resolve

import (
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/units"
)

// A clip library is a project file that declares clips (and the lets they
// use) and no asset: `import "clips/football.gw" as football`. An asset
// installs every public clip of it with `use_clips { of = football }` inside
// its `asset` block. Importing alone installs nothing.
//
// A library clip is evaluated in the library's own scope, so its lets and its
// units are its own: a bare length in a library is metres unless the library
// writes a unit, and a bare angle is degrees. Joint names in a pose, and the
// mirror pairs of `side`, are those of the skeleton the clips are installed
// on: the library is written against the athlete's joint names and bind
// frames and is not retargeted. A clip whose name starts with `_` is private
// and is not installed.

// clipLibrary is a clip library read from the project.
type clipLibrary struct {
	path string
	file *ast.File
	pins []ast.Pin
	run  *run // evaluates the library's clips in its own scope
	span diag.Span
}

// usesClipLibrary says whether a `use_clips` block of the asset names the
// qualifier.
func (r *run) usesClipLibrary(qualifier string) bool {
	if r.f.Asset == nil {
		return false
	}
	for _, u := range r.f.Asset.UseClips {
		if s := ast.FindSetting(u.Settings, "of"); s != nil {
			if n, ok := s.Value.(*ast.Name); ok && n.Name == qualifier {
				return true
			}
		}
	}
	return false
}

// loadClipLibrary reads one project import that a `use_clips` block names.
func (r *run) loadClipLibrary(im *ast.Import) *clipLibrary {
	if r.f.Project == nil || r.f.Project.FS == nil {
		r.report(diag.New(diag.CodeUnsupported, im.PathSpan, "importing the clip library %q needs a project directory, and this source was read without one", im.Path))
		return nil
	}
	g, ds := r.f.Project.ImportClips(im.Path, im.PathSpan)
	for _, d := range ds {
		r.report(d)
	}
	if g == nil {
		return nil
	}
	if diag.HasErrors(g.Diagnostics) {
		r.report(diag.New(diag.CodeSyntax, im.PathSpan, "the clip library %q does not read cleanly: %s", im.Path, g.Diagnostics[0].Message))
		return nil
	}
	f := g.File
	if f.Asset != nil {
		r.report(diag.New(diag.CodeType, im.PathSpan, "%q declares an asset, so it is an asset to mount and not a clip library; a clip library declares clips and no asset", im.Path))
		return nil
	}
	if len(f.Imports) > 0 || len(f.Params) > 0 {
		r.report(diag.New(diag.CodeUnsupported, im.PathSpan, "a clip library has no imports and no parameters; %q has some", im.Path))
		return nil
	}
	for _, d := range f.Rigs {
		if d.Kind != "clip" {
			r.report(diag.New(diag.CodeType, im.PathSpan, "a clip library declares clips; %q declares a %s", im.Path, d.Kind))
			return nil
		}
	}
	lr := &run{f: f, d: units.DefaultUnits(), overs: map[string]*Override{}, libs: map[string]*library{}, gears: map[string]*GearImport{}, painters: map[string]*pentry{}}
	lr.resolved = &Resolved{}
	lr.syms = lr.declare()
	lr.bindGlobals()
	lr.lets()
	lib := &clipLibrary{path: g.Path, file: f, pins: g.Pins, run: lr, span: im.Span}
	r.flushLibrary(lib, im.Span)
	return lib
}

// flushLibrary reports what the library's own evaluation found at the place
// the host names the library: a span of the library's source means nothing in
// the host's.
func (r *run) flushLibrary(lib *clipLibrary, at diag.Span) {
	for _, d := range lib.run.diags {
		d.Span = at
		d.Edits, d.Related = nil, nil
		d.Message = "in the clip library " + lib.path + ": " + d.Message
		r.diags = append(r.diags, d)
	}
	lib.run.diags = nil
}

// installClips evaluates the public clips of every library a `use_clips`
// block names, on the asset's skeleton, and adds them to the rig.
func (r *run) installClips(st *rigState, rg *Rig) {
	if r.f.Asset == nil {
		return
	}
	for _, u := range r.f.Asset.UseClips {
		of := ast.FindSetting(u.Settings, "of")
		if of == nil || of.Value == nil {
			r.report(diag.New(diag.CodeMissing, u.Span, "`use_clips` needs `of`, the qualifier of an imported clip library"))
			continue
		}
		q, ok := of.Value.(*ast.Name)
		if !ok {
			r.report(diag.New(diag.CodeType, of.Value.Span(), "`of` is the qualifier of an imported clip library, a name"))
			continue
		}
		lib := r.clipLibs[q.Name]
		if lib == nil {
			if r.libs[q.Name] != nil || r.gears[q.Name] != nil || r.data[q.Name] != nil {
				r.report(diag.New(diag.CodeType, q.Span(), "`%s` is not a clip library: it is imported but `use_clips` reads project files that declare clips and no asset", q.Name))
			} else {
				r.report(diag.New(diag.CodeUnknownName, q.Span(), "`%s` is not an imported clip library", q.Name))
			}
			continue
		}
		if rg.Skeleton == nil {
			r.report(diag.New(diag.CodeMissing, u.Span, "`use_clips` needs a `skeleton` to install the clips on"))
			continue
		}
		if s := ast.FindSetting(u.Settings, "skeleton"); s != nil && s.Value != nil {
			n, isName := s.Value.(*ast.Name)
			if !isName || n.Name != rg.Skeleton.Name {
				r.report(diag.New(diag.CodeUnknownName, s.Value.Span(), "the asset's skeleton is `%s`", rg.Skeleton.Name))
				continue
			}
		}
		for _, d := range lib.file.Rigs {
			if strings.HasPrefix(d.Name.Name, "_") {
				continue
			}
			c := lib.run.clip(d, st, rg)
			r.flushLibrary(lib, u.Span)
			if c == nil {
				continue
			}
			c.Span = u.Span
			rg.Clips = append(rg.Clips, c)
		}
	}
}
