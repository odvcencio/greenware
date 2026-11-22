package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"m31labs.dev/gosx"
	"m31labs.dev/gosx/scene"
	"m31labs.dev/gosx/server"
)

//go:embed scene.css
var stylesheet []byte

type windPreset struct {
	id, name string
	field    *scene.Wind
}

var winds = []windPreset{
	{"breeze", "Breeze · 4 m/s", &scene.Wind{Direction: 80, Speed: 4, Gust: 0.24, GustScale: 7, Frequency: 1}},
	{"gusts", "Gusts · 9 m/s", &scene.Wind{Direction: 80, Speed: 9, Gust: 0.55, GustScale: 11, Frequency: 1.67}},
}

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "HTTP listen address")
	source := flag.String("source", "../goalpost/goalpost.gw", "Greenware goal-post source")
	runtimeRoot := flag.String("runtime", "", "GoSX module directory with client/js runtime assets")
	flag.Parse()
	if *runtimeRoot == "" {
		log.Fatal("-runtime is required (see README.md)")
	}
	src, err := os.ReadFile(*source)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := newDemo(src, *runtimeRoot)
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Goal-post wind demo at http://%s\n", listener.Addr())
	web := http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(web.Serve(listener))
}

func newDemo(source []byte, runtimeRoot string) (http.Handler, error) {
	posts, err := compileGoalposts(source)
	if err != nil {
		return nil, err
	}
	type key struct {
		variant string
		lod     int
	}
	byLevel := make(map[key]goalpost)
	models := make(map[string][]byte)
	for _, post := range posts {
		byLevel[key{post.variant, post.lod}] = post
		models[assetURL(post, "frame")] = post.model
		for _, wind := range winds {
			for i, socket := range post.sockets {
				flagGLB, err := makeFlag(wind.field, socket)
				if err != nil {
					return nil, fmt.Errorf("%s lod %d %s: %w", post.variant, post.lod, ribbonNames[i], err)
				}
				models[assetURL(post, wind.id+"/"+ribbonNames[i])] = flagGLB
			}
		}
	}
	for _, variant := range []string{"stadium", "practice"} {
		for lod := range 3 {
			if _, ok := byLevel[key{variant, lod}]; !ok {
				return nil, fmt.Errorf("missing %s lod %d", variant, lod)
			}
		}
	}
	app := server.New()
	app.SetRuntimeRoot(runtimeRoot)
	app.Mount("/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/assets/scene.css" {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			_, _ = w.Write(stylesheet)
			return
		}
		body, ok := models[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "model/gltf-binary")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	app.Page("/", func(ctx *server.Context) gosx.Node {
		q := ctx.Request.URL.Query()
		variant := "stadium"
		if q.Get("post") == "practice" {
			variant = "practice"
		}
		lod, _ := strconv.Atoi(q.Get("lod"))
		if lod < 0 || lod > 2 {
			lod = 0
		}
		wind := winds[0]
		if q.Get("wind") == winds[1].id {
			wind = winds[1]
		}
		closeup := q.Get("view") != "full"
		return page(ctx, byLevel[key{variant, lod}], wind, closeup)
	})
	return app.Build(), nil
}

func assetURL(post goalpost, name string) string {
	return fmt.Sprintf("/assets/%s/%d/%s.glb", post.variant, post.lod, name)
}

func sceneProps(post goalpost, wind windPreset, closeup bool) scene.Props {
	nodes := []scene.Node{
		scene.Model{ID: "goal-post", Src: assetURL(post, "frame"), CastShadow: true},
		scene.Mesh{
			ID: "turf", Geometry: scene.BoxGeometry{Width: 100, Height: 0.2, Depth: 100},
			Material: scene.StandardMaterial{Color: "#3d6a55", Roughness: 0.95},
			Position: scene.Vec3(0, -0.1, 0), ReceiveShadow: true,
		},
		scene.DirectionalLight{ID: "sun", Direction: scene.Vec3(0.4, -0.8, 0.6),
			Color: "#fff3dd", Intensity: 2},
	}
	for i, p := range post.sockets {
		name := ribbonNames[i]
		nodes = append(nodes,
			scene.Mesh{
				ID: name + "_staff", Position: scene.Vec3(p[0], p[1]+0.135, p[2]),
				Geometry: scene.CylinderGeometry{RadiusTop: 0.018, RadiusBottom: 0.018, Height: 0.31, Segments: 12},
				Material: scene.StandardMaterial{Color: "#e6b841", Roughness: 0.55},
			},
			scene.Model{
				ID: name, Src: assetURL(post, wind.id+"/"+name),
				Position: scene.Vec3(p[0], p[1], p[2]), Animation: flutter, Loop: scene.Bool(true),
			},
		)
	}
	left, right := post.sockets[0], post.sockets[1]
	tip := scene.Vec3((left[0]+right[0])/2, (left[1]+right[1])/2, (left[2]+right[2])/2)
	target := scene.Vec3(tip.X+0.2, tip.Y/2+0.15, tip.Z)
	eye := scene.Vec3(target.X+2.4, target.Y+1.25, target.Z-27)
	fov := 36.0
	if closeup {
		target = scene.Vec3(tip.X+0.25, tip.Y-0.34, tip.Z)
		eye = scene.Vec3(target.X+2.95, target.Y+1.15, target.Z-10.7)
		fov = 32
	}
	return scene.Props{
		Width: 1080, Height: 574, Responsive: scene.Bool(true),
		AriaLabel:  "Orbitable football goal post with two wind flags",
		Background: "#769ab8", Controls: scene.ControlOrbit, ControlTarget: target,
		ControlZoom: scene.Bool(true),
		Camera:      scene.PerspectiveCamera{Position: eye, FOV: fov, PortraitFOV: 64, Near: 0.05, Far: 150},
		Environment: scene.Environment{
			AmbientColor: "#e9f2ff", AmbientIntensity: 0.63,
			Wind: wind.field, Exposure: 1, ToneMapping: "aces",
		},
		Graph: scene.NewGraph(nodes...),
	}
}

type option struct{ value, label string }

func selectField(label, name, current string, options ...option) gosx.Node {
	var children []gosx.Node
	for _, choice := range options {
		children = append(children, gosx.El("option",
			gosx.Attrs(gosx.Attr("value", choice.value), gosx.Attr("selected", choice.value == current)), gosx.Text(choice.label)))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "field")),
		gosx.El("label", gosx.Attrs(gosx.Attr("for", name)), gosx.Text(label)),
		gosx.El("select", gosx.Attrs(gosx.Attr("name", name), gosx.Attr("id", name)), gosx.Fragment(children...)))
}

