package build

import (
	"m31labs.dev/greenware/kernel/mesh"
)

// GoalPostOptions selects one output and level of the sample goal post
// (goalpost.gw). The zero value is the stadium output at level 0.
type GoalPostOptions struct {
	MaxError  float64 // the level's max_error in metres; 0 means level 0
	DropBolts bool    // lod 1 and 2 drop the bolt template
	DropBase  bool    // lod 2 drops the base
	DropPad   bool    // lod 2 drops the pad
	NoPadding bool    // the practice output sets padding = off
	// UprightRise overrides upright_rise in metres; 0 keeps 9.14.
	UprightRise float64
}

// GoalPostLevel returns the options of level 0, 1 or 2 of the sample file.
func GoalPostLevel(level int) GoalPostOptions {
	switch level {
	case 1:
		return GoalPostOptions{MaxError: 0.005, DropBolts: true}
	case 2:
		return GoalPostOptions{MaxError: 0.030, DropBolts: true, DropBase: true, DropPad: true}
	}
	return GoalPostOptions{}
}

// GoalPostParts holds the meshes of one built goal post, each in goal-post
// coordinates. A part that the level drops is nil.
type GoalPostParts struct {
	Bolts, Plate                    *mesh.Mesh // goalpost/base/bolts, goalpost/base/plate
	Post, Crossbar                  *mesh.Mesh
	UprightL, UprightR              *mesh.Mesh // each is a cylinder shell and a sphere shell
	Pad                             *mesh.Mesh
	PostPath                        []mesh.Vec3
	PlateSegments, PostSegments     int
	CrossbarSegments, PadSegments   int
	UprightSegments, SphereSegments int
	SphereRings                     int
}

// Frame merges the painted mesh: post, crossbar, then the left and right
// uprights, in that order.
func (g GoalPostParts) Frame() *mesh.Mesh {
	return mesh.Merge(g.Post, g.Crossbar, g.UprightL, g.UprightR)
}

// Base merges the steel mesh: plate, then bolts. It is nil when the base is
// dropped.
func (g GoalPostParts) Base() *mesh.Mesh {
	if g.Plate == nil {
		return nil
	}
	return mesh.Merge(g.Plate, g.Bolts)
}

// TriangleCounts returns triangles per part path in table order.
func (g GoalPostParts) TriangleCounts() map[string]int {
	out := map[string]int{}
	for name, m := range map[string]*mesh.Mesh{
		"goalpost/base/bolts": g.Bolts, "goalpost/base/plate": g.Plate,
		"goalpost/post": g.Post, "goalpost/crossbar": g.Crossbar,
		"goalpost/upright_l": g.UprightL, "goalpost/upright_r": g.UprightR,
		"goalpost/pad": g.Pad,
	} {
		if m != nil {
			out[name] = m.TriangleCount()
		}
	}
	return out
}

// GoalPost builds the sample goal post with its default parameters
// (crossbar_height 3.05, upright_gap 5.64, upright_rise 9.14, setback 1.83),
// re-tessellated to the level in o.
func GoalPost(o GoalPostOptions) (GoalPostParts, error) {
	const (
		crossbarHeight = 3.05
		uprightGap     = 5.64
		setback        = 1.83
		postR          = 0.10
		barR           = 0.06
		uprightR       = 0.05
	)
	rise := 9.14
	if o.UprightRise != 0 {
		rise = o.UprightRise
	}
	bendStart := crossbarHeight - setback
	halfSpan := uprightGap/2 + uprightR
	var g GoalPostParts
	var err error

	if !o.DropBase {
		plateP := RetessellateCylinder(CylinderParams{
			Centre: mesh.Vec3{0, 0.015, setback}, Radius: 0.35, Height: 0.03,
			Segments: 24, Caps: true,
			Bevel: &Bevel{Width: 0.006, Segments: 2, Edges: BevelTop},
		}, o.MaxError)
		g.PlateSegments = plateP.Segments
		if g.Plate, err = Cylinder(plateP); err != nil {
			return g, err
		}
		if !o.DropBolts {
			bolt, err := Cylinder(RetessellateCylinder(CylinderParams{
				Radius: 0.012, Height: 0.025, Segments: 6, Caps: true,
			}, o.MaxError))
			if err != nil {
				return g, err
			}
			if g.Bolts, err = RadialArray(bolt, 8, AxisY, mesh.Vec3{0, 0.03, setback}, 0.28); err != nil {
				return g, err
			}
		}
	}

	// The post path: the base point, then nine samples of the bend, a = 0..90
	// degrees in equal steps (SinCosTurn is exact at the quarter turns).
	path := []mesh.Vec3{{0, 0, setback}}
	for i := 0; i < 9; i++ {
		s, c := mesh.SinCosTurn(i, 32)
		path = append(path, mesh.Vec3{0, bendStart + float64(setback*s), float64(setback * c)})
	}
	g.PostPath = path
	postP := RetessellateSweep(SweepParams{Radius: postR, Segments: 16, Path: path, Caps: CapsBoth}, o.MaxError)
	g.PostSegments = postP.Segments
	if g.Post, err = Sweep(postP); err != nil {
		return g, err
	}

	barP := RetessellateCylinder(CylinderParams{
		Centre: mesh.Vec3{0, crossbarHeight, 0}, Axis: AxisX,
		Radius: barR, Height: 2 * halfSpan, Segments: 16, Caps: true,
	}, o.MaxError)
	g.CrossbarSegments = barP.Segments
	if g.Crossbar, err = Cylinder(barP); err != nil {
		return g, err
	}

	upP := RetessellateCylinder(CylinderParams{
		Centre: mesh.Vec3{halfSpan, crossbarHeight + (rise-barR)/2, 0},
		Radius: uprightR, Height: rise + barR, Segments: 12, Caps: true,
	}, o.MaxError)
	g.UprightSegments = upP.Segments
	upright, err := Cylinder(upP)
	if err != nil {
		return g, err
	}
	spP := RetessellateSphere(SphereParams{
		Centre: mesh.Vec3{halfSpan, crossbarHeight + rise, 0}, Radius: uprightR, Segments: 12,
	}, o.MaxError)
	g.SphereSegments, g.SphereRings = spP.Segments, spP.Rings
	sphere, err := Sphere(spP)
	if err != nil {
		return g, err
	}
	g.UprightL = mesh.Merge(upright, sphere)
	g.UprightR = g.UprightL.Mirror(mesh.PlaneX)

	if !o.NoPadding && !o.DropPad {
		padP := RetessellateLathe(LatheParams{
			Centre: mesh.Vec3{0, 0, setback}, Segments: 24, Closed: true,
			Rows: []mesh.Vec2{{0.11, 0}, {0.25, 0.02}, {0.26, 0.10}, {0.26, 1.70}, {0.25, 1.78}, {0.11, 1.80}},
		}, o.MaxError)
		g.PadSegments = padP.Segments
		if g.Pad, err = Lathe(padP); err != nil {
			return g, err
		}
	}
	return g, nil
}
