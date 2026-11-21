// Command skincompare compares the skin weights of a Greenware athlete GLB with
// a per-vertex dump of the game's Go bake (bakeathletes -dump-skin, a throwaway
// flag in the parity clone).
//
//	skincompare -go go-skin.json -glb athlete.glb -record athlete.build.json
//
// Vertices are matched by position (the two meshes share the ring tables) among
// the Go vertices of the same shell name; a Greenware vertex takes the nearest
// unused candidate within -tol yards.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/gltf"
	ap "m31labs.dev/greenware/internal/athleteparity"
)

type goVertex struct {
	P [3]float64
	J []string
	W []float64
}
type goShell struct {
	Name       string
	First, End int
	Joint      string
}
type goDump struct {
	Joints []string
	Shells []goShell
	V      []goVertex
}

func main() {
	gp := flag.String("go", "", "go-skin.json")
	glbp := flag.String("glb", "", "Greenware athlete.glb")
	rec := flag.String("record", "", "athlete.build.json")
	tol := flag.Float64("tol", 1e-4, "position tolerance in yards")
	loose := flag.Float64("loose", 0.01, "second pass for the vertices left over: tolerance in yards (0 for none)")
	goglb := flag.String("goglb", "", "the Go bake's athlete.glb: also compare UVs, normals, morph targets and triangles")
	mapOut := flag.String("map", "", "write the Go-vertex to Greenware-vertex map (JSON list; -1 for none) to this file")
	worst := flag.Int("worst", 20, "worst vertices to list")
	flag.Parse()
	var gd goDump
	b, err := os.ReadFile(*gp)
	check(err)
	check(json.Unmarshal(b, &gd))
	glb, err := os.ReadFile(*glbp)
	check(err)
	doc, err := gltf.Decode(glb)
	check(err)
	rb, err := os.ReadFile(*rec)
	check(err)
	var br struct {
		Census []struct {
			Output string
			Level  int
			Parts  []compiler.CensusPart
		}
	}
	check(json.Unmarshal(rb, &br))
	m, err := ap.NewModel("athlete.glb", doc, br.Census[0].Parts, nil)
	check(err)
	var jn []string
	for _, ni := range doc.Skins[0].Joints {
		jn = append(jn, doc.Nodes[ni].Name)
	}
	fmt.Printf("greenware joints: %v\n", jn)
	fmt.Printf("go joints:        %v\n", gd.Joints)

	// index Go vertices by shell name and a position grid.
	shellOf := make([]string, len(gd.V))
	shellIdx := make([]int, len(gd.V))
	for si, s := range gd.Shells {
		for i := s.First; i < s.End; i++ {
			shellOf[i] = s.Name
			shellIdx[i] = si
		}
	}
	type key [3]int64
	cell := math.Max(1e-3, *loose)
	grid := map[key][]int{}
	kf := func(p [3]float64) key {
		return key{int64(math.Floor(p[0] / cell)), int64(math.Floor(p[1] / cell)), int64(math.Floor(p[2] / cell))}
	}
	for i, v := range gd.V {
		k := kf(v.P)
		grid[k] = append(grid[k], i)
	}
	// With -goglb the pairing also uses texture coordinates, so that vertices at
	// one position (seam columns, pole rings) pair with the vertex of the same
	// texture place.
	var goUV []float32
	var gwUV []float32
	if *goglb != "" {
		b, err := os.ReadFile(*goglb)
		check(err)
		d, err := gltf.Decode(b)
		check(err)
		for _, n := range d.Nodes {
			if n.Mesh != nil && n.Skin != nil {
				goUV = d.Meshes[*n.Mesh].Primitives[0].UV0
			}
		}
		for _, n := range m.Doc.Nodes {
			if n.Mesh != nil && n.Skin != nil {
				gwUV = m.Doc.Meshes[*n.Mesh].Primitives[0].UV0
			}
		}
	}
	// Shells that repeat their first column as a last one (the game's seamed
	// stacks) have a vertex with u = 1; elsewhere u wraps.
	seamShell := make([]bool, len(gd.Shells))
	if goUV != nil {
		for si, sh := range gd.Shells {
			for i := sh.First; i < sh.End; i++ {
				if _, o1, _ := goCell(float64(goUV[2*i]), float64(goUV[2*i+1])); o1 > 0.999 {
					seamShell[si] = true
					break
				}
			}
		}
	}
	used := make([]bool, len(gd.V))
	direct := os.Getenv("SC_DIRECT") != ""
	type res struct {
		gw, gov int
		shell   string
		goShell string
		dpos    float64
		dw      float64 // max abs diff over all joints
		jd      string  // joint with the max diff
		sameJ   bool
	}
	var rs []res
	unmatched := 0
	loosely := 0
	var unmatchedShells = map[string]int{}
	maxPos := 0.0
	for i := range m.Bind {
		p := m.Bind[i]
		k := kf(p)
		best, bd, bestScore := -1, math.Inf(1), math.Inf(1)
		want := m.VertexShell(i)
		var ec int
		var eu, ev float64
		if goUV != nil {
			ec, eu, ev = expectedGoCell(m, i, gwUV)
		}
		for dx := int64(-1); dx <= 1; dx++ {
			for dy := int64(-1); dy <= 1; dy++ {
				for dz := int64(-1); dz <= 1; dz++ {
					for _, c := range grid[key{k[0] + dx, k[1] + dy, k[2] + dz}] {
						if used[c] || shellOf[c] != want {
							continue
						}
						d := math.Sqrt(sq(gd.V[c].P[0]-p[0]) + sq(gd.V[c].P[1]-p[1]) + sq(gd.V[c].P[2]-p[2]))
						score := d
						if goUV != nil && direct {
							score = d/5e-3 + (math.Abs(float64(gwUV[2*i])-float64(goUV[2*c]))+math.Abs(float64(gwUV[2*i+1])-float64(goUV[2*c+1])))/0.08
						} else if goUV != nil {
							oc, o1, o2 := goCell(float64(goUV[2*c]), float64(goUV[2*c+1]))
							uvd := 1.0
							if oc == ec {
								du := math.Abs(eu - o1)
								if !seamShell[shellIdx[c]] {
									du = math.Min(du, math.Abs(1-du)) // no repeated column: u wraps
								}
								uvd = du + math.Abs(ev-o2)
							}
							score = d/5e-3 + uvd/0.08
						}
						if score < bestScore-1e-9 || (score < bestScore+1e-9 && c < best) {
							best, bd, bestScore = c, d, score
						}
					}
				}
			}
		}
		if best < 0 || bd > *loose {
			if len(unmatchedShells) < 3 || unmatchedShells[want] < 2 {
				nd, ni := math.Inf(1), -1
				for c := range gd.V {
					if shellOf[c] == want {
						d := math.Sqrt(sq(gd.V[c].P[0]-p[0]) + sq(gd.V[c].P[1]-p[1]) + sq(gd.V[c].P[2]-p[2]))
						if d < nd {
							nd, ni = d, c
						}
					}
				}
				fmt.Printf("unmatched gw#%d shell %s at (%.5f %.5f %.5f): nearest same-name go vertex #%d is %.5f yd away\n", i, want, p[0], p[1], p[2], ni, nd)
			}
			unmatched++
			fmt.Printf("  unmatched gw#%d skin: %s | nearest go skin: %s\n", i, fmtGW(m, jn, i), nearestSkin(gd, shellOf, want, p))
			unmatchedShells[want]++
			continue
		}
		used[best] = true
		maxPos = math.Max(maxPos, bd)
		if bd > *tol {
			loosely++
		}
		gw := map[string]float64{}
		for j := 0; j < 4; j++ {
			if w := float64(m.Weight[i][j]); w > 0 {
				gw[jn[m.Joint[i][j]]] += w
			}
		}
		gov := map[string]float64{}
		for j, n := range gd.V[best].J {
			gov[n] += gd.V[best].W[j]
		}
		r := res{gw: i, gov: best, shell: want, goShell: shellOf[best], dpos: bd, sameJ: true}
		all := map[string]bool{}
		for n := range gw {
			all[n] = true
		}
		for n := range gov {
			all[n] = true
		}
		for n := range all {
			d := math.Abs(gw[n] - gov[n])
			if d > r.dw {
				r.dw, r.jd = d, n
			}
			if (gw[n] > 0.005) != (gov[n] > 0.005) {
				r.sameJ = false
			}
		}
		rs = append(rs, r)
	}
	fmt.Printf("greenware vertices %d, go vertices %d\n", len(m.Bind), len(gd.V))
	fmt.Printf("matched %d, unmatched greenware vertices %d, unused go vertices %d; max position error of matches %.2e yd; %d matches farther than %.0e yd\n", len(rs), unmatched, countFalse(used), maxPos, loosely, *tol)
	if unmatched > 0 {
		fmt.Printf("unmatched by greenware shell: %v\n", unmatchedShells)
	}
	// per joint max diff
	perJoint := map[string]float64{}
	diffJ := 0
	hist := make([]int, 7) // 0, <=.001, <=.005, <=.01, <=.02, <=.05, >.05
	for _, r := range rs {
		if r.dw > perJoint[r.jd] {
			perJoint[r.jd] = r.dw
		}
		if !r.sameJ {
			diffJ++
		}
		switch {
		case r.dw == 0:
			hist[0]++
		case r.dw <= .001:
			hist[1]++
		case r.dw <= .005:
			hist[2]++
		case r.dw <= .01:
			hist[3]++
		case r.dw <= .02:
			hist[4]++
		case r.dw <= .05:
			hist[5]++
		default:
			hist[6]++
		}
	}
	fmt.Printf("vertices with a different joint set (weight > 0.005): %d\n", diffJ)
	fmt.Println("histogram of the per-vertex max abs weight difference:")
	for i, l := range []string{"exactly 0", "<= 0.001", "<= 0.005", "<= 0.01", "<= 0.02", "<= 0.05", "> 0.05"} {
		fmt.Printf("  %-10s %d\n", l, hist[i])
	}
	var names []string
	for n := range perJoint {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Println("max abs weight difference per joint (over vertices where that joint has the largest difference):")
	for _, n := range names {
		fmt.Printf("  %-8s %.4f\n", n, perJoint[n])
	}
	// per-joint max over all vertices (any joint)
	sort.Slice(rs, func(a, b int) bool { return rs[a].dw > rs[b].dw })
	fmt.Printf("worst %d vertices:\n", *worst)
	for i := 0; i < *worst && i < len(rs); i++ {
		r := rs[i]
		fmt.Printf("  shell %-12s gw#%-5d go#%-5d (go shell %d) y=%.3f dw=%.4f joint=%s\n    gw: %s\n    go: %s\n", r.shell, r.gw, r.gov, shellIdx[r.gov], m.Bind[r.gw][1], r.dw, r.jd, fmtGW(m, jn, r.gw), fmtGo(gd.V[r.gov]))
	}
	if *goglb != "" {
		var prs []pair
		for _, r := range rs {
			prs = append(prs, pair{r.gw, r.gov})
		}
		meshCompare(*goglb, m, gd, prs, shellIdx)
	}
	if *mapOut != "" {
		mp := make([]int, len(gd.V))
		for i := range mp {
			mp[i] = -1
		}
		for _, r := range rs {
			mp[r.gov] = r.gw
		}
		b, _ := json.Marshal(mp)
		check(os.WriteFile(*mapOut, b, 0o644))
	}
	// per shell max
	ps := map[string]float64{}
	pn := map[string]int{}
	for _, r := range rs {
		if r.dw > ps[r.shell] {
			ps[r.shell] = r.dw
		}
		if r.dw > .02 {
			pn[r.shell]++
		}
	}
	var sn []string
	for n := range ps {
		sn = append(sn, n)
	}
	sort.Strings(sn)
	fmt.Println("per shell: max difference, vertices over 0.02:")
	for _, n := range sn {
		fmt.Printf("  %-12s %.4f %d\n", n, ps[n], pn[n])
	}
}

func fmtGW(m *ap.Model, jn []string, i int) string {
	s := ""
	for j := 0; j < 4; j++ {
		if m.Weight[i][j] > 0 {
			s += fmt.Sprintf("%s=%.4f ", jn[m.Joint[i][j]], m.Weight[i][j])
		}
	}
	return s
}
func fmtGo(v goVertex) string {
	s := ""
	for j := range v.J {
		s += fmt.Sprintf("%s=%.4f ", v.J[j], v.W[j])
	}
	return s
}
func sq(x float64) float64 { return x * x }
func countFalse(b []bool) int {
	n := 0
	for _, x := range b {
		if !x {
			n++
		}
	}
	return n
}
func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "skincompare:", err)
		os.Exit(1)
	}
}

