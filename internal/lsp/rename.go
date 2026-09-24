package lsp

import (
	"context"
	"fmt"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// onPrepareRename answers `textDocument/prepareRename` with the range of an
// identifier that names a declaration in the buffer, else null.
func (s *server) onPrepareRename(_ context.Context, params protocol.PrepareRenameParams) (any, error) {
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	view := r.view()
	c := view.cursorAt(params.Position)
	if c.at < 0 || view.tokens[c.at].Kind != lexer.Ident || findDecl(view.file, view.tokens[c.at].Text) == nil {
		return nil, nil
	}
	rng := rangeOf(view.src, view.tokens[c.at])
	return &rng, nil
}

// onRename answers `textDocument/rename`, rewriting every same-spelt identifier
// in the project when the cursor names a declaration in the buffer.
func (s *server) onRename(_ context.Context, params protocol.RenameParams) (any, error) {
	if !lexer.IsIdent(params.NewName) {
		return nil, fmt.Errorf("invalid rename target %q: not a craftgo identifier", params.NewName)
	}
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	v := r.project()
	view := r.view()
	c := view.cursorAt(params.Position)
	if c.at < 0 || view.tokens[c.at].Kind != lexer.Ident || findDecl(view.file, view.tokens[c.at].Text) == nil {
		return nil, nil
	}
	changes := map[protocol.DocumentURI][]protocol.TextEdit{}
	for _, loc := range v.nameMatches(view.tokens[c.at].Text, true, r.uri) {
		changes[loc.URI] = append(changes[loc.URI], protocol.TextEdit{
			Range:   loc.Range,
			NewText: params.NewName,
		})
	}
	if len(changes) == 0 {
		// The buffer is always listed: the rename UI fails on an empty map.
		changes[r.uri] = []protocol.TextEdit{}
	}
	return &protocol.WorkspaceEdit{Changes: changes}, nil
}
