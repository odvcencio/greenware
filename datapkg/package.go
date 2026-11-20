package datapkg

import (
	"bytes"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strings"

	"m31labs.dev/greenware/units"
)

// Dimension is what a field measures.
type Dimension string

// The four dimensions of a field.
const (
	Length Dimension = "length"
	Angle  Dimension = "angle"
	Ratio  Dimension = "ratio"
	Number Dimension = "number"
)

// unitTable gives each unit its dimension and its exact factor to the
// canonical unit: metres for a length, a fraction of one for a ratio. Angles
// are converted by Float64 (degrees multiply the exact degree value by the
// binary64 constant units.DegToRad, as a literal `90deg` does).
var unitTable = map[string]struct {
	dim      Dimension
	num, den int64
}{
	"mm": {Length, 1, 1000}, "cm": {Length, 1, 100}, "m": {Length, 1, 1}, "km": {Length, 1000, 1},
	"in": {Length, 254, 10000}, "ft": {Length, 3048, 10000}, "yd": {Length, 9144, 10000},
	"deg": {Angle, 1, 1}, "rad": {Angle, 1, 1},
	"%": {Ratio, 1, 100},
}

// Units lists the units of a dimension in the order the documentation names
// them; Number has none.
func Units(d Dimension) []string {
	switch d {
	case Length:
		return []string{"mm", "cm", "m", "km", "in", "ft", "yd"}
	case Angle:
		return []string{"deg", "rad"}
	case Ratio:
		return []string{"%"}
	}
	return nil
}

var (
	fieldRE   = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	decimalRE = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)
)

const (
	maxDecimalLen = 64
	maxFieldDepth = 8
)

// Field is one declared field of the schema.
type Field struct {
	// Path is the field's name path, for example ["goal_post", "inside_width"].
	Path      []string
	Dimension Dimension
	// Meaning says what is measured, and from what surface.
	Meaning string
}

// Name is the dotted path of the field.
func (f Field) Name() string { return strings.Join(f.Path, ".") }

// Value is one value of the data file with the schema's field.
type Value struct {
	Field
	// Text is the decimal string as written, Unit the unit as written (empty
	// for a number).
	Text string
	Unit string
	// Rat is the value exactly, in the written unit.
	Rat *big.Rat
}

// Exact returns the value as an exact rational in the canonical unit of its
// dimension, and that unit: metres ("m") for a length; the fraction of one
// for a ratio ("" ; 50 % is 1/2); the value itself for a number (""); and for
// an angle the value in the unit it was written in ("deg" or "rad", because
// a degree is not a rational number of radians).
func (v Value) Exact() (*big.Rat, string) {
	switch v.Dimension {
	case Length:
		u := unitTable[v.Unit]
		return new(big.Rat).Mul(v.Rat, big.NewRat(u.num, u.den)), "m"
	case Ratio:
		u := unitTable[v.Unit]
		return new(big.Rat).Mul(v.Rat, big.NewRat(u.num, u.den)), ""
	case Angle:
		return new(big.Rat).Set(v.Rat), v.Unit
	}
	return new(big.Rat).Set(v.Rat), ""
}

// Float64 meets float64 once: metres for a length, radians for an angle, a
// fraction for a ratio, the number for a number. The exact value is rounded
// to the nearest binary64 (ties to even); an angle in degrees then multiplies
// by units.DegToRad, the same two steps as a `90deg` literal. The result is
// bit-identical to the literal written in the same unit.
func (v Value) Float64() float64 {
	r, unit := v.Exact()
	f, _ := r.Float64()
	if v.Dimension == Angle && unit == "deg" {
		return float64(f * units.DegToRad)
	}
	return f
}

// Input is what a built file records about one package.
type Input struct {
	Contract string `json:"contract"`
	Profile  string `json:"profile"`
	SHA256   string `json:"sha256"`
}

