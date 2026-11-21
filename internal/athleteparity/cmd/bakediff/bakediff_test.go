package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/lang"
)

// tiny returns a document with two skinned-free mesh nodes "a" (a quad with a
// morph target) and "b" (a triangle), a two-joint skin on node "a", one clip
// and one material.
func tiny() *gltf.Document {
	return &gltf.Document{
		Asset:  gltf.Asset{Generator: "test"},
		Scene:  gltf.Int(0),
		Scenes: []gltf.Scene{{Nodes: []int{0, 3, 4}}},
		Nodes: []gltf.Node{
			{Name: "root", Children: []int{1}},
			{Name: "j0", Children: []int{2}, Translation: &[3]float32{0, 0, 0}},
			{Name: "j1", Translation: &[3]float32{0, 1, 0}},
			{Name: "a", Mesh: gltf.Int(0), Skin: gltf.Int(0)},
			{Name: "b", Mesh: gltf.Int(1)},
		},
		Meshes: []gltf.Mesh{{
			Name: "a",
			Primitives: []gltf.Primitive{{
				Positions: []float32{0, 0, 0, 1, 0, 0, 1, 2, 0, 0, 2, 0},
				Normals:   []float32{0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1},
				UV0:       []float32{0, 0, 1, 0, 1, 1, 0, 1},
				Indices:   []uint32{0, 1, 2, 0, 2, 3},
				Joints0:   []uint16{0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0},
				Weights0:  []float32{1, 0, 0, 0, 1, 0, 0, 0, 0.25, 0.75, 0, 0, 0.25, 0.75, 0, 0},
				Targets: []gltf.MorphTarget{
					{Positions: []float32{0, 0, 0, 0, 0, 0, 0, 0.5, 0, 0, 0.5, 0}},
				},
			}},
			Weights:     []float32{0},
			TargetNames: []string{"lift"},
		}, {
			Name: "b",
			Primitives: []gltf.Primitive{{
				Positions: []float32{5, 0, 0, 6, 0, 0, 5, 1, 0},
				Indices:   []uint32{0, 1, 2},
			}},
		}},
		Skins: []gltf.Skin{{Name: "s", Joints: []int{1, 2}, InverseBindMatrices: [][16]float32{
			{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1},
			{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, -1, 0, 1},
		}}},
		Materials: []gltf.Material{{Name: "m", BaseColor: [4]float32{1, 1, 1, 1}, Roughness: 0.5}},
		Animations: []gltf.Animation{{
			Name:     "walk",
			Channels: []gltf.Channel{{Node: 2, Path: gltf.PathTranslation, Sampler: 0}},
			Samplers: []gltf.AnimSampler{{Input: []float32{0, 1}, Output: []float32{0, 1, 0, 0, 2, 0}}},
		}},
	}
}

