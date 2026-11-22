package compiler

import (
	"math"
	"testing"

	"m31labs.dev/greenware/accept"
	"m31labs.dev/greenware/gltf"
)

func TestDecodedRingCorrespondenceControl(t *testing.T) {
	f := lowerSource(t, []byte(`greenware 1
asset cylinder { root = cylinder }
material steel = pbr { base = #909090 roughness = 0.5 metallic = 1 }
part cylinder {
  material = steel
  cylinder { radius = 1m height = 2m segments = 12 caps = on }
}
output main { file = "cylinder.glb" }
`))
	product, ds := Build(f, "")
	if product == nil {
		t.Fatalf("build: %v", ds)
	}
	doc := decode(t, product.Outputs[0].Files[0])
	primitive := &doc.Meshes[0].Primitives[0]
	spec := lowerSource(t, []byte(`greenware 1
accept cylinder {
  of = "cylinder.gw"
  outputs = [main]
  lods = all
  expect (closed(node(cylinder)), "closed")
  expect (connected(node(cylinder)), "connected")
  expect (bounds().max.y == 1m && bounds().min.y == -1m, "bounds")
}
`))
	checkTopology := func(d *gltf.Document) {
		t.Helper()
		in := accept.Inputs{{Output: "main", LOD: 0}: {Doc: d, File: "cylinder.glb"}}
		report, _ := accept.Run(spec.Accepts[0], in)
		if report.Totals.Pass != 3 || report.ExitCode() != 0 {
			t.Fatalf("controls need a closed, connected cylinder with unchanged bounds: %+v", report.Levels)
		}
	}
	checkTopology(doc)

	// Rotate just the upper side-ring index mapping by two columns, while
	// leaving the cap indices and all vertex positions intact.
	const segments = 12
	column := func(p []float32) int {
		a := math.Atan2(float64(p[0]), float64(p[2]))
		return (int(math.Round(a*segments/(2*math.Pi))) + segments) % segments
	}
	side := primitive.Indices[:6*segments]
	var upper [segments]uint32
	var found [segments]bool
	for _, i := range side {
		p := primitive.Positions[3*i : 3*i+3]
		if p[1] == 1 {
			c := column(p)
			upper[c], found[c] = i, true
		}
	}
	for c, have := range found {
		if !have {
			t.Fatalf("side strip has no upper vertex in column %d", c)
		}
	}
	original := longestCrossRingEdge(primitive)
	for j, i := range side {
		p := primitive.Positions[3*i : 3*i+3]
		if p[1] == 1 {
			side[j] = upper[(column(p)+2)%segments]
		}
	}
	badGLB, err := gltf.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := gltf.Decode(badGLB)
	if err != nil {
		t.Fatal(err)
	}
	checkTopology(bad)

	// A side-strip edge can span at most one angular step on this fixture.
	// Its longest possible length is the diagonal of a 2 m by one-chord face.
	chord := 2 * math.Sin(math.Pi/segments)
	limit := math.Hypot(2, chord) + 1e-6
	altered := longestCrossRingEdge(&bad.Meshes[0].Primitives[0])
	if original <= 0 || original > limit || altered <= limit {
		t.Fatalf("cross-ring lengths: correct %.6fm, mis-joined %.6fm, limit %.6fm", original, altered, limit)
	}
	t.Logf("cross-ring lengths: correct %.6fm, mis-joined %.6fm, limit %.6fm", original, altered, limit)
}

func longestCrossRingEdge(p *gltf.Primitive) float64 {
	var longest float64
	for i := 0; i < len(p.Indices); i += 3 {
		for j := range 3 {
			a, b := p.Indices[i+j]*3, p.Indices[i+(j+1)%3]*3
			if p.Positions[a+1] == p.Positions[b+1] {
				continue
			}
			dx := float64(p.Positions[a]) - float64(p.Positions[b])
			dy := float64(p.Positions[a+1]) - float64(p.Positions[b+1])
			dz := float64(p.Positions[a+2]) - float64(p.Positions[b+2])
			longest = max(longest, math.Hypot(math.Hypot(dx, dy), dz))
		}
	}
	return longest
}
