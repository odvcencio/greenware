package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/diag"
)

func TestViewKeepsPublishedAssetAfterRejectedBuild(t *testing.T) {
	fs, ds, _ := front("", "", []byte(`greenware 1
asset cube { root = cube }
part cube { box { size = (1m, 1m, 1m) } }
output main { file = "cube.glb" }
`), false)
	product, _, ds := buildChecked(fs, ds, "", compiler.Options{})
	if product == nil || diag.HasErrors(ds) {
		t.Fatalf("compile cube: %v", ds)
	}
	state := &previewStore{}
	handler := viewHandler(state, "127.0.0.1:5170", "cube.gw")
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:5170"+path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	state.publish(product, "")
	page := get("/updates?attempt=0&revision=0")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "hx-swap-oob") || !strings.Contains(page.Body.String(), "/model/1/0") {
		t.Fatalf("initial update did not offer the model: %d %s", page.Code, page.Body)
	}
	model := get("/model/1/0")
	if model.Code != http.StatusOK || model.Header().Get("Content-Type") != "model/gltf-binary" ||
		!bytes.Equal(model.Body.Bytes(), product.Outputs[0].Files[0].GLB) {
		t.Fatal("viewer did not serve the compiled GLB")
	}
	state.publish(nil, "invalid size <0m>")
	failed := get("/updates?attempt=1&revision=1")
	if failed.Code != http.StatusOK || !strings.Contains(failed.Body.String(), "last good build") ||
		!strings.Contains(failed.Body.String(), "&lt;0m&gt;") || strings.Contains(failed.Body.String(), "hx-swap-oob") {
		t.Fatalf("rejected build changed the viewport or lost diagnostics: %d %s", failed.Code, failed.Body)
	}
	if kept := get("/model/1/0"); !bytes.Equal(kept.Body.Bytes(), model.Body.Bytes()) {
		t.Fatal("rejected build discarded the previous GLB")
	}
	if unchanged := get("/updates?attempt=2&revision=1"); unchanged.Code != http.StatusNoContent {
		t.Fatalf("unchanged state should return 204, got %d", unchanged.Code)
	}
	if invalid := get("/model/1/a"); invalid.Code != http.StatusNotFound {
		t.Fatalf("invalid index should return 404, got %d", invalid.Code)
	}
}

func TestViewSocketOverlayUsesSelectedBuild(t *testing.T) {
	src, err := os.ReadFile(exampleSrc)
	if err != nil {
		t.Fatal(err)
	}
	file, ds, _ := front(exampleSrc, "", src, false)
	product, _, ds := buildChecked(file, ds, "", compiler.Options{})
	if product == nil || diag.HasErrors(ds) {
		t.Fatalf("compile goal post: %v", ds)
	}
	state := &previewStore{}
	handler := viewHandler(state, "127.0.0.1:5170", exampleSrc)
	get := func(path string) string {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:5170"+path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, w.Code)
		}
		return w.Body.String()
	}
	state.publish(product, "")
	if body := get("/"); strings.Contains(body, "slot=\"hotspot-socket-") || !strings.Contains(body, "Show sockets") {
		t.Fatal("socket controls or default visibility are wrong")
	}
	const marker = "slot=\"hotspot-socket-"
	practice := get("/?sockets=1&file=goalpost-practice-lod2.glb")
	if count := strings.Count(practice, marker); count != 5 ||
		!strings.Contains(practice, "9.149999618530273m") ||
		!strings.Contains(practice, "sockets=1") {
		t.Fatalf("practice overlay has %d markers or lost its positions/preference", count)
	}
	// A good rebuild carries the preference into the replacement workspace.
	state.publish(product, "")
	refresh := get("/updates?attempt=1&revision=1&sockets=1&file=goalpost-practice-lod2.glb")
	if !strings.Contains(refresh, "hx-swap-oob") || strings.Count(refresh, marker) != 5 {
		t.Fatal("refresh dropped the active socket overlay")
	}
}
