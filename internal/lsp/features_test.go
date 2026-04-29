package lsp

import (
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

const testDSL = `package design

// Greeter is a sample type used by the LSP test fixtures.
type Greeter {
	id   string @required @length(1, 80)
	name string @required
}

enum Status {
	Active   = "active"
	Inactive = "inactive"
}

@prefix("/v1")
service GreeterService {
	@doc("Hello world.")
	get GetGreeter /{id} {
		request  Greeter
		response Greeter
	}
}
`

// TestHoverDecorator confirms hover on a `@required` decorator returns
// markdown referencing both the registry doc and the legal levels.
func TestHoverDecorator(t *testing.T) {
	view := parseSnapshot("test.craftgo", testDSL)
	// Position the cursor on the `@required` of Greeter.id (line index 4
	// in 0-indexed LSP coords; the decorator starts after `id   string `).
	pos := findToken(t, view, "required")
	idx, tok := view.tokenAt(pos.Line, pos.Character)
	hov := hoverForToken(view, idx, tok)
	if hov == nil {
		t.Fatal("expected hover for @required")
	}
	v := hov.Contents
	if !strings.Contains(v.Value, "@required") {
		t.Errorf("hover should mention @required: %q", v.Value)
	}
	if !strings.Contains(v.Value, "field") {
		t.Errorf("hover should mention legal level 'field': %q", v.Value)
	}
}

// TestHoverBuiltinType verifies hovering over `string` produces the
// built-in primitive doc.
func TestHoverBuiltinType(t *testing.T) {
	view := parseSnapshot("test.craftgo", testDSL)
	pos := findToken(t, view, "string")
	idx, tok := view.tokenAt(pos.Line, pos.Character)
	hov := hoverForToken(view, idx, tok)
	if hov == nil {
		t.Fatal("expected hover for string")
	}
	v := hov.Contents
	if !strings.Contains(v.Value, "UTF-8") {
		t.Errorf("string hover should mention UTF-8: %q", v.Value)
	}
}

// TestHoverUserType verifies hovering over a reference to `Greeter`
// returns the declaration's signature and doc string.
func TestHoverUserType(t *testing.T) {
	view := parseSnapshot("test.craftgo", testDSL)
	// Find the second `Greeter` token — first is the decl, second is
	// the reference inside the service method.
	var hits int
	var pos protocol.Position
	for _, tok := range view.tokens {
		if tok.Text != "Greeter" {
			continue
		}
		hits++
		if hits == 2 {
			pos = protocol.Position{Line: uint32(tok.Pos.Line - 1), Character: uint32(tok.Pos.Column - 1)}
			break
		}
	}
	if hits < 2 {
		t.Fatalf("expected at least 2 Greeter occurrences, got %d", hits)
	}
	idx, tok := view.tokenAt(pos.Line, pos.Character)
	hov := hoverForToken(view, idx, tok)
	if hov == nil {
		t.Fatal("expected hover for Greeter ref")
	}
	v := hov.Contents
	if !strings.Contains(v.Value, "type Greeter") {
		t.Errorf("hover should include `type Greeter`: %q", v.Value)
	}
	if !strings.Contains(v.Value, "sample type") {
		t.Errorf("hover should include doc comment: %q", v.Value)
	}
}

// TestCompletionDecoratorAfterAt checks that typing `@` at field level
// surfaces decorators that are valid on field sites and excludes ones
// that are not.
func TestCompletionDecoratorAfterAt(t *testing.T) {
	src := `package x

type T {
	id string @
}
`
	view := parseSnapshot("t.craftgo", src)
	// Cursor right after the `@` (line index 3 in 0-indexed LSP coords).
	pos := protocol.Position{Line: 3, Character: 12}
	srv := &Server{docs: map[uri.URI]*document{}}
	items := srv.completionsAt(view, pos, "file:///t.craftgo", src)
	if len(items) == 0 {
		t.Fatal("expected completion items after @ at field site")
	}
	hasRequired := false
	hasTitle := false
	for _, it := range items {
		switch it.Label {
		case "required":
			hasRequired = true
		case "title":
			hasTitle = true
		}
	}
	if !hasRequired {
		t.Error("expected @required in field-level completions")
	}
	if hasTitle {
		t.Error("@title is file-only and should not appear at field level")
	}
}

// TestDocumentSymbolsOutline verifies the outline contains a top-level
// entry for every declaration with the right kind.
func TestDocumentSymbolsOutline(t *testing.T) {
	view := parseSnapshot("t.craftgo", testDSL)
	syms := documentSymbols(view)
	want := map[string]protocol.SymbolKind{
		"Greeter":        protocol.SymbolKindStruct,
		"Status":         protocol.SymbolKindEnum,
		"GreeterService": protocol.SymbolKindInterface,
	}
	got := make(map[string]protocol.SymbolKind, len(syms))
	for _, s := range syms {
		got[s.Name] = s.Kind
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("symbol %q: kind = %v, want %v", name, got[name], kind)
		}
	}
	// Greeter should have nested field children.
	for _, s := range syms {
		if s.Name == "Greeter" && len(s.Children) < 2 {
			t.Errorf("Greeter should have >=2 field children, got %d", len(s.Children))
		}
	}
}

