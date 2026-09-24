package lsp

import (
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

func formatDoc(t *testing.T, src string) []protocol.TextEdit {
	t.Helper()
	u := uri.New("file:///nowhere/t.craftgo")
	srv := &server{docs: map[uri.URI]string{u: src}}
	res, err := callHandler(t, srv, protocol.MethodTextDocumentFormatting, protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: u},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	return res.([]protocol.TextEdit)
}

// A buffer with an error gets no edit; the same layout without it gets one.
func TestFormattingRefusesBuffersWithErrors(t *testing.T) {
	if edits := formatDoc(t, "package p\n\ntype A {  x   Missing }\n"); len(edits) != 0 {
		t.Errorf("buffer with a semantic error got %d edit(s)", len(edits))
	}
	if edits := formatDoc(t, "package p\n\ntype A {  x   string }\n"); len(edits) != 1 {
		t.Errorf("clean buffer got %d edit(s), want 1", len(edits))
	}
}
