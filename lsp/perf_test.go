package lsp

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"m31labs.dev/greenware/lang"
)

// genFixture builds a valid Greenware file of about size bytes: one asset, a
// few parameters and lets, and as many parts with unique names as fit. It is
// deterministic. The parts read the parameters, so the evaluator and the
// index have real work to do.
func genFixture(size int) string {
	var b strings.Builder
	b.WriteString(`greenware 1

asset bench {
  title = "Benchmark"
  root = world
  length = m
  angle = deg
}

param radius_scale : number = 1 in 0.5..2
param height_scale : number = 1 in 0.5..2
param tilt : angle = 10deg in 0deg..45deg
let base_r = 0.05m * radius_scale
let base_h = 0.2m * height_scale

material steel = pbr { base = #8a8d8f roughness = 0.45 metallic = 1 }
material paint = pbr { base = #f2c200 roughness = 0.4 metallic = 0 }

part world {
  material = steel
`)
	for i := 0; b.Len() < size-64; i++ {
		fmt.Fprintf(&b, `
  // Segment %04d: a post with a cap and a mounting socket.
  part segment_%04d {
    at = (%d.0, 0, %d.5)
    rot = (0deg, tilt, 0deg)
    cylinder post_%04d {
      at = (0, base_h / 2, 0)
      radius = base_r
      height = base_h
      segments = 12
    }
    sphere cap_%04d {
      at = (0, base_h, 0)
      radius = base_r * 1.2
      segments = 12
      material = paint
    }
    socket mount_%04d {
      kind = anchor
      at = (0, base_h + base_r, 0)
    }
  }
`, i, i, i%50, i/50, i, i, i)
	}
	b.WriteString("}\n\noutput bench { file = \"bench.glb\" }\n")
	return b.String()
}

func p95(ds []time.Duration) time.Duration {
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)*95/100]
}

func median(ds []time.Duration) time.Duration {
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func TestFixtureIsValid(t *testing.T) {
	src := genFixture(100 << 10)
	if n := len(src); n < 100<<10-200 || n > 100<<10+4000 {
		t.Fatalf("fixture is %d bytes, want about 100 KiB", n)
	}
	d := newDocument("file:///bench.gw", 1, []byte(src))
	if len(d.diags) != 0 {
		t.Fatalf("the fixture has %d diagnostics, first: %v", len(d.diags), d.diags[0])
	}
}

// TestPerformance measures, on a 100 KiB file and 200 runs each: the time of
// a CST edit alone (Tree.Reparse), the time from an incremental didChange to
// the published diagnostics, and a hover on an indexed document. The ceilings
// are loose so a slow machine does not fail; the numbers are logged against
// the plan's targets (edit 20 ms, cached hover 50 ms).
func TestPerformance(t *testing.T) {
	const runs = 200
	src := genFixture(100 << 10)
	at := strings.Index(src, "radius = base_r\n") // inside the first segment's post

	// CST edit alone.
	tree := lang.Parse([]byte(src))
	var cst []time.Duration
	offset := strings.Index(src, "segments = 12") + len("segments = ")
	for i := 0; i < runs; i++ {
		digit := "13"
		if i%2 == 1 {
			digit = "12"
		}
		start := time.Now()
		next, err := tree.Reparse(lang.Edit{Start: offset, End: offset + 2, Text: []byte(digit)})
		cst = append(cst, time.Since(start))
		if err != nil {
			t.Fatal(err)
		}
		tree = next
	}

	// Edit to diagnostics, over the protocol.
	c := startServer(t)
	c.initialize()
	uri := "file:///bench.gw"
	if pub := c.open(uri, src); len(pub.Diagnostics) != 0 {
		t.Fatalf("the fixture has diagnostics: %v", pub.Diagnostics[0])
	}
	d := newDocument(uri, 1, []byte(src))
	pos := d.position(offset)
	var edit []time.Duration
	for i := 0; i < runs; i++ {
		digit := "13"
		if i%2 == 1 {
			digit = "12"
		}
		start := time.Now()
		c.notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": i + 2},
			"contentChanges": []any{map[string]any{"range": Range{Start: pos, End: Position{Line: pos.Line, Character: pos.Character + 2}}, "text": digit}},
		})
		pub := c.diagnostics()
		edit = append(edit, time.Since(start))
		if len(pub.Diagnostics) != 0 {
			t.Fatalf("edit %d: %v", i, pub.Diagnostics[0])
		}
	}

	// Cached hover: the index is built by the first hover and kept.
	hoverAt := d.position(at + len("radius = "))
	hp := textDocumentPosition{TextDocument: textDocumentIdentifier{URI: uri}, Position: hoverAt}
	var res Hover
	c.call("textDocument/hover", hp, &res)
	if !strings.Contains(res.Contents.Value, "base_r") {
		t.Fatalf("hover on base_r said %q", res.Contents.Value)
	}
	var hover []time.Duration
	for i := 0; i < runs; i++ {
		start := time.Now()
		c.call("textDocument/hover", hp, &res)
		hover = append(hover, time.Since(start))
	}

	// Hover right after an edit builds the index first.
	var cold []time.Duration
	for i := 0; i < 50; i++ {
		digit := "13"
		if i%2 == 1 {
			digit = "12"
		}
		c.notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": 1000 + i},
			"contentChanges": []any{map[string]any{"range": Range{Start: pos, End: Position{Line: pos.Line, Character: pos.Character + 2}}, "text": digit}},
		})
		c.diagnostics()
		start := time.Now()
		c.call("textDocument/hover", hp, &res)
		cold = append(cold, time.Since(start))
	}

	t.Logf("100 KiB fixture (%d bytes), %d runs", len(src), runs)
	t.Logf("CST edit (Tree.Reparse):             p50 %v  p95 %v  (plan: p95 <= 20 ms)", median(cst), p95(cst))
	t.Logf("edit to published diagnostics:       p50 %v  p95 %v", median(edit), p95(edit))
	t.Logf("cached hover (request to response):  p50 %v  p95 %v  (plan: p95 <= 50 ms)", median(hover), p95(hover))
	t.Logf("first hover after an edit (index build): p50 %v  p95 %v", median(cold), p95(cold))
	if p := p95(edit); p > 100*time.Millisecond {
		t.Errorf("edit to diagnostics p95 = %v, ceiling 100 ms", p)
	}
	if p := p95(hover); p > 100*time.Millisecond {
		t.Errorf("cached hover p95 = %v, ceiling 100 ms", p)
	}
}