// Package is a loaded and checked data package.
type Package struct {
	// Logical is the import path as written: "football/world@1".
	Logical string
	// Contract is the contract id the schema, the data file and the lock all
	// name; it equals Logical.
	Contract string
	// LockFile, SchemaFile and DataFile are project-relative paths.
	LockFile   string
	SchemaFile string
	DataFile   string
	// Lock is the lock as read from LockFile.
	Lock Lock
	// Profile is the data file's `profile`.
	Profile string
	// Digest is the digest of the files as they are now; the pin in Lock is
	// Lock.SHA256. Load has checked that they are equal.
	Digest string
	// SchemaCanonical and DataCanonical are the canonical forms the digest covers.
	SchemaCanonical []byte
	DataCanonical   []byte

	fields []Field // sorted by path
	decl   map[string]Field
	values map[string]Value
}

// Input returns the record of this package for a build.
func (p *Package) Input() Input {
	return Input{Contract: p.Contract, Profile: p.Profile, SHA256: p.Digest}
}

// Fields returns every declared field, sorted by dotted path.
func (p *Package) Fields() []Field { return append([]Field(nil), p.fields...) }

// Names returns the dotted path of every field, sorted.
func (p *Package) Names() []string {
	out := make([]string, len(p.fields))
	for i, f := range p.fields {
		out[i] = f.Name()
	}
	return out
}

// Value returns the value of a field: Value("goal_post", "inside_width").
// Each argument is one name; an argument with dots is also read as a path, so
// Value("goal_post.inside_width") works.
func (p *Package) Value(path ...string) (Value, bool) {
	v, ok := p.values[strings.Join(path, ".")]
	return v, ok
}

// Field returns the declaration of a field in the same way as Value.
func (p *Package) Field(path ...string) (Field, bool) {
	f, ok := p.decl[strings.Join(path, ".")]
	return f, ok
}

// Group returns the names of the fields and groups directly under a group
// path (nil path: the top level), sorted, and whether the path is a group.
func (p *Package) Group(path ...string) ([]string, bool) {
	prefix := strings.Join(path, ".")
	if prefix != "" {
		prefix += "."
	}
	seen := map[string]bool{}
	var out []string
	for _, f := range p.fields {
		n := f.Name()
		if !strings.HasPrefix(n, prefix) || n == strings.TrimSuffix(prefix, ".") {
			continue
		}
		next := strings.SplitN(strings.TrimPrefix(n, prefix), ".", 2)[0]
		if !seen[next] {
			seen[next] = true
			out = append(out, next)
		}
	}
	return out, len(out) > 0
}

// Verify checks the pin: the digest of the files must equal the lock's.
func (p *Package) Verify() error {
	if p.Digest == p.Lock.SHA256 {
		return nil
	}
	return &Error{Kind: KindLock, File: p.LockFile,
		Msg: fmt.Sprintf("the files of %s do not match the pin: expected sha256 %s, actual %s; review the change to %s and %s, then run `greenware lock --data %s`",
			p.Logical, p.Lock.SHA256, p.Digest, p.SchemaFile, p.DataFile, p.Logical),
		Expected: p.Lock.SHA256, Actual: p.Digest}
}

// Load reads the package `logical` ("football/world@1") from the project at
// root, checks it and verifies its pin. See LoadFS.
func Load(root, logical string) (*Package, error) {
	return LoadFS(os.DirFS(root), logical)
}

// LoadFS is Load on a file system rooted at the project root. It resolves
// `football/world@1` to `football/world.lock`, reads the schema and data files
// the lock names, parses all three strictly, checks the data against the
// schema and the contract against the import, and compares the digest with
// the pin. Nothing is fetched and nothing defaults. A failure is an *Error.
func LoadFS(fsys fs.FS, logical string) (*Package, error) {
	p, err := LoadUnpinnedFS(fsys, logical)
	if err != nil {
		return nil, err
	}
	if err := p.Verify(); err != nil {
		return nil, err
	}
	return p, nil
}

// LoadUnpinned is Load without the pin check: the files must parse and agree,
// but their digest may differ from the lock's. `greenware lock --data` uses
// it. Never use it to build.
func LoadUnpinned(root, logical string) (*Package, error) {
	return LoadUnpinnedFS(os.DirFS(root), logical)
}

