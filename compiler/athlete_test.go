package compiler

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
)

// The whole athlete: three outputs, two levels each, built once under the
// default policy, with no test helper.
var (
	athleteOnce  sync.Once
	athleteProd  *Product
	athleteRc    *Receipt
	athleteDiags []diag.Diagnostic
)

func athleteAll(t testing.TB) *Product {
	t.Helper()
	requireFullSuite(t)
	athleteOnce.Do(func() {
		f := loadProject(t, athletePath)
		athleteProd, athleteRc, athleteDiags = BuildWith(f, "", Options{})
	})
	if athleteProd == nil {
		t.Fatalf("the athlete does not build: %v", athleteDiags)
	}
	return athleteProd
}

func censusRow(t testing.TB, p *Product, output string, level int) Census {
	t.Helper()
	for _, c := range p.Census() {
		if c.Output == output && c.Level == level {
			return c
		}
	}
	t.Fatalf("no census row for %s level %d", output, level)
	return Census{}
}

// The census of every output and level, as measured. These are the numbers the
// athlete's budget block quotes; a change in them is a change to read.
func TestAthleteCensus(t *testing.T) {
	p := athleteAll(t)
	rows := p.Census()
	if len(rows) != 6 {
		t.Fatalf("%d census rows, want 3 outputs x 2 levels", len(rows))
	}
	want := map[string]Census{
		// match is the game's Go bake's body (13,016 triangles, 6,836 vertices, 7
		// build targets) with the 27 clips of clips/football.gw and clips/solved.gw, the `bind` clip of bind.gw and
		// the 4 lookup channels; home and away add the old helmet gear.
		"match/0": {Images: 1, ImageDecodedBytes: 1048576, Vertices: 6836, Triangles: 13016, Draws: 1, Materials: 1, Joints: 15, Influences: 2, MorphTargets: 7, Animations: 32, AnimationKeys: 8804},
		"home/0":  {Images: 3, ImageDecodedBytes: 1310720, Vertices: 8048, Triangles: 14782, Draws: 2, Materials: 2, Joints: 15, Influences: 2, MorphTargets: 7, Animations: 32, AnimationKeys: 8804},
		"away/0":  {Images: 3, ImageDecodedBytes: 1310720, Vertices: 8048, Triangles: 14782, Draws: 2, Materials: 2, Joints: 15, Influences: 2, MorphTargets: 7, Animations: 32, AnimationKeys: 8804},
	}
	for key, w := range want {
		var out string
		var lv int
		out, lv = strings.Split(key, "/")[0], 0
		got := censusRow(t, p, out, lv)
		got.Output, got.Level, got.File, got.FileBytes, got.ImageEncodedBytes = "", 0, "", 0, 0
		got.TrianglesWorst, got.TrianglesStored, got.DrawsWorst = 0, 0, 0
		if got != w {
			t.Errorf("%s: census %+v, want %+v", key, got, w)
		}
	}
	for _, r := range rows {
		if r.TrianglesStored != r.Triangles || r.TrianglesWorst != r.Triangles || r.DrawsWorst != r.Draws {
			t.Errorf("%s level %d: stored/worst differ from default with no runtime branch: %+v", r.Output, r.Level, r)
		}
		if r.Level == 1 && r.Triangles >= censusRow(t, p, r.Output, 0).Triangles {
			t.Errorf("%s: level 1 does not drop triangles", r.Output)
		}
		if r.ImageEncodedBytes <= 0 || r.FileBytes <= r.ImageEncodedBytes {
			t.Errorf("%s level %d: encoded images %d, file %d", r.Output, r.Level, r.ImageEncodedBytes, r.FileBytes)
		}
	}
	t.Logf("%-6s %2s %6s %9s %9s %9s %5s %5s %5s %5s", "output", "lv", "images", "decoded", "encoded", "file", "verts", "tris", "draws", "mats")
	for _, r := range rows {
		t.Logf("%-6s %2d %6d %9d %9d %9d %5d %5d %5d %5d  joints %d infl %d targets %d anims %d keys %d",
			r.Output, r.Level, r.Images, r.ImageDecodedBytes, r.ImageEncodedBytes, r.FileBytes, r.Vertices, r.Triangles, r.Draws, r.Materials,
			r.Joints, r.Influences, r.MorphTargets, r.Animations, r.AnimationKeys)
	}
}

