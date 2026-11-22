package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"m31labs.dev/gosx/scene"
	"m31labs.dev/greenware/kernel/rig"
	"m31labs.dev/greenware/socket"
)

func TestBeaconUpdateUsesCompleteSocketSnapshot(t *testing.T) {
	source, err := os.ReadFile("../goalpost/goalpost.gw")
	if err != nil {
		t.Fatal(err)
	}
	app, err := newDemo(source, "")
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string, status int) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != status {
			t.Fatalf("GET %s: got %d, want %d: %.300s", path, recorder.Code, status, recorder.Body.String())
		}
		return recorder
	}
	const query = "?seed=18446744073709551615&tiers=2&panels=3"
	page := get("/beacon"+query, http.StatusOK).Body.String()
	for _, want := range []string{"Field beacon", "name=\"seed\"", "value=\"18446744073709551615\"", "6 panel modules", "/beacon-assets/beacon.js"} {
		if !strings.Contains(page, want) {
			t.Errorf("beacon page is missing %q", want)
		}
	}
	var patch struct {
		Commands []struct {
			Kind scene.CommandKind
			Data struct {
				Models []struct {
					ID, Src                                  string
					X, Y, Z, RotationX, RotationY, RotationZ float64
				}
			}
		}
		Source   string
		Payloads int
	}
	res := get(beaconAssets+"recipe"+query, http.StatusOK)
	if err := json.Unmarshal(res.Body.Bytes(), &patch); err != nil {
		t.Fatal(err)
	}
	if len(patch.Commands) != 1 || patch.Commands[0].Kind != scene.CommandSetModels || patch.Payloads != 6 {
		t.Fatalf("unexpected command transaction %+v", patch)
	}
	models := patch.Commands[0].Data.Models
	if len(models) != 8 || models[0].Src != patch.Source {
		t.Fatalf("got %d models, want beacon plus all socket modules", len(models))
	}
	model := get(patch.Source, http.StatusOK)
	if got := model.Header().Get("Content-Type"); got != "model/gltf-binary" {
		t.Fatalf("unexpected model content type %q", got)
	}
	asset, err := url.Parse(patch.Source)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(model.Body.Bytes())
	if q := asset.Query(); q.Get("sha") != hex.EncodeToString(digest[:]) || q.Get("seed") != "18446744073709551615" {
		t.Fatalf("asset URL does not identify the snapshot: %s", asset)
	}
	sockets, err := socket.ReadGLB(model.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range models[1:] {
		s, err := sockets.Find(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if item.X != s.Transform.T[0] || item.Y != s.Transform.T[1] || item.Z != s.Transform.T[2] {
			t.Errorf("%s has the wrong socket origin", item.ID)
		}
		rotation := rig.QEuler(item.RotationX, item.RotationY, item.RotationZ).Mat3()
		for row, axis := range s.Transform.M {
			for column, want := range axis {
				if math.Abs(rotation[row][column]-want) > 1e-12 {
					t.Errorf("%s rotation = %v, want %v", item.ID, rotation, s.Transform.M)
					break
				}
			}
		}
		if s.Kind == "panel" && item.Src != beaconAssets+"payload.glb" ||
			s.Kind == "effect" && item.Src != beaconAssets+"status.glb" {
			t.Errorf("%s loaded %q", item.ID, item.Src)
		}
	}
	get(beaconAssets+"recipe?tiers=6", http.StatusBadRequest)
	get(beaconAssets+"recipe?seed=18446744073709551616", http.StatusBadRequest)
	get(beaconAssets+"beacon.glb"+query+"&sha=stale", http.StatusNotFound)
}
