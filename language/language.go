// Package language holds the names that identify Greenware: its name, the
// word that opens a file, the file extension and the diagnostic prefix. The
// grammar, the parser and the tools read them from here, so each is written
// once.
package language

const (
	// Name is the language name.
	Name = "greenware"
	// HeaderWord opens a file's edition line: "greenware 1".
	HeaderWord = "greenware"
	// Extension is the file extension of a Greenware source file.
	Extension = ".gw"
	// DiagnosticPrefix begins every diagnostic code, such as GW-SYNTAX.
	DiagnosticPrefix = "GW-"
)
