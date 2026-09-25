package lsp

import (
	"context"
	"fmt"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// onPrepareRename answers `textDocument/prepareRename` with the range of an
// identifier spelt like the declaration it names, else null.
func (s *server) onPrepareRename(_ context.Context, params protocol.PrepareRenameParams) (any, error) {
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	view := r.view()
	c := view.cursorAt(params.Position)
	if r.renameTarget(c) == nil {
		return nil, nil
	}
	rng := rangeOf(view.src, view.tokens[c.at])
	return &rng, nil
}

// onRename answers `textDocument/rename`, rewriting every identifier in the
// project that names the declaration the one at the cursor names.
func (s *server) onRename(_ context.Context, params protocol.RenameParams) (any, error) {
	if !lexer.IsIdent(params.NewName) {
		return nil, fmt.Errorf("invalid rename target %q: not a craftgo identifier", params.NewName)
	}
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}
	d := r.renameTarget(r.view().cursorAt(params.Position))
	if d == nil {
		return nil, nil
	}
	changes := map[protocol.DocumentURI][]protocol.TextEdit{}
	for _, loc := range r.project().references(d, true, r.uri) {
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

// renameTarget returns the declaration the identifier at c names, or nil on
// the package half of `pkg.Name`.
func (r *request) renameTarget(c cursor) ast.Decl {
	if c.at < 0 || isQualifier(r.view(), c.at) {
		return nil
	}
	return r.project().symbolAt(r.view(), c.at)
}
