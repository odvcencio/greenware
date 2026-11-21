package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/gltf"
)

// shell is one named group of vertices and triangles: a mesh node (or one of
// its primitives), a census part, or a connected piece of a single primitive.
type shell struct {
	Name  string
	Label string // dominant joint, for reading
	Verts []int  // global vertex indices, ascending
	Tris  []int  // indices into file.Tri
}

// file is a GLB reduced to flat per-vertex arrays over all mesh nodes in node
// order and all primitives in file order.
type file struct {
	Path  string
	Bytes int
	Doc   *gltf.Document
	P     [][3]float64
	N     [][3]float64 // nil when no primitive has normals
	UV    [][2]float64 // nil when no primitive has UVs
	J     [][4]string  // joint names
	W     [][4]float32
	Tri   [][3]int
	// Targets: morph target name -> per-vertex delta.
	TargetOrder []string
	Targets     map[string][][3]float64
	Shells      []shell
	Mode        string // how the shells were found
	pieces      bool
}

type block struct {
	name       string
	v0, v1     int
	t0, t1     int
	nodeSkinOK bool
}

func loadFile(path string, census []compiler.CensusPart, scale float64) (*file, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := gltf.Decode(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	scaleDoc(doc, float32(scale))
	return build(path, len(b), doc, census)
}

func jointName(doc *gltf.Document, skin *int, j uint16) string {
	if skin == nil || *skin >= len(doc.Skins) {
		return fmt.Sprintf("j%d", j)
	}
	js := doc.Skins[*skin].Joints
	if int(j) >= len(js) || js[j] >= len(doc.Nodes) {
		return fmt.Sprintf("j%d", j)
	}
	return doc.Nodes[js[j]].Name
}

func build(path string, size int, doc *gltf.Document, census []compiler.CensusPart) (*file, error) {
	f := &file{Path: path, Bytes: size, Doc: doc, Targets: map[string][][3]float64{}}
	var blocks []block
	hasN, hasUV := false, false
	for _, n := range doc.Nodes {
		if n.Mesh == nil {
			continue
		}
		for _, pr := range doc.Meshes[*n.Mesh].Primitives {
			hasN = hasN || len(pr.Normals) > 0
			hasUV = hasUV || len(pr.UV0) > 0
		}
	}
	for ni, n := range doc.Nodes {
		if n.Mesh == nil {
			continue
		}
		m := doc.Meshes[*n.Mesh]
		for pi, pr := range m.Primitives {
			nv := len(pr.Positions) / 3
			base := len(f.P)
			t0 := len(f.Tri)
			for i := 0; i < nv; i++ {
				f.P = append(f.P, [3]float64{float64(pr.Positions[3*i]), float64(pr.Positions[3*i+1]), float64(pr.Positions[3*i+2])})
				if hasN {
					var v [3]float64
					if len(pr.Normals) > 0 {
						v = [3]float64{float64(pr.Normals[3*i]), float64(pr.Normals[3*i+1]), float64(pr.Normals[3*i+2])}
					}
					f.N = append(f.N, v)
				}
				if hasUV {
					var v [2]float64
					if len(pr.UV0) > 0 {
						v = [2]float64{float64(pr.UV0[2*i]), float64(pr.UV0[2*i+1])}
					}
					f.UV = append(f.UV, v)
				}
				var jn [4]string
				var w [4]float32
				if len(pr.Joints0) > 0 {
					for k := 0; k < 4; k++ {
						w[k] = pr.Weights0[4*i+k]
						if w[k] > 0 {
							jn[k] = jointName(doc, n.Skin, pr.Joints0[4*i+k])
						}
					}
				}
				f.J = append(f.J, jn)
				f.W = append(f.W, w)
			}
			if len(pr.Indices) > 0 {
				for i := 0; i+2 < len(pr.Indices); i += 3 {
					f.Tri = append(f.Tri, [3]int{base + int(pr.Indices[i]), base + int(pr.Indices[i+1]), base + int(pr.Indices[i+2])})
				}
			} else {
				for i := 0; i+2 < nv; i += 3 {
					f.Tri = append(f.Tri, [3]int{base + i, base + i + 1, base + i + 2})
				}
			}
			for ti, t := range pr.Targets {
				name := fmt.Sprintf("target%d", ti)
				if ti < len(m.TargetNames) && m.TargetNames[ti] != "" {
					name = m.TargetNames[ti]
				}
				d, ok := f.Targets[name]
				if !ok {
					f.TargetOrder = append(f.TargetOrder, name)
				}
				f.Targets[name] = append(d, make([][3]float64, base+nv-len(d))...)
				d = f.Targets[name]
				for i := 0; i < nv && len(t.Positions) >= 3*(i+1); i++ {
					d[base+i] = [3]float64{float64(t.Positions[3*i]), float64(t.Positions[3*i+1]), float64(t.Positions[3*i+2])}
				}
			}
			name := n.Name
			if name == "" {
				name = fmt.Sprintf("node%d", ni)
			}
			if len(m.Primitives) > 1 {
				name = fmt.Sprintf("%s/p%d", name, pi)
			}
			blocks = append(blocks, block{name: name, v0: base, v1: base + nv, t0: t0, t1: len(f.Tri)})
		}
	}
	for name, d := range f.Targets { // pad to full length
		f.Targets[name] = append(d, make([][3]float64, len(f.P)-len(d))...)
	}
	switch {
	case len(census) > 0:
		f.Mode = "census parts"
		f.censusShells(blocks, census)
	case len(blocks) == 1:
		f.Mode = "connected pieces of the single primitive (the file carries no part names)"
		f.pieces = true
		f.componentShells(blocks[0])
	default:
		f.Mode = "one shell per mesh node"
		for _, bl := range blocks {
			s := shell{Name: bl.name}
			for v := bl.v0; v < bl.v1; v++ {
				s.Verts = append(s.Verts, v)
			}
			for t := bl.t0; t < bl.t1; t++ {
				s.Tris = append(s.Tris, t)
			}
			f.Shells = append(f.Shells, s)
		}
	}
	for i := range f.Shells {
		f.Shells[i].Label = f.dominantJoint(f.Shells[i].Verts)
	}
	f.uniqueNames()
	return f, nil
}

func (f *file) uniqueNames() {
	seen := map[string]int{}
	for i := range f.Shells {
		n := f.Shells[i].Name
		seen[n]++
		if seen[n] > 1 {
			f.Shells[i].Name = fmt.Sprintf("%s#%d", n, seen[n])
		}
	}
}

func (f *file) dominantJoint(vs []int) string {
	sum := map[string]float64{}
	for _, v := range vs {
		for k := 0; k < 4; k++ {
			if f.J[v][k] != "" {
				sum[f.J[v][k]] += float64(f.W[v][k])
			}
		}
	}
	best, bw := "", 0.0
	for n, w := range sum {
		if w > bw || (w == bw && n < best) {
			best, bw = n, w
		}
	}
	return best
}

// censusShells assigns the build record's parts in order, as the parity
// model does: a part takes the next Vertices vertices and Triangles triangles.
func (f *file) censusShells(blocks []block, census []compiler.CensusPart) {
	next := 0
	for _, bl := range blocks {
		v, t := bl.v0, bl.t0
		for next < len(census) && strings.HasPrefix(census[next].Name, "mount/") {
			next++
		}
		for next < len(census) && v+census[next].Vertices <= bl.v1 && t+census[next].Triangles <= bl.t1 {
			p := census[next]
			s := shell{Name: p.Name}
			for i := 0; i < p.Vertices; i++ {
				s.Verts = append(s.Verts, v+i)
			}
			for i := 0; i < p.Triangles; i++ {
				s.Tris = append(s.Tris, t+i)
			}
			f.Shells = append(f.Shells, s)
			v += p.Vertices
			t += p.Triangles
			next++
			for next < len(census) && strings.HasPrefix(census[next].Name, "mount/") {
				next++
			}
		}
		if v < bl.v1 || t < bl.t1 {
			s := shell{Name: bl.name + "/rest"}
			for i := v; i < bl.v1; i++ {
				s.Verts = append(s.Verts, i)
			}
			for i := t; i < bl.t1; i++ {
				s.Tris = append(s.Tris, i)
			}
			f.Shells = append(f.Shells, s)
		}
	}
}

func (f *file) componentShells(bl block) {
	n := bl.v1 - bl.v0
	par := make([]int, n)
	for i := range par {
		par[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for par[x] != x {
			par[x] = par[par[x]]
			x = par[x]
		}
		return x
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		if ra > rb {
			ra, rb = rb, ra
		}
		par[rb] = ra
	}
	used := make([]bool, n)
	for t := bl.t0; t < bl.t1; t++ {
		a, b, c := f.Tri[t][0]-bl.v0, f.Tri[t][1]-bl.v0, f.Tri[t][2]-bl.v0
		used[a], used[b], used[c] = true, true, true
		union(a, b)
		union(a, c)
	}
	idx := map[int]int{}
	var shells []shell
	for v := 0; v < n; v++ {
		if !used[v] {
			continue
		}
		r := find(v)
		k, ok := idx[r]
		if !ok {
			k = len(shells)
			idx[r] = k
			shells = append(shells, shell{Name: fmt.Sprintf("piece%03d", k)})
		}
		shells[k].Verts = append(shells[k].Verts, bl.v0+v)
	}
	for t := bl.t0; t < bl.t1; t++ {
		k := idx[find(f.Tri[t][0]-bl.v0)]
		shells[k].Tris = append(shells[k].Tris, t)
	}
	var unref []int
	for v := 0; v < n; v++ {
		if !used[v] {
			unref = append(unref, bl.v0+v)
		}
	}
	if len(unref) > 0 {
		shells = append(shells, shell{Name: "unreferenced", Verts: unref})
	}
	sort.SliceStable(shells, func(i, j int) bool { return false })
	f.Shells = shells
}
