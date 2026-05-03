package lexer

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Severity classifies a [Diagnostic] for IDE rendering. The values mirror
// the LSP DiagnosticSeverity enum so the LSP server can pass them through
// without translation. Zero value is [SeverityError] - every diagnostic
// constructed without an explicit severity is treated as an error.
type Severity uint8

const (
	// SeverityError is a hard failure: codegen / runtime would be wrong.
	SeverityError Severity = iota
	// SeverityWarning is a soft issue worth surfacing but not blocking.
	SeverityWarning
	// SeverityInfo is informational (style hints, redundant constructs).
	SeverityInfo
	// SeverityHint is a low-priority suggestion, often paired with a fix.
	SeverityHint
)

// String renders the severity as a short label for diagnostic formatting.
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

// Related links a [Diagnostic] to a secondary location - typically the
// "previously declared at" site for a duplicate, or the conflicting
// decorator for a combination-rule violation. The IDE renders these as
// clickable secondary markers next to the primary diagnostic.
type Related struct {
	Pos Position
	Msg string
}

// Diagnostic is a single error/warning tied to a source range. The lexer,
// parser, and semantic analyser all accumulate Diagnostics so the parser,
// formatter, and LSP server can present them at once.
//
// Pos is the start of the offending token / construct; End is the
// exclusive end of the same range and is used by the LSP layer to draw the
// red squiggle. End may equal Pos when only a point location is known
// (e.g. lexer point errors); callers should treat (Pos == End) as
// "underline a single column".
//
// Code is a stable machine-readable identifier (e.g. `decorator/placement`)
// that the IDE uses for filtering, "disable next line", and documentation
// links. It must NOT include the message - keep human text in Msg.
//
// Related carries secondary positions referenced by Msg. The IDE shows
// them as clickable cross-links rather than appending another sentence
// to Msg.
type Diagnostic struct {
	Pos      Position
	End      Position
	Severity Severity
	Code     string
	Msg      string
	Related  []Related
}

// Error implements the error interface, formatted as `pos: msg`. Severity
// and code are omitted from the default rendering; the LSP layer reads the
// structured fields directly.
func (d Diagnostic) Error() string {
	return fmt.Sprintf("%s: %s", d.Pos, d.Msg)
}

// Lexer tokenizes a single craftgo source buffer.
//
// A Lexer holds its position, the original source, accumulated diagnostics,
// and is consumed via [Lexer.Next] (one token at a time) or [Lexer.Tokenize]
// (slurp the whole stream). Lexers are not safe for concurrent use; create one
// per file.
type Lexer struct {
	src      string
	filename string
	offset   int
	line     int
	column   int
	diags    []Diagnostic

	// pendingDoc accumulates `//`-line comment text (without the slashes
	// or one optional leading space) seen since the last blank line. The
	// next non-trivia token claims the whole slice as its [Token.Doc] and
	// the buffer resets.
	pendingDoc []string

	// allComments holds every `//` comment encountered in the file,
	// regardless of whether it was claimed as leading [Token.Doc],
	// trailing [Token.Trailing], or dropped on a blank line. Each entry
	// records source position and [CommentKind] so the formatter can
	// re-emit comments at the right site without re-scanning the source.
	// Retrieved via [Lexer.Comments].
	allComments []*Comment

	// sawNewlineSinceLastToken is true while the cursor sits on
	// whitespace / comment trivia AFTER a token has been emitted but
	// BEFORE any newline has been consumed. A `//` encountered while
	// this flag is true is a leading comment for the next token; one
	// encountered while it is false is a trailing comment for the
	// previous token. Reset to false at the end of every [Lexer.Next]
	// call.
	sawNewlineSinceLastToken bool
}

// New constructs a Lexer ready to tokenize src. filename is informational -
// it appears in [Position.Filename] on every emitted token and in diagnostics.
// Pass an empty string when there is no associated file.
func New(filename, src string) *Lexer {
	return &Lexer{
		src:      src,
		filename: filename,
		line:     1,
		column:   1,
		// File start is conceptually "after a newline" - any `//` seen
		// before the first token is leading, never trailing.
		sawNewlineSinceLastToken: true,
	}
}

// Diagnostics returns every error encountered so far. Calling it does not
// reset internal state, so additional errors from later tokens append to the
// same slice.
func (l *Lexer) Diagnostics() []Diagnostic { return l.diags }

// Tokenize consumes the entire source and returns every token, terminated by
// exactly one [EOF] token. Convenience wrapper for callers that want random
// access (parser does this; LSP keeps a Lexer around for incremental work).
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

