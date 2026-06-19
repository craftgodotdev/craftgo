// Top-level declaration parsing: package/import lines and the enum / error /
// scalar / middleware declarations.
package parser

import (
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/errcat"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// parsePackage reads the `package <name>` line. Any `//` block
// preceding the `package` keyword is captured as Doc so file-header
// comments survive a parse / format round-trip.
func (p *Parser) parsePackage() *ast.PackageDecl {
	pkgTok := p.advance()
	name, _ := p.expect(lexer.Ident)
	return &ast.PackageDecl{Pos: name.Pos, Doc: pkgTok.Doc, Name: name.Text}
}

// parseImport reads `import [alias] "path"`. Both alias and path are
// optional from a token-stream perspective; missing path is reported as a
// diagnostic but not fatal.
//
// Captures Doc from the `import` keyword token (the lexer attaches the
// preceding `//` block there) and TrailingDoc from the path string token
// (the lexer's [Token.Trailing] holds same-line `// note` comments).
func (p *Parser) parseImport() *ast.Import {
	importTok := p.advance()
	imp := &ast.Import{Pos: importTok.Pos, Doc: importTok.Doc}
	if p.peek().Kind == lexer.Ident {
		imp.Alias = p.advance().Text
	}
	str, ok := p.expect(lexer.String)
	if ok {
		imp.Path = unquoteString(str.Text)
		imp.TrailingDoc = str.Trailing
	}
	return imp
}

// parseTopLevelWith parses one declaration. `extra` carries decorators that
// were already consumed by the caller (used to forward leading decorators
// from the file scope into the first declaration).
func (p *Parser) parseTopLevelWith(extra []*ast.Decorator) ast.Decl {
	p.captureDoc()
	decs := append([]*ast.Decorator{}, extra...)
	decs = append(decs, p.parseDecorators()...)
	t := p.peek()
	switch t.Kind {
	case lexer.KwType:
		return p.parseTypeDecl(decs)
	case lexer.KwEnum:
		return p.parseEnumDecl(decs)
	case lexer.KwError:
		return p.parseErrorDecl(decs)
	case lexer.KwScalar:
		return p.parseScalarDecl(decs)
	case lexer.KwMiddleware:
		return p.parseMiddlewareDecl(decs)
	case lexer.KwService:
		return p.parseServiceDecl(decs, false)
	case lexer.KwExtend:
		// parseExtendService returns a `*ast.ServiceDecl` (concrete
		// pointer) and may emit a typed nil on incomplete input
		// (`extend` with nothing - or anything other than `service`
		// - after it). Returning that pointer directly would wrap a
		// typed-nil into the ast.Decl interface, which then passes
		// the `d != nil` guard at the call site and crashes the
		// semantic phase on a nil dereference. Convert the typed nil
		// to an untyped nil-interface here so the caller's guard
		// works as expected.
		sd := p.parseExtendService(decs)
		if sd == nil {
			return nil
		}
		return sd
	case lexer.EOF:
		return nil
	}
	p.errorf(t.Pos, "expected declaration, got %s", t.Kind)
	return nil
}

// parseEnumDecl reads `enum Name { Values }`. Mixed value kinds are
// accepted at this layer; the semantic phase rejects them.
func (p *Parser) parseEnumDecl(decs []*ast.Decorator) *ast.EnumDecl {
	pos := p.advance().Pos
	name, _ := p.expect(lexer.Ident)
	ed := &ast.EnumDecl{Pos: pos, Decorators: decs, Doc: p.takeDoc(), Name: name.Text}
	p.expect(lexer.LBrace)
	for p.peek().Kind != lexer.RBrace && p.peek().Kind != lexer.EOF {
		startPos := p.pos
		v := p.parseEnumValue()
		if v != nil {
			ed.Members = append(ed.Members, v)
		}
		if p.pos == startPos {
			p.advance()
		}
	}
	rbrace, _ := p.expect(lexer.RBrace)
	if rbrace.Trailing != "" {
		ed.TrailingDoc = []string{rbrace.Trailing}
	}
	if len(rbrace.Doc) > 0 {
		ed.Members = append(ed.Members, &ast.FreeComment{
			Pos:  rbrace.Pos,
			Text: rbrace.Doc,
		})
	}
	return ed
}

// parseEnumValue reads a single `Name [= literal] [@decorators]` entry.
func (p *Parser) parseEnumValue() *ast.EnumValue {
	// An enum body holds only value names, so a reserved word here is a
	// value name (contextual keyword), e.g. `enum Kind { type ... }`.
	p.captureDoc()
	t := p.peek()
	if t.Kind != lexer.Ident && !isKeywordKind(t.Kind) {
		p.errorf(t.Pos, "expected enum value name, got %s", t.Kind)
		return nil
	}
	p.advance()
	v := &ast.EnumValue{Pos: t.Pos, Doc: p.takeDoc(), Name: t.Text, Kind: ast.EnumBare}
	if p.peek().Kind == lexer.Equal {
		p.advance()
		switch p.peek().Kind {
		case lexer.Int:
			tok := p.advance()
			n, _ := strconv.ParseInt(tok.Text, 10, 64)
			v.IntValue = n
			v.Kind = ast.EnumInt
		case lexer.String:
			tok := p.advance()
			v.StrValue = unquoteString(tok.Text)
			v.Kind = ast.EnumString
		default:
			p.errorf(p.peek().Pos, "expected int or string for enum value")
		}
	}
	v.Decorators = p.parseDecorators()
	return v
}

// parseErrorDecl reads `error <Category> Name [{ Body }]`. The reserved
// category set lives in [errcat] (the leaf codegen + the LSP also read), so the
// `error <Category>` form, the emitted HTTP status, and the editor completions
// share one catalogue.
func (p *Parser) parseErrorDecl(decs []*ast.Decorator) *ast.ErrorDecl {
	pos := p.advance().Pos
	cat, _ := p.expect(lexer.Ident)
	if cat.Text != "" && !errcat.IsCategory(cat.Text) {
		p.errorf(cat.Pos, "unknown error category %q", cat.Text)
	}
	name, _ := p.expect(lexer.Ident)
	ed := &ast.ErrorDecl{Pos: pos, Decorators: decs, Doc: p.takeDoc(), Category: cat.Text, Name: name.Text}
	if p.peek().Kind == lexer.LBrace {
		ed.HasBody = true
		body, rbrace := p.parseTypeBody()
		ed.Body = body
		if rbrace.Trailing != "" {
			ed.TrailingDoc = []string{rbrace.Trailing}
		}
	}
	return ed
}

// parseScalarDecl reads `scalar Name <PrimitiveType> [@decorators]`.
func (p *Parser) parseScalarDecl(decs []*ast.Decorator) *ast.ScalarDecl {
	pos := p.advance().Pos
	name, _ := p.expect(lexer.Ident)
	prim, _ := p.expect(lexer.Ident)
	sd := &ast.ScalarDecl{Pos: pos, Decorators: decs, Doc: p.takeDoc(), Name: name.Text, Primitive: prim.Text}
	// Trailing decorators on the same line as the primitive belong to the
	// scalar (`scalar Email string @pattern(...)`). A decorator on a later
	// line is the leading decorator of the next declaration, so stop -
	// consuming it here would steal it from the following type/enum/service.
	for p.peek().Kind == lexer.At && p.peek().Pos.Line == prim.Pos.Line {
		sd.Decorators = append(sd.Decorators, p.parseDecorator())
	}
	return sd
}

// parseMiddlewareDecl reads `middleware Name`. The DSL captures only the
// name; param shape and behaviour live in the hand-written Go impl
// file the scaffolder produces. Any trailing `(...)` on a middleware
// declaration is a parse error (the parser still consumes it to keep
// downstream passes alive when an LSP user is mid-typing, but it does
// not retain the data).
func (p *Parser) parseMiddlewareDecl(decs []*ast.Decorator) *ast.MiddlewareDecl {
	pos := p.advance().Pos
	name, _ := p.expect(lexer.Ident)
	md := &ast.MiddlewareDecl{Pos: pos, Decorators: decs, Doc: p.takeDoc(), Name: name.Text}
	if p.peek().Kind == lexer.LParen {
		p.errorf(p.peek().Pos, "middleware declaration takes no parameters - configuration lives in the generated impl file, not the DSL")
		// Recover by consuming up to the matching ')' so the rest of
		// the file still parses.
		depth := 0
		for {
			t := p.peek()
			if t.Kind == lexer.EOF {
				break
			}
			if t.Kind == lexer.LParen {
				depth++
			} else if t.Kind == lexer.RParen {
				depth--
				if depth == 0 {
					p.advance()
					break
				}
			}
			p.advance()
		}
	}
	return md
}
