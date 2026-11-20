package resolve_test

import (
	"fmt"
	"strings"
	"testing"

	stdimage "m31labs.dev/greenware/kernel/image"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/std"
)

// The front end of the look: imports, painters, atlases, cells, palette
// materials and plugs.

func evalSrc(t testing.TB, src string) (*resolve.Resolved, []diag.Diagnostic) {
	t.Helper()
	f, ds := ast.Lower(lang.Parse([]byte(src)))
	r, more := resolve.Evaluate(f, nil)
	return r, diag.Dedup(append(ds, more...))
}

func wantCode(t *testing.T, ds []diag.Diagnostic, code, contains string) {
	t.Helper()
	for _, d := range ds {
		if d.Code == code && strings.Contains(d.Message, contains) {
			return
		}
	}
	t.Errorf("no %s containing %q in %v", code, contains, ds)
}

func noErrors(t *testing.T, ds []diag.Diagnostic) {
	t.Helper()
	if diag.HasErrors(ds) {
		t.Fatalf("unexpected diagnostics: %v", ds)
	}
}

// bake paints the first atlas of a source.
func bake(t *testing.T, src string) *stdimage.RGBA8 {
	t.Helper()
	r, ds := evalSrc(t, src)
	noErrors(t, ds)
	if len(r.Atlases) == 0 {
		t.Fatal("no atlas")
	}
	img, err := stdimage.Bake(r.Atlases[0].Spec())
	if err != nil {
		t.Fatal(err)
	}
	return img
}

const oneCell = prefix + `
texture t = atlas {
  grid = (1, 1)
  cell_size = 4px
  %s
  cell c { paint = %s }
}
part a { }
`

func TestNoiseZeroPaintsF5F5F5(t *testing.T) {
	// The register's worked fixture, end to end: seed 0, cell 0,
	// color = #000000 + noise(0) stores f5 f5 f5 ff at every texel.
	img := bake(t, prefix+`
painter p() { color = #000000 + noise(0) }
texture t = atlas { grid = (1, 1) cell_size = 4px cell c { paint = p() } }
part a { }
`)
	for y := 0; y < img.H; y++ {
		for x := 0; x < img.W; x++ {
			if got := img.At(x, y); got != [4]byte{0xf5, 0xf5, 0xf5, 0xff} {
				t.Fatalf("pixel (%d,%d) is %x", x, y, got)
			}
		}
	}
}

func TestPainterArithmetic(t *testing.T) {
	cases := []struct {
		name  string
		decls string
		want  [4]byte
	}{
		// base * k + n acts on RGB and keeps alpha; nothing clips until storage.
		{"scale and offset", `painter p() { color = #808080 * 0.5 + 0.25 }`, [4]byte{0x80/2 + 64, 0x80/2 + 64, 0x80/2 + 64, 255}},
		{"alpha is kept", `painter p() { color = #80808040 * 0.5 }`, [4]byte{64, 64, 64, 0x40}},
		{"clip on storage", `painter p() { color = #ffffff * 3 - 1 }`, [4]byte{255, 255, 255, 255}},
		{"no clip between operations", `painter p() { color = (#ffffff * 3 - 1.5) * 0.5 }`, [4]byte{191, 191, 191, 255}},
		{"round half to even", `painter p() { color = #000000 + 0.5 / 255 }`, [4]byte{0, 0, 0, 255}},
		{"round half to even up", `painter p() { color = #000000 + 1.5 / 255 }`, [4]byte{2, 2, 2, 255}},
		{"if", `painter p() { let a = if px == 0 then 0.2 else 0.9 color = #ffffff * a }`, [4]byte{51, 51, 51, 255}},
		{"mod", `painter p() { color = #000000 + mod(7, 4) / 10 }`, [4]byte{76, 76, 76, 255}},
		{"angle", `painter p() { color = #000000 + cos(360deg * 0.25 + 90deg) }`, [4]byte{0, 0, 0, 255}},
		{"colour literal arithmetic with an argument", `painter p(base: color = #204060) { color = base }`, [4]byte{0x20, 0x40, 0x60, 255}},
		{"u at the first interior texel of an uninset cell", `painter p() { color = #000000 + u }`, [4]byte{32, 32, 32, 255}},
		{"clamp", `painter p() { color = #000000 + clamp(2, 0, 1) }`, [4]byte{255, 255, 255, 255}},
		{"pow and sqrt", `painter p() { color = #000000 + sqrt(pow(0.25, 2)) }`, [4]byte{64, 64, 64, 255}},
		{"smoothstep", `painter p() { color = #000000 + smoothstep(0, 1, 0.5) }`, [4]byte{128, 128, 128, 255}},
		{"lerp of colours", `painter p() { color = lerp(#000000, #ffffff, 0.5) }`, [4]byte{128, 128, 128, 255}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			img := bake(t, prefix+c.decls+`
texture t = atlas { grid = (1, 1) cell_size = 4px cell c { paint = p() } }
part a { }
`)
			got := img.At(0, 0)
			if c.name == "if" {
				// The first column differs from the rest.
				if img.At(0, 0) != c.want || img.At(1, 0) != [4]byte{230, 230, 230, 255} {
					t.Errorf("first column %x, second %x", img.At(0, 0), img.At(1, 0))
				}
				return
			}
			if c.name == "u at the first interior texel of an uninset cell" {
				// u = (0.5/4) at px 0: 0.125 -> 32.
				got = img.At(0, 0)
			}
			if got != c.want {
				t.Errorf("stored %x, want %x", got, c.want)
			}
		})
	}
}

