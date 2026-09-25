package lsp

import (
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// Renaming a declaration rewrites it and every use in the buffer.
func TestRenameRewritesEveryUse(t *testing.T) {
	u := uri.New("file:///t.craftgo")
	src := "package x\n\ntype Greeter {\n\tid string\n}\n\ntype Holder {\n\tg Greeter\n}\n"
	s := &server{docs: map[uri.URI]string{u: src}}
	res, err := callHandler(t, s, protocol.MethodTextDocumentRename, protocol.RenameParams{
		TextDocumentPositionParams: docAt(u, protocol.Position{Line: 2, Character: 5}),
		NewName:                    "Welcomer",
	})
	if err != nil {
		t.Fatal(err)
	}
	edit, _ := res.(*protocol.WorkspaceEdit)
	if edit == nil || len(edit.Changes[u]) != 2 {
		t.Fatalf("rename = %+v, want the declaration and the use", res)
	}
	for _, e := range edit.Changes[u] {
		if got := rangeText(src, e.Range); got != "Greeter" || e.NewText != "Welcomer" {
			t.Errorf("edit replaces %q with %q", got, e.NewText)
		}
	}
}

// Rename takes a new name only when it lexes as one identifier; a reserved
// word is refused.
func TestRenameAcceptsOnlyAnIdentifier(t *testing.T) {
	u := uri.New("file:///t.craftgo")
	src := "package x\n\ntype Greeter { id string }\n\ntype Holder { g Greeter }\n"
	s := &server{docs: map[uri.URI]string{u: src}}
	rename := func(newName string) error {
		t.Helper()
		_, err := callHandler(t, s, protocol.MethodTextDocumentRename, protocol.RenameParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI(u)},
				Position:     protocol.Position{Line: 2, Character: 5},
			},
			NewName: newName,
		})
		return err
	}
	for _, name := range []string{"Welcomer", "_x", "x1"} {
		if err := rename(name); err != nil {
			t.Errorf("rename to %q refused: %v", name, err)
		}
	}
	for _, name := range []string{"service", "get", "payload", "1x", "a-b", " Welcomer", ""} {
		if err := rename(name); err == nil {
			t.Errorf("rename to %q accepted", name)
		}
	}
}
