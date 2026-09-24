// Package lexer tokenizes craftgo DSL source. Malformed input yields an
// [Error] token and a [Diagnostic], and lexing carries on. The package also
// defines the [Position] and [Diagnostic] types every later phase reports with.
package lexer

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Severity grades a [Diagnostic]. The zero value is [SeverityError].
type Severity uint8

const (
	// SeverityError blocks gen and fmt.
	SeverityError Severity = iota
	// SeverityWarning is reported without blocking.
	SeverityWarning
	// SeverityInfo is informational.
	SeverityInfo
	// SeverityHint is a low-priority suggestion.
	SeverityHint
)

// String returns the severity's lower-case name.
func (s Severity) String() string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityInfo:
		return "info"
	case SeverityHint:
		return "hint"
	default:
		return "error"
	}
}

// Related is a secondary location a [Diagnostic] refers to, such as the first
// declaration of a duplicate.
type Related struct {
	Pos Position
	Msg string
}

// Diagnostic is one problem at a source range. End is exclusive, or zero when
// only Pos is known. Code is a stable identifier such as `decorator/placement`;
// Msg is the human text.
type Diagnostic struct {
	Pos      Position
	End      Position
	Severity Severity
	Code     string
	Msg      string
	Related  []Related
}

// IsError reports whether d blocks gen and fmt: any severity but warning, info
// and hint.
func (d Diagnostic) IsError() bool {
	return d.Severity != SeverityWarning && d.Severity != SeverityInfo && d.Severity != SeverityHint
}

// Error renders d as `pos: msg`.
func (d Diagnostic) Error() string {
	return fmt.Sprintf("%s: %s", d.Pos, d.Msg)
}

// Lexer tokenizes one source buffer. It is not safe for concurrent use.
type Lexer struct {
	src      string
	filename string
	offset   int
	line     int
	column   int
	diags    []Diagnostic

	// pendingDoc holds the leading comments since the last blank line; the
	// next token takes them as its Doc.
	pendingDoc []string

	// allComments records every comment, including those a blank line
	// detached from any token.
	allComments []*Comment

	// sawNewlineSinceLastToken is true at file start and after a newline; a
	// comment seen while it is false trails the previous token.
	sawNewlineSinceLastToken bool
}

// New returns a Lexer over src. filename goes into every Position and may be
// empty.
func New(filename, src string) *Lexer {
	return &Lexer{
		src:      src,
		filename: filename,
		line:     1,
		column:   1,
		// A comment before the first token is leading.
		sawNewlineSinceLastToken: true,
	}
}

// Diagnostics returns the diagnostics recorded so far.
func (l *Lexer) Diagnostics() []Diagnostic { return l.diags }

// Tokenize lexes the rest of the source; the result ends with one [EOF] token.
func (l *Lexer) Tokenize() []Token {
	var toks []Token
	for {
		t := l.Next()
		toks = append(toks, t)
		if t.Kind == EOF {
			return toks
		}
	}
}

// Next returns the next token, with the comments above it in Doc and a comment
// after it on its line in Trailing.
func (l *Lexer) Next() Token {
	l.skipWhitespaceAndComments()
	if l.offset >= len(l.src) {
		return Token{Kind: EOF, Pos: l.pos()}
	}

	pos := l.pos()
	r := l.peek()

	var tok Token
	switch {
	case isLetter(r) || r == '_':
		tok = l.lexIdentOrKeyword(pos)
	case isDigit(r):
		tok = l.lexNumber(pos)
	case r == '"':
		tok = l.lexString(pos)
	case r == '`':
		tok = l.lexRawString(pos)
	default:
		tok = l.lexPunct(pos, r)
	}

	if len(l.pendingDoc) > 0 {
		tok.Doc = l.pendingDoc
		l.pendingDoc = nil
	}
	tok.Trailing = l.consumeTrailingComment(tok.Pos.Line)
	l.sawNewlineSinceLastToken = false
	return tok
}

// consumeTrailingComment consumes a `//` comment later on the current line and
// returns its text; without one it leaves the cursor and returns "".
func (l *Lexer) consumeTrailingComment(tokenLine int) string {
	saveOffset, saveLine, saveCol := l.offset, l.line, l.column
	for l.offset < len(l.src) {
		r := l.peek()
		if r != ' ' && r != '\t' {
			break
		}
		l.advance()
	}
	if l.offset+1 >= len(l.src) || l.src[l.offset] != '/' || l.src[l.offset+1] != '/' {
		l.offset, l.line, l.column = saveOffset, saveLine, saveCol
		return ""
	}
	commentPos := l.pos()
	l.advance()
	l.advance()
	start := l.offset
	for l.offset < len(l.src) {
		r := l.peek()
		if r == '\n' {
			break
		}
		l.advance()
	}
	text := l.src[start:l.offset]
	if len(text) > 0 && text[0] == ' ' {
		text = text[1:]
	}
	l.allComments = append(l.allComments, &Comment{
		Pos:  commentPos,
		Text: text,
		Kind: CommentTrailing,
	})
	_ = tokenLine
	return text
}

