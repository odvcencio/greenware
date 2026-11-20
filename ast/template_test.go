package ast_test

import (
	"os"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
)

func TestTemplateAndRowCallLower(t *testing.T) {
	src := `greenware 1

asset a { root = a }

template rail(y0: length, reach: length = 0.2, kind: enum(a, b) = a) {
  tube {
    radius = 0.01
    sample t in 0deg..90deg by 4 -> point (reach * sin(t), y0, 0)
  }
}

part a {
  rail (1.5, reach = 0.3)
  part b { rail (2) }
}
`
	f, ds := lower(t, src)
	if diag.HasErrors(ds) {
		t.Fatal(ds)
	}
	if len(f.Templates) != 1 {
		t.Fatalf("%d templates", len(f.Templates))
	}
	tpl := f.Templates[0]
	if tpl.Name.Name != "rail" || len(tpl.Params) != 3 || tpl.Params[0].Type.Name != "length" || tpl.Params[0].Default != nil || tpl.Params[1].Default == nil {
		t.Fatalf("template %+v", tpl)
	}
	if tpl.Params[2].Type.Name != "enum" || len(tpl.Params[2].Type.Enum) != 2 || tpl.Params[2].Type.Enum[1].Name != "b" {
		t.Fatalf("enum formal %+v", tpl.Params[2].Type)
	}
	if len(tpl.Items) != 1 {
		t.Fatalf("template body: %v", tpl.Items)
	}
	if sh, ok := tpl.Items[0].(*ast.Shape); !ok || sh.Kind != "tube" || len(sh.Rows) != 1 {
		t.Fatalf("template body item %T", tpl.Items[0])
	}
	p := f.Parts[0]
	call, ok := p.Items[0].(*ast.TemplateCall)
	if !ok || call.Name != "rail" || len(call.Args) != 2 || call.Args[1].Name != "reach" {
		t.Fatalf("first item %T %+v", p.Items[0], p.Items[0])
	}
	if src[call.Span.Start:call.Span.End] != "rail (1.5, reach = 0.3)" || src[call.NameSpan.Start:call.NameSpan.End] != "rail" {
		t.Errorf("spans %q %q", src[call.Span.Start:call.Span.End], src[call.NameSpan.Start:call.NameSpan.End])
	}
	inner := p.Items[1].(*ast.Part)
	if _, ok := inner.Items[0].(*ast.TemplateCall); !ok {
		t.Fatalf("a row in a nested part is %T", inner.Items[0])
	}
}

func TestRowInCollisionIsRefused(t *testing.T) {
	_, ds := lower(t, "greenware 1\n\nasset a { root = a }\npart a { }\ncollision {\n  rail (1)\n}\n")
	if !diag.HasErrors(ds) {
		t.Fatal("a row in collision was accepted")
	}
}

func TestLoftTubeEllipsoidLower(t *testing.T) {
	src := `greenware 1

asset a { root = a }

part a {
  loft {
    segments = 24
    caps = off
    columns = (y, rx, rz)
    vertex = (rx * sin(a), y, rz * cos(a))
    arc = 110deg..250deg
    ring (1.7, 0.1, 0.1)
    ring (1.8, 0.1, 0.1)
  }
  ellipsoid chin { centre = (0, 1, 0) radii = (1, 2, 3) rings = 5 }
  mirror { plane = x
    tube (s_l, s_r) { radius = 0.1 point (0, 0, 0) point (1, 0, 0) }
  }
}
`
	f, ds := lower(t, src)
	if diag.HasErrors(ds) {
		t.Fatal(ds)
	}
	loft := f.Parts[0].Items[0].(*ast.Shape)
	if loft.Kind != "loft" || len(loft.Rows) != 2 || ast.FindSetting(loft.Settings, "vertex") == nil || ast.FindSetting(loft.Settings, "arc").Range == nil {
		t.Fatalf("loft %+v", loft)
	}
	if e := f.Parts[0].Items[1].(*ast.Shape); e.Kind != "ellipsoid" || e.Name.Name != "chin" {
		t.Fatalf("ellipsoid %+v", e)
	}
	m := f.Parts[0].Items[2].(*ast.Mirror)
	if tb := m.Items[0].(*ast.Shape); tb.Kind != "tube" || tb.Name.Name != "s_l" || tb.Pair.Name != "s_r" {
		t.Fatalf("tube %+v", tb)
	}
	// A ring row takes no name and a loft takes no blocks.
	_, ds = lower(t, "greenware 1\n\nasset a { root = a }\npart a { loft { path { } } }\n")
	if !diag.HasErrors(ds) {
		t.Error("a loft accepted a path block")
	}
}

func TestGeometryOnlyOptions(t *testing.T) {
	src, err := os.ReadFile("../examples/helmet/helmet.gw")
	if err != nil {
		t.Fatal(err)
	}
	f, ds := ast.LowerWith(lang.Parse(src), ast.Options{GeometryOnly: true})
	if diag.HasErrors(ds) {
		t.Fatal(ds)
	}
	want := []string{
		"import",
		"plug head_gear",
		"texture helmet_mask",
		"material helmet_paint: drawn as a plain grey pbr material",
		"material visor_glass: drawn as a plain grey pbr material",
		"`cell` settings (atlas cells)",
	}
	if len(f.Skipped) != len(want) {
		t.Fatalf("skipped %q", f.Skipped)
	}
	for i := range want {
		if f.Skipped[i] != want[i] {
			t.Errorf("skipped[%d] = %q, want %q", i, f.Skipped[i], want[i])
		}
	}
	if len(f.Templates) != 1 || len(f.Params) != 9 || len(f.Outputs) != 2 || len(f.Materials) != 2 {
		t.Errorf("templates %d params %d outputs %d materials %d", len(f.Templates), len(f.Params), len(f.Outputs), len(f.Materials))
	}
	// A normal lowering of the same file has none of this.
	g, _ := ast.Lower(lang.Parse(src))
	if len(g.Skipped) != 0 {
		t.Errorf("a full lowering skipped %q", g.Skipped)
	}
}
