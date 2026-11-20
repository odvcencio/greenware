package workerproto

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/greenware/ast"
	"m31labs.dev/greenware/compiler"
	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
)

func read(t testing.TB, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func goalpost(t testing.TB, output string) Request {
	return Request{Schema: RequestSchema, ID: "post", Root: "goalpost.gw", Output: output,
		Files: map[string][]byte{"goalpost.gw": read(t, "examples/goalpost/goalpost.gw")}}
}

// approved reads testdata/compiler/approved/examples-goalpost.provisional.sha256
// into name -> "sha256 bytes".
func approved(t testing.TB) map[string][2]string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata/compiler/approved/examples-goalpost.provisional.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m := map[string][2]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := strings.Fields(line)
		m[p[2]] = [2]string{p[0], p[1]}
	}
	return m
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// The worker answers with the GLB bytes the approved goal post baseline holds,
// for each output alone and for both together.
func TestGoalpostMatchesApproved(t *testing.T) {
	want := approved(t)
	for _, c := range []struct {
		output string
		files  int
	}{{"stadium", 3}, {"practice", 3}, {"", 6}} {
		resp := Handle(goalpost(t, c.output))
		if !resp.OK || resp.Error != nil || len(resp.Files) != c.files {
			t.Fatalf("%q: ok=%v err=%v files=%d diag=%s", c.output, resp.OK, resp.Error, len(resp.Files), resp.Diagnostics)
		}
		for _, f := range resp.Files {
			w, ok := want[f.Name]
			if !ok || sum(f.GLB) != w[0] || f.SHA256 != w[0] || w[1] != itoa(len(f.GLB)) {
				t.Errorf("%q: %s is %s (%d bytes), approved %v", c.output, f.Name, sum(f.GLB), len(f.GLB), w)
			}
		}
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// Handle returns what compiler.Build returns for the same source: the files
// and the record, byte for byte. cmd/greenware writes exactly these.
func TestSameAsCompiler(t *testing.T) {
	src := read(t, "examples/goalpost/goalpost.gw")
	f, ds := ast.LowerWith(lang.Parse(src), ast.Options{})
	if diag.HasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := compiler.Build(f, "stadium")
	if p == nil {
		t.Fatal(ds)
	}
	resp := Handle(goalpost(t, "stadium"))
	if !bytes.Equal(resp.Record.Bytes, p.Record()) || resp.Record.Name != "goalpost.build.json" {
		t.Errorf("the record differs from compiler.Build's")
	}
	i := 0
	for _, o := range p.Outputs {
		for _, fl := range o.Files {
			if !bytes.Equal(resp.Files[i].GLB, fl.GLB) || resp.Files[i].Name != fl.Name || resp.Files[i].Output != o.Name {
				t.Errorf("file %d differs", i)
			}
			i++
		}
	}
}

// The same request is the same bytes, and the keys come in the documented order.
func TestEncodingIsStable(t *testing.T) {
	req, _ := json.Marshal(goalpost(t, "practice"))
	a, b := HandleBytes(req), HandleBytes(req)
	if !bytes.Equal(a, b) {
		t.Fatal("two answers to one request differ")
	}
	s := string(a[:170])
	if !strings.HasPrefix(s, `{"schema":"greenware.worker.response/1","id":"post","ok":true,"files":[{"output":"practice","name":"goalpost-practice.glb","level":0,"sha256":"`) {
		t.Errorf("key order: %s", s)
	}
	if bytes.HasSuffix(a, []byte("\n")) {
		t.Error("trailing newline")
	}
	var resp Response
	if err := json.Unmarshal(a, &resp); err != nil || !resp.OK || len(resp.Files) != 3 {
		t.Fatalf("%v %+v", err, resp.Error)
	}
}

func diagCodes(t testing.TB, resp Response) []string {
	t.Helper()
	var doc struct {
		OK          bool `json:"ok"`
		Diagnostics []struct {
			Code string
			Span struct{ Start, End struct{ Byte, Line int } }
		}
	}
	if err := json.Unmarshal(resp.Diagnostics, &doc); err != nil {
		t.Fatalf("diagnostics: %v: %s", err, resp.Diagnostics)
	}
	var codes []string
	for _, d := range doc.Diagnostics {
		codes = append(codes, d.Code)
	}
	return codes
}

func single(src string) Request {
	return Request{Schema: RequestSchema, Root: "a.gw", Files: map[string][]byte{"a.gw": []byte(src)}}
}

func TestSyntaxErrorGivesDiagnostics(t *testing.T) {
	resp := Handle(single("greenware 1\nasset {"))
	codes := diagCodes(t, resp)
	if resp.OK || resp.Error != nil || len(resp.Files) != 0 || len(codes) == 0 || codes[0] != diag.CodeSyntax {
		t.Fatalf("%+v %v", resp, codes)
	}
	if !strings.Contains(string(resp.Diagnostics), `"line":2`) {
		t.Errorf("no line in %s", resp.Diagnostics)
	}
}

// The runtime_safe fixture builds natively and is refused by the worker with
// GW-RUNTIME-UNSAFE at the `check rig` block.
func TestRuntimeUnsafeFixture(t *testing.T) {
	src := read(t, "testdata/diag/GW-RUNTIME-UNSAFE.gw")
	resp := Handle(single(string(src)))
	codes := diagCodes(t, resp)
	if resp.OK || len(codes) != 1 || codes[0] != diag.CodeRuntimeUnsafe {
		t.Fatalf("worker: ok=%v %v %s", resp.OK, codes, resp.Diagnostics)
	}
	var doc struct {
		Diagnostics []struct {
			Span struct{ Start, End struct{ Byte int } }
		}
	}
	json.Unmarshal(resp.Diagnostics, &doc)
	sp := doc.Diagnostics[0].Span
	if got := string(src[sp.Start.Byte:sp.End.Byte]); got != "rig" {
		t.Errorf("span covers %q, want the word rig of the check block", got)
	}

	// A host that allows it builds the same source, which is what the native
	// greenware does.
	req := single(string(src))
	req.Options.RuntimeSafe = SafeOff
	if r := Handle(req); r.Error == nil || r.Error.Code != ErrBadRequest {
		t.Errorf("the worker accepted runtime_safe off: %+v", r)
	}
	r := Host{AllowUnrestricted: true}.Handle(req)
	if !r.OK || len(r.Files) != 1 {
		t.Errorf("unrestricted: ok=%v err=%v diag=%s", r.OK, r.Error, r.Diagnostics)
	}
}

// A `solve` step in a skin is not runtime safe: the worker refuses it at the
// word `solve`, and the same source builds under an unrestricted host.
func TestSolveStepIsRuntimeUnsafe(t *testing.T) {
	src := string(read(t, "examples/arm-solved/arm-solved.gw"))
	resp := Handle(single(src))
	codes := diagCodes(t, resp)
	if resp.OK || len(codes) != 1 || codes[0] != diag.CodeRuntimeUnsafe {
		t.Fatalf("worker: ok=%v %v %s", resp.OK, codes, resp.Diagnostics)
	}
	var doc struct {
		Diagnostics []struct {
			Span struct{ Start, End struct{ Byte int } }
		}
	}
	json.Unmarshal(resp.Diagnostics, &doc)
	sp := doc.Diagnostics[0].Span
	if got := string(src[sp.Start.Byte:sp.End.Byte]); got != "solve" {
		t.Errorf("span covers %q, want the word solve", got)
	}
	req := single(src)
	req.Options.RuntimeSafe = SafeOff
	if r := (Host{AllowUnrestricted: true}).Handle(req); !r.OK {
		t.Errorf("unrestricted: ok=%v err=%v diag=%s", r.OK, r.Error, r.Diagnostics)
	}
}

func TestWorkCeiling(t *testing.T) {
	post := goalpost(t, "stadium")
	post.Options.MaxWork = compiler.DefaultMaxTotalWork + 1
	if c := diagCodes(t, Handle(post)); len(c) != 1 || c[0] != diag.CodeLimit {
		t.Errorf("above the ceiling: %v", c)
	}
	post.Options.MaxWork = 1_000_000 // below the schedule's total
	resp := Handle(post)
	if c := diagCodes(t, resp); resp.OK || len(c) != 1 || c[0] != diag.CodeLimit {
		t.Errorf("below the schedule: ok=%v %v", resp.OK, c)
	}
	post.Options.MaxWork = compiler.DefaultMaxTotalWork
	if !Handle(post).OK {
		t.Error("the default ceiling must build the goal post")
	}
}

const gearSrc = `greenware 1
asset cap { root = cap }
plug head_top { kind = hat at = (0, 1.7, 0) }
part cap { sphere dome { radius = 0.12m at = (0, 1.78, 0) } }
output cap { file = "cap.glb" }
`

const hostSrc = `greenware 1
import "lib/cap.gw" as cap
asset a { root = a idle = rest }
skeleton s { joint base { at = (0, 0, 0) socket crown { kind = hat at = (0, 1.7, 0) } } }
part a { joint = base sphere ball { radius = 0.1m at = (0, 1, 0) } }
mount headgear { asset = cap socket = crown }
clip rest { duration = 1s pose { base = rot(0, 0, 0) } }
output demo { file = "a.glb" }
`

// An import reads the snapshot and nothing else.
func TestImportReadsSnapshotOnly(t *testing.T) {
	req := Request{Schema: RequestSchema, Root: "proj/a.gw", Files: map[string][]byte{
		"proj/a.gw": []byte(strings.Replace(hostSrc, `"lib/cap.gw"`, `"lib/cap.gw"`, 1)), "proj/lib/cap.gw": []byte(gearSrc)}}
	resp := Handle(req)
	if !resp.OK {
		t.Fatalf("with the gear: err=%v diag=%s", resp.Error, resp.Diagnostics)
	}
	delete(req.Files, "proj/lib/cap.gw")
	resp = Handle(req)
	if c := diagCodes(t, resp); resp.OK || len(c) == 0 || c[0] != diag.CodeMissing {
		t.Errorf("without the gear: ok=%v %v", resp.OK, c)
	}
}

func TestSnapshotValidation(t *testing.T) {
	big := func(n int) []byte { return bytes.Repeat([]byte(" "), n) }
	many := map[string][]byte{"a.gw": []byte("greenware 1")}
	for i := 0; i < MaxFiles; i++ {
		many["f"+itoa(i)] = nil
	}
	for _, c := range []struct {
		name  string
		files map[string][]byte
		root  string
		code  string
	}{
		{"empty", nil, "a.gw", ErrBadRequest},
		{"missing root", map[string][]byte{"b.gw": nil}, "a.gw", ErrBadRequest},
		{"root not gw", map[string][]byte{"a.txt": nil}, "a.txt", ErrBadRequest},
		{"dotdot", map[string][]byte{"a.gw": nil, "../x": nil}, "a.gw", ErrBadRequest},
		{"absolute", map[string][]byte{"a.gw": nil, "/x": nil}, "a.gw", ErrBadRequest},
		{"backslash", map[string][]byte{"a.gw": nil, `x\y`: nil}, "a.gw", ErrBadRequest},
		{"unclean", map[string][]byte{"a.gw": nil, "d//x": nil}, "a.gw", ErrBadRequest},
		{"too many", many, "a.gw", ErrTooLarge},
		{"file too big", map[string][]byte{"a.gw": big(MaxFileBytes + 1)}, "a.gw", ErrTooLarge},
		{"snapshot too big", map[string][]byte{"a.gw": big(MaxFileBytes), "b": big(MaxFileBytes), "c": big(1)}, "a.gw", ErrTooLarge},
	} {
		resp := Handle(Request{Schema: RequestSchema, ID: "x", Root: c.root, Files: c.files})
		if resp.OK || resp.Error == nil || resp.Error.Code != c.code || resp.ID != "x" {
			t.Errorf("%s: %+v", c.name, resp)
		}
	}
	bad := single("greenware 1")
	bad.Options.RuntimeSafe = "maybe"
	if r := Handle(bad); r.Error == nil || r.Error.Code != ErrBadRequest {
		t.Errorf("runtime_safe maybe: %+v", r)
	}
}

func TestRequestDecoding(t *testing.T) {
	for name, doc := range map[string]string{
		"not json":     "{",
		"unknown key":  `{"schema":"greenware.worker.request/1","extra":1}`,
		"wrong schema": `{"schema":"greenware.worker.request/2"}`,
		"trailing":     `{"schema":"greenware.worker.request/1"} {}`,
		"bad base64":   `{"schema":"greenware.worker.request/1","files":{"a.gw":"!!"}}`,
	} {
		out := HandleBytes([]byte(doc))
		var r Response
		if err := json.Unmarshal(out, &r); err != nil || r.OK || r.Error == nil || r.Error.Code != ErrBadRequest {
			t.Errorf("%s: %s", name, out)
		}
	}
	big := bytes.Repeat([]byte(" "), MaxRequestBytes+1)
	var r Response
	json.Unmarshal(HandleBytes(big), &r)
	if r.Error == nil || r.Error.Code != ErrTooLarge {
		t.Errorf("an oversized request: %+v", r)
	}
}

func TestResponseCap(t *testing.T) {
	resp := Response{ID: "big", OK: true, Files: []File{{Name: "x.glb", GLB: make([]byte, MaxResponseBytes)}}}
	out := Encode(resp)
	var r Response
	if err := json.Unmarshal(out, &r); err != nil || r.OK || r.ID != "big" || r.Error == nil || r.Error.Code != ErrTooLarge {
		t.Errorf("%v %+v", err, r)
	}
}

func TestCancelBetweenPhases(t *testing.T) {
	for stop := 1; stop <= 4; stop++ {
		calls := 0
		h := Host{Canceled: func() bool { calls++; return calls == stop }}
		resp := h.Handle(goalpost(t, "stadium"))
		if resp.OK || resp.Error == nil || resp.Error.Code != ErrCanceled || calls != stop {
			t.Errorf("stop at check %d: calls=%d %+v", stop, calls, resp)
		}
	}
	calls := 0
	if r := (Host{Canceled: func() bool { calls++; return false }}).Handle(goalpost(t, "stadium")); !r.OK || calls != 4 {
		t.Errorf("not canceled: ok=%v checks=%d", r.OK, calls)
	}
}

// The athlete example carries a rig audit, so the worker sends it to the
// native build.
func TestAthleteIsRefused(t *testing.T) {
	req := Request{Schema: RequestSchema, Root: "athlete.gw", Files: map[string][]byte{"athlete.gw": read(t, "examples/athlete/athlete.gw")}}
	resp := Handle(req)
	if c := diagCodes(t, resp); resp.OK || len(c) == 0 || c[0] != diag.CodeRuntimeUnsafe {
		t.Errorf("ok=%v %v err=%v", resp.OK, c, resp.Error)
	}
}

// A data package is read from the sealed snapshot: the lock, the schema and
// the data file are keys of `files`, and the answer is the native build's.
func TestDataPackageComesFromTheSnapshot(t *testing.T) {
	names := []string{"example/crate.gw", "example/world.lock", "example/world/schema-v1.json", "example/world/standard.json"}
	req := func() Request {
		files := map[string][]byte{}
		for _, n := range names {
			files[n] = read(t, "testdata/world/"+n)
		}
		return Request{Schema: RequestSchema, ID: "crate", Root: "example/crate.gw", Files: files}
	}
	resp := Handle(req())
	if !resp.OK || len(resp.Files) != 1 {
		t.Fatalf("ok=%v err=%v diag=%s", resp.OK, resp.Error, resp.Diagnostics)
	}
	// The same bytes as the native build of the same project.
	f, ds := ast.LowerWith(lang.Parse(read(t, "testdata/world/example/crate.gw")), ast.Options{Project: &ast.Project{FS: snapshot(req().Files), Dir: "example"}})
	if diag.HasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := compiler.Build(f, "")
	if p == nil {
		t.Fatal(ds)
	}
	if !bytes.Equal(resp.Files[0].GLB, p.Outputs[0].Files[0].GLB) || !bytes.Equal(resp.Record.Bytes, p.Record()) {
		t.Error("the worker's file or record differs from the compiler's")
	}
	if !bytes.Contains(resp.Record.Bytes, []byte(`6a972715adc467fefb3e4c892958ee75a1cdb752a407b817fbd2af606e9beab5`)) || !bytes.Contains(resp.Files[0].GLB, []byte(`"extras":{"greenware":{"inputs":[{"contract":"example/world@1"`)) {
		t.Error("the record or the GLB does not name the data package")
	}

	// A file the page did not send: the lock is GW-LOCK, the schema and the
	// data file are GW-IMPORT-DATA. Nothing is built.
	for name, want := range map[string]string{
		"example/world.lock":           diag.CodeLock,
		"example/world/schema-v1.json": diag.CodeImportData,
		"example/world/standard.json":  diag.CodeImportData,
	} {
		r := req()
		delete(r.Files, name)
		resp := Handle(r)
		if c := diagCodes(t, resp); resp.OK || len(resp.Files) != 0 || len(c) != 1 || c[0] != want {
			t.Errorf("without %s: ok=%v files=%d %v, want %s", name, resp.OK, len(resp.Files), c, want)
		}
	}
	// A changed data file with the old lock is a stale pin.
	r := req()
	r.Files["example/world/standard.json"] = bytes.Replace(r.Files["example/world/standard.json"], []byte(`"24"`), []byte(`"25"`), 1)
	if c := diagCodes(t, Handle(r)); len(c) != 1 || c[0] != diag.CodeLock {
		t.Errorf("stale pin: %v", c)
	}
	// The snapshot caps apply to the package files: 2 MiB per file.
	r = req()
	r.Files["example/world/standard.json"] = bytes.Repeat([]byte(" "), MaxFileBytes+1)
	if resp := Handle(r); resp.Error == nil || resp.Error.Code != ErrTooLarge {
		t.Errorf("a data file over the cap: %+v", resp)
	}
}