// lexPunct lexes a one-rune punctuation token; any other rune is an Error.
func (l *Lexer) lexPunct(pos Position, r rune) Token {
	l.advance()
	var k Kind
	switch r {
	case '{':
		k = LBrace
	case '}':
		k = RBrace
	case '(':
		k = LParen
	case ')':
		k = RParen
	case '[':
		k = LBracket
	case ']':
		k = RBracket
	case '<':
		k = LAngle
	case '>':
		k = RAngle
	case ',':
		k = Comma
	case ':':
		k = Colon
	case '=':
		k = Equal
	case '?':
		k = Question
	case '.':
		k = Dot
	case '/':
		k = Slash
	case '@':
		k = At
	case '-':
		k = Dash
	default:
		return l.errorf(pos, "unexpected character %q", r)
	}
	return Token{Kind: k, Text: string(r), Pos: pos}
}

// lexIdentOrKeyword lexes `[A-Za-z_][A-Za-z0-9_]*` as a keyword or an Ident.
func (l *Lexer) lexIdentOrKeyword(pos Position) Token {
	start := l.offset
	for {
		r := l.peek()
		if !isLetter(r) && !isDigit(r) && r != '_' {
			break
		}
		l.advance()
	}
	text := l.src[start:l.offset]
	if k, ok := keywords[text]; ok {
		return Token{Kind: k, Text: text, Pos: pos}
	}
	return Token{Kind: Ident, Text: text, Pos: pos}
}

// lexNumber lexes an Int or a Float, or a Duration or Size when a unit suffix
// follows; any other letter suffix makes the whole literal an Error.
func (l *Lexer) lexNumber(pos Position) Token {
	start := l.offset
	for {
		r := l.peek()
		if !isDigit(r) {
			break
		}
		l.advance()
	}
	isFloat := false
	if r := l.peek(); r == '.' && l.digitFollowsDot() {
		isFloat = true
		l.advance()
		for {
			r := l.peek()
			if !isDigit(r) {
				break
			}
			l.advance()
		}
	}
	numText := l.src[start:l.offset]

	suffStart := l.offset
	for {
		r := l.peek()
		if !isLetter(r) && r != 'µ' {
			break
		}
		l.advance()
	}
	suffix := l.src[suffStart:l.offset]
	text := l.src[start:l.offset]

	if suffix == "" {
		if isFloat {
			return Token{Kind: Float, Text: numText, Pos: pos}
		}
		return Token{Kind: Int, Text: numText, Pos: pos}
	}
	if IsDurationSuffix(suffix) {
		return Token{Kind: Duration, Text: text, Pos: pos}
	}
	if _, ok := SizeMultiplier(suffix); ok {
		return Token{Kind: Size, Text: text, Pos: pos}
	}
	return l.errorf(pos, "invalid number suffix %q", suffix)
}

// digitFollowsDot tells `3.14` (a Float) from `3.foo` (Int, Dot, Ident).
func (l *Lexer) digitFollowsDot() bool {
	if l.offset+1 >= len(l.src) {
		return false
	}
	return isDigit(rune(l.src[l.offset+1]))
}

// lexString lexes a `"..."` literal with the escapes `\n \t \r \" \\` and
// `\u{HEX}` (1-6 digits). Text keeps the quotes and escapes as written.
func (l *Lexer) lexString(pos Position) Token {
	var sb strings.Builder
	sb.WriteByte('"')
	l.advance()
	for {
		if l.offset >= len(l.src) {
			return l.errorf(pos, "unterminated string literal")
		}
		r := l.peek()
		if r == '\n' {
			return l.errorf(pos, "newline in string literal")
		}
		if r == '"' {
			sb.WriteByte('"')
			l.advance()
			return Token{Kind: String, Text: sb.String(), Pos: pos}
		}
		if r == '\\' {
			sb.WriteByte('\\')
			l.advance()
			if l.offset >= len(l.src) {
				return l.errorf(pos, "unterminated escape sequence")
			}
			esc := l.peek()
			switch esc {
			case 'n', 't', 'r', '"', '\\':
				sb.WriteRune(esc)
				l.advance()
			case 'u':
				sb.WriteRune(esc)
				l.advance()
				if !l.lexUnicodeEscape(&sb) {
					return l.errorf(pos, "invalid unicode escape")
				}
			default:
				return l.errorf(pos, "invalid escape sequence \\%c", esc)
			}
			continue
		}
		sb.WriteRune(r)
		l.advance()
	}
}

