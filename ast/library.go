package ast

import (
	"sync"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
	"m31labs.dev/greenware/std"
)

type libEntry struct {
	file *File
	ds   []diag.Diagnostic
}

var (
	libMu    sync.Mutex
	libCache = map[string]*libEntry{}
)

// Library lowers the standard library file an import path names, such as
// "std/finish". ok is false when the path names no shipped library. The
// diagnostics are those of the library's own source: a shipped library has
// none, and a test enforces that. The returned file is shared and must not be
// changed.
func Library(path string) (f *File, ds []diag.Diagnostic, ok bool) {
	libMu.Lock()
	defer libMu.Unlock()
	if e, hit := libCache[path]; hit {
		return e.file, e.ds, true
	}
	src, found := std.Source(path)
	if !found {
		return nil, nil, false
	}
	f, ds = LowerWith(lang.Parse(src), Options{Library: true})
	libCache[path] = &libEntry{f, ds}
	return f, ds, true
}
