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
	"strconv"
	"strings"
	"unicode"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
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
}

// takeDoc returns the buffered doc-comment slice and clears it so the
// next AST node sees an empty buffer until the lexer fills one again.
func (p *Parser) takeDoc() []string {
	d := p.pendingDoc
	p.pendingDoc = nil
	return d
}

// captureDoc snapshots the doc attached to the current peek token onto
// the parser's pendingDoc buffer. Safe to call multiple times — it
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
	return &Parser{tokens: toks, diags: l.Diagnostics()}
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

// ----- decorators -----

// parseDecorators reads zero or more leading `@name(...)` decorators.
func (p *Parser) parseDecorators() []*ast.Decorator {
	var decs []*ast.Decorator
	for p.peek().Kind == lexer.At {
		decs = append(decs, p.parseDecorator())
	}
	return decs
}

// parseDecorator reads a single `@Name [(args...)]`. The name token may be
// any [lexer.Ident] or any keyword spelling — this lets users name decorators
// after reserved words (e.g. `@stream`, `@true`) without clashing with
// keyword usage elsewhere in the grammar.
func (p *Parser) parseDecorator() *ast.Decorator {
	at := p.advance()
	nameTok := p.peek()
	if nameTok.Kind != lexer.Ident && !isKeywordKind(nameTok.Kind) {
		p.errorf(nameTok.Pos, "expected decorator name, got %s", nameTok.Kind)
		return &ast.Decorator{Pos: at.Pos}
	}
	p.advance()
	d := &ast.Decorator{Pos: at.Pos, Name: nameTok.Text}
	if p.peek().Kind == lexer.LParen {
		p.advance()
		for p.peek().Kind != lexer.RParen && p.peek().Kind != lexer.EOF {
			d.Args = append(d.Args, p.parseDecoratorArg())
			if p.peek().Kind == lexer.Comma {
				p.advance()
			}
		}
		p.expect(lexer.RParen)
	}
	return d
}

// parseDecoratorArg dispatches between the four DecoratorArg shapes:
// nested decorator, object literal, named `name: value`, or bare value.
func (p *Parser) parseDecoratorArg() *ast.DecoratorArg {
	pos := p.peek().Pos
	arg := &ast.DecoratorArg{Pos: pos}
	if p.peek().Kind == lexer.At {
		arg.Nested = p.parseDecorator()
		return arg
	}
	if p.peek().Kind == lexer.LBrace {
		arg.Object = p.parseObjectLiteral()
		return arg
	}
	if p.peek().Kind == lexer.Ident && p.peekAt(1).Kind == lexer.Colon {
		name := p.advance().Text
		p.advance()
		arg.Name = name
		arg.Named = true
		arg.Value = p.parseValueOrArray()
		return arg
	}
	arg.Value = p.parseValueOrArray()
	return arg
}

// parseObjectLiteral reads a `{ k: v, ... }` decorator argument body.
func (p *Parser) parseObjectLiteral() []*ast.ObjectField {
	p.expect(lexer.LBrace)
	var fields []*ast.ObjectField
	for p.peek().Kind != lexer.RBrace && p.peek().Kind != lexer.EOF {
		fpos := p.peek().Pos
		name, _ := p.expect(lexer.Ident)
		p.expect(lexer.Colon)
		val := p.parseValueOrArray()
		fields = append(fields, &ast.ObjectField{Pos: fpos, Name: name.Text, Value: val})
		if p.peek().Kind == lexer.Comma {
			p.advance()
		}
	}
	p.expect(lexer.RBrace)
	return fields
}

// parseValueOrArray dispatches between scalar and array literal forms.
func (p *Parser) parseValueOrArray() ast.Expr {
	if p.peek().Kind == lexer.LBracket {
		return p.parseArray()
	}
	return p.parseValue()
}

// parseArray reads `[v1, v2, ...]` literals.
func (p *Parser) parseArray() ast.Expr {
	pos := p.advance().Pos
	arr := &ast.ArrayLit{Pos: pos}
	for p.peek().Kind != lexer.RBracket && p.peek().Kind != lexer.EOF {
		arr.Elements = append(arr.Elements, p.parseValue())
		if p.peek().Kind == lexer.Comma {
			p.advance()
		}
	}
	p.expect(lexer.RBracket)
	return arr
}