func page(ctx *server.Context, post goalpost, wind windPreset, closeup bool) gosx.Node {
	ctx.SetLanguage("en")
	ctx.SetMetadata(server.Metadata{Title: server.Title{Absolute: "Goal-post wind flags · Greenware + GoSX"}})
	ctx.AddHead(server.Stylesheet("/assets/scene.css"))
	view := "flags"
	if !closeup {
		view = "full"
	}
	props := sceneProps(post, wind, closeup)
	preview := ctx.Runtime().Engine(props.EngineConfig(), gosx.Text("The 3D preview requires a browser with WebGL or WebGPU."))
	return gosx.El("main", nil,
		gosx.El("header", nil,
			gosx.El("h1", nil, gosx.Text("Goal-post wind flags")),
			gosx.El("p", nil, gosx.Text("Greenware sockets hold two GoSX flags in place as the wind changes.")),
		),
		gosx.El("form", gosx.Attrs(gosx.Attr("method", "get"), gosx.Attr("action", "/")),
			selectField("Post", "post", post.variant, option{"stadium", "Stadium"}, option{"practice", "Practice"}),
			selectField("Detail", "lod", strconv.Itoa(post.lod), option{"0", "Full · LOD 0"}, option{"1", "Mid · LOD 1"}, option{"2", "Far · LOD 2"}),
			selectField("Wind", "wind", wind.id, option{winds[0].id, winds[0].name}, option{winds[1].id, winds[1].name}),
			selectField("View", "view", view, option{"flags", "Flags"}, option{"full", "Full post"}),
			gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit")), gosx.Text("Apply")),
		),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "viewport")), preview),
		gosx.El("footer", nil,
			gosx.El("span", nil, gosx.Text("Drag to orbit · scroll to zoom")),
			gosx.El("a", gosx.Attrs(gosx.Attr("href", assetURL(post, "frame")), gosx.Attr("download", fmt.Sprintf("goalpost-%s-lod%d.glb", post.variant, post.lod))), gosx.Text("Download rigid GLB")),
		),
	)
}
