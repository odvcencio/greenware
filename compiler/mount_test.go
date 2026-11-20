package compiler

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
)

// A small host and gear for the mount tests. The skull is a sphere of radius
// 0.1 m on the head joint; the cap is a sphere of radius 0.104 m about the same
// centre, so the surfaces are 4 mm apart at a vertex and a little more between.
const mountGearSrc = `greenware 1
asset cap { root = cap }
plug crown_plug { kind = hat at = (0, 1.6, 0) }
part cap { sphere dome { radius = %s at = (0, %s, 0) } }
output cap { file = "cap.glb" }
`

const mountHostSrc = `greenware 1
import "gear.gw" as gear
asset host { root = host idle = rest }
skeleton s {
  joint base {
    at = (0, 0, 0)
    joint head {
      at = (0, 1.5, 0)
      socket crown { kind = hat at = (0, 1.6, 0) }
    }
  }
}
part host { joint = head sphere skull { radius = 0.1m at = (0, 1.6, 0) } }
mount hat {
  asset = gear
  socket = crown
  mode = %s
  fit { clearance = %s }
}
clip rest { duration = 1s pose { base = rot(0, 0, 0) head = rot(0, 0, 0) } }
output demo { file = "host.glb" %s }
`

func mountProject(t testing.TB, gear, host string) *ast.File {
	t.Helper()
	fsys := fstest.MapFS{"gear.gw": {Data: []byte(gear)}, "host.gw": {Data: []byte(host)}}
	return lowerIn(t, host, fsys, "")
}

func mountBuild(t testing.TB, radius, y, mode, clearance, extra string) (*Product, []diag.Diagnostic) {
	t.Helper()
	f := mountProject(t, fmt.Sprintf(mountGearSrc, radius, y), fmt.Sprintf(mountHostSrc, mode, clearance, extra))
	p, _, ds := BuildWith(f, "", Options{})
	return p, ds
}

// The fit check measures the triangles of the posed gear against the posed
// skin, in the bind pose and at every key. A clearance below the measured gap
// passes and one above it refuses with GW-MOUNT.
func TestMountFitIsMeasuredAndRefused(t *testing.T) {
	p, ds := mountBuild(t, "0.104m", "1.6m", "runtime", "2mm", "")
	if p == nil {
		t.Fatalf("2 mm clearance must pass: %v", ds)
	}
	fit := p.Outputs[0].Files[0].Audit.Fit
	if len(fit) != 1 || fit[0].Mount != "hat" || fit[0].AtLeast {
		t.Fatalf("fit report %+v", fit)
	}
	gap := fit[0].Clearance
	if gap < 0.0035 || gap > 0.004 {
		t.Errorf("measured gap %.6f m, want about 3.9 mm", gap)
	}
	if fit[0].GearPart != "cap" || fit[0].HostPart != "host" && fit[0].HostPart != "skull" {
		t.Errorf("closest parts %q %q", fit[0].GearPart, fit[0].HostPart)
	}
	// One micrometre either side of the measured gap.
	if p, ds := mountBuild(t, "0.104m", "1.6m", "runtime", fmt.Sprintf("%.9fm", gap-1e-6), ""); p == nil {
		t.Errorf("a clearance %.9f m below the gap must pass: %v", gap-1e-6, ds)
	}
	p, ds = mountBuild(t, "0.104m", "1.6m", "runtime", fmt.Sprintf("%.9fm", gap+1e-6), "")
	if p != nil {
		t.Fatal("a clearance above the gap must refuse")
	}
	var found bool
	for _, d := range ds {
		if d.Code == diag.CodeMount && strings.Contains(d.Message, "comes within") && strings.Contains(d.Message, "bind") && strings.Contains(d.Message, "requires") {
			found = true
		}
	}
	if !found {
		t.Errorf("no GW-MOUNT fit diagnostic: %v", ds)
	}
}

