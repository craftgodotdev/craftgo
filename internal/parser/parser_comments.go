package parser

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// claimDoc claims the lines of tok's Doc, which ends on the line above tok.
func (p *Parser) claimDoc(tok lexer.Token) {
	for i := range tok.Doc {
		p.claimed[tok.Pos.Line-len(tok.Doc)+i] = true
	}
}

// claimChain claims the comments inside the decorator chain decs, which ends
// at the name or keyword on line last, and records each block under the line
// of the decorator, name or keyword below it.
func (p *Parser) claimChain(decs []*ast.Decorator, last int) {
	if len(decs) == 0 {
		return
	}
	p.claimBetween(decs[0].Pos.Line, append(decoratorLines(decs[1:]), last))
}

// claimTrailing claims the comments among decs, the decorators after a member
// whose code starts on line first, and records each block under the line of
// the decorator below it.
func (p *Parser) claimTrailing(first int, decs []*ast.Decorator) {
	p.claimBetween(first, decoratorLines(decs))
}

// decoratorLines returns the source line of each of decs.
func decoratorLines(decs []*ast.Decorator) []int {
	lines := make([]int, 0, len(decs))
	for _, d := range decs {
		lines = append(lines, d.Pos.Line)
	}
	return lines
}

// claimBetween claims the comments below line start and above the last of
// lines, and records each block under the first of lines below it.
func (p *Parser) claimBetween(start int, lines []int) {
	prev := start
	for _, c := range p.allComments {
		if c.Kind != lexer.CommentLeading || c.Pos.Line <= prev {
			continue
		}
		for len(lines) > 0 && c.Pos.Line >= lines[0] {
			prev, lines = lines[0], lines[1:]
		}
		if len(lines) == 0 {
			return
		}
		p.claimed[c.Pos.Line] = true
		p.chainComments[lines[0]] = append(p.chainComments[lines[0]], c.Text)
	}
}

// harvestFreeComments claims the unclaimed leading comments strictly between
// lines lo and hi and returns them as blocks of adjacent lines.
func (p *Parser) harvestFreeComments(lo, hi int) []*ast.FreeComment {
	var out []*ast.FreeComment
	var cur *ast.FreeComment
	lastLine := 0
	for _, c := range p.allComments {
		if c == nil || c.Kind != lexer.CommentLeading {
			continue
		}
		if c.Pos.Line <= lo || c.Pos.Line >= hi || p.claimed[c.Pos.Line] {
			continue
		}
		p.claimed[c.Pos.Line] = true
		if cur != nil && c.Pos.Line == lastLine+1 {
			cur.Text = append(cur.Text, c.Text)
		} else {
			cur = &ast.FreeComment{Pos: c.Pos, Text: []string{c.Text}}
			out = append(out, cur)
		}
		lastLine = c.Pos.Line
	}
	return out
}

// mergeFreeComments interleaves fcs into members by line; both are in source
// order.
func mergeFreeComments[M interface{ MemberPos() ast.Pos }](members []M, fcs []*ast.FreeComment, asMember func(*ast.FreeComment) M) []M {
	if len(fcs) == 0 {
		return members
	}
	out := make([]M, 0, len(members)+len(fcs))
	i, j := 0, 0
	for i < len(members) || j < len(fcs) {
		if i >= len(members) || (j < len(fcs) && fcs[j].Pos.Line < members[i].MemberPos().Line) {
			out = append(out, asMember(fcs[j]))
			j++
		} else {
			out = append(out, members[i])
			i++
		}
	}
	return out
}
