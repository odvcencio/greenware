package compiler

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"m31labs.dev/greenware/accept"
	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/datapkg"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
)

// An acceptance file imports the same data package as the asset
// (testdata/world/example/crate.accept.gw) and reads world.crate.width as a
// length. These tests build the crate from one package and run the acceptance
// against the same or another package, in memory and through the command.

func acceptSource(t testing.TB, m fs.FS) string {
	t.Helper()
	b, err := fs.ReadFile(m, "example/crate.accept.gw")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// acceptCrate loads the acceptance source against m's package and runs it on
// the product. loadDiags are the import and field diagnostics (nothing runs
// when there are any); runDiags are the failed expects.
func acceptCrate(t testing.TB, m fs.FS, src string, p *Product) (rep *accept.Report, loadDiags, runDiags []diag.Diagnostic) {
	t.Helper()
	f, ds := ast.LowerWith(lang.Parse([]byte(src)), ast.Options{Project: &ast.Project{FS: m, Dir: "example"}})
	if diag.HasErrors(ds) || len(f.Accepts) != 1 {
		t.Fatalf("lower: %v", ds)
	}
	data, loadDiags := accept.LoadData(f)
	if len(loadDiags) == 0 {
		loadDiags = data.Check(f.Accepts[0])
	}
	if len(loadDiags) > 0 {
		return nil, loadDiags, nil
	}
	in := accept.Inputs{accept.Key{Output: "crate", LOD: 0}: accept.Product{Doc: decode(t, p.Outputs[0].Files[0]), File: "crate.glb"}}
	rep, runDiags = accept.RunWith(f.Accepts[0], in, data)
	return rep, nil, runDiags
}

func TestCrateAcceptancePassesAtBothValues(t *testing.T) {
	for _, tc := range []struct {
		name, width, digest string
		m                   fstest.MapFS
	}{
		{"24 in", "24", worldDigest, worldFS(t)},
		{"25 in", "25", "", withWidth(t, "25")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := buildCrate(t, tc.m)
			rep, load, run := acceptCrate(t, tc.m, acceptSource(t, tc.m), p)
			if len(load) > 0 || len(run) > 0 {
				t.Fatalf("diagnostics %v %v", load, run)
			}
			if rep.Totals.Pass != 4 || rep.ExitCode() != 0 {
				t.Errorf("totals %+v", rep.Totals)
			}
			if len(rep.Inputs) != 1 || rep.Inputs[0].Contract != "example/world@1" || rep.Inputs[0].Profile != "standard" || rep.Inputs[0].SHA256 != p.Data[0].SHA256 {
				t.Errorf("inputs %+v, the build read %+v", rep.Inputs, p.Data)
			}
			if tc.digest != "" && rep.Inputs[0].SHA256 != tc.digest {
				t.Errorf("digest %s", rep.Inputs[0].SHA256)
			}
		})
	}
}

