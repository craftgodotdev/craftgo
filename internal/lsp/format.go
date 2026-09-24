package lsp

import (
	"context"
	"encoding/json"
	"strings"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/format"
)

// onFormatting answers `textDocument/formatting` with one whole-document edit,
// or none when the buffer carries an error or is already formatted.
func (s *server) onFormatting(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.DocumentFormattingParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	src := s.snapshot(params.TextDocument.URI)
	if src == "" {
		return reply(ctx, []protocol.TextEdit{}, nil)
	}
	// A buffer with an error is left alone: a mistake the parser tolerates
	// reads as another construct, which formatting would write back.
	if s.loadProject(uriToPath(string(params.TextDocument.URI)), src).hasErrors() {
		return reply(ctx, []protocol.TextEdit{}, nil)
	}
	formatted, diags := format.Format(string(params.TextDocument.URI), src)
	if len(diags) > 0 || formatted == src {
		return reply(ctx, []protocol.TextEdit{}, nil)
	}
	return reply(ctx, []protocol.TextEdit{{
		Range:   wholeDocumentRange(src),
		NewText: formatted,
	}}, nil)
}

// wholeDocumentRange returns the range covering all of src.
func wholeDocumentRange(src string) protocol.Range {
	lines := strings.Count(src, "\n")
	lastLine := src
	if i := strings.LastIndexByte(src, '\n'); i >= 0 {
		lastLine = src[i+1:]
	}
	// The end character counts UTF-16 units.
	return protocol.Range{
		Start: protocol.Position{Line: 0, Character: 0},
		End:   protocol.Position{Line: uint32(lines), Character: uint32(utf16Len(lastLine))},
	}
}
