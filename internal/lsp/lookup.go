package lsp

import (
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

// tokenAt returns the index and token whose byte span, end included, holds
// the LSP position (the later token on a tie), or -1 when none does.
func (v snapshotView) tokenAt(line, character uint32) (int, lexer.Token) {
	off := offsetFromLSP(v.src, line, character)
	best := -1
	for i, t := range v.tokens {
		if t.Kind == lexer.EOF {
			continue
		}
		start := t.Pos.Offset
		end := start + len(t.Text)
		if start <= off && off <= end {
			best = i
		}
	}
	if best < 0 {
		return -1, lexer.Token{}
	}
	return best, v.tokens[best]
}

// rangeOf returns the LSP range covering t.
func rangeOf(t lexer.Token) protocol.Range {
	start := lspPos(t.Pos)
	endPos := t.Pos
	endPos.Column += len(t.Text)
	return protocol.Range{Start: start, End: lspPos(endPos)}
}

// rangeOfPosLen returns the range of n columns starting at p.
func rangeOfPosLen(p lexer.Position, n int) protocol.Range {
	start := lspPos(p)
	endPos := p
	endPos.Column += n
	return protocol.Range{Start: start, End: lspPos(endPos)}
}

// fieldAtCursor returns the type or error field on the cursor's line, or nil.
func fieldAtCursor(view snapshotView, pos protocol.Position) *ast.Field {
	if view.file == nil {
		return nil
	}
	line := int(pos.Line) + 1
	for _, d := range view.file.Decls {
		body, ok := declBody(d)
		if !ok {
			continue
		}
		for _, m := range body {
			f, ok := m.(*ast.Field)
			if !ok || f.Pos.Line != line {
				continue
			}
			return f
		}
	}
	return nil
}

// findDecl returns f's first declaration named name, or nil.
func findDecl(f *ast.File, name string) ast.Decl {
	if f == nil {
		return nil
	}
	for _, d := range f.Decls {
		if d.DeclName() == name {
			return d
		}
	}
	return nil
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

// declName returns the name of a type or error declaration, else "".
func declName(d ast.Decl) string {
	switch v := d.(type) {
	case *ast.TypeDecl:
		return v.Name
	case *ast.ErrorDecl:
		return v.Name
	}
	return ""
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