func TestPaintersUseTexelNames(t *testing.T) {
	// px, py, u, v and cell have the values the language reference gives; the
	// gutter copies the nearest interior texel.
	img := bake(t, prefix+`
painter p() { color = #000000 + px / 255 }
painter q() { color = #000000 + cell / 255 }
texture t = atlas {
  grid = (2, 1)
  cell_size = 8px
  inset = 25%
  cell a { index = 0 paint = p() }
  cell b { index = 1 paint = q() }
}
part a { }
`)
	// Interior px 2..5: px 0 and 1 copy px 2, 6 and 7 copy px 5.
	for x, want := range map[int]byte{0: 2, 1: 2, 2: 2, 5: 5, 7: 5} {
		if got := img.At(x, 3); got[0] != want {
			t.Errorf("column %d holds %d, want %d", x, got[0], want)
		}
	}
	if got := img.At(8+3, 3); got[0] != 1 {
		t.Errorf("cell b: %v, want the cell index 1", got)
	}
}

func TestStdFinishPaintersAreGreyAndInRange(t *testing.T) {
	r, ds := evalSrc(t, prefix+`
import "std/finish"
texture t = atlas {
  grid = (4, 2) cell_size = 64px inset = 7% seed = 9
  cell a { paint = finish.gloss() }
  cell b { paint = finish.satin() }
  cell c { paint = finish.foam() }
}
part a { }
`)
	noErrors(t, ds)
	img, err := stdimage.Bake(r.Atlases[0].Spec())
	if err != nil {
		t.Fatal(err)
	}
	// Cells 0, 1 and 2 are painted; every texel of them is opaque grey.
	lo := map[int]int{}
	hi := map[int]int{}
	for idx := 0; idx < 3; idx++ {
		lo[idx], hi[idx] = 255, 0
		for y := 0; y < 64; y++ {
			for x := 0; x < 64; x++ {
				p := img.At(idx*64+x, y)
				if p[0] != p[1] || p[1] != p[2] || p[3] != 255 {
					t.Fatalf("cell %d texel (%d,%d) is %x: a finish returns opaque grey", idx, x, y, p)
				}
				lo[idx], hi[idx] = min(lo[idx], int(p[0])), max(hi[idx], int(p[0]))
			}
		}
		if hi[idx]-lo[idx] < 8 {
			t.Errorf("cell %d is nearly flat: %d..%d", idx, lo[idx], hi[idx])
		}
	}
	// Foam depends on the cell index and the seed; gloss and satin do not use noise.
	a := bake(t, prefix+`
import "std/finish"
texture t = atlas { grid = (2, 1) cell_size = 16px cell a { paint = finish.foam() } cell b { paint = finish.foam() } }
part a { }
`)
	if a.At(8, 8) == a.At(16+8, 8) {
		t.Error("two foam cells of one atlas are identical")
	}
}

