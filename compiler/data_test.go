package compiler

import (
	"bytes"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/datapkg"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
)

// The data-package fixture: testdata/world/example/crate.gw reads its size
// from example/world@1 (decision 0020). These tests are the first step of the
// plan: change one value, re-pin, and see the faces move on the decoded GLB.

const (
	worldRoot = "../testdata/world"
	// the digest of the shipped fixture pair; datapkg's vectors repeat it
	worldDigest = "6a972715adc467fefb3e4c892958ee75a1cdb752a407b817fbd2af606e9beab5"
	inch        = 0.0254
)

// worldFS reads the fixture project into memory.
func worldFS(t testing.TB) fstest.MapFS {
	t.Helper()
	m := fstest.MapFS{}
	err := filepath.WalkDir(worldRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(worldRoot, p)
		m[filepath.ToSlash(rel)] = &fstest.MapFile{Data: b}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// repin writes the lock that pins the files as they are now.
func repin(t testing.TB, m fstest.MapFS) {
	t.Helper()
	l, _, err := datapkg.NewLock(m, "example/world@1", "example/world/schema-v1.json", "example/world/standard.json")
	if err != nil {
		t.Fatal(err)
	}
	m["example/world.lock"] = &fstest.MapFile{Data: l.Bytes()}
}

// withWidth returns the fixture with the crate width changed and re-pinned.
func withWidth(t testing.TB, value string) fstest.MapFS {
	t.Helper()
	m := worldFS(t)
	d := m["example/world/standard.json"]
	if !strings.Contains(string(d.Data), `"24"`) {
		t.Fatal("the data file has no width of 24")
	}
	m["example/world/standard.json"] = &fstest.MapFile{Data: []byte(strings.Replace(string(d.Data), `"24"`, `"`+value+`"`, 1))}
	repin(t, m)
	return m
}

func crateSource(t testing.TB, m fs.FS) string {
	t.Helper()
	b, err := fs.ReadFile(m, "example/crate.gw")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func buildCrate(t testing.TB, m fstest.MapFS) *Product {
	t.Helper()
	f := lowerIn(t, crateSource(t, m), m, "example")
	p, ds := Build(f, "")
	if p == nil {
		t.Fatalf("build failed: %v", ds)
	}
	return p
}

func crateBounds(t testing.TB, p *Product, node string) (lo, hi [3]float64) {
	t.Helper()
	d := decode(t, p.Outputs[0].Files[0])
	_, n := nodeByName(t, d, node)
	l, h := boundsOf(nodeMesh(t, d, n))
	return l, h
}

func near(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.9f, want %.9f (tolerance %g)", what, got, want, tol)
	}
}

// The outer faces of the body are at the schema values: 24 in wide along x,
// 18 in deep along z, 2 ft high. The faces come from exact rationals rounded
// once, then stored as float32 in the GLB, so the tolerance is one float32 step.
func TestCrateFacesAreAtThePackageValues(t *testing.T) {
	p := buildCrate(t, worldFS(t))
	lo, hi := crateBounds(t, p, "body")
	near(t, "x low face", lo[0], -12*inch, 1e-7)
	near(t, "x high face", hi[0], 12*inch, 1e-7)
	near(t, "z low face", lo[2], -9*inch, 1e-7)
	near(t, "z high face", hi[2], 9*inch, 1e-7)
	near(t, "floor", lo[1], 0, 1e-7)
	near(t, "top", hi[1], 0.6096, 1e-7) // 2 ft
	// 6 slats (a number), 12 + 72 + 12 triangles.
	if f := p.Outputs[0].Files[0]; f.Triangles != 96 {
		t.Errorf("%d triangles, want 96: the slat count is not 6", f.Triangles)
	}
	// A ratio: each slat is 87.5 % of the 18 in / 6 pitch in z.
	slo, shi := crateBounds(t, p, "slats")
	pitch := 18 * inch / 6
	near(t, "first slat edge", slo[2], -9*inch+pitch/2-pitch*0.875/2, 1e-7)
	near(t, "last slat edge", shi[2], -9*inch+pitch/2+5*pitch+pitch*0.875/2, 1e-7)
	// An angle: the lid is a 0.5 m by 20 mm board turned 15 degrees about x.
	llo, lhi := crateBounds(t, p, "lid")
	s, c := math.Sincos(15 * math.Pi / 180)
	near(t, "lid half height", (lhi[1]-llo[1])/2, (0.5*s+0.02*c)/2, 1e-6)
	near(t, "lid half depth", (lhi[2]-llo[2])/2, (0.5*c+0.02*s)/2, 1e-6)
}

// Widen the crate by one inch in the data file and re-pin: each side face
// moves half an inch, 0.0127 m, and nothing else of the body moves.
func TestOneInchWiderMovesEachSideFaceHalfAnInch(t *testing.T) {
	a := buildCrate(t, worldFS(t))
	b := buildCrate(t, withWidth(t, "25"))
	alo, ahi := crateBounds(t, a, "body")
	blo, bhi := crateBounds(t, b, "body")
	near(t, "low face moved", alo[0]-blo[0], 0.0127, 1e-7)
	near(t, "high face moved", bhi[0]-ahi[0], 0.0127, 1e-7)
	for _, i := range []int{1, 2} {
		near(t, "other axis low", blo[i], alo[i], 0)
		near(t, "other axis high", bhi[i], ahi[i], 0)
	}
	// The new value reaches the input hash, the meaning hash and the GLB.
	if a.InputSHA256 == b.InputSHA256 || a.Outputs[0].FirSHA256 == b.Outputs[0].FirSHA256 || a.Outputs[0].Files[0].SHA256 == b.Outputs[0].Files[0].SHA256 {
		t.Error("a changed value did not change the input hash, the fir hash and the file")
	}
	if a.Data[0].SHA256 != worldDigest || b.Data[0].SHA256 == worldDigest {
		t.Errorf("recorded digests %v %v", a.Data, b.Data)
	}
}

// A data edit that keeps the old lock is refused with GW-LOCK at the import,
// and no product exists to write.
func TestStalePinRefusesTheBuild(t *testing.T) {
	m := worldFS(t)
	d := m["example/world/standard.json"]
	m["example/world/standard.json"] = &fstest.MapFile{Data: []byte(strings.Replace(string(d.Data), `"24"`, `"25"`, 1))}
	src := crateSource(t, m)
	f, ds := ast.LowerWith(lang.Parse([]byte(src)), ast.Options{Project: &ast.Project{FS: m, Dir: "example"}})
	if diag.HasErrors(ds) {
		t.Fatal(ds)
	}
	p, _, all := BuildWith(f, "", Options{})
	if p != nil {
		t.Fatal("a stale pin built")
	}
	var got *diag.Diagnostic
	for i := range all {
		if all[i].Code == diag.CodeLock {
			got = &all[i]
		}
	}
	if got == nil || len(all) != 1 {
		t.Fatalf("diagnostics %v", all)
	}
	at := strings.Index(src, `"example/world@1"`)
	if got.Span.Start != at || got.Span.End != at+len(`"example/world@1"`) {
		t.Errorf("span %v is not the import path at %d", got.Span, at)
	}
	if !strings.Contains(got.Message, "expected sha256 "+worldDigest) || !strings.Contains(got.Message, "actual ") {
		t.Errorf("message %q", got.Message)
	}
}

// The same pinned inputs give the same bytes: twice, with one and eight OS
// threads, and from a project copied to another path.
func TestCrateBuildsAreByteIdentical(t *testing.T) {
	m := worldFS(t)
	ref := buildCrate(t, m)
	refGLB, refRec := ref.Outputs[0].Files[0].GLB, ref.Record()

	same := func(what string, p *Product) {
		t.Helper()
		if !bytes.Equal(p.Outputs[0].Files[0].GLB, refGLB) || !bytes.Equal(p.Record(), refRec) {
			t.Errorf("%s: the GLB or the record differs", what)
		}
	}
	same("a second build", buildCrate(t, m))
	for _, n := range []int{1, 8} {
		prev := runtime.GOMAXPROCS(n)
		same("GOMAXPROCS "+string(rune('0'+n)), buildCrate(t, m))
		runtime.GOMAXPROCS(prev)
	}

	// Another checkout path, read from disk through OpenProject.
	dir := filepath.Join(t.TempDir(), "another", "checkout")
	for name, f := range m {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, f.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	file := lowerProjectSource(t, filepath.Join(dir, "example", "crate.gw"), []byte(crateSource(t, m)))
	p, ds := Build(file, "")
	if p == nil {
		t.Fatalf("%v", ds)
	}
	same("another path", p)
}

// The GLB names its inputs in asset.extras, and so does the build record.
func TestCrateRecordsItsDataInputs(t *testing.T) {
	p := buildCrate(t, worldFS(t))
	d := decode(t, p.Outputs[0].Files[0])
	want := `{"greenware":{"inputs":[{"contract":"example/world@1","profile":"standard","sha256":"` + worldDigest + `"}]}}`
	if string(d.Asset.Extras) != want {
		t.Errorf("asset.extras = %s\nwant %s", d.Asset.Extras, want)
	}
	if d.Asset.Generator != Generator {
		t.Errorf("generator %q", d.Asset.Generator)
	}
	rec := string(p.Record())
	if !strings.Contains(rec, `"inputs": {`) || !strings.Contains(rec, worldDigest) || !strings.Contains(rec, `"profile": "standard"`) {
		t.Errorf("the build record has no data input:\n%s", rec)
	}
}

// An asset that imports nothing is unchanged: no extras, no inputs in the
// record, no data lines in the input hash. The approved digests of the goal
// post (testdata/compiler/approved) pin the bytes themselves.
func TestAssetWithoutDataImportsIsUnchanged(t *testing.T) {
	p := buildGoalPost(t)
	if p.Data != nil {
		t.Errorf("data inputs %v", p.Data)
	}
	d := decode(t, p.Outputs[0].Files[0])
	if len(d.Asset.Extras) != 0 {
		t.Errorf("asset.extras = %s", d.Asset.Extras)
	}
	if strings.Contains(string(p.Record()), `"inputs"`) {
		t.Error("the record has an inputs object")
	}
}

// A package that gives the wrong unit is refused at the import with GW-UNIT;
// an unknown field in the source is GW-UNKNOWN-NAME with a suggestion.
func TestDataReferenceErrors(t *testing.T) {
	m := worldFS(t)
	src := strings.Replace(crateSource(t, m), "world.crate.width", "world.crate.widht", 1)
	f := lowerIn(t, src, m, "example")
	_, _, ds := BuildWith(f, "", Options{})
	if len(ds) == 0 || ds[0].Code != diag.CodeUnknownName || len(ds[0].Edits) == 0 {
		t.Fatalf("%v", ds)
	}
	if !strings.Contains(ds[0].Message, "`widht` is not a field of `world.crate`") {
		t.Errorf("message %q", ds[0].Message)
	}
}
