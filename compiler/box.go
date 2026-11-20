package compiler

import (
	"fmt"

	"m31labs.dev/greenware/kernel/build"
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/resolve"
)

// box builds a `box` shape: six flat faces, or a rounded shell when a `bevel`
// block rounds its edges (language.md sections 3.4, 3.5 and 3.10). A box has
// no circular division, so a level's chord error leaves it unchanged.
func (e *elab) box(n *resolve.Node) (*mesh.Mesh, error) {
	p := build.BoxParams{
		Centre:   vec3(n, "at"),
		Size:     vec3(n, "size"),
		Segments: int(num(n, "segments")),
	}
	if b := child(n, "bevel"); b != nil {
		switch label(b, "edges") {
		case "", "all":
		default:
			return nil, fmt.Errorf("`edges = %s` on a box bevel is not supported yet: language.md 3.10 defines the round of all twelve edges of a box only", label(b, "edges"))
		}
		p.Bevel = &build.BoxBevel{Width: num(b, "width"), Segments: int(num(b, "segments"))}
		if p.Segments > 1 {
			return nil, fmt.Errorf("a bevel needs `segments = 1` on the box itself (language.md 3.10), not %d", p.Segments)
		}
	}
	return build.Box(p)
}
