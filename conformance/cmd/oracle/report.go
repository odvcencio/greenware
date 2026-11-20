package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"m31labs.dev/greenware/conformance/glb"
	"m31labs.dev/greenware/conformance/measure"
)

type gapSpec struct {
	node, axis string
	at         [3]float64
}

type options struct {
	strict bool
	nodes  []string
	gaps   []gapSpec
	dir    string
}

func parseGap(s string) (gapSpec, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return gapSpec{}, fmt.Errorf("--gap %q: want NODE:AXIS:X,Y,Z", s)
	}
	g := gapSpec{node: parts[0], axis: parts[1]}
	xyz := strings.Split(parts[2], ",")
	if len(xyz) != 3 {
		return gapSpec{}, fmt.Errorf("--gap %q: the point needs three numbers", s)
	}
	for i, v := range xyz {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return gapSpec{}, fmt.Errorf("--gap %q: %v", s, err)
		}
		g.at[i] = f
	}
	return g, nil
}

// Report is the JSON document the command prints.
type Report struct {
	Validator    string        `json:"validator"`
	File         string        `json:"file"`
	Bytes        int           `json:"bytes"`
	SHA256       string        `json:"sha256"`
	Counts       Counts        `json:"counts"`
	Findings     []glb.Finding `json:"findings"`
	Measurements *Measurements `json:"measurements"`
	Skipped      string        `json:"measurements_skipped,omitempty"`
}

// Counts of findings by severity.
type Counts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
}

// Measurements are the geometry results.
type Measurements struct {
	Scene          int                `json:"scene"`
	DrawCalls      int                `json:"draw_calls"`
	DrawnTriangles int                `json:"drawn_triangles"`
	Bounds         *measure.Box       `json:"bounds"`
	Whole          *Shell             `json:"scene_shell"`
	Nodes          []NodeReport       `json:"nodes"`
	Selected       []NamedShell       `json:"selected_subtrees,omitempty"`
	Sockets        []SocketReport     `json:"sockets"`
	Colliders      []measure.Collider `json:"colliders"`
	Gaps           []GapReport        `json:"gaps,omitempty"`
}

// Shell is closure and connectedness of a selection.
type Shell struct {
	DrawnTriangles int                     `json:"drawn_triangles"`
	Closed         measure.ClosedResult    `json:"closed"`
	Connected      measure.ConnectedResult `json:"connected"`
}

// NamedShell is a Shell for a named subtree.
type NamedShell struct {
	Node string `json:"node"`
	Shell
}

// NodeReport is one node with a mesh, measured on its own primitives.
type NodeReport struct {
	Name           string `json:"name"`
	InDefaultScene bool   `json:"in_default_scene"`
	DrawCalls      int    `json:"draw_calls"`
	Shell
}

// SocketReport is a socket's world position and axes.
type SocketReport struct {
	Name     string        `json:"name"`
	Position [3]float64    `json:"position"`
	Axes     [3][3]float64 `json:"axes"`
	HasMesh  bool          `json:"has_mesh"`
}

// GapReport is one requested gap.
type GapReport struct {
	Node   string            `json:"node"`
	Axis   string            `json:"axis"`
	At     [3]float64        `json:"at"`
	Result measure.GapResult `json:"result"`
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func shellOf(m *measure.Model, sel measure.Select) Shell {
	n, _ := m.DrawnTriangles(sel)
	c, _ := m.Closed(sel)
	k, _ := m.Connected(sel)
	return Shell{n, c, k}
}

func check(path string, data []byte, o options) *Report {
	sum := sha256.Sum256(data)
	rep := &Report{Validator: glb.Label, File: path, Bytes: len(data), SHA256: hex.EncodeToString(sum[:])}
	f, fs := glb.Decode(data, glb.Options{Resolve: resolver(o.dir)})
	rep.Findings = fs
	if rep.Findings == nil {
		rep.Findings = []glb.Finding{}
	}
	for _, x := range fs {
		if x.Severity == glb.Error {
			rep.Counts.Errors++
		} else {
			rep.Counts.Warnings++
		}
	}
	if rep.Counts.Errors > 0 {
		rep.Skipped = "the file has error findings; geometry is not measured from an invalid file"
		return rep
	}
	m, err := measure.Load(f)
	if err != nil {
		rep.Skipped = err.Error()
		return rep
	}
	ms := &Measurements{Nodes: []NodeReport{}, Sockets: []SocketReport{}, Colliders: m.Colliders()}
	if ms.Colliders == nil {
		ms.Colliders = []measure.Collider{}
	}
	ms.DrawCalls, _ = m.DrawCalls(measure.Whole)
	ms.DrawnTriangles, _ = m.DrawnTriangles(measure.Whole)
	if b, ok, _ := m.Bounds(measure.Whole); ok {
		ms.Bounds = &b
	}
	w := shellOf(m, measure.Whole)
	ms.Whole = &w
	for _, name := range m.NodeNames() {
		own := measure.OwnOf(name)
		id, _ := m.NodeID(name)
		if f.Doc.Nodes[id].Mesh == nil {
			continue
		}
		dc, _ := m.DrawCalls(own)
		ms.Nodes = append(ms.Nodes, NodeReport{name, m.InDefaultScene(name), dc, shellOf(m, own)})
	}
	for _, name := range o.nodes {
		if !m.HasNode(name) {
			rep.Findings = append(rep.Findings, glb.Finding{Severity: glb.Error, Code: "ORACLE-NO-NODE", Path: "--node", Message: fmt.Sprintf("no node named %q", name)})
			rep.Counts.Errors++
			continue
		}
		ms.Selected = append(ms.Selected, NamedShell{name, shellOf(m, measure.Sub(name))})
	}
	for _, s := range m.Sockets() {
		ms.Sockets = append(ms.Sockets, SocketReport{s.Name, s.Position, s.Axes, s.HasMesh})
	}
	for _, g := range o.gaps {
		sel := measure.Whole
		if g.node != "" {
			sel = measure.Sub(g.node)
		}
		r, err := m.Gap(sel, g.axis, g.at)
		if err != nil {
			rep.Findings = append(rep.Findings, glb.Finding{Severity: glb.Error, Code: "ORACLE-GAP", Path: "--gap", Message: err.Error()})
			rep.Counts.Errors++
			continue
		}
		ms.Gaps = append(ms.Gaps, GapReport{g.node, g.axis, g.at, r})
	}
	rep.Measurements = ms
	return rep
}

// resolver loads external buffers from the GLB's own directory only. It refuses
// absolute paths, parent references and URI schemes.
func resolver(dir string) func(string) ([]byte, error) {
	return func(uri string) ([]byte, error) {
		name, err := url.PathUnescape(uri)
		if err != nil {
			return nil, err
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		fh, err := root.Open(name) // rejects absolute paths and ".." escapes
		if err != nil {
			return nil, err
		}
		defer fh.Close()
		return io.ReadAll(io.LimitReader(fh, 1<<28))
	}
}
