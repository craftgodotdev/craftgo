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

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/errcat"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
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
	// allComments is the snapshot of every `//` comment seen by the
	// lexer, kept so the parser can populate `*ast.File.Comments` and
	// (in body parsing) scan for free-floating section headers.
	allComments []*lexer.Comment
}

// takeDoc returns the buffered doc-comment slice and clears it so the
// next AST node sees an empty buffer until the lexer fills one again.
func (p *Parser) takeDoc() []string {
	d := p.pendingDoc
	p.pendingDoc = nil
	return d
}

// captureDoc snapshots the doc attached to the current peek token onto
// the parser's pendingDoc buffer. Safe to call multiple times - it
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
	return &Parser{
		tokens:      toks,
		diags:       l.Diagnostics(),
		allComments: l.Comments(),
	}
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
	// Capture the file-header `//` block when it would otherwise be
	// dropped: a decorator-led file (`@version(...) ... package x`)
	// lets the lexer attach the comment to the first `@` token, but
	// [ast.Decorator] has no Doc field, so without this snapshot the
	// comment vanishes through the parser/format round trip.
	if p.peek().Kind == lexer.At {
		f.LeadingDoc = p.peek().Doc
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
		// Recovery: if no token was consumed, advance one to avoid an
		// infinite loop on unexpected input.
		if p.pos == startPos {
			p.advance()
		}
	}
	f.Comments = p.allComments
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

// rejectMixinDecorators fires a parser diagnostic for every decorator
// the user attached to a mixin reference. The AST [ast.Mixin] has no
// decorator slot - mixins are pure embedding - so silently dropping
// them would surface as "my @deprecated note disappeared" later. Fire
// at design time at the mixin reference position so the editor can
// underline the right span.
func (p *Parser) rejectMixinDecorators(mixinPos lexer.Position, decs []*ast.Decorator) {
	for _, d := range decs {
		if d == nil {
			continue
		}
		p.errorf(d.Pos, "decorators are not supported on mixin references (@%s near %s); attach the decorator to the target type or to a field that uses it", d.Name, mixinPos)
	}
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
// any [lexer.Ident] or any keyword spelling - this lets users name decorators
// after reserved words (e.g. `@true`) without clashing with keyword usage
// elsewhere in the grammar.
func (p *Parser) parseDecorator() *ast.Decorator {
	at := p.advance()
	nameTok := p.peek()
	if nameTok.Kind != lexer.Ident && !isKeywordKind(nameTok.Kind) {
		p.errorf(nameTok.Pos, "expected decorator name, got %s", nameTok.Kind)
		return &ast.Decorator{Pos: at.Pos}
	}
	p.advance()
	d := &ast.Decorator{Pos: at.Pos, Name: nameTok.Text}
	// lastTok tracks the decorator's final token so we can capture its
	// Trailing into d.TrailingDoc. For a bare `@deprecated` the last
	// token is the name Ident; for `@length(1, 80)` it is the closing
	// `)`.
	lastTok := nameTok
	if p.peek().Kind == lexer.LParen {
		p.advance()
		d.HasParens = true
		for p.peek().Kind != lexer.RParen && p.peek().Kind != lexer.EOF {
			d.Args = append(d.Args, p.parseDecoratorArg())
			if p.peek().Kind == lexer.Comma {
				p.advance()
			}
		}
		rparen, _ := p.expect(lexer.RParen)
		lastTok = rparen
	}
	d.TrailingDoc = lastTok.Trailing
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
		// parseValueOrArray (not parseValue) so a NESTED array literal
		// like `[["a", "b"], ["c"]]` parses — parseValue has no `[` case
		// and would record a diagnostic on the inner bracket.
		arr.Elements = append(arr.Elements, p.parseValueOrArray())
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
		n, err := strconv.ParseInt(t.Text, 10, 64)
		if err != nil {
			// strconv clamps an out-of-range literal to MaxInt64 with an
			// ErrRange; emitting the clamped value would silently corrupt a
			// bound (e.g. a uint64 @lte above MaxInt64). Reject instead — the
			// IntLit's int64 storage can't represent values beyond the signed
			// 64-bit range yet.
			p.errorf(t.Pos, "integer literal %s is out of range — values beyond the signed 64-bit range (max %s) aren't supported yet", t.Text, "9223372036854775807")
		}
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
			n, err := strconv.ParseInt("-"+next.Text, 10, 64)
			if err != nil {
				p.errorf(t.Pos, "integer literal -%s is out of range — values beyond the signed 64-bit range (min %s) aren't supported yet", next.Text, "-9223372036854775808")
			}
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
// entries — doing so reliably requires per-comment-line position
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
	// and a keyword never spells one — so the keyword can only be the
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

// peekIs reports whether the current token has kind k.
func (p *Parser) peekIs(k lexer.Kind) bool { return p.peek().Kind == k }

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

// ----- error -----

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

// ----- scalar -----

// parseScalarDecl reads `scalar Name <PrimitiveType> [@decorators]`.
func (p *Parser) parseScalarDecl(decs []*ast.Decorator) *ast.ScalarDecl {
	pos := p.advance().Pos
	name, _ := p.expect(lexer.Ident)
	prim, _ := p.expect(lexer.Ident)
	sd := &ast.ScalarDecl{Pos: pos, Decorators: decs, Doc: p.takeDoc(), Name: name.Text, Primitive: prim.Text}
	// Trailing decorators on the same line as the primitive belong to the
	// scalar (`scalar Email string @pattern(...)`). A decorator on a later
	// line is the leading decorator of the next declaration, so stop —
	// consuming it here would steal it from the following type/enum/service.
	for p.peek().Kind == lexer.At && p.peek().Pos.Line == prim.Pos.Line {
		sd.Decorators = append(sd.Decorators, p.parseDecorator())
	}
	return sd
}

// ----- middleware -----

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
// (`get`, `post`, ...) are accepted as parameter names — they're URL-level
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
	// Hot path: byte-by-byte escape decoding. Builder is the right
	// tool - concatenation would allocate on every byte.
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
// No further processing is performed - that is the whole point of raw
// strings in the DSL.
func unquoteRaw(s string) string {
	if len(s) < 2 {
		return s
	}
	return s[1 : len(s)-1]
}