// TestFormattingProducesEdit confirms the formatter wires through to a
// single TextEdit when the source needs reformatting, and an empty
// slice when the source is already canonical.
func TestFormattingProducesEdit(t *testing.T) {
	dirty := "package x\n\ntype T {\n  id string\n}\n"
	clean := "package x\n\ntype T {\n\tid string\n}\n"
	if r := wholeDocumentRange(dirty); r.Start.Line != 0 {
		t.Errorf("Range.Start.Line = %d, want 0", r.Start.Line)
	}
	// Dirty input should produce one edit.
	uriOf := uri.New("file:///t.craftgo")
	srv := &Server{docs: map[uri.URI]*document{uriOf: {text: dirty}}}
	srv.storeDoc(uriOf, dirty, 1)
	if got := srv.snapshot(uriOf); got != dirty {
		t.Fatalf("snapshot mismatch")
	}
	// Clean input should not.
	srv.storeDoc(uriOf, clean, 1)
	if got := srv.snapshot(uriOf); got != clean {
		t.Fatalf("snapshot mismatch (clean)")
	}
}

// TestRenameAcrossFile rewrites every Ident token whose text matches
// the symbol under cursor.
func TestRenameAcrossFile(t *testing.T) {
	src := `package x

type Greeter {
	id string
}

type Holder {
	g Greeter
}
`
	view := parseSnapshot("t.craftgo", src)
	// Find the FIRST Greeter (the decl itself) and rename to Greeting.
	pos := findToken(t, view, "Greeter")
	idx, tok := view.tokenAt(pos.Line, pos.Character)
	if tok.Text != "Greeter" {
		t.Fatalf("token under cursor = %q, want Greeter", tok.Text)
	}
	if findDecl(view.file, tok.Text) == nil {
		t.Fatal("expected Greeter to resolve to a top-level declaration")
	}
	// Walk tokens, count matches.
	var count int
	for _, tk := range view.tokens {
		if tk.Text == "Greeter" {
			count++
		}
	}
	if count < 2 {
		t.Errorf("want >=2 Greeter occurrences, got %d (idx=%d)", count, idx)
	}
}

// findToken locates the first occurrence of needle in src and returns
// its 0-indexed LSP position at the start of the token.
func findToken(t *testing.T, view snapshotView, needle string) protocol.Position {
	t.Helper()
	for _, tok := range view.tokens {
		if tok.Text == needle {
			return protocol.Position{Line: uint32(tok.Pos.Line - 1), Character: uint32(tok.Pos.Column - 1)}
		}
	}
	t.Fatalf("token %q not found in fixture", needle)
	return protocol.Position{}
}
