package measure

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"m31labs.dev/greenware/conformance/glb"
)

// Pose is the state of every node at one moment of one clip: local
// transforms, world matrices and morph weights. Build one with RestPose or
// PoseAt. A Pose belongs to the Model that made it.
type Pose struct {
	m     *Model
	nodes []glb.Node // local transform fields replaced by the sampled values
	world [][16]float64
	morph map[int][]float64 // animated morph weights by node
}

// Clip describes one animation.
type Clip struct {
	Index    int     `json:"index"`
	Name     string  `json:"name"`
	Duration float64 `json:"duration"` // latest key time over all channels, seconds
	Channels int     `json:"channels"`
	// Keys is the sum of the key counts of every channel's sampler.
	Keys int `json:"keys"`
	// UniqueTimes is the size of the sorted union of all key times.
	UniqueTimes int `json:"unique_times"`
}

// RestPose is the pose of the file as stored: every node at its own transform.
func (m *Model) RestPose() *Pose {
	p := &Pose{m: m, morph: map[int][]float64{}}
	p.nodes = append([]glb.Node(nil), m.f.Doc.Nodes...)
	p.computeWorld()
	return p
}

func (p *Pose) computeWorld() {
	m := p.m
	ids := make([]int, len(m.nodes))
	for i := range ids {
		ids[i] = i
	}
	sort.Slice(ids, func(a, b int) bool { return m.nodes[ids[a]].order < m.nodes[ids[b]].order })
	p.world = make([][16]float64, len(m.nodes))
	for _, i := range ids {
		l := localMatrix(p.nodes[i])
		if par := m.nodes[i].parent; par >= 0 {
			p.world[i] = mul(p.world[par], l)
		} else {
			p.world[i] = l
		}
	}
}

// World returns the world matrix (column-major) of a node.
func (p *Pose) World(node int) [16]float64 { return p.world[node] }

// Position returns the world position of a node's origin.
func (p *Pose) Position(node int) [3]float64 {
	w := p.world[node]
	return [3]float64{w[12], w[13], w[14]}
}

// TRS returns a node's local translation, rotation (x,y,z,w) and scale. A node
// stored as a matrix reports the stored default values; use World for those.
func (p *Pose) TRS(node int) (t [3]float64, r [4]float64, s [3]float64) {
	n := p.nodes[node]
	return n.Translation, n.Rotation, n.Scale
}

// Weights returns the morph weights in force for a node: animated values, else
// the node's own, else its mesh's, else nil.
func (p *Pose) Weights(node int) []float64 {
	if w, ok := p.morph[node]; ok {
		return w
	}
	n := p.m.f.Doc.Nodes[node]
	if n.Weights != nil {
		return n.Weights
	}
	if n.Mesh != nil && *n.Mesh < len(p.m.f.Doc.Meshes) {
		return p.m.f.Doc.Meshes[*n.Mesh].Weights
	}
	return nil
}

// ClipByName finds the first animation with exactly this name.
func (m *Model) ClipByName(name string) (int, bool) {
	for i, a := range m.f.Doc.Animations {
		if a.Name == name {
			return i, true
		}
	}
	return -1, false
}

// sampler data
type track struct {
	times  []float64
	vals   []float64
	ncomp  int // numbers per value
	interp string
}

func (m *Model) track(an glb.Animation, ch glb.Channel) (track, error) {
	if ch.Sampler < 0 || ch.Sampler >= len(an.Samplers) {
		return track{}, errors.New("sampler out of range")
	}
	s := an.Samplers[ch.Sampler]
	times, nc, ok := m.f.Floats(s.Input)
	if !ok || nc != 1 {
		return track{}, errors.New("sampler input is unreadable")
	}
	vals, onc, ok := m.f.Floats(s.Output)
	if !ok {
		return track{}, errors.New("sampler output is unreadable")
	}
	tr := track{times: times, vals: vals, ncomp: onc, interp: s.Interpolation}
	if ch.Path == "weights" {
		if len(times) == 0 {
			return track{}, errors.New("sampler input is empty")
		}
		mult := 1
		if s.Interpolation == "CUBICSPLINE" {
			mult = 3
		}
		if len(vals)%(len(times)*mult) != 0 {
			return track{}, errors.New("weights output does not divide into the keys")
		}
		tr.ncomp = len(vals) / (len(times) * mult)
	}
	mult := 1
	if tr.interp == "CUBICSPLINE" {
		mult = 3
	}
	if len(times) == 0 || len(vals) != len(times)*mult*tr.ncomp {
		return track{}, errors.New("sampler output length does not match its input")
	}
	return tr, nil
}

