// Service parsing: service / extend blocks, methods, events, consumers,
// verbs, and route paths.
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
	lbrace, _ := p.expect(lexer.LBrace)
	for p.peek().Kind != lexer.RBrace && p.peek().Kind != lexer.EOF {
		startPos := p.pos
		m := p.parseServiceMember()
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
	// Methods harvest their own body comments first (and claim them), so
	// this pass only picks up the blocks between members.
	fcs := p.harvestFreeComments(lbrace.Pos.Line, rbrace.Pos.Line)
	sd.Members = mergeFreeComments(sd.Members, fcs, func(fc *ast.FreeComment) ast.ServiceMember { return fc })
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

// rejectMethodTypeSuffix flags a clause type (`request`, `response`,
// `payload`, `event`) written with an array suffix (`Order[]`) or an
// optional marker (`User?`). Both shapes would silently parse without
// these checks - `[]`/`?` simply leave the next iteration on a stray
// token - so the diagnostic explains the gap and steers users to wrap
// the type in a struct.
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

// parseServiceMember reads one member of a service body: an HTTP method,
// an `event` contract, or a `consume` declaration. The leading doc and
// decorator chain are shared by all three, so they are read once here
// and handed to the kind-specific parser.
func (p *Parser) parseServiceMember() ast.ServiceMember {
	p.captureDoc()
	decs := p.parseDecorators()
	t := p.peek()
	switch t.Kind {
	case lexer.KwEvent:
		p.claimChainComments(decs, t)
		return p.parseEventDecl(decs)
	case lexer.KwConsume:
		p.claimChainComments(decs, t)
		return p.parseConsumerDecl(decs)
	}
	verb, ok := verbFromToken(t.Kind)
	if !ok {
		p.errorf(t.Pos, "expected an HTTP verb, `event`, or `consume`, got %s", t.Kind)
		return nil
	}
	p.claimChainComments(decs, t)
	return p.parseMethod(decs, verb)
}

// claimChainComments claims the comments sitting inside a member's
// decorator chain. The formatter re-emits them through its
// inter-decorator lookup, so the service body's harvest must not also
// pick them up.
func (p *Parser) claimChainComments(decs []*ast.Decorator, kw lexer.Token) {
	if len(decs) > 0 {
		p.claimCommentsBetween(decs[0].Pos.Line, kw.Pos.Line)
	}
}

// memberBody is the `{ ... }` tail every service member shares: the
// trailing `// note` on the closing brace, the free-floating comment
// blocks written inside, and the closing brace position (which the
// formatter uses to preserve blank-line grouping).
type memberBody struct {
	TrailingDoc []string
	Comments    []*ast.FreeComment
	EndPos      ast.Pos
}

// parseMemberBody reads a member body, delegating each clause to fn. fn
// receives the clause's first token and reports whether it recognised
// and consumed it; anything else is reported against expected and
// skipped.
func (p *Parser) parseMemberBody(fn func(lexer.Token) bool, expected string) memberBody {
	lbrace, _ := p.expect(lexer.LBrace)
	for p.peek().Kind != lexer.RBrace && p.peek().Kind != lexer.EOF {
		startPos := p.pos
		if !fn(p.peek()) {
			p.errorf(p.peek().Pos, "expected %s, got %s", expected, p.peek().Kind)
			p.advance()
			continue
		}
		if p.pos == startPos {
			p.advance()
		}
	}
	rbrace, _ := p.expect(lexer.RBrace)
	b := memberBody{EndPos: rbrace.Pos}
	if rbrace.Trailing != "" {
		b.TrailingDoc = []string{rbrace.Trailing}
	}
	b.Comments = p.harvestFreeComments(lbrace.Pos.Line, rbrace.Pos.Line)
	return b
}

// parseMethod reads `<verb> Name [ast.Path] { request? response? }`. The
// decorator chain and doc block were already read by
// [Parser.parseServiceMember].
func (p *Parser) parseMethod(decs []*ast.Decorator, verb string) *ast.Method {
	t := p.advance()
	name, _ := p.expect(lexer.Ident)
	m := &ast.Method{Pos: t.Pos, Decorators: decs, Doc: p.takeDoc(), Verb: verb, Name: name.Text}
	if p.peek().Kind == lexer.Slash {
		m.Path = p.parsePath()
	}
	body := p.parseMemberBody(func(tok lexer.Token) bool {
		switch tok.Kind {
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
			return false
		}
		return true
	}, "request or response in method body")
	m.TrailingDoc, m.BodyComments, m.EndPos = body.TrailingDoc, body.Comments, body.EndPos
	return m
}

// singleClauseMember is the shape `event` and `consume` share: a name,
// then a body holding exactly one `<keyword> <TypeRef>` clause.
type singleClauseMember struct {
	Pos         ast.Pos
	Name        string
	Doc         []string
	ClausePos   ast.Pos
	Ref         *ast.NamedTypeRef
	HasClause   bool
	TrailingDoc []string
	Comments    []*ast.FreeComment
	EndPos      ast.Pos
}

// parseSingleClauseMember reads `<member> Name { <clause> Ref }`. clause
// is the clause keyword, label its spelling, and kind the member word;
// the latter two only shape diagnostics.
func (p *Parser) parseSingleClauseMember(clause lexer.Kind, label, kind string) singleClauseMember {
	t := p.advance()
	name, _ := p.expect(lexer.Ident)
	m := singleClauseMember{Pos: t.Pos, Name: name.Text, Doc: p.takeDoc()}
	body := p.parseMemberBody(func(tok lexer.Token) bool {
		if tok.Kind != clause {
			return false
		}
		kw := p.advance()
		if m.HasClause {
			p.errorf(kw.Pos, "duplicate %s clause in %s %q", label, kind, m.Name)
		}
		m.ClausePos = p.peek().Pos
		m.Ref = p.parseNamedTypeRef()
		m.HasClause = true
		p.rejectMethodTypeSuffix(label)
		return true
	}, label+" in "+kind+" body")
	m.TrailingDoc, m.Comments, m.EndPos = body.TrailingDoc, body.Comments, body.EndPos
	return m
}

// parseEventDecl reads `event Name { payload Type }`.
func (p *Parser) parseEventDecl(decs []*ast.Decorator) *ast.EventDecl {
	m := p.parseSingleClauseMember(lexer.KwPayload, "payload", "event")
	e := &ast.EventDecl{
		Pos: m.Pos, Decorators: decs, Doc: m.Doc, Name: m.Name,
		TrailingDoc: m.TrailingDoc, BodyComments: m.Comments, EndPos: m.EndPos,
	}
	if m.HasClause {
		e.Payload = &ast.EventPayload{Pos: m.ClausePos, Type: m.Ref}
	}
	return e
}

// parseConsumerDecl reads `consume Name { event Ref }`.
func (p *Parser) parseConsumerDecl(decs []*ast.Decorator) *ast.ConsumerDecl {
	m := p.parseSingleClauseMember(lexer.KwEvent, "event", "consumer")
	c := &ast.ConsumerDecl{
		Pos: m.Pos, Decorators: decs, Doc: m.Doc, Name: m.Name,
		TrailingDoc: m.TrailingDoc, BodyComments: m.Comments, EndPos: m.EndPos,
	}
	if m.HasClause {
		c.Event = &ast.ConsumerEvent{Pos: m.ClausePos, Ref: m.Ref}
	}
	return c
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
		// `/` followed by something that is not a segment. Another `/` is
		// an empty segment; after a segment it is a trailing slash, which
		// the route cannot carry - a mux pattern ending in `/` matches a
		// whole subtree - so both are reported rather than dropped. On its
		// own, `/` is the root path.
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

// isPathWordToken reports whether k is a kind whose textual spelling
// is a legal path-segment word. Plain identifiers always qualify; so
// do every keyword and HTTP-verb keyword - when these spellings appear
// inside a URL path they're literal segments, not language tokens.
// This is what lets paths like `/echo-stream` or `/users/get` parse.
func isPathWordToken(k lexer.Kind) bool {
	// An identifier or any reserved keyword/verb spelling is a literal path
	// segment; the keyword range lives in isKeywordKind.
	return k == lexer.Ident || isKeywordKind(k)
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
