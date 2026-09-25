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

// The edit of a buffer whose lines end in a lone `\r` replaces all of it.
func TestFormattingReplacesACarriageReturnBuffer(t *testing.T) {
	src := "package p\r\rtype A {  x   string }\r"
	edits := formatDoc(t, src)
	if len(edits) != 1 || edits[0].Range.End != (protocol.Position{Line: 3}) {
		t.Errorf("edits = %+v, want one ending past the last line end", edits)
	}
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

// Formatting a clean buffer replaces the whole document with its canonical
// text.
func TestFormattingProducesEdit(t *testing.T) {
	dirty := "package x\n\ntype T {\n  id string\n}\n"
	clean := "package x\n\ntype T {\n\tid string\n}\n"
	edits := formatDoc(t, dirty)
	if len(edits) != 1 {
		t.Fatalf("edits = %+v, want one", edits)
	}
	if edits[0].Range != wholeDocumentRange(dirty) || edits[0].NewText != clean {
		t.Errorf("edit = %+v, want the whole document replaced by %q", edits[0], clean)
	}
}
