package mesh

import (
	"math"
	"sort"
)

// Welded maps each position to a welded id. Two positions get the same id
// only when all three coordinates are exactly equal (negative zero equals
// zero). There is no tolerance. Ids count up in order of first use.
type Welded struct {
	ID    []int32 // ID[p] is the welded id of position p
	Count int     // number of distinct welded positions
}

// Weld welds positions by exact equality.
func (m *Mesh) Weld() Welded {
	type key [3]uint64
	seen := map[key]int32{} // lookup only
	ids := make([]int32, len(m.Positions))
	for i, p := range m.Positions {
		var k key
		for j, v := range p {
			if v == 0 {
				v = 0 // folds -0 into +0
			}
			k[j] = math.Float64bits(v)
		}
		id, ok := seen[k]
		if !ok {
			id = int32(len(seen))
			seen[k] = id
		}
		ids[i] = id
	}
	return Welded{ID: ids, Count: len(seen)}
}

// EdgeUse is the number of triangle sides that run along one undirected edge
// between two welded positions, A < B.
type EdgeUse struct {
	A, B  int32
	Count int
}

// EdgeUses counts triangle sides per undirected edge on welded positions. A
// texture seam is not a crack: it joins corners of one position, so it does
// not appear here. The result is sorted by (A, B).
func (m *Mesh) EdgeUses() []EdgeUse {
	w := m.Weld()
	type key struct{ a, b int32 }
	counts := map[key]int{}
	for _, t := range m.Tris {
		var v [3]int32
		for i, ci := range t {
			v[i] = w.ID[m.Corners[ci].Pos]
		}
		for i := 0; i < 3; i++ {
			a, b := v[i], v[(i+1)%3]
			if a > b {
				a, b = b, a
			}
			counts[key{a, b}]++
		}
	}
	out := make([]EdgeUse, 0, len(counts))
	for k, n := range counts {
		out = append(out, EdgeUse{A: k.a, B: k.b, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}

// OpenEdges returns the number of edges used exactly once.
func (m *Mesh) OpenEdges() int {
	n := 0
	for _, e := range m.EdgeUses() {
		if e.Count == 1 {
			n++
		}
	}
	return n
}

// IsClosed reports whether the mesh has triangles and every welded edge is
// used exactly twice.
func (m *Mesh) IsClosed() bool {
	if len(m.Tris) == 0 {
		return false
	}
	for _, e := range m.EdgeUses() {
		if e.Count != 2 {
			return false
		}
	}
	return true
}

// Components groups triangles that share a welded vertex. comp[i] is the
// component of triangle i; ids count up in order of first triangle.
func (m *Mesh) Components() (comp []int32, count int) {
	w := m.Weld()
	parent := make([]int32, w.Count)
	for i := range parent {
		parent[i] = int32(i)
	}
	var find func(x int32) int32
	find = func(x int32) int32 {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for _, t := range m.Tris {
		r0 := find(w.ID[m.Corners[t[0]].Pos])
		for _, ci := range t[1:] {
			r := find(w.ID[m.Corners[ci].Pos])
			if r != r0 {
				if r < r0 {
					r0, r = r, r0
				}
				parent[r] = r0
			}
		}
	}
	comp = make([]int32, len(m.Tris))
	ids := map[int32]int32{} // lookup only
	for i, t := range m.Tris {
		root := find(w.ID[m.Corners[t[0]].Pos])
		id, ok := ids[root]
		if !ok {
			id = int32(len(ids))
			ids[root] = id
		}
		comp[i] = id
	}
	return comp, len(ids)
}

// SignedVolume returns the volume enclosed by the triangles by the divergence
// theorem. It is positive when the faces of a closed mesh wind outward.
func (m *Mesh) SignedVolume() float64 {
	var v float64
	for i := range m.Tris {
		a, b, c := m.TriPositions(i)
		v += Dot(a, Cross(b, c))
	}
	return v / 6
}

// ClosedShells reports, for each shell id, whether the shell is closed on its
// own: it has triangles and every welded edge among its triangles is used
// exactly twice by that shell. Another shell that touches it at the same
// positions does not count, so two closed shells that overlap stay closed.
func (m *Mesh) ClosedShells() []bool {
	w := m.Weld()
	type key struct{ shell, a, b int32 }
	counts := map[key]int{}
	has := make([]bool, m.NumShells())
	for i, t := range m.Tris {
		s := m.Shell[i]
		has[s] = true
		var v [3]int32
		for k, ci := range t {
			v[k] = w.ID[m.Corners[ci].Pos]
		}
		for k := 0; k < 3; k++ {
			a, b := v[k], v[(k+1)%3]
			if a > b {
				a, b = b, a
			}
			counts[key{s, a, b}]++
		}
	}
	out := make([]bool, len(has))
	copy(out, has)
	for k, n := range counts { // order does not matter: this only clears flags
		if n != 2 {
			out[k.shell] = false
		}
	}
	return out
}
