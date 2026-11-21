package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math"
	"sort"

	"m31labs.dev/greenware/gltf"
)

// Thresholds, in the file's units (yards for the game's bakes).
const (
	movedTol   = 1e-5
	normalTol  = 0.05
	uvTol      = 1e-5
	weightTol  = 1e-3
	keyTol     = 1e-4
	nodeTol    = 1e-5
	ibmTol     = 1e-5
	nearestMax = 0.05 // a nearest-position match farther than this is "no counterpart"
)

type FileInfo struct {
	Path      string `json:"path"`
	Bytes     int    `json:"bytes"`
	Vertices  int    `json:"vertices"`
	Triangles int    `json:"triangles"`
	Shells    int    `json:"shells"`
	ShellMode string `json:"shell_mode"`
	Nodes     int    `json:"nodes"`
	Joints    int    `json:"joints"`
	Clips     int    `json:"clips"`
	Targets   int    `json:"morph_targets"`
	Materials int    `json:"materials"`
	Images    int    `json:"images"`
}

type ShellRef struct {
	Name     string `json:"name"`
	Label    string `json:"dominant_joint,omitempty"`
	Vertices int    `json:"vertices"`
	Tris     int    `json:"triangles"`
}

type Moved struct {
	Count int        `json:"count"`
	Max   float64    `json:"max"`
	RMS   float64    `json:"rms"`
	Min   [3]float64 `json:"bbox_min"`
	Maxp  [3]float64 `json:"bbox_max"`
	Top   []int      `json:"top_vertices"` // global vertex indices in the before file
}

type CountMax struct {
	Count int     `json:"count"`
	Max   float64 `json:"max"`
}

type TriDiff struct {
	Compared   bool `json:"compared"`
	Reordered  int  `json:"index_list_differs"` // same position, different ordered triple
	OnlyBefore int  `json:"only_before"`        // triangles (as vertex sets) missing after
	OnlyAfter  int  `json:"only_after"`
}

type ShellDiff struct {
	Name      string   `json:"name"`
	NameAfter string   `json:"name_after,omitempty"`
	Label     string   `json:"dominant_joint,omitempty"`
	VertsA    int      `json:"vertices_before"`
	VertsB    int      `json:"vertices_after"`
	TrisA     int      `json:"triangles_before"`
	TrisB     int      `json:"triangles_after"`
	Resized   bool     `json:"resized"`
	Match     string   `json:"vertex_match"`
	NoMatch   int      `json:"vertices_without_counterpart,omitempty"`
	Moved     Moved    `json:"moved"`
	UV        CountMax `json:"uv_changed"`
	Normal    CountMax `json:"normal_changed"`
	JointSets int      `json:"joint_set_changed"`
	Weight    CountMax `json:"weight_changed"`
	Tris      TriDiff  `json:"triangles"`
}

func (d *ShellDiff) changed() bool {
	return d.Resized || d.Moved.Count > 0 || d.UV.Count > 0 || d.Normal.Count > 0 ||
		d.JointSets > 0 || d.Weight.Count > 0 || d.Tris.Reordered > 0 || d.Tris.OnlyBefore > 0 ||
		d.Tris.OnlyAfter > 0 || d.NoMatch > 0
}

type TargetDiff struct {
	Name      string   `json:"name"`
	MaxBefore float64  `json:"max_displacement_before"`
	MaxAfter  float64  `json:"max_displacement_after"`
	MaxChange float64  `json:"max_change"` // largest per-vertex change of the delta
	Vertices  int      `json:"vertices_changed"`
	Shells    []string `json:"shells"`
}

type TargetSection struct {
	Added   []string     `json:"added"`
	Removed []string     `json:"removed"`
	Changed []TargetDiff `json:"changed"`
	Same    int          `json:"unchanged"`
}

type ChannelDiff struct {
	Node       string  `json:"node"`
	Path       string  `json:"path"`
	KeysBefore int     `json:"keys_before"`
	KeysAfter  int     `json:"keys_after"`
	MaxDiff    float64 `json:"max_diff"`
}

type ClipDiff struct {
	Name            string        `json:"name"`
	KeysBefore      int           `json:"keys_before"`
	KeysAfter       int           `json:"keys_after"`
	DurBefore       float64       `json:"duration_before"`
	DurAfter        float64       `json:"duration_after"`
	ChannelsAdded   []string      `json:"channels_added,omitempty"`
	ChannelsRemoved []string      `json:"channels_removed,omitempty"`
	Changed         []ChannelDiff `json:"channels_changed,omitempty"`
	MaxDiff         float64       `json:"max_diff"`
}

