package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// markCursor removes the cursor mark from src and returns the text and the
// mark's LSP position, its character counted in UTF-16 units.
func markCursor(t *testing.T, src string) (string, protocol.Position) {
	t.Helper()
	i := strings.Index(src, cursorMark)
	if i < 0 {
		t.Fatalf("fixture carries no %q cursor mark", cursorMark)
	}
	head := src[:i]
	return head + src[i+len(cursorMark):], protocol.Position{
		Line:      uint32(strings.Count(head, "\n")),
		Character: uint32(utf16Len(head[strings.LastIndexByte(head, '\n')+1:])),
	}
}

// tokenUnder returns the index and token under pos, or -1.
func tokenUnder(view snapshotView, pos protocol.Position) (int, lexer.Token) {
	c := view.cursorAt(pos)
	if c.at < 0 {
		return -1, lexer.Token{}
	}
	return c.at, view.tokens[c.at]
}

// docAt returns the position params of pos in the document u.
func docAt(u uri.URI, pos protocol.Position) protocol.TextDocumentPositionParams {
	return protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI(u)},
		Position:     pos,
	}
}

// rangeText returns the text of src that r covers.
func rangeText(src string, r protocol.Range) string {
	start := offsetFromLSP(src, r.Start.Line, r.Start.Character)
	end := offsetFromLSP(src, r.End.Line, r.End.Character)
	if start > end {
		return "<inverted range>"
	}
	return src[start:end]
}

// Signature help's active parameter counts the commas before the cursor;
// outside the parens there is no signature.
func TestSignatureHelpActiveParameter(t *testing.T) {
	for _, c := range []struct {
		args   string
		active int // -1: no signature help
	}{
		{"|(1, 80)", -1},
		{"(|1, 80)", 0},
		{"(1|, 80)", 0},
		{"(1,| 80)", 1},
		{"(1, | 80)", 1},
		{"(1, 80)|", -1},
	} {
		t.Run(c.args, func(t *testing.T) {
			src, pos := markCursor(t, "package x\n\ntype T {\n\tid string @length"+c.args+"\n}\n")
			u := uri.New("file:///t.craftgo")
			s := &server{docs: map[uri.URI]string{u: src}}
			res, err := callHandler(t, s, protocol.MethodTextDocumentSignatureHelp, protocol.SignatureHelpParams{TextDocumentPositionParams: docAt(u, pos)})
			if err != nil {
				t.Fatal(err)
			}
			sh, _ := res.(*protocol.SignatureHelp)
			switch {
			case c.active < 0 && sh != nil:
				t.Errorf("signature help outside the parens: %+v", sh)
			case c.active >= 0 && sh == nil:
				t.Error("no signature help inside the argument list")
			case c.active >= 0 && sh.ActiveParameter != uint32(c.active):
				t.Errorf("active parameter = %d, want %d", sh.ActiveParameter, c.active)
			}
		})
	}
}

// Every range counts UTF-16 units, so a token after an astral character on
// its line is covered exactly.
func TestRangesAfterAnAstralCharacter(t *testing.T) {
	src, pos := markCursor(t, "package x\nmiddleware Auth\nservice S {\n\t@doc(\"\U0001F600\") @middlewares(Au|th)\n\t@timeout(10)\n\tget G /g {}\n}\n")
	u := uri.New("file:///t.craftgo")
	s := &server{docs: map[uri.URI]string{u: src}}
	expectAuth := func(what string, r protocol.Range) {
		t.Helper()
		if got := rangeText(src, r); got != "Auth" {
			t.Errorf("%s range covers %q, want \"Auth\"", what, got)
		}
	}

	res, _ := callHandler(t, s, protocol.MethodTextDocumentDocumentHighlight, protocol.DocumentHighlightParams{TextDocumentPositionParams: docAt(u, pos)})
	highlights, _ := res.([]protocol.DocumentHighlight)
	if len(highlights) != 2 {
		t.Fatalf("highlights = %+v, want the declaration and the use", highlights)
	}
	for _, h := range highlights {
		expectAuth("highlight", h.Range)
	}

	res, _ = callHandler(t, s, protocol.MethodTextDocumentReferences, protocol.ReferenceParams{
		TextDocumentPositionParams: docAt(u, pos),
		Context:                    protocol.ReferenceContext{IncludeDeclaration: true},
	})
	refs, _ := res.([]protocol.Location)
	if len(refs) != 2 {
		t.Fatalf("references = %+v, want the declaration and the use", refs)
	}
	for _, l := range refs {
		expectAuth("reference", l.Range)
	}

	res, _ = callHandler(t, s, protocol.MethodTextDocumentPrepareRename, protocol.PrepareRenameParams{TextDocumentPositionParams: docAt(u, pos)})
	if r, ok := res.(*protocol.Range); !ok {
		t.Errorf("prepareRename = %#v, want a range", res)
	} else {
		expectAuth("prepareRename", *r)
	}

	unitSrc, unitPos := markCursor(t, "package x\nservice S {\n\t@doc(\"\U0001F600\") @timeout(10|)\n\tget G /g {}\n}\n")
	s.storeDoc(u, unitSrc)
	res, _ = callHandler(t, s, protocol.MethodTextDocumentCompletion, protocol.CompletionParams{TextDocumentPositionParams: docAt(u, unitPos)})
	list, _ := res.(*protocol.CompletionList)
	if list == nil || len(list.Items) == 0 {
		t.Fatalf("no unit completions: %#v", res)
	}
	for _, it := range list.Items {
		if it.TextEdit == nil || rangeText(unitSrc, it.TextEdit.Range) != "10" {
			t.Errorf("%q edits %+v, want the digits \"10\"", it.Label, it.TextEdit)
		}
	}
}

// On a line holding several fields the cursor's field is the last one that
// starts before it.
func TestCursorFieldOnAOneLineBody(t *testing.T) {
	items := mustCompletionsAtCursor(t, "t.craftgo", "package x\n\nenum Kind { A B }\n\ntype T { s string  k Kind @default(|) }\n")
	expectLabels(t, items, "A", "B")
}

// The typed part of an import path is read up to the cursor in bytes, so a
// non-ASCII folder name narrows the choices exactly.
func TestImportPathPrefixAfterNonASCII(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "craftgo.design.yaml"), "package: example.com/m\noutput:\n  types: ./types\n")
	mustWrite(t, filepath.Join(root, "design", "caféX", "x.craftgo"), "package cafex\n")
	mustWrite(t, filepath.Join(root, "design", "caféY", "y.craftgo"), "package cafey\n")
	src, pos := markCursor(t, "package shop\n\nimport \"design/caféX|\"\n")
	path := filepath.Join(root, "design", "b.craftgo")
	mustWrite(t, path, src)
	u := uri.File(path)
	s := &server{docs: map[uri.URI]string{u: src}}
	res, _ := callHandler(t, s, protocol.MethodTextDocumentCompletion, protocol.CompletionParams{TextDocumentPositionParams: docAt(u, pos)})
	list, _ := res.(*protocol.CompletionList)
	if list == nil {
		t.Fatalf("completion = %#v", res)
	}
	expectLabels(t, list.Items, "design/caféX")
	expectNoLabels(t, list.Items, "design/caféY")
}
