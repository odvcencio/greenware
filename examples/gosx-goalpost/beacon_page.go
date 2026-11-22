package main

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

	"m31labs.dev/gosx"
	"m31labs.dev/gosx/scene"
	"m31labs.dev/gosx/server"
	"m31labs.dev/greenware/examples/field-beacon/beacon"
)

func beaconPageHandler(workshop *beaconWorkshop, initial *beaconVariant) server.PageHandler {
	return func(ctx *server.Context) gosx.Node {
		r, err := parseBeacon(ctx.Request.URL.Query())
		variant := initial
		problem := ""
		if err != nil {
			ctx.SetStatus(http.StatusBadRequest)
			r, problem = defaultBeacon, err.Error()
		} else if v, err := workshop.variant(r); err != nil {
			log.Printf("beacon preview: %v", err)
			ctx.SetStatus(http.StatusInternalServerError)
			r, problem = defaultBeacon, "The beacon could not be generated."
		} else {
			variant = v
		}
		return beaconPage(ctx, r, variant, problem)
	}
}

func beaconScene(v *beaconVariant) scene.Props {
	nodes := []scene.Node{
		scene.Mesh{ID: "deck", Geometry: scene.BoxGeometry{Width: 18, Height: .1, Depth: 18},
			Material: scene.StandardMaterial{Color: "#547068", Roughness: .92},
			Position: scene.Vec3(0, -.05, 0), ReceiveShadow: true},
		scene.DirectionalLight{ID: "key", Direction: scene.Vec3(.5, -.8, -.35),
			Color: "#fff1d7", Intensity: 1.85, CastShadow: true},
	}
	nodes = append(nodes, v.models...)
	return scene.Props{
		Width: 940, Height: 600, Responsive: scene.Bool(true),
		AriaLabel:  "Orbitable field beacon with socket-mounted modules",
		Background: "#739189", Controls: scene.ControlOrbit, ControlZoom: scene.Bool(true),
		ControlTarget: scene.Vec3(0, 1.02, 0),
		Camera: scene.PerspectiveCamera{
			Position: scene.Vec3(2.6, 2.5, 4.3), FOV: 35, PortraitFOV: 56, Near: .05, Far: 55,
		},
		Environment: scene.Environment{AmbientColor: "#e1f4ec", AmbientIntensity: .7, Exposure: 1, ToneMapping: "aces"},
		Graph:       scene.NewGraph(nodes...),
	}
}

func beaconRange(id, name string, value, min, max int) gosx.Node {
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "beacon-field")),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "range-label")),
			gosx.El("label", gosx.Attrs(gosx.Attr("for", id)), gosx.Text(name)),
			gosx.El("output", gosx.Attrs(gosx.Attr("for", id), gosx.Attr("data-range-value", id)), gosx.Text(strconv.Itoa(value))),
		),
		gosx.El("input", gosx.Attrs(
			gosx.Attr("type", "range"), gosx.Attr("id", id), gosx.Attr("name", id),
			gosx.Attr("value", value), gosx.Attr("min", min), gosx.Attr("max", max),
		)),
	)
}

func beaconPage(ctx *server.Context, r beacon.Recipe, v *beaconVariant, problem string) gosx.Node {
	ctx.SetLanguage("en")
	ctx.SetMetadata(server.Metadata{Title: server.Title{Absolute: "Field beacon · Greenware + GoSX"}})
	ctx.AddHead(server.Stylesheet(beaconAssets + "beacon.css"))
	ctx.LifecycleScript(beaconAssets+"beacon.js", gosx.Attrs(gosx.BoolAttr("defer")))
	viewport := ctx.Runtime().Engine(beaconScene(v).EngineConfig(), gosx.Text("The 3D scene needs WebGL or WebGPU."))
	message := fmt.Sprintf("%d panel modules · 1 status light", v.count)
	if problem != "" {
		message = problem
	}
	return gosx.El("main", gosx.Attrs(gosx.Attr("class", "beacon-page")),
		gosx.El("header", nil,
			gosx.El("a", gosx.Attrs(gosx.Attr("href", "/"), gosx.Attr("class", "beacon-back")), gosx.Text("← Goal-post flags")),
			gosx.El("h1", nil, gosx.Text("Field beacon")),
			gosx.El("p", nil, gosx.Text("Change the recipe to rebuild the beacon and its socket-mounted modules.")),
		),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "beacon-layout")),
			gosx.El("section", gosx.Attrs(gosx.Attr("class", "beacon-stage"), gosx.Attr("id", "beacon-stage"), gosx.Attr("aria-label", "Beacon viewport")),
				viewport,
				gosx.El("p", gosx.Attrs(gosx.Attr("class", "beacon-gesture")), gosx.Text("Drag to orbit · Scroll or pinch to zoom")),
			),
			gosx.El("aside", gosx.Attrs(gosx.Attr("class", "beacon-inspector"), gosx.Attr("aria-label", "Beacon recipe")),
				gosx.El("form", gosx.Attrs(gosx.Attr("method", "get"), gosx.Attr("action", "/beacon"), gosx.Attr("id", "beacon-recipe")),
					gosx.El("div", gosx.Attrs(gosx.Attr("class", "beacon-field")),
						gosx.El("label", gosx.Attrs(gosx.Attr("for", "seed")), gosx.Text("Seed")),
						gosx.El("input", gosx.Attrs(gosx.Attr("id", "seed"), gosx.Attr("name", "seed"),
							gosx.Attr("value", strconv.FormatUint(r.Seed, 10)), gosx.Attr("type", "text"), gosx.Attr("inputmode", "numeric"),
							gosx.Attr("pattern", "[0-9]{1,20}"), gosx.Attr("maxlength", 20), gosx.BoolAttr("required"),
						)),
					),
					beaconRange("tiers", "Tiers", r.Tiers, 1, 5),
					beaconRange("panels", "Panels per tier", r.Panels, 2, 6),
					gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit")), gosx.Text("Generate")),
				),
				gosx.El("p", gosx.Attrs(gosx.Attr("id", "beacon-status"), gosx.Attr("role", "status"),
					gosx.Attr("data-error", strconv.FormatBool(problem != ""))), gosx.Text(message)),
				gosx.El("a", gosx.Attrs(gosx.Attr("href", v.source), gosx.Attr("download", "field-beacon.glb"),
					gosx.Attr("id", "beacon-download")), gosx.Text("Download beacon GLB")),
			),
		),
	)
}
