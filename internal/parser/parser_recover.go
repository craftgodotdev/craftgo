package parser

import (
	"fmt"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// closerOf maps each opening bracket to the bracket that closes it.
var closerOf = map[lexer.Kind]lexer.Kind{
	lexer.LParen:   lexer.RParen,
	lexer.LBracket: lexer.RBracket,
	lexer.LBrace:   lexer.RBrace,
	lexer.LAngle:   lexer.RAngle,
}

// unclosed reports the bracket open as one nothing closes.
func (p *Parser) unclosed(open lexer.Token) {
	p.errorf(open.Pos, "unclosed `%s` - add the `%s` that closes it", open.Kind, closerOf[open.Kind])
}

// closedBrackets returns, by token index, whether each opening bracket of
// toks is closed. A `(`, `[` or `{` is closed by its closer, unless a closer
// of a bracket around it comes first; a stray closer closes nothing. A `<` is
// closed by a `>` before any token no type argument list holds.
func closedBrackets(toks []lexer.Token) []bool {
	closed := make([]bool, len(toks))
	var brackets, angles []int
	for i, t := range toks {
		switch t.Kind {
		case lexer.LAngle:
			angles = append(angles, i)
		case lexer.RAngle:
			if n := len(angles); n > 0 {
				closed[angles[n-1]] = true
				angles = angles[:n-1]
			}
		case lexer.Ident, lexer.KwMap, lexer.Dot, lexer.Comma, lexer.Question:
		case lexer.LBracket, lexer.RBracket:
		default:
			angles = angles[:0]
		}
		switch t.Kind {
		case lexer.LParen, lexer.LBracket, lexer.LBrace:
			brackets = append(brackets, i)
		case lexer.RParen, lexer.RBracket, lexer.RBrace:
			// The brackets above the one t closes were left open.
			j := len(brackets) - 1
			for j >= 0 && closerOf[toks[brackets[j]].Kind] != t.Kind {
				j--
			}
			if j >= 0 {
				closed[brackets[j]] = true
				brackets = brackets[:j]
			}
		}
	}
	return closed
}

// closes reports whether the bracket at token index open is closed.
func (p *Parser) closes(open int) bool {
	if p.closed == nil {
		p.closed = closedBrackets(p.tokens)
	}
	return p.closed[open]
}

// list is a bracketed list being parsed: the index of its opener, and whether
// anything closes it. One nothing closes ends with its opener's line.
type list struct {
	open   int
	closed bool
}

// openList consumes the opener at the cursor and returns its list.
func (p *Parser) openList() list {
	l := list{open: p.pos, closed: p.closes(p.pos)}
	p.advance()
	return l
}

// closer returns the kind of the bracket that closes l.
func (p *Parser) closer(l list) lexer.Kind { return closerOf[p.tokens[l.open].Kind] }

// more reports whether an element of l may start at the cursor: not at its
// closer or EOF and, when nothing closes l, on the line of its opener.
func (p *Parser) more(l list) bool {
	t := p.peek()
	return t.Kind != p.closer(l) && t.Kind != lexer.EOF && (l.closed || t.Pos.Line == p.tokens[l.open].Pos.Line)
}

// listSep consumes the `,` after an element of l and reports whether l goes
// on. Before its closer or EOF it consumes nothing; any other token ends l
// when nothing closes it, else is reported.
func (p *Parser) listSep(l list, element string) bool {
	switch t := p.peek(); {
	case t.Kind == lexer.Comma:
		p.advance()
	case t.Kind == p.closer(l) || t.Kind == lexer.EOF:
	case !l.closed:
		return false
	default:
		p.errorf(t.Pos, "expected ',' or '%s' after %s, got %s", p.closer(l), element, t.Kind)
	}
	return true
}

// closeList consumes the closer of l and returns it. A missing one is
// reported, at the opener when nothing closes l, and comes back as an empty
// token at the cursor.
func (p *Parser) closeList(l list) lexer.Token {
	if p.peekIs(p.closer(l)) {
		return p.advance()
	}
	if l.closed {
		p.expect(p.closer(l))
	} else {
		p.unclosed(p.tokens[l.open])
	}
	return lexer.Token{Pos: p.peek().Pos}
}

// startsLine reports whether the current token is the first on its line.
func (p *Parser) startsLine() bool {
	return p.pos == 0 || p.peek().Pos.Line > endLine(p.tokens[p.pos-1])
}

// endLine returns the line t ends on, below its own for a raw string over
// several lines.
func endLine(t lexer.Token) int {
	if t.Kind != lexer.RawString {
		return t.Pos.Line
	}
	return t.Pos.Line + strings.Count(t.Text, "\n") + strings.Count(t.Text, "\r") - strings.Count(t.Text, "\r\n")
}

// opensDeclaration reports whether the current token starts its line and a
// declaration with a body: `type`, `enum`, `service` or `event`, a name and a
// `{` or `<`, or `extend service`. No body holds one.
func (p *Parser) opensDeclaration() bool {
	if !p.startsLine() {
		return false
	}
	switch p.peek().Kind {
	case lexer.KwType, lexer.KwEnum, lexer.KwService, lexer.KwEvent:
		next := p.peekAt(2).Kind
		return p.peekAt(1).Kind == lexer.Ident && (next == lexer.LBrace || next == lexer.LAngle)
	case lexer.KwExtend:
		return p.peekAt(1).Kind == lexer.KwService
	}
	return false
}

// startsDeclaration reports whether the current token may start a
// declaration: `package`, a declaration keyword, or an `@` that starts its
// line.
func (p *Parser) startsDeclaration() bool {
	k := p.peek().Kind
	return k == lexer.KwPackage || startsDecl(k) || k == lexer.At && p.startsLine()
}

// startsMethod reports whether the current token starts its line and may
// start a method: a verb, one spelt in upper case, or an `@`.
func (p *Parser) startsMethod() bool {
	t := p.peek()
	_, upper := upperVerb(t)
	return p.startsLine() && (t.Kind.IsVerb() || upper || t.Kind == lexer.At)
}

// upperVerb returns the verb t spells in upper or mixed case, such as `GET`.
func upperVerb(t lexer.Token) (string, bool) {
	if t.Kind != lexer.Ident {
		return "", false
	}
	lower := strings.ToLower(t.Text)
	k, ok := lexer.LookupKeyword(lower)
	return lower, ok && k.IsVerb()
}

// skipBalanced consumes the current token and, when it opens a `(`, `[` or
// `{`, the tokens through the bracket that closes it.
func (p *Parser) skipBalanced() {
	depth := 0
	for !p.peekIs(lexer.EOF) {
		switch p.advance().Kind {
		case lexer.LParen, lexer.LBracket, lexer.LBrace:
			depth++
		case lexer.RParen, lexer.RBracket, lexer.RBrace:
			depth--
		}
		if depth <= 0 {
			return
		}
	}
}

// skipUntil consumes what failed to parse: the current token, then each next
// one, a bracketed group whole, until stop holds for one, or up to the `}` of
// the body the parser is in when inBody, or EOF.
func (p *Parser) skipUntil(inBody bool, stop func() bool) {
	p.skipBalanced()
	for !p.peekIs(lexer.EOF) && (!inBody || !p.peekIs(lexer.RBrace)) && !stop() {
		p.skipBalanced()
	}
}

// declarationKeywords are the words a declaration or the package clause
// starts with.
var declarationKeywords = []string{"package", "type", "enum", "error", "scalar", "service", "extend", "middleware", "event"}

// unknownKeyword is the diagnostic for word, an identifier where a
// declaration should start, naming the keyword it is a slip of, if any.
func unknownKeyword(word string) string {
	best, bestDist := "", 3
	for _, kw := range declarationKeywords {
		if d := editDistance(strings.ToLower(word), kw); d < bestDist {
			best, bestDist = kw, d
		}
	}
	if best != "" && len(word) > 2 {
		return fmt.Sprintf("unknown keyword `%s` - did you mean `%s`?", word, best)
	}
	return fmt.Sprintf("unknown keyword `%s` - a declaration starts with %s", word, strings.Join(declarationKeywords[1:], ", "))
}

// editDistance returns the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