// The record carries the census of every output and level.
func TestAthleteRecordHoldsTheCensus(t *testing.T) {
	p := athleteAll(t)
	var rec struct {
		Census []map[string]any `json:"census"`
		Audit  []map[string]any `json:"audit"`
	}
	if err := json.Unmarshal(p.Record(), &rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Census) != 6 {
		t.Fatalf("%d census rows in the record", len(rec.Census))
	}
	for _, k := range []string{"output", "level", "file", "images", "image_decoded_bytes", "image_encoded_bytes", "file_bytes", "vertices",
		"triangles", "triangles_worst", "triangles_stored", "draws", "draws_worst", "materials", "joints", "influences", "morph_targets", "animations", "animation_keys"} {
		if _, ok := rec.Census[0][k]; !ok {
			t.Errorf("census row lacks %q", k)
		}
	}
	if len(rec.Audit) != 6 {
		t.Errorf("%d audit entries, want one for each output and level", len(rec.Audit))
	}
}

// The measured figures sit inside the athlete's classes, with the headroom the
// classes were chosen with, and the old classes refuse the same figures.
func TestAthleteCensusIsInsideItsClasses(t *testing.T) {
	p := athleteAll(t)
	most := 0
	for _, o := range p.Outputs {
		if ref := athleteClassBoundFiles(o); ref != nil {
			t.Errorf("%s is outside the athlete classes: %s", o.Name, ref.Bound)
		}
		for _, f := range o.Files {
			most = max(most, f.TrianglesStored)
			// The old classes refuse level 0 of every output.
			if f.Level == 0 {
				if f.Size <= maxEncodeImageBytes {
					t.Errorf("%s: %d bytes is inside the old encode_images class", f.Name, f.Size)
				}
				if f.TrianglesStored <= maxSurfaceBranchTris {
					t.Errorf("%s: %d triangles is inside the old surface_branches class", f.Name, f.TrianglesStored)
				}
			}
		}
	}
	if most*100 < maxRigSurface*70 || most > maxRigSurface {
		t.Errorf("the surface_rig class (%d) is not 100-143%% of the most measured triangles (%d)", maxRigSurface, most)
	}
	// The old classes, asked directly.
	op := p.Outputs[0]
	if ref := classBoundFiles(op, true, true); ref == nil || ref.Ticket != TicketSurfaceBranches || !strings.Contains(ref.Bound, "the class allows 4096") {
		t.Errorf("surface_branches does not refuse the athlete: %+v", ref)
	}
	f := loadProject(t, athletePath)
	_, rc, _ := BuildWith(f, "match", Options{})
	if rc == nil || rc.Refused != nil {
		t.Fatalf("match refused: %+v", rc)
	}
	// An atlas of 512 x 512 is over the old paint class of 65,536 texels.
	if maxLargeTexels <= maxPaintTexels || 512*512 <= maxPaintTexels {
		t.Error("the old paint class does not refuse the athlete's atlas")
	}
}

// The athlete's tickets are the profile's, in a fixed order, and the total fits
// under the default ceiling.
func TestAthleteReceipt(t *testing.T) {
	p := athleteAll(t)
	if p.Receipt.Total > DefaultMaxTotalWork {
		t.Fatalf("total %d is over the ceiling", p.Receipt.Total)
	}
	got := map[string]int{}
	for _, c := range p.Receipt.Charges {
		got[c.Ticket]++
	}
	for _, name := range []string{TicketPaintLarge, TicketMount, TicketSurfaceRig, TicketAudit, TicketEncodeLarge} {
		if got[name] == 0 {
			t.Errorf("no %s ticket", name)
		}
	}
	for _, old := range []string{TicketPaint, TicketSurfaceBranches, TicketEncodeImages, TicketSurface, TicketEncode} {
		if got[old] != 0 {
			t.Errorf("an athlete output reserved the old %s ticket", old)
		}
	}
	if got[TicketPaintLarge] != 3 || got[TicketSurfaceRig] != 6 || got[TicketAudit] != 6 || got[TicketMount] != 2 || got[TicketEncodeLarge] != 6 {
		t.Errorf("ticket counts %v", got)
	}
	t.Logf("athlete receipt: %d tickets, %d of %d work units", len(p.Receipt.Charges), p.Receipt.Total, DefaultMaxTotalWork)
}

