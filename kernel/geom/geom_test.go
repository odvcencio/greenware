package geom

import (
	"math"
	"math/rand"
	"testing"
)

func tri(a, b, c Vec) Tri { return Tri{a, b, c} }

func TestTriDistanceKnownValues(t *testing.T) {
	base := tri(Vec{0, 0, 0}, Vec{1, 0, 0}, Vec{0, 1, 0})
	cases := []struct {
		name string
		b    Tri
		want float64
	}{
		{"parallel above", tri(Vec{0, 0, 2}, Vec{1, 0, 2}, Vec{0, 1, 2}), 2},
		{"corner to face", tri(Vec{0.25, 0.25, 3}, Vec{1, 1, 3}, Vec{0, 1, 3}), 3},
		{"edge to edge", tri(Vec{2, 0, 0}, Vec{3, 1, 0}, Vec{2, 1, 0}), 1},
		{"piercing", tri(Vec{0.2, 0.2, -1}, Vec{0.2, 0.2, 1}, Vec{0.4, 0.2, 1}), 0},
		{"shared edge", tri(Vec{0, 0, 0}, Vec{1, 0, 0}, Vec{0, -1, 0}), 0},
		{"skew edges", tri(Vec{0.5, 0.5, 1}, Vec{2, 0.5, 1}, Vec{0.5, 2, 1.5}), 1},
	}
	for _, c := range cases {
		if got := TriDistance(base, c.b); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
		if got := TriDistance(c.b, base); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("%s reversed: %v, want %v", c.name, got, c.want)
		}
	}
}

func soup(r *rand.Rand, n int, off Vec) []Tri {
	out := make([]Tri, n)
	for i := range out {
		c := Vec{r.Float64() + off[0], r.Float64() + off[1], r.Float64() + off[2]}
		var t Tri
		for k := range t {
			t[k] = Vec{c[0] + 0.1*r.Float64(), c[1] + 0.1*r.Float64(), c[2] + 0.1*r.Float64()}
		}
		out[i] = t
	}
	return out
}

// The tree finds the same least distance as the quadratic search.
func TestMinDistanceMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		a := soup(r, 60, Vec{})
		b := soup(r, 70, Vec{1.2, 0.3, 0.1})
		best := math.Inf(1)
		for _, x := range a {
			for _, y := range b {
				best = math.Min(best, TriDistance(x, y))
			}
		}
		got := MinDistance(NewBVH(a), NewBVH(b), 10)
		if !got.Found || math.Abs(got.Distance-best) > 1e-12 {
			t.Fatalf("trial %d: tree %v, brute force %v", trial, got, best)
		}
		if d := TriDistance(a[got.A], b[got.B]); math.Abs(d-got.Distance) > 1e-12 {
			t.Fatalf("the reported pair is not the pair at the distance")
		}
		// A limit at the answer finds nothing: the search is strict.
		if r := MinDistance(NewBVH(a), NewBVH(b), best); r.Found {
			t.Fatalf("a pair at exactly the limit was found")
		}
	}
}

func TestMinDistanceEmptyAndZero(t *testing.T) {
	a := []Tri{tri(Vec{0, 0, 0}, Vec{1, 0, 0}, Vec{0, 1, 0})}
	if r := MinDistance(NewBVH(nil), NewBVH(a), 1); r.Found {
		t.Error("an empty set has no pair")
	}
	cross := []Tri{tri(Vec{0.2, 0.2, -1}, Vec{0.2, 0.2, 1}, Vec{0.4, 0.2, 1})}
	if r := MinDistance(NewBVH(a), NewBVH(cross), 1); !r.Found || r.Distance != 0 {
		t.Errorf("crossing triangles: %+v", r)
	}
}