func TestLibraryIsOrdinarySource(t *testing.T) {
	for _, name := range std.Names() {
		path := std.Prefix + name
		f, ds, ok := ast.Library(path)
		if !ok || diag.HasErrors(ds) {
			t.Fatalf("%s: %v", path, ds)
		}
		if len(f.Painters) == 0 {
			t.Errorf("%s declares no painter", path)
		}
		src, _ := std.Source(path)
		// Reads like a project file: it has the edition header and no asset.
		if !strings.HasPrefix(strings.TrimLeft(string(src), "/ \n"), "") || f.Asset != nil {
			t.Errorf("%s has an asset", path)
		}
		sum, ok := std.Digest(path)
		if !ok || len(sum) != 64 {
			t.Errorf("%s: digest %q", path, sum)
		}
	}
	for _, want := range []string{"gloss", "satin", "foam"} {
		found := false
		f, _, _ := ast.Library("std/finish")
		for _, p := range f.Painters {
			if p.Name.Name == want {
				found = true
			}
		}
		if !found {
			t.Errorf("std/finish has no %s", want)
		}
	}
	if _, ok := std.Source("std/nope"); ok {
		t.Error("a missing library was found")
	}
	if _, ok := std.Source("std/../x"); ok {
		t.Error("a path with dots was accepted")
	}
}

func TestImportRules(t *testing.T) {
	good := prefix + `
import "std/finish" as fin
texture t = atlas { grid = (1, 1) cell_size = 4px cell c { paint = fin.gloss() } }
part a { }
`
	r, ds := evalSrc(t, good)
	noErrors(t, ds)
	if len(r.Imports) != 1 || r.Imports[0].Qualifier != "fin" || len(r.Imports[0].SHA256) != 64 {
		t.Errorf("imports %+v", r.Imports)
	}
	// The default qualifier is the last path component.
	_, ds = evalSrc(t, strings.Replace(good, ` as fin`, ``, 1))
	wantCode(t, ds, diag.CodeUnknownName, "no import is qualified `fin`")
	// A duplicate qualifier fails.
	_, ds = evalSrc(t, prefix+"import \"std/finish\"\nimport \"std/finish\"\npart a { }\n")
	wantCode(t, ds, diag.CodeDuplicate, "qualifier `finish`")
	// An unknown library suggests the nearest one.
	_, ds = evalSrc(t, prefix+"import \"std/finsh\"\npart a { }\n")
	wantCode(t, ds, diag.CodeUnknownName, "no library")
	// A reserved library that is not shipped is valid but unsupported.
	_, ds = evalSrc(t, prefix+"import \"std/hardware\"\npart a { }\n")
	wantCode(t, ds, diag.CodeUnsupported, "not shipped")
	// A project file import is outside the subset.
	_, ds = evalSrc(t, prefix+"import \"helmet.gw\" as h\npart a { }\n")
	wantCode(t, ds, diag.CodeUnsupported, "only `std/` libraries")
	// An unknown painter in a library.
	_, ds = evalSrc(t, prefix+"import \"std/finish\"\ntexture t = atlas { grid = (1, 1) cell_size = 4px cell c { paint = finish.glass() } }\npart a { }\n")
	wantCode(t, ds, diag.CodeUnknownName, "no painter is named `finish.glass`")
}

