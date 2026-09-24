package format

import (
	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

func (p *Printer) printFreeComment(c *ast.FreeComment) {
	for _, line := range c.Text {
		p.indent()
		if line == "" {
			p.write("//")
		} else {
			p.write("// ")
			p.write(line)
		}
		p.nl()
	}
}

func buildTrailingFromComments(f *ast.File) map[int]string {
	out := map[int]string{}
	if f == nil {
		return out
	}
	for _, c := range f.Comments {
		if c == nil || c.Kind != lexer.CommentTrailing {
			continue
		}
		out[c.Pos.Line] = c.Text
	}
	return out
}

// chainSpan is a declaration's decorators and the line of the keyword after them.
type chainSpan struct {
	decs        []*ast.Decorator
	keywordLine int
}

// chainSpans returns the chain of every declaration and method that prints its
// decorators one per line; a scalar prints them inline and has no span.
func chainSpans(f *ast.File) []chainSpan {
	var out []chainSpan
	add := func(decs []*ast.Decorator, keywordLine int) {
		if len(decs) > 0 {
			out = append(out, chainSpan{decs: decs, keywordLine: keywordLine})
		}
	}
	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.TypeDecl:
			add(v.Decorators, v.Pos.Line)
		case *ast.EnumDecl:
			add(v.Decorators, v.Pos.Line)
		case *ast.ErrorDecl:
			add(v.Decorators, v.Pos.Line)
		case *ast.MiddlewareDecl:
			add(v.Decorators, v.Pos.Line)
		case *ast.EventDecl:
			add(v.Decorators, v.Pos.Line)
		case *ast.ServiceDecl:
			add(v.Decorators, v.Pos.Line)
			for _, m := range v.Members {
				if mm, ok := m.(*ast.Method); ok {
					add(mm.Decorators, mm.Pos.Line)
				}
			}
		}
	}
	return out
}

// buildInterDecoratorComments keys each in-chain comment by the line of the decorator
// or keyword after it; the parser claims these lines, so no FreeComment repeats them.
func buildInterDecoratorComments(f *ast.File) map[int][]string {
	out := map[int][]string{}
	if f == nil || len(f.Comments) == 0 {
		return out
	}
	for _, span := range chainSpans(f) {
		prev := span.decs[0].Pos.Line
		boundaries := make([]int, 0, len(span.decs))
		for _, d := range span.decs[1:] {
			boundaries = append(boundaries, d.Pos.Line)
		}
		boundaries = append(boundaries, span.keywordLine)
		for _, b := range boundaries {
			if block := leadingCommentsBetween(f, prev, b); len(block) > 0 {
				out[b] = append(out[b], block...)
			}
			prev = b
		}
	}
	return out
}

// leadingCommentsBetween returns the leading comments on lines strictly between lo and hi.
func leadingCommentsBetween(f *ast.File, lo, hi int) []string {
	var block []string
	for _, c := range f.Comments {
		if c == nil || c.Kind != lexer.CommentLeading {
			continue
		}
		if c.Pos.Line > lo && c.Pos.Line < hi {
			block = append(block, c.Text)
		}
	}
	return block
}
