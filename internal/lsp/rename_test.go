package lsp

import (
	"errors"
	"testing"

	"go.lsp.dev/jsonrpc2"
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

// Rename takes a new name only when it lexes as one identifier the
// declaration may hold: no reserved word, no built-in, no name another
// declaration holds, and an upper-case one but for a service. A refusal is an
// InvalidParams error.
func TestRenameAcceptsOnlyANameTheDeclarationMayHold(t *testing.T) {
	u := uri.New("file:///t.craftgo")
	src := "package x\n\ntype Greeter { id string }\n\ntype Holder { g Greeter }\n\nmiddleware Auth\n\nservice Api { get A /a {} }\n"
	s := &server{docs: map[uri.URI]string{u: src}}
	rename := func(line, char uint32, newName string) error {
		t.Helper()
		_, err := callHandler(t, s, protocol.MethodTextDocumentRename, protocol.RenameParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI(u)},
				Position:     protocol.Position{Line: line, Character: char},
			},
			NewName: newName,
		})
		return err
	}
	for _, name := range []string{"Welcomer", "X1", "Greeter"} {
		if err := rename(2, 5, name); err != nil {
			t.Errorf("rename to %q refused: %v", name, err)
		}
	}
	for _, name := range []string{"service", "get", "payload", "1x", "a-b", " Welcomer", "", "_x", "x1", "string", "datetime", "Holder", "Auth", "Api"} {
		err := rename(2, 5, name)
		var rpcErr *jsonrpc2.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != jsonrpc2.InvalidParams {
			t.Errorf("rename to %q: error %v, want an InvalidParams error", name, err)
		}
	}
	if err := rename(8, 8, "api"); err != nil {
		t.Errorf("a service takes a lower-case name, refused: %v", err)
	}
	if err := rename(6, 11, "Greeter"); err == nil {
		t.Error("a middleware renamed to a type's name was accepted")
	}
}