// parseValue reads a single literal expression: string, number (signed),
// boolean, null, duration, size, or qualified identifier. On unrecognised
// input it records a diagnostic, advances one token, and returns a [NullLit]
// so downstream code does not see a nil [Expr].
func (p *Parser) parseValue() ast.Expr {
	t := p.peek()
	switch t.Kind {
	case lexer.String:
		p.advance()
		return &ast.StringLit{Pos: t.Pos, Value: unquoteString(t.Text)}
	case lexer.RawString:
		p.advance()
		return &ast.StringLit{Pos: t.Pos, Value: unquoteRaw(t.Text)}
	case lexer.Int:
		p.advance()
		n, _ := strconv.ParseInt(t.Text, 10, 64)
		return &ast.IntLit{Pos: t.Pos, Value: n}
	case lexer.Float:
		p.advance()
		f, _ := strconv.ParseFloat(t.Text, 64)
		return &ast.FloatLit{Pos: t.Pos, Value: f}
	case lexer.KwTrue:
		p.advance()
		return &ast.BoolLit{Pos: t.Pos, Value: true}
	case lexer.KwFalse:
		p.advance()
		return &ast.BoolLit{Pos: t.Pos, Value: false}
	case lexer.KwNull:
		p.advance()
		return &ast.NullLit{Pos: t.Pos}
	case lexer.Duration:
		p.advance()
		return &ast.DurationLit{Pos: t.Pos, Text: t.Text}
	case lexer.Size:
		p.advance()
		return &ast.SizeLit{Pos: t.Pos, Text: t.Text}
	case lexer.Dash:
		// Unary minus: only legal directly before a numeric literal.
		p.advance()
		next := p.peek()
		if next.Kind == lexer.Int {
			p.advance()
			n, _ := strconv.ParseInt("-"+next.Text, 10, 64)
			return &ast.IntLit{Pos: t.Pos, Value: n}
		}
		if next.Kind == lexer.Float {
			p.advance()
			f, _ := strconv.ParseFloat("-"+next.Text, 64)
			return &ast.FloatLit{Pos: t.Pos, Value: f}
		}
		p.errorf(t.Pos, "expected number after '-'")
		return &ast.IntLit{Pos: t.Pos, Value: 0}
	case lexer.Ident:
		qi := p.parseQualifiedIdent()
		return &ast.IdentExpr{Pos: qi.Pos, Name: qi}
	}
	p.errorf(t.Pos, "expected literal, got %s", t.Kind)
	p.advance()
	return &ast.NullLit{Pos: t.Pos}
}

// ----- top-level -----

// parsePackage reads the `package <name>` line.
func (p *Parser) parsePackage() *ast.PackageDecl {
	p.advance()
	name, _ := p.expect(lexer.Ident)
	return &ast.PackageDecl{Pos: name.Pos, Name: name.Text}
}

// parseImport reads `import [alias] "path"`. Both alias and path are
// optional from a token-stream perspective; missing path is reported as a
// diagnostic but not fatal.
func (p *Parser) parseImport() *ast.Import {
	pos := p.advance().Pos
	imp := &ast.Import{Pos: pos}
	if p.peek().Kind == lexer.Ident {
		imp.Alias = p.advance().Text
	}
	str, ok := p.expect(lexer.String)
	if ok {
		imp.Path = unquoteString(str.Text)
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
		return p.parseExtendService(decs)
	case lexer.EOF:
		return nil
	}
	p.errorf(t.Pos, "expected declaration, got %s", t.Kind)
	return nil
}

// parseTypeDecl reads `type Name[<TypeParams>] { Body }`.
func (p *Parser) parseTypeDecl(decs []*ast.Decorator) *ast.TypeDecl {
	pos := p.advance().Pos
	name, _ := p.expect(lexer.Ident)
	td := &ast.TypeDecl{Pos: pos, Decorators: decs, Doc: p.takeDoc(), Name: name.Text}
	if p.peek().Kind == lexer.LAngle {
		td.TypeParams = p.parseTypeParams()
	}
	td.Body = p.parseTypeBody()
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
	for p.peek().Kind != lexer.RAngle && p.peek().Kind != lexer.EOF {
		t, ok := p.expect(lexer.Ident)
		if !ok {
			break
		}
		params = append(params, t.Text)
		if p.peek().Kind == lexer.Comma {
			p.advance()
		}
	}
	p.expect(lexer.RAngle)
	return params
}