func encoded(t *testing.T, d *gltf.Document) *file {
	t.Helper()
	b, err := gltf.Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := gltf.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	f, err := build("x.glb", len(b), d2, nil)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSelfIsZero(t *testing.T) {
	r := compare(encoded(t, tiny()), encoded(t, tiny()))
	assertZero(t, r)
	if r.Shells.Matched != 2 || len(r.Shells.Changed) != 0 {
		t.Fatalf("matched %d changed %d", r.Shells.Matched, len(r.Shells.Changed))
	}
}

func assertZero(t *testing.T, r *Report) {
	t.Helper()
	if len(r.Shells.Added)+len(r.Shells.Removed)+len(r.Shells.Resized)+len(r.Shells.Changed) != 0 ||
		len(r.Morph.Added)+len(r.Morph.Removed)+len(r.Morph.Changed) != 0 ||
		len(r.Clips.Added)+len(r.Clips.Removed)+len(r.Clips.Changed) != 0 ||
		len(r.Joints.Diffs)+len(r.Joints.NodesAdded)+len(r.Joints.NodesRemoved)+len(r.Joints.JointsAdded)+len(r.Joints.JointsGone) != 0 ||
		r.Joints.OrderChanged || len(r.Assets.Materials) != 0 || len(r.Assets.Images) != 0 ||
		r.Assets.BytesBefore != r.Assets.BytesAfter || r.Shells.OutOfOrder != 0 {
		j, _ := json.MarshalIndent(r, "", " ")
		t.Fatalf("expected no differences:\n%s", j)
	}
}

func TestSyntheticChanges(t *testing.T) {
	a := tiny()
	b := tiny()
	// shell "a": move vertex 2, change a UV, a normal, a weight, flip the diagonal.
	p := &b.Meshes[0].Primitives[0]
	p.Positions[6] += 0.25
	p.UV0[4] = 0.5
	p.Normals[8], p.Normals[6] = 0, 1
	p.Weights0[8], p.Weights0[9] = 0.5, 0.5
	p.Indices = []uint32{0, 1, 3, 1, 2, 3}
	// morph target: change one delta, add another target.
	p.Targets[0].Positions[7] = 0.75
	p.Targets = append(p.Targets, gltf.MorphTarget{Positions: make([]float32, 12)})
	b.Meshes[0].TargetNames = []string{"lift", "wide"}
	b.Meshes[0].Weights = []float32{0, 0}
	// shell "b": gets a vertex (resized).
	q := &b.Meshes[1].Primitives[0]
	q.Positions = append(q.Positions, 6, 1, 0)
	q.Indices = []uint32{0, 1, 2, 2, 1, 3}
	// a new mesh node.
	b.Meshes = append(b.Meshes, gltf.Mesh{Name: "c", Primitives: []gltf.Primitive{{Positions: []float32{0, 0, 0, 1, 0, 0, 0, 1, 0}, Indices: []uint32{0, 1, 2}}}})
	b.Nodes = append(b.Nodes, gltf.Node{Name: "c", Mesh: gltf.Int(2)})
	b.Scenes[0].Nodes = append(b.Scenes[0].Nodes, 5)
	// clips: change a key; add a clip.
	b.Animations[0].Samplers[0].Output[4] = 2.5
	b.Animations = append(b.Animations, gltf.Animation{Name: "run",
		Channels: []gltf.Channel{{Node: 2, Path: gltf.PathTranslation, Sampler: 0}},
		Samplers: []gltf.AnimSampler{{Input: []float32{0}, Output: []float32{0, 0, 0}}}})
	// joints: move j1's bind translation and its inverse bind matrix.
	b.Nodes[2].Translation = &[3]float32{0, 1.5, 0}
	b.Skins[0].InverseBindMatrices[1][13] = -1.5
	b.Materials[0].Roughness = 0.9

	r := compare(encoded(t, a), encoded(t, b))
	if len(r.Shells.Added) != 1 || r.Shells.Added[0].Name != "c" || len(r.Shells.Removed) != 0 {
		t.Errorf("added/removed: %+v %+v", r.Shells.Added, r.Shells.Removed)
	}
	if len(r.Shells.Resized) != 1 || r.Shells.Resized[0] != "b" {
		t.Errorf("resized: %v", r.Shells.Resized)
	}
	var sa *ShellDiff
	for i := range r.Shells.Changed {
		if r.Shells.Changed[i].Name == "a" {
			sa = &r.Shells.Changed[i]
		}
	}
	if sa == nil {
		t.Fatalf("shell a not changed: %+v", r.Shells.Changed)
	}
	if sa.Match != "index" || sa.Moved.Count != 1 || sa.Moved.Max < 0.2499 || sa.Moved.Max > 0.2501 || len(sa.Moved.Top) != 1 || sa.Moved.Top[0] != 2 {
		t.Errorf("moved: %+v", sa.Moved)
	}
	if sa.UV.Count != 1 || sa.Normal.Count != 1 || sa.Weight.Count != 1 {
		t.Errorf("uv %+v normal %+v weight %+v", sa.UV, sa.Normal, sa.Weight)
	}
	if !sa.Tris.Compared || sa.Tris.OnlyBefore != 2 || sa.Tris.OnlyAfter != 2 {
		t.Errorf("triangles: %+v", sa.Tris)
	}
	if len(r.Morph.Added) != 1 || r.Morph.Added[0] != "wide" || len(r.Morph.Changed) != 1 || r.Morph.Changed[0].Name != "lift" ||
		r.Morph.Changed[0].MaxChange < 0.2499 || r.Morph.Changed[0].Shells[0] != "a" {
		t.Errorf("morph: %+v", r.Morph)
	}
	if len(r.Clips.Added) != 1 || r.Clips.Added[0] != "run" || len(r.Clips.Changed) != 1 || r.Clips.Changed[0].MaxDiff < 0.49 {
		t.Errorf("clips: %+v", r.Clips)
	}
	var what []string
	for _, d := range r.Joints.Diffs {
		what = append(what, d.Name+":"+d.What)
	}
	got := strings.Join(what, "|")
	if !strings.Contains(got, "j1:inverse bind matrix") || !strings.Contains(got, "j1:bind translation") {
		t.Errorf("joint diffs: %v", what)
	}
	if len(r.Joints.NodesAdded) != 1 || len(r.Assets.Materials) != 1 {
		t.Errorf("nodes %v materials %v", r.Joints.NodesAdded, r.Assets.Materials)
	}
	var buf bytes.Buffer
	text(&buf, r, "shell")
	for _, want := range []string{"added (1): c", "resized (1): b", "top vertices [2]", "targets added (1): wide", "clips added (1): run"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, buf.String())
		}
	}
}

