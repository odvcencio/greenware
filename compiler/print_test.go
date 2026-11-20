package compiler

import (
	"archive/zip"
	"bytes"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"hash/crc32"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"m31labs.dev/greenware/backend/threemf"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/resolve"
)

const (
	goalpostExample = "../examples/goalpost/goalpost.gw"
	printGolden     = "../testdata/print/goalpost-stadium.candidate.sha256"
	printFixture    = "../testdata/diag/GW-PRINT.gw"
)

func printGoalPost(t testing.TB, scale float64) *PrintProduct {
	t.Helper()
	p, ds := BuildPrintWith(loadProject(t, goalpostExample), "stadium", 0, PrintOptions{Scale: scale})
	if p == nil {
		t.Fatalf("print failed: %v", ds)
	}
	return p
}

// The goal post's stadium output at level 0 prints as 16 separate solids: the
// post, the crossbar, two uprights with their spheres, the base plate, eight
// bolts and the pad. The painted frame is not fused.
func TestPrintGoalPost(t *testing.T) {
	p := printGoalPost(t, 1)
	if p.Name != "goalpost.3mf" || p.Output != "stadium" || p.Level != 0 || p.Scale != 1 {
		t.Errorf("product %s %s %d %v", p.Name, p.Output, p.Level, p.Scale)
	}
	if len(p.Objects) != 16 {
		t.Fatalf("%d objects, want 16", len(p.Objects))
	}
	want := []string{
		"goalpost/post/sweep", "goalpost/crossbar/cylinder",
		"goalpost/upright_l/cylinder", "goalpost/upright_l/sphere",
		"goalpost/upright_r/cylinder", "goalpost/upright_r/sphere",
		"goalpost/base/plate",
	}
	for i, w := range want {
		if p.Objects[i].Name != w {
			t.Errorf("object %d is %q, want %q", i+1, p.Objects[i].Name, w)
		}
	}
	// Decode the bytes: an independent reading, then the print gate.
	m, err := threemf.Decode(p.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if fs := threemf.Validate(m); len(fs) != 0 {
		t.Fatalf("findings: %v", fs)
	}
	if m.Unit != threemf.Millimeter || len(m.Items) != len(m.Objects) {
		t.Errorf("unit %q, %d items for %d objects", m.Unit, len(m.Items), len(m.Objects))
	}
	tris := 0
	for i, o := range p.Objects {
		if !o.Closed || !(o.VolumeMM3 > 0) || o.Span.Len() == 0 {
			t.Errorf("object %q: closed %v, volume %v, span %+v", o.Name, o.Closed, o.VolumeMM3, o.Span)
		}
		if o.Triangles != len(m.Objects[i].Triangles) || o.Vertices != len(m.Objects[i].Vertices) {
			t.Errorf("object %q: counts disagree with the file", o.Name)
		}
		tris += o.Triangles
	}
	// The GLB of the same output draws 1340 triangles; the solids hold the same.
	if tris != 1340 || p.Triangles != 1340 {
		t.Errorf("%d triangles (%d summed), want 1340", p.Triangles, tris)
	}
	// 12.24 m of post: the tips of the uprights stand 12,242 mm above the
	// ground (3.05 m crossbar + 9.14 m rise + 2 mm + the 50 mm sphere radius).
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-6 }
	if !near(p.Lo[2], 0) || !near(p.Hi[2], 12242) {
		t.Errorf("height from %v to %v mm, want 0 to 12242", p.Lo[2], p.Hi[2])
	}
	if !near(p.Lo[0], -2920) || !near(p.Hi[0], 2920) {
		t.Errorf("width from %v to %v mm, want -2920 to 2920", p.Lo[0], p.Hi[0])
	}
	// Depth: the base plate reaches 2180 mm behind the line, along -y; the
	// crossbar's front reaches +60 mm.
	if !near(p.Lo[1], -2180) || !near(p.Hi[1], 60) {
		t.Errorf("depth from %v to %v mm", p.Lo[1], p.Hi[1])
	}
	// The sphere's volume against 4/3 pi r^3 for a 50 mm sphere: the 12 by 12
	// tessellation is inside the true sphere by a few percent.
	for _, o := range p.Objects {
		if strings.HasSuffix(o.Name, "upright_l/sphere") {
			full := 4.0 / 3.0 * math.Pi * 50 * 50 * 50
			if o.VolumeMM3 > full || o.VolumeMM3 < 0.85*full {
				t.Errorf("sphere volume %v mm3, true sphere %v", o.VolumeMM3, full)
			}
		}
	}
	joined := strings.Join(p.Notes, "\n")
	for _, s := range []string{"minimum wall thickness: not checked", "separate bodies: 21 pair(s)", "FACET-PRINT-WALL"} {
		if !strings.Contains(joined, s) {
			t.Errorf("notes lack %q:\n%s", s, joined)
		}
	}
}