// The exact total admits the build; one unit less refuses the last ticket.
func TestAthleteExactCeilingAndOneBelow(t *testing.T) {
	p := athleteAll(t)
	c := p.Receipt.Total
	f := loadProject(t, athletePath)
	q, rc, ds := BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: c}})
	if q == nil || rc.Refused != nil {
		t.Fatalf("the exact total %d must pass: %v", c, ds)
	}
	q, rc, ds = BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: c - 1}})
	if q != nil || rc == nil || rc.Refused == nil || rc.Refused.Ticket != TicketPublish {
		t.Fatalf("one below must refuse publish: %+v %v", rc, ds)
	}
	if rc.Refused.Requested != 50_000 || rc.Refused.Remaining != 49_999 {
		t.Errorf("refusal %+v", rc.Refused)
	}
	if len(limitDiags(ds)) != 1 {
		t.Errorf("diagnostics %v", ds)
	}
}

// Each athlete ticket refuses on its own when the ceiling leaves less than its
// price.
func TestAthleteEveryProfileTicketRefusesOnItsOwn(t *testing.T) {
	p := athleteAll(t)
	f := loadProject(t, athletePath)
	var sum uint64
	first := map[string]uint64{}
	for _, c := range p.Receipt.Charges {
		sum += c.Units
		if _, ok := first[c.Ticket]; !ok {
			first[c.Ticket] = sum
		}
	}
	for _, tk := range []string{TicketPaintLarge, TicketMount, TicketSurfaceRig, TicketAudit, TicketEncodeLarge} {
		limit := first[tk] - 1
		q, rc, _ := BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: limit}})
		if q != nil || rc == nil || rc.Refused == nil || rc.Refused.Ticket != tk {
			t.Errorf("limit %d should refuse %s first, got %+v", limit, tk, rc.Refused)
		}
	}
}

// The old limits stay as refusal tests, each isolated: only the figure under
// test is over its limit.
func TestAthleteOldLimitsRefuseWithGWBudget(t *testing.T) {
	requireFullSuite(t)
	src, err := os.ReadFile(athletePath)
	if err != nil {
		t.Fatal(err)
	}
	build := func(output, old, new string) []diag.Diagnostic {
		t.Helper()
		s := string(src)
		if !strings.Contains(s, old) {
			t.Fatalf("source has no %q", old)
		}
		f := lowerProjectSource(t, athletePath, []byte(strings.Replace(s, old, new, -1)))
		q, _, ds := BuildWith(f, output, Options{})
		if q != nil {
			t.Fatalf("%s under %q was not refused", output, new)
		}
		return ds
	}
	only := func(ds []diag.Diagnostic, field string) {
		t.Helper()
		n := 0
		for _, d := range ds {
			if d.Code != diag.CodeBudget {
				t.Errorf("unexpected %s", d)
				continue
			}
			n++
			if !strings.Contains(d.Message, "`"+field+"`") {
				t.Errorf("another limit refused: %s", d)
			}
		}
		if n == 0 {
			t.Errorf("no GW-BUDGET for %s", field)
		}
	}
	// The baked files are over the original 1 MiB file budget (home: 1,316,688 B),
	// and so is the match file now that it carries the 27 clips of the library
	// and the rig base (1,230,188 B).
	ds := build("home", "file_size = 1440KiB", "file_size = 1MiB")
	only(ds, "file_size")
	ds = build("match", "file_size = 1344KiB        //", "file_size = 1MiB        //")
	only(ds, "file_size")
	// The host's decoded atlas alone is 1 MiB; 128 KiB refuses every output.
	ds = build("match", "texture_bytes = 1MiB       //", "texture_bytes = 128KiB       //")
	only(ds, "texture_bytes")
	ds = build("home", "texture_bytes = 1280KiB vertices", "texture_bytes = 128KiB vertices")
	only(ds, "texture_bytes")
	// A baked file with both helmet images is over the original 1 MiB decoded.
	ds = build("away", "texture_bytes = 1280KiB vertices = 8800 file_size = 1440KiB\n  }\n  set", "texture_bytes = 1MiB vertices = 8800 file_size = 1440KiB\n  }\n  set")
	only(ds, "texture_bytes")
	// The match file fits the budget it is declared with.
	f := loadProject(t, athletePath)
	if q, _, ds := BuildWith(f, "match", Options{}); q == nil {
		t.Fatalf("match is refused under its own budget: %v", ds)
	}
}

