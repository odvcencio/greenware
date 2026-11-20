// Package mesh is the float64 surface type for the Greenware geometry kernel.
//
// A Mesh keeps positions, per-corner attributes and triangles apart. A corner
// is one use of a position with its own normal and texture coordinate, so a
// texture seam duplicates a corner and leaves the position alone. Triangles
// index corners. Every triangle belongs to one shell. A shell is a connected
// component by construction, and normals are averaged only inside a shell.
//
// All arithmetic is float64 and goes through package det for sin, cos and
// sqrt, so the same inputs give the same bits on every architecture. Float32
// appears only in the Float32 accessors.
package mesh

import (
	"errors"
	"fmt"
	"math"
)

// Corner is one use of a position by a face: the position plus the attributes
// that may differ between faces meeting there.
type Corner struct {
	Pos    int32 // index into Mesh.Positions
	Normal Vec3  // unit length once normals are computed
	UV     Vec2  // texture coordinate, set by the builder
}

// Tri is a triangle as three corner indices, counter-clockwise seen from the
// front (the side the normal points to).
type Tri [3]int32

// Mesh is an indexed surface.
type Mesh struct {
	Positions []Vec3
	Corners   []Corner
	Tris      []Tri
	Shell     []int32 // Shell[i] is the shell id of Tris[i]
}

// TriangleCount returns the number of triangles.
func (m *Mesh) TriangleCount() int { return len(m.Tris) }

// NumShells returns one more than the largest shell id, or zero when the mesh
// has no triangles.
func (m *Mesh) NumShells() int {
	n := int32(-1)
	for _, s := range m.Shell {
		if s > n {
			n = s
		}
	}
	return int(n) + 1
}

// TriPositions returns the three positions of triangle i.
func (m *Mesh) TriPositions(i int) (a, b, c Vec3) {
	t := m.Tris[i]
	return m.Positions[m.Corners[t[0]].Pos], m.Positions[m.Corners[t[1]].Pos], m.Positions[m.Corners[t[2]].Pos]
}

// Bounds returns the axis-aligned bounds of the positions used by triangles.
// ok is false for a mesh with no triangles.
func (m *Mesh) Bounds() (lo, hi Vec3, ok bool) {
	for i := range m.Tris {
		for _, ci := range m.Tris[i] {
			p := m.Positions[m.Corners[ci].Pos]
			if !ok {
				lo, hi, ok = p, p, true
				continue
			}
			for k := 0; k < 3; k++ {
				if p[k] < lo[k] {
					lo[k] = p[k]
				}
				if p[k] > hi[k] {
					hi[k] = p[k]
				}
			}
		}
	}
	return lo, hi, ok
}

// Clone returns a deep copy.
func (m *Mesh) Clone() *Mesh {
	return &Mesh{
		Positions: append([]Vec3(nil), m.Positions...),
		Corners:   append([]Corner(nil), m.Corners...),
		Tris:      append([]Tri(nil), m.Tris...),
		Shell:     append([]int32(nil), m.Shell...),
	}
}

// Merge concatenates meshes in argument order into a new mesh. Shell ids of
// each input are shifted past the shells of the inputs before it, so shells
// never collide.
func Merge(ms ...*Mesh) *Mesh {
	out := &Mesh{}
	var shellBase int32
	for _, m := range ms {
		if m == nil {
			continue
		}
		pb := int32(len(out.Positions))
		cb := int32(len(out.Corners))
		out.Positions = append(out.Positions, m.Positions...)
		for _, c := range m.Corners {
			c.Pos += pb
			out.Corners = append(out.Corners, c)
		}
		for i, t := range m.Tris {
			out.Tris = append(out.Tris, Tri{t[0] + cb, t[1] + cb, t[2] + cb})
			out.Shell = append(out.Shell, m.Shell[i]+shellBase)
		}
		shellBase += int32(m.NumShells())
	}
	return out
}

// Validate checks indices, finite values and unit normals.
func (m *Mesh) Validate() error {
	if len(m.Shell) != len(m.Tris) {
		return errors.New("mesh: shell list and triangle list differ in length")
	}
	for i, p := range m.Positions {
		for _, v := range p {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("mesh: position %d is not finite", i)
			}
		}
	}
	for i, c := range m.Corners {
		if c.Pos < 0 || int(c.Pos) >= len(m.Positions) {
			return fmt.Errorf("mesh: corner %d uses position %d out of range", i, c.Pos)
		}
		l := Len(c.Normal)
		if math.IsNaN(l) || l < 1-1e-9 || l > 1+1e-9 {
			return fmt.Errorf("mesh: corner %d normal has length %g", i, l)
		}
		if math.IsNaN(c.UV[0]) || math.IsNaN(c.UV[1]) {
			return fmt.Errorf("mesh: corner %d has a NaN uv", i)
		}
	}
	for i, t := range m.Tris {
		for _, ci := range t {
			if ci < 0 || int(ci) >= len(m.Corners) {
				return fmt.Errorf("mesh: triangle %d uses corner %d out of range", i, ci)
			}
		}
		if m.Shell[i] < 0 {
			return fmt.Errorf("mesh: triangle %d has a negative shell id", i)
		}
	}
	return nil
}

// PositionsF32 returns the positions as flat float32 triples.
func (m *Mesh) PositionsF32() []float32 {
	out := make([]float32, 0, 3*len(m.Positions))
	for _, p := range m.Positions {
		f := p.F32()
		out = append(out, f[0], f[1], f[2])
	}
	return out
}

// NormalsF32 returns the corner normals as flat float32 triples.
func (m *Mesh) NormalsF32() []float32 {
	out := make([]float32, 0, 3*len(m.Corners))
	for _, c := range m.Corners {
		f := c.Normal.F32()
		out = append(out, f[0], f[1], f[2])
	}
	return out
}

// UVsF32 returns the corner texture coordinates as flat float32 pairs.
func (m *Mesh) UVsF32() []float32 {
	out := make([]float32, 0, 2*len(m.Corners))
	for _, c := range m.Corners {
		f := c.UV.F32()
		out = append(out, f[0], f[1])
	}
	return out
}
