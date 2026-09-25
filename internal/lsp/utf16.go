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

// nextLine returns the offset of the line after the one holding offset i of
// s, and false on the last line. A line ends at `\n`, `\r\n` or a lone `\r`,
// as the lexer ends it.
func nextLine(s string, i int) (int, bool) {
	j := strings.IndexAny(s[i:], "\r\n")
	if j < 0 {
		return len(s), false
	}
	j += i
	if strings.HasPrefix(s[j:], "\r\n") {
		return j + 2, true
	}
	return j + 1, true
}

// lastLine returns the number of line ends in s and the offset of its last
// line.
func lastLine(s string) (ends, start int) {
	for {
		next, ok := nextLine(s, start)
		if !ok {
			return ends, start
		}
		ends, start = ends+1, next
	}
}

// offsetFromLSP converts a 0-based LSP position (character in UTF-16 units) into
// a byte offset into src, clamped to the line's end and to len(src).
func offsetFromLSP(src string, line, character uint32) int {
	off := 0
	for l := uint32(0); l < line; l++ {
		next, ok := nextLine(src, off)
		if !ok {
			return len(src)
		}
		off = next
	}
	want, units := int(character), 0
	for off < len(src) && src[off] != '\n' && src[off] != '\r' && units < want {
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

// utf16Position converts a lexer position (1-based line, rune column) into an
// LSP one over src; a line src lacks keeps the rune column as the character.
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

// spanRange returns the LSP range of the n bytes of src that start at p, a
// position in src.
func spanRange(src string, p lexer.Position, n int) protocol.Range {
	start := utf16Position(src, p)
	from := min(max(p.Offset, 0), len(src))
	text := src[from:min(from+n, len(src))]
	end := start
	if ends, last := lastLine(text); ends > 0 {
		end.Line += uint32(ends)
		end.Character = uint32(utf16Len(text[last:]))
	} else {
		end.Character += uint32(utf16Len(text))
	}
	return protocol.Range{Start: start, End: end}
}

// rangeOf returns the LSP range of t, a token lexed from src.
func rangeOf(src string, t lexer.Token) protocol.Range {
	return spanRange(src, t.Pos, len(t.Text))
}

// nthLine returns the text of the 0-indexed line n, without its line end,
// and whether the line exists in src.
func nthLine(src string, n int) (string, bool) {
	start := 0
	for i := 0; i < n; i++ {
		next, ok := nextLine(src, start)
		if !ok {
			return "", false
		}
		start = next
	}
	rest := src[start:]
	if end := strings.IndexAny(rest, "\r\n"); end >= 0 {
		return rest[:end], true
	}
	return rest, true
}

// runeColToUTF16 returns the UTF-16 width of the first runeCol runes of line,
// or of the whole line when it is shorter.
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