func TestResizedFallsBackToNearest(t *testing.T) {
	a := tiny()
	b := tiny()
	q := &b.Meshes[1].Primitives[0]
	q.Positions = append(q.Positions, 6, 1, 0)
	q.Indices = []uint32{0, 1, 2, 2, 1, 3}
	r := compare(encoded(t, a), encoded(t, b))
	for _, d := range r.Shells.Changed {
		if d.Name == "b" {
			if !strings.HasPrefix(d.Match, "nearest") || d.Tris.Compared || d.Moved.Count != 0 {
				t.Errorf("b: %+v", d)
			}
			return
		}
	}
	t.Fatal("shell b not reported")
}

// Shells found as connected pieces of one primitive pair by content, so a
// reordering of the pieces is not a change of their vertices.
func TestPiecesPairByContent(t *testing.T) {
	two := func(swap bool) *gltf.Document {
		d := tiny()
		d.Nodes = d.Nodes[:4]
		d.Scenes[0].Nodes = []int{0, 3}
		d.Meshes = d.Meshes[:1]
		p := &d.Meshes[0].Primitives[0]
		p.Targets = nil
		d.Meshes[0].Weights, d.Meshes[0].TargetNames = nil, nil
		tri := []float32{0, 0, 0, 1, 0, 0, 0, 1, 0}
		far := []float32{5, 0, 0, 7, 0, 0, 5, 2, 0}
		first, second := tri, far
		if swap {
			first, second = far, tri
		}
		p.Positions = append(append([]float32{}, first...), second...)
		p.Normals, p.UV0 = nil, nil
		p.Indices = []uint32{0, 1, 2, 3, 4, 5}
		p.Joints0 = make([]uint16, 24)
		p.Weights0 = []float32{1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0}
		return d
	}
	r := compare(encoded(t, two(false)), encoded(t, two(true)))
	if r.Shells.OutOfOrder != 1 || len(r.Shells.Added)+len(r.Shells.Removed) != 0 || r.Shells.Matched != 2 {
		t.Fatalf("%+v", r.Shells)
	}
	if len(r.Shells.Changed) != 0 {
		t.Fatalf("vertices moved though the pieces only swapped: %+v", r.Shells.Changed)
	}
}

func TestScaleOption(t *testing.T) {
	b := tiny()
	scaleDoc(b, 2)
	fa, fb := encoded(t, tiny()), encoded(t, b)
	if r := compare(fa, fb); len(r.Shells.Changed) == 0 {
		t.Fatal("a doubled mesh should differ")
	}
	scaleDoc(fb.Doc, 0.5)
	fb2, err := build("x.glb", fb.Bytes, fb.Doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := compare(fa, fb2); len(r.Shells.Changed) != 0 {
		t.Fatalf("scaled back, still differs: %+v", r.Shells.Changed)
	}
}

func TestRunJSONAndSummary(t *testing.T) {
	dir := t.TempDir()
	b, err := gltf.Encode(tiny())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "a.glb")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"-json", p, p}, &out); err != nil {
		t.Fatal(err)
	}
	var r Report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	assertZero(t, &r)
	out.Reset()
	if err := run([]string{"-by", "summary", p, p}, &out); err != nil || !strings.Contains(out.String(), "== 5.") {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if err := run([]string{"-by", "x", p, p}, &out); err == nil {
		t.Fatal("bad -by accepted")
	}
}

// A committed rigged GLB and the compiled goal post compare as all zero.
func TestCommittedFilesCompareToThemselves(t *testing.T) {
	p := filepath.Join("..", "..", "..", "..", "gltf", "testdata", "rigged.glb")
	f1, err := loadFile(p, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	f2, _ := loadFile(p, nil, 1)
	assertZero(t, compare(f1, f2))

	src, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "syntax", "goalpost.gw"))
	if err != nil {
		t.Fatal(err)
	}
	f, ds := ast.Lower(lang.Parse(src))
	if diag.HasErrors(ds) {
		t.Fatal(ds)
	}
	prod, ds := compiler.Build(f, "")
	if prod == nil {
		t.Fatal(ds)
	}
	glb := prod.Outputs[0].Files[0].GLB
	d1, err := gltf.Decode(glb)
	if err != nil {
		t.Fatal(err)
	}
	d2, _ := gltf.Decode(glb)
	g1, err := build("goalpost.glb", len(glb), d1, nil)
	if err != nil {
		t.Fatal(err)
	}
	g2, _ := build("goalpost.glb", len(glb), d2, nil)
	if len(g1.P) == 0 {
		t.Fatal("empty goal post")
	}
	assertZero(t, compare(g1, g2))
}