// lexUnicodeEscape consumes the `{HEX}` after `\u` and reports whether it holds
// 1-6 hex digits.
func (l *Lexer) lexUnicodeEscape(sb *strings.Builder) bool {
	r := l.peek()
	if r != '{' {
		return false
	}
	sb.WriteByte('{')
	l.advance()
	n := 0
	for n < 6 {
		rr := l.peek()
		if rr == '}' {
			if n == 0 {
				return false
			}
			sb.WriteByte('}')
			l.advance()
			return true
		}
		if !isHex(rr) {
			return false
		}
		sb.WriteRune(rr)
		l.advance()
		n++
	}
	rr := l.peek()
	if rr != '}' {
		return false
	}
	sb.WriteByte('}')
	l.advance()
	return true
}

// lexRawString lexes a backtick literal: no escapes, newlines allowed.
func (l *Lexer) lexRawString(pos Position) Token {
	var sb strings.Builder
	sb.WriteByte('`')
	l.advance()
	for {
		if l.offset >= len(l.src) {
			return l.errorf(pos, "unterminated raw string literal")
		}
		r := l.peek()
		if r == '`' {
			sb.WriteByte('`')
			l.advance()
			return Token{Kind: RawString, Text: sb.String(), Pos: pos}
		}
		sb.WriteRune(r)
		l.advance()
	}
}

// skipWhitespaceAndComments skips whitespace and `//` comments, recording each
// comment and queueing the leading ones as the next token's Doc.
func (l *Lexer) skipWhitespaceAndComments() {
	consecutiveNewlines := 0
	for l.offset < len(l.src) {
		r := l.peek()
		switch {
		case r == '\n':
			consecutiveNewlines++
			l.sawNewlineSinceLastToken = true
			if consecutiveNewlines >= 2 {
				// A blank line detaches the comments above from the next token.
				l.pendingDoc = nil
			}
			l.advance()
		case r == ' ' || r == '\t' || r == '\r':
			l.advance()
		case r == '/' && l.offset+1 < len(l.src) && l.src[l.offset+1] == '/':
			commentPos := l.pos()
			start := l.offset + 2 // skip the two slashes
			for l.offset < len(l.src) {
				rr := l.peek()
				if rr == '\n' {
					break
				}
				l.advance()
			}
			line := l.src[start:l.offset]
			// Drop the '\r' of a CRLF line ending.
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			if len(line) > 0 && line[0] == ' ' {
				line = line[1:]
			}
			kind := CommentLeading
			if !l.sawNewlineSinceLastToken {
				kind = CommentTrailing
			}
			l.allComments = append(l.allComments, &Comment{
				Pos:  commentPos,
				Text: line,
				Kind: kind,
			})
			if kind == CommentLeading {
				l.pendingDoc = append(l.pendingDoc, line)
			}
			consecutiveNewlines = 0
		default:
			return
		}
	}
}

// Comments returns every `//` comment seen so far, in source order.
func (l *Lexer) Comments() []*Comment { return l.allComments }

// peek returns the rune at the cursor, or 0 at EOF.
func (l *Lexer) peek() rune {
	if l.offset >= len(l.src) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.src[l.offset:])
	return r
}

// advance consumes one rune and updates line and column; the caller ensures a
// rune remains.
func (l *Lexer) advance() {
	r, size := utf8.DecodeRuneInString(l.src[l.offset:])
	l.offset += size
	if r == '\n' {
		l.line++
		l.column = 1
	} else {
		l.column++
	}
}

// pos returns the cursor's Position.
func (l *Lexer) pos() Position {
	return Position{
		Filename: l.filename,
		Offset:   l.offset,
		Line:     l.line,
		Column:   l.column,
	}
}

// errorf records a diagnostic at pos and returns it as an Error token.
func (l *Lexer) errorf(pos Position, format string, args ...any) Token {
	msg := fmt.Sprintf(format, args...)
	l.diags = append(l.diags, Diagnostic{Pos: pos, Msg: msg})
	return Token{Kind: Error, Text: msg, Pos: pos}
}

// isLetter reports whether r is an ASCII letter.
func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// isDigit reports whether r is an ASCII decimal digit.
func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// isHex reports whether r is a hexadecimal digit.
func isHex(r rune) bool {
	return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}
