package parser

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/prims"
)

// parseTypeDecl parses `type Name { ... }` or `type Name<T, ...> { ... }`; a
// missing body is reported, and the next declaration starts at the token
// found instead.
func (p *Parser) parseTypeDecl(decs []*ast.Decorator, doc []string) *ast.TypeDecl {
	pos := p.advance().Pos
	name, _ := p.expect(lexer.Ident)
	td := &ast.TypeDecl{Pos: pos, Decorators: decs, Doc: doc, Name: name.Text}
	if p.peek().Kind == lexer.LAngle {
		td.TypeParams = p.parseTypeParams()
	}
	if !p.peekIs(lexer.LBrace) {
		p.expect(lexer.LBrace)
		return td
	}
	body, rbrace := p.parseTypeBody()
	td.Body, td.EndPos = body, rbrace.Pos
	return td
}

// parseTypeParams parses `<T, U>`, reporting an empty list and repeated names.
func (p *Parser) parseTypeParams() []string {
	p.advance()
	if p.peek().Kind == lexer.RAngle {
		p.errorf(p.peek().Pos, "type params list cannot be empty")
		p.advance()
		return nil
	}
	var params []string
	seen := map[string]bool{}
	for p.peek().Kind != lexer.RAngle && p.peek().Kind != lexer.EOF {
		t, ok := p.expect(lexer.Ident)
		if !ok {
			break
		}
		// A repeated name would generate Go that does not compile.
		if seen[t.Text] {
			p.errorf(t.Pos, "duplicate type parameter %q", t.Text)
		} else {
			seen[t.Text] = true
			params = append(params, t.Text)
		}
		p.listSep(lexer.RAngle, "type parameter")
	}
	p.expect(lexer.RAngle)
	return params
}

// parseTypeBody parses the type or error body that opens at the current `{`
// into fields, mixins and free comments, and returns the closing brace token.
func (p *Parser) parseTypeBody() ([]ast.TypeMember, lexer.Token) {
	var members []ast.TypeMember
	lbrace, rbrace := p.braced(func() {
		if m := p.parseTypeMember(); m != nil {
			members = append(members, m)
		}
	})
	fcs := p.harvestFreeComments(lbrace.Pos.Line, rbrace.Pos.Line)
	members = mergeFreeComments(members, fcs, func(fc *ast.FreeComment) ast.TypeMember { return fc })
	return members, rbrace
}

// parseTypeMember parses a field or a mixin. A mixin is a name followed by `.`
// or `<`, or an upper-case name not followed on its line by a primitive or `map`.
func (p *Parser) parseTypeMember() ast.TypeMember {
	doc := p.docAbove()
	decs := p.parseDecorators()
	t := p.peek()
	p.claimChain(decs, t.Pos.Line)
	switch {
	case t.Kind.IsKeyword():
		// A reserved word never names a type, so here it is a field name.
		return p.parseField(doc, decs)
	case t.Kind != lexer.Ident:
		p.errorf(t.Pos, "expected field or mixin, got %s", t.Kind)
		return nil
	}
	next := p.peekAt(1)
	if next.Kind != lexer.Dot && next.Kind != lexer.LAngle && (isFieldFollower(next, t.Pos.Line) || !isUpperFirst(t.Text)) {
		return p.parseField(doc, decs)
	}
	ref := p.parseNamedTypeRef()
	p.rejectMixinDecorators(t.Pos, decs)
	p.rejectMixinTrailingDecorators(t.Pos)
	return &ast.Mixin{Pos: t.Pos, Doc: doc, Ref: ref}
}

// parseField parses `name Type` and the decorators after it; decs are the
// ones before it.
func (p *Parser) parseField(doc []string, decs []*ast.Decorator) *ast.Field {
	name := p.advance()
	tref := p.parseTypeRef()
	trailing := p.parseDecorators()
	p.claimTrailing(name.Pos.Line, trailing)
	return &ast.Field{Pos: name.Pos, Doc: doc, Name: name.Text, Type: tref, Decorators: append(decs, trailing...)}
}

// rejectMixinTrailingDecorators reports and consumes decorators on the mixin's
// line, which would otherwise attach to the next member.
func (p *Parser) rejectMixinTrailingDecorators(pos lexer.Position) {
	if p.peek().Kind != lexer.At || p.peek().Pos.Line != p.tokens[p.pos-1].Pos.Line {
		return
	}
	p.rejectMixinDecorators(pos, p.parseDecorators())
}

// isFieldFollower reports whether next is a primitive or `map` on line
// sameLine; any other identifier may be a type or the next member's name.
func isFieldFollower(next lexer.Token, sameLine int) bool {
	if next.Pos.Line != sameLine {
		return false
	}
	if next.Kind == lexer.KwMap {
		return true
	}
	if next.Kind != lexer.Ident {
		return false
	}
	return prims.Is(next.Text)
}

// parseTypeRef parses `map<K, V>` or a named type, then any `[]` suffixes and
// an optional `?`.
func (p *Parser) parseTypeRef() *ast.TypeRef {
	pos := p.peek().Pos
	tr := &ast.TypeRef{Pos: pos}
	if p.peek().Kind == lexer.KwMap {
		tr.Map = p.parseMapType()
	} else {
		tr.Named = p.parseNamedTypeRef()
	}
	for p.peek().Kind == lexer.LBracket {
		p.advance()
		p.expect(lexer.RBracket)
		tr.ArrayDepth++
	}
	tr.Array = tr.ArrayDepth > 0
	if p.peek().Kind == lexer.Question {
		p.advance()
		tr.Optional = true
	}
	return tr
}

// parseMapType parses `map<K, V>`.
func (p *Parser) parseMapType() *ast.MapType {
	pos := p.advance().Pos
	p.expect(lexer.LAngle)
	key := p.parseTypeRef()
	p.expect(lexer.Comma)
	val := p.parseTypeRef()
	p.expect(lexer.RAngle)
	return &ast.MapType{Pos: pos, Key: key, Value: val}
}

// parseNamedTypeRef parses a type name such as `User`, `pkg.User` or
// `Page<User, Org>`.
func (p *Parser) parseNamedTypeRef() *ast.NamedTypeRef {
	qi := p.parseQualifiedIdent()
	nt := &ast.NamedTypeRef{Pos: qi.Pos, Name: qi}
	if p.peek().Kind == lexer.LAngle {
		langle := p.advance()
		if p.peek().Kind == lexer.RAngle {
			p.errorf(langle.Pos, "type argument list cannot be empty")
		}
		for p.peek().Kind != lexer.RAngle && p.peek().Kind != lexer.EOF {
			start := p.pos
			nt.Args = append(nt.Args, p.parseTypeRef())
			p.listSep(lexer.RAngle, "type argument")
			if p.pos == start {
				// No progress: parseTypeRef already reported this token.
				break
			}
		}
		p.expect(lexer.RAngle)
	}
	return nt
}

// parseQualifiedIdent parses `a.b.C`; on an error it returns the parts read so
// far.
func (p *Parser) parseQualifiedIdent() *ast.QualifiedIdent {
	pos := p.peek().Pos
	qi := &ast.QualifiedIdent{Pos: pos}
	first, ok := p.expect(lexer.Ident)
	if !ok {
		return qi
	}
	qi.Parts = append(qi.Parts, first.Text)
	for p.peek().Kind == lexer.Dot {
		p.advance()
		next, ok := p.expect(lexer.Ident)
		if !ok {
			return qi
		}
		qi.Parts = append(qi.Parts, next.Text)
	}
	return qi
}
