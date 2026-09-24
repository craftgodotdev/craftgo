package parser

import (
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/errcat"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// parsePackage parses `package name`, taking the comment above as its Doc.
func (p *Parser) parsePackage() *ast.PackageDecl {
	pkgTok := p.advance()
	p.claimDoc(pkgTok)
	name, _ := p.expect(lexer.Ident)
	return &ast.PackageDecl{Pos: name.Pos, Doc: pkgTok.Doc, Name: name.Text}
}

// parseImport parses `import "path"` or `import alias "path"`, taking the
// comment above as Doc and the one after the path as TrailingDoc.
func (p *Parser) parseImport() *ast.Import {
	importTok := p.advance()
	p.claimDoc(importTok)
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

// parseTopLevelWith parses one declaration, prefixing its decorators with
// extra.
func (p *Parser) parseTopLevelWith(extra []*ast.Decorator) ast.Decl {
	p.captureDoc()
	decs := append([]*ast.Decorator{}, extra...)
	decs = append(decs, p.parseDecorators()...)
	t := p.peek()
	// Comments inside the decorator chain are not free comments.
	if len(decs) > 0 {
		p.claimCommentsBetween(decs[0].Pos.Line, t.Pos.Line)
	}
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
	case lexer.KwEvent:
		return p.parseEventDecl(decs)
	case lexer.KwService:
		return p.parseServiceDecl(decs, false)
	case lexer.KwExtend:
		// Keep a nil *ServiceDecl out of the Decl interface.
		sd := p.parseExtendService(decs)
		if sd == nil {
			return nil
		}
		return sd
	case lexer.EOF:
		if len(decs) > 0 {
			p.errorf(decs[0].Pos, "decorators without a declaration to attach to")
		}
		return nil
	}
	p.errorf(t.Pos, "expected declaration, got %s", t.Kind)
	return nil
}

// parseEnumDecl parses `enum Name { ... }`, accepting any mix of value kinds.
func (p *Parser) parseEnumDecl(decs []*ast.Decorator) *ast.EnumDecl {
	pos := p.advance().Pos
	name, _ := p.expect(lexer.Ident)
	ed := &ast.EnumDecl{Pos: pos, Decorators: decs, Doc: p.takeDoc(), Name: name.Text}
	lbrace, _ := p.expect(lexer.LBrace)
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
	fcs := p.harvestFreeComments(lbrace.Pos.Line, rbrace.Pos.Line)
	ed.Members = mergeFreeComments(ed.Members, fcs, func(fc *ast.FreeComment) ast.EnumMember { return fc })
	return ed
}

// parseEnumValue parses `Name` or `Name = literal`, then its decorators.
func (p *Parser) parseEnumValue() *ast.EnumValue {
	p.captureDoc()
	t := p.peek()
	// A reserved word is a value name here.
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
		case lexer.Dash:
			// `Name = -1`: the sign and the integer are one value.
			p.advance()
			if p.peek().Kind != lexer.Int {
				p.errorf(p.peek().Pos, "expected integer after '-' in enum value")
				break
			}
			tok := p.advance()
			n, _ := strconv.ParseInt("-"+tok.Text, 10, 64)
			v.IntValue = n
			v.Kind = ast.EnumInt
		default:
			p.errorf(p.peek().Pos, "expected int or string for enum value")
		}
	}
	v.Decorators = p.parseDecorators()
	return v
}

// parseErrorDecl parses `error Category Name` with an optional `{ ... }` body;
// Category must be one of [errcat.Categories].
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

// parseScalarDecl parses `scalar Name primitive` and the decorators after it.
func (p *Parser) parseScalarDecl(decs []*ast.Decorator) *ast.ScalarDecl {
	pos := p.advance().Pos
	name, _ := p.expect(lexer.Ident)
	prim, _ := p.expect(lexer.Ident)
	sd := &ast.ScalarDecl{Pos: pos, Decorators: decs, Doc: p.takeDoc(), Name: name.Text, Primitive: prim.Text}
	// Only decorators on the primitive's line trail the scalar; one on a later
	// line starts the next declaration's chain.
	for p.peek().Kind == lexer.At && p.peek().Pos.Line == prim.Pos.Line {
		sd.Decorators = append(sd.Decorators, p.parseDecorator())
	}
	return sd
}

// parseMiddlewareDecl parses `middleware Name`; a parameter list after the
// name is reported and skipped.
func (p *Parser) parseMiddlewareDecl(decs []*ast.Decorator) *ast.MiddlewareDecl {
	pos := p.advance().Pos
	name, _ := p.expect(lexer.Ident)
	md := &ast.MiddlewareDecl{Pos: pos, Decorators: decs, Doc: p.takeDoc(), Name: name.Text}
	if p.peek().Kind == lexer.LParen {
		p.errorf(p.peek().Pos, "middleware declaration takes no parameters - configuration lives in the generated impl file, not the DSL")
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
