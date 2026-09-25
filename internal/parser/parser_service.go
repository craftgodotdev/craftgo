package parser

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// parseServiceDecl parses `service Name { ... }`; extend marks the body of an
// `extend service`. header is the line of the declaration's first keyword.
func (p *Parser) parseServiceDecl(decs []*ast.Decorator, doc []string, header int, extend bool) *ast.ServiceDecl {
	pos := p.advance().Pos
	name, _ := p.expect(lexer.Ident)
	sd := &ast.ServiceDecl{Pos: pos, Decorators: decs, Doc: doc, Name: name.Text, Extend: extend}
	_, rbrace := p.braced(func() {
		if m := p.parseServiceMember(); m != nil {
			sd.Members = append(sd.Members, m)
		}
	})
	sd.EndPos = rbrace.Pos
	// Method bodies already claimed their comments, so this collects the
	// blocks between members and, as the body's first, one in the header.
	fcs := p.harvestFreeComments(header, rbrace.Pos.Line)
	sd.Members = mergeFreeComments(sd.Members, fcs, func(fc *ast.FreeComment) ast.ServiceMember { return fc })
	return sd
}

// parseExtendService parses `extend service Name { ... }`, returning nil when
// `service` does not follow `extend`.
func (p *Parser) parseExtendService(decs []*ast.Decorator, doc []string) ast.Decl {
	extend := p.advance()
	if p.peek().Kind != lexer.KwService {
		p.errorf(p.peek().Pos, "expected 'service' after 'extend'")
		return nil
	}
	return p.parseServiceDecl(decs, doc, extend.Pos.Line, true)
}

// rejectMethodTypeSuffix reports and skips a `[]` or `?` after a request or
// response type.
func (p *Parser) rejectMethodTypeSuffix(slot string) {
	t := p.peek()
	if t.Kind != lexer.LBracket {
		p.rejectOptionalSuffix(slot)
		return
	}
	p.errorf(t.Pos, "%s type cannot be a bare array - wrap it in a type (e.g. `type Items { items Order[] }`) and reference that type instead", slot)
	p.advance()
	if p.peek().Kind == lexer.RBracket {
		p.advance()
	}
}

// rejectOptionalSuffix reports and skips a `?` after a clause type.
func (p *Parser) rejectOptionalSuffix(slot string) {
	t := p.peek()
	if t.Kind != lexer.Question {
		return
	}
	p.errorf(t.Pos, "%s type cannot be optional - omit the `?` (use a struct field with `?` if a nullable payload is needed)", slot)
	p.advance()
}

// parseEventPayloadSuffix parses a payload's suffix: one `[]` sets Array, and a
// second `[]` or a `?` is reported and skipped.
func (p *Parser) parseEventPayloadSuffix(pl *ast.EventPayload) {
	for p.peek().Kind == lexer.LBracket {
		t := p.advance()
		p.expect(lexer.RBracket)
		if pl.Array {
			p.errorf(t.Pos, "payload type cannot be a nested array - a payload is a type or an array of one; wrap the inner array in a type instead")
			continue
		}
		pl.Array = true
	}
	p.rejectOptionalSuffix("payload")
}

// parseServiceMember parses one method with its doc and decorators.
func (p *Parser) parseServiceMember() ast.ServiceMember {
	doc := p.docAbove()
	decs := p.parseDecorators()
	t := p.peek()
	if !t.Kind.IsVerb() {
		p.errorf(t.Pos, "%s", serviceMemberError(t))
		return nil
	}
	p.claimChain(decs, t.Pos.Line)
	m := p.parseMethod(decs, doc)
	p.rejectDecoratorsAfter("method")
	return m
}

// serviceMemberError is the diagnostic for a service member that is not a
// method; `event` and `consume` get their own.
func serviceMemberError(t lexer.Token) string {
	switch {
	case t.Kind == lexer.KwEvent:
		return "`event` is a file-level declaration - move `event ... { payload ... }` out of the service body; a service holds HTTP methods only"
	case t.Kind == lexer.Ident && t.Text == "consume":
		return "a service has no `consume` member - which events a deployable listens to is Go code, written where its bus is built"
	}
	return "expected an HTTP verb, got " + t.Kind.String()
}

// memberBody is the closing brace and the comments of a method or event body.
type memberBody struct {
	Comments []*ast.FreeComment
	EndPos   ast.Pos
}

// parseMemberBody parses a `{ ... }` body. fn parses a clause starting at the
// given token and reports whether it knew it; others are reported and skipped.
// A comment block below header, the keyword's line, and above the `{` is the
// body's first comment.
func (p *Parser) parseMemberBody(header int, fn func(lexer.Token) bool, expected string) memberBody {
	_, rbrace := p.braced(func() {
		if !fn(p.peek()) {
			p.errorf(p.peek().Pos, "expected %s, got %s", expected, p.peek().Kind)
			p.advance()
		}
	})
	return memberBody{
		Comments: p.harvestFreeComments(header, rbrace.Pos.Line),
		EndPos:   rbrace.Pos,
	}
}

