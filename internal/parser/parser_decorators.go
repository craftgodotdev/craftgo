package parser

import (
	"strconv"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// rejectMixinDecorators reports every decorator in decs: a mixin takes none.
func (p *Parser) rejectMixinDecorators(mixinPos lexer.Position, decs []*ast.Decorator) {
	for _, d := range decs {
		if d == nil {
			continue
		}
		p.errorf(d.Pos, "decorators are not supported on mixin references (@%s near %s); attach the decorator to the target type or to a field that uses it", d.Name, mixinPos)
	}
}

// parseDecorators parses zero or more decorators.
func (p *Parser) parseDecorators() []*ast.Decorator {
	var decs []*ast.Decorator
	for p.peek().Kind == lexer.At {
		decs = append(decs, p.parseDecorator())
	}
	return decs
}

// decoratorsOnLine parses the decorators that start on line, each one after
// the first starting on the line where the one before it ends.
func (p *Parser) decoratorsOnLine(line int) []*ast.Decorator {
	var decs []*ast.Decorator
	for p.peek().Kind == lexer.At && p.peek().Pos.Line == line {
		decs = append(decs, p.parseDecorator())
		line = p.tokens[p.pos-1].Pos.Line
	}
	return decs
}

// rejectDecoratorsAfter reports and consumes the decorators on the last line
// of the construct what, unless starts holds for the next token on their line.
func (p *Parser) rejectDecoratorsAfter(what string, starts func(lexer.Kind) bool) {
	pos := p.pos
	decs := p.decoratorsOnLine(p.tokens[p.pos-1].Pos.Line)
	if next := p.peek(); len(decs) > 0 && next.Pos.Line == p.tokens[p.pos-1].Pos.Line && starts(next.Kind) {
		p.pos = pos
		return
	}
	for _, d := range decs {
		p.errorf(d.Pos, "decorator @%s follows a %s on its line; a decorator goes before what it decorates", d.Name, what)
	}
}

// parseDecorator parses `@name` or `@name(args)`; the name may be a reserved
// word. An argument list with a parse error holds an [ast.BadExpr]: in place
// of the argument it could not read, else at its end.
func (p *Parser) parseDecorator() *ast.Decorator {
	at := p.advance()
	nameTok := p.peek()
	if nameTok.Kind != lexer.Ident && !nameTok.Kind.IsKeyword() {
		p.errorf(nameTok.Pos, "expected decorator name, got %s", nameTok.Kind)
		return &ast.Decorator{Pos: at.Pos}
	}
	p.advance()
	d := &ast.Decorator{Pos: at.Pos, Name: nameTok.Text}
	if p.peek().Kind == lexer.LParen {
		p.advance()
		d.HasParens = true
		errs := len(p.diags)
		// A `}` ends the arguments too: it closes the body they were left open in.
		for !p.peekIs(lexer.RParen) && !p.peekIs(lexer.RBrace) && !p.peekIs(lexer.EOF) {
			d.Args = append(d.Args, p.parseDecoratorArg(d.Name))
			p.listSep(lexer.RParen, "decorator argument")
		}
		rparen, closed := p.expect(lexer.RParen)
		if (!closed || len(p.diags) > errs) && !d.HoldsBadExpr() {
			d.Args = append(d.Args, &ast.DecoratorArg{Pos: rparen.Pos, Value: &ast.BadExpr{Pos: rparen.Pos}})
		}
		p.claimInside(at.Pos.Line, rparen.Pos.Line)
	}
	return d
}

// parseDecoratorArg parses an argument of the decorator named dec: an object
// literal, `name: value` or a bare value.
func (p *Parser) parseDecoratorArg(dec string) *ast.DecoratorArg {
	pos := p.peek().Pos
	arg := &ast.DecoratorArg{Pos: pos}
	if p.peek().Kind == lexer.LBrace {
		arg.Object = p.parseObjectLiteral(dec)
		return arg
	}
	if p.peek().Kind == lexer.Ident && p.peekAt(1).Kind == lexer.Colon {
		name := p.advance().Text
		p.advance()
		arg.Name = name
		arg.Named = true
		arg.Value = p.parseValueOrArray(dec, lexer.RParen)
		return arg
	}
	arg.Value = p.parseValueOrArray(dec, lexer.RParen)
	return arg
}

// parseObjectLiteral parses `{ key: value, ... }` in the arguments of the
// decorator named dec.
func (p *Parser) parseObjectLiteral(dec string) []*ast.ObjectField {
	p.expect(lexer.LBrace)
	var fields []*ast.ObjectField
	for p.peek().Kind != lexer.RBrace && p.peek().Kind != lexer.EOF {
		fpos := p.peek().Pos
		name := p.expectFieldKey()
		p.expect(lexer.Colon)
		val := p.parseValueOrArray(dec, lexer.RBrace)
		fields = append(fields, &ast.ObjectField{Pos: fpos, Name: name, Value: val})
		p.listSep(lexer.RBrace, "object field")
	}
	p.expect(lexer.RBrace)
	return fields
}

// expectFieldKey parses an object-literal key, which like a field name may be a
// reserved word.
func (p *Parser) expectFieldKey() string {
	t := p.peek()
	if t.Kind == lexer.Ident || t.Kind.IsKeyword() {
		p.advance()
		return t.Text
	}
	p.errorf(t.Pos, "expected object field name, got %s", t.Kind)
	return ""
}

// parseValueOrArray parses an array literal or a single value in the arguments
// of the decorator named dec, in a list that closer closes.
func (p *Parser) parseValueOrArray(dec string, closer lexer.Kind) ast.Expr {
	if p.peek().Kind == lexer.LBracket {
		return p.parseArray(dec)
	}
	return p.parseValue(dec, closer)
}

// parseArray parses `[a, b, ...]`, whose elements may be arrays, in the
// arguments of the decorator named dec.
func (p *Parser) parseArray(dec string) ast.Expr {
	pos := p.advance().Pos
	arr := &ast.ArrayLit{Pos: pos}
	for p.peek().Kind != lexer.RBracket && p.peek().Kind != lexer.EOF {
		arr.Elements = append(arr.Elements, p.parseValueOrArray(dec, lexer.RBracket))
		p.listSep(lexer.RBracket, "array element")
	}
	p.expect(lexer.RBracket)
	return arr
}

// parseValue parses a literal or a qualified name in a list of @dec's arguments
// that closer closes; other input is reported, skipped and read as an
// [ast.BadExpr].
func (p *Parser) parseValue(dec string, closer lexer.Kind) ast.Expr {
	t := p.peek()
	switch t.Kind {
	case lexer.At:
		p.errorf(t.Pos, "a decorator cannot be an argument of @%s", dec)
		p.skipDecorator(closer == lexer.RParen)
		return &ast.BadExpr{Pos: t.Pos}
	case lexer.String, lexer.RawString:
		p.advance()
		return &ast.StringLit{Pos: t.Pos, Value: unquote(t), Text: t.Text}
	case lexer.Int:
		p.advance()
		return &ast.IntLit{Pos: t.Pos, Value: p.signedInt(t.Pos, t, false)}
	case lexer.Float:
		p.advance()
		f, _ := strconv.ParseFloat(t.Text, 64)
		return &ast.FloatLit{Pos: t.Pos, Value: f, Text: t.Text}
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
			return &ast.IntLit{Pos: t.Pos, Value: p.signedInt(t.Pos, next, true)}
		}
		if next.Kind == lexer.Float {
			p.advance()
			f, _ := strconv.ParseFloat("-"+next.Text, 64)
			return &ast.FloatLit{Pos: t.Pos, Value: f, Text: "-" + next.Text}
		}
		p.errorf(t.Pos, "expected number after '-'")
		return &ast.BadExpr{Pos: t.Pos}
	case lexer.Ident:
		qi := p.parseQualifiedIdent()
		return &ast.IdentExpr{Pos: qi.Pos, Name: qi}
	}
	// Any other reserved word is a name, such as the field in
	// `@requiresOneOf(payload, name)`.
	if t.Kind.IsKeyword() {
		p.advance()
		return &ast.IdentExpr{Pos: t.Pos, Name: &ast.QualifiedIdent{Pos: t.Pos, Parts: []string{t.Text}}}
	}
	p.errorf(t.Pos, "expected literal, got %s", t.Kind)
	p.advance()
	return &ast.BadExpr{Pos: t.Pos}
}

