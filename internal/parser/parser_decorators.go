// Decorator and literal-expression parsing: @name(args), object/array/value
// literals, and string unquoting.
package parser

import (
	"strconv"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

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
		// like `[["a", "b"], ["c"]]` parses - parseValue has no `[` case
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
			// bound (e.g. a uint64 @lte above MaxInt64). Reject instead - the
			// IntLit's int64 storage can't represent values beyond the signed
			// 64-bit range yet.
			p.errorf(t.Pos, "integer literal %s is out of range - values beyond the signed 64-bit range (max %s) aren't supported yet", t.Text, "9223372036854775807")
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
				p.errorf(t.Pos, "integer literal -%s is out of range - values beyond the signed 64-bit range (min %s) aren't supported yet", next.Text, "-9223372036854775808")
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
