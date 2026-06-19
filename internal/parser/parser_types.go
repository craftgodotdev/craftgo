// Type declaration and type-reference parsing: type bodies, fields vs
// mixins, generics, maps, arrays, and qualified names.
package parser

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// parseTypeDecl reads `type Name[<TypeParams>] { Body }`.
func (p *Parser) parseTypeDecl(decs []*ast.Decorator) *ast.TypeDecl {
	pos := p.advance().Pos
	name, _ := p.expect(lexer.Ident)
	td := &ast.TypeDecl{Pos: pos, Decorators: decs, Doc: p.takeDoc(), Name: name.Text}
	if p.peek().Kind == lexer.LAngle {
		td.TypeParams = p.parseTypeParams()
	}
	body, rbrace := p.parseTypeBody()
	td.Body = body
	if rbrace.Trailing != "" {
		td.TrailingDoc = []string{rbrace.Trailing}
	}
	return td
}

// parseTypeParams reads `<T1, T2, ...>` after a generic type name. An empty
// `<>` list is reported as an error but parsing recovers gracefully.
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
		// A duplicate type-parameter name lowers to `type X[T any, T any]`,
		// which the Go compiler rejects ("T redeclared"). Reject it here -
		// parallel to the empty-list guard above - so the design fails with a
		// clear diagnostic instead of non-compiling generated Go.
		if seen[t.Text] {
			p.errorf(t.Pos, "duplicate type parameter %q", t.Text)
		} else {
			seen[t.Text] = true
			params = append(params, t.Text)
		}
		if p.peek().Kind == lexer.Comma {
			p.advance()
		}
	}
	p.expect(lexer.RAngle)
	return params
}

// parseTypeBody reads the contents of a `{ ... }` type/error body. Returns
// the body slice plus the closing `}` token (whose Trailing field carries
// the `// note` after the brace, captured by the lexer). Callers stash
// that trailing on the surrounding decl's TrailingDoc so it survives
// parse → format round-trip.
//
// The body slice contains only [*Field] and [*Mixin] members today.
// The [*FreeComment] member type is defined and the format printer
// can render it, but the parser doesn't yet populate FreeComment
// entries - doing so reliably requires per-comment-line position
// tracking in the lexer so the disambiguation between free-floating
// comments and trailing comments mis-attached to the next non-trivia
// token is sound.
func (p *Parser) parseTypeBody() ([]ast.TypeMember, lexer.Token) {
	if !p.peekIs(lexer.LBrace) {
		return nil, lexer.Token{}
	}
	p.advance()
	var members []ast.TypeMember
	for p.peek().Kind != lexer.RBrace && p.peek().Kind != lexer.EOF {
		startPos := p.pos
		m := p.parseTypeMember()
		if m != nil {
			members = append(members, m)
		}
		if p.pos == startPos {
			p.advance()
		}
	}
	rbrace, _ := p.expect(lexer.RBrace)
	if len(rbrace.Doc) > 0 {
		members = append(members, &ast.FreeComment{
			Pos:  rbrace.Pos,
			Text: rbrace.Doc,
		})
	}
	return members, rbrace
}