// parseTypeBody reads the contents of a `{ ... }` type/error body. Returns
// nil when there is no opening brace — callers (e.g. [parseTypeDecl]) decide
// whether that is an error or a deliberate empty body.
func (p *Parser) parseTypeBody() []ast.TypeMember {
	if !p.peekIs(lexer.LBrace) {
		return nil
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
	p.expect(lexer.RBrace)
	return members
}

// parseTypeMember reads one member of a type body — either a [Field] or a
// [Mixin]. Disambiguation rules (in priority order):
//
//  1. Next token is `.` or `<` → mixin (qualified or generic name).
//  2. First identifier starts uppercase (PascalCase) → mixin.
//  3. Otherwise → field (lowercase first letter ⇒ field name).
//
// This soft-enforces the "type names are PascalCase, field names are
// lowercase-first" convention without losing flexibility for cross-package
// mixin references like `shared.Profile`.
func (p *Parser) parseTypeMember() ast.TypeMember {
	p.captureDoc()
	decs := p.parseDecorators()
	t := p.peek()
	if t.Kind != lexer.Ident {
		p.errorf(t.Pos, "expected field or mixin, got %s", t.Kind)
		return nil
	}
	next := p.peekAt(1)
	mixin := next.Kind == lexer.Dot || next.Kind == lexer.LAngle || isUpperFirst(t.Text)
	if mixin {
		ref := p.parseNamedTypeRef()
		_ = decs
		_ = p.takeDoc() // mixins don't carry a doc-comment field yet
		return &ast.Mixin{Pos: t.Pos, Ref: ref}
	}
	name := p.advance()
	tref := p.parseTypeRef()
	fieldDecs := p.parseDecorators()
	return &ast.Field{Pos: name.Pos, Doc: p.takeDoc(), Name: name.Text, Type: tref, Decorators: append(decs, fieldDecs...)}
}

// peekIs reports whether the current token has kind k.
func (p *Parser) peekIs(k lexer.Kind) bool { return p.peek().Kind == k }

// parseTypeRef parses TypeRef = (MapType | NamedTypeRef) ArrayMod? OptionalMod?
//
// Array (`[]`) and Optional (`?`) are independent suffix flags — `T[]?`
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

// ----- enum -----

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
			ed.Values = append(ed.Values, v)
		}
		if p.pos == startPos {
			p.advance()
		}
	}
	p.expect(lexer.RBrace)
	return ed
}

// parseEnumValue reads a single `Name [= literal] [@decorators]` entry.
func (p *Parser) parseEnumValue() *ast.EnumValue {
	t, ok := p.expect(lexer.Ident)
	if !ok {
		return nil
	}
	v := &ast.EnumValue{Pos: t.Pos, Name: t.Text, Kind: ast.EnumBare}
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

// ----- error -----

// errorCategories enumerates the 19 reserved HTTP-status categories the
// `error <Category> Name` form may use. Anything outside this set produces a
// diagnostic.
var errorCategories = map[string]bool{
	"BadRequest": true, "Unauthorized": true, "PaymentRequired": true,
	"Forbidden": true, "NotFound": true, "MethodNotAllowed": true,
	"NotAcceptable": true, "Conflict": true, "Gone": true,
	"PreconditionFailed": true, "PayloadTooLarge": true, "UnsupportedMediaType": true,
	"UnprocessableEntity": true, "TooManyRequests": true, "Internal": true,
	"NotImplemented": true, "BadGateway": true, "ServiceUnavailable": true,
	"GatewayTimeout": true,
}

// parseErrorDecl reads `error <Category> Name [{ Body }]`.
func (p *Parser) parseErrorDecl(decs []*ast.Decorator) *ast.ErrorDecl {
	pos := p.advance().Pos
	cat, _ := p.expect(lexer.Ident)
	if cat.Text != "" && !errorCategories[cat.Text] {
		p.errorf(cat.Pos, "unknown error category %q", cat.Text)
	}
	name, _ := p.expect(lexer.Ident)
	ed := &ast.ErrorDecl{Pos: pos, Decorators: decs, Doc: p.takeDoc(), Category: cat.Text, Name: name.Text}
	if p.peek().Kind == lexer.LBrace {
		ed.HasBody = true
		ed.Body = p.parseTypeBody()
	}
	return ed
}

// ----- scalar -----

// parseScalarDecl reads `scalar Name <PrimitiveType> [@decorators]`.
func (p *Parser) parseScalarDecl(decs []*ast.Decorator) *ast.ScalarDecl {
	pos := p.advance().Pos
	name, _ := p.expect(lexer.Ident)
	prim, _ := p.expect(lexer.Ident)
	sd := &ast.ScalarDecl{Pos: pos, Decorators: decs, Name: name.Text, Primitive: prim.Text}
	sd.Decorators = append(sd.Decorators, p.parseDecorators()...)
	return sd
}

// ----- middleware -----

// parseMiddlewareDecl reads `middleware Name [(p1: T1 [= default], ...)]`.
func (p *Parser) parseMiddlewareDecl(decs []*ast.Decorator) *ast.MiddlewareDecl {
	pos := p.advance().Pos
	name, _ := p.expect(lexer.Ident)
	md := &ast.MiddlewareDecl{Pos: pos, Decorators: decs, Name: name.Text}
	if p.peek().Kind == lexer.LParen {
		p.advance()
		for p.peek().Kind != lexer.RParen && p.peek().Kind != lexer.EOF {
			md.Params = append(md.Params, p.parseMiddlewareParam())
			if p.peek().Kind == lexer.Comma {
				p.advance()
			}
		}
		p.expect(lexer.RParen)
	}
	return md
}

// parseMiddlewareParam reads `name: Type [= literal]`.
func (p *Parser) parseMiddlewareParam() *ast.MiddlewareParam {
	name, _ := p.expect(lexer.Ident)
	p.expect(lexer.Colon)
	tref := p.parseTypeRef()
	mp := &ast.MiddlewareParam{Pos: name.Pos, Name: name.Text, Type: tref}
	if p.peek().Kind == lexer.Equal {
		p.advance()
		mp.Default = p.parseValue()
	}
	return mp
}

// ----- service -----

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
			sd.Methods = append(sd.Methods, m)
		}
		if p.pos == startPos {
			p.advance()
		}
	}
	p.expect(lexer.RBrace)
	return sd
}

