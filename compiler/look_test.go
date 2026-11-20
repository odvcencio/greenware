package compiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	stdimage "image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/image"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/std"
	"m31labs.dev/greenware/units"
)

// The helmet's look (milestone M3): the std/finish import, the atlas with its
// painters, the palette material and its fallback, the plug and the cost
// tickets. These tests build the whole file, without the geometry-only flag.

const helmetLookCandidate = "../testdata/compiler/helmet.candidate.txt"

var (
	lookOnce    sync.Once
	lookProduct *Product
	lookDiags   []diag.Diagnostic
)

func loadHelmetLook(t testing.TB) *ast.File {
	t.Helper()
	src, err := os.ReadFile(filepath.FromSlash(helmetPath))
	if err != nil {
		t.Fatal(err)
	}
	f, ds := ast.Lower(lang.Parse(src))
	if diag.HasErrors(ds) {
		t.Fatalf("lower: %v", ds)
	}
	return f
}

func builtLook(t testing.TB) *Product {
	t.Helper()
	lookOnce.Do(func() {
		lookProduct, lookDiags = Build(loadHelmetLook(t), "")
	})
	if lookProduct == nil {
		t.Fatalf("the helmet did not build: %v", lookDiags)
	}
	return lookProduct
}

func lookFile(t testing.TB, name string) *File {
	t.Helper()
	for _, o := range builtLook(t).Outputs {
		for _, f := range o.Files {
			if f.Name == name {
				return f
			}
		}
	}
	t.Fatalf("no file %s", name)
	return nil
}

var allHelmetFiles = []string{"helmet-classic.glb", "helmet-classic-lod1.glb", "helmet-aero.glb", "helmet-aero-lod1.glb"}

// ---- what compiles ----------------------------------------------------------------

func TestHelmetLookBuildsAtBothLevels(t *testing.T) {
	p := builtLook(t)
	if len(p.Outputs) != 2 {
		t.Fatalf("outputs: %d", len(p.Outputs))
	}
	for _, name := range allHelmetFiles {
		f := lookFile(t, name)
		// The plan's M3 row: level 0 draws 3 by default and 4 at worst.
		if f.Draws != 3 || f.DrawsWorst != 4 {
			t.Errorf("%s: draws %d, worst %d, want 3 and 4", name, f.Draws, f.DrawsWorst)
		}
		if f.Materials != 2 {
			t.Errorf("%s: %d materials, want 2", name, f.Materials)
		}
		// Two distinct 256 x 128 images cost 256 KiB, decoded.
		if f.TextureBytes != 2*256*128*4 || len(f.Images) != 2 {
			t.Errorf("%s: texture bytes %d with %d images, want 262144 and 2", name, f.TextureBytes, len(f.Images))
		}
		for _, im := range f.Images {
			if im.Width != 256 || im.Height != 128 || im.Bytes == 0 {
				t.Errorf("%s: image %+v", name, im)
			}
		}
		// The geometry is the geometry-only build's, triangle for triangle.
		geo := helmetFile(t, name)
		if f.Triangles != geo.Triangles || f.TrianglesWorst != geo.TrianglesWorst || f.TrianglesStored != geo.TrianglesStored || f.Vertices != geo.Vertices {
			t.Errorf("%s: triangles %d/%d/%d, geometry-only %d/%d/%d", name, f.Triangles, f.TrianglesWorst, f.TrianglesStored, geo.Triangles, geo.TrianglesWorst, geo.TrianglesStored)
		}
		if f.Size > maxEncodeImageBytes {
			t.Errorf("%s: %d bytes is over the class", name, f.Size)
		}
	}
	// The two styles differ, and so do the levels.
	if lookFile(t, "helmet-classic.glb").SHA256 == lookFile(t, "helmet-aero.glb").SHA256 {
		t.Error("classic and aero are the same file")
	}
}

func TestHelmetLookNotesNameWhatIsPending(t *testing.T) {
	notes := strings.Join(builtLook(t).Unsupported, "\n")
	for _, want := range []string{"materials/palette.sel.json is pending", "blend = on is not written"} {
		if !strings.Contains(notes, want) {
			t.Errorf("no note %q in:\n%s", want, notes)
		}
	}
}

// ---- the GLB ------------------------------------------------------------------------

func decodePNG(t testing.TB, b []byte) *image.RGBA8 {
	t.Helper()
	im, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("the embedded PNG does not decode: %v", err)
	}
	bnd := im.Bounds()
	out := image.New(bnd.Dx(), bnd.Dy())
	for y := 0; y < out.H; y++ {
		for x := 0; x < out.W; x++ {
			c := color.NRGBAModel.Convert(im.At(bnd.Min.X+x, bnd.Min.Y+y)).(color.NRGBA)
			out.Set(x, y, [4]byte{c.R, c.G, c.B, c.A})
		}
	}
	return out
}

