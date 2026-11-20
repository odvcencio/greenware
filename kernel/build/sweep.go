package build

import (
	"errors"
	"fmt"

	"m31labs.dev/greenware/kernel/mesh"
	"math"
)

// Caps selects which ends of a sweep are closed.
type Caps int

// Cap choices.
const (
	CapsOff Caps = iota
	CapsBoth
	CapsStart
	CapsEnd
)

// FrameRule selects how a sweep's profile frame follows its path.
type FrameRule int

// Frame rules.
const (
	// FrameMinimal carries the frame along with rotation-minimising
	// (double-reflection) transport, starting from the tube rule's frame.
	FrameMinimal FrameRule = iota
	// FrameTube recomputes the frame at every point by the tube rule.
	FrameTube
)

// Frame is the profile frame at one path point. Axis is the unit direction of
// travel; U and V are unit and perpendicular to it, with U x V = Axis.
type Frame struct {
	Axis, U, V mesh.Vec3
}

// SweepParams carries a circle of Radius with Segments vertices along Path.
type SweepParams struct {
	Radius   float64
	Segments int
	Path     []mesh.Vec3
	Caps     Caps
	Frame    FrameRule
}

// PathTangents returns the unit direction at each path point by the tube
// rule: the first point uses p1-p0, the last p(last)-p(last-1), and interior
// points the direction from the previous point to the next. Where that
// centred direction is zero (the path doubles back), the incoming segment's
// direction is used. Coincident consecutive points are an error.
func PathTangents(path []mesh.Vec3) ([]mesh.Vec3, error) {
	if len(path) < 2 {
		return nil, errors.New("build: a path needs at least 2 points")
	}
	for i := 1; i < len(path); i++ {
		if path[i] == path[i-1] {
			return nil, fmt.Errorf("build: path points %d and %d coincide", i-1, i)
		}
	}
	t := make([]mesh.Vec3, len(path))
	last := len(path) - 1
	for i := range path {
		var d mesh.Vec3
		switch i {
		case 0:
			d = mesh.Sub(path[1], path[0])
		case last:
			d = mesh.Sub(path[last], path[last-1])
		default:
			d = mesh.Sub(path[i+1], path[i-1])
			if d == (mesh.Vec3{}) {
				d = mesh.Sub(path[i], path[i-1])
			}
		}
		t[i] = mesh.Normalize(d)
	}
	return t, nil
}

// startFrame is the tube rule's frame for a direction: u = normalize(axis x
// up), v = axis x u, where up is +Y unless |axis.y| >= 0.91, then +X.
func startFrame(axis mesh.Vec3) Frame {
	up := mesh.Vec3{0, 1, 0}
	if math.Abs(axis[1]) >= 0.91 {
		up = mesh.Vec3{1, 0, 0}
	}
	u := mesh.Normalize(mesh.Cross(axis, up))
	return Frame{Axis: axis, U: u, V: mesh.Cross(axis, u)}
}

// TransportFrames returns one frame per path point. With FrameTube each point
// takes the tube rule. With FrameMinimal the first frame is the tube rule's
// and each next frame comes from the previous one by the double-reflection
// method (Wang and others, 2008), which turns the frame only as much as the
// path turns.
func TransportFrames(path []mesh.Vec3, rule FrameRule) ([]Frame, error) {
	tan, err := PathTangents(path)
	if err != nil {
		return nil, err
	}
	fr := make([]Frame, len(path))
	if rule == FrameTube {
		for i := range path {
			fr[i] = startFrame(tan[i])
		}
		return fr, nil
	}
	fr[0] = startFrame(tan[0])
	u := fr[0].U
	for i := 0; i+1 < len(path); i++ {
		v1 := mesh.Sub(path[i+1], path[i])
		c1 := mesh.Dot(v1, v1)
		rL := mesh.Sub(u, mesh.Scale(v1, 2*mesh.Dot(v1, u)/c1))
		tL := mesh.Sub(tan[i], mesh.Scale(v1, 2*mesh.Dot(v1, tan[i])/c1))
		v2 := mesh.Sub(tan[i+1], tL)
		c2 := mesh.Dot(v2, v2)
		next := rL
		if c2 != 0 {
			next = mesh.Sub(rL, mesh.Scale(v2, 2*mesh.Dot(v2, rL)/c2))
		}
		// Re-orthogonalise against the new direction so rounding cannot build up.
		next = mesh.Sub(next, mesh.Scale(tan[i+1], mesh.Dot(next, tan[i+1])))
		u = mesh.Normalize(next)
		fr[i+1] = Frame{Axis: tan[i+1], U: u, V: mesh.Cross(tan[i+1], u)}
	}
	return fr, nil
}