// parseExtendService reads `extend service Name { ... }`. Anything other
// than `service` immediately after `extend` is an error — `extend type` and
// friends are NOT supported.
func (p *Parser) parseExtendService(decs []*ast.Decorator) *ast.ServiceDecl {
	p.advance()
	if p.peek().Kind != lexer.KwService {
		p.errorf(p.peek().Pos, "expected 'service' after 'extend'")
		return nil
	}
	return p.parseServiceDecl(decs, true)
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
			p.advance()
			m.Request = p.parseNamedTypeRef()
		case lexer.KwResponse:
			p.advance()
			mr := &ast.MethodResponse{Pos: p.peek().Pos}
			if p.peek().Kind == lexer.KwStream {
				p.advance()
				mr.Stream = true
			}
			mr.Type = p.parseNamedTypeRef()
			m.Response = mr
		default:
			p.errorf(p.peek().Pos, "expected request or response in method body, got %s", p.peek().Kind)
			p.advance()
		}
	}
	p.expect(lexer.RBrace)
	return m
}

// parsePath reads `/seg1/seg2/...`. A segment is either a literal (including
// hyphenated forms like `api-v1`) or a `{param}`. To avoid swallowing the
// method's opening brace, the `{` form is only recognised when followed
// immediately by an identifier and a `}`.
func (p *Parser) parsePath() *ast.Path {
	pos := p.peek().Pos
	path := &ast.Path{Pos: pos}
	for p.peek().Kind == lexer.Slash {
		p.advance()
		segPos := p.peek().Pos
		// Path param: `{ident}` — disambiguate from method body `{` by
		// requiring an Ident immediately after the brace.
		if p.peek().Kind == lexer.LBrace && p.peekAt(1).Kind == lexer.Ident {
			p.advance()
			id, _ := p.expect(lexer.Ident)
			p.expect(lexer.RBrace)
			path.Segments = append(path.Segments, &ast.PathSegment{Pos: segPos, Param: true, Literal: id.Text})
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
		// Trailing slash: anything else means we have `/` followed by a
		// non-segment token (typically the method body's opening brace).
		path.Segments = append(path.Segments, &ast.PathSegment{Pos: segPos, Literal: ""})
		break
	}
	return path
}

// isPathWordToken reports whether k is a kind whose textual spelling
// is a legal path-segment word. Plain identifiers always qualify; so
// do every keyword and HTTP-verb keyword — when these spellings appear
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

// ----- string unquoting -----

// unquoteString unescapes a `"..."` literal, supporting `\n \t \r \" \\`
// and `\u{HEX}` Unicode escapes. Unknown escapes pass through verbatim
// (without the leading `\`) so partially-malformed input still produces
// useful values for IDE autocomplete.
func unquoteString(s string) string {
	if len(s) < 2 {
		return s
	}
	inner := s[1 : len(s)-1]
	var sb strings.Builder
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c != '\\' || i+1 >= len(inner) {
			sb.WriteByte(c)
			continue
		}
		i++
		switch inner[i] {
		case 'n':
			sb.WriteByte('\n')
		case 't':
			sb.WriteByte('\t')
		case 'r':
			sb.WriteByte('\r')
		case '"':
			sb.WriteByte('"')
		case '\\':
			sb.WriteByte('\\')
		case 'u':
			if i+1 < len(inner) && inner[i+1] == '{' {
				end := strings.Index(inner[i+2:], "}")
				if end >= 0 {
					hex := inner[i+2 : i+2+end]
					if n, err := strconv.ParseInt(hex, 16, 32); err == nil {
						sb.WriteRune(rune(n))
						i = i + 2 + end
						continue
					}
				}
			}
			sb.WriteByte(inner[i])
		default:
			sb.WriteByte(inner[i])
		}
	}
	return sb.String()
}

// unquoteRaw strips the surrounding backticks from a raw string literal.
// No further processing is performed — that is the whole point of raw
// strings in the DSL.
func unquoteRaw(s string) string {
	if len(s) < 2 {
		return s
	}
	return s[1 : len(s)-1]
}
