// Package accept runs the owner's acceptance documents on decoded GLB files.
//
// An acceptance file (`accept name { ... expect (condition, "message") ... }`,
// language reference section 6.7, spec section 2.7) states what must be true
// of the delivered asset. This package evaluates its expressions against the
// decoded glTF of each named output at each level of detail. It measures the
// decoded triangles and node transforms, never the author's anchors or the
// compiler's intermediate data, and it imports neither the compiler nor the
// conformance checker.
//
// The measurement rules follow section 6.7:
//
//   - Positions are in the model frame after node transforms, rounded to
//     binary32. Vertices weld only on exactly equal positions.
//   - gap, closed, connected, open_edges and drawn_triangles read the node's
//     own visible triangles, not its children's. bounds(node) reads the
//     subtree. bounds() and draw_calls() read the whole active scene.
//   - A socket is the meshless node named "socket.<name>".
//   - outline needs reference masks that do not exist yet; it reports
//     Unavailable and never passes.
package accept

import (
	"fmt"
	"sort"
	"strings"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/units"
)

// Key names one decoded product: an output and a level of detail.
type Key struct {
	Output string
	LOD    int
}

// Product is one decoded GLB with the facts the compiled source supplies.
type Product struct {
	// Doc is the decoded file. It is nil when Problem is set.
	Doc *gltf.Document
	// File is the file name, for the report.
	File string
	// Problem says why Doc is missing: the file was absent or did not decode.
	Problem string
	// LODError is the level's lod_error in metres: max_error for a level above
	// 0, the largest chord error of the level-0 shapes at level 0.
	LODError float64
	// LODErrorUnknown is true when the caller could not supply LODError. An
	// expect that reads lod_error is then refused; the others still run.
	LODErrorUnknown bool
	// Distance is the level's switch distance in metres, for the report.
	Distance float64
}

// Inputs maps each (output, lod) to its decoded product.
type Inputs map[Key]Product

// Run evaluates the acceptance document on every selected output and level and
// returns the report with one error diagnostic per expect that failed or was
// refused, and one warning per expect that was unavailable. Diagnostic spans
// are byte offsets in the acceptance source.
func Run(acc *ast.Accept, inputs Inputs) (*Report, []diag.Diagnostic) {
	return RunWith(acc, inputs, nil)
}

// RunWith is Run for an acceptance file that imports data packages. data comes
// from LoadData and has passed Check; nil means the file imports none. The
// report lists the data inputs.
func RunWith(acc *ast.Accept, inputs Inputs, data *Data) (*Report, []diag.Diagnostic) {
	rep := &Report{Schema: Schema, Accept: acc.Name.Name}
	if data != nil {
		rep.Inputs = data.Inputs
	}
	var ds []diag.Diagnostic
	if s := ast.FindSetting(acc.Settings, "of"); s != nil {
		if lit, ok := s.Value.(*ast.StringLit); ok {
			rep.Of = lit.Value
		}
	}
	outputs := selectOutputs(acc, inputs)
	for _, out := range outputs {
		for _, lod := range selectLods(acc, inputs, out) {
			prod, ok := inputs[Key{out, lod}]
			lr := LevelReport{Output: out, LOD: lod, File: prod.File, Expects: []ExpectReport{}}
			if ok && prod.Doc != nil {
				if !prod.LODErrorUnknown {
					lr.LODError = jsonNum(prod.LODError)
				}
				if prod.Distance > 0 {
					lr.Distance = jsonNum(prod.Distance)
				}
				sc := newScene(prod.Doc)
				ev := &evaluator{sc: sc, output: out, lod: lod, lodErr: prod.LODError, lodKnown: !prod.LODErrorUnknown, d: units.DefaultUnits(), data: data}
				walkItems(acc.Items, ev, &lr, &ds)
			} else {
				lr.Problem = prod.Problem
				if !ok {
					lr.Problem = "no decoded file was supplied for this output and level"
				}
				if lr.Problem == "" {
					lr.Problem = "the decoded file is missing"
				}
				ds = append(ds, newDiag(diag.Error, Span{acc.Span.Start, acc.Span.End},
					fmt.Sprintf("output %s lod %d: %s", out, lod, lr.Problem)))
			}
			rep.Levels = append(rep.Levels, lr)
		}
	}
	rep.tally()
	return rep, ds
}

