package lsp

import (
	"context"

	"go.lsp.dev/protocol"

	"github.com/craftgodotdev/craftgo/internal/designopts"
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
	if len(designopts.FileErrors(r.project().diags, r.path)) > 0 {
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
	ends, last := lastLine(src)
	return protocol.Range{End: protocol.Position{Line: uint32(ends), Character: uint32(utf16Len(src[last:]))}}
}
