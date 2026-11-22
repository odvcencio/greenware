// Package beacon turns gameplay parameters into modular field beacons.
package beacon

import (
	"fmt"
	"image/color"
	"math"

	"m31labs.dev/greenware/author"
	"m31labs.dev/greenware/kernel/build"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/kernel/rig"
)

// Recipe selects the beacon geometry and appearance.
type Recipe struct {
	Seed          uint64
	Tiers, Panels int
}

// Build returns a self-contained GLB for the recipe.
func Build(r Recipe) ([]byte, error) {
	if r.Tiers < 1 || r.Tiers > 5 || r.Panels < 2 || r.Panels > 6 {
		return nil, fmt.Errorf("field beacon needs 1–5 tiers and 2–6 panels per tier")
	}
	base, err := build.Cylinder(build.CylinderParams{Axis: build.AxisY, Radius: .43, Height: .16,
		Segments: 28, Caps: true, Bevel: &build.Bevel{Width: .028, Segments: 2}})
	if err != nil {
		return nil, err
	}
	post, err := build.Cylinder(build.CylinderParams{Axis: build.AxisY, Radius: .09, Height: 1, Segments: 20, Caps: true})
	if err != nil {
		return nil, err
	}
	collar, err := build.Cylinder(build.CylinderParams{Axis: build.AxisY, Radius: .19, Height: .09,
		Segments: 24, Caps: true, Bevel: &build.Bevel{Width: .015, Segments: 2}})
	if err != nil {
		return nil, err
	}
	bracket, err := build.Box(build.BoxParams{Size: mesh.Vec3{.075, .075, .27}, Bevel: &build.BoxBevel{Width: .013}})
	if err != nil {
		return nil, err
	}
	panel, err := build.Box(build.BoxParams{Size: mesh.Vec3{.25, .24, .09}, Bevel: &build.BoxBevel{Width: .026, Segments: 3}})
	if err != nil {
		return nil, err
	}
	lens, err := build.Box(build.BoxParams{Size: mesh.Vec3{.177, .145, .038}, Bevel: &build.BoxBevel{Width: .016, Segments: 2}})
	if err != nil {
		return nil, err
	}
	lamp, err := build.Sphere(build.SphereParams{Radius: .105, Segments: 24, Rings: 12})
	if err != nil {
		return nil, err
	}
	palette := []color.NRGBA{
		{R: 234, G: 129, B: 30, A: 255},
		{R: 63, G: 193, B: 172, A: 255},
		{R: 238, G: 112, B: 93, A: 255},
		{R: 108, G: 162, B: 235, A: 255},
	}
	accent := palette[r.Seed%uint64(len(palette))]
	asset := author.Assembly{
		Name: "field_beacon",
		Materials: []author.Material{
			{Name: "body", Base: color.NRGBA{R: 49, G: 60, B: 68, A: 255}, Metallic: .48, Roughness: .48},
			{Name: "frame", Base: color.NRGBA{R: 131, G: 149, B: 153, A: 255}, Metallic: .53, Roughness: .38},
			{Name: "lens", Base: accent, Roughness: .26},
		},
		Prototypes: []author.Prototype{
			{Name: "base", Mesh: base, Material: "body"}, {Name: "post", Mesh: post, Material: "body"},
			{Name: "collar", Mesh: collar, Material: "frame"}, {Name: "bracket", Mesh: bracket, Material: "frame"},
			{Name: "panel", Mesh: panel, Material: "body"}, {Name: "lens", Mesh: lens, Material: "lens"},
			{Name: "lamp", Mesh: lamp, Material: "lens"},
		},
	}
	const bottom = .08
	top := .65 + float64(float64(r.Tiers-1)*.33)
	asset.Instances = []author.Instance{
		{Name: "base", Prototype: "base", Translation: mesh.Vec3{0, .08, 0}},
		{Name: "post", Prototype: "post", Scale: mesh.Vec3{1, top - bottom, 1}, Translation: mesh.Vec3{0, (top + bottom) / 2, 0}},
		{Name: "crown", Prototype: "collar", Translation: mesh.Vec3{0, top, 0}},
		{Name: "lamp", Prototype: "lamp", Translation: mesh.Vec3{0, top + .099, 0}},
	}
	asset.Sockets = []author.Socket{
		{Name: "ground", Kind: "anchor"},
		{Name: "status", Kind: "effect", Parent: "lamp", Translation: mesh.Vec3{0, .105, 0}},
	}
	// One rational turn denominator gives each tier a half-panel stagger and
	// a whole-asset seed rotation, while keeping each ring evenly spaced.
	const seedSteps = 16
	turn := 2 * r.Panels * seedSteps
	seedAngle := int((r.Seed / uint64(len(palette))) % seedSteps)
	for tier := range r.Tiers {
		y := .39 + float64(float64(tier)*.33)
		asset.Instances = append(asset.Instances, author.Instance{
			Name: fmt.Sprintf("collar_%d", tier), Prototype: "collar", Translation: mesh.Vec3{0, y, 0},
		})
		for index := range r.Panels {
			step := (2*r.Panels*seedAngle + 2*index*seedSteps + tier%2*seedSteps) % turn
			s, c := mesh.SinCosTurn(step, turn)
			yaw := float64((2 * math.Pi * float64(step)) / float64(turn))
			rotation := rig.QAxis(mesh.Vec3{0, 1, 0}, yaw)
			suffix := fmt.Sprintf("_%d_%d", tier, index)
			for _, part := range []struct {
				name string
				r    float64
			}{
				{"bracket", .19}, {"panel", .343}, {"lens", .393},
			} {
				asset.Instances = append(asset.Instances, author.Instance{
					Name: part.name + suffix, Prototype: part.name, Rotation: rotation,
					Translation: mesh.Vec3{float64(s * part.r), y, float64(c * part.r)},
				})
			}
			asset.Sockets = append(asset.Sockets, author.Socket{
				Name: "payload" + suffix, Kind: "panel", Parent: "lens" + suffix,
				Translation: mesh.Vec3{0, 0, .021},
			})
		}
	}
	return author.BuildAssembly(asset)
}