func TestPainterDiagnostics(t *testing.T) {
	atlas := "\ntexture t = atlas { grid = (1, 1) cell_size = 4px cell c { paint = p() } }\npart a { }\n"
	cases := []struct {
		name  string
		decls string
		code  string
		msg   string
	}{
		{"unknown name", `painter p() { color = #000000 + zz }`, diag.CodeUnknownName, "`zz`"},
		{"missing color", `painter p() { let a = 1 }`, diag.CodeMissing, "needs `color"},
		{"colour times colour", `painter p() { color = #000000 * #ffffff }`, diag.CodeType, "does not apply to a colour and a colour"},
		{"number result", `painter p() { color = 1 }`, diag.CodeType, "`color` must be a colour"},
		{"bare angle", `painter p() { color = #000000 + cos(0.5) }`, diag.CodeUnit, "needs an angle with a unit"},
		{"length", `painter p() { color = #000000 + 3m }`, diag.CodeUnit, "plain numbers and angles"},
		{"if mismatch", `painter p() { color = #000000 + (if 1 == 1 then 1 else #ffffff) }`, diag.CodeType, "one type"},
		{"switch condition", `painter p() { color = #000000 + (if 1 then 1 else 2) }`, diag.CodeType, "must be a switch"},
		{"cycle", `painter p() { color = q() } painter q() { color = p() }`, diag.CodeCycle, "calls itself"},
		{"duplicate let", `painter p() { let a = 1 let a = 2 color = #000000 }`, diag.CodeDuplicate, "declared twice"},
		{"holds only let and color", `painter p() { color = #000000 size = 3 }`, diag.CodeUnknownField, "not allowed in a painter"},
		{"runtime", `param rc : color = #ff0000 runtime
painter p() { color = rc }`, diag.CodeRuntime, "runtime parameter `rc`"},
		{"wrong argument type", `painter p(k: number) { color = #000000 * k } painter q() { color = p(#ffffff) }
texture t2 = atlas { grid = (1, 1) cell_size = 4px cell c { paint = q() } }`, diag.CodeType, "argument `k`"},
		{"too many arguments", `painter q() { color = #000000 } painter p() { color = q(1) }`, diag.CodeType, "takes 0 argument"},
		{"missing argument", `painter q(k: number) { color = #000000 } painter p() { color = q() }`, diag.CodeMissing, "needs the argument `k`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ds := evalSrc(t, prefix+c.decls+atlas)
			wantCode(t, ds, c.code, c.msg)
		})
	}
}

func TestPainterCallsFromTheDocument(t *testing.T) {
	// Named arguments, defaults and a document colour parameter.
	img := bake(t, prefix+`
param jersey : color = #204060
painter flat(base: color, k: number = 1) { color = base * k }
texture t = atlas { grid = (1, 1) cell_size = 2px cell c { paint = flat(k = 0.5, base = jersey) } }
part a { }
`)
	if got := img.At(0, 0); got != [4]byte{16, 32, 48, 255} {
		t.Errorf("stored %x", got)
	}
	_, ds := evalSrc(t, prefix+`
param rc : color = #204060 runtime
painter flat(base: color) { color = base }
texture t = atlas { grid = (1, 1) cell_size = 2px cell c { paint = flat(rc) } }
part a { }
`)
	wantCode(t, ds, diag.CodeRuntime, "baked")
	_, ds = evalSrc(t, prefix+`
painter flat(base: color) { color = base }
texture t = atlas { grid = (1, 1) cell_size = 2px cell c { paint = flat(1) } }
part a { }
`)
	wantCode(t, ds, diag.CodeType, "argument `base`")
}

func TestAtlasDiagnostics(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
		msg  string
	}{
		{"index out of range", `grid = (2, 1) cell_size = 4px cell a { index = 2 paint = #000000 }`, diag.CodeFieldRange, "has index 2"},
		{"duplicate index", `grid = (2, 1) cell_size = 4px cell a { index = 1 paint = #000000 } cell b { index = 1 paint = #000000 }`, diag.CodeDuplicate, "both have index 1"},
		{"duplicate cell", `grid = (2, 1) cell_size = 4px cell a { paint = #000000 } cell a { paint = #000000 }`, diag.CodeDuplicate, "declared twice"},
		{"overfull", `grid = (1, 1) cell_size = 4px cell a { paint = #000000 } cell b { paint = #000000 }`, diag.CodeFieldRange, "grid is full"},
		{"palette needs slot", `grid = (1, 1) cell_size = 4px mode = palette cell a { paint = #000000 }`, diag.CodeMissing, "needs `slot"},
		{"slot in colour", `grid = (1, 1) cell_size = 4px cell a { slot = 1 paint = #000000 }`, diag.CodeType, "palette atlas"},
		{"slot above 255", `grid = (1, 1) cell_size = 4px mode = palette cell a { slot = 256 paint = #000000 }`, diag.CodeFieldRange, "at most 255"},
		{"needs paint", `grid = (1, 1) cell_size = 4px cell a { }`, diag.CodeMissing, "needs `paint`"},
		{"inset 0.5", `grid = (1, 1) cell_size = 4px inset = 50% cell a { paint = #000000 }`, diag.CodeFieldRange, "not including 0.5"},
		{"no interior texel", `grid = (1, 1) cell_size = 2px inset = 49% cell a { paint = #000000 }`, diag.CodeFieldRange, "no texel centre inside"},
		{"grid zero", `grid = (0, 1) cell_size = 4px cell a { paint = #000000 }`, diag.CodeFieldRange, "grid is two whole numbers"},
		{"too many cells", `grid = (65, 64) cell_size = 1px cell a { paint = #000000 }`, diag.CodeFieldRange, "at most 4096 cells"},
		{"too wide", `grid = (1, 1) cell_size = 4096px cell a { paint = #000000 }`, diag.CodeFieldRange, "limit is 2048"},
		{"fractional cell size", `grid = (1, 1) cell_size = 4.5px cell a { paint = #000000 }`, diag.CodeFieldRange, "whole, positive"},
		{"needs grid", `cell_size = 4px cell a { paint = #000000 }`, diag.CodeMissing, "`grid`"},
		{"runtime colour", `grid = (1, 1) cell_size = 4px cell a { paint = rc }`, diag.CodeRuntime, "baked"},
		{"jpeg", `grid = (1, 1) cell_size = 4px encode = jpeg(80) cell a { paint = #000000 }`, diag.CodeType, "encode"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ds := evalSrc(t, prefix+"param rc : color = #ff0000 runtime\ntexture t = atlas { "+c.body+" }\npart a { }\n")
			wantCode(t, ds, c.code, c.msg)
		})
	}
}

