// Comment recovery: trailing / loose / free-comment span derivation from f.Comments.
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

// buildLooseFromComments walks f.Comments to find leading-comment blocks
// separated from the following decl by a blank line - the lexer drops
// those from any AST node's Doc, so without the loose lookup the
// formatter would lose them. Equivalent to [scanLooseComments] but
// driven entirely from the AST so [Print] (which has no source buffer)
// works the same as [Format].
//
// Comments already promoted to a [*ast.FreeComment] body member by the
// parser (e.g. closing notes captured from `rbrace.Doc`) are skipped
// here - otherwise they would also appear above the next anchor decl,
// double-printing the comment.
func buildLooseFromComments(f *ast.File) map[int][]string {
	out := map[int][]string{}
	if f == nil || len(f.Comments) == 0 {
		return out
	}
	anchors := declAnchorLines(f)
	if len(anchors) == 0 {
		return out
	}
	claimed := freeCommentLines(f)
	i := 0
	for i < len(f.Comments) {
		c := f.Comments[i]
		if c == nil || c.Kind != lexer.CommentLeading {
			i++
			continue
		}
		// Capture the contiguous leading run starting at c.
		block := []string{c.Text}
		startLine := c.Pos.Line
		lastLine := c.Pos.Line
		j := i + 1
		for j < len(f.Comments) {
			n := f.Comments[j]
			if n == nil || n.Kind != lexer.CommentLeading || n.Pos.Line != lastLine+1 {
				break
			}
			block = append(block, n.Text)
			lastLine = n.Pos.Line
			j++
		}
		// Skip the whole block when its first line is already owned by
		// a body-level FreeComment. Parser promotes `}.Doc` content into
		// FreeComment members with Pos = the closing brace, but the
		// comment text itself sits one or more lines above the brace -
		// the FreeComment Text length tells us the span.
		if claimed[startLine] {
			i = j
			continue
		}
		// Anchor = first decl line >= lastLine + 1.
		anchor := nextAnchor(anchors, lastLine+1)
		if anchor != 0 && anchor > lastLine+1 {
			// Blank line between block end and anchor → loose.
			if existing, ok := out[anchor]; ok {
				out[anchor] = append(append(existing, ""), block...)
			} else {
				out[anchor] = block
			}
		}
		i = j
	}
	return out
}

// freeCommentLines collects every source line covered by a [*ast.FreeComment]
// body member. The parser sets FreeComment.Pos to the closing `}` of the
// body and FreeComment.Text to the comment lines that immediately precede
// it - so the covered span is `Pos.Line - len(Text) .. Pos.Line - 1`.
// Used by [buildLooseFromComments] to suppress duplicate emission of
// comments that already live inside a body as FreeComment.
func freeCommentLines(f *ast.File) map[int]bool {
	out := map[int]bool{}
	for _, d := range f.Decls {
		walkBodyForFreeComments(d, out)
	}
	return out
}

func walkBodyForFreeComments(d ast.Decl, out map[int]bool) {
	switch v := d.(type) {
	case *ast.TypeDecl:
		collectFreeCommentSpans(v.Body, out)
	case *ast.ErrorDecl:
		collectFreeCommentSpans(v.Body, out)
	case *ast.EnumDecl:
		for _, m := range v.Members {
			if fc, ok := m.(*ast.FreeComment); ok {
				markFreeCommentSpan(fc, out)
			}
		}
	case *ast.ServiceDecl:
		for _, m := range v.Members {
			if fc, ok := m.(*ast.FreeComment); ok {
				markFreeCommentSpan(fc, out)
			}
		}
	}
}

func collectFreeCommentSpans(members []ast.TypeMember, out map[int]bool) {
	for _, m := range members {
		if fc, ok := m.(*ast.FreeComment); ok {
			markFreeCommentSpan(fc, out)
		}
	}
}

func markFreeCommentSpan(fc *ast.FreeComment, out map[int]bool) {
	if fc == nil || len(fc.Text) == 0 {
		return
	}
	startLine := fc.Pos.Line - len(fc.Text)
	for ln := startLine; ln < fc.Pos.Line; ln++ {
		out[ln] = true
	}
}

// declAnchorLines returns the source-line anchor of every AST node that
// can claim a preceding `//` block as its leading Doc. Used by the loose-
// comment resolver to decide which blocks the lexer flushed on a blank
// line still have an "owning" code line nearby.
//
// Includes:
//   - imports (Import.Doc captures comments directly above)
//   - top-level decls (each Decl.Doc — see [declFirstSourceLine] for
//     the decorator-aware anchor)
//   - body members that carry leading doc: fields, methods, enum values
//
// Excluding any of these would cause the loose resolver to mis-classify
// a comment as "free-floating" and re-emit it as a section block on the
// next decl, double-printing the comment.
func declAnchorLines(f *ast.File) []int {
	out := make([]int, 0, len(f.Imports)+len(f.Decls)*4)
	if f.Package != nil {
		out = append(out, f.Package.Pos.Line)
	}
	for _, imp := range f.Imports {
		out = append(out, imp.Pos.Line)
	}
	for _, d := range f.Decls {
		out = append(out, declFirstSourceLine(d))
		out = append(out, bodyMemberAnchors(d)...)
	}
	return out
}

// bodyMemberAnchors collects the source-line anchor of every body
// member inside a top-level decl that can carry leading Doc:
// fields/mixins inside type/error bodies, methods inside service
// bodies, and enum values inside enum bodies. FreeComment members
// are skipped - they ARE the comments we're trying to anchor.
func bodyMemberAnchors(d ast.Decl) []int {
	var out []int
	switch v := d.(type) {
	case *ast.TypeDecl:
		for _, m := range v.Body {
			if pos, ok := memberAnchor(m); ok {
				out = append(out, pos)
			}
		}
	case *ast.ErrorDecl:
		for _, m := range v.Body {
			if pos, ok := memberAnchor(m); ok {
				out = append(out, pos)
			}
		}
	case *ast.EnumDecl:
		for _, m := range v.Members {
			if v, ok := m.(*ast.EnumValue); ok {
				out = append(out, v.Pos.Line)
			}
		}
	case *ast.ServiceDecl:
		for _, m := range v.Members {
			if mm, ok := m.(*ast.Method); ok {
				out = append(out, mm.Pos.Line)
			}
		}
	}
	return out
}

// memberAnchor returns the source line of a TypeMember when it can
// claim a preceding leading-doc block. FreeComment is excluded.
func memberAnchor(m ast.TypeMember) (int, bool) {
	switch v := m.(type) {
	case *ast.Field:
		return v.Pos.Line, true
	case *ast.Mixin:
		return v.Pos.Line, true
	}
	return 0, false
}

// nextAnchor returns the smallest entry in anchors that is >= line, or
// 0 when none. anchors is in source order (which is also numeric order
// for valid input). Linear scan is fine - top-level decl counts are
// small (~tens to hundreds, never thousands).
func nextAnchor(anchors []int, line int) int {
	for _, a := range anchors {
		if a >= line {
			return a
		}
	}
	return 0
}