// Penetration fails even at clearance zero.
func TestMountPenetrationFailsAtZeroClearance(t *testing.T) {
	// A cap lifted 0.03 m crosses the skull's surface.
	p, ds := mountBuild(t, "0.104m", "1.63m", "runtime", "0mm", "")
	if p != nil {
		t.Fatal("a gear that crosses the host surface must refuse at clearance zero")
	}
	found := false
	for _, d := range ds {
		if d.Code == diag.CodeMount && strings.Contains(d.Message, "touches or penetrates") {
			found = true
		}
	}
	if !found {
		t.Errorf("diagnostics %v", ds)
	}
	// And a clearance of zero with a gap passes.
	if p, ds := mountBuild(t, "0.104m", "1.6m", "runtime", "0mm", ""); p == nil {
		t.Errorf("zero clearance with a gap: %v", ds)
	}
}

// The fit follows the head through a clip: a key that turns the head about the
// socket keeps the gear and the skull together, one that swings the head away
// from a fixed root does not change the gap.
func TestMountFitIsCheckedAtEveryKey(t *testing.T) {
	gear := fmt.Sprintf(mountGearSrc, "0.104m", "1.6m")
	host := fmt.Sprintf(mountHostSrc, "runtime", "2mm", "")
	host = strings.Replace(host, "clip rest { duration = 1s pose { base = rot(0, 0, 0) head = rot(0, 0, 0) } }",
		"clip rest { duration = 1s pose { base = rot(0, 0, 0) head = rot(0, 0, 0) } }\nclip nod { duration = 1s keys = 5 pose { base = rot(20 * t, 0, 0) head = rot(30 * t, 0, 10) } }", 1)
	f := mountProject(t, gear, host)
	p, _, ds := BuildWith(f, "", Options{})
	if p == nil {
		t.Fatalf("%v", ds)
	}
	a := p.Outputs[0].Files[0].Audit
	// bind + rest (1 key... two ends) + nod 5 keys
	if a.Poses < 7 {
		t.Errorf("%d poses measured", a.Poses)
	}
	if a.Fit[0].Clearance < 0.0035 {
		t.Errorf("a rigid head keeps its gap: %.6f", a.Fit[0].Clearance)
	}
}

// A runtime mount is a record, and a baked mount merges the gear into the host.
func TestMountModes(t *testing.T) {
	rt, ds := mountBuild(t, "0.104m", "1.6m", "runtime", "2mm", "")
	if rt == nil {
		t.Fatal(ds)
	}
	bk, ds := mountBuild(t, "0.104m", "1.6m", "bake", "2mm", "")
	if bk == nil {
		t.Fatal(ds)
	}
	r, b := rt.Outputs[0].Files[0], bk.Outputs[0].Files[0]
	if len(r.Doc.Meshes[0].Primitives) != 1 || len(b.Doc.Meshes[0].Primitives) != 2 {
		t.Errorf("primitives: runtime %d, bake %d", len(r.Doc.Meshes[0].Primitives), len(b.Doc.Meshes[0].Primitives))
	}
	if b.Triangles <= r.Triangles || b.Draws != r.Draws+1 {
		t.Errorf("bake adds geometry and a draw: %d/%d tris, %d/%d draws", b.Triangles, r.Triangles, b.Draws, r.Draws)
	}
	if !strings.Contains(string(r.Doc.Extras), `"mode":"runtime"`) || !strings.Contains(string(b.Doc.Extras), `"mode":"bake"`) ||
		!strings.Contains(string(b.Doc.Extras), `"baked":{`) {
		t.Errorf("mount records: %s / %s", r.Doc.Extras, b.Doc.Extras)
	}
	if rt.Outputs[0].FirSHA256 == bk.Outputs[0].FirSHA256 {
		t.Error("the mode is part of the host's meaning")
	}
	// The output's `mounts` overrides the mount's mode.
	ov, ds := mountBuild(t, "0.104m", "1.6m", "runtime", "2mm", `mounts = bake`)
	if ov == nil {
		t.Fatal(ds)
	}
	if ov.Outputs[0].Files[0].SHA256 != b.SHA256 {
		t.Error("`mounts = bake` on the output does not give the baked file")
	}
	back, ds := mountBuild(t, "0.104m", "1.6m", "bake", "2mm", `mounts = runtime`)
	if back == nil {
		t.Fatal(ds)
	}
	if back.Outputs[0].Files[0].SHA256 != r.SHA256 {
		t.Error("`mounts = runtime` on the output does not give the runtime file")
	}
	// The gear in the host sits where the helmet's own file puts it: the plug
	// and the socket coincide, so no vertex moved.
	hp := b.Doc.Meshes[0].Primitives[1]
	if len(hp.Positions) == 0 || hp.Positions[1] < 1.4 || hp.Positions[1] > 1.8 {
		t.Errorf("gear vertex at %v", hp.Positions[:3])
	}
}

