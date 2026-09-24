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

// parseDecorator parses `@name` or `@name(args)`; the name may be a reserved
// word.
func (p *Parser) parseDecorator() *ast.Decorator {
	at := p.advance()
	nameTok := p.peek()
	if nameTok.Kind != lexer.Ident && !nameTok.Kind.IsKeyword() {
		p.errorf(nameTok.Pos, "expected decorator name, got %s", nameTok.Kind)
		return &ast.Decorator{Pos: at.Pos}
	}
	p.advance()
	d := &ast.Decorator{Pos: at.Pos, Name: nameTok.Text}
	// The comment after the decorator's last token is its TrailingDoc.
	lastTok := nameTok
	if p.peek().Kind == lexer.LParen {
		p.advance()
		d.HasParens = true
		for p.peek().Kind != lexer.RParen && p.peek().Kind != lexer.EOF {
			d.Args = append(d.Args, p.parseDecoratorArg())
			switch p.peek().Kind {
			case lexer.Comma:
				p.advance()
			case lexer.RParen, lexer.EOF:
			default:
				p.errorf(p.peek().Pos, "expected ',' or ')' after decorator argument, got %s", p.peek().Kind)
			}
		}
		rparen, _ := p.expect(lexer.RParen)
		lastTok = rparen
	}
	d.TrailingDoc = lastTok.Trailing
	return d
}

// parseDecoratorArg parses a nested decorator, an object literal, `name: value`
// or a bare value.
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

// parseObjectLiteral parses `{ key: value, ... }`.
func (p *Parser) parseObjectLiteral() []*ast.ObjectField {
	p.expect(lexer.LBrace)
	var fields []*ast.ObjectField
	for p.peek().Kind != lexer.RBrace && p.peek().Kind != lexer.EOF {
		fpos := p.peek().Pos
		name := p.expectFieldKey()
		p.expect(lexer.Colon)
		val := p.parseValueOrArray()
		fields = append(fields, &ast.ObjectField{Pos: fpos, Name: name, Value: val})
		switch p.peek().Kind {
		case lexer.Comma:
			p.advance()
		case lexer.RBrace, lexer.EOF:
		default:
			p.errorf(p.peek().Pos, "expected ',' or '}' after object field, got %s", p.peek().Kind)
		}
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

// parseValueOrArray parses an array literal or a single value.
func (p *Parser) parseValueOrArray() ast.Expr {
	if p.peek().Kind == lexer.LBracket {
		return p.parseArray()
	}
	return p.parseValue()
}

// parseArray parses `[a, b, ...]`, whose elements may be arrays.
func (p *Parser) parseArray() ast.Expr {
	pos := p.advance().Pos
	arr := &ast.ArrayLit{Pos: pos}
	for p.peek().Kind != lexer.RBracket && p.peek().Kind != lexer.EOF {
		arr.Elements = append(arr.Elements, p.parseValueOrArray())
		switch p.peek().Kind {
		case lexer.Comma:
			p.advance()
		case lexer.RBracket, lexer.EOF:
		default:
			p.errorf(p.peek().Pos, "expected ',' or ']' after array element, got %s", p.peek().Kind)
		}
	}
	p.expect(lexer.RBracket)
	return arr
}

// parseValue parses one literal or qualified identifier. Other input is
// reported and skipped, and yields a NullLit rather than nil.
func (p *Parser) parseValue() ast.Expr {
	t := p.peek()
	switch t.Kind {
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
		return &ast.IntLit{Pos: t.Pos, Value: 0}
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
	return &ast.NullLit{Pos: t.Pos}
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
		p.errorf(pos, "integer literal %s is out of range - values beyond the signed 64-bit range (%s) aren't supported yet", text, bound)
	}
	return n
}

// unquote returns the value of a String or RawString token; the lexer emits
// one only when [lexer.Unquote] accepts its text.
func unquote(t lexer.Token) string {
	v, _ := lexer.Unquote(t.Text)
	return v
}
