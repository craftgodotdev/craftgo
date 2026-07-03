// Comment lookup maps derived from f.Comments: trailing `// note` text and
// inter-decorator blocks. Free-floating blocks need no recovery here - the
// parser owns them as position-accurate [ast.FreeComment] nodes.
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

// chainSpan describes one declaration's decorator chain: the decorators in
// source order plus the 1-indexed line of the keyword they precede (the
// `type` / `service` / verb token). A comment written between two decorators,
// or between the last decorator and the keyword, sits inside this span - the
// lexer records it in f.Comments but no AST node owns it.
type chainSpan struct {
	decs        []*ast.Decorator
	keywordLine int
}

// chainSpans returns the decorator chain of every declaration the formatter
// renders with a vertical (one-per-line) decorator block: the six
// declDecorators callers plus service methods. Scalars render their
// decorators inline on the declaration line, so a comment can never sit
// between them on its own line; they are excluded.
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
		case *ast.ServiceDecl:
			add(v.Decorators, v.Pos.Line)
			for _, m := range v.Methods() {
				add(m.Decorators, m.Pos.Line)
			}
		}
	}
	return out
}

// buildInterDecoratorComments routes comments that sit inside a decorator
// chain to the decorator (or keyword) they immediately precede. The key is
// the 1-indexed line of that following token; [Printer.declDecorators] flushes
// the block just before emitting it. The parser claims these comment lines
// (see claimCommentsBetween) so its FreeComment harvest never duplicates
// them.
func buildInterDecoratorComments(f *ast.File) map[int][]string {
	out := map[int][]string{}
	if f == nil || len(f.Comments) == 0 {
		return out
	}
	for _, span := range chainSpans(f) {
		// Boundary tokens after the first decorator, in source order: the
		// remaining decorators, then the keyword. A leading-comment run
		// strictly between the previous token and a boundary belongs to it.
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

// leadingCommentsBetween returns the text of every CommentLeading whose line
// is strictly between lo and hi.
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
