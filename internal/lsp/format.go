package lsp

import (
	"context"
	"strings"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/format"
)

// onFormatting answers `textDocument/formatting` with one whole-document edit,
// or none when the buffer carries an error or is already formatted.
func (s *server) onFormatting(_ context.Context, params protocol.DocumentFormattingParams) (any, error) {
	r, ok := s.open(params.TextDocument.URI)
	if !ok {
		return []protocol.TextEdit{}, nil
	}
	// A buffer with an error is left alone: a mistake the parser tolerates
	// reads as another construct, which formatting would write back.
	if r.project().hasErrors() {
		return []protocol.TextEdit{}, nil
	}
	formatted, diags := format.Format(string(params.TextDocument.URI), r.src)
	if len(diags) > 0 || formatted == r.src {
		return []protocol.TextEdit{}, nil
	}
	return []protocol.TextEdit{{
		Range:   wholeDocumentRange(r.src),
		NewText: formatted,
	}}, nil
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
