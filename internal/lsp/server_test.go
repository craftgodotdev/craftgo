package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// An open buffer holding no text is open: completion answers in it.
func TestAnEmptyBufferIsOpen(t *testing.T) {
	u := uri.New("file:///t.craftgo")
	items := completionItems(t, &server{docs: map[uri.URI]string{u: ""}}, u, protocol.Position{})
	expectLabels(t, items, "package", "type", "service")
}

// An open sibling is analysed from its buffer, whatever escaping its URI
// uses, and an empty one counts as empty rather than as its disk copy.
func TestOpenSiblingsAreReadFromTheirBuffers(t *testing.T) {
	design := designProject(t, map[string]string{
		"a+b/types.craftgo": "package app\n\ntype Old { id string }\n",
		"a+b/stale.craftgo": "package app\n\ntype Stale { x Missing }\n",
	})
	dir := filepath.Join(design, "a+b")
	main := filepath.Join(dir, "main.craftgo")
	src := "package app\n\ntype Holder { n New }\n"
	mustWrite(t, main, src)
	escaped := uri.URI(strings.Replace(string(uri.File(filepath.Join(dir, "types.craftgo"))), "+", "%2B", 1))
	s := &server{docs: map[uri.URI]string{
		uri.File(main): src,
		escaped:        "package app\n\ntype New { id string }\n",
		uri.File(filepath.Join(dir, "stale.craftgo")): "",
	}}
	perFile, _ := s.buildProjectDiagnostics(uri.File(main), src)
	for path, diags := range perFile {
		for _, d := range diags {
			t.Errorf("%s: %s", filepath.Base(path), d.Message)
		}
	}
}

// workspace/symbol searches the project of every open document.
func TestWorkspaceSymbolsSpanEveryOpenProject(t *testing.T) {
	alpha := filepath.Join(designProject(t, map[string]string{"a.craftgo": "package a\n\ntype Alpha {}\n"}), "a.craftgo")
	beta := filepath.Join(designProject(t, map[string]string{"b.craftgo": "package b\n\ntype Beta {}\n"}), "b.craftgo")
	s := &server{docs: map[uri.URI]string{
		uri.File(alpha): readFileT(t, alpha),
		uri.File(beta):  readFileT(t, beta),
	}}
	res, err := callHandler(t, s, protocol.MethodWorkspaceSymbol, protocol.WorkspaceSymbolParams{})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, sym := range res.([]protocol.SymbolInformation) {
		names[sym.Name] = true
	}
	if !names["Alpha"] || !names["Beta"] {
		t.Errorf("workspace symbols = %v, want Alpha and Beta", names)
	}
}
