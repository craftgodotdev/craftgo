package format

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// source is the token stream a file was parsed from. It answers what the AST
// leaves out: the tokens of a declaration's header, the line of an extend
// block's keyword, and where a bracket closes. A nil source answers from the
// AST alone.
type source struct {
	toks []lexer.Token
	// index maps a token's byte offset to its index in toks.
	index map[int]int
	// closer maps the index of a `(`, `[`, `{` or `<` to the index of the
	// bracket that closes it.
	closer map[int]int
	// argLines holds the lines strictly between the `@` and the `)` of a
	// decorator with arguments.
	argLines map[int]bool
}

func newSource(toks []lexer.Token) *source {
	s := &source{toks: toks, index: make(map[int]int, len(toks)), closer: map[int]int{}, argLines: map[int]bool{}}
	var open []int
	for i, t := range toks {
		s.index[t.Pos.Offset] = i
		switch t.Kind {
		case lexer.LParen, lexer.LBracket, lexer.LBrace, lexer.LAngle:
			open = append(open, i)
		case lexer.RParen, lexer.RBracket, lexer.RBrace, lexer.RAngle:
			if n := len(open); n > 0 {
				s.closer[open[n-1]] = i
				open = open[:n-1]
			}
		}
	}
	for i, t := range toks {
		if t.Kind != lexer.At || i+2 >= len(toks) || toks[i+2].Kind != lexer.LParen {
			continue
		}
		if c, ok := s.closer[i+2]; ok {
			for l := t.Pos.Line + 1; l < toks[c].Pos.Line; l++ {
				s.argLines[l] = true
			}
		}
	}
	return s
}

// after returns the token n places after the one at pos, n negative for one
// before it, or a token at pos when there is none.
func (s *source) after(pos lexer.Position, n int) lexer.Token {
	if s == nil {
		return lexer.Token{Pos: pos}
	}
	i, ok := s.index[pos.Offset]
	if !ok || i+n < 0 || i+n >= len(s.toks) {
		return lexer.Token{Pos: pos}
	}
	return s.toks[i+n]
}

// closeLine returns the line of the bracket that closes the one at pos.
func (s *source) closeLine(pos lexer.Position) int {
	return s.closing(pos).Pos.Line
}

// closing returns the bracket that closes the one at pos, or a token at pos
// when there is none.
func (s *source) closing(pos lexer.Position) lexer.Token {
	if s == nil {
		return lexer.Token{Pos: pos}
	}
	if c, ok := s.closer[s.index[pos.Offset]]; ok {
		return s.toks[c]
	}
	return lexer.Token{Pos: pos}
}

// typeEnd returns the last token of the type reference t.
func (s *source) typeEnd(t *ast.TypeRef) lexer.Token {
	var end lexer.Token
	if t.Map != nil {
		end = s.closing(s.after(t.Map.Pos, 1).Pos)
	} else {
		end = s.namedEnd(t.Named)
	}
	n := 2 * t.ArrayDepth
	if t.Optional {
		n++
	}
	return s.after(end.Pos, n)
}

// namedEnd returns the last token of the named type reference t: the last
// part of its name, or the `>` after its arguments.
func (s *source) namedEnd(t *ast.NamedTypeRef) lexer.Token {
	name := s.after(t.Pos, 2*(len(t.Name.Parts)-1))
	if len(t.Args) == 0 {
		return name
	}
	return s.closing(s.after(name.Pos, 1).Pos)
}

// argsCloseLine returns the line of the `)` that closes d's arguments, or d's
// own line when it has none.
func (s *source) argsCloseLine(d *ast.Decorator) int {
	if !d.HasParens {
		return d.Pos.Line
	}
	return s.closeLine(s.after(d.Pos, 2).Pos)
}

// inArguments reports whether line lies strictly between the `@` and the `)`
// of a decorator, where a comment belongs to its arguments.
func (s *source) inArguments(line int) bool {
	return s != nil && s.argLines[line]
}

// keyword returns the position of d's first keyword: `extend` for an extend
// block, which the AST places at its `service`.
func (s *source) keyword(d ast.Decl) lexer.Position {
	if sd, ok := d.(*ast.ServiceDecl); ok && sd.Extend {
		return s.after(sd.Pos, -1).Pos
	}
	return d.DeclPos()
}

// firstLine returns the line of d's first decorator, or of its keyword.
func (s *source) firstLine(d ast.Decl) int {
	var decs []*ast.Decorator
	switch v := d.(type) {
	case *ast.TypeDecl:
		decs = v.Decorators
	case *ast.EnumDecl:
		decs = v.Decorators
	case *ast.ErrorDecl:
		decs = v.Decorators
	case *ast.ScalarDecl:
		decs = v.Decorators
	case *ast.MiddlewareDecl:
		decs = v.Decorators
	case *ast.ServiceDecl:
		decs = v.Decorators
	case *ast.EventDecl:
		decs = v.Decorators
	}
	return memberStartLine(s.keyword(d).Line, decs, 0)
}
