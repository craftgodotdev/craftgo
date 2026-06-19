package lsp

import (
	"context"
	"encoding/json"
	"strings"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/format"
)

// onFormatting answers `textDocument/formatting`. We rely on the
// canonical formatter in internal/format and replace the document text
// wholesale via a single TextEdit. If the formatter reports parse
// diagnostics we leave the buffer untouched - formatting a syntactically
// broken file would mangle it.
func (s *Server) onFormatting(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
	var params protocol.DocumentFormattingParams
	if err := json.Unmarshal(req.Params(), &params); err != nil {
		return reply(ctx, nil, err)
	}
	src := s.snapshot(params.TextDocument.URI)
	if src == "" {
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

// wholeDocumentRange returns a range covering the entire source buffer.
// Replacing this range with the formatted output is how LSP servers
// implement whole-document formatting without exchanging diff hunks.
func wholeDocumentRange(src string) protocol.Range {
	lines := strings.Count(src, "\n")
	lastLine := src
	if i := strings.LastIndexByte(src, '\n'); i >= 0 {
		lastLine = src[i+1:]
	}
	// LSP character offsets are UTF-16 code units, not bytes - a last line
	// holding multi-byte UTF-8 (Vietnamese, CJK, emoji) would otherwise
	// over-shoot and the formatting TextEdit would target the wrong range.
	return protocol.Range{
		Start: protocol.Position{Line: 0, Character: 0},
		End:   protocol.Position{Line: uint32(lines), Character: uint32(utf16Len(lastLine))},
	}
}
