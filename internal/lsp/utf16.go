// UTF-16 ↔ internal position conversion. The LSP protocol measures
// Position.Character in UTF-16 code units, whereas craftgo's lexer counts
// runes (Position.Column) and bytes (Position.Offset). A character in the
// Basic Multilingual Plane (<= U+FFFF) is one UTF-16 unit; a supplementary
// character (emoji, rare CJK, ...) is a surrogate PAIR — two units. Every
// crossing between an editor position and an internal position therefore
// has to go through these helpers, or columns drift on any line that
// carries multi-byte UTF-8 (byte ≠ rune) or supplementary runes
// (rune ≠ UTF-16).
package lsp

import (
	"strings"
	"unicode/utf8"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// utf16Len returns the number of UTF-16 code units encoding s.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// offsetFromLSP converts a 0-indexed LSP (line, character) — character in
// UTF-16 code units — into a byte offset into src. It walks to the start
// of `line`, then consumes UTF-16 units until `character` is reached or
// the line ends. A line past EOF returns len(src); a character past the
// line's end clamps to the newline (or EOF).
func offsetFromLSP(src string, line, character uint32) int {
	off := 0
	for l := uint32(0); l < line; l++ {
		nl := strings.IndexByte(src[off:], '\n')
		if nl < 0 {
			return len(src)
		}
		off += nl + 1
	}
	want, units := int(character), 0
	for off < len(src) && src[off] != '\n' && units < want {
		r, size := utf8.DecodeRuneInString(src[off:])
		off += size
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return off
}

// utf16Position converts a lexer Position (1-indexed line, 1-indexed RUNE
// column) into an LSP Position (0-indexed line, 0-indexed UTF-16
// character) using the file's source text. When src is empty or does not
// reach the line, it falls back to copying the rune column straight onto
// the UTF-16 character — correct for the BMP, off only for supplementary
// runes on a line we could not read.
func utf16Position(src string, p lexer.Position) protocol.Position {
	line := p.Line - 1
	if line < 0 {
		line = 0
	}
	col := p.Column - 1
	if col < 0 {
		col = 0
	}
	lineText, ok := nthLine(src, line)
	if !ok {
		return protocol.Position{Line: uint32(line), Character: uint32(col)}
	}
	return protocol.Position{Line: uint32(line), Character: uint32(runeColToUTF16(lineText, col))}
}

// nthLine returns the text of the 0-indexed line n (without its trailing
// newline) and whether the line exists in src.
func nthLine(src string, n int) (string, bool) {
	start := 0
	for i := 0; i < n; i++ {
		nl := strings.IndexByte(src[start:], '\n')
		if nl < 0 {
			return "", false
		}
		start += nl + 1
	}
	rest := src[start:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		return rest[:nl], true
	}
	return rest, true
}

// runeColToUTF16 returns the UTF-16 code-unit width of the first runeCol
// runes of line. A runeCol past the line's rune count clamps to the
// whole line's width.
func runeColToUTF16(line string, runeCol int) int {
	units, runes := 0, 0
	for _, r := range line {
		if runes >= runeCol {
			break
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
		runes++
	}
	return units
}