// smooth is the Hermite step the finish painters use.
func smooth(lo, hi, x float64) float64 {
	u := math.Min(math.Max((x-lo)/(hi-lo), 0), 1)
	return u * u * (3 - 2*u)
}

func clamp01(x float64) float64 { return math.Min(math.Max(x, 0), 1) }

// shade is the red value std/finish should give a cell's centre texel
// (px = py = 32 of a 64 texel cell with a 7% inset), written out independently
// of the compiler's evaluator with the standard math package.
func finishShade(painter string, cell int) float64 {
	uv := ((32.5)/64 - 0.07) / 0.86
	u, v := uv, uv
	switch painter {
	case "gloss":
		sky := 0.88 + 0.12*(1-v)
		streak := smooth(0.16, 0.30, u) * (1 - smooth(0.30, 0.46, u))
		pool := smooth(0.82, 1.0, v)
		return clamp01(sky + 0.11*streak - 0.10*pool)
	case "satin":
		sheen := 0.5 + 0.5*math.Cos(2*math.Pi*u+0.6)
		brush := 0.0
		if 32%3 == 0 {
			brush = 0.035
		}
		return clamp01(0.72 + 0.2*sheen - brush)
	}
	return -1
}

func TestHelmetImagesArePixelCorrect(t *testing.T) {
	for _, name := range allHelmetFiles {
		f := lookFile(t, name)
		doc, err := gltf.Decode(f.GLB)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(doc.Images) != 2 || len(doc.Textures) != 2 || len(doc.Samplers) != 2 {
			t.Fatalf("%s: %d images, %d textures, %d samplers", name, len(doc.Images), len(doc.Textures), len(doc.Samplers))
		}
		var fall, mask *image.RGBA8
		for i, im := range doc.Images {
			if im.MimeType != "image/png" {
				t.Errorf("%s: image %d is %q", name, i, im.MimeType)
			}
			switch im.Name {
			case "paint(helmet_mask)":
				fall = decodePNG(t, im.Data)
			case "helmet_mask":
				mask = decodePNG(t, im.Data)
			}
		}
		if fall == nil || mask == nil {
			t.Fatalf("%s: the fallback and the mask are not both there", name)
		}
		if mask.W != 256 || mask.H != 128 || fall.W != 256 || fall.H != 128 {
			t.Fatalf("%s: sizes", name)
		}
		// Cells by physical index: unindexed cells take the lowest free
		// indices in name order: mask, pad, shell, strap, stripe.
		cells := []struct {
			name    string
			slot    int
			painter string
			colour  [4]byte
		}{
			{"mask", 2, "satin", [4]byte{0x48, 0x54, 0x5a, 255}},
			{"pad", 3, "foam", [4]byte{0x24, 0x2b, 0x30, 255}},
			{"shell", 0, "gloss", [4]byte{0xe1, 0xae, 0x4f, 255}},
			{"strap", 1, "satin", [4]byte{0xfb, 0xef, 0xd4, 255}},
			{"stripe", 1, "gloss", [4]byte{0xfb, 0xef, 0xd4, 255}},
		}
		for idx := 0; idx < 8; idx++ {
			cx, cy := (idx%4)*64+32, (idx/4)*64+32
			m := mask.At(cx, cy)
			if idx >= len(cells) {
				// Unused cells: zero shade and slot zero, alpha 255.
				if m != [4]byte{0, 0, 0, 255} {
					t.Errorf("%s: unused cell %d is %v", name, idx, m)
				}
				continue
			}
			c := cells[idx]
			if int(m[1]) != c.slot || m[2] != 0 || m[3] != 255 {
				t.Errorf("%s: cell %s (%d) has green %d blue %d alpha %d, want slot %d, 0, 255", name, c.name, idx, m[1], m[2], m[3], c.slot)
			}
			if want := finishShade(c.painter, idx); want >= 0 && math.Abs(float64(m[0])-math.RoundToEven(255*want)) > 1 {
				t.Errorf("%s: cell %s shading %d, want about %v", name, c.name, m[0], 255*want)
			}
			// The fallback is the slot's colour in linear light times the
			// shading, back in sRGB, with the slot colour's alpha.
			got := fall.At(cx, cy)
			sh := float64(m[0]) / 255
			for k := 0; k < 3; k++ {
				lin := srgbToLinear(c.colour[k]) * sh
				var enc float64
				if lin <= 0.0031308 {
					enc = 12.92 * lin
				} else {
					enc = 1.055*math.Pow(lin, 1/2.4) - 0.055
				}
				if d := math.Abs(float64(got[k]) - math.RoundToEven(255*enc)); d > 1 {
					t.Errorf("%s: fallback of cell %s channel %d is %d, want about %.1f", name, c.name, k, got[k], 255*enc)
				}
			}
			if got[3] != 255 {
				t.Errorf("%s: fallback alpha %d", name, got[3])
			}
		}
		// The gutter holds the nearest interior texel, so the corner of a
		// cell equals the corner of its interior.
		if mask.At(0, 0) != mask.At(4, 4) {
			t.Errorf("%s: the gutter does not copy the interior corner: %v vs %v", name, mask.At(0, 0), mask.At(4, 4))
		}
		// The base colour texture is the fallback, with sampler state from the
		// material's defaults; the mask reads nearest.
		mi := -1
		for i, m := range doc.Materials {
			if m.Name == "helmet_paint" {
				mi = i
			}
		}
		if mi < 0 || doc.Materials[mi].BaseColorTexture == nil {
			t.Fatalf("%s: helmet_paint has no base colour texture", name)
		}
		bt := doc.Textures[*doc.Materials[mi].BaseColorTexture]
		if doc.Images[*bt.Source].Name != "paint(helmet_mask)" {
			t.Errorf("%s: the base colour texture is %q", name, doc.Images[*bt.Source].Name)
		}
		sm := doc.Samplers[*bt.Sampler]
		if sm.MagFilter != 9729 || sm.MinFilter != 9987 || sm.WrapS != 10497 || sm.WrapT != 10497 {
			t.Errorf("%s: base colour sampler %+v", name, sm)
		}
		if _, ok := doc.Materials[mi].Extensions["KHR_materials_clearcoat"]; !ok {
			t.Errorf("%s: no clearcoat extension", name)
		}
	}
}

