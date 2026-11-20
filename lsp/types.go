package lsp

// The LSP structures the server reads and writes. Only the fields it uses
// appear here.

// Position is a zero-based line and a character offset in UTF-16 code units.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a half-open span between two positions.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a range in a document.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// TextEdit replaces a range with new text.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// WorkspaceEdit holds edits by document URI.
type WorkspaceEdit struct {
	Changes map[string][]TextEdit `json:"changes"`
}

// Diagnostic severities.
const (
	sevError   = 1
	sevWarning = 2
	sevInfo    = 3
)

type relatedInformation struct {
	Location Location `json:"location"`
	Message  string   `json:"message"`
}

// Diagnostic is one published problem.
type Diagnostic struct {
	Range              Range                `json:"range"`
	Severity           int                  `json:"severity"`
	Code               string               `json:"code"`
	Source             string               `json:"source"`
	Message            string               `json:"message"`
	RelatedInformation []relatedInformation `json:"relatedInformation,omitempty"`
}

// CodeAction is a quick fix.
type CodeAction struct {
	Title       string        `json:"title"`
	Kind        string        `json:"kind"`
	Diagnostics []Diagnostic  `json:"diagnostics,omitempty"`
	IsPreferred bool          `json:"isPreferred,omitempty"`
	Edit        WorkspaceEdit `json:"edit"`
}

// MarkupContent is hover or documentation text.
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Hover is the answer to a hover request.
type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// DocumentSymbol is one node of the outline.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// CompletionItem is one completion candidate.
type CompletionItem struct {
	Label         string         `json:"label"`
	Kind          int            `json:"kind,omitempty"`
	Detail        string         `json:"detail,omitempty"`
	Documentation *MarkupContent `json:"documentation,omitempty"`
	SortText      string         `json:"sortText,omitempty"`
	FilterText    string         `json:"filterText,omitempty"`
	TextEdit      *TextEdit      `json:"textEdit,omitempty"`
}

// CompletionList is the answer to a completion request.
type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type textDocumentPosition struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}
