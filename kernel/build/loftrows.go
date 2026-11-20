package build

import (
	"math/big"

	"m31labs.dev/greenware/kernel/mesh"
)

// LoftKeepRows chooses which rows of a loft a level keeps (language.md section
// 6.1 step 4). grid holds the points of every original row at the level's
// chosen columns (n columns for a full turn, n+1 for an arc), so the row
// indices, values, angles and chart coordinates are those of level 0.
//
// The first and last rows always stay. Starting from all rows, the rule
// tries to delete each surviving interior row in turn, scores each trial by
// the largest distance between the trial strip and the all-row strip, and
// commits the trial with the smallest score (the smaller row index on a tie)
// if that score is at most maxError. It repeats until no trial qualifies. A
// row is never renumbered. The result is the surviving original indices in
// order.
//
// A strip joins corresponding columns with the quad (a, b, c, d) split into
// (a, b, c) and (a, c, d). Both strips are affine in the common plane of
// (original row index, column index) inside each triangle, so the
// difference of the two placed surfaces is affine on every overlap polygon
// and its largest norm is at a polygon vertex. The vertices are the corners
// of the reference rows and the points where the trial's diagonal crosses a
// reference row line or a reference diagonal. The positions are float64
// values, taken as the exact rationals they are, and every vertex, distance
// and comparison is exact, so the answer does not depend on rounding or on the
// machine. The wrap column of a full turn is unwrapped: its parameter is n.
func LoftKeepRows(grid [][]mesh.Vec3, wrap bool, maxError float64) []int {
	k := len(grid)
	keep := make([]int, k)
	for i := range keep {
		keep[i] = i
	}
	if k <= 2 || !(maxError > 0) {
		return keep
	}
	lim := new(big.Rat)
	lim.SetFloat64(maxError)
	lim.Mul(lim, lim)
	rat := make([][][3]*big.Rat, k)
	for i, r := range grid {
		rat[i] = make([][3]*big.Rat, len(r))
		for j, p := range r {
			for c := 0; c < 3; c++ {
				x := new(big.Rat)
				x.SetFloat64(p[c])
				rat[i][j][c] = x
			}
		}
	}
	cols := len(grid[0])
	cells := cols
	if !wrap {
		cells = cols - 1
	}
	memo := map[[2]int]*big.Rat{} // lookup only
	band := func(p, q int) *big.Rat {
		if q == p+1 {
			return new(big.Rat)
		}
		key := [2]int{p, q}
		if v, ok := memo[key]; ok {
			return v
		}
		worst := new(big.Rat)
		for j := 0; j < cells; j++ {
			if d := cellError(rat, p, q, j, cols); d.Cmp(worst) > 0 {
				worst = d
			}
		}
		memo[key] = worst
		return worst
	}
	for {
		bestIdx := -1
		var bestScore *big.Rat
		for ix := 1; ix+1 < len(keep); ix++ {
			// Trial: delete keep[ix]. Only the merged band changes; every other
			// band keeps its own error against the original strip.
			score := band(keep[ix-1], keep[ix+1])
			for b := 0; b+1 < len(keep); b++ {
				if b == ix-1 || b == ix {
					continue
				}
				if e := band(keep[b], keep[b+1]); e.Cmp(score) > 0 {
					score = e
				}
			}
			if bestScore == nil || score.Cmp(bestScore) < 0 {
				bestIdx, bestScore = ix, score
			}
		}
		if bestIdx < 0 || bestScore.Cmp(lim) > 0 {
			return keep
		}
		keep = append(keep[:bestIdx:bestIdx], keep[bestIdx+1:]...)
	}
}

type ratPt = [3]*big.Rat

