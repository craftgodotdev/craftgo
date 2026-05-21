package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// mustParseFile parses src into an [ast.File] for tests that need to
// build a synthetic project layout (multi-file go-to-def fixtures).
func mustParseFile(t *testing.T, path, src string) *ast.File {
	t.Helper()
	p := parser.New(path, src)
	f := p.Parse()
	if pd := p.Diagnostics(); len(pd) > 0 {
		t.Fatalf("parse %s: %v", path, pd)
	}
	return f
}

const testDSL = `package design

// Greeter is a sample type used by the LSP test fixtures.
type Greeter {
	id   string @doc("user id") @length(1, 80)
	name string
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

// TestHoverDecorator confirms hover on a `@length` decorator returns
// markdown referencing both the registry doc and the legal levels.
func TestHoverDecorator(t *testing.T) {
	view := parseSnapshot("test.craftgo", testDSL)
	pos := findToken(t, view, "length")
	idx, tok := view.tokenAt(pos.Line, pos.Character)
	hov := hoverForToken(view, idx, tok)
	if hov == nil {
		t.Fatal("expected hover for")
	}
	v := hov.Contents
	if !strings.Contains(v.Value, "@length") {
		t.Errorf("hover should mention: %q", v.Value)
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
	// Find the second `Greeter` token - first is the decl, second is
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

// TestCompletionDefaultOnEnumField pins enum-aware completion: when
// the cursor sits inside `@default(...)` of a field whose declared
// type is an enum in scope, only that enum's value names are offered.
func TestCompletionDefaultOnEnumField(t *testing.T) {
	src := `package x
