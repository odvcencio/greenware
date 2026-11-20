package compiler

import (
	"encoding/json"
	"fmt"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

// binding ties a runtime parameter to one field of one material.
type binding struct {
	param    string
	material string
	field    string // baseColorFactor, roughnessFactor, metallicFactor
	mapping  string // srgb_to_linear or identity

	// A visibility binding (kind "visibility") shows or hides one node. The
	// node, its intended parent and its local translation say where the node
	// belongs; scene says whether the default scene draws it ("default") or it
	// is a root of the "alternates" scene.
	kind string
	// target is "palette" for a slot colour of a Selena material's palette;
	// it is empty for a core glTF field.
	target      string
	node        string
	parent      string
	translation mesh.Vec3
	scene       string
	visible     bool // the node's own state at the defaults
	cond        resolve.Cond
}

// matOut is a material as written, with the runtime bindings it carries.
type matOut struct {
	name     string
	m        gltf.Material
	bindings []binding
	// look says which images the material reads; the file's tables give them
	// indices.
	look *matLook
	// filter and wrap are the fallback's sampler state.
	filter, wrap string
	// selena is the Selena material node when the material is one.
	selena *resolve.Node
}

// srgbToLinear converts one sRGB byte to the linear factor glTF stores
// (language.md section 2.15): c/12.92 up to 0.04045, else ((c+0.055)/1.055)^2.4.
// The power uses only +, *, / and a fixed number of Newton steps, so every
// machine gets the same bits.
func srgbToLinear(b uint8) float64 {
	c := float64(b) / 255
	if c <= 0.04045 {
		return c / 12.92
	}
	x := (c + 0.055) / 1.055
	// x^2.4 = x^2 * x^(2/5); x^(2/5) is the fifth root of x^2.
	a := float64(x * x)
	r := 1.0
	for i := 0; i < 80; i++ {
		r4 := det.Pow(r, 4)
		r = (float64(4*r) + a/r4) / 5
	}
	return float64(a * r)
}

func (e *elab) material(name string) (*matOut, bool) {
	var node *resolve.Node
	for _, m := range e.r.Materials {
		if m.Name == name {
			node = m
		}
	}
	if node == nil {
		e.unsupported(name, "the material `%s` was not found", name)
		return nil, false
	}
	var am *ast.Material
	for _, m := range e.file.Materials {
		if m.Name.Name == name {
			am = m
		}
	}
	src := node
	var asrc *ast.Material = am
	out := &matOut{name: name}
	if node.Kind == "selena" {
		var fb *resolve.Node
		for _, c := range node.Children {
			if c.Kind == "pbr" {
				fb = c
			}
		}
		if fb == nil {
			e.report(diag.CodeMissing, e.spanOf(name), "the selena material `%s` has no pbr fallback", name)
			return nil, false
		}
		out.selena = node
		for _, f := range node.Fields {
			if !f.Value.DependsOnRuntime() {
				continue
			}
			// A palette slot colour binds to the descriptor field; any other
			// runtime value would need the Selena artifact to switch.
			if _, isSlot := slotNumber(f.Name); isSlot && f.Value.Kind == units.KColor && am != nil {
				p, ok := e.runtimeParam(am, f.Name)
				if !ok {
					e.unsupported(name, "the selena field `%s` mixes a runtime parameter into an expression; this compiler binds a parameter used by itself", f.Name)
					return nil, false
				}
				out.bindings = append(out.bindings, binding{param: p, material: name, field: f.Name, mapping: "srgb_to_linear", target: "palette"})
				continue
			}
			e.unsupported(name, "the selena material `%s` binds a runtime parameter to `%s`: this compiler writes the pbr fallback only, so it cannot switch a Selena field other than a palette slot colour", name, f.Name)
			return nil, false
		}
		src = fb
		if am != nil {
			asrc = am.Fallback
		}
	} else if node.Kind != "pbr" {
		e.unsupported(name, "the material kind `%s` is not supported", node.Kind)
		return nil, false
	}
	ml, ok := e.materialLook(node)
	if !ok {
		return nil, false
	}
	out.look = ml
	base := units.Col(units.Color{R: 255, G: 255, B: 255, A: 255})
	rough, metal, alpha := 1.0, 0.0, 1.0
	coat, coatRough := 0.0, 0.0
	out.filter, out.wrap = "trilinear", "repeat"
	ok = true
	for _, f := range src.Fields {
		switch f.Name {
		case "base":
			base = f.Value
		case "roughness":
			rough = f.Value.Num
		case "metallic":
			metal = f.Value.Num
		case "alpha":
			alpha = f.Value.Num
		case "clearcoat":
			coat = f.Value.Num
		case "clearcoat_roughness":
			coatRough = f.Value.Num
		case "filter":
			out.filter = f.Value.Str
		case "wrap":
			out.wrap = f.Value.Str
		case "name", "base_texture":
			// The artifact name is unused; the base texture is in ml.
		case "blend":
			if f.Value.Bool {
				e.notes = appendUnique(e.notes, fmt.Sprintf("material %s: blend = on is not written: the glTF writer has no alphaMode field, so the material keeps its alpha factor in an opaque-mode material", name))
			}
		default:
			if !f.Default && !fieldIsInert(f) {
				e.unsupported(name, "the material field `%s` is not supported yet", f.Name)
				ok = false
			}
		}
	}
	if !ok {
		return nil, false
	}
	if base.Kind != units.KColor {
		e.unsupported(name, "a material's `base` must be a colour in the first compiler")
		return nil, false
	}
	c := base.Color
	out.m = gltf.Material{
		Name: name,
		BaseColor: [4]float32{
			det.Round32(srgbToLinear(c.R)), det.Round32(srgbToLinear(c.G)), det.Round32(srgbToLinear(c.B)),
			det.Round32(float64(float64(c.A) / 255 * alpha)),
		},
		Metallic:  det.Round32(metal),
		Roughness: det.Round32(rough),
	}
	if coat > 0 || coatRough > 0 {
		// KHR_materials_clearcoat carries both coat factors whenever a coat is used.
		out.m.Extensions = map[string]json.RawMessage{"KHR_materials_clearcoat": mustJSON(map[string]any{
			"clearcoatFactor": f32(det.Round32(coat)), "clearcoatRoughnessFactor": f32(det.Round32(coatRough)),
		})}
	}
	// Runtime bindings: a field that depends on a runtime parameter is written
	// at its default and recorded.
	for _, f := range []struct {
		field, glt, mapping string
	}{{"base", "baseColorFactor", "srgb_to_linear"}, {"alpha", "baseColorFactor.alpha", "identity"}, {"roughness", "roughnessFactor", "identity"}, {"metallic", "metallicFactor", "identity"}} {
		var val units.Value
		for _, sf := range src.Fields {
			if sf.Name == f.field {
				val = sf.Value
			}
		}
		if !val.DependsOnRuntime() {
			continue
		}
		p, ok := e.runtimeParam(asrc, f.field)
		if !ok {
			e.unsupported(name, "the material field `%s` mixes a runtime parameter into an expression; the first compiler binds a parameter used by itself", f.field)
			return nil, false
		}
		out.bindings = append(out.bindings, binding{param: p, material: name, field: f.glt, mapping: f.mapping})
	}
	return out, true
}

// fieldIsInert reports whether a registry default that the resolver wrote
// back needs no support (it is the glTF default).
func fieldIsInert(f resolve.Field) bool { return f.Default }

// runtimeParam finds the runtime parameter a material setting names.
func (e *elab) runtimeParam(am *ast.Material, field string) (string, bool) {
	if am == nil {
		return "", false
	}
	s := ast.FindSetting(am.Settings, field)
	if s == nil {
		return "", false
	}
	n, ok := s.Value.(*ast.Name)
	if !ok {
		return "", false
	}
	for _, p := range e.r.Params {
		if p.Name == n.Name && p.Runtime {
			return p.Name, true
		}
	}
	return "", false
}

// paramDefault formats a runtime parameter's default for the table.
func paramDefault(p resolve.Param) any {
	v := p.Value
	switch v.Kind {
	case units.KColor:
		if v.Color.A == 255 {
			return fmt.Sprintf("#%02x%02x%02x", v.Color.R, v.Color.G, v.Color.B)
		}
		return v.Color.Hex()
	case units.KSwitch:
		return v.Bool
	case units.KNumber:
		return f32(det.Round32(v.Num))
	}
	return v.Format()
}