// Sweep carries a circle along a path as one shell. Ring vertex j of a path
// point sits at point + r*(cos(t)*U + sin(t)*V), t = 2*pi*j/n. The side has
// 2n triangles per path interval; each cap adds n-2. The texture u is the
// profile fraction j/n and v the path-length fraction; caps are planar. Side
// normals are exact radial directions; caps face along the path.
func Sweep(p SweepParams) (*mesh.Mesh, error) {
	if !(p.Radius > 0) {
		return nil, errors.New("build: sweep radius must be positive")
	}
	n := p.Segments
	if n < 3 {
		return nil, errors.New("build: sweep profile needs at least 3 segments")
	}
	fr, err := TransportFrames(p.Path, p.Frame)
	if err != nil {
		return nil, err
	}
	np := len(p.Path)
	sn := make([]float64, n+1)
	cs := make([]float64, n+1)
	for j := 0; j < n; j++ {
		sn[j], cs[j] = mesh.SinCosTurn(j, n)
	}
	sn[n], cs[n] = sn[0], cs[0]

	// Path-length fraction for v.
	vs := make([]float64, np)
	total := 0.0
	for i := 1; i < np; i++ {
		total += mesh.Len(mesh.Sub(p.Path[i], p.Path[i-1]))
		vs[i] = total
	}
	for i := range vs {
		vs[i] /= total
	}

	m := &mesh.Mesh{}
	for i := 0; i < np; i++ {
		for j := 0; j < n; j++ {
			off := mesh.Add(mesh.Scale(fr[i].U, float64(p.Radius*cs[j])), mesh.Scale(fr[i].V, float64(p.Radius*sn[j])))
			m.Positions = append(m.Positions, mesh.Add(p.Path[i], off))
		}
	}
	corner := func(i, j int) int32 { return int32(i*(n+1) + j) }
	for i := 0; i < np; i++ {
		for j := 0; j <= n; j++ {
			nrm := mesh.Add(mesh.Scale(fr[i].U, cs[j]), mesh.Scale(fr[i].V, sn[j]))
			m.Corners = append(m.Corners, mesh.Corner{
				Pos:    int32(i*n + j%n),
				Normal: mesh.Normalize(nrm),
				UV:     mesh.Vec2{float64(j) / float64(n), vs[i]},
			})
		}
	}
	tri := func(a, b, c int32) {
		m.Tris = append(m.Tris, mesh.Tri{a, b, c})
		m.Shell = append(m.Shell, 0)
	}
	for i := 0; i+1 < np; i++ {
		for j := 0; j < n; j++ {
			a, b, c, d := corner(i, j), corner(i, j+1), corner(i+1, j+1), corner(i+1, j)
			tri(a, b, c)
			tri(a, c, d)
		}
	}
	cap := func(ring int, sign float64) {
		base := int32(len(m.Corners))
		nrm := mesh.Scale(fr[ring].Axis, sign)
		for j := 0; j < n; j++ {
			m.Corners = append(m.Corners, mesh.Corner{
				Pos:    int32(ring*n + j),
				Normal: nrm,
				UV:     mesh.Vec2{0.5 + float64(0.5*cs[j]), 0.5 + float64(0.5*float64(sign*sn[j]))},
			})
		}
		for j := 1; j+1 < n; j++ {
			if sign > 0 {
				tri(base, base+int32(j), base+int32(j+1))
			} else {
				tri(base, base+int32(j+1), base+int32(j))
			}
		}
	}
	if p.Caps == CapsBoth || p.Caps == CapsStart {
		cap(0, -1)
	}
	if p.Caps == CapsBoth || p.Caps == CapsEnd {
		cap(np-1, 1)
	}
	return m, nil
}