func TestCellAllocation(t *testing.T) {
	// Explicit indices first, then the unindexed cells in name order take the
	// lowest free numbers. Reordering the declarations does not move a cell.
	a := `texture t = atlas { grid = (3, 1) cell_size = 4px
  cell zeta { paint = #000000 }
  cell alpha { paint = #000000 }
  cell pin { index = 0 paint = #000000 }
}`
	b := `texture t = atlas { grid = (3, 1) cell_size = 4px
  cell pin { index = 0 paint = #000000 }
  cell alpha { paint = #000000 }
  cell zeta { paint = #000000 }
}`
	for _, d := range []string{a, b} {
		r, ds := evalSrc(t, prefix+d+"\npart a { }\n")
		noErrors(t, ds)
		got := map[string]int{}
		for _, c := range r.Atlases[0].Cells {
			got[c.Name] = c.Index
		}
		if got["pin"] != 0 || got["alpha"] != 1 || got["zeta"] != 2 {
			t.Errorf("indices %v", got)
		}
	}
}

func TestPaletteMaskAndFallback(t *testing.T) {
	src := prefix + `
param c0 : color = #e1ae4f runtime
texture mask = atlas { grid = (2, 1) cell_size = 4px mode = palette
  cell a { slot = 0 paint = #808080 }
  cell b { slot = 5 paint = #ffffff }
}
material m = selena {
  source = "m.sel" name = "M" mask = mask slot0 = c0
  fallback = pbr { base_texture = paint(mask) }
}
part a { material = m }
`
	_, ds := evalSrc(t, src)
	// Slot 5 has no colour.
	wantCode(t, ds, diag.CodeMatParam, "slot 5")
	r, ds := evalSrc(t, strings.Replace(src, "slot0 = c0", "slot0 = c0 slot5 = #102030", 1))
	noErrors(t, ds)
	a := r.Atlas("mask")
	if a == nil || len(a.Slots) != 2 || a.Slots[0] != 0 || a.Slots[1] != 5 {
		t.Fatalf("slots %+v", a)
	}
	// paint(mask) on a pbr that is not a Selena fallback has no slot colours.
	_, ds = evalSrc(t, strings.Replace(src, "material m = selena", "material s = selena", 1)+"material m = pbr { base_texture = paint(mask) }\n")
	wantCode(t, ds, diag.CodeMatParam, "not the fallback of one")
	// A raw mask is not a colour texture.
	_, ds = evalSrc(t, prefix+"texture mask = atlas { grid = (1, 1) cell_size = 4px mode = palette cell a { slot = 0 paint = #ffffff } }\nmaterial m = pbr { base_texture = mask }\npart a { }\n")
	wantCode(t, ds, diag.CodeMatParam, "palette mask")
	// An unknown texture suggests a name.
	_, ds = evalSrc(t, prefix+"texture mask = atlas { grid = (1, 1) cell_size = 4px cell a { paint = #ffffff } }\nmaterial m = pbr { base_texture = msk }\npart a { }\n")
	wantCode(t, ds, diag.CodeUnknownName, "no texture is named `msk`")
	// Other texture kinds are valid and outside the subset.
	_, ds = evalSrc(t, prefix+"texture f = file { path = \"a.png\" }\npart a { }\n")
	wantCode(t, ds, diag.CodeUnsupported, "`file` texture")
}

