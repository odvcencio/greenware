package compiler

import (
	"errors"
	"fmt"

	"m31labs.dev/greenware/resolve"
	"m31labs.dev/greenware/units"
)

// reflectInputs returns the node as built on the far side of a mirror plane
// without reflecting any mesh: every position written in it (a loft's ring
// centres, an ellipsoid's centre, a tube's points, an array's step, a part's
// `at`) is reflected, and the shape is built again from them. A ring stack
// built this way has the columns, triangles and texture coordinates of a
// shape authored on that side, not those of a reflected copy (whose columns
// run the other way round and whose bands split along the other diagonal).
// It supports the shapes that are symmetric about their own centre under the
// reflection: lofts with the default columns and no vertex rule, ellipsoids,
// tubes, linear arrays of them, and parts that move them.
func reflectInputs(n *resolve.Node, ax int) (*resolve.Node, error) {
	cp := *n
	cp.Fields = append([]resolve.Field(nil), n.Fields...)
	cp.Rows = nil
	flipField := func(name string) {
		for i, f := range cp.Fields {
			if f.Name == name && f.Value.Valid() && len(f.Value.Elems) == 3 {
				v := f.Value
				v.Elems = append([]units.Value(nil), f.Value.Elems...)
				v.Elems[ax].Num = -v.Elems[ax].Num
				cp.Fields[i].Value = v
			}
		}
	}
	if f, ok := n.Get("rot"); ok && f.Valid() && !isTuple(n, "rot", 0) {
		return nil, fmt.Errorf("`reflect = inputs` cannot reflect `%s`, which is turned by `rot`", nameOr(n))
	}
	switch n.Kind {
	case "loft":
		if n.Loft != nil {
			return nil, fmt.Errorf("`reflect = inputs` cannot reflect the loft `%s`, which has a vertex rule", nameOr(n))
		}
		if v, ok := n.Get("arc"); ok && len(v.Elems) == 2 && !(v.Elems[0].Num == 0 && v.Elems[1].Num == units.TwoPi) {
			return nil, fmt.Errorf("`reflect = inputs` cannot reflect the open loft `%s`", nameOr(n))
		}
		idx := -1
		for i, c := range loftColumns(n) {
			if c == string("xyz"[ax]) {
				idx = i
			}
		}
		flipField("at")
		for _, r := range n.Rows {
			vals := append([]units.Value(nil), r.Values...)
			if idx >= 0 {
				vals[idx].Num = -vals[idx].Num
			}
			cp.Rows = append(cp.Rows, resolve.Row{Name: r.Name, Values: vals})
		}
	case "ellipsoid":
		flipField("centre")
		flipField("at")
	case "tube":
		flipField("at")
		for _, r := range n.Rows {
			vals := append([]units.Value(nil), r.Values...)
			vals[ax].Num = -vals[ax].Num
			cp.Rows = append(cp.Rows, resolve.Row{Name: r.Name, Values: vals})
		}
	case "array":
		if m, ok := n.Get("mode"); ok && m.Str != "linear" {
			return nil, errors.New("`reflect = inputs` reflects only linear arrays")
		}
		flipField("step")
	case "part":
		flipField("at")
	default:
		return nil, fmt.Errorf("`reflect = inputs` cannot reflect a `%s`", n.Kind)
	}
	cp.Children = nil
	for _, c := range n.Children {
		rc, err := reflectInputs(c, ax)
		if err != nil {
			return nil, err
		}
		cp.Children = append(cp.Children, rc)
	}
	cp.Copies = nil
	for _, body := range n.Copies {
		var rb []*resolve.Node
		for _, c := range body {
			rc, err := reflectInputs(c, ax)
			if err != nil {
				return nil, err
			}
			rb = append(rb, rc)
		}
		cp.Copies = append(cp.Copies, rb)
	}
	return &cp, nil
}