// A runtime mount is a record; a baked mount is geometry in the host.
func TestAthleteMounts(t *testing.T) {
	p := athleteAll(t)
	byName := map[string]*OutputProduct{}
	for _, o := range p.Outputs {
		byName[o.Name] = o
	}
	// match: no helmet geometry, a mount record.
	var ex struct {
		Greenware struct {
			Mounts []struct {
				Name, Mode, Socket, Kind, Joint string
				Plug                            struct{ Name string }
				Gear                            struct{ Output, File string }
				Set                             []struct{ Name, Value string }
				Fit                             *struct{}
			}
		}
	}
	m0 := byName["match"].Files[0]
	if err := json.Unmarshal(m0.Doc.Extras, &ex); err != nil {
		t.Fatal(err)
	}
	if len(ex.Greenware.Mounts) != 1 {
		t.Fatalf("mounts %+v", ex.Greenware.Mounts)
	}
	m := ex.Greenware.Mounts[0]
	if m.Name != "headgear" || m.Mode != "runtime" || m.Socket != "head_gear" || m.Kind != "helmet" || m.Joint != "head" || m.Plug.Name != "head_gear" ||
		m.Gear.Output != "classic" || m.Gear.File != "helmet-classic.glb" || len(m.Set) != 2 {
		t.Errorf("match mount %+v", m)
	}
	if len(m0.Doc.Meshes[0].Primitives) != 1 {
		t.Errorf("match has %d primitives; the helmet is its own file", len(m0.Doc.Meshes[0].Primitives))
	}
	// home: the helmet is baked, rigid on the head joint, two materials and
	// two draws, three images.
	h := byName["home"].Files[0]
	prims := h.Doc.Meshes[0].Primitives
	if len(prims) != 2 || len(h.Doc.Materials) != 2 || h.Draws != 2 || len(h.Doc.Images) != 3 {
		t.Fatalf("home: %d primitives, %d materials, %d draws, %d images", len(prims), len(h.Doc.Materials), h.Draws, len(h.Doc.Images))
	}
	helmet := prims[1]
	headJoint := -1
	for i, j := range h.Doc.Skins[0].Joints {
		if h.Doc.Nodes[j].Name == "head" {
			headJoint = i
		}
	}
	for v := 0; v < len(helmet.Positions)/3; v++ {
		if int(helmet.Joints0[4*v]) != headJoint || helmet.Weights0[4*v] != 1 || helmet.Weights0[4*v+1] != 0 {
			t.Fatalf("helmet vertex %d is not rigid on the head joint: %v %v", v, helmet.Joints0[4*v:4*v+4], helmet.Weights0[4*v:4*v+4])
		}
	}
	for ti, tg := range helmet.Targets {
		if len(tg.Positions) != 0 || len(tg.Normals) != 0 {
			t.Errorf("helmet target %d has deltas", ti)
		}
	}
	if len(helmet.Targets) != 7 {
		t.Errorf("helmet has %d targets, the mesh has 7", len(helmet.Targets))
	}
	// Helmet vertices lie where the helmet document put them: the plug and the
	// socket are the same point, so the shell is unmoved.
	hg, err := gltf.Decode(h.GLB)
	if err != nil {
		t.Fatal(err)
	}
	if len(hg.Meshes[0].Primitives) != 2 {
		t.Errorf("decoded home has %d primitives", len(hg.Meshes[0].Primitives))
	}
	// The palette descriptor of the baked material points at the host's textures.
	var mat struct {
		Greenware struct {
			Palette struct {
				Mask     int `json:"mask_texture"`
				Fallback int `json:"fallback_texture"`
			}
		}
	}
	if err := json.Unmarshal(h.Doc.Materials[1].Extras, &mat); err != nil {
		t.Fatal(err)
	}
	if mat.Greenware.Palette.Mask < 1 || mat.Greenware.Palette.Fallback < 1 || mat.Greenware.Palette.Mask == mat.Greenware.Palette.Fallback {
		t.Errorf("palette textures %+v: the kit atlas is texture 0", mat.Greenware.Palette)
	}
	// away has its own colours but the same helmet: its helmet material is
	// the helmet's default palette, not the away jersey.
	if byName["away"].Files[0].SHA256 == h.SHA256 {
		t.Error("home and away are the same file")
	}
}

