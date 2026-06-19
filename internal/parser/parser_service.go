// Service parsing: service / extend blocks, methods, verbs, and route paths.
package parser

import (
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// parseServiceDecl reads either a primary `service` or (when extend is true)
// a continuation produced by `extend service`. The body parsing is identical
// in both cases.
func (p *Parser) parseServiceDecl(decs []*ast.Decorator, extend bool) *ast.ServiceDecl {
	pos := p.advance().Pos
	name, _ := p.expect(lexer.Ident)
	sd := &ast.ServiceDecl{Pos: pos, Decorators: decs, Doc: p.takeDoc(), Name: name.Text, Extend: extend}
	p.expect(lexer.LBrace)
	for p.peek().Kind != lexer.RBrace && p.peek().Kind != lexer.EOF {
		startPos := p.pos
		m := p.parseMethod()
		if m != nil {
			sd.Members = append(sd.Members, m)
		}
		if p.pos == startPos {
			p.advance()
		}
	}
	rbrace, _ := p.expect(lexer.RBrace)
	if rbrace.Trailing != "" {
		sd.TrailingDoc = []string{rbrace.Trailing}
	}
	if len(rbrace.Doc) > 0 {
		sd.Members = append(sd.Members, &ast.FreeComment{
			Pos:  rbrace.Pos,
			Text: rbrace.Doc,
		})
	}
	return sd
}

// parseExtendService reads `extend service Name { ... }`. Anything other
// than `service` immediately after `extend` is an error - `extend type` and
// friends are NOT supported.
func (p *Parser) parseExtendService(decs []*ast.Decorator) *ast.ServiceDecl {
	p.advance()
	if p.peek().Kind != lexer.KwService {
		p.errorf(p.peek().Pos, "expected 'service' after 'extend'")
		return nil
	}
	return p.parseServiceDecl(decs, true)
}

// rejectMethodTypeSuffix flags `request`/`response` types written with
// an array suffix (`Order[]`) or optional marker (`User?`). Both shapes
// would silently parse without these checks - `[]`/`?` simply leave the
// next iteration on a stray token - so the diagnostic explains the gap
// and steers users to wrap the type in a struct.
func (p *Parser) rejectMethodTypeSuffix(slot string) {
	t := p.peek()
	switch t.Kind {
	case lexer.LBracket:
		p.errorf(t.Pos, "%s type cannot be a bare array - wrap it in a type (e.g. `type Items { items Order[] }`) and reference that type instead", slot)
		// consume `[]` so subsequent parsing doesn't compound the error.
		p.advance()
		if p.peek().Kind == lexer.RBracket {
			p.advance()
		}
	case lexer.Question:
		p.errorf(t.Pos, "%s type cannot be optional - omit the `?` (use a struct field with `?` if a nullable payload is needed)", slot)
		p.advance()
	}
}

// parseMethod reads `[@decorators] <verb> Name [Path] { request? response? }`.
func (p *Parser) parseMethod() *ast.Method {
	p.captureDoc()
	decs := p.parseDecorators()
	t := p.peek()
	verb, ok := verbFromToken(t.Kind)
	if !ok {
		p.errorf(t.Pos, "expected HTTP verb, got %s", t.Kind)
		return nil
	}
	p.advance()
	name, _ := p.expect(lexer.Ident)
	m := &ast.Method{Pos: t.Pos, Decorators: decs, Doc: p.takeDoc(), Verb: verb, Name: name.Text}
	if p.peek().Kind == lexer.Slash {
		m.Path = p.parsePath()
	}
	p.expect(lexer.LBrace)
	for p.peek().Kind != lexer.RBrace && p.peek().Kind != lexer.EOF {
		switch p.peek().Kind {
		case lexer.KwRequest:
			kw := p.advance()
			if m.Request != nil {
				// A second `request` clause would silently discard the first -
				// reject it so the ambiguity surfaces instead of vanishing.
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
			p.errorf(p.peek().Pos, "expected request or response in method body, got %s", p.peek().Kind)
			p.advance()
		}
	}
	rbrace, _ := p.expect(lexer.RBrace)
	if rbrace.Trailing != "" {
		m.TrailingDoc = []string{rbrace.Trailing}
	}
	return m
}

// parsePath reads `/seg1/seg2/...`. A segment is either a literal (including
// hyphenated forms like `api-v1`) or a `{param}`. To avoid swallowing the
// method's opening brace, the `{` form is only recognised when followed
// immediately by an identifier-shaped token and a `}`.
//
// Reserved keywords (`service`, `file`, `type`, ...) and verb tokens
// (`get`, `post`, ...) are accepted as parameter names - they're URL-level
// labels, not language constructs, so collisions with the DSL keyword
// table do not propagate to route grammar (`/logs/{service}` is a path-param
// named `service`, not a literal `/logs/` plus a method body opened by the
// `service` keyword).
func (p *Parser) parsePath() *ast.Path {
	pos := p.peek().Pos
	path := &ast.Path{Pos: pos}
	for p.peek().Kind == lexer.Slash {
		p.advance()
		segPos := p.peek().Pos
		// Path param: `{name}` - disambiguate from method body `{` by
		// requiring an identifier-shaped token followed IMMEDIATELY by
		// `}`. The trailing `}` lookahead matters because once we accept
		// keywords as parameter names, `/ {request ...}` (method body
		// opening with the `request` keyword) would otherwise look like
		// a path-param named `request`. The 3-token shape `{ <word> }`
		// is unambiguous - no method body starts with `<word> }`.
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
			// Hot path: build `word(-word)*` per segment. Builder
			// keeps the inner concat allocation-free.
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
		// Trailing slash: anything else means we have `/` followed by a
		// non-segment token (typically the method body's opening brace).
		path.Segments = append(path.Segments, &ast.PathSegment{Pos: segPos, Literal: ""})
		break
	}
	return path
}

// isPathWordToken reports whether k is a kind whose textual spelling
// is a legal path-segment word. Plain identifiers always qualify; so
// do every keyword and HTTP-verb keyword - when these spellings appear
// inside a URL path they're literal segments, not language tokens.
// This is what lets paths like `/echo-stream` or `/users/get` parse.
func isPathWordToken(k lexer.Kind) bool {
	if k == lexer.Ident {
		return true
	}
	if k >= lexer.KwPackage && k <= lexer.VerbOptions {
		return true
	}
	return false
}

// verbFromToken maps a verb-token [lexer.Kind] to its lowercase spelling.
// Returns ok=false for non-verb kinds so callers can produce a clear error.
func verbFromToken(k lexer.Kind) (string, bool) {
	switch k {
	case lexer.VerbGet:
		return "get", true
	case lexer.VerbPost:
		return "post", true
	case lexer.VerbPut:
		return "put", true
	case lexer.VerbPatch:
		return "patch", true
	case lexer.VerbDelete:
		return "delete", true
	case lexer.VerbHead:
		return "head", true
	case lexer.VerbOptions:
		return "options", true
	}
	return "", false
}
