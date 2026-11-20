package ast_test

import (
	"os"
	"strings"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
)

func TestArmLowers(t *testing.T) {
	src, err := os.ReadFile("../examples/arm/arm.gw")
	if err != nil {
		t.Fatal(err)
	}
	f, ds := lower(t, string(src))
	for _, d := range ds {
		t.Errorf("%v", d)
	}
	var kinds []string
	for _, r := range f.Rigs {
		kinds = append(kinds, r.Kind+":"+r.Name.Name)
	}
	if got := strings.Join(kinds, " "); got != "skeleton:arm_rig skin: morph:long morph:bulk clip:rest clip:wave" {
		t.Errorf("rig declarations %q", got)
	}
	sk := f.Rigs[0]
	// The joint tree is nested as written: shoulder holds elbow holds wrist.
	j := sk.Items[0].(*ast.RigBlock)
	if j.Kind != "joint" || j.Name.Name != "shoulder" {
		t.Fatalf("first item %+v", j)
	}
	var names []string
	var walk func(items []ast.RigItem)
	walk = func(items []ast.RigItem) {
		for _, it := range items {
			if b, ok := it.(*ast.RigBlock); ok {
				if b.Kind == "joint" {
					names = append(names, b.Name.Name)
				}
				walk(b.Items)
			}
		}
	}
	walk(sk.Items)
	if got := strings.Join(names, " "); got != "shoulder elbow wrist" {
		t.Errorf("joints %q", got)
	}
	// Skin steps keep their written order.
	var steps []string
	for _, it := range f.Rigs[1].Items {
		b := it.(*ast.RigBlock)
		steps = append(steps, b.Kind+":"+b.Name.Name)
	}
	if got := strings.Join(steps, " "); got != "lengthen: blend:elbow_band blend:wrist_band" {
		t.Errorf("skin steps %q", got)
	}
	// A clip keeps its lets and its pose block.
	wave := f.Rigs[5]
	var sawLet, sawPose bool
	for _, it := range wave.Items {
		switch x := it.(type) {
		case *ast.Let:
			sawLet = x.Name.Name == "s"
		case *ast.RigBlock:
			sawPose = x.Kind == "pose"
		}
	}
	if !sawLet || !sawPose {
		t.Errorf("clip items: let %v pose %v", sawLet, sawPose)
	}
}

func TestRigLoweringDiagnostics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
		frag string
	}{
		{"unknown joint field", "greenware 1\nasset a { root = a }\nskeleton { joint j { at = (0, 0, 0) lenght = 1 } }\npart a { }\n", diag.CodeUnknownField, "lenght"},
		{"unknown block in skeleton", "greenware 1\nasset a { root = a }\nskeleton { jint j { at = (0, 0, 0) } }\npart a { }\n", diag.CodeUnknownField, "jint"},
		{"joint needs a name", "greenware 1\nasset a { root = a }\nskeleton { joint { at = (0, 0, 0) } }\npart a { }\n", diag.CodeMissing, "needs a name"},
		{"pair outside a mirror", "greenware 1\nasset a { root = a }\nskeleton { joint (l, r) { at = (0, 0, 0) } }\npart a { }\n", diag.CodeMirrorName, "mirror"},
		{"morph field", "greenware 1\nasset a { root = a }\nmorph m { wieght = 1 }\npart a { }\n", diag.CodeUnknownField, "wieght"},
		{"clip row name", "greenware 1\nasset a { root = a }\nclip c { kay (0s, (0, 0, 0)) }\npart a { }\n", diag.CodeUnknownField, "kay"},
		{"skin field", "greenware 1\nasset a { root = a }\nskin { max_influence = 2 }\npart a { }\n", diag.CodeUnknownField, "max_influence"},
		{"mount stays unsupported", "greenware 1\nasset a { root = a }\nmount m { asset = x socket = y }\npart a { }\n", diag.CodeUnsupported, "mount"},
		{"rows in a joint", "greenware 1\nasset a { root = a }\nskeleton { joint j { at = (0, 0, 0) key (0s, (0, 0, 0)) } }\npart a { }\n", diag.CodeUnknownField, "takes no rows"},
		{"waive", "greenware 1\nasset a { root = a }\nskeleton { joint j { at = (0, 0, 0) waive GW-RIG-BAND \"because\" } }\npart a { }\n", diag.CodeUnsupported, "waive"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ds := lower(t, c.src)
			for _, d := range ds {
				if d.Code == c.code && strings.Contains(d.Message, c.frag) {
					return
				}
			}
			t.Errorf("want %s mentioning %q, got %v", c.code, c.frag, ds)
		})
	}
}
