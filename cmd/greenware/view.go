package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"embed"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/diag"
)

const viewUsage = "  greenware view <file> [--output NAME] [--project DIR]\n"

//go:embed viewer/*
var viewerFiles embed.FS

var viewerPage = template.Must(template.ParseFS(viewerFiles, "viewer/view.html"))

type previewVariant struct {
	Label, Name             string
	Triangles, Draws, Bytes int
	Animations              []string
	GLB                     []byte
}

type previewState struct {
	Attempt, Revision uint64
	Asset, Problem    string
	Variants          []previewVariant
}

type previewStore struct {
	mu                sync.RWMutex
	current, previous previewState
}

func (p *previewStore) publish(product *compiler.Product, problem string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current.Attempt++
	p.current.Problem = problem
	if product == nil {
		return
	}
	p.previous = p.current
	var variants []previewVariant
	for _, output := range product.Outputs {
		for _, file := range output.Files {
			var animations []string
			for _, clip := range file.Doc.Animations {
				animations = append(animations, clip.Name)
			}
			variants = append(variants, previewVariant{
				Label: fmt.Sprintf("%s · LOD %d", output.Name, file.Level), Name: file.Name,
				Triangles: file.Triangles, Draws: file.Draws, Bytes: file.Size,
				Animations: animations, GLB: file.GLB,
			})
		}
	}
	p.current = previewState{
		Attempt: p.current.Attempt, Revision: p.current.Attempt, Asset: product.Asset, Variants: variants,
	}
}

func (p *previewStore) snapshot() previewState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.current
}

func (p *previewStore) model(revision uint64, index int) []byte {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, state := range []previewState{p.current, p.previous} {
		if revision == state.Revision && index >= 0 && index < len(state.Variants) {
			return state.Variants[index].GLB
		}
	}
	return nil
}

type viewPage struct {
	previewState
	Source, PollURL, ModelURL string
	Camera, Orbit, Animation  string
	Selected                  previewVariant
	Swap                      bool
}

var previewOrbits = map[string]string{
	"three-quarter": "35deg 70deg auto",
	"front":         "0deg 90deg auto",
	"side":          "90deg 90deg auto",
	"top":           "0deg 0deg auto",
}

func previewPage(state previewState, source string, query url.Values, swap bool) viewPage {
	camera := query.Get("camera")
	if _, ok := previewOrbits[camera]; !ok {
		camera = "three-quarter"
	}
	page := viewPage{
		previewState: state, Source: source, Camera: camera, Orbit: previewOrbits[camera], Swap: swap,
	}
	if len(state.Variants) > 0 {
		index := slices.IndexFunc(state.Variants, func(v previewVariant) bool { return v.Name == query.Get("file") })
		index = max(index, 0)
		page.Selected = state.Variants[index]
		page.ModelURL = fmt.Sprintf("/model/%d/%d", state.Revision, index)
		if animation := query.Get("animation"); slices.Contains(page.Selected.Animations, animation) {
			page.Animation = animation
		}
	}
	poll := url.Values{
		"attempt":  {strconv.FormatUint(state.Attempt, 10)},
		"revision": {strconv.FormatUint(state.Revision, 10)},
		"camera":   {page.Camera}, "file": {page.Selected.Name}, "animation": {page.Animation},
	}
	page.PollURL = "/updates?" + poll.Encode()
	return page
}

func viewHandler(state *previewStore, address, source string) http.Handler {
	mux := http.NewServeMux()
	render := func(w http.ResponseWriter, name string, page viewPage) {
		var html bytes.Buffer
		if err := viewerPage.ExecuteTemplate(&html, name, page); err != nil {
			http.Error(w, "The preview page could not be rendered", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(html.Bytes())
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		render(w, "page", previewPage(state.snapshot(), source, r.URL.Query(), false))
	})
	mux.HandleFunc("GET /updates", func(w http.ResponseWriter, r *http.Request) {
		current := state.snapshot()
		query := r.URL.Query()
		attempt, _ := strconv.ParseUint(query.Get("attempt"), 10, 64)
		if current.Attempt == attempt {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		revision, _ := strconv.ParseUint(query.Get("revision"), 10, 64)
		render(w, "update", previewPage(current, source, query, current.Revision != revision))
	})
	mux.HandleFunc("GET /model/{revision}/{index}", func(w http.ResponseWriter, r *http.Request) {
		revision, revErr := strconv.ParseUint(r.PathValue("revision"), 10, 64)
		index, indexErr := strconv.Atoi(r.PathValue("index"))
		data := state.model(revision, index)
		if revErr != nil || indexErr != nil || data == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "model/gltf-binary")
		http.ServeContent(w, r, "scene.glb", time.Time{}, bytes.NewReader(data))
	})
	for _, file := range []struct {
		name, contentType string
		compressed        bool
	}{
		{"view.css", "text/css", false},
		{"model-viewer.min.js", "text/javascript", true},
		{"htmx.min.js", "text/javascript", true},
	} {
		data := previewAsset(file.name, file.compressed)
		mux.HandleFunc("GET /"+file.name, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", file.contentType+"; charset=utf-8")
			http.ServeContent(w, r, file.name, time.Time{}, bytes.NewReader(data))
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != address {
			http.Error(w, "Preview is available only at its loopback address", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' blob: data:; connect-src 'self' blob:; worker-src blob:; "+
				"form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	})
}

func previewAsset(name string, compressed bool) []byte {
	path := "viewer/" + name
	if compressed {
		path += ".gz"
	}
	data, err := viewerFiles.ReadFile(path)
	if err != nil {
		panic(err)
	}
	if compressed {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			panic(err)
		}
		defer reader.Close()
		data, err = io.ReadAll(reader)
		if err != nil {
			panic(err)
		}
	}
	return data
}

func runView(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	allowed := map[string]bool{"output": true, "project": true}
	o, err := parse(args, allowed)
	if err != nil {
		return usageFailure(stderr, "view", err, "usage:\n"+viewUsage)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(stderr, "greenware view:", err)
		return 2
	}
	defer listener.Close()
	state := &previewStore{}
	address := listener.Addr().String()
	server := &http.Server{
		Handler:           viewHandler(state, address, filepath.Base(o.file)),
		ReadHeaderTimeout: 3 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	fmt.Fprintln(stdout, "Greenware preview at http://"+address)
	go func() {
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		watchInputs(ctx, ticker.C, o, func(o options, source []byte, readErr error) {
			if readErr != nil {
				state.publish(nil, readErr.Error())
				return
			}
			f, ds, loc := frontWithProject(o.file, o.project, source, false, o.observeProject)
			product, _, ds := buildChecked(f, ds, o.output, compiler.Options{})
			var messages bytes.Buffer
			printText(&messages, o.file, ds, loc)
			switch {
			case product == nil && messages.Len() == 0:
				state.publish(nil, "The build produced no asset")
			case diag.HasErrors(ds):
				state.publish(nil, messages.String())
			default:
				state.publish(product, "")
			}
		})
	}()
	if err := server.Serve(listener); err != http.ErrServerClosed {
		fmt.Fprintln(stderr, "greenware view:", err)
		return 2
	}
	return 0
}
