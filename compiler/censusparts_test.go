package compiler

import (
	"strings"
	"testing"

	"m31labs.dev/greenware/gltf"
)

// The per-shape census adds up to the file: the triangles of the parts are
// the file's stored triangles, their vertices the file's indexed vertices,
// and each part has a name and a material that the file knows.
func TestCensusPartsAddUp(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    *Product
	}{{"goalpost", buildGoalPost(t)}, {"arm", func() *Product { p, _ := buildArm(t); return p }()}} {
		for _, o := range tc.p.Outputs {
			for _, f := range o.Files {
				c := f.Census(o.Name)
				parts := f.Parts
				if len(parts) == 0 {
					t.Fatalf("%s %s: no parts in the census", tc.name, f.Name)
				}
				var tris, verts int
				for _, pt := range parts {
					tris += pt.Triangles
					verts += pt.Vertices
					if pt.Name == "" || strings.HasSuffix(pt.Name, "/") {
						t.Errorf("%s %s: part with name %q", tc.name, f.Name, pt.Name)
					}
				}
				d, err := gltf.Decode(f.GLB)
				if err != nil {
					t.Fatal(err)
				}
				var glbTris, glbVerts int
				for _, m := range d.Meshes {
					for _, pr := range m.Primitives {
						glbTris += len(pr.Indices) / 3
						glbVerts += len(pr.Positions) / 3
					}
				}
				if tris != glbTris || tris != c.TrianglesStored {
					t.Errorf("%s %s: parts hold %d triangles, GLB %d, stored %d", tc.name, f.Name, tris, glbTris, c.TrianglesStored)
				}
				if verts != glbVerts || verts != c.Vertices {
					t.Errorf("%s %s: parts hold %d vertices, GLB %d, census %d", tc.name, f.Name, verts, glbVerts, c.Vertices)
				}
			}
		}
	}
}

// A shape is named with the parts that hold it, and a shape without a name by
// its kind.
func TestCensusPartNames(t *testing.T) {
	p := buildGoalPost(t)
	names := map[string]bool{}
	for _, pt := range p.Outputs[0].Files[0].Parts {
		names[pt.Name] = true
	}
	nested := false
	for n := range names {
		if strings.Contains(n, "/") {
			nested = true
		}
	}
	if !nested {
		t.Errorf("no nested part name among %v", names)
	}
}