// skipDecorator consumes the decorator at the current `@` and its arguments;
// with mayClose, a `)` that ends its line, no `,` or `)` next, closes the list.
func (p *Parser) skipDecorator(mayClose bool) {
	p.advance()
	if t := p.peek(); t.Kind != lexer.Ident && !t.Kind.IsKeyword() {
		return
	}
	p.advance()
	if !p.peekIs(lexer.LParen) || !p.skipParens() || !mayClose {
		return
	}
	if next := p.peek(); next.Pos.Line > p.tokens[p.pos-1].Pos.Line && next.Kind != lexer.Comma && next.Kind != lexer.RParen {
		p.pos--
	}
}

// signedInt returns the value of the Int token tok, negated when neg, and
// reports at pos a value outside the int64 range.
func (p *Parser) signedInt(pos lexer.Position, tok lexer.Token, neg bool) int64 {
	text, bound := tok.Text, "max 9223372036854775807"
	if neg {
		text, bound = "-"+tok.Text, "min -9223372036854775808"
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		p.errorf(pos, "integer literal %s is outside the signed 64-bit range (%s)", text, bound)
	}
	return n
}

// unquote returns the value of a String or RawString token; the lexer emits
// one only when [lexer.Unquote] accepts its text.
func unquote(t lexer.Token) string {
	v, _ := lexer.Unquote(t.Text)
	return v
}
