// Package lexer tokenizes craftgo DSL source. Malformed input yields an
// [Error] token and a [Diagnostic], and lexing carries on. The package also
// defines the [Position] and [Diagnostic] types every later phase reports with.
package lexer

import (
	"errors"
	"fmt"
	"strconv"
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
}

// New returns a Lexer over src. filename goes into every Position and may be
// empty.
func New(filename, src string) *Lexer {
	return &Lexer{
		src:      src,
		filename: filename,
		line:     1,
		column:   1,
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
	case isIdentStart(r):
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
	tok.Trailing = l.consumeTrailingComment()
	return tok
}

// consumeTrailingComment consumes a `//` comment later on the current line and
// returns its text; without one it leaves the cursor and returns "".
func (l *Lexer) consumeTrailingComment() string {
	saveOffset, saveLine, saveCol := l.offset, l.line, l.column
	for l.offset < len(l.src) {
		r := l.peek()
		if r != ' ' && r != '\t' && r != '\r' {
			break
		}
		l.advance()
	}
	if !l.atLineComment() {
		l.offset, l.line, l.column = saveOffset, saveLine, saveCol
		return ""
	}
	return l.lineComment(CommentTrailing)
}

// atLineComment reports whether a `//` comment starts at the cursor.
func (l *Lexer) atLineComment() bool {
	return strings.HasPrefix(l.src[l.offset:], "//")
}

// lineComment consumes the `//` comment at the cursor up to the end of its
// line, records it as kind and returns its text: without the slashes, one
// space after them and a CRLF's '\r'.
func (l *Lexer) lineComment(kind CommentKind) string {
	pos := l.pos()
	start := l.offset + len("//")
	for l.offset < len(l.src) && l.src[l.offset] != '\n' {
		l.advance()
	}
	text := strings.TrimPrefix(strings.TrimSuffix(l.src[start:l.offset], "\r"), " ")
	l.allComments = append(l.allComments, &Comment{Pos: pos, Text: text, Kind: kind})
	return text
}

// lexPunct lexes a one-rune punctuation token; any other rune is an Error.
func (l *Lexer) lexPunct(pos Position, r rune) Token {
	l.advance()
	text := string(r)
	k, ok := punctuation[text]
	if !ok {
		return l.errorf(pos, "unexpected character %q", r)
	}
	return Token{Kind: k, Text: text, Pos: pos}
}

// lexIdentOrKeyword lexes `[A-Za-z_][A-Za-z0-9_]*` as a keyword or an Ident.
func (l *Lexer) lexIdentOrKeyword(pos Position) Token {
	start := l.offset
	for r := l.peek(); isIdentStart(r) || isDigit(r); r = l.peek() {
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

// lexString lexes a `"..."` literal on one line; Text keeps the quotes and
// escapes as written, and the literal is an Error unless [Unquote] accepts it.
func (l *Lexer) lexString(pos Position) Token {
	start := l.offset
	l.advance()
	for {
		if l.offset >= len(l.src) {
			return l.errorf(pos, "unterminated string literal")
		}
		switch l.peek() {
		case '\n':
			return l.errorf(pos, "newline in string literal")
		case '"':
			l.advance()
			text := l.src[start:l.offset]
			if _, err := Unquote(text); err != nil {
				return l.errorf(pos, "%s", err)
			}
			return Token{Kind: String, Text: text, Pos: pos}
		case '\\':
			// The escaped rune, a quote included, never ends the literal.
			l.advance()
			if l.offset < len(l.src) && l.peek() != '\n' {
				l.advance()
			}
		default:
			l.advance()
		}
	}
}

// simpleEscapes maps the rune after a backslash to the byte it stands for.
var simpleEscapes = map[byte]byte{'n': '\n', 't': '\t', 'r': '\r', '"': '"', '\\': '\\'}

// Unquote returns the value of a String or RawString token's text: a raw
// literal's content, or a quoted literal's with its escapes decoded. The
// escapes are \n \t \r \" \\ and \u{HEX} with 1 to 6 hex digits.
func Unquote(text string) (string, error) {
	n := len(text)
	if n >= 2 && text[0] == '`' && text[n-1] == '`' {
		return text[1 : n-1], nil
	}
	if n < 2 || text[0] != '"' || text[n-1] != '"' {
		return "", fmt.Errorf("%q is not a string literal", text)
	}
	var sb strings.Builder
	s := text[1 : n-1]
	for {
		i := strings.IndexByte(s, '\\')
		if i < 0 {
			sb.WriteString(s)
			return sb.String(), nil
		}
		sb.WriteString(s[:i])
		s = s[i+1:]
		if s == "" {
			return "", errors.New("unterminated escape sequence")
		}
		if b, ok := simpleEscapes[s[0]]; ok {
			sb.WriteByte(b)
			s = s[1:]
			continue
		}
		if s[0] != 'u' {
			r, _ := utf8.DecodeRuneInString(s)
			return "", fmt.Errorf("invalid escape sequence \\%c", r)
		}
		end := strings.IndexByte(s, '}')
		if len(s) < 2 || s[1] != '{' || end < 3 || end > 8 {
			return "", errors.New("invalid unicode escape")
		}
		v, err := strconv.ParseUint(s[2:end], 16, 32)
		if err != nil {
			return "", errors.New("invalid unicode escape")
		}
		sb.WriteRune(rune(v))
		s = s[end+1:]
	}
}

// lexRawString lexes a backtick literal: no escapes, newlines allowed.
func (l *Lexer) lexRawString(pos Position) Token {
	start := l.offset
	l.advance()
	for {
		if l.offset >= len(l.src) {
			return l.errorf(pos, "unterminated raw string literal")
		}
		if l.peek() == '`' {
			l.advance()
			return Token{Kind: RawString, Text: l.src[start:l.offset], Pos: pos}
		}
		l.advance()
	}
}

// skipWhitespaceAndComments skips whitespace and `//` comments, recording each
// and queueing it as the next token's Doc; the comment after a token on its
// line is consumed with the token.
func (l *Lexer) skipWhitespaceAndComments() {
	consecutiveNewlines := 0
	for l.offset < len(l.src) {
		r := l.peek()
		switch {
		case r == '\n':
			consecutiveNewlines++
			if consecutiveNewlines >= 2 {
				// A blank line detaches the comments above from the next token.
				l.pendingDoc = nil
			}
			l.advance()
		case r == ' ' || r == '\t' || r == '\r':
			l.advance()
		case l.atLineComment():
			l.pendingDoc = append(l.pendingDoc, l.lineComment(CommentLeading))
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

// isIdentStart reports whether r can start an identifier: a letter or `_`.
func isIdentStart(r rune) bool {
	return isLetter(r) || r == '_'
}

// isDigit reports whether r is an ASCII decimal digit.
func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}
