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

// claimCommentsBetween claims every leading comment strictly between lines lo
// and hi.
func (p *Parser) claimCommentsBetween(lo, hi int) {
	for _, c := range p.allComments {
		if c != nil && c.Kind == lexer.CommentLeading && c.Pos.Line > lo && c.Pos.Line < hi {
			p.claimed[c.Pos.Line] = true
		}
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
