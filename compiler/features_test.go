package compiler

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/greenware/diag"
)

// A small asset that exercises composition the goal post does not: a linear
// array, a grid, a template instance, an exported child part with its own
// offset, a mirrored pair, a bake `when` with an else branch, and a socket in
// an exported part.
const featureSource = `greenware 1

asset rig { root = rig }

param tall : switch = on
param colour : color = #336699 runtime

material red = pbr { base = #ff0000 roughness = 0.5 }
material tint = pbr { base = colour }

part peg {
  cylinder { radius = 0.1 height = 1 segments = 8 }
}

part rig {
  material = red
  array dots {
    mode = linear
    count = 3
    step = (1, 0, 0)
    sphere { radius = 0.1 segments = 8 }
  }
  array lattice {
    mode = grid
    count = (2, 2, 1)
    step = (1, 1, 1)
    instance { of = peg }
  }
  part arm {
    export = on
    at = (0, 1, 0)
    material = tint
    mirror {
      plane = x
      cylinder (a, b) { at = (1, 0, 0) radius = 0.1 height = 1 segments = 8 }
      socket (tip_l, tip_r) { kind = tip at = (1, 0.5, 0) }
    }
  }
  when tall {
    sphere { at = (0, 5, 0) radius = 0.2 segments = 8 }
  } else {
    sphere { at = (0, 2, 0) radius = 0.2 segments = 8 }
  }
}

output o { file = "rig.glb" }
`

func TestComposition(t *testing.T) {
	p, ds := Build(lowerSource(t, []byte(featureSource)), "")
	if p == nil {
		t.Fatalf("build failed: %v", ds)
	}
	f := p.Outputs[0].Files[0]
	// 3 dots (48 each) + 4 pegs (28 each) + 1 tall sphere (48) in the root's
	// red primitive; the arm's two cylinders (28 each) in its own.
	if f.Triangles != 3*48+4*28+48+2*28 {
		t.Errorf("triangles %d", f.Triangles)
	}
	if f.Draws != 2 || f.Materials != 2 {
		t.Errorf("draws %d, materials %d", f.Draws, f.Materials)
	}
	d := f.Doc
	var arm int = -1
	for i, n := range d.Nodes {
		if n.Name == "arm" {
			arm = i
		}
	}
	if arm < 0 || d.Nodes[arm].Translation == nil || *d.Nodes[arm].Translation != [3]float32{0, 1, 0} {
		t.Fatalf("arm node: %+v", d.Nodes)
	}
	// The arm's geometry is in the arm's frame: x in [-1.1, 1.1], y in [-0.5, 0.5].
	var lo, hi [3]float32
	first := true
	for _, pr := range d.Meshes[*d.Nodes[arm].Mesh].Primitives {
		for i := 0; i < len(pr.Positions); i += 3 {
			for k := 0; k < 3; k++ {
				v := pr.Positions[i+k]
				if first || v < lo[k] {
					lo[k] = v
				}
				if first || v > hi[k] {
					hi[k] = v
				}
			}
			first = false
		}
	}
	if math.Abs(float64(lo[0])+1.1) > 1e-6 || math.Abs(float64(hi[0])-1.1) > 1e-6 || math.Abs(float64(hi[1])-0.5) > 1e-6 {
		t.Errorf("arm bounds %v %v", lo, hi)
	}
	// A mirrored socket pair, as children of the arm, with the right sides.
	got := map[string][3]float32{}
	for _, n := range d.Nodes {
		if strings.HasPrefix(n.Name, "socket.") {
			got[n.Name] = *n.Translation
		}
	}
	if got["socket.tip_l"] != [3]float32{1, 0.5, 0} || got["socket.tip_r"] != [3]float32{-1, 0.5, 0} {
		t.Errorf("sockets %v", got)
	}
	for _, k := range d.Nodes[arm].Children {
		if !strings.HasPrefix(d.Nodes[k].Name, "socket.tip_") {
			t.Errorf("arm child %s", d.Nodes[k].Name)
		}
	}
	// The runtime colour is written at its default and bound.
	if !strings.Contains(string(d.Extras), `"material":"tint"`) {
		t.Errorf("extras: %s", d.Extras)
	}
	// The `when` took its Then branch: a sphere at y = 5, bounds reach 5.2.
	if hi := d.Meshes[*d.Nodes[0].Mesh].Primitives[0]; true {
		var top float32
		for i := 1; i < len(hi.Positions); i += 3 {
			if hi.Positions[i] > top {
				top = hi.Positions[i]
			}
		}
		if math.Abs(float64(top)-5.2) > 1e-5 {
			t.Errorf("highest y %v, want 5.2", top)
		}
	}
}