// cellError is the largest squared distance between the trial band that joins
// original rows p and q (p < q) and the reference rows from p to q, inside
// column cell j.
func cellError(rat [][][3]*big.Rat, p, q, j, cols int) *big.Rat {
	j1 := (j + 1) % cols
	pos := func(r, col int) ratPt { return rat[r][col] }
	colOf := func(c int) int { // column index of parameter coordinate j or j+1
		if c == 0 {
			return j
		}
		return j1
	}
	one := big.NewRat(1, 1)
	// Trial: parameter (s, t) in the unit cell, s across, t along the band.
	// Triangle (a,b,c) holds s >= t; triangle (a,c,d) holds s <= t.
	a, b, c, d := pos(p, colOf(0)), pos(p, colOf(1)), pos(q, colOf(1)), pos(q, colOf(0))
	trial := func(s, t *big.Rat) ratPt {
		if s.Cmp(t) >= 0 {
			// a + s*(b-a) + t*(c-b)
			return affine(a, b, c, s, t)
		}
		// a + s*(c-d) + t*(d-a)
		return affine(a, d, c, t, s)
	}
	// Reference: row r in [p, q), local parameter (s, w) in the unit quad.
	ref := func(s, v *big.Rat) ratPt {
		// v is the row coordinate in [p, q]; find the sub-quad.
		r := new(big.Int).Div(v.Num(), v.Denom()).Int64()
		if int(r) >= q {
			r = int64(q - 1)
		}
		w := new(big.Rat).Sub(v, big.NewRat(r, 1))
		ra, rb, rc, rd := pos(int(r), colOf(0)), pos(int(r), colOf(1)), pos(int(r)+1, colOf(1)), pos(int(r)+1, colOf(0))
		if s.Cmp(w) >= 0 {
			return affine(ra, rb, rc, s, w)
		}
		return affine(ra, rd, rc, w, s)
	}
	span := big.NewRat(int64(q-p), 1)
	worst := new(big.Rat)
	test := func(s, v *big.Rat) {
		// v is the row coordinate in [p, q]; the trial's t is (v-p)/(q-p).
		t := new(big.Rat).Sub(v, big.NewRat(int64(p), 1))
		t.Quo(t, span)
		x, y := trial(s, t), ref(s, v)
		sum := new(big.Rat)
		for k := 0; k < 3; k++ {
			dd := new(big.Rat).Sub(x[k], y[k])
			sum.Add(sum, dd.Mul(dd, dd))
		}
		if sum.Cmp(worst) > 0 {
			worst = sum
		}
	}
	zero := new(big.Rat)
	for r := p; r <= q; r++ {
		v := big.NewRat(int64(r), 1)
		test(zero, v)
		test(one, v)
	}
	// The trial diagonal s = t = (v-p)/(q-p) against each reference row line
	// v = r and each reference diagonal s = v - r.
	for r := p + 1; r < q; r++ {
		v := big.NewRat(int64(r), 1)
		s := new(big.Rat).Sub(v, big.NewRat(int64(p), 1))
		s.Quo(s, span)
		test(s, v)
	}
	if q-p > 1 {
		for r := p; r < q; r++ {
			// (v-p)/(q-p) = v - r  =>  v = (r*(q-p) - p) / (q-p-1)
			num := big.NewRat(int64(r*(q-p)-p), int64(q-p-1))
			if num.Cmp(big.NewRat(int64(r), 1)) < 0 || num.Cmp(big.NewRat(int64(r+1), 1)) > 0 {
				continue
			}
			s := new(big.Rat).Sub(num, big.NewRat(int64(r), 1))
			test(s, num)
		}
	}
	return worst
}

// affine returns o + s*(u-o) + t*(w-u): the point at parameter (s, t) of the
// triangle whose corners are o at (0,0), u at (1,0) and w at (1,1).
func affine(o, u, w ratPt, s, t *big.Rat) ratPt {
	var out ratPt
	for k := 0; k < 3; k++ {
		du := new(big.Rat).Sub(u[k], o[k])
		dw := new(big.Rat).Sub(w[k], u[k])
		x := new(big.Rat).Mul(s, du)
		y := new(big.Rat).Mul(t, dw)
		out[k] = new(big.Rat).Add(new(big.Rat).Add(o[k], x), y)
	}
	return out
}