type ClipSection struct {
	Added   []string   `json:"added"`
	Removed []string   `json:"removed"`
	Changed []ClipDiff `json:"changed"`
	Same    int        `json:"unchanged"`
}

type NodeDiff struct {
	Name   string  `json:"name"`
	What   string  `json:"what"`
	Amount float64 `json:"amount,omitempty"`
}

type JointSection struct {
	JointsBefore []string   `json:"joints_before"`
	JointsAfter  []string   `json:"joints_after"`
	JointsAdded  []string   `json:"joints_added"`
	JointsGone   []string   `json:"joints_removed"`
	OrderChanged bool       `json:"joint_order_changed"`
	NodesAdded   []string   `json:"nodes_added"`
	NodesRemoved []string   `json:"nodes_removed"`
	Diffs        []NodeDiff `json:"differences"`
}

type MaterialDiff struct {
	Name string   `json:"name"`
	What []string `json:"what"`
}

type AssetSection struct {
	BytesBefore int            `json:"file_bytes_before"`
	BytesAfter  int            `json:"file_bytes_after"`
	Materials   []MaterialDiff `json:"materials"`
	Images      []string       `json:"images"`
	Generator   string         `json:"generator,omitempty"`
}

type Report struct {
	Before FileInfo `json:"before"`
	After  FileInfo `json:"after"`
	Shells struct {
		Added      []ShellRef  `json:"added"`
		Removed    []ShellRef  `json:"removed"`
		Resized    []string    `json:"resized"`
		Matched    int         `json:"matched"`
		Unchanged  int         `json:"unchanged"`
		Pairing    string      `json:"pairing"`
		OutOfOrder int         `json:"paired_out_of_order"`
		Changed    []ShellDiff `json:"changed"`
	} `json:"shells"`
	Morph  TargetSection `json:"morph_targets"`
	Clips  ClipSection   `json:"clips"`
	Joints JointSection  `json:"joints_and_nodes"`
	Assets AssetSection  `json:"assets"`
}

func info(f *file) FileInfo {
	d := f.Doc
	fi := FileInfo{Path: f.Path, Bytes: f.Bytes, Vertices: len(f.P), Triangles: len(f.Tri), Shells: len(f.Shells),
		ShellMode: f.Mode, Nodes: len(d.Nodes), Clips: len(d.Animations), Targets: len(f.TargetOrder),
		Materials: len(d.Materials), Images: len(d.Images)}
	for _, s := range d.Skins {
		fi.Joints += len(s.Joints)
	}
	return fi
}

func ref(f *file, s *shell) ShellRef {
	return ShellRef{Name: s.Name, Label: s.Label, Vertices: len(s.Verts), Tris: len(s.Tris)}
}

func compare(a, b *file) *Report {
	r := &Report{Before: info(a), After: info(b)}
	pairs, addedIdx, removedIdx, how := pairShells(a, b)
	r.Shells.Pairing = how
	for _, i := range removedIdx {
		r.Shells.Removed = append(r.Shells.Removed, ref(a, &a.Shells[i]))
	}
	for _, i := range addedIdx {
		r.Shells.Added = append(r.Shells.Added, ref(b, &b.Shells[i]))
	}
	r.Shells.Matched = len(pairs)
	r.Shells.OutOfOrder = outOfOrder(pairs)
	match := make([]int, len(a.P)) // before vertex -> after vertex, -1 none
	for i := range match {
		match[i] = -1
	}
	shellOfA := make([]string, len(a.P))
	for _, p := range pairs {
		sa, sb := &a.Shells[p[0]], &b.Shells[p[1]]
		for _, v := range sa.Verts {
			shellOfA[v] = sa.Name
		}
		d := diffShell(a, b, sa, sb, match)
		if d.Resized {
			r.Shells.Resized = append(r.Shells.Resized, d.Name)
		}
		if d.changed() {
			r.Shells.Changed = append(r.Shells.Changed, d)
		} else {
			r.Shells.Unchanged++
		}
	}
	r.Morph = diffTargets(a, b, match, shellOfA)
	r.Clips = diffClips(a.Doc, b.Doc)
	r.Joints = diffJoints(a.Doc, b.Doc)
	r.Assets = diffAssets(a, b)
	return r
}