// parseMethod parses `verb Name /path { ... }`, where the path is optional.
func (p *Parser) parseMethod(decs []*ast.Decorator, doc []string) *ast.Method {
	verb := p.advance()
	name, _ := p.expect(lexer.Ident)
	m := &ast.Method{Pos: verb.Pos, Decorators: decs, Doc: doc, Verb: verb.Text, Name: name.Text}
	if p.peek().Kind == lexer.Slash {
		m.Path = p.parsePath()
	}
	body := p.parseMemberBody(verb.Pos.Line, func(tok lexer.Token) bool {
		switch tok.Kind {
		case lexer.KwRequest:
			kw := p.advance()
			if m.Request != nil {
				p.errorf(kw.Pos, "duplicate request clause in method %q", m.Name)
			}
			m.Request = p.parseNamedTypeRef()
			p.rejectMethodTypeSuffix("request")
		case lexer.KwResponse:
			kw := p.advance()
			if m.Response != nil {
				p.errorf(kw.Pos, "duplicate response clause in method %q", m.Name)
			}
			mr := &ast.MethodResponse{Pos: p.peek().Pos}
			mr.Type = p.parseNamedTypeRef()
			m.Response = mr
			p.rejectMethodTypeSuffix("response")
		default:
			return false
		}
		return true
	}, "request or response in method body")
	m.BodyComments, m.EndPos = body.Comments, body.EndPos
	return m
}

// parseEventDecl parses `event Name { payload Type }`; Type may carry one `[]`.
func (p *Parser) parseEventDecl(decs []*ast.Decorator, doc []string) *ast.EventDecl {
	t := p.advance()
	name, _ := p.expect(lexer.Ident)
	e := &ast.EventDecl{Pos: t.Pos, Decorators: decs, Doc: doc, Name: name.Text}
	body := p.parseMemberBody(t.Pos.Line, func(tok lexer.Token) bool {
		if tok.Kind != lexer.KwPayload {
			return false
		}
		kw := p.advance()
		if e.Payload != nil {
			p.errorf(kw.Pos, "duplicate payload clause in event %q", e.Name)
		}
		e.Payload = &ast.EventPayload{Pos: p.peek().Pos, Type: p.parseNamedTypeRef()}
		p.parseEventPayloadSuffix(e.Payload)
		return true
	}, "payload in event body")
	e.BodyComments, e.EndPos = body.Comments, body.EndPos
	return e
}

// parsePath parses a route such as `/api-v1/users/{id}`. A segment is words
// joined by `-`, or a `{word}` parameter; reserved words count as words.
func (p *Parser) parsePath() *ast.Path {
	pos := p.peek().Pos
	path := &ast.Path{Pos: pos}
	for p.peek().Kind == lexer.Slash {
		p.advance()
		segPos := p.peek().Pos
		// Only `{ word }` is a parameter; no valid method body has that shape.
		if p.peek().Kind == lexer.LBrace &&
			isPathWordToken(p.peekAt(1).Kind) &&
			p.peekAt(2).Kind == lexer.RBrace {
			p.advance()
			nameTok := p.advance()
			p.expect(lexer.RBrace)
			path.Segments = append(path.Segments, &ast.PathSegment{Pos: segPos, Param: true, Literal: nameTok.Text})
			continue
		}
		if isPathWordToken(p.peek().Kind) {
			var sb strings.Builder
			sb.WriteString(p.advance().Text)
			for p.peek().Kind == lexer.Dash {
				dashPos := p.advance().Pos
				if !isPathWordToken(p.peek().Kind) {
					p.errorf(dashPos, "path segment ends in '-'")
					break
				}
				sb.WriteByte('-')
				sb.WriteString(p.advance().Text)
			}
			path.Segments = append(path.Segments, &ast.PathSegment{Pos: segPos, Literal: sb.String()})
			continue
		}
		// `//` and a trailing `/` are errors, since a mux pattern ending in `/`
		// matches a whole subtree; a lone `/` is the root path.
		if p.peek().Kind == lexer.Slash {
			p.errorf(segPos, "empty path segment ('//')")
			continue
		}
		if len(path.Segments) > 0 {
			p.errorf(segPos, "path ends with '/'")
			break
		}
		path.Segments = append(path.Segments, &ast.PathSegment{Pos: segPos, Literal: ""})
		break
	}
	return path
}

// isPathWordToken reports whether k spells a path word: an identifier or a
// reserved word.
func isPathWordToken(k lexer.Kind) bool {
	return k == lexer.Ident || k.IsKeyword()
}