// LoadUnpinnedFS is LoadUnpinned on a file system.
func LoadUnpinnedFS(fsys fs.FS, logical string) (*Package, error) {
	name, _, perr := ParseLogical(logical)
	if perr != nil {
		return nil, errf(KindData, "", "", "%v", perr)
	}
	lockFile := name + ".lock"
	lb, rerr := fs.ReadFile(fsys, lockFile)
	if rerr != nil {
		return nil, errf(KindLock, lockFile, "", "there is no lock file for %s (%s: %s); create it with `greenware lock --data %s --schema-file <schema.json> --data-file <data.json>`", logical, lockFile, reason(rerr), logical)
	}
	lock, err := parseLock(lockFile, lb)
	if err != nil {
		return nil, err
	}
	if lock.Contract != logical {
		return nil, errf(KindContract, lockFile, "$.contract", "the import asks for contract %s, but %s is the package %s", logical, lockFile, lock.Contract)
	}
	p, perr2 := loadFiles(fsys, logical, lockFile, lock)
	if perr2 != nil {
		return nil, perr2
	}
	return p, nil
}

func reason(err error) string {
	if os.IsNotExist(err) {
		return "no such file"
	}
	return err.Error()
}

func loadFiles(fsys fs.FS, logical, lockFile string, lock Lock) (*Package, *Error) {
	sb, rerr := fs.ReadFile(fsys, lock.SchemaFile)
	if rerr != nil {
		return nil, errf(KindData, lock.SchemaFile, "", "the schema file named by %s cannot be read: %s", lockFile, reason(rerr))
	}
	db, rerr := fs.ReadFile(fsys, lock.DataFile)
	if rerr != nil {
		return nil, errf(KindData, lock.DataFile, "", "the data file named by %s cannot be read: %s", lockFile, reason(rerr))
	}
	sn, err := parseJSON(lock.SchemaFile, sb)
	if err != nil {
		return nil, err
	}
	dn, err := parseJSON(lock.DataFile, db)
	if err != nil {
		return nil, err
	}
	p := &Package{Logical: logical, LockFile: lockFile, SchemaFile: lock.SchemaFile, DataFile: lock.DataFile, Lock: lock, decl: map[string]Field{}, values: map[string]Value{}}
	if err := p.readSchema(sn); err != nil {
		return nil, err
	}
	if err := p.readData(dn); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	writeCanonical(&buf, sn)
	p.SchemaCanonical = append([]byte(nil), buf.Bytes()...)
	buf.Reset()
	writeCanonical(&buf, dn)
	p.DataCanonical = append([]byte(nil), buf.Bytes()...)
	p.Digest = digestOf(p.SchemaCanonical, p.DataCanonical)
	return p, nil
}

// NewLock reads and checks the package whose schema and data files are given
// (project-relative) and returns the lock that pins them, with the checked
// package. It is what `greenware lock --data` writes with WriteLock.
func NewLock(fsys fs.FS, logical, schemaFile, dataFile string) (Lock, *Package, error) {
	if _, _, err := ParseLogical(logical); err != nil {
		return Lock{}, nil, errf(KindData, "", "", "%v", err)
	}
	for _, f := range []string{schemaFile, dataFile} {
		if !validFile(f) {
			return Lock{}, nil, errf(KindData, f, "", "%s is not a slash-separated path inside the project", quote(f))
		}
	}
	lockFile, _ := LockPath(logical)
	l := Lock{Schema: LockSchema, Contract: logical, SchemaFile: schemaFile, DataFile: dataFile}
	p, err := loadFiles(fsys, logical, lockFile, l)
	if err != nil {
		return Lock{}, nil, err
	}
	l.SHA256 = p.Digest
	p.Lock = l
	return l, p, nil
}

