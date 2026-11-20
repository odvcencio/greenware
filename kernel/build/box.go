package build

import (
	"errors"
	"fmt"

	"m31labs.dev/greenware/kernel/mesh"
)

// BoxBevel rounds every edge of a box (language reference 3.10). Width is the
// setback on each adjoining face and the radius of the round; Segments is the
// number of equal angular steps in it, with 1 a chamfer.
type BoxBevel struct {
	Width    float64
	Segments int
}

// BoxParams describes a box centred on Centre with full extent Size along x, y
// and z.
type BoxParams struct {
	Centre mesh.Vec3
	Size   mesh.Vec3
	// Segments is the number of equal divisions of each face along each of its
	// edge directions; 0 means 1. A bevel needs it to be 1.
	Segments int
	Bevel    *BoxBevel
}

// Box builds a box as one closed shell with outward winding.
//
// Without a bevel it has six faces of Segments x Segments quads, so 12
// triangles at one division, and every face has corners of its own with a flat
// normal (24 corners at one division). With a bevel of b steps it has six
// trimmed face quads, 12 edges of b quads and 8 corner patches of b*b
// triangles, which is 12 + 24b + 8b*b triangles (44 at b = 1). Edge strips and
// corner patches carry the normal of the round, so a face meets its bevel
// without a visible edge.
//
// UVs follow the box projection of language reference 3.5: each triangle
// takes the plane perpendicular to the largest component of its normal (ties
// x, then y, then z) and each projected coordinate is normalised over the
// bounds of the triangles that share the plane and sign. A plain box
// therefore has one unit square per face.
func Box(p BoxParams) (*mesh.Mesh, error) {
	for _, s := range p.Size {
		if !(s > 0) {
			return nil, errors.New("build: box size must be positive in every direction")
		}
	}
	g := p.Segments
	if g == 0 {
		g = 1
	}
	if g < 1 {
		return nil, errors.New("build: box segments must be at least 1")
	}
	h := mesh.Vec3{float64(p.Size[0] / 2), float64(p.Size[1] / 2), float64(p.Size[2] / 2)}
	var m *mesh.Mesh
	if p.Bevel == nil {
		m = plainBox(h, g)
	} else {
		if g != 1 {
			return nil, errors.New("build: a bevel needs a box with segments = 1")
		}
		b := p.Bevel.Segments
		if b == 0 {
			b = 1
		}
		w := p.Bevel.Width
		if !(w > 0) || b < 1 {
			return nil, errors.New("build: bevel width and segments must be positive")
		}
		if w >= min(h[0], h[1], h[2]) {
			return nil, fmt.Errorf("build: bevel width %g consumes a face of the box", w)
		}
		m = bevelBox(h, w, b)
	}
	u, err := m.ProjectUV(mesh.ProjBox, mesh.ProjectOptions{Axis: 1})
	if err != nil {
		return nil, err
	}
	if p.Centre != (mesh.Vec3{}) {
		u = u.Translate(p.Centre)
	}
	return u, nil
}

// latticeCoord is the i-th of g equal divisions across [-h, h]. It is exactly
// antisymmetric, with the ends exactly -h and h.
func latticeCoord(h float64, i, g int) float64 {
	switch {
	case i == 0:
		return -h
	case i == g:
		return h
	case 2*i == g:
		return 0
	case 2*i > g:
		return -latticeCoord(h, g-i, g)
	}
	return float64(h*float64(2*i-g)) / float64(g)
}

// faceAxes returns, for the face on axis a, the in-plane axes (b, c) with
// e_b x e_c = e_a.
func faceAxes(a int) (b, c int) { return (a + 1) % 3, (a + 2) % 3 }

func plainBox(h mesh.Vec3, g int) *mesh.Mesh {
	m := &mesh.Mesh{}
	type key [3]int
	pos := map[key]int32{} // lookup only
	at := func(a, sa, b, i, c, j int) int32 {
		var k key
		k[a] = sa * g // lattice index along the face axis: the end, -g or g
		k[b], k[c] = 2*i-g, 2*j-g
		if id, ok := pos[k]; ok {
			return id
		}
		var p mesh.Vec3
		p[a] = float64(sa) * h[a]
		p[b], p[c] = latticeCoord(h[b], i, g), latticeCoord(h[c], j, g)
		id := int32(len(m.Positions))
		m.Positions = append(m.Positions, p)
		pos[k] = id
		return id
	}
	for a := 0; a < 3; a++ {
		for _, sa := range []int{-1, 1} {
			b, c := faceAxes(a)
			var n mesh.Vec3
			n[a] = float64(sa)
			base := int32(len(m.Corners))
			for i := 0; i <= g; i++ {
				for j := 0; j <= g; j++ {
					m.Corners = append(m.Corners, mesh.Corner{Pos: at(a, sa, b, i, c, j), Normal: n})
				}
			}
			cn := func(i, j int) int32 { return base + int32(i*(g+1)+j) }
			for i := 0; i < g; i++ {
				for j := 0; j < g; j++ {
					p00, p10, p11, p01 := cn(i, j), cn(i+1, j), cn(i+1, j+1), cn(i, j+1)
					if sa > 0 {
						addTri(m, p00, p10, p11)
						addTri(m, p00, p11, p01)
					} else {
						addTri(m, p00, p11, p10)
						addTri(m, p00, p01, p11)
					}
				}
			}
		}
	}
	return m
}

