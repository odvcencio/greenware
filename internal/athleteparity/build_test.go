package athleteparity

import "testing"

// The frozen athlete (the Python port) is built from outside the examples
// project, so this also covers serving the source inside the examples root.
// Every vertex and triangle must belong to a census part, and the parts'
// counts must add up to the file's.
func TestBuildFrozenAthlete(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the athlete")
	}
	repo, err := FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(BuildOptions{Source: repo + "/testdata/athlete-frozen/athlete.gw", RepoRoot: repo, Output: "match"})
	if err != nil {
		t.Fatal(err)
	}
	m := b.Model
	if b.Output != "match" || len(m.Bind) == 0 || len(m.Parts) == 0 {
		t.Fatalf("output %q, %d vertices, %d parts", b.Output, len(m.Bind), len(m.Parts))
	}
	for i, o := range m.Owner {
		if o < 0 {
			t.Fatalf("vertex %d has no owner", i)
		}
	}
	for i, o := range m.TriOwner {
		if o < 0 {
			t.Fatalf("triangle %d has no owner", i)
		}
	}
	var tris, verts int
	for _, p := range m.Parts {
		tris += p.Triangles
		verts += p.Vertices
	}
	if tris != len(m.Tri) || verts != len(m.Bind) {
		t.Errorf("parts hold %d triangles %d vertices, file %d and %d", tris, verts, len(m.Tri), len(m.Bind))
	}
	if !m.HasClip("ready") {
		t.Error("no ready clip")
	}
	// The pose at t = 0 stands upright and about 2 yards tall.
	w := m.Measure(m.Posed(nil, nil, 1, "ready", 0))
	if w.Height < 1.5 || w.Height > 2.5 {
		t.Errorf("standing height %.3f yd", w.Height)
	}
}
