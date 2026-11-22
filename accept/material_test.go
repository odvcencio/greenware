package accept

import (
	"strings"
	"testing"

	"m31labs.dev/greenware/gltf"
)

func TestAcceptMeasuresDecodedPBRFactors(t *testing.T) {
	acc := parseAccept(t, `greenware 1
accept test {
  outputs = [main]
  lods = [0]
  expect (material(paint).base == #800000, "base was encoded in linear space")
  expect (material(paint).alpha == 0.5, "alpha stays linear")
  expect (material(paint).metallic == 0.25, "metallic")
  expect (material(paint).roughness == 0.75, "roughness")
}
`)
	primitive := gltf.Primitive{
		Positions: []float32{0, 0, 0, 1, 0, 0, 0, 1, 0},
		Indices:   []uint32{0, 1, 2}, Material: gltf.Int(0),
	}
	doc := &gltf.Document{
		Nodes:  []gltf.Node{{Name: "paint", Mesh: gltf.Int(0)}},
		Meshes: []gltf.Mesh{{Primitives: []gltf.Primitive{primitive}}},
		Materials: []gltf.Material{
			{BaseColor: [4]float32{0.2158605001, 0, 0, 0.5}, Metallic: 0.25, Roughness: 0.75},
			{BaseColor: [4]float32{0, 1, 0, 1}, Roughness: 1},
		},
	}
	run := func() *Report {
		report, _ := Run(acc, Inputs{{"main", 0}: {Doc: roundTrip(t, doc)}})
		return report
	}
	if report := run(); report.Totals.Pass != 4 {
		t.Fatalf("PBR values: %+v", report.Levels)
	}
	primitive.Material = gltf.Int(1)
	doc.Meshes[0].Primitives = append(doc.Meshes[0].Primitives, primitive)
	report := run()
	if report.Totals.Refused != 4 || !strings.Contains(report.Levels[0].Expects[0].Detail, "more than one visible material") {
		t.Fatalf("measurements accepted an ambiguous material: %+v", report.Levels)
	}
}
