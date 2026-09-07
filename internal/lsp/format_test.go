package lsp

import (
	"context"
	"testing"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

func formatDoc(t *testing.T, src string) []protocol.TextEdit {
	t.Helper()
	u := uri.New("file:///nowhere/t.craftgo")
	srv := &Server{docs: map[uri.URI]*document{u: {text: src}}}
	params := protocol.DocumentFormattingParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI(u)}}
	req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), protocol.MethodTextDocumentFormatting, params)
	if err != nil {
		t.Fatal(err)
	}
	var got []protocol.TextEdit
	replier := func(_ context.Context, result interface{}, err error) error {
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
		got = result.([]protocol.TextEdit)
		return nil
	}
	if err := srv.onFormatting(context.Background(), replier, req); err != nil {
		t.Fatal(err)
	}
	return got
}

// A buffer the analyser rejects is left alone even when its layout is off;
// the same layout formats once the error is gone.
func TestFormattingRefusesBuffersWithErrors(t *testing.T) {
	if edits := formatDoc(t, "package p\n\ntype A {  x   Missing }\n"); len(edits) != 0 {
		t.Errorf("buffer with a semantic error got %d edit(s)", len(edits))
	}
	if edits := formatDoc(t, "package p\n\ntype A {  x   string }\n"); len(edits) != 1 {
		t.Errorf("clean buffer got %d edit(s), want 1", len(edits))
	}
}
