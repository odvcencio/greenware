package rig

import (
	"fmt"
	"math"

	"m31labs.dev/greenware/kernel/mesh"
)

// A morph is built on the finished bind mesh. Each operation here moves a
// copy of the bind positions at weight 1, in the order the morph's body
// writes them; the target delta is then the difference from the bind
// positions. Operations never read another morph's result.

// Stretch adds by*clamp(s-from, 0, to-from) to the coordinate s along axis of
// every position, so what lies past `to` moves by by*(to-from) and the
// interval between stretches. It returns an error unless from < to.
func Stretch(pos []Vec, axis int, from, to, by float64) error {
	if !(from < to) {
		return fmt.Errorf("stretch needs `from` below `to`, found %v and %v", from, to)
	}
	for i := range pos {
		pos[i][axis] += StretchShift(pos[i][axis], from, to, by)
	}
	return nil
}

// StretchShift is the displacement the stretch gives the coordinate s.
func StretchShift(s, from, to, by float64) float64 {
	t := s - from
	if t < 0 {
		t = 0
	}
	if t > to-from {
		t = to - from
	}
	return float64(by * t)
}

// Reshape scales the positions of the selected shapes about a point,
// componentwise.
func Reshape(pos []Vec, shape []int, parts []int, about, scale Vec) {
	sel := selection(parts)
	for i, p := range pos {
		if !sel[shape[i]] {
			continue
		}
		d := mesh.Sub(p, about)
		pos[i] = Vec{
			about[0] + float64(d[0]*scale[0]),
			about[1] + float64(d[1]*scale[1]),
			about[2] + float64(d[2]*scale[2]),
		}
	}
}

// Translate moves the positions of the selected shapes by by.
func Translate(pos []Vec, shape []int, parts []int, by Vec) {
	sel := selection(parts)
	for i := range pos {
		if sel[shape[i]] {
			pos[i] = mesh.Add(pos[i], by)
		}
	}
}

// Inflate grows the selected shapes about their centre line: each position
// moves from its nearest point on the line to (1+by) times its distance.
// lines holds the centre line of each shape id, as a polyline of one or more
// points; a shape without a line (a nil entry) is left alone.
func Inflate(pos []Vec, shape []int, parts []int, lines [][]Vec, by float64) {
	sel := selection(parts)
	for i, p := range pos {
		if !sel[shape[i]] || shape[i] >= len(lines) || len(lines[shape[i]]) == 0 {
			continue
		}
		c := NearestOnLine(lines[shape[i]], p)
		pos[i] = mesh.Add(c, mesh.Scale(mesh.Sub(p, c), 1+by))
	}
}

func selection(parts []int) map[int]bool {
	sel := map[int]bool{} // lookup only
	for _, p := range parts {
		sel[p] = true
	}
	return sel
}

// NearestOnLine returns the point of the polyline closest to p. With equal
// distances the earlier segment wins.
func NearestOnLine(line []Vec, p Vec) Vec {
	if len(line) == 1 {
		return line[0]
	}
	best, bestD := line[0], math.Inf(1)
	for k := 0; k+1 < len(line); k++ {
		a, b := line[k], line[k+1]
		ab := mesh.Sub(b, a)
		den := mesh.Dot(ab, ab)
		t := 0.0
		if den > 0 {
			t = clamp01(mesh.Dot(mesh.Sub(p, a), ab) / den)
		}
		c := mesh.Add(a, mesh.Scale(ab, t))
		d := mesh.Sub(p, c)
		if dd := mesh.Dot(d, d); dd < bestD {
			best, bestD = c, dd
		}
	}
	return best
}

// Deltas returns target-base, position by position.
func Deltas(base, target []Vec) []Vec {
	out := make([]Vec, len(base))
	for i := range base {
		out[i] = mesh.Sub(target[i], base[i])
	}
	return out
}

// Follow computes the parent-local rest offsets a morph that moves space by
// move gives the joints (`joints = follow`). Each bind point goes through
// move; the deformed hierarchy keeps every bind rotation, and the offset of a
// joint is the difference of its local translation, not of its world
// position. A rigid shift shared by a whole subtree therefore moves only the
// subtree's root. Joints whose offset is zero are left out. The result is in
// joint order.
func Follow(sk *Skeleton, move func(Vec) Vec, morph int) []Offset {
	newPos := make([]Vec, len(sk.Joints))
	for i, j := range sk.Joints {
		newPos[i] = move(j.Pos)
	}
	var out []Offset
	for i, j := range sk.Joints {
		var d Vec
		if j.Parent == -1 {
			d = mesh.Sub(newPos[i], j.Pos)
		} else {
			p := sk.Joints[j.Parent]
			was := mesh.Sub(j.Pos, p.Pos)
			now := mesh.Sub(newPos[i], newPos[j.Parent])
			d = p.Rot.Conj().Rotate(mesh.Sub(now, was))
		}
		if d != (Vec{}) {
			out = append(out, Offset{Joint: i, Morph: morph, Delta: d})
		}
	}
	return out
}
