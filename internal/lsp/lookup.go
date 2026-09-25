package lsp

import (
	"iter"
	"slices"
	"strings"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
)

// snapshotView is one buffer's source, tokens and AST, parsed per request.
type snapshotView struct {
	src    string
	tokens []lexer.Token
	file   *ast.File
}

func parseSnapshot(filename, src string) snapshotView {
	p := parser.New(filename, src)
	f := p.Parse()
	return snapshotView{src: src, tokens: p.Tokens(), file: f}
}

// cursor is an LSP position located in a snapshotView's tokens.
type cursor struct {
	off  int // byte offset in the buffer
	line int // 1-based
	// at is the token whose span, end included, holds off (the later of two
	// that touch), or -1 on whitespace.
	at int
	// prev is the token before at or, on whitespace, the last token before
	// off; -1 when there is none.
	prev int
}

// cursorAt locates pos in the buffer.
func (v snapshotView) cursorAt(pos protocol.Position) cursor {
	c := cursor{off: offsetFromLSP(v.src, pos.Line, pos.Character), line: int(pos.Line) + 1, at: -1, prev: -1}
	for i, t := range v.tokens {
		if t.Kind == lexer.EOF || t.Pos.Offset > c.off {
			break
		}
		if c.off <= v.end(i) {
			c.at = i
		} else {
			c.prev = i
		}
	}
	if c.at >= 0 {
		c.prev = c.at - 1
	}
	return c
}

// end returns the offset just past token i in the buffer. An Error token's
// text is its diagnostic, so it ends where the next token or comment starts,
// less the blanks before that.
func (v snapshotView) end(i int) int {
	t := v.tokens[i]
	if t.Kind != lexer.Error {
		return t.Pos.Offset + len(t.Text)
	}
	end := len(v.src)
	if i+1 < len(v.tokens) {
		end = v.tokens[i+1].Pos.Offset
	}
	if v.file != nil {
		if j := slices.IndexFunc(v.file.Comments, func(c *ast.Comment) bool { return c.Pos.Offset > t.Pos.Offset }); j >= 0 {
			end = min(end, v.file.Comments[j].Pos.Offset)
		}
	}
	return t.Pos.Offset + len(strings.TrimRight(v.src[t.Pos.Offset:end], " \t\r\n"))
}

// cursorOn returns the cursor at the start of token i.
func (v snapshotView) cursorOn(i int) cursor {
	t := v.tokens[i]
	return cursor{off: t.Pos.Offset, line: t.Pos.Line, at: i, prev: i - 1}
}

// packageName returns the package the file declares, "" for none.
func (v snapshotView) packageName() string {
	if v.file == nil || v.file.Package == nil {
		return ""
	}
	return v.file.Package.Name
}

// token returns the token at index i, or nil for -1.
func (v snapshotView) token(i int) *lexer.Token {
	if i < 0 {
		return nil
	}
	return &v.tokens[i]
}

// kind returns the kind of the token at index i, or [lexer.EOF] outside the
// tokens.
func (v snapshotView) kind(i int) lexer.Kind {
	if i < 0 || i >= len(v.tokens) {
		return lexer.EOF
	}
	return v.tokens[i].Kind
}

// outsideParens yields, in order, the indices of the tokens that start after
// byte offset after, skipping every decorator argument list and stray `)`.
func (v snapshotView) outsideParens(after int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := 0; i < len(v.tokens); i++ {
			switch t := v.tokens[i]; {
			case t.Pos.Offset <= after, t.Kind == lexer.RParen:
			case t.Kind == lexer.LParen:
				i = v.argEnd(i)
			case !yield(i):
				return
			}
		}
	}
}

// argEnd returns the index of the `)` that closes the argument list opened at
// token i or, for one left open, of the last token on the `(`'s line.
func (v snapshotView) argEnd(i int) int {
	depth, last := 0, i
	for j := i; j < len(v.tokens); j++ {
		switch v.tokens[j].Kind {
		case lexer.LParen:
			depth++
		case lexer.RParen:
			if depth--; depth == 0 {
				return j
			}
		}
		if v.tokens[j].Pos.Line == v.tokens[i].Pos.Line {
			last = j
		}
	}
	return last
}

// lead returns the number of tokens before the one under c, or before c on
// whitespace.
func (c cursor) lead() int {
	if c.at >= 0 {
		return c.at
	}
	return c.prev + 1
}

// lastBefore returns the last token that starts before c, or -1.
func (v snapshotView) lastBefore(c cursor) int {
	if c.at >= 0 && v.tokens[c.at].Pos.Offset < c.off {
		return c.at
	}
	return c.prev
}

// fieldAtCursor returns the type or error field on the cursor's line: the last
// one starting before the cursor, else the line's first; nil off a field row.
func fieldAtCursor(view snapshotView, c cursor) *ast.Field {
	if view.file == nil {
		return nil
	}
	var at *ast.Field
	for _, d := range view.file.Decls {
		body, ok := declBody(d)
		if !ok {
			continue
		}
		for _, m := range body {
			if f, ok := m.(*ast.Field); ok && f.Pos.Line == c.line && (at == nil || f.Pos.Offset < c.off) {
				at = f
			}
		}
	}
	return at
}

// declBody returns the members of a type or of an error with a body, and
// whether d has one.
func declBody(d ast.Decl) ([]ast.TypeMember, bool) {
	switch v := d.(type) {
	case *ast.TypeDecl:
		return v.Body, true
	case *ast.ErrorDecl:
		if v.HasBody {
			return v.Body, true
		}
	}
	return nil, false
}

// noDeclBetween reports whether f declares nothing on the lines strictly
// between from and to.
func noDeclBetween(f *ast.File, from, to int) bool {
	for _, d := range f.Decls {
		l := d.DeclPos().Line
		if l > from && l < to {
			return false
		}
	}
	return true
}

// pathToFileURIString returns the file:// URI of path, or "" for an empty path.
func pathToFileURIString(path string) string {
	if path == "" {
		return ""
	}
	return string(uri.File(path))
}
