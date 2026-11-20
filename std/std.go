// Package std is the standard library of Greenware: ordinary Greenware source
// that ships inside the compiler (compiler.md: `std/` is embedded source, not a
// Go plugin package). `import "std/finish"` resolves here.
//
// A library is subject to the same unit, name, expansion and work rules as
// project code. It has no privileged executable hooks: the compiler reads
// these files with the same parser and evaluator it uses for a project, and
// pins their bytes by SHA-256 in the build's input hash. A change to a file
// here is a change to the products that import it.
package std

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"sort"
	"strings"
)

//go:embed *.gw
var files embed.FS

// Prefix starts the path of every standard library import.
const Prefix = "std/"

// Planned lists the library names the language reserves and this compiler does
// not ship yet.
var Planned = []string{"cloth", "hardware", "kinds", "lettering"}

// Names returns the names of the shipped libraries, sorted: "finish" for the
// import "std/finish".
func Names() []string {
	ents, err := files.ReadDir(".")
	if err != nil {
		panic("std: " + err.Error())
	}
	var out []string
	for _, e := range ents {
		out = append(out, strings.TrimSuffix(e.Name(), ".gw"))
	}
	sort.Strings(out)
	return out
}

// Source returns the bytes of the library an import path names, such as
// "std/finish".
func Source(path string) ([]byte, bool) {
	name, ok := strings.CutPrefix(path, Prefix)
	if !ok || name == "" || strings.ContainsAny(name, "/\\.") {
		return nil, false
	}
	b, err := files.ReadFile(name + ".gw")
	if err != nil {
		return nil, false
	}
	return b, true
}

// Digest returns the hex SHA-256 of a library's bytes, the pin that the
// build's input hash records.
func Digest(path string) (string, bool) {
	b, ok := Source(path)
	if !ok {
		return "", false
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:]), true
}

// IsPlanned reports whether the path names a reserved library that is not
// shipped.
func IsPlanned(path string) bool {
	name, ok := strings.CutPrefix(path, Prefix)
	if !ok {
		return false
	}
	for _, p := range Planned {
		if p == name {
			return true
		}
	}
	return false
}
