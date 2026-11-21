package build

import (
	"errors"
	"fmt"

	"m31labs.dev/greenware/kernel/mesh"
)

// TubeParams describes a circle of Radius with Segments vertices carried
// through the points of Path.
type TubeParams struct {
	Radius   float64
	Segments int
	Path     []mesh.Vec3
	// Closed joins the last path point back to the first with one more band.
	// The first point is not repeated at the end.
	Closed bool
	// NoSeam drops the repeated column: every ring has n corners and the last
	// band's texture u runs from (n-1)/n back to 0.
	NoSeam bool
}

// tubeTangents returns the unit direction at each path point (language.md
// section 3.6). An open path uses the single neighbouring segment at each end
// and the centred difference inside; a closed path wraps its neighbours. Where
// the centred difference is zero (an exact reversal) the incoming segment is
// used. A zero-length segment, including the wrap edge of a closed path, is an
// error.
func tubeTangents(path []mesh.Vec3, closed bool) ([]mesh.Vec3, error) {
	p := len(path)
	least := 2
	if closed {
		least = 3
	}
	if p < least {
		return nil, fmt.Errorf("build: a tube path needs at least %d points", least)
	}
	segs := p - 1
	if closed {
		segs = p
	}
	for i := 0; i < segs; i++ {
		if path[i] == path[(i+1)%p] {
			return nil, fmt.Errorf("build: tube path points %d and %d coincide", i, (i+1)%p)
		}
	}
	t := make([]mesh.Vec3, p)
	for i := range path {
		var d mesh.Vec3
		switch {
		case !closed && i == 0:
			d = mesh.Sub(path[1], path[0])
		case !closed && i == p-1:
			d = mesh.Sub(path[p-1], path[p-2])
		default:
			prev, next := path[(i+p-1)%p], path[(i+1)%p]
			d = mesh.Sub(next, prev)
			if d == (mesh.Vec3{}) {
				d = mesh.Sub(path[i], prev)
			}
		}
		t[i] = mesh.Normalize(d)
	}
	return t, nil
}

// Tube carries a circle through a path and recomputes the frame at every
// point by the tube rule (u = normalize(t x up), v = t x u; see startFrame).
// Vertex j of a ring sits at point + r*(cos(a)*u + sin(a)*v), a = 2*pi*j/n.
// An open path with p points has 2n(p-1) triangles and no end caps; a closed
// path has 2np. Texture u is the angle fraction j/n and v the path-length
// fraction, which for a closed path includes the closing segment. Normals are
// the exact radial directions. The tube is one shell.
func Tube(p TubeParams) (*mesh.Mesh, error) {
	if !(p.Radius > 0) {
		return nil, errors.New("build: tube radius must be positive")
	}
	n := p.Segments
	if n < 3 {
		return nil, errors.New("build: a tube needs at least 3 segments")
	}
	tan, err := tubeTangents(p.Path, p.Closed)
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
	// Path-length fraction of each ring (the closing segment counts when closed).
	segs := np - 1
	if p.Closed {
		segs = np
	}
	vs := make([]float64, np+1)
	total := 0.0
	for i := 1; i <= segs; i++ {
		total += mesh.Len(mesh.Sub(p.Path[i%np], p.Path[i-1]))
		vs[i] = total
	}
	for i := range vs[:segs+1] {
		vs[i] /= total
	}
	m := &mesh.Mesh{}
	frames := make([]Frame, np)
	for i := range p.Path {
		frames[i] = startFrame(tan[i])
		for j := 0; j < n; j++ {
			off := mesh.Add(mesh.Scale(frames[i].U, float64(p.Radius*cs[j])), mesh.Scale(frames[i].V, float64(p.Radius*sn[j])))
			m.Positions = append(m.Positions, mesh.Add(p.Path[i], off))
		}
	}
	// A closed tube repeats ring zero as a last row of corners (v = 1) so the
	// texture does not wrap; the geometry joins ring np-1 to ring 0.
	last := n
	if p.NoSeam {
		last = n - 1
	}
	rowsOfCorners := np
	if p.Closed {
		rowsOfCorners = np + 1
	}
	for i := 0; i < rowsOfCorners; i++ {
		ring := i % np
		for j := 0; j <= last; j++ {
			nrm := mesh.Add(mesh.Scale(frames[ring].U, cs[j]), mesh.Scale(frames[ring].V, sn[j]))
			m.Corners = append(m.Corners, mesh.Corner{
				Pos:    int32(ring*n + j%n),
				Normal: mesh.Normalize(nrm),
				UV:     mesh.Vec2{float64(j) / float64(n), vs[i]},
			})
		}
	}
	cols := n + 1
	if p.NoSeam {
		cols = n
	}
	corner := func(i, j int) int32 { return int32(i*cols + j%cols) }
	for i := 0; i < segs; i++ {
		for j := 0; j < n; j++ {
			a, b, c, d := corner(i, j), corner(i, j+1), corner(i+1, j+1), corner(i+1, j)
			m.Tris = append(m.Tris, mesh.Tri{a, b, c}, mesh.Tri{a, c, d})
			m.Shell = append(m.Shell, 0, 0)
		}
	}
	return m, nil
}