func TestPrintScale(t *testing.T) {
	p := printGoalPost(t, 0.01) // a 122 mm post
	if math.Abs(p.Hi[2]-122.42) > 1e-6 || p.Scale != 0.01 {
		t.Errorf("height %v mm at scale %v", p.Hi[2], p.Scale)
	}
	one := printGoalPost(t, 1)
	// The volume scales with the cube, to the six decimals of the file.
	for i := range p.Objects {
		got, want := p.Objects[i].VolumeMM3, one.Objects[i].VolumeMM3*1e-6
		if math.Abs(got-want) > 1e-3*want {
			t.Errorf("%s: volume %v, want %v", p.Objects[i].Name, got, want)
		}
	}
	file := loadProject(t, goalpostExample)
	for _, s := range []float64{-1, math.NaN(), math.Inf(1), 1e9} {
		bp, ds := BuildPrintWith(file, "stadium", 0, PrintOptions{Scale: s})
		if bp != nil || len(ds) != 1 || ds[0].Code != diag.CodePrint {
			t.Errorf("scale %v: %v %v", s, bp != nil, ds)
		}
	}
}

func TestPrintLevelsAndOutputs(t *testing.T) {
	file := loadProject(t, goalpostExample)
	// Level 2 drops the base and the pad: only the frame remains.
	p, ds := BuildPrint(file, "stadium", 2)
	if p == nil {
		t.Fatal(ds)
	}
	if p.Name != "goalpost-lod2.3mf" || len(p.Objects) != 6 {
		t.Errorf("level 2: %s with %d objects", p.Name, len(p.Objects))
	}
	for _, bad := range []struct {
		name  string
		level int
		code  string
	}{{"stadium", 3, diag.CodeMissing}, {"stadium", -1, diag.CodeMissing}, {"nope", 0, diag.CodeUnknownName}, {"", 0, diag.CodeUnknownName}} {
		bp, ds := BuildPrint(file, bad.name, bad.level)
		if bp != nil || len(ds) == 0 || ds[0].Code != bad.code {
			t.Errorf("%q level %d: product %v, %v", bad.name, bad.level, bp != nil, ds)
		}
	}
}

// A shape with open ends is a surface, not a solid. It is refused with
// GW-PRINT at the shape's own declaration.
func TestPrintRefusesOpenShape(t *testing.T) {
	src, err := os.ReadFile(printFixture)
	if err != nil {
		t.Fatal(err)
	}
	f := lowerSource(t, src)
	p, ds := BuildPrint(f, "demo", 0)
	if p != nil {
		t.Fatal("an open pipe was printed")
	}
	if !diag.HasErrors(ds) || len(ds) < 1 {
		t.Fatalf("diagnostics %v", ds)
	}
	for _, d := range ds {
		if d.Code != diag.CodePrint {
			t.Errorf("unexpected %s", d)
		}
		if got := string(src[d.Span.Start:d.Span.End]); got != "pipe" {
			t.Errorf("span marks %q, want the shape name", got)
		}
	}
	if !strings.Contains(ds[0].Message, "open-edge") || !strings.Contains(ds[0].Message, "goalpost") && !strings.Contains(ds[0].Message, "a/pipe") {
		t.Errorf("message %q", ds[0].Message)
	}
	// The same source still builds as a GLB: the refusal belongs to print.
	if gp, gds := Build(f, "demo"); gp == nil {
		t.Errorf("the GLB build fails too: %v", gds)
	}
	// The catalogue knows the code, and the front end accepts the fixture.
	e, ok := diag.Lookup(diag.CodePrint)
	if !ok || e.Severity != diag.Error || e.Fixture != "testdata/diag/GW-PRINT.gw" || e.Doc == "" {
		t.Errorf("catalogue entry %+v", e)
	}
	if _, fds := resolve.EvaluateAll(f); len(fds) != 0 {
		t.Errorf("front end reports %v", fds)
	}
}

// Capping the pipe makes it printable.
func TestPrintCappedPipeIsAccepted(t *testing.T) {
	src, err := os.ReadFile(printFixture)
	if err != nil {
		t.Fatal(err)
	}
	capped := strings.Replace(string(src), "segments = 12 caps = off", "segments = 12 caps = on", 1)
	p, ds := BuildPrint(lowerSource(t, []byte(capped)), "demo", 0)
	if p == nil {
		t.Fatal(ds)
	}
	if len(p.Objects) != 1 || !p.Objects[0].Closed {
		t.Errorf("objects %+v", p.Objects)
	}
	// The pipe is 100 mm long along the asset's y axis, which is z in the file.
	if got := p.Hi[2] - p.Lo[2]; math.Abs(got-100) > 1e-6 {
		t.Errorf("height %v mm, want 100", got)
	}
}