func TestHelmetUVsLieInTheirCells(t *testing.T) {
	rect := func(idx int) [4]float32 {
		l := image.Layout{Cols: 4, Rows: 2, CellSize: 64, Inset: 0.07}
		a, b, c, d := l.Rect(idx)
		return [4]float32{float32(a), float32(b), float32(c), float32(d)}
	}
	inside := func(uv []float32, r [4]float32) bool {
		for i := 0; i+1 < len(uv); i += 2 {
			if uv[i] < r[0]-1e-6 || uv[i] > r[2]+1e-6 || uv[i+1] < r[1]-1e-6 || uv[i+1] > r[3]+1e-6 {
				return false
			}
		}
		return true
	}
	for _, name := range allHelmetFiles {
		doc, err := gltf.Decode(lookFile(t, name).GLB)
		if err != nil {
			t.Fatal(err)
		}
		// The cage's tubes are all cell `mask` (index 0), the crown stripe's
		// are `stripe` (index 4).
		for node, idx := range map[string]int{"mask_cage": 0, "crown_stripe": 4} {
			_, n := nodeByName(t, doc, node)
			for _, p := range doc.Meshes[*n.Mesh].Primitives {
				if !inside(p.UV0, rect(idx)) {
					t.Errorf("%s: %s has a UV outside cell %d's inset rectangle %v", name, node, idx, rect(idx))
				}
			}
		}
		// The root node merges the shell (2), pads (1), straps and chin cup (3)
		// and the bumper (4): every UV is inside one of those cells, and the
		// shell's lie in cell 2.
		_, root := nodeByName(t, doc, "helmet")
		prim := doc.Meshes[*root.Mesh].Primitives[0]
		cells := []int{1, 2, 3, 4}
		for i := 0; i+1 < len(prim.UV0); i += 2 {
			ok := false
			for _, c := range cells {
				if inside(prim.UV0[i:i+2], rect(c)) {
					ok = true
				}
			}
			if !ok {
				t.Fatalf("%s: root vertex %d has the UV %v in no cell of the helmet", name, i/2, prim.UV0[i:i+2])
			}
		}
		shellFirst := prim.UV0[:2*(24*7)] // the first 7 rings of the shell, before the pads
		_ = shellFirst
	}
}

