// Free-floating comment ownership: claim tracking for doc-attached comment
// lines and per-body harvesting of the unclaimed remainder into
// [ast.FreeComment] members, so `craftgo fmt` re-emits every comment in place.
package parser

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// claimDoc marks the source lines of tok's leading doc block as owned by an
// AST Doc field, so [Parser.harvestFreeComments] does not re-emit them as
// free-floating comments. The lexer guarantees a token's Doc is a contiguous
// run of `//` lines ending on the line directly above the token.
func (p *Parser) claimDoc(tok lexer.Token) {
	for i := range tok.Doc {
		p.claimed[tok.Pos.Line-len(tok.Doc)+i] = true
	}
}

// claimCommentsBetween marks every leading comment on a line strictly
// between lo and hi as owned. Used for a method's vertical decorator chain,
// whose in-chain comments are recovered by the formatter's inter-decorator
// lookup rather than by body harvesting - claiming them here prevents the
// same comment printing twice.
func (p *Parser) claimCommentsBetween(lo, hi int) {
	for _, c := range p.allComments {
		if c != nil && c.Kind == lexer.CommentLeading && c.Pos.Line > lo && c.Pos.Line < hi {
			p.claimed[c.Pos.Line] = true
		}
	}
}

// harvestFreeComments collects every unclaimed leading comment on a line
// strictly between lo and hi into position-accurate [ast.FreeComment] blocks
// (one block per contiguous run) and marks the lines claimed so enclosing
// bodies do not harvest them again. lo/hi are the body's brace lines.
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

// mergeFreeComments interleaves harvested comment blocks into a body's
// member list by source line. Both inputs are already in source order.
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
