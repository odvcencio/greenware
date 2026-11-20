package mesh

// ComputeNormals sets every corner normal to the normalised sum of the area
// vectors of the faces that touch the corner's position inside the same
// shell. Positions are welded by exact equality. Faces of another shell are
// never mixed in, even where their positions coincide. Corners that share a
// position and a shell get the same normal, so a texture seam shows no
// crease. A position whose faces cancel out gets a zero normal and fails
// Validate.
func (m *Mesh) ComputeNormals() {
	w := m.Weld()
	type key struct{ shell, weld int32 }
	index := map[key]int{} // lookup only; never ranged over
	var sums []Vec3
	slot := func(t int, ci int32) int {
		k := key{m.Shell[t], w.ID[m.Corners[ci].Pos]}
		s, ok := index[k]
		if !ok {
			s = len(sums)
			index[k] = s
			sums = append(sums, Vec3{})
		}
		return s
	}
	for t, tri := range m.Tris {
		a, b, c := m.TriPositions(t)
		av := Cross(Sub(b, a), Sub(c, a)) // twice the area, along the face normal
		for _, ci := range tri {
			s := slot(t, ci)
			sums[s] = Add(sums[s], av)
		}
	}
	for t, tri := range m.Tris {
		for _, ci := range tri {
			m.Corners[ci].Normal = Normalize(sums[slot(t, ci)])
		}
	}
}
