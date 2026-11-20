package lsp

import (
	"bytes"
	"encoding/json"
	"errors"

	"m31labs.dev/greenware/diag"
	"m31labs.dev/greenware/lang"
)

// formatting formats the whole document. It sends one edit that covers only
// the lines that changed, and none when the text is already formatted. A
// document with syntax errors cannot be formatted: the request fails.
func (s *Server) formatting(params json.RawMessage) (any, *respError) {
	var p struct {
		TextDocument textDocumentIdentifier `json:"textDocument"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	d := s.doc(p.TextDocument.URI)
	if d == nil {
		return []TextEdit{}, nil
	}
	out, err := lang.Format(d.text)
	if err != nil {
		var fe *lang.FormatError
		if errors.As(err, &fe) {
			return nil, &respError{codeRequestFailed, err.Error()}
		}
		return nil, &respError{codeInternalError, err.Error()}
	}
	return d.lineDiff(out), nil
}

// lineDiff returns the edit that turns the document into next, trimmed to the
// lines that differ.
func (d *document) lineDiff(next []byte) []TextEdit {
	old := d.text
	if bytes.Equal(old, next) {
		return []TextEdit{}
	}
	// Common prefix and suffix, cut back to line starts.
	pre := 0
	for pre < len(old) && pre < len(next) && old[pre] == next[pre] {
		pre++
	}
	for pre > 0 && old[pre-1] != '\n' {
		pre--
	}
	suf := 0
	for suf < len(old)-pre && suf < len(next)-pre && old[len(old)-1-suf] == next[len(next)-1-suf] {
		suf++
	}
	for suf > 0 && !(atLineStart(old, len(old)-suf) && atLineStart(next, len(next)-suf)) {
		suf--
	}
	oldEnd, newEnd := len(old)-suf, len(next)-suf
	return []TextEdit{{
		Range:   d.rangeOf(diag.Span{Start: pre, End: oldEnd}),
		NewText: string(next[pre:newEnd]),
	}}
}

// codeAction offers the edits of the diagnostics that meet the requested
// range: one quick fix for each alternative edit.
func (s *Server) codeAction(params json.RawMessage) (any, *respError) {
	var p struct {
		TextDocument textDocumentIdentifier `json:"textDocument"`
		Range        Range                  `json:"range"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	out := []CodeAction{}
	d := s.doc(p.TextDocument.URI)
	if d == nil {
		return out, nil
	}
	lo, hi := d.offset(p.Range.Start), d.offset(p.Range.End)
	if hi < lo {
		lo, hi = hi, lo
	}
	for _, x := range d.diags {
		if len(x.Edits) == 0 {
			continue
		}
		// The ranges meet when they overlap or touch.
		if x.Span.End < lo || x.Span.Start > hi {
			continue
		}
		for _, e := range x.Edits {
			title := e.Title
			if title == "" {
				title = "Fix " + x.Code
			}
			out = append(out, CodeAction{
				Title:       title,
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{d.lspDiagnostic(x)},
				IsPreferred: len(x.Edits) == 1,
				Edit: WorkspaceEdit{Changes: map[string][]TextEdit{
					d.uri: {{Range: d.rangeOf(e.Span), NewText: e.NewText}},
				}},
			})
		}
	}
	return out, nil
}

func atLineStart(b []byte, i int) bool { return i == 0 || b[i-1] == '\n' }
