package rig

import (
	"errors"
	"testing"
)

// A loft's ring at height 1 and 2, centres leaning in x: the girth of the
// ring is taken about the centre at the position's own height.
func TestGirthWidensAboutTheRingCentre(t *testing.T) {
	line := []Vec{{0, 0, 0}, {0.1, 1, 0}, {0.2, 2, 0}}
	pos := []Vec{
		{0.1 + 0.5, 1, 0.25},  // behind the centre
		{0.1 - 0.5, 1, -0.25}, // in front of it
		{0.2 + 0.5, 2, 0},     // exactly on the centre's z: back
		{9, 1, 9},             // another shape
	}
	shape := []int{0, 0, 0, 1}
	at := func(y float64) (float64, float64, float64, error) { return 0.1 * y, 0.2 * y, 0.4 * y, nil }
	if err := Girth(pos, shape, []int{0}, [][]Vec{line, nil}, at); err != nil {
		t.Fatal(err)
	}
	want := []Vec{
		{0.1 + 0.5*1.1, 1, 0.25 * 1.4},
		{0.1 - 0.5*1.1, 1, -0.25 * 1.2},
		{0.2 + 0.5*1.2, 2, 0},
		{9, 1, 9},
	}
	for i := range want {
		if !near(pos[i], want[i], 1e-12) {
			t.Errorf("position %d = %v, want %v", i, pos[i], want[i])
		}
	}
}

func TestGirthKeepsHeightAndLeavesUnselectedAndLinelessShapes(t *testing.T) {
	line := []Vec{{0, 0, 0}, {0, 1, 0}}
	pos := []Vec{{1, 0.5, 1}, {1, 0.5, 1}, {1, 0.5, 1}}
	shape := []int{0, 1, 2}
	at := func(float64) (float64, float64, float64, error) { return 1, 1, 1, nil }
	// shape 0 selected with a line, shape 1 unselected, shape 2 selected without a line.
	if err := Girth(pos, shape, []int{0, 2}, [][]Vec{line, line, nil}, at); err != nil {
		t.Fatal(err)
	}
	if !near(pos[0], Vec{2, 0.5, 2}, 1e-12) || !near(pos[1], Vec{1, 0.5, 1}, 0) || !near(pos[2], Vec{1, 0.5, 1}, 0) {
		t.Errorf("positions %v", pos)
	}
}

func TestGirthStopsOnAnError(t *testing.T) {
	boom := errors.New("no amount here")
	at := func(float64) (float64, float64, float64, error) { return 0, 0, 0, boom }
	err := Girth([]Vec{{1, 1, 1}}, []int{0}, []int{0}, [][]Vec{{{0, 0, 0}, {0, 2, 0}}}, at)
	if !errors.Is(err, boom) {
		t.Errorf("error %v", err)
	}
}

func TestCentreAtHeight(t *testing.T) {
	line := []Vec{{0, 0, 0}, {2, 1, 4}, {2, 1, 4}, {4, 3, 0}}
	for _, c := range []struct {
		y    float64
		want Vec
	}{
		{0, Vec{0, 0, 0}},
		{0.5, Vec{1, 0.5, 2}},
		{1, Vec{2, 1, 4}},
		{2, Vec{3, 2, 2}}, // a level segment (1 to 1) is skipped
		{-1, Vec{0, 0, 0}},
		{5, Vec{4, 3, 0}},
	} {
		if got := CentreAtHeight(line, c.y); !near(got, c.want, 1e-12) {
			t.Errorf("height %v: %v, want %v", c.y, got, c.want)
		}
	}
	if got := CentreAtHeight([]Vec{{1, 2, 3}}, 9); got != (Vec{1, 2, 3}) {
		t.Errorf("a one-point line: %v", got)
	}
}