func addTri(m *mesh.Mesh, a, b, c int32) {
	m.Tris = append(m.Tris, mesh.Tri{a, b, c})
	m.Shell = append(m.Shell, 0)
}

// bevelBox builds the beveled box of half-extents h, setback w and b steps.
func bevelBox(h mesh.Vec3, w float64, b int) *mesh.Mesh {
	m := &mesh.Mesh{}
	// A patch vertex is named by its octant (bit k set: +axis k) and the
	// weights of the three axes, which add to b. Each owns one position and one
	// corner, so every piece of the shell shares them and nothing cracks.
	type vkey struct {
		oct int
		wt  [3]int
	}
	idx := map[vkey]int32{} // lookup only
	sign := func(oct, k int) float64 {
		if oct>>k&1 == 1 {
			return 1
		}
		return -1
	}
	vert := func(oct int, wt [3]int) int32 {
		k := vkey{oct, wt}
		if id, ok := idx[k]; ok {
			return id
		}
		var d mesh.Vec3
		var on []int
		for a := 0; a < 3; a++ {
			if wt[a] > 0 {
				on = append(on, a)
			}
		}
		switch len(on) {
		case 1:
			d[on[0]] = sign(oct, on[0])
		case 2:
			p, q := on[0], on[1]
			sn, cs := mesh.SinCosTurn(wt[q], 4*b) // the angle (pi/2)*wt[q]/b from p toward q
			d[p], d[q] = float64(sign(oct, p)*cs), float64(sign(oct, q)*sn)
		default:
			d = mesh.Normalize(mesh.Vec3{
				float64(sign(oct, 0) * float64(wt[0])),
				float64(sign(oct, 1) * float64(wt[1])),
				float64(sign(oct, 2) * float64(wt[2])),
			})
		}
		var p mesh.Vec3
		for a := 0; a < 3; a++ {
			if d[a] == 1 || d[a] == -1 {
				p[a] = d[a] * h[a] // on a face plane: exactly the half extent
			} else {
				p[a] = float64(sign(oct, a)*(h[a]-w)) + float64(w*d[a])
			}
		}
		id := int32(len(m.Positions))
		m.Positions = append(m.Positions, p)
		m.Corners = append(m.Corners, mesh.Corner{Pos: id, Normal: d})
		idx[k] = id
		return id
	}
	tri := func(a, bb, c int32) {
		pa, pb, pc := m.Positions[a], m.Positions[bb], m.Positions[c]
		n := mesh.Cross(mesh.Sub(pb, pa), mesh.Sub(pc, pa))
		want := mesh.Add(mesh.Add(m.Corners[a].Normal, m.Corners[bb].Normal), m.Corners[c].Normal)
		if mesh.Dot(n, want) < 0 {
			bb, c = c, bb
		}
		addTri(m, a, bb, c)
	}
	// Faces: the vertex of each octant whose weight is b on the face axis.
	for a := 0; a < 3; a++ {
		for sa := 0; sa < 2; sa++ {
			u, v := faceAxes(a)
			corner := func(su, sv int) int32 {
				oct := sa<<a | su<<u | sv<<v
				var wt [3]int
				wt[a] = b
				return vert(oct, wt)
			}
			p00, p10, p11, p01 := corner(0, 0), corner(1, 0), corner(1, 1), corner(0, 1)
			tri(p00, p10, p11)
			tri(p00, p11, p01)
		}
	}
	// Edges: along axis c, between faces on the two other axes p < q.
	for c := 0; c < 3; c++ {
		p, q := (c+1)%3, (c+2)%3
		if p > q {
			p, q = q, p
		}
		for sp := 0; sp < 2; sp++ {
			for sq := 0; sq < 2; sq++ {
				end := func(sc, t int) int32 {
					oct := sp<<p | sq<<q | sc<<c
					var wt [3]int
					wt[p], wt[q] = b-t, t
					return vert(oct, wt)
				}
				for t := 0; t < b; t++ {
					a0, a1, b1, b0 := end(0, t), end(0, t+1), end(1, t+1), end(1, t)
					tri(a0, a1, b1)
					tri(a0, b1, b0)
				}
			}
		}
	}
	// Corners: a triangular grid of b divisions over the three weights.
	for oct := 0; oct < 8; oct++ {
		at := func(x, y int) int32 { return vert(oct, [3]int{x, y, b - x - y}) }
		for x := 0; x < b; x++ {
			for y := 0; x+y < b; y++ {
				tri(at(x, y), at(x+1, y), at(x, y+1))
				if x+y+2 <= b {
					tri(at(x+1, y), at(x+1, y+1), at(x, y+1))
				}
			}
		}
	}
	return m
}
