package lsp

import (
	"context"
	"encoding/json"
	"fmt"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/dropship-dev/craftgo/internal/lexer"
)

// onPrepareRename answers `textDocument/prepareRename`. The editor calls
// this before showing its rename UI to learn whether the symbol under
// the cursor is renameable and what range covers it. We accept renames
// of identifiers that match a top-level declaration in the same file —
// every other position returns nil (LSP for "not supported here").
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

// onRename answers `textDocument/rename`. We rewrite every Ident token
// whose text matches the symbol's old name. The result is one
// WorkspaceEdit with a single entry under the current document — multi-
// file rename will land with the workspace-wide pass.
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
	edits := make([]protocol.TextEdit, 0)
	for _, t := range view.tokens {
		if t.Kind != lexer.Ident || t.Text != tok.Text {
			continue
		}
		edits = append(edits, protocol.TextEdit{Range: rangeOf(t), NewText: params.NewName})
	}
	return reply(ctx, &protocol.WorkspaceEdit{
		Changes: map[protocol.DocumentURI][]protocol.TextEdit{
			params.TextDocument.URI: edits,
		},
	}, nil)
}

// isValidIdent enforces the lexer's identifier rule (`[A-Za-z_][A-Za-z0-9_]*`)
// so the rename result will lex back into a single Ident token. Empty
// strings, leading digits, and embedded punctuation are rejected.
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
