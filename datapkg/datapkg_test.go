package datapkg

import (
	"errors"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"m31labs.dev/greenware/units"
)

const fixtureRoot = "../testdata/world"

// The test vectors. A second implementation (the game's worldgen) must give
// these canonical bytes and this digest for the fixture files. They are
// repeated in docs/reference/data-package.md.
const (
	vectorSchemaCanonical = `{"contract":"example/world@1","fields":{"crate":{"depth":{"dimension":"length","meaning":"distance between the front and back outer faces of the crate, along z"},"height":{"dimension":"length","meaning":"height of the crate from the floor to the top face"},"slat_count":{"dimension":"number","meaning":"number of slats across the top, counted along z"},"slat_gap":{"dimension":"ratio","meaning":"share of each slat pitch left open between neighbouring slats"},"tilt":{"dimension":"angle","meaning":"angle of the lid about the x axis when the crate is open"},"width":{"dimension":"length","meaning":"distance between the two outer side faces of the crate, along x"}}}}`
	vectorDataCanonical   = `{"contract":"example/world@1","profile":"standard","values":{"crate":{"depth":{"unit":"in","value":"18"},"height":{"unit":"ft","value":"2"},"slat_count":{"value":"6"},"slat_gap":{"unit":"%","value":"12.5"},"tilt":{"unit":"deg","value":"15"},"width":{"unit":"in","value":"24"}}}}`
	vectorDigest          = "6a972715adc467fefb3e4c892958ee75a1cdb752a407b817fbd2af606e9beab5"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureRoot, "example", "world", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVectorCanonicalAndDigest(t *testing.T) {
	s, d := readFixture(t, "schema-v1.json"), readFixture(t, "standard.json")
	cs, err := Canonical("schema", s)
	if err != nil {
		t.Fatal(err)
	}
	cd, err := Canonical("data", d)
	if err != nil {
		t.Fatal(err)
	}
	if string(cs) != vectorSchemaCanonical || len(cs) != 667 {
		t.Errorf("schema canonical form (%d bytes):\n%s", len(cs), cs)
	}
	if string(cd) != vectorDataCanonical || len(cd) != 278 {
		t.Errorf("data canonical form (%d bytes):\n%s", len(cd), cd)
	}
	got, err := Digest(s, d)
	if err != nil || got != vectorDigest {
		t.Errorf("digest %s, %v; want %s", got, err, vectorDigest)
	}
	// The canonical bytes are a fixed point: digesting them gives the same pin.
	if again, _ := Digest(cs, cd); again != vectorDigest {
		t.Errorf("digest of the canonical forms %s", again)
	}
}

// Escaping vector: only the quote, the backslash and control bytes are
// escaped; "<", ">", "&", "/", U+2028 and non-ASCII text stay as UTF-8; numbers
// stay as written; keys sort by UTF-8 bytes.
func TestCanonicalEscapes(t *testing.T) {
	in := "{ \"b\" : \"a\\u0001\\\"\\\\\\n<>&\\/\u2028\\u00e9\\ud83d\\ude00\",\n \"a\":[1.50, true,null,-0,1E+2],\"\u00e9\":{},\"B\":false }"
	want := "{\"B\":false,\"a\":[1.50,true,null,-0,1E+2],\"b\":\"a\\u0001\\\"\\\\\\u000a<>&/\u2028\u00e9\U0001F600\",\"\u00e9\":{}}"
	got, err := Canonical("v", []byte(in))
	if err != nil || string(got) != want {
		t.Fatalf("got %q, %v\nwant %q", got, err, want)
	}
}

func TestCanonicalRejects(t *testing.T) {
	for name, in := range map[string]string{
		"duplicate key":         `{"a":1,"a":2}`,
		"duplicate by escape":   `{"a":1,"a":2}`,
		"trailing text":         `{"a":1} x`,
		"trailing comma":        `{"a":1,}`,
		"single quotes":         `{'a':1}`,
		"comment":               `{"a":1} // c`,
		"leading zero":          `{"a":01}`,
		"lone high surrogate":   `"\ud800"`,
		"lone low surrogate":    `"\udc00"`,
		"bad escape":            `"\q"`,
		"raw newline in string": "\"a\nb\"",
		"byte order mark":       "\xef\xbb\xbf{}",
		"invalid utf-8":         "\"\xff\"",
		"empty":                 ``,
		"unclosed object":       `{"a":1`,
		"deeply nested":         strings.Repeat("[", 40) + strings.Repeat("]", 40),
		"nan":                   `{"a":NaN}`,
		"missing digits":        `{"a":1.}`,
		"plus sign":             `{"a":+1}`,
	} {
		if _, err := Canonical("f.json", []byte(in)); err == nil {
			t.Errorf("%s: accepted %q", name, in)
		} else if !strings.HasPrefix(err.Error(), "f.json") {
			t.Errorf("%s: error does not name the file: %v", name, err)
		}
	}
	_, err := Canonical("f.json", []byte("{\n  \"a\": 1,\n  \"a\": 2\n}"))
	var e *Error
	if !errors.As(err, &e) || e.Line != 3 || e.Col != 3 || e.Kind != KindData {
		t.Errorf("duplicate key position: %+v", err)
	}
}

func TestDigestIgnoresOrderAndWhitespace(t *testing.T) {
	s, d := readFixture(t, "schema-v1.json"), readFixture(t, "standard.json")
	cs, _ := Canonical("s", s)
	cd, _ := Canonical("d", d)
	// A compact copy with the sorted keys, and a copy with tabs and CRLF.
	crlf := strings.ReplaceAll(string(d), "\n", "\r\n\t")
	for name, pair := range map[string][2][]byte{
		"canonical":      {cs, cd},
		"crlf and tabs":  {s, []byte(crlf)},
		"escaped letter": {s, []byte(strings.Replace(string(d), `"standard"`, `"standard"`, 1))},
	} {
		got, err := Digest(pair[0], pair[1])
		if err != nil || got != vectorDigest {
			t.Errorf("%s: %s, %v", name, got, err)
		}
	}
	// Swapped member order in a hand-written copy.
	swapped := `{"values":{"crate":{"width":{"unit":"in","value":"24"},"tilt":{"value":"15","unit":"deg"},"slat_gap":{"value":"12.5","unit":"%"},"slat_count":{"value":"6"},"height":{"unit":"ft","value":"2"},"depth":{"value":"18","unit":"in"}}},"profile":"standard","contract":"example/world@1"}`
	if got, err := Digest(s, []byte(swapped)); err != nil || got != vectorDigest {
		t.Errorf("swapped order: %s, %v", got, err)
	}
}

func TestDigestSeesEveryByteOfContent(t *testing.T) {
	s, d := readFixture(t, "schema-v1.json"), readFixture(t, "standard.json")
	for name, edit := range map[string]func(string) string{
		"one digit of a value": func(x string) string { return strings.Replace(x, `"24"`, `"25"`, 1) },
		"a unit":               func(x string) string { return strings.Replace(x, `"in"`, `"cm"`, 1) },
		"the profile":          func(x string) string { return strings.Replace(x, "standard", "standarD", 1) },
		"a key":                func(x string) string { return strings.Replace(x, `"depth"`, `"depht"`, 1) },
		"a trailing decimal":   func(x string) string { return strings.Replace(x, `"12.5"`, `"12.50"`, 1) },
	} {
		got, err := Digest(s, []byte(edit(string(d))))
		if err != nil {
			t.Errorf("%s: %v", name, err)
		} else if got == vectorDigest {
			t.Errorf("%s: the digest did not change", name)
		}
	}
	// A schema edit changes it too.
	if got, _ := Digest([]byte(strings.Replace(string(s), "along x", "along y", 1)), d); got == vectorDigest {
		t.Error("a schema edit did not change the digest")
	}
	// The two files are not interchangeable: the length prefixes keep a shift
	// of bytes between S and D from giving the same pin.
	a, _ := Digest([]byte(`{"a":"bc"}`), []byte(`{"d":"e"}`))
	b, _ := Digest([]byte(`{"a":"b"}`), []byte(`{"c":"d","d":"e"}`))
	if a == b {
		t.Error("moving bytes between the files kept the digest")
	}
}

func fixtureFS(t *testing.T) fstest.MapFS {
	t.Helper()
	m := fstest.MapFS{}
	for _, f := range []string{"example/world.lock", "example/world/schema-v1.json", "example/world/standard.json"} {
		b, err := os.ReadFile(filepath.Join(fixtureRoot, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		m[f] = &fstest.MapFile{Data: b}
	}
	return m
}

func TestLoadFixtureAndAccessors(t *testing.T) {
	p, err := Load(fixtureRoot, "example/world@1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Digest != vectorDigest || p.Lock.SHA256 != vectorDigest || p.Profile != "standard" || p.Contract != "example/world@1" {
		t.Errorf("package %+v", p)
	}
	if got := p.Input(); got != (Input{"example/world@1", "standard", vectorDigest}) {
		t.Errorf("input %+v", got)
	}
	if got := strings.Join(p.Names(), " "); got != "crate.depth crate.height crate.slat_count crate.slat_gap crate.tilt crate.width" {
		t.Errorf("names %s", got)
	}
	w, ok := p.Value("crate", "width")
	if !ok || w.Dimension != Length || w.Unit != "in" || w.Text != "24" || !strings.Contains(w.Meaning, "outer side faces") {
		t.Fatalf("width %+v", w)
	}
	if r, unit := w.Exact(); unit != "m" || r.Cmp(big.NewRat(381, 625)) != 0 { // 24 in = 0.6096 m
		t.Errorf("width exact %s %s", r, unit)
	}
	if w2, ok := p.Value("crate.width"); !ok || w2.Text != "24" {
		t.Error("a dotted path is not read")
	}
	if _, ok := p.Value("crate", "nothing"); ok {
		t.Error("an undeclared field has a value")
	}
	if _, ok := p.Value("crate"); ok {
		t.Error("a group has a value")
	}
	if kids, ok := p.Group("crate"); !ok || len(kids) != 6 {
		t.Errorf("group %v", kids)
	}
	h, _ := p.Value("crate", "height")
	if r, _ := h.Exact(); r.Cmp(big.NewRat(3048*2, 10000)) != 0 {
		t.Errorf("2 ft = %s m", r)
	}
	g, _ := p.Value("crate", "slat_gap")
	if r, unit := g.Exact(); unit != "" || r.Cmp(big.NewRat(1, 8)) != 0 || g.Dimension != Ratio { // 12.5 % = 1/8
		t.Errorf("gap %s %q", r, unit)
	}
	n, _ := p.Value("crate", "slat_count")
	if r, _ := n.Exact(); n.Unit != "" || r.Cmp(big.NewRat(6, 1)) != 0 {
		t.Errorf("count %v", n)
	}
	// Each float is the bits of the literal written in the same unit.
	for _, c := range []struct{ field, lit string }{
		{"crate.width", "24in"}, {"crate.height", "2ft"}, {"crate.depth", "18in"}, {"crate.slat_gap", "12.5%"},
		{"crate.tilt", "15deg"}, {"crate.slat_count", "6"},
	} {
		v, _ := p.Value(c.field)
		want, err := units.ParseNumber(c.lit)
		if err != nil {
			t.Fatal(err)
		}
		if math.Float64bits(v.Float64()) != math.Float64bits(want.Num) {
			t.Errorf("%s = %v, literal %s = %v", c.field, v.Float64(), c.lit, want.Num)
		}
	}
}

func TestLoadIsIndependentOfCheckoutPath(t *testing.T) {
	dir := t.TempDir()
	for f, mf := range fixtureFS(t) {
		full := filepath.Join(dir, "deep", "er", filepath.FromSlash(f))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, mf.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Load(filepath.Join(dir, "deep", "er"), "example/world@1")
	if err != nil || p.Digest != vectorDigest {
		t.Fatalf("%v", err)
	}
}

// mutate loads the fixture with one file edited and returns the error.
func mutate(t *testing.T, file string, edit func(string) string) error {
	t.Helper()
	m := fixtureFS(t)
	m[file] = &fstest.MapFile{Data: []byte(edit(string(m[file].Data)))}
	_, err := LoadFS(m, "example/world@1")
	return err
}

func wantErr(t *testing.T, name string, err error, kind Kind, file, path, text string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Errorf("%s: no error (%v)", name, err)
		return
	}
	if e.Kind != kind || e.File != file || e.Path != path || !strings.Contains(e.Msg, text) {
		t.Errorf("%s: got kind %v file %q path %q: %s\nwant kind %v file %q path %q containing %q", name, e.Kind, e.File, e.Path, e.Msg, kind, file, path, text)
	}
}

func TestStrictData(t *testing.T) {
	const data = "example/world/standard.json"
	const schema = "example/world/schema-v1.json"
	repl := func(old, new string) func(string) string {
		return func(x string) string {
			if !strings.Contains(x, old) {
				t.Fatalf("fixture lacks %q", old)
			}
			return strings.Replace(x, old, new, 1)
		}
	}
	// A data edit that keeps the lock is a stale pin only when it is valid.
	wantErr(t, "stale pin", mutate(t, data, repl(`"24"`, `"25"`)), KindLock, "example/world.lock", "", "expected sha256 "+vectorDigest)
	var le *Error
	if err := mutate(t, data, repl(`"24"`, `"25"`)); errors.As(err, &le) {
		if le.Expected != vectorDigest || len(le.Actual) != 64 || le.Actual == vectorDigest {
			t.Errorf("pin error digests %q %q", le.Expected, le.Actual)
		}
	}
	for _, c := range []struct {
		name      string
		file      string
		edit      func(string) string
		kind      Kind
		path, msg string
	}{
		{"duplicate key", data, repl(`"width"`, `"depth"`), KindData, "", "duplicate key"},
		{"unknown field in data", data, repl(`"tilt"`, `"tilted"`), KindData, "$.values.crate.tilted", "declares no field"},
		{"missing field in data", data, repl(",\n      \"tilt\": { \"value\": \"15\", \"unit\": \"deg\" }", ""), KindData, "$.values.crate.tilt", "no value for"},
		{"missing unit", data, repl(`, "unit": "in" }`, ` }`), KindData, "$.values.crate.width.unit", "unit is missing"},
		{"unit on a number", data, repl(`{ "value": "6" }`, `{ "value": "6", "unit": "in" }`), KindData, "$.values.crate.slat_count.unit", "no unit"},
		{"unknown unit", data, repl(`"ft"`, `"furlong"`), KindData, "$.values.crate.height.unit", "not a unit"},
		{"wrong dimension", data, repl(`"deg"`, `"mm"`), KindUnit, "$.values.crate.tilt.unit", "unit of length"},
		{"percent for a length", data, repl(`"in"`, `"%"`), KindUnit, "$.values.crate.width.unit", "unit of ratio"},
		{"value is a number", data, repl(`"24"`, `24`), KindData, "$.values.crate.width.value", "decimal string"},
		{"exponent", data, repl(`"24"`, `"2.4e1"`), KindData, "$.values.crate.width.value", "not a decimal string"},
		{"plus sign", data, repl(`"24"`, `"+24"`), KindData, "$.values.crate.width.value", "not a decimal string"},
		{"leading zero", data, repl(`"24"`, `"024"`), KindData, "$.values.crate.width.value", "not a decimal string"},
		{"bare dot", data, repl(`"24"`, `".5"`), KindData, "$.values.crate.width.value", "not a decimal string"},
		{"space", data, repl(`"24"`, `" 24"`), KindData, "$.values.crate.width.value", "not a decimal string"},
		{"empty value", data, repl(`"24"`, `""`), KindData, "$.values.crate.width.value", "not a decimal string"},
		{"extra key in a value", data, repl(`"value": "24"`, `"value": "24", "note": "x"`), KindData, "$.values.crate.width.note", "only"},
		{"data contract", data, repl(`"contract": "example/world@1"`, `"contract": "example/world@2"`), KindContract, "$.contract", "schema declares"},
		{"data has an extra top key", data, repl(`"profile"`, `"extra": 1, "profile"`), KindData, "$.extra", "unknown field"},
		{"data lacks the profile", data, repl(`  "profile": "standard",`+"\n", ""), KindData, "$.profile", "missing"},
		{"empty profile", data, repl(`"standard"`, `""`), KindData, "$.profile", "non-empty"},
		{"a group where a value goes", data, repl(`"width": { "value": "24", "unit": "in" }`, `"width": { "x": { "value": "24", "unit": "in" } }`), KindData, "$.values.crate.width.x", "only"},
		{"schema contract", schema, repl(`"contract": "example/world@1"`, `"contract": "example/world@2"`), KindContract, "$.contract", "schema declares"},
		{"schema bad dimension", schema, repl(`"ratio"`, `"percent"`), KindData, "$.fields.crate.slat_gap.dimension", "not a dimension"},
		{"schema field with no meaning", schema, repl(`"share of each slat pitch left open between neighbouring slats"`, `" "`), KindData, "$.fields.crate.slat_gap.meaning", "needs a meaning"},
		{"schema extra leaf key", schema, repl(`"dimension": "angle",`, `"dimension": "angle", "unit": "deg",`), KindData, "$.fields.crate.tilt.unit", "unknown field"},
		{"schema bad name", schema, repl(`"slat_gap"`, `"Slat-Gap"`), KindData, "$.fields.crate.Slat-Gap", "not a field name"},
		{"schema unknown top key", schema, repl(`"fields"`, `"title": "x", "fields"`), KindData, "$.title", "unknown field"},
		{"data is not an object", data, func(string) string { return `[]` }, KindData, "$", "JSON object"},
	} {
		err := mutate(t, c.file, c.edit)
		if c.name == "duplicate key" {
			// The duplicate is found while reading, with a position.
			var e *Error
			if !errors.As(err, &e) || e.Kind != KindData || e.File != c.file || e.Line == 0 || !strings.Contains(e.Msg, "duplicate key") {
				t.Errorf("duplicate key: %v", err)
			}
			continue
		}
		wantErr(t, c.name, err, c.kind, c.file, c.path, c.msg)
	}
}

func TestLockFile(t *testing.T) {
	m := fixtureFS(t)
	// Missing lock.
	delete(m, "example/world.lock")
	_, err := LoadFS(m, "example/world@1")
	wantErr(t, "missing lock", err, KindLock, "example/world.lock", "", "no lock file")
	// A missing schema or data file is a data error that names the file.
	m = fixtureFS(t)
	delete(m, "example/world/standard.json")
	_, err = LoadFS(m, "example/world@1")
	wantErr(t, "missing data", err, KindData, "example/world/standard.json", "", "cannot be read")
	// The import's major does not match the package's.
	_, err = LoadFS(fixtureFS(t), "example/world@2")
	wantErr(t, "major", err, KindContract, "example/world.lock", "$.contract", "asks for contract example/world@2")
	// A bad name.
	for _, bad := range []string{"example/world", "Example/world@1", "example/../world@1", "/example@1", "example/world@0", "example/world@01", "example//world@1", "@1", "example/world@1.2", "a b@1"} {
		_, err = LoadFS(fixtureFS(t), bad)
		wantErr(t, "name "+bad, err, KindData, "", "", "is not a package name")
	}
	lock := func(edit func(string) string) error {
		m := fixtureFS(t)
		m["example/world.lock"] = &fstest.MapFile{Data: []byte(edit(string(m["example/world.lock"].Data)))}
		_, err := LoadFS(m, "example/world@1")
		return err
	}
	wantErr(t, "unknown lock key", lock(func(x string) string { return strings.Replace(x, `"schema"`, `"extra": "x", "schema"`, 1) }), KindData, "example/world.lock", "$.extra", "no field")
	wantErr(t, "lock schema", lock(func(x string) string { return strings.Replace(x, "datalock/1", "datalock/2", 1) }), KindData, "example/world.lock", "$.schema", "reads")
	wantErr(t, "escaping path", lock(func(x string) string { return strings.Replace(x, "example/world/standard.json", "../standard.json", 1) }), KindData, "example/world.lock", "$.data_file", "inside the project")
	wantErr(t, "absolute path", lock(func(x string) string { return strings.Replace(x, "example/world/standard.json", "/etc/passwd", 1) }), KindData, "example/world.lock", "$.data_file", "inside the project")
	wantErr(t, "backslash path", lock(func(x string) string {
		return strings.Replace(x, "example/world/standard.json", `example\\world\\standard.json`, 1)
	}), KindData, "example/world.lock", "$.data_file", "inside the project")
	wantErr(t, "short pin", lock(func(x string) string { return strings.Replace(x, vectorDigest, vectorDigest[:60], 1) }), KindData, "example/world.lock", "$.sha256", "64 lower-case")
	wantErr(t, "upper-case pin", lock(func(x string) string { return strings.Replace(x, vectorDigest, strings.ToUpper(vectorDigest), 1) }), KindData, "example/world.lock", "$.sha256", "64 lower-case")
	wantErr(t, "wrong pin", lock(func(x string) string { return strings.Replace(x, vectorDigest[:8], "00000000", 1) }), KindLock, "example/world.lock", "", "expected sha256 00000000")
	wantErr(t, "lock for another contract", lock(func(x string) string {
		return strings.Replace(x, `"contract": "example/world@1"`, `"contract": "example/other@1"`, 1)
	}), KindContract, "example/world.lock", "$.contract", "is the package example/other@1")
}

func TestLockBytesRoundTrip(t *testing.T) {
	m := fixtureFS(t)
	l, err := parseLock("l", m["example/world.lock"].Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(l.Bytes()) != string(m["example/world.lock"].Data) {
		t.Errorf("the fixture lock is not in the written form:\n%s", l.Bytes())
	}
	want := Lock{LockSchema, "example/world@1", "example/world/schema-v1.json", "example/world/standard.json", vectorDigest}
	if l != want {
		t.Errorf("%+v", l)
	}
}

func TestNewLockAndWriteLock(t *testing.T) {
	m := fixtureFS(t)
	delete(m, "example/world.lock")
	l, p, err := NewLock(m, "example/world@1", "example/world/schema-v1.json", "example/world/standard.json")
	if err != nil || l.SHA256 != vectorDigest || p.Digest != vectorDigest {
		t.Fatalf("%+v %v", l, err)
	}
	dir := t.TempDir()
	if err := WriteLock(dir, l); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "example", "world.lock"))
	if err != nil || string(got) != string(l.Bytes()) {
		t.Errorf("%s %v", got, err)
	}
	// NewLock refuses what Load refuses.
	m["example/world/standard.json"] = &fstest.MapFile{Data: []byte(`{"contract":"example/world@1","profile":"x","values":{}}`)}
	if _, _, err := NewLock(m, "example/world@1", "example/world/schema-v1.json", "example/world/standard.json"); err == nil {
		t.Error("NewLock pinned an incomplete data file")
	}
	if _, _, err := NewLock(m, "example/world@1", "../x.json", "example/world/standard.json"); err == nil {
		t.Error("NewLock took a path outside the project")
	}
}

func TestVerifyAndUnpinned(t *testing.T) {
	m := fixtureFS(t)
	m["example/world/standard.json"] = &fstest.MapFile{Data: []byte(strings.Replace(string(m["example/world/standard.json"].Data), `"24"`, `"25"`, 1))}
	p, err := LoadUnpinnedFS(m, "example/world@1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Digest == p.Lock.SHA256 {
		t.Error("the edit left the digest")
	}
	var e *Error
	if err := p.Verify(); !errors.As(err, &e) || e.Kind != KindLock || e.Expected != vectorDigest || e.Actual != p.Digest {
		t.Errorf("verify %v", err)
	}
}

func TestIsImport(t *testing.T) {
	for in, want := range map[string]bool{"football/world@1": true, "std/shapes": false, "helmet.gw": false, "a/b.gw": false, "x@1.gw": false, "football/world": false} {
		if IsImport(in) != want {
			t.Errorf("IsImport(%q) = %v", in, !want)
		}
	}
}

// The smallest vector: two empty objects. Hand-checkable by another
// implementation before it reads any schema.
func TestEmptyVector(t *testing.T) {
	const want = "4ae6067eb3d076297c552218bd57421b6c41dedc855d268a74672633c70ece3e"
	got, err := Digest([]byte(" { } "), []byte("{}\n"))
	if err != nil || got != want {
		t.Errorf("digest of {} and {} = %s, %v; want %s", got, err, want)
	}
}

// The reference page repeats the vectors, so that another implementation can
// check itself against them. The page and the tests must not drift.
func TestDocumentHoldsTheVectors(t *testing.T) {
	b, err := os.ReadFile("../docs/reference/data-package.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, want := range []string{
		vectorSchemaCanonical, vectorDataCanonical, vectorDigest,
		"4ae6067eb3d076297c552218bd57421b6c41dedc855d268a74672633c70ece3e",
		string(readFixture(t, "schema-v1.json"))[:len("{\n  \"contract\"")],
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("data-package.md lacks %q", want)
		}
	}
	for _, f := range []string{"schema-v1.json", "standard.json"} {
		if !strings.Contains(doc, strings.TrimSpace(string(readFixture(t, f)))) {
			t.Errorf("data-package.md does not show %s as it is in the fixture", f)
		}
	}
	lock, _ := os.ReadFile(filepath.Join(fixtureRoot, "example", "world.lock"))
	if !strings.Contains(doc, strings.TrimSpace(string(lock))) {
		t.Error("data-package.md does not show the fixture lock")
	}
}
