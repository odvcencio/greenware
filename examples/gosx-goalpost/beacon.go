package main

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"image/color"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"m31labs.dev/gosx/scene"
	"m31labs.dev/gosx/server"
	"m31labs.dev/greenware/author"
	"m31labs.dev/greenware/examples/field-beacon/beacon"
	"m31labs.dev/greenware/kernel/build"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/socket"
)

const beaconAssets = "/beacon-assets/"
const beaconCacheSize = 32

//go:embed beacon.css
var beaconStyles []byte

//go:embed beacon.js
var beaconScript []byte

var defaultBeacon = beacon.Recipe{Seed: 17, Tiers: 3, Panels: 5}

type beaconVariant struct {
	data   []byte
	digest string
	source string
	models []scene.Node
	count  int
}

type beaconWorkshop struct {
	mu     sync.Mutex
	recent map[beacon.Recipe]*beaconVariant
	order  [beaconCacheSize]beacon.Recipe
	next   int
}

func parseBeacon(query url.Values) (beacon.Recipe, error) {
	r := defaultBeacon
	if query.Has("seed") {
		seed, err := strconv.ParseUint(query.Get("seed"), 10, 64)
		if err != nil {
			return r, fmt.Errorf("Enter a seed from 0 to 18446744073709551615.")
		}
		r.Seed = seed
	}
	if query.Has("tiers") {
		tiers, err := strconv.Atoi(query.Get("tiers"))
		if err != nil || tiers < 1 || tiers > 5 {
			return r, fmt.Errorf("Choose 1 to 5 tiers.")
		}
		r.Tiers = tiers
	}
	if query.Has("panels") {
		panels, err := strconv.Atoi(query.Get("panels"))
		if err != nil || panels < 2 || panels > 6 {
			return r, fmt.Errorf("Choose 2 to 6 panels per tier.")
		}
		r.Panels = panels
	}
	return r, nil
}

func (w *beaconWorkshop) variant(r beacon.Recipe) (*beaconVariant, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if cached := w.recent[r]; cached != nil {
		return cached, nil
	}
	data, err := beacon.Build(r)
	if err != nil {
		return nil, err
	}
	frames, err := socket.ReadGLB(data)
	if err != nil {
		return nil, err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	q := url.Values{
		"seed": {strconv.FormatUint(r.Seed, 10)}, "tiers": {strconv.Itoa(r.Tiers)},
		"panels": {strconv.Itoa(r.Panels)}, "sha": {digest},
	}
	source := beaconAssets + "beacon.glb?" + q.Encode()
	v := &beaconVariant{
		data: data, digest: digest, source: source,
		models: []scene.Node{scene.Model{ID: "beacon", Src: source, CastShadow: true}},
	}
	for _, frame := range frames {
		var asset string
		switch frame.Kind {
		case "panel":
			asset = "payload.glb"
			v.count++
		case "effect":
			asset = "status.glb"
		default:
			continue
		}
		p := frame.Transform.T
		// The beacon's panels turn around Y. Forward is the socket's local +Z.
		forward := frame.Transform.M.MulVec(mesh.Vec3{0, 0, 1})
		v.models = append(v.models, scene.Model{
			ID: frame.Name, Src: beaconAssets + asset,
			Position: scene.Vec3(p[0], p[1], p[2]), Rotation: scene.Euler{Y: math.Atan2(forward[0], forward[2])},
		})
	}
	if len(w.recent) == beaconCacheSize {
		delete(w.recent, w.order[w.next])
	}
	w.recent[r] = v
	w.order[w.next] = r
	w.next = (w.next + 1) % len(w.order)
	return v, nil
}

type beaconUpdate struct {
	Commands []scene.Command `json:"commands"`
	Source   string          `json:"source"`
	Payloads int             `json:"payloads"`
}

func mountBeacon(app *server.App) error {
	workshop := &beaconWorkshop{recent: make(map[beacon.Recipe]*beaconVariant)}
	initial, err := workshop.variant(defaultBeacon)
	if err != nil {
		return err
	}
	payload, err := beaconAttachment("payload")
	if err != nil {
		return err
	}
	status, err := beaconAttachment("status")
	if err != nil {
		return err
	}

	app.Page("/beacon", beaconPageHandler(workshop, initial))
	assets := http.NewServeMux()
	assets.HandleFunc("GET "+beaconAssets+"recipe", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		recipe, err := parseBeacon(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		variant, err := workshop.variant(recipe)
		if err != nil {
			log.Printf("beacon recipe: %v", err)
			http.Error(w, "The beacon could not be generated.", http.StatusInternalServerError)
			return
		}
		models := scene.NewGraph(variant.models...).SceneIR().Models
		result, err := json.Marshal(beaconUpdate{
			Commands: []scene.Command{scene.SetModelsCommand(models)},
			Source:   variant.source, Payloads: variant.count,
		})
		if err != nil {
			log.Printf("beacon scene: %v", err)
			http.Error(w, "The beacon scene could not be prepared.", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	})
	assets.HandleFunc("GET "+beaconAssets+"beacon.glb", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		recipe, err := parseBeacon(query)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		variant, err := workshop.variant(recipe)
		if err != nil {
			log.Printf("beacon asset: %v", err)
			http.Error(w, "The beacon could not be generated.", http.StatusInternalServerError)
			return
		}
		if variant.digest != query.Get("sha") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", strconv.Quote(variant.digest))
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		serveBeaconAsset(w, r, "beacon.glb", "model/gltf-binary", variant.data)
	})
	for _, asset := range []struct {
		name, contentType string
		data              []byte
	}{
		{"beacon.css", "text/css; charset=utf-8", beaconStyles},
		{"beacon.js", "text/javascript; charset=utf-8", beaconScript},
		{"payload.glb", "model/gltf-binary", payload},
		{"status.glb", "model/gltf-binary", status},
	} {
		assets.HandleFunc("GET "+beaconAssets+asset.name, func(w http.ResponseWriter, r *http.Request) {
			serveBeaconAsset(w, r, asset.name, asset.contentType, asset.data)
		})
	}
	app.Mount(beaconAssets, assets)
	return nil
}

func serveBeaconAsset(w http.ResponseWriter, r *http.Request, name, contentType string, data []byte) {
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func beaconAttachment(kind string) ([]byte, error) {
	axis, radius, height := build.AxisZ, .060, .040
	lampRadius := .032
	centre := mesh.Vec3{0, 0, .024}
	if kind == "status" {
		axis, radius, height = build.AxisY, .075, .025
		lampRadius = .046
		centre = mesh.Vec3{0, .037, 0}
	}
	housing, err := build.Cylinder(build.CylinderParams{Axis: axis, Radius: radius, Height: height,
		Segments: 12, Caps: true, Bevel: &build.Bevel{Width: .007, Segments: 2}})
	if err != nil {
		return nil, err
	}
	lamp, err := build.Sphere(build.SphereParams{Radius: lampRadius, Segments: 14, Rings: 7})
	if err != nil {
		return nil, err
	}
	return author.Build(author.Asset{Name: kind + "_module",
		Materials: []author.Material{
			{Name: "housing", Base: color.NRGBA{R: 224, G: 150, B: 45, A: 255}, Roughness: .39},
			{Name: "lamp", Base: color.NRGBA{R: 255, G: 241, B: 181, A: 255}, Roughness: .19},
		},
		Parts: []author.Part{
			{Name: "housing", Mesh: housing, Material: "housing"},
			{Name: "lamp", Mesh: lamp, Material: "lamp", Translation: centre},
		},
	})
}
