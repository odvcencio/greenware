package rig

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

// tube is a straight chain j0 (0,0,0), j1 (0,1,0), j2 (0,2,0). The leaf j2
// has the segment (0,2,0)..(0,3,0).
func tube(t *testing.T) *Skeleton {
	return mustSkeleton(t,
		Joint{Name: "j0", Parent: -1, Pos: Vec{0, 0, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "j1", Parent: 0, Pos: Vec{0, 1, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "j2", Parent: 1, Pos: Vec{0, 2, 0}, Rot: QIdentity, TwistAxis: -1},
	)
}

// boundOn returns a Bound of one shape whose positions are all on joint.
func boundOn(joint int, pos ...Vec) *Bound {
	b := &Bound{}
	b.AddShape(0, pos, joint)
	return b
}

func solveParams() Solve {
	return Solve{MaxInfluences: 4, Reach: 1, Power: 1, Own: 1, Soft: 0.5}
}

const q24 = 1.0 / quantumOne

func weightsOf(b *Bound, i int) map[int]float64 {
	m := map[int]float64{}
	for _, in := range b.Infl[i] {
		m[in.Joint] = in.W
	}
	return m
}

// Vertices at a joint plane and on a bone, with the weights worked by hand.
// With power 1, soft 0.5 (soft^2 = 0.25) the weight of a joint is
// 1/(d^2 + 0.25) times its factor, over the sum. The radius of every vertex is
// 0.5, so d^2 is 0.25 plus the squared axial distance to the bone.
func TestSolveAnalyticTube(t *testing.T) {
	sk := tube(t)
	cases := []struct {
		name  string
		y     float64
		own   float64
		limit int
		// want is the exact weight of each joint, as a fraction.
		want map[int]float64
	}{
		// y = 1, d^2 = 0.25, 0.25, 1.25: weights 2, 2, 2/3 -> 3/7, 3/7, 1/7.
		{"joint plane, no own bias", 1, 1, 4, map[int]float64{0: 3.0 / 7, 1: 3.0 / 7, 2: 1.0 / 7}},
		// With own = 4 on j1 (the shape's joint): 2, 8, 2/3 -> 3/16, 3/4, 1/16 (exact on the grid).
		{"joint plane, own = 4", 1, 4, 4, map[int]float64{0: 0.1875, 1: 0.75, 2: 0.0625}},
		// Two influences: j0 and j1 tie at 3/7, so 1/2 each after the cut.
		{"joint plane, two influences", 1, 1, 2, map[int]float64{0: 0.5, 1: 0.5}},
		// y = 0.5: d^2 = 0.25, 0.5, 2.5 -> weights 2, 4/3, 4/11 -> 33/61, 22/61, 6/61.
		{"mid bone j0", 0.5, 1, 4, map[int]float64{0: 33.0 / 61, 1: 22.0 / 61, 2: 6.0 / 61}},
		// y = 1.5: d^2 = 0.5, 0.25, 0.5 -> weights 4/3, 2, 4/3 -> 2/7, 3/7, 2/7.
		{"mid bone j1", 1.5, 1, 4, map[int]float64{0: 2.0 / 7, 1: 3.0 / 7, 2: 2.0 / 7}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := boundOn(1, Vec{0.5, c.y, 0})
			p := solveParams()
			p.Own, p.MaxInfluences = c.own, c.limit
			if _, err := b.Solve(sk, p, nil); err != nil {
				t.Fatal(err)
			}
			got := weightsOf(b, 0)
			var sum float64
			for j, w := range got {
				sum += w
				if k := w / q24; k != math.Trunc(k) {
					t.Errorf("joint %d weight %v is not a multiple of 2^-24", j, w)
				}
			}
			if sum != 1 {
				t.Errorf("weights sum to %v, not exactly 1", sum)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want joints %v", got, c.want)
			}
			// The cut renormalises, so compare against the fractions of the kept joints.
			var kept float64
			for j := range c.want {
				kept += c.want[j]
			}
			for j, w := range c.want {
				if math.Abs(got[j]-w/kept) > q24 {
					t.Errorf("joint %d: got %.9f, want %.9f", j, got[j], w/kept)
				}
			}
		})
	}
}

// The middle vertex of a symmetric tube is symmetric between the joints on
// either side, to one quantum (the grid cannot split an odd number of units).
// The exact values are pinned.
func TestSolveSymmetricMiddleVertex(t *testing.T) {
	sk := tube(t)
	b := boundOn(1, Vec{0.5, 1, 0})
	if _, err := b.Solve(sk, solveParams(), nil); err != nil {
		t.Fatal(err)
	}
	got := weightsOf(b, 0)
	if d := math.Abs(got[0] - got[1]); d > q24 {
		t.Errorf("j0 %v and j1 %v differ by %v", got[0], got[1], d)
	}
	// 3/7 of 2^24 is 7190235.43: j1 rounds to 7190235, j2 (1/7) to 2396745, and
	// j0, the first of the tie, takes the remainder 7190236.
	want := map[int]float64{0: 7190236 * q24, 1: 7190235 * q24, 2: 2396745 * q24}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// A vertex off to the other side of the axis gives the same weights.
	c := boundOn(1, Vec{-0.5, 1, 0})
	if _, err := c.Solve(sk, solveParams(), nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.Infl, c.Infl) {
		t.Errorf("x and -x differ: %v vs %v", b.Infl, c.Infl)
	}
}

// Rigid shapes keep their weights, whatever the solver would give them.
func TestSolveRigidShapeKept(t *testing.T) {
	sk := tube(t)
	b := &Bound{}
	b.AddShape(7, []Vec{{0.5, 1.9, 0}}, 1) // near j2, bound to j1, rigid
	b.AddShape(8, []Vec{{0.5, 1.9, 0}}, 1) // the same place, free
	p := solveParams()
	p.Rigid = []int{7}
	if _, err := b.Solve(sk, p, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.Infl[0], []Influence{{Joint: 1, W: 1}}) {
		t.Errorf("rigid shape changed: %v", b.Infl[0])
	}
	if len(b.Infl[1]) < 2 || b.Infl[1][0].Joint == 1 && b.Infl[1][0].W == 1 {
		t.Errorf("free shape was not solved: %v", b.Infl[1])
	}
}

// A vertex cannot take weight from a joint beyond its reach.
func TestSolveReach(t *testing.T) {
	sk := mustSkeleton(t,
		Joint{Name: "a", Parent: -1, Pos: Vec{0, 0, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "b", Parent: 0, Pos: Vec{0, 1, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "c", Parent: 1, Pos: Vec{0, 2, 0}, Rot: QIdentity, TwistAxis: -1},
		Joint{Name: "d", Parent: 2, Pos: Vec{0, 3, 0}, Rot: QIdentity, TwistAxis: -1},
	)
	for reach, want := range map[int][]int{0: {0}, 1: {0, 1}, 2: {0, 1, 2}, 3: {0, 1, 2, 3}} {
		b := boundOn(0, Vec{0.1, 0.1, 0})
		p := solveParams()
		p.Reach = reach
		if _, err := b.Solve(sk, p, nil); err != nil {
			t.Fatal(err)
		}
		var got []int
		for _, in := range b.Infl[0] {
			got = append(got, in.Joint)
		}
		if reflect.DeepEqual(got, want) {
			continue
		}
		// Weights are sorted by size, so compare as sets.
		set := map[int]bool{}
		for _, j := range got {
			set[j] = true
		}
		for _, j := range want {
			delete(set, j)
		}
		if len(set) != 0 {
			t.Errorf("reach %d: joints %v are outside %v", reach, got, want)
		}
	}
}

// A ring of positions around a joint, joined by edges, for the smoothing.
func ringBound(n int) (*Bound, [][2]int32) {
	b := &Bound{}
	var pos []Vec
	var edges [][2]int32
	for i := 0; i < n; i++ {
		y := 0.2 + float64(i)*(1.6/float64(n-1))
		pos = append(pos, Vec{0.3, y, 0})
		if i > 0 {
			edges = append(edges, [2]int32{int32(i - 1), int32(i)})
		}
	}
	b.AddShape(0, pos, 1)
	return b, edges
}

func TestSolveSmoothingConvergesWithinCap(t *testing.T) {
	sk := tube(t)
	b, edges := ringBound(40)
	p := solveParams()
	p.Power, p.Own = 2, 4
	p.Refine, p.Smooth, p.Iterations, p.Tolerance = true, 0.5, 64, 1e-4
	res, err := b.Solve(sk, p, edges)
	if err != nil {
		t.Fatal(err)
	}
	if res.Iterations < 1 || res.Iterations > 64 || res.Residual > 1e-4 {
		t.Errorf("iterations %d residual %v", res.Iterations, res.Residual)
	}
	for i, row := range b.Infl {
		var sum float64
		for _, in := range row {
			if in.W < 0 {
				t.Errorf("position %d: negative weight", i)
			}
			sum += in.W
		}
		if sum != 1 {
			t.Errorf("position %d: weights sum to %v", i, sum)
		}
	}
	// Smoothing a chain makes neighbouring rows closer than the raw falloff.
	raw, _ := ringBound(40)
	q := p
	q.Refine = false
	if _, err := raw.Solve(sk, q, nil); err != nil {
		t.Fatal(err)
	}
	jump := func(b *Bound) float64 {
		var worst float64
		for i := 1; i < len(b.Infl); i++ {
			worst = math.Max(worst, math.Abs(weightsOf(b, i)[1]-weightsOf(b, i-1)[1]))
		}
		return worst
	}
	if jump(b) >= jump(raw) {
		t.Errorf("smoothing did not reduce the largest step: %v >= %v", jump(b), jump(raw))
	}
}

// A tolerance no sweep count can reach refuses, names the count and the
// residual, and leaves every weight as it was.
func TestSolveRefusesNonConvergence(t *testing.T) {
	sk := tube(t)
	b, edges := ringBound(40)
	before := make([][]Influence, len(b.Infl))
	for i := range before {
		before[i] = append([]Influence(nil), b.Infl[i]...)
	}
	p := solveParams()
	p.Refine, p.Smooth, p.Iterations, p.Tolerance = true, 0.5, 3, 1e-12
	res, err := b.Solve(sk, p, edges)
	var nc *NoConvergence
	if !errors.As(err, &nc) {
		t.Fatalf("got %v, want *NoConvergence", err)
	}
	if nc.Iterations != 3 || !(nc.Residual > 1e-12) || nc.Tolerance != 1e-12 || res != (SolveResult{}) {
		t.Errorf("%+v %+v", nc, res)
	}
	if !reflect.DeepEqual(b.Infl, before) {
		t.Errorf("a refused solve changed the weights")
	}
}

func TestSolveRejectsBadParameters(t *testing.T) {
	sk := tube(t)
	for name, edit := range map[string]func(*Solve){
		"limit 0":     func(p *Solve) { p.MaxInfluences = 0 },
		"limit 5":     func(p *Solve) { p.MaxInfluences = 5 },
		"power 0":     func(p *Solve) { p.Power = 0 },
		"cutoff 0.5":  func(p *Solve) { p.Cutoff = 0.5 },
		"own < 1":     func(p *Solve) { p.Own = 0.5 },
		"soft 0":      func(p *Solve) { p.Soft = 0 },
		"reach < 0":   func(p *Solve) { p.Reach = -1 },
		"no sweeps":   func(p *Solve) { p.Refine, p.Smooth, p.Tolerance = true, 1, 1 },
		"smooth 0":    func(p *Solve) { p.Refine, p.Iterations, p.Tolerance = true, 1, 1 },
		"tolerance 0": func(p *Solve) { p.Refine, p.Iterations, p.Smooth = true, 1, 1 },
	} {
		p := solveParams()
		edit(&p)
		if _, err := boundOn(1, Vec{0.5, 1, 0}).Solve(sk, p, nil); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// The solve does not depend on the order of the edge list or on repeats in it.
func TestSolveEdgeOrderDoesNotMatter(t *testing.T) {
	sk := tube(t)
	p := solveParams()
	p.Power, p.Own = 2, 4
	p.Refine, p.Smooth, p.Iterations, p.Tolerance = true, 0.5, 64, 1e-6
	a, edges := ringBound(25)
	if _, err := a.Solve(sk, p, edges); err != nil {
		t.Fatal(err)
	}
	var shuffled [][2]int32
	for i := len(edges) - 1; i >= 0; i-- {
		shuffled = append(shuffled, [2]int32{edges[i][1], edges[i][0]}, edges[i])
	}
	b, _ := ringBound(25)
	if _, err := b.Solve(sk, p, shuffled); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Infl, b.Infl) {
		t.Error("the edge order changed the weights")
	}
}

// Two runs give the same bits.
func TestSolveRepeats(t *testing.T) {
	sk := tube(t)
	p := solveParams()
	p.Power, p.Own = 2, 4
	p.Refine, p.Smooth, p.Iterations, p.Tolerance = true, 0.5, 64, 1e-6
	a, edges := ringBound(60)
	b, _ := ringBound(60)
	ra, err := a.Solve(sk, p, edges)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := b.Solve(sk, p, edges)
	if ra != rb || !reflect.DeepEqual(a.Infl, b.Infl) {
		t.Error("two runs differ")
	}
}

func TestQuantiseExact(t *testing.T) {
	rows := [][]Influence{
		{{0, 1}},
		{{0, 0.5}, {1, 0.5}},
		{{0, 1.0 / 3}, {1, 1.0 / 3}, {2, 1.0 / 3}},
		{{0, 0.7}, {1, 0.2}, {2, 0.06}, {3, 0.04}},
		{{0, 0.34}, {1, 0.26}, {2, 0.2}, {3, 0.12}, {4, 0.08}},
	}
	for _, r := range rows {
		q, err := quantise(r, 4, 0)
		if err != nil {
			t.Fatal(err)
		}
		var sum float64
		for _, in := range q {
			if in.W <= 0 || in.W/q24 != math.Trunc(in.W/q24) || float64(float32(in.W)) != in.W {
				t.Errorf("%v is not a positive float32 multiple of 2^-24", in.W)
			}
			sum += in.W
		}
		if sum != 1 || len(q) > 4 {
			t.Errorf("%v: sum %v, %d influences", q, sum, len(q))
		}
	}
}

// The cutoff is a soft threshold: it comes off every share, a share that
// reaches zero is dropped, the largest never is, and what is left is
// renormalised on the grid. Two rows that differ by a little differ by a
// little after it.
func TestQuantiseCutoff(t *testing.T) {
	row := []Influence{{0, 0.90}, {1, 0.09}, {2, 0.01}}
	q, err := quantise(row, 4, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	// 0.85 and 0.04 remain: 0.85/0.89 and 0.04/0.89.
	if len(q) != 2 || q[0].Joint != 0 || q[1].Joint != 1 || math.Abs(q[1].W-0.04/0.89) > q24 {
		t.Fatalf("%v", q)
	}
	if q[0].W+q[1].W != 1 {
		t.Errorf("sum %v", q[0].W+q[1].W)
	}
	// A cutoff above every other weight leaves the largest alone at weight 1.
	q, _ = quantise(row, 4, 0.4)
	if len(q) != 1 || q[0].W != 1 {
		t.Errorf("%v", q)
	}
	// Continuity: a weight just either side of the cutoff gives nearly the same row.
	a, _ := quantise([]Influence{{0, 0.9501}, {1, 0.0499}}, 4, 0.05)
	b, _ := quantise([]Influence{{0, 0.9499}, {1, 0.0501}}, 4, 0.05)
	if math.Abs(a[0].W-b[0].W) > 1e-3 {
		t.Errorf("%v vs %v", a, b)
	}
}
