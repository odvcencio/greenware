package fixture

import "math"

// Merge joins primitives into one, re-indexing. Material is taken from the first.
func Merge(ps ...Prim) Prim {
	out := Prim{Material: -1}
	if len(ps) > 0 {
		out.Material = ps[0].Material
	}
	for _, p := range ps {
		base := uint32(len(out.Pos))
		out.Pos = append(out.Pos, p.Pos...)
		for _, i := range p.Idx {
			out.Idx = append(out.Idx, i+base)
		}
	}
	return out
}

// Move returns a copy shifted by (dx,dy,dz), computed in float64 and rounded.
func Move(p Prim, dx, dy, dz float64) Prim {
	q := Prim{Material: p.Material, Idx: append([]uint32(nil), p.Idx...), Wide: p.Wide}
	for _, v := range p.Pos {
		q.Pos = append(q.Pos, [3]float32{float32(float64(v[0]) + dx), float32(float64(v[1]) + dy), float32(float64(v[2]) + dz)})
	}
	return q
}

// Cube is a closed box with 8 shared vertices and outward counter-clockwise faces.
func Cube(cx, cy, cz, hx, hy, hz float64) Prim {
	var pos [][3]float32
	for _, z := range []float64{-1, 1} {
		for _, y := range []float64{-1, 1} {
			for _, x := range []float64{-1, 1} {
				pos = append(pos, [3]float32{float32(cx + x*hx), float32(cy + y*hy), float32(cz + z*hz)})
			}
		}
	}
	// vertex index = x + 2*y + 4*z with 0 meaning -1
	q := func(a, b, c, d uint32) []uint32 { return []uint32{a, c, b, a, d, c} }
	var idx []uint32
	idx = append(idx, q(1, 5, 7, 3)...) // +x
	idx = append(idx, q(0, 2, 6, 4)...) // -x
	idx = append(idx, q(2, 3, 7, 6)...) // +y
	idx = append(idx, q(0, 4, 5, 1)...) // -y
	idx = append(idx, q(4, 6, 7, 5)...) // +z
	idx = append(idx, q(0, 1, 3, 2)...) // -z
	return Prim{Pos: pos, Idx: idx, Material: -1}
}

// SplitCube is the same box with 24 vertices (4 per face), as exporters with
// per-face normals write it.
func SplitCube(cx, cy, cz, hx, hy, hz float64) Prim {
	c := Cube(cx, cy, cz, hx, hy, hz)
	out := Prim{Material: -1}
	for t := 0; t+2 < len(c.Idx); t += 6 {
		// each face is two triangles sharing 4 corners
		for k := 0; k < 6; k++ {
			out.Pos = append(out.Pos, c.Pos[c.Idx[t+k]])
			out.Idx = append(out.Idx, uint32(len(out.Pos)-1))
		}
	}
	return out
}

// ringPoint is shared by Cylinder and Sphere so equal rings are equal in binary32.
func ringPoint(cx, cz, r, theta, y float64) [3]float32 {
	return [3]float32{float32(cx + r*math.Cos(theta)), float32(y), float32(cz + r*math.Sin(theta))}
}

// Cylinder is a Y-axis cylinder of radius r from y0 to y1 with seg sides.
// Ring vertex k is at angle phase + 2*pi*k/seg, so phase 0 puts a vertex on +X.
// The side ring repeats its first vertex (a texture seam) and each cap has its
// own copy of the ring, as real exports do. With caps false it is an open tube.
func Cylinder(cx, cz, r, y0, y1 float64, seg int, phase float64, caps bool) Prim {
	var pos [][3]float32
	ang := func(k int) float64 { return phase + 2*math.Pi*float64(k)/float64(seg) }
	for k := 0; k <= seg; k++ { // bottom side ring, seam copy last
		pos = append(pos, ringPoint(cx, cz, r, ang(k%seg), y0))
	}
	for k := 0; k <= seg; k++ { // top side ring
		pos = append(pos, ringPoint(cx, cz, r, ang(k%seg), y1))
	}
	var idx []uint32
	lo, hi := uint32(0), uint32(seg+1)
	for k := 0; k < seg; k++ {
		a, b := lo+uint32(k), lo+uint32(k+1)
		d, c := hi+uint32(k), hi+uint32(k+1)
		idx = append(idx, a, c, b, a, d, c) // outward
	}
	if caps {
		for _, top := range []bool{false, true} {
			y := y0
			if top {
				y = y1
			}
			ctr := uint32(len(pos))
			pos = append(pos, [3]float32{float32(cx), float32(y), float32(cz)})
			ring := uint32(len(pos))
			for k := 0; k < seg; k++ {
				pos = append(pos, ringPoint(cx, cz, r, ang(k), y))
			}
			for k := 0; k < seg; k++ {
				v0, v1 := ring+uint32(k), ring+uint32((k+1)%seg)
				if top {
					idx = append(idx, ctr, v1, v0)
				} else {
					idx = append(idx, ctr, v0, v1)
				}
			}
		}
	}
	return Prim{Pos: pos, Idx: idx, Material: -1}
}

// Sphere is a UV sphere with lon longitudes and lat latitude intervals. Pole
// vertices are single points. For even lat the equator ring uses the same
// binary32 coordinates as Cylinder with phase 0.
func Sphere(cx, cy, cz, r float64, lon, lat int) Prim {
	var pos [][3]float32
	pos = append(pos, [3]float32{float32(cx), float32(cy + r), float32(cz)}) // north
	for k := 1; k < lat; k++ {
		phi := math.Pi * float64(k) / float64(lat)
		ring, y := r*math.Sin(phi), cy+r*math.Cos(phi)
		if 2*k == lat {
			ring, y = r, cy
		}
		for j := 0; j < lon; j++ {
			pos = append(pos, ringPoint(cx, cz, ring, 2*math.Pi*float64(j)/float64(lon), y))
		}
	}
	pos = append(pos, [3]float32{float32(cx), float32(cy - r), float32(cz)}) // south
	south := uint32(len(pos) - 1)
	at := func(k, j int) uint32 { return uint32(1 + (k-1)*lon + j%lon) }
	var idx []uint32
	for j := 0; j < lon; j++ {
		idx = append(idx, 0, at(1, j+1), at(1, j))
		idx = append(idx, south, at(lat-1, j), at(lat-1, j+1))
	}
	for k := 1; k < lat-1; k++ {
		for j := 0; j < lon; j++ {
			a, b := at(k, j), at(k, j+1)
			d, c := at(k+1, j), at(k+1, j+1)
			idx = append(idx, d, b, c, d, a, b)
		}
	}
	return Prim{Pos: pos, Idx: idx, Material: -1}
}