func TestDropByNameAndTemplate(t *testing.T) {
	src := featureSource + `
lod 1 {
  distance = 10m
  max_error = 5mm
  drop = [peg, a]
}
`
	p, ds := Build(lowerSource(t, []byte(src)), "")
	if p == nil {
		t.Fatalf("build failed: %v", ds)
	}
	l0, l1 := p.Outputs[0].Files[0], p.Outputs[0].Files[1]
	// The 4 pegs (28 each at lod 0) and the left cylinder are gone. The
	// spheres and the right cylinder are re-tessellated (radius 0.1 at 5 mm:
	// 8 segments to 5), so only check what was removed.
	if l1.Triangles >= l0.Triangles-4*28-28+1 {
		t.Errorf("lod 1 has %d triangles, lod 0 has %d", l1.Triangles, l0.Triangles)
	}
	if l1.Name != "rig-lod1.glb" {
		t.Errorf("name %s", l1.Name)
	}
}

func TestUnsupportedIsRefusedNotSubstituted(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"cone", strings.Replace(featureSource, "sphere { radius = 0.1 segments = 8 }\n  }\n  array lattice", "cone { radius = 0.1 height = 1 }\n  }\n  array lattice", 1), "cone"},
		{"socket rotation", strings.Replace(featureSource, "kind = tip at = (1, 0.5, 0)", "kind = tip at = (1, 0.5, 0) rot = (0, 90, 0)", 1), "rot"},
	} {
		p, ds := Build(lowerSource(t, []byte(c.src)), "")
		if p != nil {
			t.Errorf("%s: built", c.name)
			continue
		}
		found := false
		for _, d := range ds {
			if d.Code == diag.CodeUnsupported && strings.Contains(d.Message, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no GW-UNSUPPORTED naming %q in %v", c.name, c.want, ds)
		}
	}
}

func TestManifestIsReportedNotWritten(t *testing.T) {
	p := buildGoalPost(t)
	var manifest, selena bool
	for _, n := range p.Unsupported {
		manifest = manifest || strings.Contains(n, "manifest")
		selena = selena || strings.Contains(n, "selena")
	}
	if !manifest || !selena {
		t.Errorf("unsupported notes: %v", p.Unsupported)
	}
}

func TestNoOutputAndUnknownOutput(t *testing.T) {
	f := lowerSource(t, []byte("greenware 1\nasset a { root = a }\npart a { sphere { radius = 1 segments = 8 } }\n"))
	if p, ds := Build(f, ""); p != nil || len(ds) == 0 || ds[0].Code != diag.CodeMissing {
		t.Errorf("no output: %v", ds)
	}
	if p, ds := Build(loadFile(t, goalpostPath), "nope"); p != nil || len(ds) == 0 || ds[0].Code != diag.CodeUnknownName {
		t.Errorf("unknown output: %v", ds)
	}
}

// ---- no fused multiply-add ---------------------------------------------------

// TestNoUnwrappedMultiply fails when a float multiply is a direct operand of an
// add or subtract without float64(...) between. On arm64 the compiler would
// fuse the pair and change the bits.
func TestNoUnwrappedMultiply(t *testing.T) {
	fset := token.NewFileSet()
	names, _ := filepath.Glob("*.go")
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil), Error: func(error) {}}
	conf.Check("p", fset, files, info)
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
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch b := n.(type) {
			case *ast.BinaryExpr:
				if (b.Op == token.ADD || b.Op == token.SUB) && (isMul(b.X) || isMul(b.Y)) {
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
}