func nearestSkin(gd goDump, shellOf []string, shell string, p [3]float64) string {
	nd, ni := math.Inf(1), -1
	for c := range gd.V {
		if shellOf[c] == shell {
			if d := sq(gd.V[c].P[0]-p[0]) + sq(gd.V[c].P[1]-p[1]) + sq(gd.V[c].P[2]-p[2]); d < nd {
				nd, ni = d, c
			}
		}
	}
	return fmtGo(gd.V[ni])
}

type pair struct{ gw, gov int }

func meshCompare(path string, m *ap.Model, gd goDump, prs []pair, shellIdx []int) {
	b, err := os.ReadFile(path)
	check(err)
	d, err := gltf.Decode(b)
	check(err)
	var pos, nor, uv []float32
	var idx []uint32
	var tg [][]float32
	for _, n := range d.Nodes {
		if n.Mesh != nil && n.Skin != nil {
			pr := d.Meshes[*n.Mesh].Primitives[0]
			pos, nor, uv, idx = pr.Positions, pr.Normals, pr.UV0, pr.Indices
			for _, t := range pr.Targets {
				tg = append(tg, t.Positions)
			}
		}
	}
	fmt.Printf("\n== mesh compare against %s: %d vertices, %d triangles, %d targets, normals %d, uv %d\n", path, len(pos)/3, len(idx)/3, len(tg), len(nor)/3, len(uv)/2)
	const k = 1 / ap.MetresPerYard
	// the Go glb is in yards; the model is in yards after conversion.
	_ = k
	g2 := map[int]int{}
	maxP, maxUV, maxN := 0.0, 0.0, 0.0
	nHist := map[string]int{}
	nShown, nAmb := 0, 0
	angHist := make([]int, 5)
	nUV, nN := 0, 0
	for _, p := range prs {
		g2[p.gw] = p.gov
		gv := p.gov
		for c := 0; c < 3; c++ {
			maxP = math.Max(maxP, math.Abs(float64(pos[3*gv+c])-m.Bind[p.gw][c]))
		}
	}
	fmt.Printf("max |go glb position - go dump position| is not computed; max |go glb pos - gw pos (yd)| over matched = %.2e\n", maxP)
	// uv and normals from the gw doc
	var gpr gltf.Primitive
	for _, n := range m.Doc.Nodes {
		if n.Mesh != nil && n.Skin != nil {
			gpr = m.Doc.Meshes[*n.Mesh].Primitives[0]
		}
	}
	uvHist := map[string]int{}
	var uvWorst []string
	for _, p := range prs {
		gv := p.gov
		du := math.Max(math.Abs(float64(gpr.UV0[2*p.gw])-float64(uv[2*gv])), math.Abs(float64(gpr.UV0[2*p.gw+1])-float64(uv[2*gv+1])))
		maxUV = math.Max(maxUV, du)
		if du > 1e-4 {
			nUV++
			if len(uvWorst) < 8 {
				uvWorst = append(uvWorst, fmt.Sprintf("gw#%d go#%d shell %s: gw uv (%.4f %.4f) go uv (%.4f %.4f)", p.gw, gv, m.VertexShell(p.gw), gpr.UV0[2*p.gw], gpr.UV0[2*p.gw+1], uv[2*gv], uv[2*gv+1]))
			}
			uvHist[m.VertexShell(p.gw)]++
		}
		dn := 0.0
		if os.Getenv("SC_DIRECT") == "" && ambiguous(gd, shellIdx, gv, 5e-4) {
			nAmb++
			continue
		}
		for c := 0; c < 3; c++ {
			dn = math.Max(dn, math.Abs(float64(gpr.Normals[3*p.gw+c])-float64(nor[3*gv+c])))
		}
		maxN = math.Max(maxN, dn)
		if dn > 0.05 {
			dot := 0.0
			for c := 0; c < 3; c++ {
				dot += float64(gpr.Normals[3*p.gw+c]) * float64(nor[3*gv+c])
			}
			ang := math.Acos(math.Max(-1, math.Min(1, dot))) * 180 / math.Pi
			switch {
			case ang < 5:
				angHist[0]++
			case ang < 15:
				angHist[1]++
			case ang < 45:
				angHist[2]++
			case ang < 90:
				angHist[3]++
			default:
				angHist[4]++
			}
			nN++
			if sh := os.Getenv("SC_NORM"); sh != "" && m.VertexShell(p.gw) == sh && nShown < 12 {
				nShown++
				fmt.Printf("  norm %s gw#%d (leaf %s local %d) go#%d local %d pos(%.4f %.4f %.4f) gw n(%.3f %.3f %.3f) go n(%.3f %.3f %.3f)\n", sh, p.gw, m.Leaf[m.Owner[p.gw]], p.gw-firstOf(m, p.gw), gv, gv-gd.Shells[shellIdx[gv]].First, m.Bind[p.gw][0], m.Bind[p.gw][1], m.Bind[p.gw][2], gpr.Normals[3*p.gw], gpr.Normals[3*p.gw+1], gpr.Normals[3*p.gw+2], nor[3*gv], nor[3*gv+1], nor[3*gv+2])
			}
			nHist[m.Leaf[m.Owner[p.gw]]]++
		}
	}
	if v := os.Getenv("SC_TRIS"); v != "" {
		var gwi, goi int
		fmt.Sscanf(v, "%d,%d", &gwi, &goi)
		fmt.Printf("triangles at gw#%d:\n", gwi)
		for _, t := range m.Tri {
			if t[0] == gwi || t[1] == gwi || t[2] == gwi {
				p0, p1, p2 := m.Bind[t[0]], m.Bind[t[1]], m.Bind[t[2]]
				u := [3]float64{p1[0] - p0[0], p1[1] - p0[1], p1[2] - p0[2]}
				w := [3]float64{p2[0] - p0[0], p2[1] - p0[1], p2[2] - p0[2]}
				c := [3]float64{u[1]*w[2] - u[2]*w[1], u[2]*w[0] - u[0]*w[2], u[0]*w[1] - u[1]*w[0]}
				fmt.Printf("  gw local %v cross (%.2e %.2e %.2e)\n", [3]int{t[0] - firstOf(m, gwi), t[1] - firstOf(m, gwi), t[2] - firstOf(m, gwi)}, c[0], c[1], c[2])
			}
		}
		fmt.Printf("triangles at go#%d (first of shell %d):\n", goi, gd.Shells[shellIdx[goi]].First)
		sf := gd.Shells[shellIdx[goi]].First
		for t := 0; t+2 < len(idx); t += 3 {
			a, b, c := int(idx[t]), int(idx[t+1]), int(idx[t+2])
			if a == goi || b == goi || c == goi {
				q := func(i int) [3]float64 { return [3]float64{float64(pos[3*i]), float64(pos[3*i+1]), float64(pos[3*i+2])} }
				p0, p1, p2 := q(a), q(b), q(c)
				u := [3]float64{p1[0] - p0[0], p1[1] - p0[1], p1[2] - p0[2]}
				w := [3]float64{p2[0] - p0[0], p2[1] - p0[1], p2[2] - p0[2]}
				cr := [3]float64{u[1]*w[2] - u[2]*w[1], u[2]*w[0] - u[0]*w[2], u[0]*w[1] - u[1]*w[0]}
				fmt.Printf("  go local %v cross (%.2e %.2e %.2e)\n", [3]int{a - sf, b - sf, c - sf}, cr[0], cr[1], cr[2])
			}
		}
	}
	uvDirect(m, gpr, uv, prs)
	uvCells(m, gpr, uv, prs)
	if lf := os.Getenv("SC_DUMP"); lf != "" {
		for i := range m.Bind {
			if m.Leaf[m.Owner[i]] == lf && i-firstOf(m, i) == 72 {
				fmt.Printf("dump %s local 72: gw#%d pos %v normal (%.3f %.3f %.3f)\n", lf, i, m.Bind[i], gpr.Normals[3*i], gpr.Normals[3*i+1], gpr.Normals[3*i+2])
			}
		}
	}
	fmt.Printf("UV: max abs difference %.4f; %d of %d matched vertices differ by more than 1e-4; by shell %v\n", maxUV, nUV, len(prs), uvHist)
	for _, l := range uvWorst {
		fmt.Println("   ", l)
	}
	fmt.Printf("normals: %d vertices skipped because another vertex of the same shell lies within 5e-4 yd (pole rings and seams, so the pairing is a guess)\n", nAmb)
	fmt.Printf("normals: max component difference %.4f; %d vertices differ by more than 0.05; by shell %v\n", maxN, nN, nHist)
	fmt.Printf("normals that differ by more than 0.05: angle between the two unit normals, vertex counts: <5deg %d, 5-15 %d, 15-45 %d, 45-90 %d, >=90 %d\n", angHist[0], angHist[1], angHist[2], angHist[3], angHist[4])
	// morph targets: gw targets are in the doc by name (yards after the model's conversion)
	for ti, name := range m.TargetNames {
		gt := m.Targets[name]
		mx, over := 0.0, 0
		for _, p := range prs {
			gv := p.gov
			for c := 0; c < 3; c++ {
				a := gt[p.gw][c]
				bb := float64(tg[ti][3*gv+c])
				mx = math.Max(mx, math.Abs(a-bb))
				if math.Abs(a-bb) > 1e-4 {
					over++
					break
				}
			}
		}
		fmt.Printf("morph target %-11s max abs difference %.2e yd; %d vertices differ by more than 1e-4\n", name, mx, over)
	}
	// triangles
	inv := map[int]int{}
	for g, o := range g2 {
		inv[o] = g
	}
	goTri := map[[3]int]int{}
	for t := 0; t+2 < len(idx); t += 3 {
		a, b2, c := int(idx[t]), int(idx[t+1]), int(idx[t+2])
		goTri[canon(a, b2, c)]++
	}
	setTri := map[[3]int]int{}
	for t := 0; t+2 < len(idx); t += 3 {
		v := []int{int(idx[t]), int(idx[t+1]), int(idx[t+2])}
		sort.Ints(v)
		setTri[[3]int{v[0], v[1], v[2]}]++
	}
	sameSet := 0
	missByShell := map[string]int{}
	same, miss, unmapped := 0, 0, 0
	for ti, t := range m.Tri {
		if a, ok := g2[t[0]]; ok {
			if b2, ok := g2[t[1]]; ok {
				if c, ok := g2[t[2]]; ok {
					v := []int{a, b2, c}
					sort.Ints(v)
					if setTri[[3]int{v[0], v[1], v[2]}] > 0 {
						sameSet++
					}
				}
			}
		}
		_ = ti

		a, ok1 := g2[t[0]]
		b2, ok2 := g2[t[1]]
		c, ok3 := g2[t[2]]
		if !(ok1 && ok2 && ok3) {
			unmapped++
			continue
		}
		key := canon(a, b2, c)
		if goTri[key] > 0 {
			goTri[key]--
			same++
		} else {
			miss++
			missByShell[m.VertexShell(t[0])]++
		}
	}
	fmt.Printf("triangles equal as vertex sets (ignoring winding and order): %d; gw triangles absent from go by shell: %v\n", sameSet, missByShell)
	{
		first := map[int]int{}
		for i := range m.Bind {
			if _, ok := first[m.Owner[i]]; !ok {
				first[m.Owner[i]] = i
			}
		}
		bad := map[string]int{}
		nbad := 0
		for _, p := range prs {
			lg := p.gw - first[m.Owner[p.gw]]
			lo := p.gov - gd.Shells[shellIdx[p.gov]].First
			if lg != lo {
				nbad++
				bad[m.Leaf[m.Owner[p.gw]]]++
			}
		}
		fmt.Printf("matched pairs whose index inside the shell differs between gw and go: %d, by gw shape %v\n", nbad, bad)
	}
	if os.Getenv("SC_TRI") != "" {
		// first shell named by SC_TRI: print local triangles of both
		var gfirst, glast, ofirst int = -1, -1, -1
		for i := range m.Bind {
			if m.VertexShell(i) == os.Getenv("SC_TRI") {
				if gfirst < 0 {
					gfirst = i
				}
				glast = i
			}
		}
		for si, sh := range gd.Shells {
			if sh.Name == os.Getenv("SC_TRI") {
				ofirst = sh.First
				_ = si
				break
			}
		}
		n := 0
		fmt.Printf("shell %s: gw first %d, go first %d\n", os.Getenv("SC_TRI"), gfirst, ofirst)
		for _, t := range m.Tri {
			if t[0] >= gfirst && t[0] <= glast && n < 14 {
				fmt.Printf("  gw %v\n", [3]int{t[0] - gfirst, t[1] - gfirst, t[2] - gfirst})
				n++
			}
		}
		n = 0
		for t := 0; t+2 < len(idx) && n < 14; t += 3 {
			if int(idx[t]) >= ofirst && int(idx[t]) < ofirst+glast-gfirst+1 {
				fmt.Printf("  go %v\n", [3]int{int(idx[t]) - ofirst, int(idx[t+1]) - ofirst, int(idx[t+2]) - ofirst})
				n++
			}
		}
		for _, p := range prs[:0] {
			_ = p
		}
		for i := gfirst; i < gfirst+14; i++ {
			fmt.Printf("  gw v%d -> go local %d  uv gw (%.3f %.3f)\n", i-gfirst, g2[i]-ofirst, gpr.UV0[2*i], gpr.UV0[2*i+1])
		}
	}
	fmt.Printf("triangles: gw %d, go %d; same (up to cyclic order, same winding) %d, gw triangle absent from go %d, unmapped %d\n", len(m.Tri), len(idx)/3, same, miss, unmapped)
}