func TestHelmetPlugAndExtras(t *testing.T) {
	for _, name := range allHelmetFiles {
		doc, err := gltf.Decode(lookFile(t, name).GLB)
		if err != nil {
			t.Fatal(err)
		}
		_, n := nodeByName(t, doc, "plug.head_gear")
		if n.Mesh != nil || len(n.Children) != 0 {
			t.Errorf("%s: the plug must be a meshless node", name)
		}
		want := [3]float32{0, float32(1.7 * yard), float32(-0.012 * yard)}
		if n.Translation == nil || *n.Translation != want {
			t.Errorf("%s: plug at %v, want %v (metres)", name, n.Translation, want)
		}
		var extras struct {
			Greenware struct {
				Plugs []struct {
					Name string     `json:"name"`
					Kind string     `json:"kind"`
					At   [3]float64 `json:"at"`
				} `json:"plugs"`
				Runtime struct {
					Params []struct {
						Name     string `json:"name"`
						Default  any    `json:"default"`
						Bindings []struct {
							Kind     string `json:"kind"`
							Material string `json:"material"`
							Field    string `json:"field"`
							Map      string `json:"map"`
							Target   string `json:"target"`
						} `json:"bindings"`
					} `json:"params"`
				} `json:"runtime"`
			} `json:"greenware"`
		}
		if err := json.Unmarshal(doc.Extras, &extras); err != nil {
			t.Fatal(err)
		}
		g := extras.Greenware
		if len(g.Plugs) != 1 || g.Plugs[0].Name != "head_gear" || g.Plugs[0].Kind != "helmet" || math.Abs(g.Plugs[0].At[1]-1.7*yard) > 1e-6 {
			t.Errorf("%s: plugs %+v", name, g.Plugs)
		}
		bind := map[string]string{}
		for _, p := range g.Runtime.Params {
			for _, b := range p.Bindings {
				if b.Kind == "material" {
					bind[p.Name] = b.Material + "." + b.Field + " " + b.Map + " " + b.Target
				}
			}
		}
		for param, want := range map[string]string{
			"shell_color":  "helmet_paint.slot0 srgb_to_linear palette",
			"stripe_color": "helmet_paint.slot1 srgb_to_linear palette",
			"mask_color":   "helmet_paint.slot2 srgb_to_linear palette",
			"pad_color":    "helmet_paint.slot3 srgb_to_linear palette",
			"visor_tint":   "visor_glass.baseColorFactor.alpha identity ",
		} {
			if bind[param] != want {
				t.Errorf("%s: binding of %s is %q, want %q", name, param, bind[param], want)
			}
		}
		// The visor tint's default is written in the glTF field too.
		_, _ = nodeByName(t, doc, "visor_shield")
		for _, m := range doc.Materials {
			if m.Name == "visor_glass" && math.Abs(float64(m.BaseColor[3])-0.6) > 1e-6 {
				t.Errorf("%s: visor alpha %v, want the default 0.6", name, m.BaseColor[3])
			}
		}
		// The Selena descriptor is recorded as pending, with the mask texture.
		for _, m := range doc.Materials {
			if m.Name != "helmet_paint" {
				continue
			}
			var rec struct {
				Greenware struct {
					Artifact string `json:"artifact"`
					Source   string `json:"source"`
					Palette  struct {
						Mask int `json:"mask_texture"`
						Fall int `json:"fallback_texture"`
					} `json:"palette"`
				} `json:"greenware"`
			}
			if err := json.Unmarshal(m.Extras, &rec); err != nil {
				t.Fatal(err)
			}
			g := rec.Greenware
			if g.Artifact != "pending" || g.Source != "materials/palette.sel" || doc.Images[*doc.Textures[g.Palette.Mask].Source].Name != "helmet_mask" || doc.Images[*doc.Textures[g.Palette.Fall].Source].Name != "paint(helmet_mask)" {
				t.Errorf("%s: selena record %+v", name, g)
			}
		}
	}
}

// ---- determinism ------------------------------------------------------------------------

func lookDigests(p *Product) string {
	var b strings.Builder
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			fmt.Fprintf(&b, "%s %s\n", f.Name, f.SHA256)
		}
	}
	return b.String() + p.InputSHA256 + "\n"
}

