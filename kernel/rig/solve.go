package rig

import (
	"fmt"
	"math"
	"sort"

	"m31labs.dev/greenware/kernel/mesh"
)

// The skin weight solver (milestone M5). It chooses influences for a skeleton
// that is already placed: it never moves or adds a joint.
//
// Method "falloff". For every position p and every candidate joint c:
//
//	w0[p][c] = g / (d2 + soft^2)^power
//
// where d2 is the squared distance from p to the nearest bone segment of c,
// g is `own` when c carries weight on p before the solve and 1 otherwise, and
// the row is then divided by its sum. The bone segments of c run from c to
// each of its children; a leaf runs from c along its parent's bone direction
// for the length of that bone (a lone joint is a point). The candidates of p
// are the joints that carry weight on p before the solve and every joint at
// most `reach` steps from one of them in the joint tree.
//
// Refinement ("smooth"). A screened Laplacian solve on the mesh edges, by
// Jacobi iteration from the start vector w0, for every candidate joint c:
//
//	w[p][c] <- (w0[p][c] + smooth * sum over neighbours n of w[n][c]) / (1 + smooth * deg(p))
//
// Neighbours are the other end of every geometric edge, in index order.
// Weight on a joint that is not a candidate of p is ignored. The map is a
// contraction (its row sums are at most smooth*deg/(1+smooth*deg) < 1), so
// the iteration converges to the solution of (I + smooth*L) w = w0. The
// residual of a sweep is the largest change of any weight in it. The sweeps
// stop at the first one whose residual is at most `tolerance`. If the cap of
// `iterations` sweeps is reached with a larger residual, Solve returns a
// *NoConvergence and changes nothing.
//
// Finish. Each row has `cutoff` taken off the share of every weight (a share that falls to zero is dropped), is cut to its
// `max_influences` largest weights (ties by joint index), renormalised, and quantised to multiples of 2^-24: every
// weight but the largest is rounded half to even, and the largest takes the
// remainder, so the weights are exactly representable as float32, sum to
// exactly 1 and are not negative. A weight that rounds to 0 is dropped.
//
// There are no maps in the numeric paths. Vertices, joints, edges and
// segments are visited in index order, and every multiply that feeds an add
// is wrapped in float64(...), so the bits do not depend on the architecture.

// quantum is the weight grid of a solved row: 2^-24.
const (
	quantumBits = 24
	quantumOne  = 1 << quantumBits
)

// Solve holds the parameters of one solve step.
type Solve struct {
	MaxInfluences int     // 1 to 4
	Reach         int     // hops in the joint tree; 0 keeps the candidates to the joints a position already has
	Power         int     // 1 to 4
	Own           float64 // at least 1
	Cutoff        float64 // 0 to below 0.5: taken off the share of each weight, a share that reaches 0 is dropped
	Soft          float64 // metres, positive
	Refine        bool    // false skips the smoothing
	Smooth        float64 // positive
	Iterations    int     // cap on sweeps, at least 1
	Tolerance     float64 // positive
	// Rigid lists the shape ids whose positions keep the weights they have.
	Rigid []int
}

// SolveResult is what a finished solve measured.
type SolveResult struct {
	Iterations int
	Residual   float64
}

// NoConvergence is the error of a solve whose residual is still above the
// tolerance after the sweep cap.
type NoConvergence struct {
	Iterations int
	Residual   float64
	Tolerance  float64
}

func (e *NoConvergence) Error() string {
	return fmt.Sprintf("the solve did not converge: after %d iterations the residual is %.6g, above the tolerance %.6g", e.Iterations, e.Residual, e.Tolerance)
}

// segment is one straight piece of a bone.
type segment struct{ a, b Vec }

// boneSegments returns the segments of every joint, in joint order.
func boneSegments(sk *Skeleton) [][]segment {
	out := make([][]segment, len(sk.Joints))
	for j := range sk.Joints {
		p := sk.Joints[j].Pos
		kids := sk.Children(j)
		for _, k := range kids {
			out[j] = append(out[j], segment{p, sk.Joints[k].Pos})
		}
		if len(kids) > 0 {
			continue
		}
		if par := sk.Joints[j].Parent; par >= 0 {
			out[j] = append(out[j], segment{p, mesh.Add(p, mesh.Sub(p, sk.Joints[par].Pos))})
		} else {
			out[j] = append(out[j], segment{p, p})
		}
	}
	return out
}

// distSq returns the squared distance from p to the segment s.
func distSq(p Vec, s segment) float64 {
	ab := mesh.Sub(s.b, s.a)
	ap := mesh.Sub(p, s.a)
	den := mesh.Dot(ab, ab)
	t := 0.0
	if den > 0 {
		t = clamp01(mesh.Dot(ap, ab) / den)
	}
	d := mesh.Sub(ap, mesh.Scale(ab, t))
	return mesh.Dot(d, d)
}