// Socket, plug and output selection errors are GW-MOUNT (or an unknown name).
func TestMountErrors(t *testing.T) {
	gear := fmt.Sprintf(mountGearSrc, "0.104m", "1.6m")
	host := fmt.Sprintf(mountHostSrc, "runtime", "2mm", "")
	cases := []struct {
		name, gear, host string
		code             string
		frag             string
	}{
		{"missing socket", gear, strings.Replace(host, "socket = crown", "socket = nape", 1), diag.CodeMount, "does not have"},
		{"plug of another kind", strings.Replace(gear, "kind = hat", "kind = shoe", 1), host, diag.CodeMount, "no plug of the kind `hat`"},
		{"unknown asset", gear, strings.Replace(host, "asset = gear", "asset = glove", 1), diag.CodeUnknownName, "not an imported project asset"},
		{"unknown gear parameter", gear, strings.Replace(host, "mode = runtime", "mode = runtime\n  set { colour = red }", 1), diag.CodeUnknownName, "not a parameter of"},
		{"negative clearance", gear, strings.Replace(host, "fit { clearance = 2mm }", "fit { clearance = -1mm }", 1), diag.CodeFieldRange, "negative"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fsys := fstest.MapFS{"gear.gw": {Data: []byte(c.gear)}}
			f, ds := ast.LowerWith(lang.Parse([]byte(c.host)), ast.Options{Project: &ast.Project{FS: fsys}})
			p, _, more := BuildWith(f, "", Options{})
			ds = append(ds, more...)
			if p != nil {
				t.Fatal("built")
			}
			for _, d := range ds {
				if d.Code == c.code && strings.Contains(d.Message, c.frag) {
					return
				}
			}
			t.Errorf("want %s mentioning %q, got %v", c.code, c.frag, ds)
		})
	}
}

// A mount's `set` picks the gear output by its bake parameter; with no bake
// value, two outputs are ambiguous.
func TestMountSelectsOneGearOutput(t *testing.T) {
	gear := `greenware 1
asset cap { root = cap }
param style : enum(round, tall) = round
plug crown_plug { kind = hat at = (0, 1.6, 0) }
part cap { sphere dome { radius = 0.104m at = (0, 1.6, 0) } }
output round { file = "round.glb" set { style = round } }
output tall { file = "tall.glb" set { style = tall } }
`
	host := fmt.Sprintf(mountHostSrc, "runtime", "2mm", "")
	build := func(set string) (*Product, []diag.Diagnostic) {
		h := strings.Replace(host, "mode = runtime", "mode = runtime\n  set { "+set+" }", 1)
		f := lowerIn(t, h, fstest.MapFS{"gear.gw": {Data: []byte(gear)}}, "")
		p, _, ds := BuildWith(f, "", Options{})
		return p, ds
	}
	p, ds := build("style = tall")
	if p == nil {
		t.Fatal(ds)
	}
	if !strings.Contains(string(p.Outputs[0].Files[0].Doc.Extras), `"file":"tall.glb"`) {
		t.Errorf("the mount did not select tall.glb: %s", p.Outputs[0].Files[0].Doc.Extras)
	}
	h := strings.Replace(host, "mode = runtime", "mode = runtime", 1)
	f := lowerIn(t, h, fstest.MapFS{"gear.gw": {Data: []byte(gear)}}, "")
	_, _, ds = BuildWith(f, "", Options{})
	found := false
	for _, d := range ds {
		if d.Code == diag.CodeMount && strings.Contains(d.Message, "alike") {
			found = true
		}
	}
	if !found {
		t.Errorf("two outputs and no bake value: %v", ds)
	}
}