// parseTypeMember reads one member of a type body - either a [Field] or a
// [Mixin]. Disambiguation rules (in priority order):
//
//  1. Next token is `.` or `<` → mixin (qualified or generic name,
//     e.g. `shared.Profile`, `Page<User>`).
//  2. Next token on the same line is a builtin primitive (`string`,
//     `int`, ...) or `map` → field. Works for PascalCase names too
//     (`CreateUser int` is a field with an exported JSON tag).
//  3. First identifier starts lowercase → field (the canonical form).
//  4. Otherwise → mixin (PascalCase ident alone, OR followed by a
//     non-builtin Ident which is the start of the NEXT member in
//     compact `Profile  name string` form).
//
// The "Pascal + builtin → field" carve-out lets users name a field
// anything they want - including PascalCase JSON keys - without
// breaking the compact mixin+field-on-one-line form that test
// fixtures rely on. Rule (4) only kicks in when the next token is
// an Ident that is NOT a builtin (i.e. another field name in the
// compact form).
func (p *Parser) parseTypeMember() ast.TypeMember {
	p.captureDoc()
	decs := p.parseDecorators()
	t := p.peek()
	// A reserved word at the start of a type-body member is a FIELD NAME:
	// a member is a field or a mixin, a mixin is a named type reference,
	// and a keyword never spells one - so the keyword can only be the
	// field's name. Take its spelling as the identifier (contextual
	// keyword), letting `type`, `error`, `map`, ... be field names.
	if isKeywordKind(t.Kind) {
		name := p.advance()
		tref := p.parseTypeRef()
		fieldDecs := p.parseDecorators()
		return &ast.Field{Pos: name.Pos, Doc: p.takeDoc(), Name: name.Text, Type: tref, Decorators: append(decs, fieldDecs...)}
	}
	if t.Kind != lexer.Ident {
		p.errorf(t.Pos, "expected field or mixin, got %s", t.Kind)
		return nil
	}
	next := p.peekAt(1)
	if next.Kind == lexer.Dot || next.Kind == lexer.LAngle {
		ref := p.parseNamedTypeRef()
		p.rejectMixinDecorators(t.Pos, decs)
		p.takeDoc()
		return &ast.Mixin{Pos: t.Pos, Ref: ref}
	}
	if isFieldFollower(next, t.Pos.Line) || !isUpperFirst(t.Text) {
		name := p.advance()
		tref := p.parseTypeRef()
		fieldDecs := p.parseDecorators()
		return &ast.Field{Pos: name.Pos, Doc: p.takeDoc(), Name: name.Text, Type: tref, Decorators: append(decs, fieldDecs...)}
	}
	ref := p.parseNamedTypeRef()
	p.rejectMixinDecorators(t.Pos, decs)
	p.takeDoc()
	return &ast.Mixin{Pos: t.Pos, Ref: ref}
}

// isFieldFollower reports whether `next` (the token AFTER a leading
// identifier in a type-body member) is the first token of a TypeRef
// on the same line - the unambiguous signal that the leading ident
// is a field NAME and `next` begins its type. Builtin primitives
// and `map` are the only signals that work without semantic info;
// arbitrary Idents are ambiguous (could be a custom type, could be
// the next member's name in compact form) and stay covered by the
// case-based default.
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
	return parserBuiltinTypes[next.Text]
}

// parserBuiltinTypes aliases the canonical [idents.BuiltinTypes]
// table so the parser's disambiguation rules ("Pascal followed by
// builtin → field") consult the same set as the semantic resolver.
// Adding a new primitive is a one-line edit in [internal/idents].
var parserBuiltinTypes = idents.BuiltinTypes

// parseTypeRef parses TypeRef = (MapType | NamedTypeRef) ArrayMod? OptionalMod?
//
// Array (`[]`) and Optional (`?`) are independent suffix flags - `T[]?`
// produces a TypeRef with both set.
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

// parseMapType reads `map<K, V>`.
func (p *Parser) parseMapType() *ast.MapType {
	pos := p.advance().Pos
	p.expect(lexer.LAngle)
	key := p.parseTypeRef()
	p.expect(lexer.Comma)
	val := p.parseTypeRef()
	p.expect(lexer.RAngle)
	return &ast.MapType{Pos: pos, Key: key, Value: val}
}

// parseNamedTypeRef reads a (possibly-generic, possibly-qualified) type
// reference such as `User`, `pkg.User`, or `Page<User, Org>`.
func (p *Parser) parseNamedTypeRef() *ast.NamedTypeRef {
	qi := p.parseQualifiedIdent()
	nt := &ast.NamedTypeRef{Pos: qi.Pos, Name: qi}
	if p.peek().Kind == lexer.LAngle {
		p.advance()
		for p.peek().Kind != lexer.RAngle && p.peek().Kind != lexer.EOF {
			nt.Args = append(nt.Args, p.parseTypeRef())
			if p.peek().Kind == lexer.Comma {
				p.advance()
			}
		}
		p.expect(lexer.RAngle)
	}
	return nt
}

// parseQualifiedIdent reads `Ident(.Ident)*` and returns the parts. On any
// expectation failure it returns the partial result so downstream code can
// still associate the error with a named-ish entity.
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