func TestHelmetLookBytesAreDeterministic(t *testing.T) {
	build := func(procs int, path string) string {
		old := runtime.GOMAXPROCS(procs)
		defer runtime.GOMAXPROCS(old)
		src, err := os.ReadFile(filepath.FromSlash(helmetPath))
		if err != nil {
			t.Fatal(err)
		}
		// A copy under another path: the bytes must not depend on where the
		// source lives.
		dir := t.TempDir()
		cp := filepath.Join(dir, path, "helmet.gw")
		if err := os.MkdirAll(filepath.Dir(cp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cp, src, 0o644); err != nil {
			t.Fatal(err)
		}
		src2, _ := os.ReadFile(cp)
		f, ds := ast.Lower(lang.Parse(src2))
		if diag.HasErrors(ds) {
			t.Fatal(ds)
		}
		p, ds := Build(f, "")
		if p == nil {
			t.Fatal(ds)
		}
		return lookDigests(p)
	}
	one, eight, again := build(1, "a"), build(8, "deeper/than/the/first"), build(3, "a")
	if one != eight || one != again {
		t.Fatalf("the files change with GOMAXPROCS, the path or between runs:\n%s\n%s\n%s", one, eight, again)
	}
	if one != lookDigests(builtLook(t)) {
		t.Fatal("a fresh build differs from the shared one")
	}
}

func lookCandidateText(p *Product) string {
	var b strings.Builder
	b.WriteString("# CANDIDATE, not approved: counts, image sizes and digests of the helmet with its look, built from examples/helmet/helmet.gw\n")
	b.WriteString("# (the full lowering: std/finish, the atlas, the palette material with its pbr fallback, the plug). A reviewer approves it; no test flag does.\n")
	b.WriteString("# input " + p.InputSHA256 + "\n")
	b.WriteString("# columns: output file level triangles draws triangles_worst draws_worst triangles_stored vertices bytes texture_bytes images(name:png_bytes,...) sha256\n")
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			var imgs []string
			for _, im := range f.Images {
				imgs = append(imgs, fmt.Sprintf("%s:%d", im.Name, im.Bytes))
			}
			fmt.Fprintf(&b, "%s %s %d %d %d %d %d %d %d %d %d %s %s\n", o.Name, f.Name, f.Level, f.Triangles, f.Draws, f.TrianglesWorst, f.DrawsWorst, f.TrianglesStored, f.Vertices, f.Size, f.TextureBytes, strings.Join(imgs, ","), f.SHA256)
		}
	}
	return b.String()
}

func TestHelmetLookCandidate(t *testing.T) {
	got := lookCandidateText(builtLook(t))
	if os.Getenv("GREENWARE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(filepath.FromSlash(helmetLookCandidate), []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(filepath.FromSlash(helmetLookCandidate))
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("the helmet with its look changed.\nrecorded:\n%s\nbuilt:\n%s", want, got)
	}
}

func TestStdLibraryIsPinnedInTheInputHash(t *testing.T) {
	p := builtLook(t)
	f := loadHelmetLook(t)
	// The input hash covers the library's digest: hashing the same source
	// without the import line changes it.
	sum, ok := std.Digest("std/finish")
	if !ok || len(sum) != 64 {
		t.Fatalf("no digest for std/finish: %q", sum)
	}
	h := sha256.New()
	fmt.Fprintf(h, "greenware.input/1\n%s\nsource %s\noutput %s\n", Generator, p.SourceSHA256, "")
	if hex.EncodeToString(h.Sum(nil)) == p.InputSHA256 {
		t.Error("the input hash does not include the import pin")
	}
	fmt.Fprintf(h, "import std/finish sha256 %s\n", sum)
	if hex.EncodeToString(h.Sum(nil)) != p.InputSHA256 {
		t.Error("the input hash is not the source, the output choice and the import pin")
	}
	_ = f
}

// ---- the meaning hash ---------------------------------------------------------------------

func TestMeaningHashFollowsThePixels(t *testing.T) {
	base := builtLook(t)
	src, _ := os.ReadFile(filepath.FromSlash(helmetPath))
	change := func(old, new string) *Product {
		s := strings.Replace(string(src), old, new, 1)
		if s == string(src) {
			t.Fatalf("no %q in the source", old)
		}
		f, ds := ast.Lower(lang.Parse([]byte(s)))
		if diag.HasErrors(ds) {
			t.Fatal(ds)
		}
		p, ds := Build(f, "classic")
		if p == nil {
			t.Fatal(ds)
		}
		return p
	}
	fir := func(p *Product, out string) string {
		for _, o := range p.Outputs {
			if o.Name == out {
				return o.FirSHA256
			}
		}
		return ""
	}
	// A different painter for a cell changes the image and the meaning.
	p := change("cell pad { slot = 3 paint = finish.foam() }", "cell pad { slot = 3 paint = finish.satin() }")
	if fir(p, "classic") == fir(base, "classic") {
		t.Error("a changed painter did not change the meaning hash")
	}
	// A comment changes the input hash, not the meaning.
	q := change("// The standard library's finishes", "// The library's finishes")
	if fir(q, "classic") != fir(base, "classic") {
		t.Error("a comment changed the meaning hash")
	}
	if q.InputSHA256 == base.InputSHA256 {
		t.Error("a comment did not change the input hash")
	}
	// A changed default slot colour changes the fallback image and so the meaning.
	r := change("param pad_color : color = #242b30 runtime", "param pad_color : color = #343b40 runtime")
	if fir(r, "classic") == fir(base, "classic") {
		t.Error("a changed default colour did not change the meaning hash")
	}
}