func (p *Package) readSchema(n *node) *Error {
	file := p.SchemaFile
	if n.kind != nObject {
		return errf(KindData, file, "$", "a schema is a JSON object, not %s", n.kind)
	}
	if err := exactKeys(file, "$", n, "contract", "fields"); err != nil {
		return err
	}
	c := n.get("contract")
	if c.kind != nString {
		return errf(KindData, file, "$.contract", "must be a string, not %s", c.kind)
	}
	if c.s != p.Logical {
		return errf(KindContract, file, "$.contract", "the schema declares contract %s, but the lock and the import name %s", quote(c.s), quote(p.Logical))
	}
	p.Contract = c.s
	f := n.get("fields")
	if f.kind != nObject || len(f.members) == 0 {
		return errf(KindData, file, "$.fields", "must be an object that declares at least one field")
	}
	if err := p.schemaGroup(f, nil, "$.fields"); err != nil {
		return err
	}
	sort.Slice(p.fields, func(i, j int) bool { return p.fields[i].Name() < p.fields[j].Name() })
	return nil
}

func (p *Package) schemaGroup(n *node, path []string, jp string) *Error {
	file := p.SchemaFile
	if len(path) >= maxFieldDepth {
		return errf(KindData, file, jp, "fields nest deeper than %d names", maxFieldDepth)
	}
	for _, m := range n.members {
		mp := jp + "." + m.key
		if !fieldRE.MatchString(m.key) {
			return errf(KindData, file, mp, "%s is not a field name: use lower-case letters, digits and `_`, not starting with a digit", quote(m.key))
		}
		if m.val.kind != nObject {
			return errf(KindData, file, mp, "must be an object (a field with `dimension` and `meaning`, or a group of fields), not %s", m.val.kind)
		}
		np := append(append([]string(nil), path...), m.key)
		if m.val.get("dimension") != nil || m.val.get("meaning") != nil {
			if err := exactKeys(file, mp, m.val, "dimension", "meaning"); err != nil {
				return err
			}
			d, me := m.val.get("dimension"), m.val.get("meaning")
			if d.kind != nString {
				return errf(KindData, file, mp+".dimension", "must be a string, not %s", d.kind)
			}
			if me.kind != nString {
				return errf(KindData, file, mp+".meaning", "must be a string, not %s", me.kind)
			}
			switch dim := Dimension(d.s); dim {
			case Length, Angle, Ratio, Number:
			default:
				return errf(KindData, file, mp+".dimension", "%s is not a dimension; use length, angle, ratio or number", quote(d.s))
			}
			if strings.TrimSpace(me.s) == "" {
				return errf(KindData, file, mp+".meaning", "a field needs a meaning: say what is measured and from what surface")
			}
			f := Field{Path: np, Dimension: Dimension(d.s), Meaning: me.s}
			p.fields = append(p.fields, f)
			p.decl[f.Name()] = f
			continue
		}
		if len(m.val.members) == 0 {
			return errf(KindData, file, mp, "a group must declare at least one field")
		}
		if err := p.schemaGroup(m.val, np, mp); err != nil {
			return err
		}
	}
	return nil
}

func (p *Package) readData(n *node) *Error {
	file := p.DataFile
	if n.kind != nObject {
		return errf(KindData, file, "$", "a data file is a JSON object, not %s", n.kind)
	}
	if err := exactKeys(file, "$", n, "contract", "profile", "values"); err != nil {
		return err
	}
	c := n.get("contract")
	if c.kind != nString {
		return errf(KindData, file, "$.contract", "must be a string, not %s", c.kind)
	}
	if c.s != p.Contract {
		return errf(KindContract, file, "$.contract", "the data file is for contract %s, but the schema declares %s", quote(c.s), quote(p.Contract))
	}
	pr := n.get("profile")
	if pr.kind != nString || strings.TrimSpace(pr.s) == "" {
		return errf(KindData, file, "$.profile", "must be a non-empty string")
	}
	p.Profile = pr.s
	v := n.get("values")
	if v.kind != nObject {
		return errf(KindData, file, "$.values", "must be an object, not %s", v.kind)
	}
	return p.dataGroup(v, nil, "$.values")
}

