package lsp

import (
	"path/filepath"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	"github.com/craftgodotdev/craftgo/internal/parser"
)

// snapshot is the shared parse view that every feature handler operates
// on. Re-tokenising and re-parsing on every request keeps the wire model
// simple and matches what `craftgo lint` would see - no risk of stale
// AST drift between editor and CLI.
type snapshotView struct {
	src    string
	tokens []lexer.Token
	file   *ast.File
}

func parseSnapshot(filename, src string) snapshotView {
	toks := lexer.New(filename, src).Tokenize()
	f := parser.New(filename, src).Parse()
	return snapshotView{src: src, tokens: toks, file: f}
}

// tokenAt returns the token whose source span covers the supplied
// LSP-style (0-indexed) cursor and the index into the token slice. The
// hit token may be EOF for cursors past the last real token; callers
// should check Kind to filter that out.
func (v snapshotView) tokenAt(line, character uint32) (int, lexer.Token) {
	// Resolve the editor's UTF-16 (line, character) to a byte offset and
	// match tokens by their byte span. Byte offsets avoid the unit
	// mismatches a line/column compare carries (the LSP character is
	// UTF-16 while the lexer column is runes, and `Column + len(Text)`
	// would mix a rune column with a byte length): every Token carries
	// Pos.Offset and its byte length is len(Text).
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

// rangeOfPosLen builds a range starting at p with width n columns.
func rangeOfPosLen(p lexer.Position, n int) protocol.Range {
	start := lspPos(p)
	endPos := p
	endPos.Column += n
	return protocol.Range{Start: start, End: lspPos(endPos)}
}

// fieldAtCursor returns the field whose row matches the cursor's line
// when the cursor is inside a type / error body. Returns nil when the
// cursor is not on a field row.
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

// declBody returns a type-body slice for declarations that have one
// (TypeDecl always; ErrorDecl when HasBody is set). The bool says
// whether a body exists; nil-body decls return false so callers can
// short-circuit cleanly.
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

// noDeclBetween reports whether the file has zero declarations on
// lines strictly between `from` (exclusive) and `to` (exclusive). Used
// by scalarPrimAt to make sure the "above" attribution stays adjacent.
func noDeclBetween(f *ast.File, from, to int) bool {
	for _, d := range f.Decls {
		l := d.DeclPos().Line
		if l > from && l < to {
			return false
		}
	}
	return true
}

// pathToFileURIString builds a `file://...` URI string from an absolute
// filesystem path. Feature handlers feed the result into [uri.New] so
// the typed URI lines up with what the editor would have sent for a
// sibling file in the same project.
func pathToFileURIString(path string) string {
	if path == "" {
		return ""
	}
	abs := path
	if !filepath.IsAbs(abs) {
		if a, err := filepath.Abs(abs); err == nil {
			abs = a
		}
	}
	return "file://" + abs
}