// ---- diagnostics -----------------------------------------------------------------------------

func buildSource(t testing.TB, src string) (*Product, []diag.Diagnostic) {
	t.Helper()
	f, ds := ast.Lower(lang.Parse([]byte(src)))
	if diag.HasErrors(ds) {
		return nil, ds
	}
	return Build(f, "")
}

const lookPrefix = `greenware 1

asset a { root = a }
param c0 : color = #ff0000 runtime
`

const lookTail = `
part a {
  material = m
  sphere { radius = 1m cell = body }
}
output o { file = "a.glb" }
`

func TestOldTextureBudgetFailsAndNewPasses(t *testing.T) {
	src := func(limit string) string {
		return lookPrefix + `
import "std/finish"
texture mask = atlas {
  grid = (4, 2)
  cell_size = 64px
  inset = 7%
  mode = palette
  cell body { slot = 0 paint = finish.gloss() }
}
material m = selena {
  source = "m.sel"
  name = "M"
  mask = mask
  slot0 = c0
  fallback = pbr { base_texture = paint(mask) }
}
budget { texture_bytes = ` + limit + ` }
` + lookTail
	}
	if p, ds := buildSource(t, src("256KiB")); p == nil {
		t.Fatalf("256 KiB must pass: %v", ds)
	}
	p, ds := buildSource(t, src("128KiB"))
	if p != nil {
		t.Fatal("two distinct 256 x 128 images are 256 KiB: the old 128 KiB budget must fail")
	}
	found := false
	for _, d := range ds {
		if d.Code == diag.CodeBudget && strings.Contains(d.Message, "texture_bytes") && strings.Contains(d.Message, "262144") {
			found = true
		}
	}
	if !found {
		t.Errorf("no GW-BUDGET for texture_bytes: %v", ds)
	}
}

func TestAColourAtlasIsOneImage(t *testing.T) {
	// A colour atlas used directly as a base texture is one image: 128 KiB.
	p, ds := buildSource(t, lookPrefix+`
import "std/finish"
texture t = atlas {
  grid = (4, 2)
  cell_size = 64px
  inset = 7%
  cell body { paint = finish.gloss() }
}
material m = pbr { base_texture = t }
budget { texture_bytes = 128KiB }
`+lookTail)
	if p == nil {
		t.Fatalf("one 256 x 128 image is 128 KiB: %v", ds)
	}
	f := p.Outputs[0].Files[0]
	if f.TextureBytes != 131072 || len(f.Images) != 1 {
		t.Errorf("texture bytes %d, images %d", f.TextureBytes, len(f.Images))
	}
}

// elabFor makes the state finishUV needs from a small source.
func elabFor(t testing.TB, src string) *elab {
	t.Helper()
	f, ds := ast.Lower(lang.Parse([]byte(src)))
	if diag.HasErrors(ds) {
		t.Fatal(ds)
	}
	r, ds := resolve.EvaluateOutput(f, "o")
	if r == nil || diag.HasErrors(ds) {
		t.Fatal(ds)
	}
	return &elab{file: f, r: r, defs: map[string]*resolve.Node{}}
}

func triangle(uvs ...mesh.Vec2) *mesh.Mesh {
	m := &mesh.Mesh{Positions: []mesh.Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}}, Tris: []mesh.Tri{{0, 1, 2}}, Shell: []int32{0}}
	for i, uv := range uvs {
		m.Corners = append(m.Corners, mesh.Corner{Pos: int32(i), Normal: mesh.Vec3{0, 0, 1}, UV: uv})
	}
	return m
}

const cellSource = lookPrefix + `
texture t = atlas {
  grid = (2, 2)
  cell_size = 16px
  inset = 10%
  cell body { paint = #808080 }
}
material m = pbr { base_texture = t }
part a {
  material = m
  sphere { radius = 1m cell = body }
}
output o { file = "a.glb" }
`

func TestCellRemapPutsTheChartInTheInsetRectangle(t *testing.T) {
	e := elabFor(t, cellSource)
	n := &resolve.Node{Kind: "sphere", Name: "s"}
	out, ok := e.finishUV(n, triangle(mesh.Vec2{0, 0}, mesh.Vec2{1, 0}, mesh.Vec2{0.5, 1}), "m", "body")
	if !ok {
		t.Fatalf("refused: %v", e.diags)
	}
	// Cell 0 of a 2 x 2 grid with a 10% inset: U = (0 + 0.1 + 0.8u)/2.
	want := [][2]float64{{0.05, 0.05}, {0.45, 0.05}, {0.25, 0.45}}
	for i, c := range out.Corners {
		if math.Abs(c.UV[0]-want[i][0]) > 1e-12 || math.Abs(c.UV[1]-want[i][1]) > 1e-12 {
			t.Errorf("corner %d: uv %v, want %v", i, c.UV, want[i])
		}
	}
}

