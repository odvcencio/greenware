package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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
