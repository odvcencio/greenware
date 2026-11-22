package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"testing"

	"m31labs.dev/gosx/scene"
	"m31labs.dev/greenware/gltf"
)

func TestGoalPostFlagSockets(t *testing.T) {
	source, err := os.ReadFile("../goalpost/goalpost.gw")
	if err != nil {
		t.Fatal(err)
	}
	posts, err := compileGoalposts(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 6 {
		t.Fatalf("got %d levels, want six", len(posts))
	}
	for _, post := range posts {
		if left, right := post.sockets[0], post.sockets[1]; left[0]*right[0] >= 0 || left[1] != right[1] || left[1] < 6 {
			t.Fatalf("%s level %d has unexpected ribbon sockets %v", post.variant, post.lod, post.sockets)
		}
	}
	demo, err := newDemo(source, "")
	if err != nil {
		t.Fatal(err)
	}
	request := func(path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		demo.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s: got HTTP %d", path, recorder.Code)
		}
		return recorder
	}
	for _, post := range posts {
		model := request(assetURL(post, "frame"))
		if !bytes.Equal(model.Body.Bytes(), post.model) {
			t.Fatal("demo changed the compiled goal-post bytes")
		}
		for _, wind := range winds {
			props := sceneProps(post, wind, true)
			if props.Environment.Wind != wind.field {
				t.Fatal("scene and flags must share a wind field")
			}
			for i, socket := range post.sockets {
				name := ribbonNames[i]
				path := assetURL(post, wind.id+"/"+name)
				if page := request("/?post=" + post.variant + "&lod=" + strconv.Itoa(post.lod) + "&wind=" + wind.id); !bytes.Contains(page.Body.Bytes(), []byte(path)) {
					t.Fatalf("%s missing from page", path)
				}
				asset := request(path)
				if got := asset.Header().Get("Content-Type"); got != "model/gltf-binary" {
					t.Fatalf("asset media type %q", got)
				}
				want, err := makeFlag(wind.field, socket)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(asset.Body.Bytes(), want) {
					t.Fatalf("%s did not use the wind at its socket", path)
				}
				matched := false
				for _, node := range props.Graph.Nodes {
					model, ok := node.(scene.Model)
					if ok && model.ID == name {
						matched = model.Position == scene.Vec3(socket[0], socket[1], socket[2]) &&
							model.Src == path && model.Animation == flutter && model.Loop != nil && *model.Loop
					}
				}
				if !matched {
					t.Fatalf("%s is missing its socket transform or looping clip", path)
				}
			}
		}
	}
}

func TestFlagKeepsAttachmentEdgeFixed(t *testing.T) {
	data, err := makeFlag(winds[1].field, [3]float64{2.9, 12.2, 0})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := gltf.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	primitive := doc.Meshes[0].Primitives[0]
	clip := doc.Animations[0]
	sampler := clip.Samplers[0]
	if clip.Name != flutter || clip.Channels[0].Path != gltf.PathWeights ||
		len(sampler.Input) != frames+1 || sampler.Input[frames] != loopSeconds ||
		!slices.Equal(sampler.Output[:frames], sampler.Output[frames*frames:]) {
		t.Fatal("clip does not close on its first pose")
	}
	for frame, pose := range primitive.Targets {
		for row := 0; row <= rows; row++ {
			offset := 3 * row * (columns + 1)
			if !slices.Equal(pose.Positions[offset:offset+3], []float32{0, 0, 0}) {
				t.Fatalf("attachment edge moved in frame %d", frame)
			}
		}
	}
	tip := 3 * columns
	if slices.Equal(primitive.Targets[0].Positions[tip:tip+3], primitive.Targets[3].Positions[tip:tip+3]) {
		t.Fatal("free end does not move")
	}
}
