package athleteparity

import (
	"fmt"
	"strings"

	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/gltf"
)

// MetresPerYard converts the compiled GLB (metres) to the game's yards.
const MetresPerYard = 0.9144

// Model is a decoded athlete: the vertices of every primitive of the skinned
// mesh in file order, each with the shape that owns it, its bind position in
// yards, its skin and its morph deltas.
type Model struct {
	// Name labels the model in reports (the GLB file name).
	Name string
	Doc  *gltf.Document
	// Parts is the census of the leaf shapes, as the build record holds it.
	Parts []compiler.CensusPart
	// Leaf is the last component of each part's name; Shell is the shell
	// name the game's tables know it by ("" when no table knows it).
	Leaf  []string
	Shell []string

	// Per vertex, all primitives in file order.
	Bind   [][3]float64 // bind position in yards
	Joint  [][4]uint16
	Weight [][4]float32
	Owner  []int // index into Parts; -1 for vertices no part row covers
	// Tri lists the triangles as indices into the per-vertex arrays.
	Tri [][3]int
	// TriOwner is the part of each triangle.
	TriOwner []int

	// Targets are the morph targets by name, in file order: deltas in yards
	// per vertex.
	TargetNames []string
	Targets     map[string][][3]float64

	skinNode int
	// Warnings collects what could not be matched while the model was read.
	Warnings []string
}

// NewModel decodes a built file. parts is the file's census of leaf shapes;
// aliases map a shape name to the game's shell name (see ShellName).
func NewModel(name string, doc *gltf.Document, parts []compiler.CensusPart, aliases map[string]string) (*Model, error) {
	m := &Model{Name: name, Doc: doc, Parts: parts, Targets: map[string][][3]float64{}, skinNode: -1}
	for i, n := range doc.Nodes {
		if n.Skin != nil && n.Mesh != nil {
			m.skinNode = i
			break
		}
	}
	if m.skinNode < 0 {
		return nil, fmt.Errorf("the file has no skinned mesh: an athlete is a skinned mesh")
	}
	for _, p := range parts {
		leaf := p.Name
		if i := strings.LastIndex(leaf, "/"); i >= 0 {
			leaf = leaf[i+1:]
		}
		m.Leaf = append(m.Leaf, leaf)
		m.Shell = append(m.Shell, ShellName(leaf, aliases))
	}
	mesh := doc.Meshes[*doc.Nodes[m.skinNode].Mesh]
	m.TargetNames = append(m.TargetNames, mesh.TargetNames...)
	for len(m.TargetNames) < targetCount(mesh) {
		m.TargetNames = append(m.TargetNames, fmt.Sprintf("target%d", len(m.TargetNames)))
	}
	for _, n := range m.TargetNames {
		m.Targets[n] = nil
	}
	const k = 1 / MetresPerYard
	for _, pr := range mesh.Primitives {
		base := len(m.Bind)
		nv := len(pr.Positions) / 3
		for v := 0; v < nv; v++ {
			m.Bind = append(m.Bind, [3]float64{float64(pr.Positions[3*v]) * k, float64(pr.Positions[3*v+1]) * k, float64(pr.Positions[3*v+2]) * k})
			var j [4]uint16
			var w [4]float32
			if len(pr.Joints0) >= 4*(v+1) {
				copy(j[:], pr.Joints0[4*v:4*v+4])
				copy(w[:], pr.Weights0[4*v:4*v+4])
			}
			m.Joint, m.Weight = append(m.Joint, j), append(m.Weight, w)
		}
		for ti, tg := range pr.Targets {
			name := m.TargetNames[ti]
			for v := 0; v < nv; v++ {
				var d [3]float64
				if len(tg.Positions) >= 3*(v+1) {
					d = [3]float64{float64(tg.Positions[3*v]) * k, float64(tg.Positions[3*v+1]) * k, float64(tg.Positions[3*v+2]) * k}
				}
				m.Targets[name] = append(m.Targets[name], d)
			}
		}
		for t := 0; t+2 < len(pr.Indices); t += 3 {
			m.Tri = append(m.Tri, [3]int{base + int(pr.Indices[t]), base + int(pr.Indices[t+1]), base + int(pr.Indices[t+2])})
		}
	}
	if err := m.assignParts(mesh); err != nil {
		return nil, err
	}
	return m, nil
}

func targetCount(m gltf.Mesh) int {
	n := 0
	for _, p := range m.Primitives {
		n = max(n, len(p.Targets))
	}
	return n
}

// assignParts hands the census rows to the file's vertices and triangles. The
// census lists the shapes in file order (compiler.CensusPart): primitive by
// primitive, and in each primitive in the order the shapes were written, so a
// running count of vertices and triangles cuts each primitive into shapes. A
// row of a baked mount ("mount/...") has no place in that order and is
// skipped; the vertices it owns stay unowned.
func (m *Model) assignParts(mesh gltf.Mesh) error {
	m.Owner = make([]int, len(m.Bind))
	for i := range m.Owner {
		m.Owner[i] = -1
	}
	m.TriOwner = make([]int, len(m.Tri))
	for i := range m.TriOwner {
		m.TriOwner[i] = -1
	}
	next := 0
	vbase, tbase := 0, 0
	for pi, pr := range mesh.Primitives {
		nv, nt := len(pr.Positions)/3, len(pr.Indices)/3
		v, t := 0, 0
		matName := ""
		if pr.Material != nil && *pr.Material < len(m.Doc.Materials) {
			matName = m.Doc.Materials[*pr.Material].Name
		}
		for v < nv || t < nt {
			for next < len(m.Parts) && strings.HasPrefix(m.Parts[next].Name, "mount/") {
				next++
			}
			if next >= len(m.Parts) {
				// Baked gear primitives come after the shapes; nothing in the
				// census describes them one by one.
				m.Warnings = append(m.Warnings, fmt.Sprintf("primitive %d: %d vertices and %d triangles are in no census part (baked gear)", pi, nv-v, nt-t))
				break
			}
			p := m.Parts[next]
			if v+p.Vertices > nv || t+p.Triangles > nt {
				return fmt.Errorf("census part %q (%d vertices, %d triangles) does not fit primitive %d (%d left, %d left): the build record and the file disagree", p.Name, p.Vertices, p.Triangles, pi, nv-v, nt-t)
			}
			if p.Material != matName {
				m.Warnings = append(m.Warnings, fmt.Sprintf("census part %q says material %q, the file's primitive %d has %q", p.Name, p.Material, pi, matName))
			}
			for i := 0; i < p.Vertices; i++ {
				m.Owner[vbase+v+i] = next
			}
			for i := 0; i < p.Triangles; i++ {
				m.TriOwner[tbase+t+i] = next
			}
			v += p.Vertices
			t += p.Triangles
			next++
		}
		vbase += nv
		tbase += nt
	}
	return nil
}

// VertexShell is the shell name of a vertex ("" when no shape owns it or no
// table knows the shape's name).
func (m *Model) VertexShell(i int) string {
	if o := m.Owner[i]; o >= 0 {
		return m.Shell[o]
	}
	return ""
}