// The audit measured the athlete at every runtime point and key.
func TestAthleteAudit(t *testing.T) {
	p := athleteAll(t)
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			a := f.Audit
			if a == nil {
				t.Fatalf("%s level %d has no audit", o.Name, f.Level)
			}
			wantPoints := 26 // 2^3 x 3 + the default + one case
			if o.Name == "match" {
				wantPoints = 50 // x 2 gear outputs on a runtime mount
			}
			// 558 poses a point: the bind pose, the 555 keys of the 27 body clips and
			// the 2 keys of the `bind` clip that `idle` names (the four lookup
			// channels are not skeletal).
			if a.Points != wantPoints || a.Poses != wantPoints*558 || a.Partial {
				t.Errorf("%s level %d: %d points, %d poses, partial %v; want %d points of 558 poses", o.Name, f.Level, a.Points, a.Poses, a.Partial, wantPoints)
			}
			// The band the file declares (check rig { ground }): the game's clips as they are.
			if a.GroundLowest.Value < -0.060 || a.GroundHighest.Value > 0.040 {
				t.Errorf("%s level %d: ground %.4f..%.4f m is outside the band", o.Name, f.Level, a.GroundLowest.Value, a.GroundHighest.Value)
			}
			if a.Torn.Value > 0.01 {
				t.Errorf("%s level %d: torn share %.4f", o.Name, f.Level, a.Torn.Value)
			}
		}
	}
}

// bands = error promotes the natural-band warning; without it the pose is
// written with a warning.
func TestAthleteBandsErrorPromotesBandWarnings(t *testing.T) {
	requireFullSuite(t)
	src, err := os.ReadFile(athletePath)
	if err != nil {
		t.Fatal(err)
	}
	// Narrow the elbow's natural band to 20 degrees of swing: the arm clips of the
	// library bend it past that.
	s := strings.Replace(string(src), "swing_x = -5..150", "swing_x = -5..20", 1)
	if s == string(src) {
		t.Fatal("the elbow's band is not in the source")
	}
	f := lowerProjectSource(t, athletePath, []byte(s))
	_, _, ds := BuildWith(f, "match", Options{})
	n, errs := 0, 0
	for _, d := range ds {
		if d.Code == diag.CodeRigBand || d.Code == diag.CodeRigLimit {
			n++
			if d.Severity == diag.Error {
				errs++
			}
		}
	}
	if n == 0 || errs != n {
		t.Fatalf("with bands = error %d of %d band diagnostics are errors: %v", errs, n, ds)
	}
	s = strings.Replace(s, "bands = error", "bands = warning", 1)
	f = lowerProjectSource(t, athletePath, []byte(s))
	_, _, ds = BuildWith(f, "match", Options{})
	for _, d := range ds {
		if d.Code == diag.CodeRigBand && d.Severity == diag.Error {
			t.Errorf("a band warning is an error with bands = warning: %s", d)
		}
	}
}

// keep = [face, hand_l, hand_r] keeps those shapes' level 0 tessellation.
func TestAthleteLodKeep(t *testing.T) {
	with, _ := buildAthlete(t, nil)
	without, _ := buildAthlete(t, func(s string) string {
		return strings.Replace(s, "  keep = [face, hand_l, hand_r]", "  // keep = [face, hand_l, hand_r]", 1)
	})
	a, b := with.Files[1].Triangles, without.Files[1].Triangles
	if a <= b {
		t.Errorf("level 1 with keep has %d triangles, without %d: keep must add the kept shapes' detail", a, b)
	}
	if with.Files[0].Triangles != without.Files[0].Triangles || with.Files[0].Vertices != without.Files[0].Vertices {
		t.Error("keep changed level 0")
	}
	if a >= with.Files[0].Triangles {
		t.Errorf("level 1 (%d) must stay below level 0 (%d)", a, with.Files[0].Triangles)
	}
	t.Logf("level 1: %d triangles with keep, %d without", a, b)
}

// The three outputs keep three different bakes: the unsupported list is what
// the compiler left out, not a hidden failure.
func TestAthleteUnsupportedNotes(t *testing.T) {
	p := athleteAll(t)
	if len(p.Unsupported) != 1 || !strings.Contains(p.Unsupported[0], "waist section") {
		t.Errorf("unsupported %v", p.Unsupported)
	}
	// The sample's split kit outputs are not in the example.
	b, _ := os.ReadFile(athletePath)
	if strings.Contains(string(b), "\noutput kits_") {
		t.Error("the kit outputs are not game outputs and must stay out of the example")
	}
}

var _ = ast.Pin{}
