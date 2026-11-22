package accept

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"
	"testing/fstest"

	"m31labs.dev/greenware/gltf"
)

func TestOutlineLockedCamera(t *testing.T) {
	acc := parseAccept(t, `greenware 1
accept fixture {
  outputs = [main]
  lods = [0]
  expect (outline(front, "ref-{output}.png", within = 0px) == 1, "locked view")
}
`)
	// The identity matrices project this quad to [1,3] x [1,3] pixel edges.
	// The expected mask is set by hand and includes both sides of the shared
	// diagonal. A one-pixel parent translation must fail at zero tolerance.
	identity4 := []float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	camera, err := json.Marshal(outlineCamera{
		Schema: outlineSchema, View: "front", Width: 4, Height: 4, Eye: identity4, Clip: identity4,
	})
	if err != nil {
		t.Fatal(err)
	}
	mask := image.NewGray(image.Rect(0, 0, 4, 4))
	for y := 1; y < 3; y++ {
		for x := 1; x < 3; x++ {
			mask.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, mask); err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{
		"ref-main.png":           {Data: pngBytes.Bytes()},
		"ref-main.png.view.json": {Data: camera},
	}
	zero := 0
	doc := &gltf.Document{
		Nodes: []gltf.Node{{Children: []int{1}}, {Mesh: &zero}},
		Meshes: []gltf.Mesh{{Primitives: []gltf.Primitive{{
			Positions: []float32{-.5, -.5, 0, .5, -.5, 0, .5, .5, 0, -.5, .5, 0},
			Indices:   []uint32{0, 1, 2, 0, 2, 3},
		}}}},
	}
	run := func() ExpectReport {
		t.Helper()
		rep, _ := runWithReferences(acc, Inputs{{"main", 0}: {Doc: roundTrip(t, doc)}}, nil,
			&outlineRefs{fs: files, loaded: make(map[string]outlineInput)})
		return rep.Levels[0].Expects[0]
	}
	good := run()
	if good.Verdict != Pass || len(good.Measures) != 3 || good.Measures[2].Value != "4 of 4 reference pixels" {
		t.Fatalf("locked reference verdict: %+v", good)
	}
	doc.Nodes[0].Translation = &[3]float32{.5, 0, 0}
	bad := run()
	if bad.Verdict != Fail || len(bad.Measures) != 3 || bad.Measures[2].Value != "2 of 4 reference pixels" {
		t.Fatalf("shifted reference verdict: %+v", bad)
	}
	delete(files, "ref-main.png.view.json")
	missing := run()
	if missing.Verdict != Unavailable || !strings.Contains(missing.Detail, ".view.json") {
		t.Fatalf("missing camera verdict: %+v", missing)
	}
}

func TestOutlineEuclideanDistanceMatchesBruteForce(t *testing.T) {
	const w, h = 7, 5
	ref, rendered := make([]bool, w*h), make([]bool, w*h)
	for i := range ref {
		ref[i] = i%3 != 0
	}
	for pattern := range 23 {
		for i := range rendered {
			rendered[i] = pattern > 0 && (i*i+7*pattern+i*pattern)%13 < pattern%5
		}
		for _, tolerance := range []float64{0, 1, math.Sqrt2, 2.1, 4, 8} {
			want, count := 0, 0
			for i, check := range ref {
				if !check {
					continue
				}
				count++
				for j, drawn := range rendered {
					x, y := i%w-j%w, i/w-j/w
					if drawn && float64(x*x+y*y) <= tolerance*tolerance {
						want++
						break
					}
				}
			}
			got, total := scoreBoundary(ref, rendered, w, h, tolerance)
			if got != want || total != count {
				t.Fatalf("pattern %d tolerance %g, got %d/%d, want %d/%d", pattern, tolerance, got, total, want, count)
			}
		}
	}
}
