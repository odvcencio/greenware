package build

import (
	"errors"
	"fmt"

	"m31labs.dev/greenware/kernel/mesh"
)

// BevelEdges chooses which cap edges a bevel rounds.
type BevelEdges int

// Cap edges. Top is toward +axis.
const (
	BevelCaps BevelEdges = iota // both cap edges (the default)
	BevelTop
	BevelBottom
)

// Bevel rounds cap edges of a cylinder. Segments is the number of equal
// angular steps in the round; 1 is a chamfer.
type Bevel struct {
	Width    float64
	Segments int
	Edges    BevelEdges
}

// CylinderParams describes a cylinder centred on Centre, with its axis along
// Axis and its ends half of Height away.
type CylinderParams struct {
	Centre   mesh.Vec3
	Axis     Axis
	Radius   float64
	Height   float64
	Segments int
	Caps     bool
	Bevel    *Bevel
}

// Cylinder builds a cylinder as one shell. With n segments it has 2n side
// triangles, plus n-2 per cap, plus 2*n*b per beveled cap edge of b steps.
// Caps are fans from the first ring vertex, with no centre vertex. Side and
// cap share ring positions, so a capped cylinder is closed. The side's
// texture coordinate v is the height share; caps are planar in the square
// enclosing the circle.
func Cylinder(p CylinderParams) (*mesh.Mesh, error) {
	if !(p.Radius > 0) || !(p.Height > 0) {
		return nil, errors.New("build: cylinder radius and height must be positive")
	}
	n := p.Segments
	if n < 3 {
		return nil, errors.New("build: cylinder needs at least 3 segments")
	}
	var w float64
	var b int
	var bevBot, bevTop bool
	if p.Bevel != nil {
		if !p.Caps {
			return nil, errors.New("build: a bevel needs capped ends")
		}
		w = p.Bevel.Width
		b = p.Bevel.Segments
		if b == 0 {
			b = 1
		}
		if !(w > 0) || b < 1 {
			return nil, errors.New("build: bevel width and segments must be positive")
		}
		bevTop = p.Bevel.Edges == BevelCaps || p.Bevel.Edges == BevelTop
		bevBot = p.Bevel.Edges == BevelCaps || p.Bevel.Edges == BevelBottom
		used := 0.0
		if bevTop {
			used += w
		}
		if bevBot {
			used += w
		}
		if w >= p.Radius || used >= p.Height {
			return nil, fmt.Errorf("build: bevel width %g consumes the cylinder face", w)
		}
	}
	h2 := p.Height / 2
	var rows []revRow
	arc := func(centreR, centreY float64, ny float64, from, to, step int) {
		for i := from; ; i += step {
			s, c := mesh.SinCosTurn(i, 4*b) // angle (pi/2)*i/b: s = sin, c = cos
			rows = append(rows, revRow{
				R: centreR + float64(w*c), Y: centreY + float64(w*s*ny),
				NR: c, NY: float64(s * ny), Share: -1,
			})
			if i == to {
				break
			}
		}
	}
	if bevBot {
		arc(p.Radius-w, -h2+w, -1, b, 0, -1)
	} else {
		rows = append(rows, revRow{R: p.Radius, Y: -h2, NR: 1, Share: -1})
	}
	if bevTop {
		arc(p.Radius-w, h2-w, 1, 0, b, 1)
	} else {
		rows = append(rows, revRow{R: p.Radius, Y: h2, NR: 1, Share: -1})
	}
	for i := range rows {
		rows[i].V = (rows[i].Y + h2) / p.Height
	}
	bands := make([][2]int, 0, len(rows)-1)
	for i := 0; i+1 < len(rows); i++ {
		bands = append(bands, [2]int{i, i + 1})
	}
	rv, err := revolve(rows, n, bands)
	if err != nil {
		return nil, err
	}
	if p.Caps {
		addCap(rv, 0, p.Radius, false)
		addCap(rv, len(rows)-1, p.Radius, true)
	}
	return place(rv.m, p.Axis, p.Centre), nil
}

// addCap adds a flat n-gon fan over the ring at row r. top selects the +Y
// facing cap; the bottom cap faces -Y and winds the other way.
func addCap(rv *revolved, r int, radius float64, top bool) {
	m := rv.m
	n := rv.n
	// The ring radius is the z of column 0, which sits at angle zero (+Z).
	ringR := m.Positions[rv.pos(r, 0)][2]
	ny := -1.0
	vs := -1.0
	if top {
		ny, vs = 1, 1
	}
	base := int32(len(m.Corners))
	for j := 0; j < n; j++ {
		share := ringR / radius
		m.Corners = append(m.Corners, mesh.Corner{
			Pos:    rv.pos(r, j),
			Normal: mesh.Vec3{0, ny, 0},
			UV: mesh.Vec2{
				0.5 + float64(0.5*float64(share*rv.sin[j])),
				0.5 + float64(0.5*float64(vs*float64(share*rv.cos[j]))),
			},
		})
	}
	for j := 1; j+1 < n; j++ {
		if top {
			rv.tri(base, base+int32(j), base+int32(j+1))
		} else {
			rv.tri(base, base+int32(j+1), base+int32(j))
		}
	}
}