// reachSets returns, for every joint, the sorted joints at most reach steps
// away in the tree (itself included).
func reachSets(sk *Skeleton, reach int) [][]int {
	n := len(sk.Joints)
	adj := make([][]int, n)
	for j, jt := range sk.Joints {
		if jt.Parent >= 0 {
			adj[j] = append(adj[j], jt.Parent)
			adj[jt.Parent] = append(adj[jt.Parent], j)
		}
	}
	out := make([][]int, n)
	for j := range out {
		dist := make([]int, n)
		for k := range dist {
			dist[k] = -1
		}
		dist[j] = 0
		queue := []int{j}
		for len(queue) > 0 {
			c := queue[0]
			queue = queue[1:]
			if dist[c] == reach {
				continue
			}
			for _, nb := range adj[c] {
				if dist[nb] < 0 {
					dist[nb] = dist[c] + 1
					queue = append(queue, nb)
				}
			}
		}
		for k := range dist {
			if dist[k] >= 0 {
				out[j] = append(out[j], k)
			}
		}
	}
	return out
}

func (p Solve) validate() error {
	switch {
	case p.MaxInfluences < 1 || p.MaxInfluences > 4:
		return fmt.Errorf("solve `max_influences` must be 1 to 4, found %d", p.MaxInfluences)
	case p.Reach < 0:
		return fmt.Errorf("solve `reach` must not be negative, found %d", p.Reach)
	case p.Power < 1 || p.Power > 4:
		return fmt.Errorf("solve `power` must be 1 to 4, found %d", p.Power)
	case !(p.Cutoff >= 0 && p.Cutoff < 0.5):
		return fmt.Errorf("solve `cutoff` must be at least 0 and below 0.5, found %v", p.Cutoff)
	case !(p.Own >= 1):
		return fmt.Errorf("solve `own` must be at least 1, found %v", p.Own)
	case !(p.Soft > 0):
		return fmt.Errorf("solve `soft` must be positive, found %v", p.Soft)
	case p.Refine && !(p.Smooth > 0):
		return fmt.Errorf("solve `smooth` must be positive, found %v", p.Smooth)
	case p.Refine && p.Iterations < 1:
		return fmt.Errorf("solve `iterations` must be at least 1, found %d", p.Iterations)
	case p.Refine && !(p.Tolerance > 0):
		return fmt.Errorf("solve `tolerance` must be positive, found %v", p.Tolerance)
	}
	return nil
}

// Solve replaces the influences of every position that is not in a rigid
// shape. edges lists the geometric edges as pairs of position indices; it need
// not be sorted. Nothing in b changes unless Solve returns a nil error.
func (b *Bound) Solve(sk *Skeleton, p Solve, edges [][2]int32) (SolveResult, error) {
	var res SolveResult
	if err := p.validate(); err != nil {
		return res, err
	}
	nj, nv := len(sk.Joints), len(b.Pos)
	pinned := make([]bool, nv)
	for i := range pinned {
		for _, r := range p.Rigid {
			if b.Shape[i] == r {
				pinned[i] = true
				break
			}
		}
	}
	segs := boneSegments(sk)
	reach := reachSets(sk, p.Reach)
	soft2 := float64(p.Soft * p.Soft)

	// Start vector and candidate mask, both dense [position][joint].
	cand := make([]bool, nv*nj)
	w0 := make([]float64, nv*nj)
	for i := 0; i < nv; i++ {
		if pinned[i] {
			continue
		}
		own := make([]bool, nj)
		for _, in := range b.Infl[i] {
			if in.W > 0 && in.Joint >= 0 && in.Joint < nj {
				own[in.Joint] = true
				for _, c := range reach[in.Joint] {
					cand[i*nj+c] = true
				}
			}
		}
		var sum float64
		for c := 0; c < nj; c++ {
			if !cand[i*nj+c] {
				continue
			}
			d2 := math.Inf(1)
			for _, s := range segs[c] {
				d2 = math.Min(d2, distSq(b.Pos[i], s))
			}
			den := d2 + soft2
			pw := den
			for k := 1; k < p.Power; k++ {
				pw = float64(pw * den)
			}
			g := 1.0
			if own[c] {
				g = p.Own
			}
			w := g / pw
			w0[i*nj+c] = w
			sum += w
		}
		if !(sum > 0) || math.IsInf(sum, 0) {
			return res, fmt.Errorf("position %d has no usable candidate joint", i)
		}
		for c := 0; c < nj; c++ {
			w0[i*nj+c] /= sum
		}
	}

	w := w0
	if p.Refine {
		// Adjacency in index order: offsets into one array. The edge list is
		// sorted and deduplicated first, so every neighbour list is sorted.
		sorted := sortedEdges(edges)
		deg := make([]int32, nv+1)
		for _, e := range sorted {
			if e[0] == e[1] {
				continue
			}
			deg[e[0]]++
			deg[e[1]]++
		}
		start := make([]int32, nv+1)
		for i := 0; i < nv; i++ {
			start[i+1] = start[i] + deg[i]
		}
		fill := append([]int32(nil), start[:nv]...)
		nbr := make([]int32, start[nv])
		for _, e := range sorted {
			if e[0] == e[1] {
				continue
			}
			nbr[fill[e[0]]] = e[1]
			fill[e[0]]++
			nbr[fill[e[1]]] = e[0]
			fill[e[1]]++
		}
		cur := append([]float64(nil), w0...)
		next := make([]float64, len(w0))
		converged := false
		for it := 1; it <= p.Iterations; it++ {
			var resid float64
			for i := 0; i < nv; i++ {
				if pinned[i] {
					continue
				}
				d := float64(start[i+1] - start[i])
				scale := 1 + float64(p.Smooth*d)
				for c := 0; c < nj; c++ {
					if !cand[i*nj+c] {
						continue
					}
					var s float64
					for _, n := range nbr[start[i]:start[i+1]] {
						s += cur[int(n)*nj+c]
					}
					v := (w0[i*nj+c] + float64(p.Smooth*s)) / scale
					next[i*nj+c] = v
					resid = math.Max(resid, math.Abs(v-cur[i*nj+c]))
				}
			}
			cur, next = next, cur
			res.Iterations, res.Residual = it, resid
			if resid <= p.Tolerance {
				converged = true
				break
			}
		}
		if !converged {
			return SolveResult{}, &NoConvergence{Iterations: res.Iterations, Residual: res.Residual, Tolerance: p.Tolerance}
		}
		w = cur
	}

	// Finish every row into a fresh slice; b is written only when all rows pass.
	out := make([][]Influence, nv)
	for i := 0; i < nv; i++ {
		if pinned[i] {
			out[i] = b.Infl[i]
			continue
		}
		row := make([]Influence, 0, 4)
		for c := 0; c < nj; c++ {
			if v := w[i*nj+c]; cand[i*nj+c] && v > 0 {
				row = append(row, Influence{c, v})
			}
		}
		q, err := quantise(row, p.MaxInfluences, p.Cutoff)
		if err != nil {
			return SolveResult{}, fmt.Errorf("position %d: %v", i, err)
		}
		out[i] = q
	}
	copy(b.Infl, out)
	return res, nil
}

