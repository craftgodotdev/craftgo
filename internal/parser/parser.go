// Package parser turns a craftgo source buffer into an [ast.File].
//
// The implementation is a hand-rolled recursive-descent parser. It runs the
// [lexer] on construction (in [New]) so callers do not interact with tokens
// directly. Errors are accumulated as [lexer.Diagnostic] entries; the parser
// always returns a (possibly partial) AST so that LSP / formatters / linting
// can keep working in the presence of mistakes.
package parser

import (
	"fmt"
	"unicode"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// Parser holds the token stream and accumulates diagnostics. Use [New] to
// construct one and call [Parser.Parse] to drive parsing. Parsers are not
// safe for concurrent use; create one per file.
type Parser struct {
	tokens []lexer.Token
	pos    int
	diags  []lexer.Diagnostic
	// pendingDoc carries the leading `//` comments captured from the
	// first token of the next declaration / member / method. The parser
	// snapshots it at known sites and `takeDoc()` clears it after the
	// AST node has claimed the slice.
	pendingDoc []string
	// allComments is the snapshot of every `//` comment seen by the
	// lexer, kept so the parser can populate `*ast.File.Comments` and
	// (in body parsing) scan for free-floating section headers.
	allComments []*lexer.Comment
}

// takeDoc returns the buffered doc-comment slice and clears it so the
// next AST node sees an empty buffer until the lexer fills one again.
func (p *Parser) takeDoc() []string {
	d := p.pendingDoc
	p.pendingDoc = nil
	return d
}

// captureDoc snapshots the doc attached to the current peek token onto
// the parser's pendingDoc buffer. Safe to call multiple times - it
// overwrites any previous buffer because the freshest peek dominates.
func (p *Parser) captureDoc() {
	if len(p.peek().Doc) > 0 {
		p.pendingDoc = p.peek().Doc
	}
}

// New tokenises src (with filename used for diagnostics) and returns a Parser
// ready to call [Parser.Parse]. Lexer-level errors are propagated into the
// parser's diagnostics so callers only need to inspect one slice.
func New(filename, src string) *Parser {
	l := lexer.New(filename, src)
	toks := l.Tokenize()
	return &Parser{
		tokens:      toks,
		diags:       l.Diagnostics(),
		allComments: l.Comments(),
	}
}

// Diagnostics returns all errors collected during lexing and parsing.
func (p *Parser) Diagnostics() []lexer.Diagnostic { return p.diags }

// Parse consumes the entire token stream and returns an [*ast.File]. The
// returned file is non-nil even when diagnostics were recorded, so callers
// can offer best-effort downstream behaviour.
//
// File-level decorators (those that appear BEFORE `package`) are attached to
// `f.Decorators`; decorators with no following `package` keyword are passed
// to the first declaration instead.
func (p *Parser) Parse() *ast.File {
	f := &ast.File{}
	// Capture the file-header `//` block when it would otherwise be
	// dropped: a decorator-led file (`@version(...) ... package x`)
	// lets the lexer attach the comment to the first `@` token, but
	// [ast.Decorator] has no Doc field, so without this snapshot the
	// comment vanishes through the parser/format round trip.
	if p.peek().Kind == lexer.At {
		f.LeadingDoc = p.peek().Doc
	}
	leading := p.parseDecorators()
	if p.peek().Kind == lexer.KwPackage {
		f.Decorators = leading
		leading = nil
		f.Package = p.parsePackage()
	}
	for p.peek().Kind == lexer.KwImport {
		f.Imports = append(f.Imports, p.parseImport())
	}
	for p.peek().Kind != lexer.EOF {
		startPos := p.pos
		d := p.parseTopLevelWith(leading)
		leading = nil
		if d != nil {
			f.Decls = append(f.Decls, d)
		}
		// Recovery: if no token was consumed, advance one to avoid an
		// infinite loop on unexpected input.
		if p.pos == startPos {
			p.advance()
		}
	}
	f.Comments = p.allComments
	return f
}

// peek returns the current token without consuming it.
func (p *Parser) peek() lexer.Token { return p.tokens[p.pos] }

// peekAt returns the token n positions ahead. Out-of-range indices clamp to
// the last token in the stream (always the EOF sentinel) so disambiguation
// look-aheads do not need bounds checks.
func (p *Parser) peekAt(n int) lexer.Token {
	idx := p.pos + n
	if idx >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[idx]
}

// advance consumes and returns the current token. The cursor stops at the
// final EOF token so repeated calls past the end are idempotent.
func (p *Parser) advance() lexer.Token {
	t := p.tokens[p.pos]
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return t
}

// expect consumes the current token if its kind matches; otherwise records a
// diagnostic and returns ok=false WITHOUT advancing. The caller is then free
// to decide between aborting the current production or attempting recovery.
func (p *Parser) expect(k lexer.Kind) (lexer.Token, bool) {
	if p.peek().Kind == k {
		return p.advance(), true
	}
	p.errorf(p.peek().Pos, "expected %s, got %s", k, p.peek().Kind)
	return p.peek(), false
}

// errorf records a diagnostic at pos. Used by every error-reporting path so
// formatting stays uniform across productions.
func (p *Parser) errorf(pos lexer.Position, format string, args ...any) {
	p.diags = append(p.diags, lexer.Diagnostic{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// peekIs reports whether the current token has kind k.
func (p *Parser) peekIs(k lexer.Kind) bool { return p.peek().Kind == k }

// isKeywordKind reports whether k is a reserved keyword token (including
// HTTP verbs). Used to allow keyword spellings as decorator names.
func isKeywordKind(k lexer.Kind) bool {
	return k >= lexer.KwPackage && k <= lexer.VerbOptions
}

// isUpperFirst reports whether the first rune of s is an uppercase letter.
// Used by [parseTypeMember] to bias mixin vs field disambiguation toward Go
// naming conventions (PascalCase types, lowercase-first field names).
func isUpperFirst(s string) bool {
	if s == "" {
		return false
	}
	r := []rune(s)[0]
	return unicode.IsUpper(r)
}