// dataGroup walks the data object against the schema fields under path.
func (p *Package) dataGroup(n *node, path []string, jp string) *Error {
	file := p.DataFile
	children, _ := p.Group(path...)
	known := map[string]bool{}
	for _, c := range children {
		known[c] = true
	}
	for _, m := range n.members {
		if !known[m.key] {
			e := errf(KindData, file, jp+"."+m.key, "the schema declares no field %s here", quote(m.key))
			if len(children) > 0 {
				e.Msg += "; it declares " + strings.Join(children, ", ")
			}
			return e
		}
	}
	for _, c := range children {
		cp, cj := append(append([]string(nil), path...), c), jp+"."+c
		m := n.get(c)
		if m == nil {
			return errf(KindData, file, cj, "the data file has no value for %s", quote(strings.Join(cp, ".")))
		}
		if f, ok := p.Field(cp...); ok {
			if err := p.dataLeaf(m, f, cj); err != nil {
				return err
			}
			continue
		}
		if m.kind != nObject {
			return errf(KindData, file, cj, "must be an object of values, not %s", m.kind)
		}
		if err := p.dataGroup(m, cp, cj); err != nil {
			return err
		}
	}
	return nil
}

func (p *Package) dataLeaf(n *node, f Field, jp string) *Error {
	file := p.DataFile
	if n.kind != nObject {
		return errf(KindData, file, jp, "must be an object like {\"value\":\"222\",\"unit\":\"in\"}, not %s", n.kind)
	}
	keys := []string{"value", "unit"}
	if f.Dimension == Number {
		keys = keys[:1]
	}
	for _, m := range n.members {
		ok := false
		for _, k := range keys {
			ok = ok || k == m.key
		}
		if !ok {
			msg := "a value has only %s"
			if f.Dimension == Number && m.key == "unit" {
				msg = "a number has no unit; a value has only %s"
			}
			return errf(KindData, file, jp+"."+m.key, msg, "`"+strings.Join(keys, "` and `")+"`")
		}
	}
	vn := n.get("value")
	if vn == nil {
		return errf(KindData, file, jp+".value", "the value is missing")
	}
	if vn.kind != nString {
		return errf(KindData, file, jp+".value", "must be a decimal string such as \"222\", not %s", vn.kind)
	}
	if len(vn.s) > maxDecimalLen || !decimalRE.MatchString(vn.s) {
		return errf(KindData, file, jp+".value", "%s is not a decimal string: write digits with an optional leading `-` and an optional fraction, for example \"222\" or \"-1.5\"", quote(vn.s))
	}
	rat, _ := new(big.Rat).SetString(vn.s)
	v := Value{Field: f, Text: vn.s, Rat: rat}
	if f.Dimension != Number {
		un := n.get("unit")
		if un == nil {
			return errf(KindData, file, jp+".unit", "the unit is missing; %s uses %s", f.Name(), unitList(f.Dimension))
		}
		if un.kind != nString {
			return errf(KindData, file, jp+".unit", "must be a string, not %s", un.kind)
		}
		def, ok := unitTable[un.s]
		if !ok {
			return errf(KindData, file, jp+".unit", "%s is not a unit; %s uses %s", quote(un.s), f.Name(), unitList(f.Dimension))
		}
		if def.dim != f.Dimension {
			return errf(KindUnit, file, jp+".unit", "%s is a unit of %s, but %s is a %s; use %s", quote(un.s), def.dim, f.Name(), f.Dimension, unitList(f.Dimension))
		}
		v.Unit = un.s
	}
	if f.Dimension == Number {
		v.Unit = ""
	}
	p.values[f.Name()] = v
	return nil
}

func unitList(d Dimension) string {
	return strings.Join(Units(d), ", ")
}

// exactKeys checks that n has each of keys and no other key.
func exactKeys(file, jp string, n *node, keys ...string) *Error {
	for _, m := range n.members {
		ok := false
		for _, k := range keys {
			ok = ok || k == m.key
		}
		if !ok {
			return errf(KindData, file, jp+"."+m.key, "unknown field %s; the fields here are %s", quote(m.key), strings.Join(keys, ", "))
		}
	}
	for _, k := range keys {
		if n.get(k) == nil {
			return errf(KindData, file, jp+"."+k, "the field %s is missing", quote(k))
		}
	}
	return nil
}