func canon(a, b, c int) [3]int {
	switch {
	case a <= b && a <= c:
		return [3]int{a, b, c}
	case b <= a && b <= c:
		return [3]int{b, c, a}
	}
	return [3]int{c, a, b}
}

// The game's atlas regions (cmd/bakeathletes/layout.go), in texels of 1024.
var goRegions = func() [25][4]int {
	var r [25][4]int
	r[0] = [4]int{0, 0, 512, 512}
	r[1] = [4]int{512, 0, 256, 256}
	r[21] = [4]int{768, 0, 256, 256}
	r[2] = [4]int{512, 256, 512, 256}
	r[3] = [4]int{0, 512, 256, 256}
	r[4] = [4]int{256, 512, 256, 256}
	r[5] = [4]int{512, 512, 256, 256}
	r[6] = [4]int{768, 512, 128, 256}
	r[22] = [4]int{0, 832, 256, 96}
	r[23] = [4]int{0, 928, 256, 96}
	r[24] = [4]int{256, 832, 128, 96}
	for c := 7; c < 21; c++ {
		k := c - 7
		r[c] = [4]int{k % 16 * 64, 768 + k/16*64, 64, 64}
	}
	return r
}()

// gwToGoCell maps the athlete's atlas cell numbers to the game's (the game has a
// cell for the right thigh and right sleeve, and a glove cell, that the
// body does not use).
var gwToGoCell = []int{0, 1, 2, 3, 4, 6, 7, 8, 9, 11, 12, 13, 14, 15, 17, 18, 19, 20, 22, 23, 24}