// Clips lists the animations with their durations and key counts.
func (m *Model) Clips() []Clip {
	var out []Clip
	for i, an := range m.f.Doc.Animations {
		c := Clip{Index: i, Name: an.Name, Channels: len(an.Channels)}
		union := map[float64]bool{}
		for _, ch := range an.Channels {
			tr, err := m.track(an, ch)
			if err != nil {
				continue
			}
			c.Keys += len(tr.times)
			c.Duration = math.Max(c.Duration, tr.times[len(tr.times)-1])
			for _, t := range tr.times {
				union[t] = true
			}
		}
		c.UniqueTimes = len(union)
		out = append(out, c)
	}
	return out
}

// KeyTimes returns the sorted union of the key times of a clip.
func (m *Model) KeyTimes(clip int) ([]float64, error) {
	if clip < 0 || clip >= len(m.f.Doc.Animations) {
		return nil, fmt.Errorf("clip %d is out of range", clip)
	}
	an := m.f.Doc.Animations[clip]
	seen := map[float64]bool{}
	var out []float64
	for _, ch := range an.Channels {
		tr, err := m.track(an, ch)
		if err != nil {
			return nil, err
		}
		for _, t := range tr.times {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	sort.Float64s(out)
	return out, nil
}

// PoseAt evaluates a clip at time t seconds. Channels not in the clip keep the
// node's stored value. Before the first key a channel holds its first value
// and after the last key it holds its last value; clips do not loop here.
// A negative clip index returns the rest pose.
func (m *Model) PoseAt(clip int, t float64) (*Pose, error) {
	p := m.RestPose()
	if clip < 0 {
		return p, nil
	}
	if clip >= len(m.f.Doc.Animations) {
		return nil, fmt.Errorf("clip %d is out of range", clip)
	}
	if math.IsNaN(t) || math.IsInf(t, 0) {
		return nil, errors.New("time must be finite")
	}
	an := m.f.Doc.Animations[clip]
	for ci, ch := range an.Channels {
		if ch.Node == nil || *ch.Node < 0 || *ch.Node >= len(p.nodes) {
			continue
		}
		tr, err := m.track(an, ch)
		if err != nil {
			return nil, fmt.Errorf("channel %d: %w", ci, err)
		}
		want := map[string]int{"translation": 3, "scale": 3, "rotation": 4}[ch.Path]
		if ch.Path == "weights" {
			want = tr.ncomp
		}
		if want == 0 || tr.ncomp != want {
			return nil, fmt.Errorf("channel %d: path %q needs %d numbers per value; the output has %d", ci, ch.Path, want, tr.ncomp)
		}
		v := tr.at(t, ch.Path == "rotation")
		n := &p.nodes[*ch.Node]
		switch ch.Path {
		case "translation":
			copy(n.Translation[:], v)
			n.Matrix = nil
		case "scale":
			copy(n.Scale[:], v)
			n.Matrix = nil
		case "rotation":
			copy(n.Rotation[:], v)
			n.Matrix = nil
		case "weights":
			p.morph[*ch.Node] = v
		}
	}
	p.computeWorld()
	return p, nil
}

// SampleTRS samples one clip at time t and returns the local transform of the
// named node. It is PoseAt followed by TRS.
func (m *Model) SampleTRS(clip int, t float64, node int) (tr [3]float64, rot [4]float64, sc [3]float64, err error) {
	p, err := m.PoseAt(clip, t)
	if err != nil {
		return
	}
	tr, rot, sc = p.TRS(node)
	return
}

// value returns key k's value (the middle element of a CUBICSPLINE triple).
func (tr track) value(k int) []float64 {
	if tr.interp == "CUBICSPLINE" {
		return tr.vals[(3*k+1)*tr.ncomp : (3*k+2)*tr.ncomp]
	}
	return tr.vals[k*tr.ncomp : (k+1)*tr.ncomp]
}

// at samples the track. rot selects quaternion handling.
func (tr track) at(t float64, rot bool) []float64 {
	n := len(tr.times)
	out := make([]float64, tr.ncomp)
	if t <= tr.times[0] {
		copy(out, tr.value(0))
		return out
	}
	if t >= tr.times[n-1] {
		copy(out, tr.value(n-1))
		return out
	}
	k := sort.Search(n, func(i int) bool { return tr.times[i] > t }) - 1 // times[k] <= t < times[k+1]
	t0, t1 := tr.times[k], tr.times[k+1]
	switch tr.interp {
	case "STEP":
		copy(out, tr.value(k))
	case "CUBICSPLINE":
		dt := t1 - t0
		s := (t - t0) / dt
		s2, s3 := s*s, s*s*s
		h00, h10, h01, h11 := 2*s3-3*s2+1, s3-2*s2+s, -2*s3+3*s2, s3-s2
		nc := tr.ncomp
		p0 := tr.vals[(3*k+1)*nc : (3*k+2)*nc]
		b0 := tr.vals[(3*k+2)*nc : (3*k+3)*nc] // out-tangent of key k
		a1 := tr.vals[(3*(k+1))*nc : (3*(k+1)+1)*nc]
		p1 := tr.vals[(3*(k+1)+1)*nc : (3*(k+1)+2)*nc]
		for i := range out {
			out[i] = h00*p0[i] + h10*dt*b0[i] + h01*p1[i] + h11*dt*a1[i]
		}
		if rot {
			normalize4(out)
		}
	default: // LINEAR
		s := (t - t0) / (t1 - t0)
		a, b := tr.value(k), tr.value(k+1)
		if rot {
			q := Slerp([4]float64(a), [4]float64(b), s)
			copy(out, q[:])
		} else {
			for i := range out {
				out[i] = a[i] + (b[i]-a[i])*s
			}
		}
	}
	return out
}

func normalize4(q []float64) {
	l := math.Sqrt(q[0]*q[0] + q[1]*q[1] + q[2]*q[2] + q[3]*q[3])
	if l > 0 {
		for i := range q {
			q[i] /= l
		}
	}
}

// Slerp interpolates between unit quaternions (x,y,z,w) along the shorter
// arc: when the dot product is negative, b is negated first. For nearly
// parallel inputs (dot above 0.9995) it uses a normalised linear blend.
func Slerp(a, b [4]float64, s float64) [4]float64 {
	dot := a[0]*b[0] + a[1]*b[1] + a[2]*b[2] + a[3]*b[3]
	if dot < 0 {
		for i := range b {
			b[i] = -b[i]
		}
		dot = -dot
	}
	var out [4]float64
	if dot > 0.9995 {
		for i := range out {
			out[i] = a[i] + (b[i]-a[i])*s
		}
		normalize4(out[:])
		return out
	}
	theta := math.Acos(math.Min(dot, 1))
	sin := math.Sin(theta)
	wa, wb := math.Sin((1-s)*theta)/sin, math.Sin(s*theta)/sin
	for i := range out {
		out[i] = wa*a[i] + wb*b[i]
	}
	return out
}

// QuatDistance compares two quaternions after sign alignment: it negates b
// when the dot product is negative, then returns the largest component
// difference. Two quaternions of one rotation are at distance 0.
func QuatDistance(a, b [4]float64) float64 {
	dot := a[0]*b[0] + a[1]*b[1] + a[2]*b[2] + a[3]*b[3]
	sign := 1.0
	if dot < 0 {
		sign = -1
	}
	d := 0.0
	for i := range a {
		d = math.Max(d, math.Abs(a[i]-sign*b[i]))
	}
	return d
}

// PosedPrim is one triangle-list primitive with every vertex placed in the
// world by a pose: the morph targets at the pose's weights, then either linear
// blend skinning (the mesh node's own transform is not applied) or the node's
// world matrix.
type PosedPrim struct {
	Node, Mesh, Prim int
	Skinned          bool
	Pos              [][3]float64 // world position of every vertex
	Used             []bool       // vertex is referenced by at least one triangle
}

// skinMatrices returns jointWorld * inverseBind for each joint of a skin.
func (m *Model) skinMatrices(p *Pose, skin int) ([][16]float64, error) {
	d := m.f.Doc
	if skin < 0 || skin >= len(d.Skins) {
		return nil, errors.New("skin out of range")
	}
	s := d.Skins[skin]
	var ibm []float64
	if s.InverseBindMatrices != nil {
		v, nc, ok := m.f.Floats(*s.InverseBindMatrices)
		if !ok || nc != 16 || len(v) != 16*len(s.Joints) {
			return nil, errors.New("inverse bind matrices are unreadable or the wrong length")
		}
		ibm = v
	}
	out := make([][16]float64, len(s.Joints))
	for i, j := range s.Joints {
		if j < 0 || j >= len(p.world) {
			return nil, errors.New("joint out of range")
		}
		inv := identity
		if ibm != nil {
			copy(inv[:], ibm[16*i:16*i+16])
		}
		out[i] = mul(p.world[j], inv)
	}
	return out, nil
}

func apply(mat [16]float64, v [3]float64) [3]float64 {
	var o [3]float64
	for r := 0; r < 3; r++ {
		o[r] = mat[r]*v[0] + mat[4+r]*v[1] + mat[8+r]*v[2] + mat[12+r]
	}
	return o
}

// morphed returns the stored positions plus the weighted POSITION deltas.
func (m *Model) morphed(pr glb.Primitive, weights []float64) ([][3]float64, error) {
	pos, nc, ok := m.f.Floats(pr.Attributes["POSITION"])
	if !ok || nc != 3 {
		return nil, errors.New("POSITION is unreadable")
	}
	pts := make([][3]float64, len(pos)/3)
	for i := range pts {
		pts[i] = [3]float64{pos[3*i], pos[3*i+1], pos[3*i+2]}
	}
	for ti, t := range pr.Targets {
		if ti >= len(weights) || weights[ti] == 0 {
			continue
		}
		ai, has := t["POSITION"]
		if !has {
			continue
		}
		dv, nc, ok := m.f.Floats(ai)
		if !ok || nc != 3 || len(dv) != len(pos) {
			return nil, errors.New("morph target POSITION is unreadable")
		}
		for i := range pts {
			for k := 0; k < 3; k++ {
				pts[i][k] += weights[ti] * dv[3*i+k]
			}
		}
	}
	return pts, nil
}

// vertexSkin holds the influences of every vertex, merged over all JOINTS_n sets.
type vertexSkin struct {
	joints  [][]int
	weights [][]float64
}

// influences reads every JOINTS_n/WEIGHTS_n pair of a primitive. Entries with
// weight 0 are dropped.
func (m *Model) influences(pr glb.Primitive, nv int) (*vertexSkin, error) {
	vs := &vertexSkin{joints: make([][]int, nv), weights: make([][]float64, nv)}
	for set := 0; set < 16; set++ {
		ja, hasJ := pr.Attributes[fmt.Sprintf("JOINTS_%d", set)]
		wa, hasW := pr.Attributes[fmt.Sprintf("WEIGHTS_%d", set)]
		if !hasJ && !hasW {
			if set == 0 {
				return nil, errors.New("the primitive has no JOINTS_0 and WEIGHTS_0")
			}
			continue
		}
		if hasJ != hasW {
			return nil, fmt.Errorf("JOINTS_%d and WEIGHTS_%d are not a pair", set, set)
		}
		jv, jn, ok1 := m.f.Floats(ja)
		wv, wn, ok2 := m.f.Floats(wa)
		if !ok1 || !ok2 || jn != 4 || wn != 4 || len(jv)/4 != nv || len(wv)/4 != nv {
			return nil, fmt.Errorf("JOINTS_%d or WEIGHTS_%d is unreadable or has the wrong count", set, set)
		}
		for v := 0; v < nv; v++ {
			for k := 0; k < 4; k++ {
				if w := wv[4*v+k]; w != 0 {
					vs.joints[v] = append(vs.joints[v], int(jv[4*v+k]))
					vs.weights[v] = append(vs.weights[v], w)
				}
			}
		}
	}
	return vs, nil
}

func (m *Model) posedPrim(p *Pose, ni, pi int) (PosedPrim, error) {
	d := m.f.Doc
	nd := d.Nodes[ni]
	pr := d.Meshes[*nd.Mesh].Primitives[pi]
	out := PosedPrim{Node: ni, Mesh: *nd.Mesh, Prim: pi}
	pts, err := m.morphed(pr, p.Weights(ni))
	if err != nil {
		return out, err
	}
	out.Pos = make([][3]float64, len(pts))
	if nd.Skin != nil {
		out.Skinned = true
		jm, err := m.skinMatrices(p, *nd.Skin)
		if err != nil {
			return out, err
		}
		vs, err := m.influences(pr, len(pts))
		if err != nil {
			return out, err
		}
		for v, pt := range pts {
			if len(vs.joints[v]) == 0 {
				out.Pos[v] = pt // zero total weight: not deformed (see NOTES)
				continue
			}
			var acc [3]float64
			for i, j := range vs.joints[v] {
				if j < 0 || j >= len(jm) {
					return out, fmt.Errorf("vertex %d uses joint %d; the skin has %d", v, j, len(jm))
				}
				q := apply(jm[j], pt)
				for k := 0; k < 3; k++ {
					acc[k] += vs.weights[v][i] * q[k]
				}
			}
			out.Pos[v] = acc
		}
	} else {
		for v, pt := range pts {
			out.Pos[v] = apply(p.world[ni], pt)
		}
	}
	out.Used = make([]bool, len(pts))
	if pr.Indices != nil {
		idx, ok := m.f.Indices(*pr.Indices)
		if !ok {
			return out, errors.New("indices are unreadable")
		}
		for k := 0; k+2 < len(idx); k += 3 {
			for _, i := range idx[k : k+3] {
				if int(i) >= len(pts) {
					return out, errors.New("index out of range")
				}
				out.Used[i] = true
			}
		}
	} else {
		for i := 0; i+2 < len(pts); i += 3 {
			out.Used[i], out.Used[i+1], out.Used[i+2] = true, true, true
		}
	}
	return out, nil
}

// Posed places the triangle-list primitives of a selection in the world.
func (m *Model) Posed(p *Pose, s Select) ([]PosedPrim, error) {
	ids, err := m.selectedNodes(s)
	if err != nil {
		return nil, err
	}
	d := m.f.Doc
	var out []PosedPrim
	for _, ni := range ids {
		nd := d.Nodes[ni]
		if nd.Mesh == nil {
			continue
		}
		for pi, pr := range d.Meshes[*nd.Mesh].Primitives {
			if pr.Mode != 4 {
				continue
			}
			pp, err := m.posedPrim(p, ni, pi)
			if err != nil {
				return nil, fmt.Errorf("node %d primitive %d: %w", ni, pi, err)
			}
			out = append(out, pp)
		}
	}
	return out, nil
}

// PosedBounds is the box of every drawn vertex of the selection in a pose. A
// vertex is drawn when a triangle of a primitive in the default scene uses it.
// ok is false when nothing is drawn.
func (m *Model) PosedBounds(p *Pose, s Select) (Box, bool, error) {
	prims, err := m.Posed(p, s)
	if err != nil {
		return Box{}, false, err
	}
	b := Box{Min: [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}, Max: [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}}
	any := false
	for _, pp := range prims {
		for v, pt := range pp.Pos {
			if !pp.Used[v] {
				continue
			}
			any = true
			for k := 0; k < 3; k++ {
				b.Min[k] = math.Min(b.Min[k], pt[k])
				b.Max[k] = math.Max(b.Max[k], pt[k])
			}
		}
	}
	return b, any, nil
}

// Lowest is the drawn vertex with the smallest world Y.
type Lowest struct {
	Y        float64    `json:"y"`
	Node     string     `json:"node"`
	Prim     int        `json:"primitive"`
	Vertex   int        `json:"vertex"`
	Position [3]float64 `json:"position"`
}

// LowestVertex finds the drawn vertex with the smallest world Y in a pose
// (ground contact). The first such vertex in file order wins a tie. ok is
// false when nothing is drawn.
func (m *Model) LowestVertex(p *Pose, s Select) (Lowest, bool, error) {
	prims, err := m.Posed(p, s)
	if err != nil {
		return Lowest{}, false, err
	}
	best := Lowest{Y: math.Inf(1)}
	found := false
	for _, pp := range prims {
		for v, pt := range pp.Pos {
			if pp.Used[v] && pt[1] < best.Y {
				best = Lowest{Y: pt[1], Node: m.nodes[pp.Node].name, Prim: pp.Prim, Vertex: v, Position: pt}
				found = true
			}
		}
	}
	return best, found, nil
}

// NodePosition is the world position of the first node with this name.
func (m *Model) NodePosition(p *Pose, name string) ([3]float64, error) {
	i, ok := m.NodeID(name)
	if !ok {
		return [3]float64{}, fmt.Errorf("no node named %q", name)
	}
	return p.Position(i), nil
}

// JointDistance is the straight-line distance between the world positions of
// two named nodes in a pose.
func (m *Model) JointDistance(p *Pose, a, b string) (float64, error) {
	pa, err := m.NodePosition(p, a)
	if err != nil {
		return 0, err
	}
	pb, err := m.NodePosition(p, b)
	if err != nil {
		return 0, err
	}
	return math.Sqrt(sq(pa[0]-pb[0]) + sq(pa[1]-pb[1]) + sq(pa[2]-pb[2])), nil
}

func sq(x float64) float64 { return x * x }

// BindDeviation is the worst difference between the skinned and the stored
// positions of one skinned primitive at the rest pose.
type BindDeviation struct {
	Node   string  `json:"node"`
	Prim   int     `json:"primitive"`
	Vertex int     `json:"vertex"`
	Max    float64 `json:"max"`
}

// BindPoseDeviation skins every skinned primitive at the rest pose (each node
// at its stored transform, morph weights at their defaults) and compares each
// vertex with its stored position moved by the same default morph weights. A
// consistent rig reproduces the bind positions; a difference means the inverse
// bind matrices do not match the stored joint transforms.
func (m *Model) BindPoseDeviation() ([]BindDeviation, error) {
	p := m.RestPose()
	d := m.f.Doc
	var out []BindDeviation
	for ni, nd := range d.Nodes {
		if nd.Mesh == nil || nd.Skin == nil {
			continue
		}
		for pi, pr := range d.Meshes[*nd.Mesh].Primitives {
			if pr.Mode != 4 {
				continue
			}
			pp, err := m.posedPrim(p, ni, pi)
			if err != nil {
				return nil, fmt.Errorf("node %d primitive %d: %w", ni, pi, err)
			}
			ref, err := m.morphed(pr, p.Weights(ni))
			if err != nil {
				return nil, err
			}
			dev := BindDeviation{Node: m.nodes[ni].name, Prim: pi}
			for v := range ref {
				for k := 0; k < 3; k++ {
					if e := math.Abs(pp.Pos[v][k] - ref[v][k]); e > dev.Max || math.IsNaN(e) {
						dev.Max, dev.Vertex = e, v
					}
				}
			}
			out = append(out, dev)
		}
	}
	return out, nil
}

// WeightStats summarises the skin weights of one skinned primitive.
type WeightStats struct {
	Node     string `json:"node"`
	Prim     int    `json:"primitive"`
	Vertices int    `json:"vertices"`
	// MaxInfluences is the largest number of joints with a nonzero weight on one
	// vertex, counted over all JOINTS_n sets.
	MaxInfluences int `json:"max_influences"`
	// OverFour counts vertices with more than four nonzero influences.
	OverFour int `json:"over_four"`
	// ZeroSum counts vertices whose weights are all zero.
	ZeroSum int `json:"zero_sum"`
	// MaxSumError is the largest |sum - 1| over vertices.
	MaxSumError float64 `json:"max_sum_error"`
	// Negative counts weights below zero.
	Negative int `json:"negative"`
}

// WeightStats reports per-primitive weight statistics for every primitive of a
// node that has a skin.
func (m *Model) WeightStats() ([]WeightStats, error) {
	d := m.f.Doc
	var out []WeightStats
	for ni, nd := range d.Nodes {
		if nd.Mesh == nil || nd.Skin == nil {
			continue
		}
		for pi, pr := range d.Meshes[*nd.Mesh].Primitives {
			pos, nc, ok := m.f.Floats(pr.Attributes["POSITION"])
			if !ok || nc != 3 {
				return nil, fmt.Errorf("node %d primitive %d: POSITION is unreadable", ni, pi)
			}
			nv := len(pos) / 3
			vs, err := m.influences(pr, nv)
			if err != nil {
				return nil, fmt.Errorf("node %d primitive %d: %w", ni, pi, err)
			}
			st := WeightStats{Node: m.nodes[ni].name, Prim: pi, Vertices: nv}
			for v := 0; v < nv; v++ {
				sum := 0.0
				per := map[int]float64{}
				for i, j := range vs.joints[v] {
					w := vs.weights[v][i]
					sum += w
					if w < 0 {
						st.Negative++
					}
					per[j] += w
				}
				n := 0
				for _, w := range per {
					if w != 0 {
						n++
					}
				}
				if n > st.MaxInfluences {
					st.MaxInfluences = n
				}
				if n > 4 {
					st.OverFour++
				}
				if sum == 0 {
					st.ZeroSum++
				}
				if e := math.Abs(sum - 1); e > st.MaxSumError {
					st.MaxSumError = e
				}
			}
			out = append(out, st)
		}
	}
	return out, nil
}

// SkinInfo describes one skin.
type SkinInfo struct {
	Index      int      `json:"index"`
	Joints     int      `json:"joints"`
	JointNames []string `json:"joint_names"`
	HasInverse bool     `json:"has_inverse_bind_matrices"`
}

// MorphInfo describes the morph targets of one mesh.
type MorphInfo struct {
	Mesh  int      `json:"mesh"`
	Name  string   `json:"name"`
	Count int      `json:"count"`
	Names []string `json:"target_names"`
	// Sparse is true when any target accessor of the mesh is stored sparse.
	Sparse bool `json:"sparse"`
	// Vertices is the vertex count summed over the mesh's primitives.
	Vertices int `json:"vertices"`
	// Targets has one entry per morph target, in order.
	Targets []MorphTarget `json:"targets"`
}

// MorphTarget describes one morph target of a mesh.
type MorphTarget struct {
	Index int    `json:"index"`
	Name  string `json:"name,omitempty"`
	// Sparse is true when the target's POSITION accessor (or, if it has none,
	// any attribute accessor) is stored sparse in any primitive.
	Sparse bool `json:"sparse"`
	// MovedVertices counts vertices whose POSITION delta is nonzero in any
	// component, summed over the mesh's primitives. A reviewer compares it
	// with Vertices: glTF advises sparse storage when fewer than about half
	// the vertices move.
	MovedVertices int `json:"moved_vertices"`
}

// morphTargets summarises the targets of mesh mi.
func (m *Model) morphTargets(mi int) (targets []MorphTarget, vertices int, sparse bool) {
	d := m.f.Doc
	mesh := d.Meshes[mi]
	names, _ := d.TargetNames(mi)
	n := len(mesh.Primitives[0].Targets)
	targets = make([]MorphTarget, n)
	for ti := range targets {
		targets[ti].Index = ti
		if ti < len(names) {
			targets[ti].Name = names[ti]
		}
	}
	for _, pr := range mesh.Primitives {
		if pa, ok := pr.Attributes["POSITION"]; ok && pa < len(d.Accessors) {
			vertices += d.Accessors[pa].Count
		}
		for ti := 0; ti < n && ti < len(pr.Targets); ti++ {
			for name, ai := range pr.Targets[ti] {
				if ai < 0 || ai >= len(d.Accessors) {
					continue
				}
				if d.Accessors[ai].Sparse != nil && (name == "POSITION" || !targets[ti].Sparse) {
					targets[ti].Sparse = true
				}
				if name != "POSITION" {
					continue
				}
				dv, nc, ok := m.f.Floats(ai)
				if !ok || nc != 3 {
					continue
				}
				for e := 0; e+2 < len(dv); e += 3 {
					if dv[e] != 0 || dv[e+1] != 0 || dv[e+2] != 0 {
						targets[ti].MovedVertices++
					}
				}
			}
		}
	}
	for _, t := range targets {
		sparse = sparse || t.Sparse
	}
	return targets, vertices, sparse
}

// Rig is the summary the command prints in the report's rig section.
type Rig struct {
	JointCount int           `json:"joint_count"`
	Skins      []SkinInfo    `json:"skins"`
	Animations []Clip        `json:"animations"`
	Morphs     []MorphInfo   `json:"morph_targets"`
	Weights    []WeightStats `json:"weights"`
	// BindPoseMaxError is the largest bind-pose deviation over all skinned primitives.
	BindPoseMaxError float64 `json:"bind_pose_max_error"`
}

// Rig summarises skins, clips, morph targets, weights and bind-pose fit.
func (m *Model) Rig() (*Rig, error) {
	d := m.f.Doc
	r := &Rig{Skins: []SkinInfo{}, Animations: []Clip{}, Morphs: []MorphInfo{}, Weights: []WeightStats{}}
	uniq := map[int]bool{}
	for i, s := range d.Skins {
		si := SkinInfo{Index: i, Joints: len(s.Joints), JointNames: []string{}, HasInverse: s.InverseBindMatrices != nil}
		for _, j := range s.Joints {
			uniq[j] = true
			if j >= 0 && j < len(d.Nodes) {
				si.JointNames = append(si.JointNames, d.Nodes[j].Name)
			}
		}
		r.Skins = append(r.Skins, si)
	}
	r.JointCount = len(uniq)
	if c := m.Clips(); c != nil {
		r.Animations = c
	}
	for i, mesh := range d.Meshes {
		if len(mesh.Primitives) == 0 || len(mesh.Primitives[0].Targets) == 0 {
			continue
		}
		names, _ := d.TargetNames(i)
		if names == nil {
			names = []string{}
		}
		targets, vertices, sparse := m.morphTargets(i)
		r.Morphs = append(r.Morphs, MorphInfo{Mesh: i, Name: mesh.Name, Count: len(mesh.Primitives[0].Targets), Names: names,
			Sparse: sparse, Vertices: vertices, Targets: targets})
	}
	ws, err := m.WeightStats()
	if err != nil {
		return nil, err
	}
	if ws != nil {
		r.Weights = ws
	}
	devs, err := m.BindPoseDeviation()
	if err != nil {
		return nil, err
	}
	for _, dv := range devs {
		r.BindPoseMaxError = math.Max(r.BindPoseMaxError, dv.Max)
	}
	return r, nil
}

// JointNodes lists the node indices of every joint of every skin, without repeats.
func (m *Model) JointNodes() []int {
	seen := map[int]bool{}
	var out []int
	for _, s := range m.f.Doc.Skins {
		for _, j := range s.Joints {
			if j >= 0 && j < len(m.nodes) && !seen[j] {
				seen[j] = true
				out = append(out, j)
			}
		}
	}
	return out
}

// NodeName returns a node's name.
func (m *Model) NodeName(i int) string { return m.nodes[i].name }