// sortedEdges returns a sorted copy of the edges, each as (low, high), with
// repeats removed.
func sortedEdges(edges [][2]int32) [][2]int32 {
	out := make([][2]int32, 0, len(edges))
	for _, e := range edges {
		if e[0] > e[1] {
			e[0], e[1] = e[1], e[0]
		}
		out = append(out, e)
	}
	// Equal pairs are identical values, so the result does not depend on how
	// the sort orders them.
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	n := 0
	for i, e := range out {
		if i == 0 || e != out[n-1] {
			out[n] = e
			n++
		}
	}
	return out[:n]
}

// quantise takes cutoff off the share of each weight in the row (a weight
// that falls to zero is dropped, the largest always stays), cuts the row to its n largest and writes them on the 2^-24
// grid, summing to exactly 1. The row comes in joint order.
func quantise(row []Influence, n int, cutoff float64) ([]Influence, error) {
	in := SortInfluences(row)
	if len(in) == 0 {
		return nil, fmt.Errorf("no influence")
	}
	var total float64
	for _, x := range in {
		if math.IsNaN(x.W) || math.IsInf(x.W, 0) || x.W < 0 {
			return nil, fmt.Errorf("invalid weight %v on joint %d", x.W, x.Joint)
		}
		total += x.W
	}
	if !(total > 0) {
		return nil, fmt.Errorf("the weights sum to %v", total)
	}
	// A soft threshold: the cutoff share is taken off every weight, and what
	// goes below zero is dropped. A hard cut would give two neighbours that
	// straddle it a jump in weight, which tears a thin shape. The largest
	// stays whatever the cutoff.
	shifted := make([]Influence, 0, len(in))
	for _, x := range in {
		if w := x.W/total - cutoff; w > 0 {
			shifted = append(shifted, Influence{x.Joint, w})
		}
	}
	if len(shifted) == 0 {
		shifted = append(shifted, Influence{in[0].Joint, 1})
	}
	in = shifted
	if len(in) > n {
		in = in[:n]
	}
	var sum float64
	for _, x := range in {
		sum += x.W
	}
	if !(sum > 0) {
		return nil, fmt.Errorf("the weights sum to %v", sum)
	}
	q := make([]int64, len(in))
	var rest int64
	for k := 1; k < len(in); k++ {
		q[k] = int64(math.RoundToEven(float64(in[k].W/sum) * quantumOne))
		rest += q[k]
	}
	q[0] = quantumOne - rest
	if q[0] <= 0 {
		return nil, fmt.Errorf("the largest weight rounds to %d of %d", q[0], quantumOne)
	}
	out := make([]Influence, 0, len(in))
	for k, x := range in {
		if q[k] > 0 {
			out = append(out, Influence{x.Joint, float64(q[k]) / quantumOne})
		}
	}
	return out, nil
}
