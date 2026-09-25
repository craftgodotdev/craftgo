package format

import (
	"fmt"
	"math"
	"strings"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// anchor is a construct that starts a line of its own in the canonical text:
// the package clause, an import, a declaration, a member, a method or event
// clause, or a closing brace. line is its first source line.
type anchor struct {
	line int
	name string
}

// placedComment is a comment's text and the place it holds in a file.
type placedComment struct {
	place string
	text  string
}

// layout is where the constructs and comments of a file sit.
type layout struct {
	// anchors are in source order, each construct before the ones inside it.
	anchors []anchor
	// tops are the first lines of the package clause, imports and declarations.
	tops []int
	// frees are the first lines of the free comment blocks.
	frees    []int
	comments []placedComment
}

// fileLayout returns the anchors of f and the place of every comment in it: the
// construct a trailing comment follows, the construct a doc or chain comment
// documents, or the scope of a free comment block with the number of the
// scope's constructs above it.
func fileLayout(f *ast.File) *layout {
	l := &layout{}
	l.doc("file", f.LeadingDoc)
	if f.Package != nil {
		l.top(f.Package.Pos.Line, "package")
		l.doc("package", f.Package.Doc)
	}
	for i, imp := range f.Imports {
		name := fmt.Sprintf("import %d", i)
		l.top(imp.Pos.Line, name)
		l.doc(name, imp.Doc)
	}
	for i, d := range f.Decls {
		l.decl(fmt.Sprintf("declaration %d", i), d)
	}
	for _, c := range f.FreeComments {
		above := 0
		for _, line := range l.tops {
			if line < c.Pos.Line {
				above++
			}
		}
		l.free("file", above, c)
	}
	for line, texts := range f.ChainComments {
		for _, text := range texts {
			l.comments = append(l.comments, placedComment{"in the decorators of " + l.at(line), text})
		}
	}
	for _, c := range f.Comments {
		if c.Kind == lexer.CommentTrailing {
			l.comments = append(l.comments, placedComment{"after " + l.at(c.Pos.Line), c.Text})
		}
	}
	return l
}

func (l *layout) anchor(line int, name string) {
	if line > 0 {
		l.anchors = append(l.anchors, anchor{line, name})
	}
}

// top records a top-level construct.
func (l *layout) top(line int, name string) {
	l.tops = append(l.tops, line)
	l.anchor(line, name)
}

func (l *layout) doc(name string, lines []string) {
	for _, text := range lines {
		l.comments = append(l.comments, placedComment{"the doc of " + name, text})
	}
}

// free records the free comment block c in scope, below above of the scope's
// constructs.
func (l *layout) free(scope string, above int, c *ast.FreeComment) {
	l.frees = append(l.frees, c.Pos.Line)
	for _, text := range c.Text {
		l.comments = append(l.comments, placedComment{fmt.Sprintf("in %s below %d constructs", scope, above), text})
	}
}

func (l *layout) decl(name string, d ast.Decl) {
	l.top(declFirstSourceLine(d), name)
	switch v := d.(type) {
	case *ast.TypeDecl:
		l.doc(name, v.Doc)
		l.typeBody(name, v.Body, v.EndPos)
	case *ast.ErrorDecl:
		l.doc(name, v.Doc)
		if v.HasBody {
			l.typeBody(name, v.Body, v.EndPos)
		}
	case *ast.EnumDecl:
		l.doc(name, v.Doc)
		n := 0
		for _, m := range v.Members {
			switch m := m.(type) {
			case *ast.EnumValue:
				member := fmt.Sprintf("%s member %d", name, n)
				l.anchor(m.Pos.Line, member)
				l.doc(member, m.Doc)
				n++
			case *ast.FreeComment:
				l.free(name, n, m)
			}
		}
		l.anchor(v.EndPos.Line, name+" end")
	case *ast.ScalarDecl:
		l.doc(name, v.Doc)
	case *ast.MiddlewareDecl:
		l.doc(name, v.Doc)
	case *ast.ServiceDecl:
		l.doc(name, v.Doc)
		n := 0
		for _, m := range v.Members {
			switch m := m.(type) {
			case *ast.Method:
				l.method(fmt.Sprintf("%s member %d", name, n), m)
				n++
			case *ast.FreeComment:
				l.free(name, n, m)
			}
		}
		l.anchor(v.EndPos.Line, name+" end")
	case *ast.EventDecl:
		l.doc(name, v.Doc)
		var clauses []anchor
		if v.Payload != nil {
			clauses = append(clauses, anchor{v.Payload.Pos.Line, name + " payload"})
		}
		l.body(name, clauses, v.BodyComments, v.EndPos)
	}
}

func (l *layout) typeBody(name string, body []ast.TypeMember, end ast.Pos) {
	n := 0
	for _, m := range body {
		member := fmt.Sprintf("%s member %d", name, n)
		switch m := m.(type) {
		case *ast.Field:
			l.anchor(memberStartLine(m.Pos.Line, m.Decorators, 0), member)
			l.doc(member, m.Doc)
			n++
		case *ast.Mixin:
			l.anchor(m.Pos.Line, member)
			l.doc(member, m.Doc)
			n++
		case *ast.FreeComment:
			l.free(name, n, m)
		}
	}
	l.anchor(end.Line, name+" end")
}

func (l *layout) method(name string, m *ast.Method) {
	l.anchor(memberStartLine(m.Pos.Line, m.Decorators, 0), name)
	l.doc(name, m.Doc)
	var clauses []anchor
	if m.Request != nil {
		clauses = append(clauses, anchor{m.Request.Pos.Line, name + " request"})
	}
	if m.Response != nil {
		clauses = append(clauses, anchor{m.Response.Pos.Line, name + " response"})
	}
	l.body(name, clauses, m.BodyComments, m.EndPos)
}

// body records the clauses of a method or event body, its comment blocks
// below the clauses above them, and its closing brace.
func (l *layout) body(name string, clauses []anchor, comments []*ast.FreeComment, end ast.Pos) {
	for _, cl := range clauses {
		l.anchor(cl.line, cl.name)
	}
	for _, c := range comments {
		above := 0
		for _, cl := range clauses {
			if cl.line < c.Pos.Line {
				above++
			}
		}
		l.free(name, above, c)
	}
	l.anchor(end.Line, name+" end")
}

// at returns the construct a comment on line follows: the last one starting
// on or above line, or the first when none does, as when the package clause
// starts above the name it records.
func (l *layout) at(line int) string {
	if len(l.anchors) == 0 {
		return ""
	}
	best := l.anchors[0]
	for _, a := range l.anchors {
		if a.line <= line && (best.line > line || a.line >= best.line) {
			best = a
		}
	}
	return best.name
}

// codeLines returns the source lines that hold a token or a comment, a raw
// string's every line included.
func codeLines(toks []lexer.Token, comments []*lexer.Comment) map[int]bool {
	lines := map[int]bool{}
	for _, t := range toks {
		if t.Kind == lexer.EOF {
			continue
		}
		for l := t.Pos.Line; l <= t.Pos.Line+lineEnds(t.Text); l++ {
			lines[l] = true
		}
	}
	for _, c := range comments {
		lines[c.Pos.Line] = true
	}
	return lines
}

// lineEnds counts the line ends in s: "\n", "\r\n" and a lone "\r".
func lineEnds(s string) int {
	return strings.Count(s, "\n") + strings.Count(s, "\r") - strings.Count(s, "\r\n")
}

// codeAfterFreeComments maps the first line of each free comment block to the
// first line of the next construct below it, math.MaxInt at the end of the file.
func (l *layout) codeAfterFreeComments() map[int]int {
	out := make(map[int]int, len(l.frees))
	for _, line := range l.frees {
		next := math.MaxInt
		for _, a := range l.anchors {
			if a.line > line && a.line < next {
				next = a.line
			}
		}
		out[line] = next
	}
	return out
}