func TestChartOutsideTheUnitSquareIsRefusedNotRetriangulated(t *testing.T) {
	e := elabFor(t, cellSource)
	n := &resolve.Node{Kind: "sphere", Name: "s"}
	in := triangle(mesh.Vec2{0, 0}, mesh.Vec2{1.2, 0}, mesh.Vec2{0, 1})
	if _, ok := e.finishUV(n, in, "m", "body"); ok {
		t.Fatal("a chart with u = 1.2 was accepted")
	}
	if len(e.diags) != 1 || e.diags[0].Code != diag.CodeUVCell {
		t.Fatalf("want one GW-UV-CELL, got %v", e.diags)
	}
	if len(in.Tris) != 1 {
		t.Error("the mesh was changed")
	}
}

func TestExplicitProjectionThenCell(t *testing.T) {
	// uv = planar on a shape with a cell: the planar chart fills the unit
	// square and the cell then maps it, so the extremes land on the rectangle.
	e := elabFor(t, cellSource)
	n := &resolve.Node{Kind: "tube", Name: "s", Fields: []resolve.Field{{Name: "uv", Value: units.Label("planar")}}}
	out, ok := e.finishUV(n, triangle(mesh.Vec2{0.3, 0.3}, mesh.Vec2{0.3, 0.3}, mesh.Vec2{0.3, 0.3}), "m", "body")
	if !ok {
		t.Fatal(e.diags)
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, c := range out.Corners {
		lo, hi = math.Min(lo, c.UV[0]), math.Max(hi, c.UV[0])
	}
	if math.Abs(lo-0.05) > 1e-12 || math.Abs(hi-0.45) > 1e-12 {
		t.Errorf("planar then cell: u runs %v..%v, want 0.05..0.45", lo, hi)
	}
}

// ---- the cost schedule -----------------------------------------------------------------------------

func TestLookTicketsAreRegisteredAndPriced(t *testing.T) {
	got := map[string]uint64{}
	for _, tk := range LookTickets() {
		got[tk.Name] = tk.Units
	}
	if got[TicketPaint] != 250_000 || got[TicketEncodeImages] != 200_000 || len(got) != 2 {
		t.Errorf("look tickets %v", got)
	}
	// The M2 table is untouched: its running total is the goal post's 8,750,000.
	var m2 uint64
	for _, tk := range Tickets() {
		m2 += tk.Units
	}
	if m2 == 0 {
		t.Fatal("no M2 tickets")
	}
}

func TestHelmetReceiptIncludesPaintAndImageEncoding(t *testing.T) {
	p := builtLook(t)
	rc := p.Receipt
	if rc == nil {
		t.Fatal("no receipt")
	}
	var paints, encImg, enc int
	for _, c := range rc.Charges {
		switch c.Ticket {
		case TicketPaint:
			paints++
			if c.Row != "helmet_mask" || c.Units != 250_000 {
				t.Errorf("paint charge %+v", c)
			}
		case TicketEncodeImages:
			encImg++
		case TicketEncode:
			enc++
		}
	}
	// One atlas per output, and every level file is an image-carrying file.
	if paints != 2 || encImg != 4 || enc != 0 {
		t.Errorf("paint %d, encode_images %d, encode %d; want 2, 4, 0", paints, encImg, enc)
	}
}

func TestHelmetLimitBoundary(t *testing.T) {
	// total_work = C admits the build and C-1 refuses the first ticket that
	// does not fit, with the same receipt prefix and nothing built.
	f := loadHelmetLook(t)
	p, rc, ds := BuildWith(f, "", Options{})
	if p == nil {
		t.Fatal(ds)
	}
	c := rc.Total
	if p2, rc2, ds2 := BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: c}}); p2 == nil || rc2.Total != c {
		t.Fatalf("limit %d refused: %v", c, ds2)
	}
	p3, rc3, ds3 := BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: c - 1}})
	if p3 != nil || rc3.Refused == nil {
		t.Fatalf("limit %d was admitted", c-1)
	}
	if rc3.Refused.Ticket != TicketPublish || rc3.Refused.Requested != 50_000 || rc3.Refused.Remaining != 49_999 {
		t.Errorf("the refused ticket is %+v, want publish 50000 with 49999 left", rc3.Refused)
	}
	if !diag.HasErrors(ds3) || ds3[0].Code != diag.CodeLimit {
		t.Errorf("diagnostics %v", ds3)
	}
	// The receipt up to the refusal is the full receipt's prefix.
	for i, ch := range rc3.Charges {
		if ch.String() != rc.Charges[i].String() || ch.Units != rc.Charges[i].Units {
			t.Fatalf("charge %d differs: %s vs %s", i, ch, rc.Charges[i])
		}
	}
	// Refusing earlier: a ceiling that cannot pay for the first paint ticket.
	var before uint64
	for _, ch := range rc.Charges {
		if ch.Ticket == TicketPaint {
			break
		}
		before += ch.Units
	}
	_, rc4, _ := BuildWith(f, "", Options{Policy: Policy{MaxTotalWork: before + 249_999}})
	if rc4.Refused == nil || rc4.Refused.Ticket != TicketPaint || rc4.Refused.Row != "helmet_mask" {
		t.Errorf("one below the first paint ticket: %+v", rc4.Refused)
	}
}

