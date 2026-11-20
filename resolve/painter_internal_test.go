package resolve

import (
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/kernel/image"
	"m31labs.dev/greenware/lang"
)

// The static operation count of a painter is an upper bound of the operations
// one texel executes, and equal to it where the painter has no `if`. The cost
// class for painting is charged from the static count before any texel runs.
func TestStaticOpsBoundExecutedOps(t *testing.T) {
	src := "greenware 1\nasset a { root = a }\nimport \"std/finish\"\n" +
		"painter plain() { let a = 0.5 * u + v color = #ffffff * a - 0.1 }\n" +
		"painter branchy() { let a = if px == 0 then 1 + 2 * 3 else 0 color = #ffffff * a }\n" +
		"painter nested() { color = plain() * 0.5 }\n" +
		"texture t = atlas { grid = (3, 2) cell_size = 8px inset = 10%\n" +
		"  cell a { paint = plain() } cell b { paint = branchy() } cell c { paint = nested() }\n" +
		"  cell d { paint = finish.gloss() } cell e { paint = finish.satin() } cell f { paint = finish.foam() } }\n" +
		"part a { }\n"
	f, ds := ast.Lower(lang.Parse([]byte(src)))
	r, more := Evaluate(f, nil)
	if diag.HasErrors(append(ds, more...)) {
		t.Fatal(ds, more)
	}
	a := r.Atlases[0]
	lo, hi, _ := a.Layout.Interior()
	exact := map[string]bool{"a": true, "c": true, "d": true, "e": false, "f": true}
	for _, c := range a.Cells {
		pc := c.Paint
		var most, total int64
		n := int64(0)
		for py := lo; py <= hi; py++ {
			for px := lo; px <= hi; px++ {
				env := &penv{tex: image.Texel{Cell: c.Index, PX: px, PY: py, U: a.Layout.TexelUV(px), V: a.Layout.TexelUV(py)}}
				pc.Painter.call(env, pc.Args)
				if env.err != nil {
					t.Fatal(env.err)
				}
				most = max(most, env.ops)
				total += env.ops
				n++
			}
		}
		// pc.Ops counts the painter's own nodes; a call adds one for itself.
		if most > int64(pc.Ops) {
			t.Errorf("cell %s: %d operations executed, static count %d", c.Name, most, pc.Ops)
		}
		if exact[c.Name] && most != int64(pc.Ops) {
			t.Errorf("cell %s has no branch, so %d executed should equal the static %d", c.Name, most, pc.Ops)
		}
		_ = total
	}
}