func goCell(u, v float64) (int, float64, float64) {
	px, py := u*1024, v*1024
	for c, r := range goRegions {
		in := 6.0
		if r[2] <= 64 {
			in = 8
		}
		if px >= float64(r[0]) && px < float64(r[0]+r[2]) && py >= float64(r[1]) && py < float64(r[1]+r[3]) {
			return c, (px - float64(r[0]) - in) / (float64(r[2]) - 2*in), (py - float64(r[1]) - in) / (float64(r[3]) - 2*in)
		}
	}
	return -1, 0, 0
}

func uvCells(m *ap.Model, gpr gltf.Primitive, uv []float32, prs []pair) {
	same, diffCell, diffUV := 0, 0, 0
	shellBad := map[string]int{}
	mx := 0.0
	cellPairs := map[[2]int]int{}
	var worst []string
	for _, p := range prs {
		gu, gv := float64(gpr.UV0[2*p.gw]), float64(gpr.UV0[2*p.gw+1])
		col, row := int(gu*5), int(gv*5)
		gc := row*5 + col
		if gc < len(gwToGoCell) {
			gc = gwToGoCell[gc]
		}
		if gc == 4 && m.Shell[m.Owner[p.gw]] == "thigh" && strings.HasSuffix(m.Leaf[m.Owner[p.gw]], "_r") {
			gc = 5 // the game's right thigh has a cell of its own; the mirrored copy shares cell 4 here
		}
		if gc == 1 && strings.HasSuffix(m.Leaf[m.Owner[p.gw]], "_l") && m.Shell[m.Owner[p.gw]] == "shoulderpad" {
			gc = 21 // the game's +X sleeve is cell 21
		}
		g1 := (gu*5 - float64(col) - 0.07) / 0.86
		g2 := (gv*5 - float64(row) - 0.07) / 0.86
		oc, o1, o2 := goCell(float64(uv[2*p.gov]), float64(uv[2*p.gov+1]))
		cellPairs[[2]int{gc, oc}]++
		if gc != oc {
			diffCell++
			continue
		}
		if os.Getenv("SC_FLIPU") != "" && strings.HasSuffix(m.Leaf[m.Owner[p.gw]], "_r") {
			g1 = 1 - g1 // a mirrored copy runs u the other way round
		}
		d := math.Max(math.Abs(g1-o1), math.Abs(g2-o2))
		if d > 0.5 {
			d = math.Min(d, math.Abs(1-d)) // u wraps
		}
		mx = math.Max(mx, d)
		shellBad[m.VertexShell(p.gw)+" "+fmt.Sprint(gc)] += btoi(d > 1e-3)
		if d > 1e-3 {
			diffUV++
			if len(worst) < 6 {
				worst = append(worst, fmt.Sprintf("gw#%d go#%d %s cell %d: gw (u,v)=(%.4f %.4f) go (%.4f %.4f)", p.gw, p.gov, m.VertexShell(p.gw), gc, g1, g2, o1, o2))
			}
		} else {
			same++
		}
	}
	fmt.Printf("UV by cell: same cell and same (u,v) within 1e-3: %d; same cell, different (u,v): %d; different cell: %d; max (u,v) difference in same-cell vertices %.4f\n", same, diffUV, diffCell, mx)
	fmt.Printf("vertices still off by shell and cell:")
	for k, n := range shellBad {
		if n > 0 {
			fmt.Printf(" [%s]:%d", k, n)
		}
	}
	fmt.Println()
	fmt.Printf("cell pairs (gw cell, go cell) -> vertices, where they differ:")
	for k, n := range cellPairs {
		if k[0] != k[1] {
			fmt.Printf(" (%d,%d):%d", k[0], k[1], n)
		}
	}
	fmt.Println()
	for _, w := range worst {
		fmt.Println("   ", w)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func firstOf(m *ap.Model, i int) int {
	o := m.Owner[i]
	for k := i; k >= 0; k-- {
		if m.Owner[k] != o {
			return k + 1
		}
	}
	return 0
}

// ambiguous reports whether another vertex of the same Go shell lies within d
// yards of vertex v.
func ambiguous(gd goDump, shellIdx []int, v int, d float64) bool {
	sh := gd.Shells[shellIdx[v]]
	for i := sh.First; i < sh.End; i++ {
		if i != v && math.Abs(gd.V[i].P[0]-gd.V[v].P[0]) < d && math.Abs(gd.V[i].P[1]-gd.V[v].P[1]) < d && math.Abs(gd.V[i].P[2]-gd.V[v].P[2]) < d {
			return true
		}
	}
	return false
}

// expectedGoCell is where vertex i of the Greenware mesh should fall in the
// game's atlas: its cell renumbered to the game's, and its (u, v) with a
// mirrored ring shape's u reversed and a mirrored tube's u turned half way.
func expectedGoCell(m *ap.Model, i int, uv []float32) (int, float64, float64) {
	gu, gv := float64(uv[2*i]), float64(uv[2*i+1])
	col, row := int(gu*5), int(gv*5)
	if gu*5 == 5 {
		col = 4
	}
	if gv*5 == 5 {
		row = 4
	}
	gc := row*5 + col
	u := (gu*5 - float64(col) - 0.07) / 0.86
	v := (gv*5 - float64(row) - 0.07) / 0.86
	if gc < len(gwToGoCell) {
		gc = gwToGoCell[gc]
	}
	leaf := m.Leaf[m.Owner[i]]
	sh := m.Shell[m.Owner[i]]
	if gc == 4 && sh == "thigh" && strings.HasSuffix(leaf, "_r") {
		gc = 5
	}
	if gc == 1 && sh == "shoulderpad" && strings.HasSuffix(leaf, "_l") {
		gc = 21
	}
	if strings.HasSuffix(leaf, "_r") && sh != "lace" {
		if sh == "chinstrap" {
			u = math.Mod(u+0.5, 1)
		} else {
			u = 1 - u
		}
	}
	return gc, u, v
}

// uvDirect compares the two files' texture coordinates as they are (both are
// in the game's atlas layout): maximum and root-mean-square difference over the
// paired vertices, and the vertices over 1e-4 by shape.
func uvDirect(m *ap.Model, gpr gltf.Primitive, uv []float32, prs []pair) {
	mx, sum := 0.0, 0.0
	over := map[string]int{}
	n1, n2, n3 := 0, 0, 0
	for _, p := range prs {
		du := math.Abs(float64(gpr.UV0[2*p.gw]) - float64(uv[2*p.gov]))
		dv := math.Abs(float64(gpr.UV0[2*p.gw+1]) - float64(uv[2*p.gov+1]))
		d := math.Max(du, dv)
		mx = math.Max(mx, d)
		sum += du*du + dv*dv
		if d > 1e-4 {
			if n1 < 6 {
				fmt.Printf("  uv off: gw#%d %s local %d go#%d: gw (%.5f %.5f) go (%.5f %.5f)\n", p.gw, m.Leaf[m.Owner[p.gw]], p.gw-firstOf(m, p.gw), p.gov, gpr.UV0[2*p.gw], gpr.UV0[2*p.gw+1], uv[2*p.gov], uv[2*p.gov+1])
			}
			n1++
			over[m.Leaf[m.Owner[p.gw]]]++
		}
		if d > 1e-3 {
			n2++
		}
		if d > 1e-2 {
			n3++
		}
	}
	fmt.Printf("UV direct: max abs difference %.3e, RMS per coordinate %.3e over %d vertices; over 1e-4: %d, over 1e-3: %d, over 1e-2: %d; by shape: %v\n", mx, math.Sqrt(sum/float64(2*len(prs))), len(prs), n1, n2, n3, over)
}
