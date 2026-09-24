// Package parser builds an [ast.File] from craftgo source by recursive
// descent. It always returns a tree, partial when the source has errors, and
// reports problems as [lexer.Diagnostic] values.
package parser

import (
	"fmt"
	"unicode"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// Parser parses one file. It is not safe for concurrent use.
type Parser struct {
	tokens []lexer.Token
	pos    int
	diags  []lexer.Diagnostic
	// pendingDoc is the doc captureDoc read for the node being parsed.
	pendingDoc []string
	// allComments is every comment in the file.
	allComments []*lexer.Comment
	// claimed holds the lines of comments a node owns, which
	// harvestFreeComments skips.
	claimed map[int]bool
}

// takeDoc returns pendingDoc and clears it.
func (p *Parser) takeDoc() []string {
	d := p.pendingDoc
	p.pendingDoc = nil
	return d
}

// captureDoc moves the next token's Doc, if it has one, into pendingDoc and
// claims its lines.
func (p *Parser) captureDoc() {
	if len(p.peek().Doc) > 0 {
		p.pendingDoc = p.peek().Doc
		p.claimDoc(p.peek())
	}
}

// New lexes src and returns a Parser whose diagnostics start with the lexer's.
func New(filename, src string) *Parser {
	l := lexer.New(filename, src)
	toks := l.Tokenize()
	return &Parser{
		tokens:      toks,
		diags:       l.Diagnostics(),
		allComments: l.Comments(),
		claimed:     map[int]bool{},
	}
}

// Diagnostics returns the lexer and parser diagnostics.
func (p *Parser) Diagnostics() []lexer.Diagnostic { return p.diags }

// Tokens returns the token stream the parser consumes.
func (p *Parser) Tokens() []lexer.Token { return p.tokens }

// Parse returns the file's AST, never nil even with diagnostics. Decorators
// before `package` are the file's; without a package clause they go to the
// first declaration.
func (p *Parser) Parse() *ast.File {
	f := &ast.File{}
	// The comment above a leading decorator is the file's LeadingDoc.
	if p.peek().Kind == lexer.At {
		f.LeadingDoc = p.peek().Doc
		p.claimDoc(p.peek())
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
		// Skip a token no production consumed.
		if p.pos == startPos {
			p.advance()
		}
	}
	if len(leading) > 0 {
		p.errorf(leading[0].Pos, "decorators without a declaration to attach to")
	}
	f.Comments = p.allComments
	// The comments still unclaimed are file-scope blocks.
	f.FreeComments = p.harvestFreeComments(0, int(^uint(0)>>1))
	return f
}

// peek returns the current token without consuming it.
func (p *Parser) peek() lexer.Token { return p.tokens[p.pos] }

// peekAt returns the token n ahead, clamped to the final EOF token.
func (p *Parser) peekAt(n int) lexer.Token {
	idx := p.pos + n
	if idx >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[idx]
}

// advance returns the current token and moves past it, stopping at EOF.
func (p *Parser) advance() lexer.Token {
	t := p.tokens[p.pos]
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return t
}

// expect consumes a token of kind k. On a mismatch it reports the error and
// returns the current token, unconsumed, with ok=false.
func (p *Parser) expect(k lexer.Kind) (lexer.Token, bool) {
	if p.peek().Kind == k {
		return p.advance(), true
	}
	p.errorf(p.peek().Pos, "expected %s, got %s", k, p.peek().Kind)
	return p.peek(), false
}

// errorf records an error diagnostic at pos.
func (p *Parser) errorf(pos lexer.Position, format string, args ...any) {
	p.diags = append(p.diags, lexer.Diagnostic{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// peekIs reports whether the current token has kind k.
func (p *Parser) peekIs(k lexer.Kind) bool { return p.peek().Kind == k }

// isKeywordKind reports whether k is a reserved word, HTTP verbs included.
func isKeywordKind(k lexer.Kind) bool {
	return k >= lexer.KwPackage && k <= lexer.VerbOptions
}

// isUpperFirst reports whether s starts with an upper-case letter.
func isUpperFirst(s string) bool {
	if s == "" {
		return false
	}
	r := []rune(s)[0]
	return unicode.IsUpper(r)
}
