package lsp

import (
	"context"
	"encoding/json"
	"fmt"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// onPrepareRename answers `textDocument/prepareRename` with the range of an
// identifier that names a declaration in the buffer, else null.
func (s *Server) onPrepareRename(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.PrepareRenameParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	src := s.snapshot(params.TextDocument.URI)
	if src == "" {
		return reply(ctx, nil, nil)
	}
	view := parseSnapshot(string(params.TextDocument.URI), src)
	idx, tok := view.tokenAt(params.Position.Line, params.Position.Character)
	if idx < 0 || tok.Kind != lexer.Ident {
		return reply(ctx, nil, nil)
	}
	if findDecl(view.file, tok.Text) == nil {
		return reply(ctx, nil, nil)
	}
	r := rangeOf(tok)
	return reply(ctx, &r, nil)
}

// onRename answers `textDocument/rename`, rewriting every same-spelt identifier
// in the project when the cursor names a declaration in the buffer.
func (s *Server) onRename(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.RenameParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	if !isValidIdent(params.NewName) {
		return reply(ctx, nil, fmt.Errorf("invalid rename target %q: not a craftgo identifier", params.NewName))
	}
	src := s.snapshot(params.TextDocument.URI)
	if src == "" {
		return reply(ctx, nil, nil)
	}
	view := parseSnapshot(string(params.TextDocument.URI), src)
	idx, tok := view.tokenAt(params.Position.Line, params.Position.Character)
	if idx < 0 || tok.Kind != lexer.Ident || findDecl(view.file, tok.Text) == nil {
		return reply(ctx, nil, nil)
	}
	matches := s.projectNameMatches(view, params.TextDocument.URI, src, tok.Text, true)
	changes := map[protocol.DocumentURI][]protocol.TextEdit{}
	for _, loc := range matches {
		changes[loc.URI] = append(changes[loc.URI], protocol.TextEdit{
			Range:   loc.Range,
			NewText: params.NewName,
		})
	}
	if len(changes) == 0 {
		// The buffer is always listed: the rename UI fails on an empty map.
		changes[params.TextDocument.URI] = []protocol.TextEdit{}
	}
	return reply(ctx, &protocol.WorkspaceEdit{Changes: changes}, nil)
}

// isValidIdent reports whether s matches `[A-Za-z_][A-Za-z0-9_]*`.
func isValidIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
