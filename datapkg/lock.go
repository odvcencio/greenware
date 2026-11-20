package datapkg

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Schema names of the lock file and the digest.
const (
	LockSchema   = "greenware.datalock/1"
	DigestPrefix = "greenware.datapkg/1"
)

// Lock is the lock file of a package: where its schema and data files are and
// the pin of both. Paths are slash-separated and relative to the project root.
type Lock struct {
	Schema     string
	Contract   string
	SchemaFile string
	DataFile   string
	SHA256     string
}

var (
	nameRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(/[a-z0-9][a-z0-9_-]*)*$`)
	majorRE  = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)
	digestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// IsImport reports whether an import path names a data package rather than a
// library (`std/...`) or a project file (`....gw`): it has an `@`.
func IsImport(importPath string) bool {
	return !strings.HasSuffix(importPath, ".gw") && !strings.HasPrefix(importPath, "std/") && strings.Contains(importPath, "@")
}

// ParseLogical splits `football/world@1` into the package name and the
// contract major.
func ParseLogical(logical string) (name string, major int, err error) {
	name, m, ok := strings.Cut(logical, "@")
	if !ok || !nameRE.MatchString(name) || !majorRE.MatchString(m) {
		return "", 0, fmt.Errorf("%q is not a package name: write lower-case path words, `@` and a major version, for example football/world@1", logical)
	}
	major, _ = strconv.Atoi(m)
	return name, major, nil
}

// LockPath is the project-relative path of the lock file of a package:
// `football/world@1` is `football/world.lock`.
func LockPath(logical string) (string, error) {
	name, _, err := ParseLogical(logical)
	if err != nil {
		return "", err
	}
	return name + ".lock", nil
}

// validFile reports whether p is a slash-separated path inside the project.
func validFile(p string) bool {
	return p != "" && !strings.Contains(p, `\`) && !strings.HasPrefix(p, "/") && fs.ValidPath(p) && p != "."
}

// ParseLock reads a lock file. Every key is required, none is optional and an
// unknown key is an error. file names the file in an error.
func ParseLock(file string, b []byte) (Lock, error) {
	l, err := parseLock(file, b)
	if err != nil {
		return Lock{}, err
	}
	return l, nil
}

func parseLock(file string, b []byte) (Lock, *Error) {
	n, err := parseJSON(file, b)
	if err != nil {
		return Lock{}, err
	}
	var l Lock
	if n.kind != nObject {
		return l, errf(KindData, file, "$", "a lock file is a JSON object, not %s", n.kind)
	}
	want := []string{"schema", "contract", "schema_file", "data_file", "sha256"}
	for _, m := range n.members {
		known := false
		for _, w := range want {
			known = known || w == m.key
		}
		if !known {
			return l, errf(KindData, file, "$."+m.key, "a lock file has no field %s; its fields are %s", quote(m.key), strings.Join(want, ", "))
		}
	}
	vals := map[string]string{}
	for _, w := range want {
		v := n.get(w)
		if v == nil {
			return l, errf(KindData, file, "$."+w, "the lock file has no field %s", quote(w))
		}
		if v.kind != nString {
			return l, errf(KindData, file, "$."+w, "must be a string, not %s", v.kind)
		}
		vals[w] = v.s
	}
	l = Lock{vals["schema"], vals["contract"], vals["schema_file"], vals["data_file"], vals["sha256"]}
	if l.Schema != LockSchema {
		return l, errf(KindData, file, "$.schema", "the lock schema is %s; this Greenware reads %s", quote(l.Schema), quote(LockSchema))
	}
	if _, _, e := ParseLogical(l.Contract); e != nil {
		return l, errf(KindData, file, "$.contract", "%v", e)
	}
	for _, f := range []struct{ key, val string }{{"schema_file", l.SchemaFile}, {"data_file", l.DataFile}} {
		if !validFile(f.val) {
			return l, errf(KindData, file, "$."+f.key, "%s is not a slash-separated path inside the project", quote(f.val))
		}
	}
	if !digestRE.MatchString(l.SHA256) {
		return l, errf(KindData, file, "$.sha256", "the pin must be 64 lower-case hex digits")
	}
	return l, nil
}

// Bytes returns the lock file as WriteLock writes it: two-space indent, the
// fields in a fixed order, one final newline. A reader accepts any key order
// and any white space.
func (l Lock) Bytes() []byte {
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, kv := range [][2]string{{"schema", l.Schema}, {"contract", l.Contract}, {"schema_file", l.SchemaFile}, {"data_file", l.DataFile}, {"sha256", l.SHA256}} {
		b.WriteString("  ")
		writeString(&b, kv[0])
		b.WriteString(": ")
		writeString(&b, kv[1])
		if i < 4 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("}\n")
	return b.Bytes()
}

// WriteLock writes the lock file of l.Contract under root, at
// `<root>/<name>.lock`, through a temporary file and a rename. It does not
// check the files; NewLock does that and sets the pin.
func WriteLock(root string, l Lock) error {
	rel, err := LockPath(l.Contract)
	if err != nil {
		return err
	}
	if l.Schema == "" {
		l.Schema = LockSchema
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".greenware-lock-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(l.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), full)
}