// Next returns the next token in the stream. It skips whitespace and `//`
// line comments, then dispatches to a specialised lexer based on the leading
// rune. Any malformed input produces a token of kind [Error] (with the message
// in Text) and adds a corresponding [Diagnostic]; the lexer continues from
// the next available position.
func (l *Lexer) Next() Token {
	l.skipWhitespaceAndComments()
	if l.offset >= len(l.src) {
		return Token{Kind: EOF, Pos: l.pos()}
	}

	pos := l.pos()
	r, _ := l.peek()

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

// consumeTrailingComment scans forward from the current cursor for a
// `// note` that sits on the same line as the token just emitted (no
// newline encountered before the `//`). When found, the comment text
// is returned (with the leading `// ` stripped) and the cursor is
// advanced past the comment so [skipWhitespaceAndComments] on the
// next [Lexer.Next] call does not re-process it as a leading-doc
// candidate. The captured comment is also appended to [allComments]
// with [CommentTrailing] kind so the [File.Comments] side channel
// stays exhaustive.
//
// When no trailing is found the cursor is restored to its pre-scan
// position and the empty string is returned.
//
// This is the only place in the lexer where we look ahead past the
// freshly emitted token; without this hook the trailing `// note` would
// be claimed as the leading doc of whichever non-trivia token came
// next, which is almost never what the author intended.
func (l *Lexer) consumeTrailingComment(tokenLine int) string {
	saveOffset, saveLine, saveCol := l.offset, l.line, l.column
	// Skip space/tab on the current line (newline ends the search).
	for l.offset < len(l.src) {
		r, _ := l.peek()
		if r != ' ' && r != '\t' {
			break
		}
		l.advance()
	}
	// Need at least 2 bytes for `//`.
	if l.offset+1 >= len(l.src) || l.src[l.offset] != '/' || l.src[l.offset+1] != '/' {
		l.offset, l.line, l.column = saveOffset, saveLine, saveCol
		return ""
	}
	commentPos := l.pos()
	l.advance() // first '/'
	l.advance() // second '/'
	start := l.offset
	for l.offset < len(l.src) {
		r, _ := l.peek()
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
	_ = tokenLine // currently unused; reserved for future per-line-aware logic
	return text
}

// lexPunct produces a single-character punctuation token. Unrecognised runes
// fall through to an Error token so the parser can keep going (for instance,
// `$` mid-file should not lose subsequent valid tokens).
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

// lexIdentOrKeyword reads a maximal `[A-Za-z_][A-Za-z0-9_]*` run and looks
// the result up in [keywords]. Non-ASCII letters are rejected.
func (l *Lexer) lexIdentOrKeyword(pos Position) Token {
	start := l.offset
	for {
		r, _ := l.peek()
		if !(isLetter(r) || isDigit(r) || r == '_') {
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

// lexNumber reads an integer or float literal, then peeks for an optional
// duration or size suffix. Returns Int / Float when no suffix follows;
// returns Duration / Size when the suffix matches one of the configured units;
// returns Error otherwise so a typo like `1xyz` is reported instead of being
// silently split into two tokens.
func (l *Lexer) lexNumber(pos Position) Token {
	start := l.offset
	for {
		r, _ := l.peek()
		if !isDigit(r) {
			break
		}
		l.advance()
	}
	isFloat := false
	if r, _ := l.peek(); r == '.' && l.digitFollowsDot() {
		isFloat = true
		l.advance()
		for {
			r, _ := l.peek()
			if !isDigit(r) {
				break
			}
			l.advance()
		}
	}
	numText := l.src[start:l.offset]

	suffStart := l.offset
	for {
		r, _ := l.peek()
		if !(isLetter(r) || r == 'µ') {
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
	switch suffix {
	case "ns", "us", "µs", "ms", "s", "m", "h":
		return Token{Kind: Duration, Text: text, Pos: pos}
	case "B", "KB", "MB", "GB":
		return Token{Kind: Size, Text: text, Pos: pos}
	}
	return l.errorf(pos, "invalid number suffix %q", suffix)
}

// digitFollowsDot reports whether the byte AFTER the current `.` is a decimal
// digit. Used to disambiguate `3.14` (float) from `3.foo` (Int + Dot + Ident).
// Cheap byte-level check is sufficient because '0'-'9' are ASCII.
func (l *Lexer) digitFollowsDot() bool {
	if l.offset+1 >= len(l.src) {
		return false
	}
	return isDigit(rune(l.src[l.offset+1]))
}

// lexString reads a double-quoted string literal, supporting the escape
// sequences `\n \t \r \" \\` and the Unicode form `\u{HEX}` (1-6 hex digits).
// Newlines inside the literal and unterminated strings produce [Error] tokens
// - both are common authoring mistakes worth flagging early.
//
// The returned token's Text retains the surrounding quotes and the original
// escape sequences verbatim; the parser is responsible for unescaping when it
// builds AST literal nodes (see parser.unquoteString).
func (l *Lexer) lexString(pos Position) Token {
	// Hot path: byte-by-byte string-literal scan. Builder keeps the
	// per-rune append allocation-free.
	var sb strings.Builder
	sb.WriteByte('"')
	l.advance()
	for {
		if l.offset >= len(l.src) {
			return l.errorf(pos, "unterminated string literal")
		}
		r, _ := l.peek()
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
			esc, _ := l.peek()
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

// lexUnicodeEscape consumes a `{HEX}` body following an already-read `\u`.
// The opening brace is required, the body must contain 1-6 hex digits, and a
// closing `}` must appear. Returns false when any of those constraints is
// violated; the caller wraps that into a single user-facing error.
func (l *Lexer) lexUnicodeEscape(sb *strings.Builder) bool {
	r, _ := l.peek()
	if r != '{' {
		return false
	}
	sb.WriteByte('{')
	l.advance()
	n := 0
	for n < 6 {
		rr, _ := l.peek()
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
	rr, _ := l.peek()
	if rr != '}' {
		return false
	}
	sb.WriteByte('}')
	l.advance()
	return true
}

// lexRawString reads a backtick-quoted string. Contents pass through
// verbatim - escape sequences are NOT interpreted, and embedded newlines are
// allowed. This is the right form for long `@doc()` text and complex
// `@pattern()` regular expressions where backslash escapes would be noisy.
func (l *Lexer) lexRawString(pos Position) Token {
	// Hot path: byte-by-byte raw-string scan. Builder keeps the
	// inner append allocation-free.
	var sb strings.Builder
	sb.WriteByte('`')
	l.advance()
	for {
		if l.offset >= len(l.src) {
			return l.errorf(pos, "unterminated raw string literal")
		}
		r, _ := l.peek()
		if r == '`' {
			sb.WriteByte('`')
			l.advance()
			return Token{Kind: RawString, Text: sb.String(), Pos: pos}
		}
		sb.WriteRune(r)
		l.advance()
	}
}

// skipWhitespaceAndComments advances past any sequence of ASCII whitespace
// (space, tab, CR, LF) and `//` line comments. `/* block */` comments are
// not supported.
func (l *Lexer) skipWhitespaceAndComments() {
	consecutiveNewlines := 0
	for l.offset < len(l.src) {
		r, _ := l.peek()
		switch {
		case r == '\n':
			consecutiveNewlines++
			l.sawNewlineSinceLastToken = true
			if consecutiveNewlines >= 2 {
				// Blank line: comments above are detached from the
				// upcoming token. Drop the leading buffer so they do
				// not leak into the next AST node's Doc. They remain
				// in [allComments] so the formatter can recover them
				// as free-floating section / closing notes via the
				// [File.Comments] side channel populated by the parser.
				l.pendingDoc = nil
			}
			l.advance()
		case r == ' ' || r == '\t' || r == '\r':
			l.advance()
		case r == '/' && l.offset+1 < len(l.src) && l.src[l.offset+1] == '/':
			commentPos := l.pos()
			start := l.offset + 2 // skip the two slashes
			for l.offset < len(l.src) {
				rr, _ := l.peek()
				if rr == '\n' {
					break
				}
				l.advance()
			}
			line := l.src[start:l.offset]
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

// Comments returns every `//` comment encountered in the source so far,
// in source order, with their position and leading/trailing kind.
// Callers (parser snapshot, format printer, lint tools) consume this
// slice instead of re-scanning the source; it is the single source of
// truth for "every comment in the file".
func (l *Lexer) Comments() []*Comment { return l.allComments }

// peek returns the rune at the current offset without consuming it. Returns
// `(0, 0)` at EOF - callers should treat rune 0 as a sentinel and not try to
// classify it as a valid character (none of the [isLetter] / [isDigit] /
// [isHex] helpers accept it).
func (l *Lexer) peek() (rune, int) {
	if l.offset >= len(l.src) {
		return 0, 0
	}
	return utf8.DecodeRuneInString(l.src[l.offset:])
}

// advance consumes one rune. Callers MUST ensure l.offset < len(l.src) by
// peeking first; an out-of-bounds advance would corrupt the column counter
// without making progress. The function maintains line/column for [pos].
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

// pos returns the current position (used to tag freshly-emitted tokens).
func (l *Lexer) pos() Position {
	return Position{
		Filename: l.filename,
		Offset:   l.offset,
		Line:     l.line,
		Column:   l.column,
	}
}

// errorf records a diagnostic at pos and returns a synthetic Error token so
// the caller can plug it back into the stream without further branching.
func (l *Lexer) errorf(pos Position, format string, args ...any) Token {
	msg := fmt.Sprintf(format, args...)
	l.diags = append(l.diags, Diagnostic{Pos: pos, Msg: msg})
	return Token{Kind: Error, Text: msg, Pos: pos}
}

// isLetter reports whether r is an ASCII letter (a-z or A-Z).
func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// isDigit reports whether r is an ASCII decimal digit ('0'-'9').
func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// isHex reports whether r is a valid hexadecimal digit ('0'-'9', 'a'-'f',
// 'A'-'F'). Used inside `\u{...}` escape parsing.
func isHex(r rune) bool {
	return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}