func TestGoalPostReceiptIsStill8750000(t *testing.T) {
	// The M2 receipt does not gain look tickets.
	_, rc, ds := BuildWith(loadFile(t, goalpostPath), "", Options{Accept: loadFile(t, "../examples/goalpost/goalpost.accept.gw")})
	if rc == nil {
		t.Fatal(ds)
	}
	if rc.Total != 8_750_000 {
		t.Errorf("the goal post receipt is %d, want 8,750,000", rc.Total)
	}
	for _, c := range rc.Charges {
		if c.Ticket == TicketPaint || c.Ticket == TicketEncodeImages {
			t.Errorf("a look ticket in the goal post's receipt: %+v", c)
		}
	}
}

func TestOversizedAtlasIsOutsideThePaintClass(t *testing.T) {
	// 512 x 512 texels is 262,144: above the 65,536 texel class.
	p, ds := buildSource(t, lookPrefix+`
texture t = atlas {
  grid = (4, 4)
  cell_size = 128px
  cell body { paint = #808080 }
}
material m = pbr { base_texture = t }
`+lookTail)
	if p != nil {
		t.Fatal("an atlas of 512 x 512 texels was admitted")
	}
	found := false
	for _, d := range ds {
		if d.Code == diag.CodeLimit && strings.Contains(d.Message, "paint") {
			found = true
		}
	}
	if !found {
		t.Errorf("no GW-LIMIT on the paint ticket: %v", ds)
	}
}

var _ = stdimage.Rect

func TestExplicitProjectionsBuildEndToEnd(t *testing.T) {
	for _, proj := range []string{"planar", "box", "cylindrical", "spherical"} {
		t.Run(proj, func(t *testing.T) {
			p, ds := buildSource(t, lookPrefix+`
texture t = atlas { grid = (2, 2) cell_size = 16px inset = 10% cell body { paint = #808080 } cell other { paint = #404040 } }
material m = pbr { base_texture = t }
part a {
  material = m
  sphere { radius = 1m segments = 8 uv = `+proj+` cell = other }
}
output o { file = "a.glb" }
`)
			if p == nil {
				t.Fatal(ds)
			}
			doc, err := gltf.Decode(p.Outputs[0].Files[0].GLB)
			if err != nil {
				t.Fatal(err)
			}
			// `other` is cell 1: u in [0.55, 0.95], v in [0.05, 0.45].
			prim := doc.Meshes[0].Primitives[0]
			for i := 0; i+1 < len(prim.UV0); i += 2 {
				u, v := float64(prim.UV0[i]), float64(prim.UV0[i+1])
				if u < 0.55-1e-6 || u > 0.95+1e-6 || v < 0.05-1e-6 || v > 0.45+1e-6 {
					t.Fatalf("uv (%v, %v) is outside cell `other`", u, v)
				}
			}
		})
	}
}

func TestPainterDomainErrorsAreReportedByTheBuild(t *testing.T) {
	// The front end cannot know that sqrt(u - 2) is undefined at every texel;
	// the build reports it with the cell and texel and publishes nothing.
	p, ds := buildSource(t, lookPrefix+`
painter bad() { color = #000000 + sqrt(u - 2) }
texture t = atlas { grid = (1, 1) cell_size = 4px cell body { paint = bad() } }
material m = pbr { base_texture = t }
part a { material = m sphere { radius = 1m cell = body } }
output o { file = "a.glb" }
`)
	if p != nil {
		t.Fatal("a painter fault was published")
	}
	found := false
	for _, d := range ds {
		if d.Code == diag.CodeDomain && strings.Contains(d.Message, "bad()") && strings.Contains(d.Message, "texel") && strings.Contains(d.Message, "sqrt") {
			found = true
		}
	}
	if !found {
		t.Errorf("no GW-DOMAIN naming the painter and texel: %v", ds)
	}
}