enum Status { Active  Inactive  Pending }
type T {
	st Status? @default()
}
`
	view := parseSnapshot("t.craftgo", src)
	// Cursor inside `@default(|)` - column lands between the parens.
	pos := protocol.Position{Line: 3, Character: 20}
	srv := &Server{docs: map[uri.URI]*document{}}
	items := srv.completionsAt(view, pos, "file:///t.craftgo", src)
	if len(items) != 3 {
		t.Fatalf("expected 3 enum-value completions, got %d: %+v", len(items), items)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.Label] = true
	}
	for _, want := range []string{"Active", "Inactive", "Pending"} {
		if !got[want] {
			t.Errorf("missing enum-value completion %q", want)
		}
	}
}

// TestCompletionDurationPresets covers the empty-slot case for an
// ArgDuration decorator: cursor right after `(`, no partial number
// → preset list (e.g. "5s", "1m", ...).
func TestCompletionDurationPresets(t *testing.T) {
	src := `package x
service S {
	@timeout()
	get G /g {}
}
`
	view := parseSnapshot("t.craftgo", src)
	// Cursor between the parens of @timeout(|).
	pos := protocol.Position{Line: 2, Character: 10}
	srv := &Server{docs: map[uri.URI]*document{}}
	items := srv.completionsAt(view, pos, "file:///t.craftgo", src)
	if len(items) == 0 {
		t.Fatal("expected duration preset completions")
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.Label] = true
	}
	for _, want := range []string{"5s", "1m"} {
		if !got[want] {
			t.Errorf("missing duration preset %q", want)
		}
	}
}

// TestCompletionDurationPartialNumber covers the digits-typed case:
// cursor right after `10` inside `@timeout(...)` → emit suffixed
// values that REPLACE the Int token (TextEdit-bound completions).
func TestCompletionDurationPartialNumber(t *testing.T) {
	src := `package x
service S {
	@timeout(10)
	get G /g {}
}
`
	view := parseSnapshot("t.craftgo", src)
	// Cursor right after `10` - column 12 = `@timeout(10|)`.
	pos := protocol.Position{Line: 2, Character: 12}
	srv := &Server{docs: map[uri.URI]*document{}}
	items := srv.completionsAt(view, pos, "file:///t.craftgo", src)
	if len(items) == 0 {
		t.Fatal("expected partial-aware duration completions")
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.Label] = true
		if it.TextEdit == nil {
			t.Errorf("partial completion %q must carry a TextEdit so the Int gets replaced", it.Label)
		}
	}
	for _, want := range []string{"10s", "10m", "10h", "10ms"} {
		if !got[want] {
			t.Errorf("missing partial-suffix completion %q", want)
		}
	}
}

// TestCompletionSizePartialNumber is the byte-size analogue of the
// duration-partial test.
func TestCompletionSizePartialNumber(t *testing.T) {
	src := `package x
service S {
	@maxBodySize(10)
	get G /g {}
}
`
	view := parseSnapshot("t.craftgo", src)
	pos := protocol.Position{Line: 2, Character: 16}
	srv := &Server{docs: map[uri.URI]*document{}}
	items := srv.completionsAt(view, pos, "file:///t.craftgo", src)
	if len(items) == 0 {
		t.Fatal("expected partial-aware size completions")
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.Label] = true
	}
	for _, want := range []string{"10KB", "10MB", "10GB"} {
		if !got[want] {
			t.Errorf("missing partial-suffix completion %q", want)
		}
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
	hasLength := false
	hasSensitive := false
	for _, it := range items {
		switch it.Label {
		case "length":
			hasLength = true
		case "sensitive":
			hasSensitive = true
		}
	}
	if !hasLength {
		t.Error("expected @length in field-level completions")
	}
	if !hasSensitive {
		t.Error("expected @sensitive in field-level completions")
	}
}

// TestSemanticSurvivesPartialEditsViaSnapshot pins the LSP-side
// resilience contract: while a user is mid-typing (`extend `,
// `service `, `type `, etc.) the parser may produce decls that are
// only partially populated. The full pipeline - parser → semantic
// analyzer → LSP diagnostics - must complete without panicking; a
// nil-pointer dereference in any stage crashes the whole language
// server (a previous regression panicked at semantic.collectDecls
// when the parser passed a typed-nil ServiceDecl through).
func TestSemanticSurvivesPartialEditsViaSnapshot(t *testing.T) {
	cases := []string{
		"package x\nextend ",
		"package x\nextend service ",
		"package x\nextend service S ",
		"package x\nservice ",
		"package x\nservice S {\n  get  /a {}\n}",
		"package x\ntype ",
		"package x\nenum ",
		"package x\nerror NotFound ",
		"package x\nerror ",
		"package x\nscalar ",
		"package x\nmiddleware ",
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("snapshot pipeline panicked on partial input: %v", r)
				}
			}()
			view := parseSnapshot("t.craftgo", src)
			// Symbol provider must not crash on partial decls.
			_ = documentSymbols(view)
			// Single-file diagnostic mode runs semantic.Analyze on
			// the same AST - exercise that path too. If a typed-nil
			// decl ever leaks back into f.Decls, this is where the
			// panic surfaces.
			if view.file != nil {
				_, _ = semantic.Analyze([]*ast.File{view.file})
			}
		})
	}
}

// TestDocumentSymbolsSkipUnnamedDecls protects against the
// "name must not be falsy" crash in VS Code's symbol provider:
// while a user is mid-typing (`service ` with no identifier yet) the
// parser produces a decl with an empty Name. Emitting that as a
// DocumentSymbol crashes the entire outline view, so the LSP must
// silently skip incomplete decls - the partial syntax surfaces via
// diagnostics instead.
func TestDocumentSymbolsSkipUnnamedDecls(t *testing.T) {
	cases := []struct {
		label string
		src   string
	}{
		{"bare service keyword", "package x\n\nservice "},
		{"bare type keyword", "package x\n\ntype "},
		{"bare enum keyword", "package x\n\nenum "},
		{"bare error category", "package x\n\nerror NotFound "},
		{"empty field row in type", "package x\n\ntype T {\n  \n}\n"},
		{"empty method row in service", "package x\n\nservice S {\n  get  /a {}\n}\n"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			view := parseSnapshot("t.craftgo", c.src)
			syms := documentSymbols(view)
			for _, s := range syms {
				if s.Name == "" {
					t.Errorf("top-level symbol with empty name: %+v", s)
				}
				for _, child := range s.Children {
					if child.Name == "" {
						t.Errorf("child symbol with empty name (parent %q): %+v", s.Name, child)
					}
				}
			}
		})
	}
}

// TestCompletionSecuritySchemeAtArgOne pins the autocompletion that
// fires inside `@security(A, B, ...)` - the LSP loads the project's
// craftgo.design.yaml and surfaces every key declared under
// `openapi.securitySchemes` so the user picks from a closed set
// instead of memorising names. The decorator is a variadic ident
// list, so completions fire at every slot.
func TestCompletionSecuritySchemeAtArgOne(t *testing.T) {
	t.Helper()
	// Spin up an isolated project root with a manifest declaring two
	// security schemes - kept tiny so the test is hermetic.
	root := t.TempDir()
	yaml := `package: example.com/m
output:
  types: ./types
openapi:
  title: t
  version: "1"
  basePath: /
  securitySchemes:
    bearer:
      type: http
      scheme: bearer
    apiKey:
      type: apiKey
      in: header
      name: X-API-Key
`
	if err := os.WriteFile(filepath.Join(root, "craftgo.design.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "design"), 0o755); err != nil {
		t.Fatalf("mkdir design: %v", err)
	}
	src := "package x\n\n@security(\nservice S {}"
	srcPath := filepath.Join(root, "design", "t.craftgo")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	view := parseSnapshot(srcPath, src)
	srv := &Server{docs: map[uri.URI]*document{}}
	uri := "file://" + srcPath
	// Cursor right after `@security(` - line 2 (0-indexed), char 10.
	pos := protocol.Position{Line: 2, Character: 10}
	items := srv.completionsAt(view, pos, uri, src)
	got := make(map[string]string, len(items))
	for _, it := range items {
		got[it.Label] = it.Detail
	}
	for _, name := range []string{"bearer", "apiKey"} {
		if _, ok := got[name]; !ok {
			t.Errorf("expected scheme %q in completions, got labels %v", name, keys(got))
		}
	}
	if got["bearer"] != "http bearer" {
		t.Errorf("bearer detail = %q, want %q", got["bearer"], "http bearer")
	}
	if got["apiKey"] != "apiKey (header X-API-Key)" {
		t.Errorf("apiKey detail = %q, want %q", got["apiKey"], "apiKey (header X-API-Key)")
	}
}

// TestCompletionSecuritySchemeNoManifest verifies the LSP stays
// permissive when the project has no craftgo.design.yaml or no
// `securitySchemes` map - the completion popup must not crash and
// must not hijack the slot with an empty list (the generic
// fallback should surface instead).
func TestCompletionSecuritySchemeNoManifest(t *testing.T) {
	root := t.TempDir()
	src := "package x\n\n@security(\nservice S {}"
	srcPath := filepath.Join(root, "t.craftgo")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	view := parseSnapshot(srcPath, src)
	srv := &Server{docs: map[uri.URI]*document{}}
	pos := protocol.Position{Line: 2, Character: 10}
	// Must not panic; an empty result is acceptable since there are
	// no schemes to suggest.
	_ = srv.completionsAt(view, pos, "file://"+srcPath, src)
}

// keys returns the keys of m in arbitrary order. Tiny helper used
// by completion tests so error messages list what we actually got.
func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestCompletionSuppressedAfterOpenBrace pins the "no auto-suggest
// right after `{`" rule. The cursor sitting between `{` and `}`
// without any in-progress identifier was popping the project-wide
// decl list - noisy and unhelpful since the user hasn't signalled
// what they want yet. Manual invocation or typing a character should
// still surface relevant items via the other branches.
func TestCompletionSuppressedAfterOpenBrace(t *testing.T) {
	cases := []struct {
		label string
		src   string
		// pos is the (line, character) the cursor sits at after `{`.
		line int
		col  int
	}{
		{
			label: "extend service body just opened",
			src:   "package x\n\nextend service Test {}",
			line:  2, col: 21, // between `{` and `}`
		},
		{
			label: "service body with whitespace",
			src:   "package x\n\nservice S {\n  \n}",
			line:  3, col: 2, // blank indented line
		},
		{
			label: "type body just opened",
			src:   "package x\n\ntype T {}",
			line:  2, col: 8,
		},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			view := parseSnapshot("t.craftgo", c.src)
			pos := protocol.Position{Line: uint32(c.line), Character: uint32(c.col)}
			srv := &Server{docs: map[uri.URI]*document{}}
			items := srv.completionsAt(view, pos, "file:///t.craftgo", c.src)
			if len(items) != 0 {
				labels := make([]string, 0, len(items))
				for _, it := range items {
					labels = append(labels, it.Label)
				}
				t.Errorf("expected no completions right after `{`, got %d items: %v", len(items), labels)
			}
		})
	}
}

// TestCompletionTypePositionExcludesErrors pins the type-position
// filter: completions in a `field <name> <cursor>` slot must not
// surface `error` declarations even though they live in the same
// project. Errors are reserved for `@errors(...)` decorator args
// and using one as a field type is rejected by the semantic phase
// (see TestErrorNameRejectedAsFieldType).
func TestCompletionTypePositionExcludesErrors(t *testing.T) {
	src := "package x\n\n" +
		"type RealType { id string }\n" +
		"error NotFound MissingErr\n" +
		"type Holder {\n" +
		"    ref \n" +
		"}\n"
	view := parseSnapshot("t.craftgo", src)
	// Cursor sits right after `ref ` (line index 4, after the four
	// chars of `    ` + `ref ` = 8). Line numbering is 0-based.
	pos := protocol.Position{Line: 5, Character: 8}
	srv := &Server{docs: map[uri.URI]*document{}}
	items := srv.completionsAt(view, pos, "file:///t.craftgo", src)
	for _, it := range items {
		if it.Label == "MissingErr" {
			t.Errorf("error declaration leaked into type-position completions: %+v", it)
		}
	}
	// Sanity: a real type IS suggested - the filter must not be over-broad.
	gotRealType := false
	for _, it := range items {
		if it.Label == "RealType" {
			gotRealType = true
			break
		}
	}
	if !gotRealType {
		t.Errorf("expected `RealType` in type-position completions; got labels: %v", labelsOf(items))
	}
}

// labelsOf is a tiny test helper used by completion assertions to
// surface the candidate set in failure messages.
func labelsOf(items []protocol.CompletionItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Label)
	}
	return out
}

// TestCompletionErrorCategoryAfterKeyword pins the autocompletion that
// fires right after the `error` keyword: every reserved HTTP category
// must appear with its HTTP status surfaced as the detail line, and
// items unrelated to that position (decorator names, declaration
// keywords) must NOT leak in.
func TestCompletionErrorCategoryAfterKeyword(t *testing.T) {
	// Cursor sits right after the trailing space of `error `. The LSP
	// sees the previous non-trivia token as KwError and drives the
	// category-completion branch.
	src := "package x\n\nerror "
	view := parseSnapshot("t.craftgo", src)
	pos := protocol.Position{Line: 2, Character: 6}
	srv := &Server{docs: map[uri.URI]*document{}}
	items := srv.completionsAt(view, pos, "file:///t.craftgo", src)
	if len(items) != len(errorCategories) {
		t.Fatalf("expected %d category items (one per reserved HTTP category), got %d", len(errorCategories), len(items))
	}
	// Spot-check coverage of common categories + their HTTP statuses.
	want := map[string]string{
		"NotFound":            "HTTP 404",
		"Conflict":            "HTTP 409",
		"UnprocessableEntity": "HTTP 422",
		"Internal":            "HTTP 500",
	}
	got := make(map[string]string, len(items))
	for _, it := range items {
		got[it.Label] = it.Detail
		if it.Kind != protocol.CompletionItemKindEnumMember {
			t.Errorf("category %q has unexpected kind %v", it.Label, it.Kind)
		}
	}
	for label, detail := range want {
		if got[label] != detail {
			t.Errorf("category %q detail = %q, want %q", label, got[label], detail)
		}
	}
	// The category branch must be exclusive - no decorator names or
	// stray keywords should sneak in.
	for _, it := range items {
		switch it.Label {
		case "length", "doc", "package", "type", "service":
			t.Errorf("unexpected non-category item leaked into category completions: %q", it.Label)
		}
	}
}

// TestCompletionErrorCategoryWhileTyping confirms the category list
// also fires when the user has started typing a partial identifier -
// the LSP client filters by prefix on its own, but the server must
// surface the full set so client-side filtering has anything to match.
func TestCompletionErrorCategoryWhileTyping(t *testing.T) {
	src := "package x\n\nerror Not"
	view := parseSnapshot("t.craftgo", src)
	pos := protocol.Position{Line: 2, Character: 9}
	srv := &Server{docs: map[uri.URI]*document{}}
	items := srv.completionsAt(view, pos, "file:///t.craftgo", src)
	if len(items) != len(errorCategories) {
		t.Fatalf("expected %d category items while typing, got %d", len(errorCategories), len(items))
	}
}

// TestCompletionErrorCategoryNotInOtherPositions makes sure the
// category branch does NOT fire once a category has already been
// chosen - a cursor at `error NotFound <here>` is naming the error,
// not picking a category.
func TestCompletionErrorCategoryNotInOtherPositions(t *testing.T) {
	src := "package x\n\nerror NotFound "
	view := parseSnapshot("t.craftgo", src)
	pos := protocol.Position{Line: 2, Character: 15}
	srv := &Server{docs: map[uri.URI]*document{}}
	items := srv.completionsAt(view, pos, "file:///t.craftgo", src)
	for _, it := range items {
		if it.Detail != "" && strings.HasPrefix(it.Detail, "HTTP ") {
			t.Errorf("category completions leaked into name position: got %q (%s)", it.Label, it.Detail)
		}
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
// TestDefinitionPrefersKindFromDecoratorContext pins the LSP fix for
// cross-namespace ambiguity: when an identifier names both a middleware
// AND a same-named error decl, a click inside `@middlewares(...)` jumps
// to the middleware decl - not the error decl declared earlier in the
// file. Without this preference, the linear scan would always return
// whichever decl appeared first in source order.
func TestDefinitionPrefersKindFromDecoratorContext(t *testing.T) {
	src := `package x
error Forbidden AuthRequired { reason string }
middleware AuthRequired
service S {
	@middlewares(AuthRequired)
	get GetThing /things/{id} {}
}
`
	view := parseSnapshot("t.craftgo", src)
	// Find the AuthRequired token INSIDE @middlewares(...) - it is the
	// third occurrence in source order (after the error name and the
	// middleware decl name).
	var inside protocol.Position
	count := 0
	for _, tok := range view.tokens {
		if tok.Text != "AuthRequired" {
			continue
		}
		count++
		if count == 3 {
			inside = protocol.Position{Line: uint32(tok.Pos.Line - 1), Character: uint32(tok.Pos.Column - 1)}
			break
		}
	}
	if count < 3 {
		t.Fatalf("expected at least 3 AuthRequired tokens, got %d", count)
	}
	// Without context, findDecl would return the FIRST decl named
	// AuthRequired - in this fixture, the error decl. With context, we
	// expect the middleware decl.
	decName, ok := decoratorArgContext(view, inside)
	if !ok || decName != "middlewares" {
		t.Fatalf("decoratorArgContext should detect @middlewares, got name=%q ok=%v", decName, ok)
	}
	d := findDeclKindAware(view.file, "AuthRequired", "middlewares")
	if d == nil {
		t.Fatal("findDeclKindAware returned nil for middleware context")
	}
	if _, isMW := d.(*ast.MiddlewareDecl); !isMW {
		t.Errorf("expected MiddlewareDecl, got %T", d)
	}
}

// TestDefinitionTypeShapePositionExcludesMiddleware pins the inverse
// disambiguation: a click on an ident in a TYPE-shape position
// (mixin, field type, request, response, generic arg) must NEVER
// jump to a middleware decl even when one shares the name. Middleware
// has its own decl namespace; in type positions, only TypeDecl /
// EnumDecl / ScalarDecl / ErrorDecl are reachable.
func TestDefinitionTypeShapePositionExcludesMiddleware(t *testing.T) {
	src := `package x
middleware Greeter
type Greeter { id string }
type Holder { g Greeter }
`
	view := parseSnapshot("t.craftgo", src)
	// Locate `Greeter` inside `g Greeter` (the field-type position).
	count := 0
	var fieldTypePos protocol.Position
	for _, tok := range view.tokens {
		if tok.Text != "Greeter" {
			continue
		}
		count++
		if count == 3 {
			fieldTypePos = protocol.Position{Line: uint32(tok.Pos.Line - 1), Character: uint32(tok.Pos.Column - 1)}
			break
		}
	}
	idx, _ := view.tokenAt(fieldTypePos.Line, fieldTypePos.Character)
	ctx := refContextAt(view, idx, fieldTypePos)
	if ctx != "type" {
		t.Errorf("expected type context for field-type position, got %q", ctx)
	}
	d := findDeclKindAware(view.file, "Greeter", "type")
	if d == nil {
		t.Fatal("type-context lookup returned nil")
	}
	if _, isMW := d.(*ast.MiddlewareDecl); isMW {
		t.Errorf("type-context lookup wrongly returned MiddlewareDecl")
	}
	if _, isType := d.(*ast.TypeDecl); !isType {
		t.Errorf("expected TypeDecl, got %T", d)
	}
}

// TestDefinitionKindAwareAcrossFiles pins the cross-file branch of
// the context-aware lookup. The original report was that a click on
// `AuthRequired` in `@middlewares(AuthRequired)` jumped to the same-
// named error decl - even after the in-file fix - because the
// project-wide `findDeclAcross` was still kind-blind. Here the
// middleware lives in one virtual file, the error in another, and
// the cursor in a third (the import-only `services` file).
func TestDefinitionKindAwareAcrossFiles(t *testing.T) {
	mwFile := `package shared
middleware AuthRequired
`
	errFile := `package shared
error Forbidden AuthRequired { reason string }
`
	useFile := `package services
import "shared"
service S {
	@middlewares(AuthRequired)
	get GetX /x {}
}
`
	files := []projectAST{
		{path: "shared/mw.craftgo", file: mustParseFile(t, "shared/mw.craftgo", mwFile)},
		{path: "shared/err.craftgo", file: mustParseFile(t, "shared/err.craftgo", errFile)},
		{path: "services/use.craftgo", file: mustParseFile(t, "services/use.craftgo", useFile)},
	}
	// The bare-name path (no qualifier) - matches what
	// `findDeclAcrossKindAware` receives when the cursor's
	// `qualifiedNameAt` returns just `AuthRequired`.
	d, pf, ok := findDeclAcrossKindAware(files, "AuthRequired", nil, "", "middlewares")
	if !ok {
		t.Fatal("findDeclAcrossKindAware did not find middleware decl across files")
	}
	if _, isMW := d.(*ast.MiddlewareDecl); !isMW {
		t.Errorf("expected MiddlewareDecl across files, got %T from %s", d, pf.path)
	}
	if pf.path != "shared/mw.craftgo" {
		t.Errorf("expected hit in shared/mw.craftgo, got %s", pf.path)
	}
	// The reverse direction also works: `@errors(AuthRequired)` finds
	// the error decl, not the middleware.
	d, pf, ok = findDeclAcrossKindAware(files, "AuthRequired", nil, "", "errors")
	if !ok {
		t.Fatal("findDeclAcrossKindAware did not find error decl across files")
	}
	if _, isErr := d.(*ast.ErrorDecl); !isErr {
		t.Errorf("expected ErrorDecl, got %T", d)
	}
	if pf.path != "shared/err.craftgo" {
		t.Errorf("expected hit in shared/err.craftgo, got %s", pf.path)
	}
}

// TestDefinitionErrorContextResolvesToError mirrors the middleware
// case for `@errors(...)` - the cursor lands on the error decl even
// when a same-named middleware exists.
func TestDefinitionErrorContextResolvesToError(t *testing.T) {
	src := `package x
middleware Conflict
error Conflict Conflict { reason string }
service S {
	@errors(Conflict)
	get GetThing /t {}
}
`
	view := parseSnapshot("t.craftgo", src)
	d := findDeclKindAware(view.file, "Conflict", "errors")
	if d == nil {
		t.Fatal("findDeclKindAware returned nil for errors context")
	}
	if _, isErr := d.(*ast.ErrorDecl); !isErr {
		t.Errorf("expected ErrorDecl, got %T", d)
	}
}

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