// An asset built from the 24 in package, checked against the 25 in package:
// the width expects fail and say the measured and the expected number.
func TestCrateAcceptanceFailsWhenBuiltAndPackageDisagree(t *testing.T) {
	built := buildCrate(t, worldFS(t))
	m25 := withWidth(t, "25")
	rep, load, run := acceptCrate(t, m25, acceptSource(t, m25), built)
	if len(load) > 0 {
		t.Fatal(load)
	}
	if rep.Totals.Fail != 1 || rep.Totals.Pass != 3 || rep.ExitCode() != 1 {
		t.Fatalf("totals %+v", rep.Totals)
	}
	if len(run) != 1 || run[0].Code != accept.CodeAccept {
		t.Fatalf("diagnostics %v", run)
	}
	msg := run[0].Message
	// 24 in = 0.6096 m measured; 25 in = 0.635 m, less the 0.1 mm tolerance.
	for _, want := range []string{"as wide as the package says", "expectation failed", "0.6096m", "world.crate.width - 0.1mm is 0.6349m"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	// The diagnostic lies on the expect in the accept file.
	src := acceptSource(t, m25)
	at := strings.Index(src, "expect (bounds(node(body)).max.x")
	if run[0].Span.Start != at {
		t.Errorf("span starts at %d, the expect is at %d", run[0].Span.Start, at)
	}
}

func TestCrateAcceptanceStalePinIsGWLock(t *testing.T) {
	m := worldFS(t)
	d := m["example/world/standard.json"]
	m["example/world/standard.json"] = &fstest.MapFile{Data: []byte(strings.Replace(string(d.Data), `"24"`, `"25"`, 1))}
	src := acceptSource(t, m)
	_, load, _ := acceptCrate(t, m, src, buildCrate(t, worldFS(t)))
	if len(load) != 1 || load[0].Code != diag.CodeLock {
		t.Fatalf("diagnostics %v", load)
	}
	at := strings.Index(src, `"example/world@1"`)
	if load[0].Span.Start != at || load[0].Span.End != at+len(`"example/world@1"`) {
		t.Errorf("span %v is not the import path at %d", load[0].Span, at)
	}
	if !strings.Contains(load[0].Message, "expected sha256 "+worldDigest) {
		t.Errorf("message %q", load[0].Message)
	}
}

func TestCrateAcceptanceUnknownFieldSuggestsAName(t *testing.T) {
	m := worldFS(t)
	src := strings.Replace(acceptSource(t, m), "world.crate.depth", "world.crate.depht", 1)
	_, load, _ := acceptCrate(t, m, src, buildCrate(t, m))
	if len(load) != 1 || load[0].Code != diag.CodeUnknownName || len(load[0].Edits) == 0 {
		t.Fatalf("diagnostics %v", load)
	}
	if !strings.Contains(load[0].Message, "`depht` is not a field of `world.crate`") {
		t.Errorf("message %q", load[0].Message)
	}
	if got := src[load[0].Span.Start:load[0].Span.End]; got != "depht" {
		t.Errorf("span covers %q", got)
	}
	// A whole group is GW-TYPE, a wrong qualifier root is the label it always was.
	src = strings.Replace(acceptSource(t, m), "world.crate.depth", "world.crate", 1)
	_, load, _ = acceptCrate(t, m, src, buildCrate(t, m))
	if len(load) != 1 || load[0].Code != diag.CodeType {
		t.Errorf("group: %v", load)
	}
}

// A length compared with an angle is GW-UNIT at the expect, with the data
// field named; a package value of the wrong unit is GW-UNIT at the import.
func TestCrateAcceptanceWrongDimensionIsGWUnit(t *testing.T) {
	m := worldFS(t)
	src := strings.Replace(acceptSource(t, m), "world.crate.width - 0.1mm", "world.crate.tilt - 0.1mm", 1)
	rep, load, run := acceptCrate(t, m, src, buildCrate(t, m))
	if len(load) > 0 {
		t.Fatal(load)
	}
	if rep.Totals.Refused != 1 || rep.ExitCode() != 1 {
		t.Fatalf("totals %+v", rep.Totals)
	}
	var got *diag.Diagnostic
	for i := range run {
		if run[i].Code == diag.CodeUnit {
			got = &run[i]
		}
	}
	if got == nil || len(run) != 1 {
		t.Fatalf("diagnostics %v", run)
	}

	bad := worldFS(t)
	d := bad["example/world/standard.json"]
	bad["example/world/standard.json"] = &fstest.MapFile{Data: []byte(strings.Replace(string(d.Data), `"value": "24", "unit": "in"`, `"value": "24", "unit": "deg"`, 1))}
	// The lock is written by hand: datapkg.NewLock refuses the wrong unit.
	sum, err := datapkg.Digest(bad["example/world/schema-v1.json"].Data, bad["example/world/standard.json"].Data)
	if err != nil {
		t.Fatal(err)
	}
	lock := datapkg.Lock{Schema: "greenware.datalock/1", Contract: "example/world@1", SchemaFile: "example/world/schema-v1.json", DataFile: "example/world/standard.json", SHA256: sum}
	bad["example/world.lock"] = &fstest.MapFile{Data: lock.Bytes()}
	_, load, _ = acceptCrate(t, bad, acceptSource(t, bad), buildCrate(t, m))
	if len(load) != 1 || load[0].Code != diag.CodeUnit {
		t.Errorf("a degree for a length: %v", load)
	}
}

// writeProject copies the in-memory project to disk and returns its root.
func writeProject(t testing.TB, m fstest.MapFS) string {
	t.Helper()
	root := t.TempDir()
	for name, f := range m {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, f.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// The command's exit codes with a data import: 0 pass, 1 an expect fails,
// 2 a stale pin or an unknown field or a usage error, 3 an expect that cannot
// be measured and none failed.
func TestAcceptCommandWithDataImport(t *testing.T) {
	built := buildCrate(t, worldFS(t))
	glbDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(glbDir, "crate.glb"), built.Outputs[0].Files[0].GLB, 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(m fstest.MapFS, accSrc string, extra ...string) (int, string, string) {
		t.Helper()
		root := writeProject(t, m)
		acc := filepath.Join(root, "example", "crate.accept.gw")
		if accSrc != "" {
			if err := os.WriteFile(acc, []byte(accSrc), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		args := append([]string{filepath.Join(root, "example", "crate.gw"), acc, "--glb-dir", glbDir}, extra...)
		var out, errOut bytes.Buffer
		code := accept.Command(args, &out, &errOut)
		return code, out.String(), errOut.String()
	}
	m24 := worldFS(t)

	code, out, errOut := run(m24, "")
	if code != accept.ExitPass {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "input example/world@1 profile standard sha256 "+worldDigest) {
		t.Errorf("the text report lists no input:\n%s", out)
	}

	code, out, _ = run(m24, "", "--json")
	var rep accept.Report
	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil || len(rep.Inputs) != 1 || rep.Inputs[0].SHA256 != worldDigest {
		t.Errorf("json report: exit %d inputs %+v\n%s", code, rep.Inputs, out)
	}
	if !strings.Contains(out, `"inputs": [`) || !strings.Contains(out, `"profile": "standard"`) {
		t.Errorf("json:\n%s", out)
	}

	// The GLB was built at 24 in; the package says 25 in.
	code, _, errOut = run(withWidth(t, "25"), "")
	if code != accept.ExitFail || !strings.Contains(errOut, "GW-ACCEPT") || !strings.Contains(errOut, "crate.accept.gw:") {
		t.Errorf("disagreement: exit %d\n%s", code, errOut)
	}

	// A stale pin and an unknown field are diagnostics in the accept file.
	stale := worldFS(t)
	sd := stale["example/world/standard.json"]
	stale["example/world/standard.json"] = &fstest.MapFile{Data: []byte(strings.Replace(string(sd.Data), `"24"`, `"25"`, 1))}
	code, out, errOut = run(stale, "")
	if code != accept.ExitUsage || !strings.Contains(errOut, "GW-LOCK") || strings.Contains(out, "pass ") {
		t.Errorf("stale pin: exit %d\nout %s\nerr %s", code, out, errOut)
	}
	code, out, errOut = run(m24, strings.Replace(acceptSource(t, m24), "world.crate.depth", "world.crate.depht", 1))
	if code != accept.ExitUsage || !strings.Contains(errOut, "GW-UNKNOWN-NAME") || !strings.Contains(errOut, "crate.accept.gw:") || out != "" {
		t.Errorf("unknown field: exit %d\nout %s\nerr %s", code, out, errOut)
	}

	// Nothing failed, but the outline cannot be measured.
	src := acceptSource(t, m24)
	outline := strings.TrimRight(src, "\n")
	outline = strings.TrimSuffix(outline, "}") + "  expect (outline(front, \"ref/crate.png\", within = 3px) >= 0.9, \"front outline\")\n}\n"
	if code, out, errOut = run(m24, outline); code != accept.ExitUnavailable {
		t.Errorf("unavailable: exit %d\n%s\n%s", code, out, errOut)
	}

	// Usage: no --glb-dir.
	var o, e bytes.Buffer
	if code := accept.Command([]string{"a.gw", "b.gw"}, &o, &e); code != accept.ExitUsage {
		t.Errorf("usage: exit %d", code)
	}
}