func walkItems(items []ast.AcceptItem, ev *evaluator, lr *LevelReport, ds *[]diag.Diagnostic) {
	for _, it := range items {
		switch x := it.(type) {
		case *ast.Expect:
			er := runExpect(x, ev)
			lr.Expects = append(lr.Expects, er)
			if d, ok := expectDiag(lr, er); ok {
				*ds = append(*ds, d)
			}
		case *ast.AcceptWhen:
			ev.measures = nil
			v, err := ev.eval(x.Cond)
			switch {
			case err != nil:
				er := ExpectReport{Span: spanOf(x.Span), Message: "when " + exprString(x.Cond), Verdict: Refused, Detail: err.msg, Measures: []Measure{}}
				if err.unavailable {
					er.Verdict = Unavailable
				}
				lr.Expects = append(lr.Expects, er)
				if d, ok := expectDiag(lr, er); ok {
					*ds = append(*ds, d)
				}
			case v.u.Kind != units.KSwitch:
				er := ExpectReport{Span: spanOf(x.Span), Message: "when " + exprString(x.Cond), Verdict: Refused,
					Detail: "the `when` condition is a " + describe(v) + ", not a switch", Measures: []Measure{}}
				lr.Expects = append(lr.Expects, er)
				if d, ok := expectDiag(lr, er); ok {
					*ds = append(*ds, d)
				}
			case v.u.Bool:
				walkItems(x.Then, ev, lr, ds)
			default:
				walkItems(x.Else, ev, lr, ds)
			}
		}
	}
}

func spanOf(s diag.Span) Span { return Span{s.Start, s.End} }

func runExpect(x *ast.Expect, ev *evaluator) ExpectReport {
	ev.measures = nil
	ev.dataUsed = false
	er := ExpectReport{Span: spanOf(x.Span), Measures: []Measure{}}
	switch m := x.Message.(type) {
	case *ast.StringLit:
		er.Message = m.Value
	default:
		er.Message = exprString(x.Message)
	}
	ok, err := ev.condition(x.Cond)
	switch {
	case err != nil && err.unavailable:
		er.Verdict, er.Detail = Unavailable, err.msg
	case err != nil:
		er.Verdict, er.Detail, er.Code = Refused, err.msg, err.code
	case ok:
		er.Verdict = Pass
	default:
		er.Verdict = Fail
	}
	if len(ev.measures) > 0 {
		er.Measures = ev.measures
	}
	return er
}

// expectDiag builds the diagnostic for a verdict other than pass.
func expectDiag(lr *LevelReport, er ExpectReport) (diag.Diagnostic, bool) {
	var b strings.Builder
	fmt.Fprintf(&b, "output %s lod %d: %s", lr.Output, lr.LOD, er.Message)
	switch er.Verdict {
	case Pass:
		return diag.Diagnostic{}, false
	case Fail:
		b.WriteString(": expectation failed")
	case Refused:
		b.WriteString(": measurement refused: " + er.Detail)
	case Unavailable:
		b.WriteString(": not measured: " + er.Detail)
	}
	if len(er.Measures) > 0 {
		parts := make([]string, len(er.Measures))
		for i, m := range er.Measures {
			parts[i] = m.Name + " is " + m.Value
		}
		b.WriteString(" (" + strings.Join(parts, "; ") + ")")
	}
	sev := diag.Error
	if er.Verdict == Unavailable {
		sev = diag.Warning
	}
	if er.Code != "" {
		return newCodeDiag(er.Code, sev, er.Span, b.String()), true
	}
	return newDiag(sev, er.Span, b.String()), true
}

// selectOutputs returns the output names to evaluate, in the order the file
// lists them. Without an `outputs` setting (or with `all`) it is every output
// present in the inputs, sorted.
func selectOutputs(acc *ast.Accept, inputs Inputs) []string {
	if s := ast.FindSetting(acc.Settings, "outputs"); s != nil {
		var names []string
		switch v := s.Value.(type) {
		case *ast.List:
			for _, el := range v.Elems {
				if n, ok := identifier(el); ok {
					names = append(names, n)
				}
			}
			return dedupe(names)
		case *ast.Name:
			if v.Name != "all" {
				return []string{v.Name}
			}
		case *ast.StringLit:
			return []string{v.Value}
		}
	}
	seen := map[string]bool{}
	var out []string
	for k := range inputs {
		if !seen[k.Output] {
			seen[k.Output] = true
			out = append(out, k.Output)
		}
	}
	sort.Strings(out)
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// selectLods returns the levels to evaluate for an output, ascending. `all`
// (the default) is every level in the inputs for that output; if there is
// none, level 0, so the missing file is reported.
func selectLods(acc *ast.Accept, inputs Inputs, output string) []int {
	all := func() []int {
		var out []int
		for k := range inputs {
			if k.Output == output {
				out = append(out, k.LOD)
			}
		}
		sort.Ints(out)
		if len(out) == 0 {
			out = []int{0}
		}
		return out
	}
	s := ast.FindSetting(acc.Settings, "lods")
	if s == nil {
		return all()
	}
	var out []int
	num := func(e ast.Expr) {
		if n, ok := e.(*ast.NumberLit); ok && n.Value.Num == float64(int(n.Value.Num)) {
			out = append(out, int(n.Value.Num))
		}
	}
	switch v := s.Value.(type) {
	case *ast.Name:
		if v.Name == "all" {
			return all()
		}
	case *ast.List:
		for _, el := range v.Elems {
			num(el)
		}
	case *ast.NumberLit:
		num(v)
	}
	if len(out) == 0 {
		return all()
	}
	sort.Ints(out)
	res := out[:1]
	for _, l := range out[1:] {
		if l != res[len(res)-1] {
			res = append(res, l)
		}
	}
	return res
}