// Project imports stay inside the project, are pinned by the SHA-256 of their
// bytes, and cannot form a cycle.
func TestProjectImports(t *testing.T) {
	gear := fmt.Sprintf(mountGearSrc, "0.104m", "1.6m")
	host := fmt.Sprintf(mountHostSrc, "runtime", "2mm", "")
	base, _, _ := BuildWith(mountProject(t, gear, host), "", Options{})
	if base == nil {
		t.Fatal("base does not build")
	}
	// A changed gear byte changes the input record and the host's meaning; the
	// same bytes in a second build change nothing.
	again, _, _ := BuildWith(mountProject(t, gear, host), "", Options{})
	if again.InputSHA256 != base.InputSHA256 || again.Outputs[0].Files[0].SHA256 != base.Outputs[0].Files[0].SHA256 {
		t.Error("a repeated build differs")
	}
	moved, _, ds := BuildWith(mountProject(t, strings.Replace(gear, "0.104m", "0.105m", 1), host), "", Options{})
	if moved == nil {
		t.Fatal(ds)
	}
	if moved.InputSHA256 == base.InputSHA256 {
		t.Error("the gear's bytes are not part of the input record")
	}
	// A comment in the gear changes the pin, not the product.
	cm, _, _ := BuildWith(mountProject(t, gear+"// a comment\n", host), "", Options{})
	if cm.InputSHA256 == base.InputSHA256 {
		t.Error("a comment in the gear is invisible to the pin")
	}
	if cm.Outputs[0].Files[0].SHA256 != base.Outputs[0].Files[0].SHA256 {
		t.Error("a comment in the gear changed the host's bytes")
	}

	check := func(name string, files fstest.MapFS, dir, src, code, frag string) {
		t.Helper()
		f, ds := ast.LowerWith(lang.Parse([]byte(src)), ast.Options{Project: &ast.Project{FS: files, Dir: dir}})
		_, _, more := BuildWith(f, "", Options{})
		for _, d := range append(ds, more...) {
			if d.Code == code && strings.Contains(d.Message, frag) {
				return
			}
		}
		t.Errorf("%s: want %s mentioning %q, got %v %v", name, code, frag, ds, more)
	}
	imp := func(path string) string {
		return strings.Replace(host, `import "gear.gw" as gear`, `import "`+path+`" as gear`, 1)
	}
	files := fstest.MapFS{"gear.gw": {Data: []byte(gear)}, "sub/gear.gw": {Data: []byte(gear)}}
	check("missing file", files, "", imp("nothing.gw"), diag.CodeMissing, "cannot import")
	check("parent escape", files, "", imp("../gear.gw"), diag.CodeMissing, "leaves the project")
	check("absolute path", files, "", imp("/etc/gear.gw"), diag.CodeMissing, "relative")
	check("backslash", files, "", imp(`sub\\gear.gw`), diag.CodeMissing, "relative")
	check("not a gw file", files, "", imp("gear.txt"), diag.CodeUnknownName, "neither a `std/` library")
	// A path is relative to the importing document and may climb inside the root.
	if p, _, ds := BuildWith(lowerIn(t, imp("../gear.gw"), files, "sub"), "", Options{}); p == nil {
		t.Errorf("a path that climbs inside the root: %v", ds)
	}
	if p, _, ds := BuildWith(lowerIn(t, imp("sub/gear.gw"), files, ""), "", Options{}); p == nil {
		t.Errorf("a path into a subdirectory: %v", ds)
	}
	// Cycle: a.gw imports b.gw, which imports a.gw.
	cyc := fstest.MapFS{
		"a.gw": {Data: []byte("greenware 1\nimport \"b.gw\" as b\nasset a { root = a }\npart a { sphere s { radius = 1m } }\noutput o { file = \"a.glb\" }\n")},
		"b.gw": {Data: []byte("greenware 1\nimport \"a.gw\" as a\nasset b { root = b }\npart b { sphere s { radius = 1m } }\noutput o { file = \"b.glb\" }\n")},
	}
	check("cycle", cyc, "", string(cyc["a.gw"].Data), diag.CodeCycle, "b.gw -> a.gw -> b.gw")
	// Without a project directory the import is reported, not guessed.
	f, _ := ast.LowerWith(lang.Parse([]byte(host)), ast.Options{})
	_, _, ds = BuildWith(f, "", Options{})
	found := false
	for _, d := range ds {
		if d.Code == diag.CodeUnsupported && strings.Contains(d.Message, "needs a project directory") {
			found = true
		}
	}
	if !found {
		t.Errorf("no project: %v", ds)
	}
}
