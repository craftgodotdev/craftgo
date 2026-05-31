package lsp

import (
	"context"
	"encoding/json"
	"fmt"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// onPrepareRename answers `textDocument/prepareRename`. The editor calls
// this before showing its rename UI to learn whether the symbol under
// the cursor is renameable and what range covers it. We accept renames
// of identifiers that match a top-level declaration in the same file -
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

// onRename answers `textDocument/rename`. Every `.craftgo` file under
// the design root is scanned for Ident tokens matching the symbol's
// current name and rewritten in one WorkspaceEdit so a project-wide
// rename leaves no stale references in sibling files.
//
// Precondition: the cursor must sit on an identifier whose decl exists
// in the current file, so the user renames a thing they own rather than
// an imported foreign symbol.
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
		// Ensure the current document still appears in the response
		// so the editor's rename UI does not error out on empty maps.
		changes[params.TextDocument.URI] = []protocol.TextEdit{}
	}
	return reply(ctx, &protocol.WorkspaceEdit{Changes: changes}, nil)
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