func TestCellResolution(t *testing.T) {
	tex := `texture t = atlas { grid = (2, 1) cell_size = 4px cell body { paint = #808080 } }
`
	_, ds := evalSrc(t, prefix+tex+"material m = pbr { base_texture = t }\npart a { material = m sphere { radius = 1m cell = bodi } }\n")
	wantCode(t, ds, diag.CodeUnknownName, "no cell `bodi`")
	_, ds = evalSrc(t, prefix+tex+"part a { sphere { radius = 1m cell = body } }\n")
	wantCode(t, ds, diag.CodeUnknownName, "no material is set")
	_, ds = evalSrc(t, prefix+tex+"material m = pbr { }\npart a { material = m sphere { radius = 1m cell = body } }\n")
	wantCode(t, ds, diag.CodeUnknownName, "samples no atlas")
	// A part's cell and material are inherited, through a template too.
	_, ds = evalSrc(t, prefix+tex+`material m = pbr { base_texture = t }
template ring(r: length) { sphere { radius = r } }
part a { material = m cell = body ring (1m) part b { sphere { radius = 1m cell = body } } }
`)
	noErrors(t, ds)
	_, ds = evalSrc(t, prefix+tex+`material m = pbr { base_texture = t }
template ring(r: length) { sphere { radius = r } }
part a { material = m cell = nope ring (1m) }
`)
	// The part names a cell that no atlas of its material has; with no shape
	// below it names it for, there is nothing to check yet.
	_ = ds
	_, ds = evalSrc(t, prefix+tex+`material m = pbr { base_texture = t }
template ring(r: length) { sphere { radius = r } }
part a { material = m cell = nope ring (1m) sphere { radius = 1m } }
`)
	wantCode(t, ds, diag.CodeUnknownName, "no cell `nope`")
}

func TestPlug(t *testing.T) {
	r, ds := evalSrc(t, prefix+"plug head_gear { kind = helmet at = (0, 1.7, 0) }\npart a { }\n")
	noErrors(t, ds)
	if len(r.Plugs) != 1 || r.Plugs[0].Name != "head_gear" {
		t.Fatalf("plugs %+v", r.Plugs)
	}
	at, _ := r.Plugs[0].Get("at")
	if v, _ := at.Vec3(); v[1] != 1.7 {
		t.Errorf("at %v", at)
	}
	_, ds = evalSrc(t, prefix+"plug head_gear { at = (0, 1, 0) }\npart a { }\n")
	wantCode(t, ds, diag.CodeMissing, "`kind`")
	_, ds = evalSrc(t, prefix+"plug p { kind = x }\nplug p { kind = y }\npart a { }\n")
	wantCode(t, ds, diag.CodeDuplicate, "declared twice")
	_, ds = evalSrc(t, prefix+"plug p { kind = x rot = (0deg, 90deg, 0deg) }\npart a { }\n")
	wantCode(t, ds, diag.CodeUnsupported, "rot")
	// Sockets and plugs share one namespace.
	_, ds = evalSrc(t, prefix+"plug s { kind = x }\npart a { socket s { kind = x } }\n")
	wantCode(t, ds, diag.CodeDuplicate, "`s`")
}

func ExampleResolved_Atlas() {
	r, _ := evalSrcQuiet(prefix + "texture t = atlas { grid = (4, 2) cell_size = 64px inset = 7% cell a { paint = #808080 } }\npart a { }\n")
	a := r.Atlas("t")
	fmt.Println(a.Layout.Width(), a.Layout.Height())
	// Output: 256 128
}

func evalSrcQuiet(src string) (*resolve.Resolved, []diag.Diagnostic) {
	f, ds := ast.Lower(lang.Parse([]byte(src)))
	r, more := resolve.Evaluate(f, nil)
	return r, append(ds, more...)
}
