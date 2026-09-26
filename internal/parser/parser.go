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
	// reported holds the position of every diagnostic in diags.
	reported map[lexer.Position]bool
	// allComments is every comment in the file.
	allComments []*lexer.Comment
	// claimed holds the lines of comments a node owns, which
	// harvestFreeComments skips.
	claimed map[int]bool
	// chainComments becomes the file's [ast.File.ChainComments].
	chainComments map[int][]string
}

// docAbove claims the comment lines directly above the current token and
// returns them.
func (p *Parser) docAbove() []string {
	t := p.peek()
	p.claimDoc(t)
	return t.Doc
}

// New lexes src and returns a Parser whose diagnostics start with the lexer's.
func New(filename, src string) *Parser {
	l := lexer.New(filename, src)
	toks := l.Tokenize()
	reported := map[lexer.Position]bool{}
	for _, d := range l.Diagnostics() {
		reported[d.Pos] = true
	}
	return &Parser{
		tokens:        toks,
		diags:         l.Diagnostics(),
		reported:      reported,
		allComments:   l.Comments(),
		claimed:       map[int]bool{},
		chainComments: map[int][]string{},
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
		f.LeadingDoc = p.docAbove()
	}
	leading := p.parseDecorators()
	if p.peek().Kind == lexer.KwPackage {
		// The comment directly above `package` is its Doc.
		if n := len(leading); n > 0 {
			p.claimChain(leading, leading[n-1].Pos.Line)
		}
		f.Decorators = leading
		leading = nil
		f.Package = p.parsePackage()
		p.rejectDecoratorsAfter("declaration", startsDecl)
	}
	for p.peek().Kind == lexer.KwImport {
		f.Imports = append(f.Imports, p.parseImport())
		p.rejectDecoratorsAfter("declaration", startsDecl)
	}
	p.each(lexer.EOF, func() {
		d := p.parseTopLevelWith(leading)
		leading = nil
		if d != nil {
			f.Decls = append(f.Decls, d)
			p.rejectDecoratorsAfter("declaration", startsDecl)
		}
	})
	if len(leading) > 0 {
		p.errorf(leading[0].Pos, "decorators without a declaration to attach to")
	}
	f.Comments = p.allComments
	f.ChainComments = p.chainComments
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

// expect consumes a token of kind k. On a mismatch it reports the error,
// consumes nothing and returns ok=false with an empty token at the current
// position.
func (p *Parser) expect(k lexer.Kind) (lexer.Token, bool) {
	if p.peek().Kind == k {
		return p.advance(), true
	}
	p.errorf(p.peek().Pos, "expected %s, got %s", k, p.peek().Kind)
	return lexer.Token{Pos: p.peek().Pos}, false
}

// errorf records an error diagnostic at pos unless the lexer or the parser
// already reported one there, which a second one would only follow from.
func (p *Parser) errorf(pos lexer.Position, format string, args ...any) {
	if p.reported[pos] {
		return
	}
	p.reported[pos] = true
	p.diags = append(p.diags, lexer.Diagnostic{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// peekIs reports whether the current token has kind k.
func (p *Parser) peekIs(k lexer.Kind) bool { return p.peek().Kind == k }

// each calls member at every token before closer or EOF, and skips the token
// when member consumes none.
func (p *Parser) each(closer lexer.Kind, member func()) {
	for !p.peekIs(closer) && !p.peekIs(lexer.EOF) {
		start := p.pos
		member()
		if p.pos == start {
			p.advance()
		}
	}
}

// braced parses `{`, [Parser.each] member up to `}`, and `}`, and returns the
// two braces; a missing one is reported and comes back empty.
func (p *Parser) braced(member func()) (lbrace, rbrace lexer.Token) {
	lbrace, _ = p.expect(lexer.LBrace)
	p.each(lexer.RBrace, member)
	rbrace, _ = p.expect(lexer.RBrace)
	return lbrace, rbrace
}

// skipParens consumes from the current `(` through its matching `)`, and
// reports whether it got there; EOF or a `}` it did not open stops it first.
func (p *Parser) skipParens() bool {
	parens, braces := 0, 0
	for !p.peekIs(lexer.EOF) {
		switch p.peek().Kind {
		case lexer.LParen:
			parens++
		case lexer.RParen:
			parens--
			if parens == 0 {
				p.advance()
				return true
			}
		case lexer.LBrace:
			braces++
		case lexer.RBrace:
			if braces == 0 {
				return false
			}
			braces--
		}
		p.advance()
	}
	return false
}

// listSep consumes the `,` after a list element. Before closer or EOF it
// consumes nothing; any other token is reported.
func (p *Parser) listSep(closer lexer.Kind, element string) {
	switch p.peek().Kind {
	case lexer.Comma:
		p.advance()
	case closer, lexer.EOF:
	default:
		p.errorf(p.peek().Pos, "expected ',' or '%s' after %s, got %s", closer, element, p.peek().Kind)
	}
}

// isUpperFirst reports whether s starts with an upper-case letter.
func isUpperFirst(s string) bool {
	if s == "" {
		return false
	}
	r := []rune(s)[0]
	return unicode.IsUpper(r)
}
