package accept

import (
	"fmt"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/internal/pbrcolor"
	"m31labs.dev/greenware/units"
)

func (ev *evaluator) material(c *ast.Call) (val, *evalErr) {
	args, err := bind(c, "node")
	if err != nil {
		return val{}, err
	}
	handle, err := ev.handleArg(args[0], "material")
	if err != nil {
		return val{}, err
	}
	node := ev.sc.doc.Nodes[handle.idx]
	if node.Mesh == nil || !ev.sc.nodes[handle.idx].visible {
		return val{}, refuse("node %q has no visible primitives of its own", handle.name)
	}
	primitives := ev.sc.doc.Meshes[*node.Mesh].Primitives
	index := -1
	for _, p := range primitives {
		if p.Material == nil {
			return val{}, refuse("node %q has a primitive without an assigned material", handle.name)
		}
		if index >= 0 && index != *p.Material {
			return val{}, refuse("node %q has more than one visible material", handle.name)
		}
		index = *p.Material
	}
	if index < 0 {
		return val{}, refuse("node %q has no visible primitives of its own", handle.name)
	}
	return val{m: &ev.sc.doc.Materials[index]}, nil
}

func (ev *evaluator) materialField(material *gltf.Material, name string) (val, *evalErr) {
	var number float64
	switch name {
	case "base":
		c := units.Color{
			R: pbrcolor.SRGB(float64(material.BaseColor[0])),
			G: pbrcolor.SRGB(float64(material.BaseColor[1])),
			B: pbrcolor.SRGB(float64(material.BaseColor[2])),
			A: 255,
		}
		ev.record("material.base", colorText(c), nil)
		return uval(units.Col(c)), nil
	case "alpha":
		number = float64(material.BaseColor[3])
	case "metallic":
		number = float64(material.Metallic)
	case "roughness":
		number = float64(material.Roughness)
	default:
		return val{}, refuse("material factors have no member %q", name)
	}
	ev.record("material."+name, fmtNum(number), &number)
	return uval(units.Num(number)), nil
}

func colorText(c units.Color) string {
	if c.A == 255 {
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A)
}