// copyTree copies a directory so a build reads the same project from another
// path.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPrintDeterminism(t *testing.T) {
	// The goal post lives in a project; copy its folder and the project file.
	root := filepath.Dir(filepath.Dir(goalpostExample))
	dirA, dirB := t.TempDir(), t.TempDir()
	copyTree(t, filepath.Join(root, "goalpost"), filepath.Join(dirA, "goalpost"))
	copyTree(t, filepath.Join(root, "goalpost"), filepath.Join(dirB, "goalpost"))
	for _, d := range []string{dirA, dirB} {
		if mod, err := os.ReadFile(filepath.Join(root, "greenware.mod")); err == nil {
			os.WriteFile(filepath.Join(d, "greenware.mod"), mod, 0o644)
		}
	}
	prev := runtime.GOMAXPROCS(1)
	a := printAt(t, filepath.Join(dirA, "goalpost", "goalpost.gw"))
	runtime.GOMAXPROCS(8)
	b := printAt(t, filepath.Join(dirB, "goalpost", "goalpost.gw"))
	runtime.GOMAXPROCS(prev)
	if !bytes.Equal(a.Bytes, b.Bytes) || a.SHA256 != b.SHA256 {
		t.Errorf("the 3MF differs between GOMAXPROCS 1 and 8 and two directories: %s %s", a.SHA256, b.SHA256)
	}
	if c := printGoalPost(t, 1); c.SHA256 != a.SHA256 {
		t.Errorf("the 3MF differs from the build in the repository: %s %s", c.SHA256, a.SHA256)
	}
}

func printAt(t *testing.T, path string) *PrintProduct {
	t.Helper()
	p, ds := BuildPrint(loadProject(t, path), "stadium", 0)
	if p == nil {
		t.Fatal(ds)
	}
	return p
}

// The standard library reads the package: three stored parts in name order,
// fixed timestamp, no extra field, every CRC right.
func TestPrintZipWithStandardReader(t *testing.T) {
	p := printGoalPost(t, 1)
	zr, err := zip.NewReader(bytes.NewReader(p.Bytes), int64(len(p.Bytes)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		if f.Method != zip.Store || len(f.Extra) != 0 || f.Comment != "" {
			t.Errorf("%s: method %d extra %d", f.Name, f.Method, len(f.Extra))
		}
		if f.Modified.Year() != 1980 || f.Modified.Month() != 1 || f.Modified.Day() != 1 {
			t.Errorf("%s: modified %v", f.Name, f.Modified)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Errorf("%s: %v", f.Name, err)
		}
		if crc32.ChecksumIEEE(data) != f.CRC32 {
			t.Errorf("%s: CRC-32 differs", f.Name)
		}
	}
	if strings.Join(names, ",") != "3D/3dmodel.model,[Content_Types].xml,_rels/.rels" {
		t.Errorf("entries %v", names)
	}
}

func TestPrintGolden(t *testing.T) {
	p := printGoalPost(t, 1)
	got := fmt.Sprintf("# sha256 bytes name: the 3MF of output stadium, level 0, scale 1, from examples/goalpost/goalpost.gw (candidate; a reviewer approves it)\n%s %d %s\n", p.SHA256, len(p.Bytes), p.Name)
	if os.Getenv("GREENWARE_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(printGolden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(printGolden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(printGolden)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("the goal post 3MF changed on %s/%s.\nrecorded:\n%s\nbuilt:\n%s", runtime.GOOS, runtime.GOARCH, want, got)
	}
}

// print.go does its float arithmetic in the same bits on every architecture:
// a multiply that feeds an add or subtract is wrapped in float64(...), so that
// arm64 does not fuse the pair.
func TestPrintWrapsMultiplies(t *testing.T) {
	fset := token.NewFileSet()
	var all []*ast.File
	names, _ := filepath.Glob("*.go")
	var target *ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, f)
		if name == "print.go" {
			target = f
		}
	}
	if target == nil {
		t.Fatal("print.go not found")
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil), Error: func(error) {}}
	conf.Check("compiler", fset, all, info)
	isFloat := func(e ast.Expr) bool {
		tv, ok := info.Types[e]
		if !ok || tv.Type == nil {
			return true
		}
		b, ok := tv.Type.Underlying().(*types.Basic)
		if !ok {
			return true
		}
		return b.Info()&(types.IsFloat|types.IsUntyped) != 0 && b.Info()&types.IsInteger == 0
	}
	isMul := func(e ast.Expr) bool {
		for {
			p, ok := e.(*ast.ParenExpr)
			if !ok {
				break
			}
			e = p.X
		}
		b, ok := e.(*ast.BinaryExpr)
		return ok && b.Op == token.MUL && isFloat(b)
	}
	ast.Inspect(target, func(n ast.Node) bool {
		switch b := n.(type) {
		case *ast.BinaryExpr:
			if (b.Op == token.ADD || b.Op == token.SUB) && (isMul(b.X) || isMul(b.Y)) && isFloat(b) {
				t.Errorf("%s: multiply feeds add or subtract without float64(...)", fset.Position(b.Pos()))
			}
		case *ast.AssignStmt:
			if b.Tok == token.ADD_ASSIGN || b.Tok == token.SUB_ASSIGN {
				for _, r := range b.Rhs {
					if isMul(r) {
						t.Errorf("%s: multiply feeds += or -= without float64(...)", fset.Position(b.Pos()))
					}
				}
			}
		}
		return true
	})
}
