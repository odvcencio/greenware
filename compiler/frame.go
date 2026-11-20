package compiler

import (
	"m31labs.dev/greenware/kernel/mesh"
	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

// This file places a part, an instance or a shape by `at`, `rot`, `scale` and
// `pivot` (language.md sections 2.4, 3.3 and 3.4). The rotation is the
// intrinsic X, Y, Z turn of mesh.EulerXYZ, for column vectors Rx * Ry * Rz.
//
// A part is placed by T(at) * T(pivot) * R(rot) * S(scale) * T(-pivot). An
// instance has no pivot. A shape is placed by T(at) * R(rot): it is built
// about its own origin, turned, then moved, so `at` is never turned with it.
// When no field of the frame is set, every path here leaves the frame exactly
// as the translation-only code did, so existing files keep their bytes.

// localFrame returns the placement a node's own fields describe, and whether
// it has a rotation or a scale. pivot is read only when usePivot is set.
func localFrame(n *resolve.Node, usePivot bool) (a mesh.Affine, turned bool) {
	at := vec3(n, "at")
	var rot mesh.Vec3
	if v, ok := n.Get("rot"); ok {
		if r, ok := v.Vec3(); ok {
			rot = mesh.Vec3(r)
		}
	}
	scale := mesh.Vec3{1, 1, 1}
	if v, ok := n.Get("scale"); ok {
		if s, ok := v.Vec3(); ok {
			scale = mesh.Vec3(s)
		}
	}
	if rot == (mesh.Vec3{}) && scale == (mesh.Vec3{1, 1, 1}) {
		return mesh.Affine{M: mesh.Identity3, T: at}, false
	}
	m := mesh.EulerXYZ(rot[0], rot[1], rot[2])
	// M = R * S: scale each column of R.
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			m[i][j] = float64(m[i][j] * scale[j])
		}
	}
	var pivot mesh.Vec3
	if usePivot {
		pivot = vec3(n, "pivot")
	}
	// T = at + pivot - M*pivot, so the pivot stays where it is before `at`.
	t := mesh.Add(at, mesh.Sub(pivot, m.MulVec(pivot)))
	return mesh.Affine{M: m, T: t}, true
}

// placeNode returns the frame inside c.a at the node's own placement. A node
// with no rotation and no scale is placed by translation alone, as before.
func placeNode(c mesh.Affine, n *resolve.Node, usePivot bool) mesh.Affine {
	l, turned := localFrame(n, usePivot)
	if !turned {
		return placeAt(c, l.T)
	}
	return l.Then(c)
}

// shapeNode walks a shape. A shape with a `rot` is built about its own origin
// from a copy of its node whose `at` and `rot` are the identity, and the frame
// carries the turn and the translation, so every builder, the UV projection
// and the rig's centre line see the shape unturned and the mesh is moved once.
func (e *elab) shapeNode(n *resolve.Node, c wctx) {
	if _, ok := n.Get("rot"); !ok || isTuple(n, "rot", 0) {
		e.shape(n, c)
		return
	}
	l, _ := localFrame(n, false)
	c.a = l.Then(c.a)
	cp := *n
	cp.Fields = make([]resolve.Field, len(n.Fields))
	copy(cp.Fields, n.Fields)
	for i, f := range cp.Fields {
		switch f.Name {
		case "at":
			cp.Fields[i].Value = units.Tuple(units.Len(0), units.Len(0), units.Len(0))
		case "rot":
			cp.Fields[i].Value = units.Tuple(units.Ang(0), units.Ang(0), units.Ang(0))
		}
	}
	e.shape(&cp, c)
}
