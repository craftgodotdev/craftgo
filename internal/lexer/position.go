package lexer

import "fmt"

// Position is a location in a source file. The zero value is invalid.
type Position struct {
	Filename string // may be empty
	Offset   int    // 0-based byte offset
	Line     int    // 1-based
	Column   int    // 1-based, counted in runes
}

// String renders the position as `file:line:col`, or `line:col` without a
// filename.
func (p Position) String() string {
	if p.Filename != "" {
		return fmt.Sprintf("%s:%d:%d", p.Filename, p.Line, p.Column)
	}
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// IsValid reports whether p is set rather than the zero value.
func (p Position) IsValid() bool {
	return p.Line > 0
}
