package datapkg

import (
	"fmt"
	"strings"
)

// Kind says which compiler diagnostic an Error becomes.
type Kind int

const (
	// KindData is a file that cannot be read, parsed or checked against the
	// schema (GW-IMPORT-DATA).
	KindData Kind = iota
	// KindUnit is a unit of the wrong dimension for its field (GW-UNIT).
	KindUnit
	// KindContract is a contract id that does not match the one asked for, or
	// the one the lock, the schema and the data file name (GW-IMPORT-CONTRACT).
	KindContract
	// KindLock is a missing lock file or a pin that does not match the files
	// (GW-LOCK).
	KindLock
)

func (k Kind) String() string {
	switch k {
	case KindUnit:
		return "unit"
	case KindContract:
		return "contract"
	case KindLock:
		return "lock"
	}
	return "data"
}

// Error is a failure to load a package. File is the project-relative file,
// Path the JSON path inside it ("$.values.crate.width.unit"), Line and Col the
// 1-based position of a parse error (zero when not known). Expected and Actual
// hold the two digests of a KindLock pin mismatch.
type Error struct {
	Kind     Kind
	File     string
	Path     string
	Line     int
	Col      int
	Msg      string
	Expected string
	Actual   string
}

func (e *Error) Error() string {
	var b strings.Builder
	if e.File != "" {
		b.WriteString(e.File)
		if e.Line > 0 {
			fmt.Fprintf(&b, ":%d:%d", e.Line, e.Col)
		}
		b.WriteString(": ")
	}
	if e.Path != "" {
		b.WriteString(e.Path)
		b.WriteString(": ")
	}
	b.WriteString(e.Msg)
	return b.String()
}

func errf(kind Kind, file, path, format string, args ...any) *Error {
	return &Error{Kind: kind, File: file, Path: path, Msg: fmt.Sprintf(format, args...)}
}
