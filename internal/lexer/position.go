// Package lexer tokenizes craftgo DSL source files.
//
// The lexer performs a single linear pass over UTF-8 input and produces a flat
// stream of [Token] values, each tagged with a [Position]. Errors are recorded
// as [Diagnostic] entries (retrievable via [Lexer.Diagnostics]) and surfaced as
// tokens of [Kind] [Error] in the stream — the lexer never panics or aborts on
// malformed input. Downstream phases (parser, semantic, LSP) all consume this
// same token stream so diagnostics are consistent across CLI and IDE.
package lexer

import "fmt"

// Position identifies a single byte location in a source file.
//
// The zero value is invalid (Line == 0). All non-zero positions use 1-indexed
// Line/Column counts; Offset is a 0-indexed byte offset suitable for slicing
// into the original source. Filename is optional — when empty, [Position.String]
// omits it.
type Position struct {
	// Filename is the path or label of the source file (may be empty).
	Filename string
	// Offset is the 0-indexed byte offset into the source.
	Offset int
	// Line is the 1-indexed line number.
	Line int
	// Column is the 1-indexed rune column on the current line.
	Column int
}

// String renders the position as `file:line:col` (or `line:col` when Filename
// is empty). Output matches the convention used by `go vet`, `gopls`, and most
// editors so that error messages are clickable.
func (p Position) String() string {
	if p.Filename != "" {
		return fmt.Sprintf("%s:%d:%d", p.Filename, p.Line, p.Column)
	}
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// IsValid reports whether the position has been initialised. A position is
// considered valid once Line > 0; the zero-value [Position] is invalid.
func (p Position) IsValid() bool {
	return p.Line > 0
}