type bbox struct{ min, max [3]float64 }

func shellBox(f *file, s *shell) (c [3]float64) {
	if len(s.Verts) == 0 {
		return
	}
	mn := [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
	mx := [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for _, v := range s.Verts {
		for k := 0; k < 3; k++ {
			mn[k] = math.Min(mn[k], f.P[v][k])
			mx[k] = math.Max(mx[k], f.P[v][k])
		}
	}
	for k := 0; k < 3; k++ {
		c[k] = (mn[k] + mx[k]) / 2
	}
	return
}

func dist3(a, b [3]float64) float64 {
	return math.Sqrt((a[0]-b[0])*(a[0]-b[0]) + (a[1]-b[1])*(a[1]-b[1]) + (a[2]-b[2])*(a[2]-b[2]))
}

// pairShells pairs the shells of the two files. Files whose shells are
// connected pieces are paired in order: an alignment on (vertices, triangles,
// dominant joint), then the leftovers between two aligned shells by nearest
// bounding-box centre within 0.25. Otherwise shells pair by name.
func pairShells(a, b *file) (pairs [][2]int, added, removed []int, how string) {
	if !a.pieces || !b.pieces {
		bi := map[string]int{}
		for i, s := range b.Shells {
			bi[s.Name] = i
		}
		used := make([]bool, len(b.Shells))
		for i, s := range a.Shells {
			if j, ok := bi[s.Name]; ok {
				pairs = append(pairs, [2]int{i, j})
				used[j] = true
			} else {
				removed = append(removed, i)
			}
		}
		for j := range b.Shells {
			if !used[j] {
				added = append(added, j)
			}
		}
		return pairs, added, removed, "by shell name"
	}
	how = "by content, not order: shells with equal (vertices, triangles, dominant joint) pair by nearest bounding-box centre within 0.5, then the rest by nearest centre within 0.25"
	type cand struct {
		i, j int
		d    float64
	}
	ca := make([][3]float64, len(a.Shells))
	cb := make([][3]float64, len(b.Shells))
	for i := range a.Shells {
		ca[i] = shellBox(a, &a.Shells[i])
	}
	for j := range b.Shells {
		cb[j] = shellBox(b, &b.Shells[j])
	}
	ua, ub := make([]bool, len(a.Shells)), make([]bool, len(b.Shells))
	pass := func(ok func(i, j int) bool, limit float64) {
		var cs []cand
		for i := range a.Shells {
			if ua[i] {
				continue
			}
			for j := range b.Shells {
				if ub[j] || !ok(i, j) {
					continue
				}
				if d := dist3(ca[i], cb[j]); d <= limit {
					cs = append(cs, cand{i, j, d})
				}
			}
		}
		sort.SliceStable(cs, func(x, y int) bool { return cs[x].d < cs[y].d })
		for _, c := range cs {
			if !ua[c.i] && !ub[c.j] {
				ua[c.i], ub[c.j] = true, true
				pairs = append(pairs, [2]int{c.i, c.j})
			}
		}
	}
	pass(func(i, j int) bool {
		x, y := &a.Shells[i], &b.Shells[j]
		return len(x.Verts) == len(y.Verts) && len(x.Tris) == len(y.Tris) && x.Label == y.Label
	}, 0.5)
	pass(func(i, j int) bool { return true }, 0.25)
	for i := range a.Shells {
		if !ua[i] {
			removed = append(removed, i)
		}
	}
	for j := range b.Shells {
		if !ub[j] {
			added = append(added, j)
		}
	}
	sort.Slice(pairs, func(x, y int) bool { return pairs[x][0] < pairs[y][0] })
	return
}

// outOfOrder counts the pairs that are not on the longest run that keeps
// their order in both files.
func outOfOrder(pairs [][2]int) int {
	var tails []int
	for _, p := range pairs {
		k := sort.SearchInts(tails, p[1])
		if k == len(tails) {
			tails = append(tails, p[1])
		} else {
			tails[k] = p[1]
		}
	}
	return len(pairs) - len(tails)
}

func diffShell(a, b *file, sa, sb *shell, match []int) ShellDiff {
	d := ShellDiff{Name: sa.Name, Label: sa.Label, VertsA: len(sa.Verts), VertsB: len(sb.Verts),
		TrisA: len(sa.Tris), TrisB: len(sb.Tris)}
	if sb.Name != sa.Name {
		d.NameAfter = sb.Name
	}
	d.Resized = d.VertsA != d.VertsB || d.TrisA != d.TrisB
	byIndex := d.VertsA == d.VertsB
	local := make([]int, len(sa.Verts)) // A local -> B global
	if byIndex {
		d.Match = "index"
		copy(local, sb.Verts)
	} else {
		d.Match = "nearest position (vertex counts differ)"
		for i, v := range sa.Verts {
			best, bd := -1, math.Inf(1)
			for _, w := range sb.Verts {
				if dd := dist3(a.P[v], b.P[w]); dd < bd {
					best, bd = w, dd
				}
			}
			if bd > nearestMax {
				best = -1
				d.NoMatch++
			}
			local[i] = best
		}
	}
	type mv struct {
		v int
		d float64
	}
	var moved []mv
	var sum2 float64
	first := true
	for i, v := range sa.Verts {
		w := local[i]
		match[v] = w
		if w < 0 {
			continue
		}
		if dd := dist3(a.P[v], b.P[w]); dd > movedTol {
			moved = append(moved, mv{v, dd})
			sum2 += dd * dd
			d.Moved.Max = math.Max(d.Moved.Max, dd)
			for k := 0; k < 3; k++ {
				if first || b.P[w][k] < d.Moved.Min[k] {
					d.Moved.Min[k] = b.P[w][k]
				}
				if first || b.P[w][k] > d.Moved.Maxp[k] {
					d.Moved.Maxp[k] = b.P[w][k]
				}
			}
			first = false
		}
		if a.UV != nil && b.UV != nil {
			if dd := math.Max(math.Abs(a.UV[v][0]-b.UV[w][0]), math.Abs(a.UV[v][1]-b.UV[w][1])); dd > uvTol {
				d.UV.Count++
				d.UV.Max = math.Max(d.UV.Max, dd)
			}
		}
		if a.N != nil && b.N != nil {
			dd := 0.0
			for k := 0; k < 3; k++ {
				dd = math.Max(dd, math.Abs(a.N[v][k]-b.N[w][k]))
			}
			if dd > normalTol {
				d.Normal.Count++
				d.Normal.Max = math.Max(d.Normal.Max, dd)
			}
		}
		wa, wb := weightMap(a, v), weightMap(b, w)
		same := len(wa) == len(wb)
		for k := range wa {
			if _, ok := wb[k]; !ok {
				same = false
			}
		}
		if !same {
			d.JointSets++
		}
		wd := 0.0
		for k, x := range wa {
			wd = math.Max(wd, math.Abs(float64(x-wb[k])))
		}
		for k, x := range wb {
			if _, ok := wa[k]; !ok {
				wd = math.Max(wd, math.Abs(float64(x)))
			}
		}
		if wd > weightTol {
			d.Weight.Count++
			d.Weight.Max = math.Max(d.Weight.Max, wd)
		}
	}
	d.Moved.Count = len(moved)
	if len(moved) > 0 {
		d.Moved.RMS = math.Sqrt(sum2 / float64(len(moved)))
		sort.SliceStable(moved, func(x, y int) bool { return moved[x].d > moved[y].d })
		for i := 0; i < len(moved) && i < 5; i++ {
			d.Moved.Top = append(d.Moved.Top, moved[i].v)
		}
	}
	if byIndex {
		d.Tris = diffTris(a, b, sa, sb)
	}
	return d
}

func weightMap(f *file, v int) map[string]float32 {
	m := map[string]float32{}
	for k := 0; k < 4; k++ {
		if f.W[v][k] > 1e-6 && f.J[v][k] != "" {
			m[f.J[v][k]] += f.W[v][k]
		}
	}
	return m
}

func diffTris(a, b *file, sa, sb *shell) TriDiff {
	t := TriDiff{Compared: true}
	la := map[int]int{}
	for i, v := range sa.Verts {
		la[v] = i
	}
	lb := map[int]int{}
	for i, v := range sb.Verts {
		lb[v] = i
	}
	conv := func(f *file, l map[int]int, ti int) [3]int {
		x := f.Tri[ti]
		return [3]int{l[x[0]], l[x[1]], l[x[2]]}
	}
	canon := func(x [3]int) [3]int {
		s := x[:]
		sort.Ints(s)
		return [3]int{s[0], s[1], s[2]}
	}
	ms := map[[3]int]int{}
	for i, ti := range sa.Tris {
		x := conv(a, la, ti)
		ms[canon(x)]++
		if len(sa.Tris) == len(sb.Tris) && x != conv(b, lb, sb.Tris[i]) {
			t.Reordered++
		}
	}
	for _, ti := range sb.Tris {
		ms[canon(conv(b, lb, ti))]--
	}
	for _, c := range ms {
		if c > 0 {
			t.OnlyBefore += c
		} else {
			t.OnlyAfter -= c
		}
	}
	return t
}

func diffTargets(a, b *file, match []int, shellOfA []string) TargetSection {
	var s TargetSection
	for _, n := range a.TargetOrder {
		if _, ok := b.Targets[n]; !ok {
			s.Removed = append(s.Removed, n)
		}
	}
	for _, n := range b.TargetOrder {
		if _, ok := a.Targets[n]; !ok {
			s.Added = append(s.Added, n)
		}
	}
	for _, n := range a.TargetOrder {
		tb, ok := b.Targets[n]
		if !ok {
			continue
		}
		ta := a.Targets[n]
		td := TargetDiff{Name: n}
		shells := map[string]bool{}
		for v := range ta {
			td.MaxBefore = math.Max(td.MaxBefore, dist3(ta[v], [3]float64{}))
			w := match[v]
			if w < 0 {
				continue
			}
			if dd := dist3(ta[v], tb[w]); dd > movedTol {
				td.Vertices++
				td.MaxChange = math.Max(td.MaxChange, dd)
				shells[shellOfA[v]] = true
			}
		}
		for w := range tb {
			td.MaxAfter = math.Max(td.MaxAfter, dist3(tb[w], [3]float64{}))
		}
		for k := range shells {
			td.Shells = append(td.Shells, k)
		}
		sort.Strings(td.Shells)
		if td.Vertices > 0 || math.Abs(td.MaxBefore-td.MaxAfter) > movedTol {
			s.Changed = append(s.Changed, td)
		} else {
			s.Same++
		}
	}
	return s
}

type chan3 struct {
	in, out []float32
}

func clipChannels(d *gltf.Document, a gltf.Animation) (map[string]chan3, int, float64) {
	m := map[string]chan3{}
	keys := 0
	dur := 0.0
	for _, c := range a.Channels {
		nm := fmt.Sprintf("node%d", c.Node)
		if c.Node < len(d.Nodes) && d.Nodes[c.Node].Name != "" {
			nm = d.Nodes[c.Node].Name
		}
		s := a.Samplers[c.Sampler]
		m[nm+" "+c.Path] = chan3{s.Input, s.Output}
		keys += len(s.Input)
		if n := len(s.Input); n > 0 {
			dur = math.Max(dur, float64(s.Input[n-1]))
		}
	}
	return m, keys, dur
}

func diffClips(a, b *gltf.Document) ClipSection {
	var s ClipSection
	bi := map[string]int{}
	for i, c := range b.Animations {
		bi[c.Name] = i
	}
	ai := map[string]bool{}
	for _, c := range a.Animations {
		ai[c.Name] = true
		j, ok := bi[c.Name]
		if !ok {
			s.Removed = append(s.Removed, c.Name)
			continue
		}
		ma, ka, da := clipChannels(a, c)
		mb, kb, db := clipChannels(b, b.Animations[j])
		cd := ClipDiff{Name: c.Name, KeysBefore: ka, KeysAfter: kb, DurBefore: da, DurAfter: db}
		var names []string
		for n := range ma {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			x := ma[n]
			y, ok := mb[n]
			if !ok {
				cd.ChannelsRemoved = append(cd.ChannelsRemoved, n)
				continue
			}
			diff := 0.0
			if len(x.in) != len(y.in) || len(x.out) != len(y.out) {
				diff = math.Inf(1)
			} else {
				for i := range x.in {
					diff = math.Max(diff, math.Abs(float64(x.in[i]-y.in[i])))
				}
				for i := range x.out {
					diff = math.Max(diff, math.Abs(float64(x.out[i]-y.out[i])))
				}
			}
			if diff > keyTol || len(x.in) != len(y.in) {
				cdiff := diff
				if math.IsInf(cdiff, 1) {
					cdiff = -1 // JSON has no Inf; -1 means the key counts differ
				}
				cd.Changed = append(cd.Changed, ChannelDiff{Node: n[:len(n)-len(lastWord(n))-1], Path: lastWord(n), KeysBefore: len(x.in), KeysAfter: len(y.in), MaxDiff: cdiff})
				cd.MaxDiff = math.Max(cd.MaxDiff, cdiff)
			}
		}
		for n := range mb {
			if _, ok := ma[n]; !ok {
				cd.ChannelsAdded = append(cd.ChannelsAdded, n)
			}
		}
		sort.Strings(cd.ChannelsAdded)
		if len(cd.Changed) > 0 || len(cd.ChannelsAdded) > 0 || len(cd.ChannelsRemoved) > 0 || ka != kb {
			s.Changed = append(s.Changed, cd)
		} else {
			s.Same++
		}
	}
	for _, c := range b.Animations {
		if !ai[c.Name] {
			s.Added = append(s.Added, c.Name)
		}
	}
	return s
}

func lastWord(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ' ' {
			return s[i+1:]
		}
	}
	return s
}

type nodeInfo struct {
	parent string
	n      gltf.Node
}

func nodeMap(d *gltf.Document) (map[string]nodeInfo, []string) {
	par := make([]string, len(d.Nodes))
	for i, n := range d.Nodes {
		for _, c := range n.Children {
			if c < len(par) {
				par[c] = nameOf(d, i)
			}
		}
	}
	m := map[string]nodeInfo{}
	var order []string
	for i, n := range d.Nodes {
		nm := nameOf(d, i)
		if _, dup := m[nm]; dup {
			nm = fmt.Sprintf("%s#%d", nm, i)
		}
		m[nm] = nodeInfo{par[i], n}
		order = append(order, nm)
	}
	return m, order
}

func nameOf(d *gltf.Document, i int) string {
	if d.Nodes[i].Name != "" {
		return d.Nodes[i].Name
	}
	return fmt.Sprintf("node%d", i)
}

func maxDiff32(a, b []float32) float64 {
	m := 0.0
	for i := range a {
		m = math.Max(m, math.Abs(float64(a[i]-b[i])))
	}
	return m
}

func diffJoints(a, b *gltf.Document) JointSection {
	var s JointSection
	jn := func(d *gltf.Document) (names []string, ibm map[string][16]float32) {
		ibm = map[string][16]float32{}
		for _, sk := range d.Skins {
			for k, ni := range sk.Joints {
				nm := nameOf(d, ni)
				names = append(names, nm)
				if k < len(sk.InverseBindMatrices) {
					ibm[nm] = sk.InverseBindMatrices[k]
				} else {
					ibm[nm] = [16]float32{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
				}
			}
		}
		return
	}
	ja, ia := jn(a)
	jb, ib := jn(b)
	s.JointsBefore, s.JointsAfter = ja, jb
	sa, sb := map[string]bool{}, map[string]bool{}
	for _, n := range ja {
		sa[n] = true
	}
	for _, n := range jb {
		sb[n] = true
	}
	for _, n := range ja {
		if !sb[n] {
			s.JointsGone = append(s.JointsGone, n)
		}
	}
	for _, n := range jb {
		if !sa[n] {
			s.JointsAdded = append(s.JointsAdded, n)
		}
	}
	var ca, cb []string
	for _, n := range ja {
		if sb[n] {
			ca = append(ca, n)
		}
	}
	for _, n := range jb {
		if sa[n] {
			cb = append(cb, n)
		}
	}
	for i := range ca {
		if ca[i] != cb[i] {
			s.OrderChanged = true
			break
		}
	}
	for _, n := range ca {
		x, y := ia[n], ib[n]
		if dd := maxDiff32(x[:], y[:]); dd > ibmTol {
			s.Diffs = append(s.Diffs, NodeDiff{n, "inverse bind matrix", dd})
		}
	}
	na, oa := nodeMap(a)
	nb, ob := nodeMap(b)
	for _, n := range oa {
		if _, ok := nb[n]; !ok {
			s.NodesRemoved = append(s.NodesRemoved, n)
		}
	}
	for _, n := range ob {
		if _, ok := na[n]; !ok {
			s.NodesAdded = append(s.NodesAdded, n)
		}
	}
	for _, n := range oa {
		x, ok := na[n]
		y, ok2 := nb[n]
		if !ok || !ok2 {
			continue
		}
		if x.parent != y.parent {
			s.Diffs = append(s.Diffs, NodeDiff{n, fmt.Sprintf("parent %q -> %q", x.parent, y.parent), 0})
		}
		tx, ty := trs(x.n), trs(y.n)
		if dd := maxDiff32(tx[0:3], ty[0:3]); dd > nodeTol {
			s.Diffs = append(s.Diffs, NodeDiff{n, "bind translation", dd})
		}
		if dd := maxDiff32(tx[3:7], ty[3:7]); dd > nodeTol {
			s.Diffs = append(s.Diffs, NodeDiff{n, "bind rotation", dd})
		}
		if dd := maxDiff32(tx[7:10], ty[7:10]); dd > nodeTol {
			s.Diffs = append(s.Diffs, NodeDiff{n, "bind scale", dd})
		}
	}
	return s
}

func trs(n gltf.Node) []float32 {
	v := []float32{0, 0, 0, 0, 0, 0, 1, 1, 1, 1}
	if n.Translation != nil {
		copy(v[0:3], n.Translation[:])
	}
	if n.Rotation != nil {
		copy(v[3:7], n.Rotation[:])
	}
	if n.Scale != nil {
		copy(v[7:10], n.Scale[:])
	}
	return v
}

func diffAssets(a, b *file) AssetSection {
	s := AssetSection{BytesBefore: a.Bytes, BytesAfter: b.Bytes}
	if a.Doc.Asset.Generator != b.Doc.Asset.Generator {
		s.Generator = fmt.Sprintf("%q -> %q", a.Doc.Asset.Generator, b.Doc.Asset.Generator)
	}
	mb := map[string]gltf.Material{}
	for i, m := range b.Doc.Materials {
		mb[matName(m, i)] = m
	}
	seen := map[string]bool{}
	for i, m := range a.Doc.Materials {
		n := matName(m, i)
		seen[n] = true
		o, ok := mb[n]
		if !ok {
			s.Materials = append(s.Materials, MaterialDiff{n, []string{"removed"}})
			continue
		}
		var w []string
		if m.BaseColor != o.BaseColor {
			w = append(w, fmt.Sprintf("base colour %v -> %v", m.BaseColor, o.BaseColor))
		}
		if m.Metallic != o.Metallic {
			w = append(w, fmt.Sprintf("metallic %g -> %g", m.Metallic, o.Metallic))
		}
		if m.Roughness != o.Roughness {
			w = append(w, fmt.Sprintf("roughness %g -> %g", m.Roughness, o.Roughness))
		}
		if m.EmissiveFactor != o.EmissiveFactor {
			w = append(w, "emissive changed")
		}
		if m.AlphaMode != o.AlphaMode || m.AlphaCutoff != o.AlphaCutoff {
			w = append(w, fmt.Sprintf("alpha %q/%g -> %q/%g", m.AlphaMode, m.AlphaCutoff, o.AlphaMode, o.AlphaCutoff))
		}
		if m.DoubleSided != o.DoubleSided {
			w = append(w, "double-sided changed")
		}
		if (m.BaseColorTexture == nil) != (o.BaseColorTexture == nil) {
			w = append(w, "base colour texture added or removed")
		}
		if len(w) > 0 {
			s.Materials = append(s.Materials, MaterialDiff{n, w})
		}
	}
	for i, m := range b.Doc.Materials {
		if n := matName(m, i); !seen[n] {
			s.Materials = append(s.Materials, MaterialDiff{n, []string{"added"}})
		}
	}
	ia, ib := a.Doc.Images, b.Doc.Images
	if len(ia) != len(ib) {
		s.Images = append(s.Images, fmt.Sprintf("image count %d -> %d", len(ia), len(ib)))
	}
	for i := 0; i < len(ia) && i < len(ib); i++ {
		switch {
		case ia[i].URI != ib[i].URI:
			s.Images = append(s.Images, fmt.Sprintf("image %d uri changed", i))
		case !bytes.Equal(ia[i].Data, ib[i].Data):
			ha, hb := sha256.Sum256(ia[i].Data), sha256.Sum256(ib[i].Data)
			s.Images = append(s.Images, fmt.Sprintf("image %d bytes %d -> %d (sha256 %x -> %x)", i, len(ia[i].Data), len(ib[i].Data), ha[:6], hb[:6]))
		case ia[i].MimeType != ib[i].MimeType:
			s.Images = append(s.Images, fmt.Sprintf("image %d mime type changed", i))
		}
	}
	return s
}

func matName(m gltf.Material, i int) string {
	if m.Name != "" {
		return m.Name
	}
	return fmt.Sprintf("material%d", i)
}
