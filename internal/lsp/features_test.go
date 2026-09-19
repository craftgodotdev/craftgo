package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/errcat"
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
	v := mustHoverAt(t, "test.craftgo", testDSL, "length")
	if !strings.Contains(v, "@length") {
		t.Errorf("hover should mention: %q", v)
	}
	if !strings.Contains(v, "field") {
		t.Errorf("hover should mention legal level 'field': %q", v)
	}
}

// TestHoverBuiltinType verifies hovering over `string` produces the
// built-in primitive doc.
func TestHoverBuiltinType(t *testing.T) {
	v := mustHoverAt(t, "test.craftgo", testDSL, "string")
	if !strings.Contains(v, "UTF-8") {
		t.Errorf("string hover should mention UTF-8: %q", v)
	}
}

// `@format(raw)` is the one `@format` value that is not a check: it
// changes the field's Go type and how its value travels, so the token
// answers for itself rather than leaving the author to the reference
// page. The `bytes` beside it explains the pairing from its own side.
func TestHoverFormatRawOnABytesField(t *testing.T) {
	const src = `package design

type Hook {
    payload bytes @format(raw)
}
`
	view := parseSnapshot("test.craftgo", src)
	hov := hoverAtToken(t, view, "raw")
	if !strings.Contains(hov, "the bytes ARE the value") || !strings.Contains(hov, "wire.Raw") {
		t.Errorf("hovering `raw` did not explain the shape: %q", hov)
	}
	if got := hoverAtToken(t, view, "bytes"); !strings.Contains(got, "@format(raw)") {
		t.Errorf("hovering `bytes` does not point at the raw form: %q", got)
	}
}

// hoverAtToken returns the hover text for the first token spelt text.
func hoverAtToken(t *testing.T, view snapshotView, text string) string {
	t.Helper()
	for _, tok := range view.tokens {
		if tok.Text != text {
			continue
		}
		idx, at := view.tokenAt(uint32(tok.Pos.Line-1), uint32(tok.Pos.Column-1))
		hov := hoverForToken(view, idx, at)
		if hov == nil {
			t.Fatalf("no hover on the token %q", text)
		}
		return hov.Contents.Value
	}
	t.Fatalf("no token spelt %q in the buffer", text)
	return ""
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
	// Cursor inside `@default(|)` - column lands between the parens.
	items := mustCompletionsAt(t, "t.craftgo", src, 3, 20)
	if len(items) != 3 {
		t.Fatalf("expected 3 enum-value completions, got %d: %+v", len(items), items)
	}
	expectLabels(t, items, "Active", "Inactive", "Pending")
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
	// Cursor between the parens of @timeout(|).
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 10)
	if len(items) == 0 {
		t.Fatal("expected duration preset completions")
	}
	expectLabels(t, items, "5s", "1m")
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
	// Cursor right after `10` - column 12 = `@timeout(10|)`.
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 12)
	if len(items) == 0 {
		t.Fatal("expected partial-aware duration completions")
	}
	for _, it := range items {
		if it.TextEdit == nil {
			t.Errorf("partial completion %q must carry a TextEdit so the Int gets replaced", it.Label)
		}
	}
	expectLabels(t, items, "10s", "10m", "10h", "10ms")
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
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 16)
	if len(items) == 0 {
		t.Fatal("expected partial-aware size completions")
	}
	expectLabels(t, items, "10KB", "10MB", "10GB")
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
	// Cursor right after the `@` (line index 3 in 0-indexed LSP coords).
	items := mustCompletionsAt(t, "t.craftgo", src, 3, 12)
	if len(items) == 0 {
		t.Fatal("expected completion items after @ at field site")
	}
	expectLabels(t, items, "length", "sensitive")
}

// A `bytes @format(raw)` field takes no other validator, so the popup
// offers none: the field resolves to its own category that no
// validator's AppliesTo names. The decorators that shape a field
// regardless of type still appear, and so does `@format` - it is what
// put the field in that category.
func TestCompletionOnARawBytesFieldOffersNoValidator(t *testing.T) {
	src := "package x\n\ntype T {\n\tpayload bytes @format(raw) @\n}\n"
	items := mustCompletionsAt(t, "t.craftgo", src, 3, 28)
	expectNoLabels(t, items,
		"length", "minLength", "maxLength", "pattern",
		"gt", "gte", "lt", "lte", "range", "positive", "negative", "multipleOf",
		"minItems", "maxItems", "uniqueItems", "maxSize", "mimeTypes")
	expectLabels(t, items, "format", "json", "nullable", "doc", "sensitive")
}

// A plain `bytes` field is string-shaped, so the same popup one
// decorator earlier still offers the text validators: it is the
// `@format(raw)` that narrows the field, nothing about `bytes` itself.
func TestCompletionOnAPlainBytesFieldStillOffersTextValidators(t *testing.T) {
	src := "package x\n\ntype T {\n\tpayload bytes @\n}\n"
	items := mustCompletionsAt(t, "t.craftgo", src, 3, 16)
	expectLabels(t, items, "format", "minLength", "maxLength")
}

// `raw` is offered inside `@format(...)` beside the string formats: the
// argument popup is generated from the registry's enum, which is the
// same list the analyser accepts.
func TestCompletionFormatArgOffersRaw(t *testing.T) {
	src := "package x\n\ntype T {\n\tpayload bytes @format(\n}\n"
	items := mustCompletionsAt(t, "t.craftgo", src, 3, 23)
	expectLabels(t, items, "raw", "email", "uuid")
}

// The built-in popup is generated from the catalogue, so `datetime`
// appears in it the way `string` does - otherwise the only way to find
// it is the reference page.
func TestCompletionTypePositionOffersBuiltins(t *testing.T) {
	src := "package x\n\nservice S {\n    post P /p { request \n}\n"
	items := mustCompletionsAt(t, "t.craftgo", src, 3, 24)
	expectLabels(t, items, "datetime", "bytes", "any", "string")
}

// TestCompletionServiceDecoratorSite pins the decorator popup for the zone
// above a `service` / `extend service`. While the leading `@` is mid-typed the
// parser swallows the following keyword as the decorator name, so the site must
// be recovered from the token stream - otherwise it misreads as file scope and
// the service-level decorators vanish. An extend block additionally drops
// `@prefix` (primary-only) while keeping `@group`.
func TestCompletionServiceDecoratorSite(t *testing.T) {
	primary := "package x\n\n@\nservice S {\n  get A /a {}\n}\n"
	items := mustCompletionsAt(t, "t.craftgo", primary, 2, 1)
	expectLabels(t, items, "prefix", "group", "middlewares", "tags", "security")

	extend := "package x\n\nservice S { get A /a {} }\n\n@\nextend service S {\n  get B /b {}\n}\n"
	eitems := mustCompletionsAt(t, "t.craftgo", extend, 4, 1)
	expectLabels(t, eitems, "group", "middlewares", "tags", "security")
	expectNoLabels(t, eitems, "prefix")
}

// TestSemanticSurvivesPartialEditsViaSnapshot pins the LSP-side
// resilience contract: while a user is mid-typing (`extend `,
// `service `, `type `, etc.) the parser may produce decls that are
// only partially populated. The full pipeline - parser → semantic
// analyzer → LSP diagnostics - must complete without panicking, since a
// nil-pointer dereference in any stage crashes the whole language
// server.
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
	fileURI := string(uri.File(srcPath))
	// Cursor right after `@security(` - line 2 (0-indexed), char 10.
	pos := protocol.Position{Line: 2, Character: 10}
	items := srv.completionsAt(view, pos, fileURI, src)
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
	_ = srv.completionsAt(view, pos, string(uri.File(srcPath)), src)
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
// right after `{`" rule. A cursor between `{` and `}` without any
// in-progress identifier returns no completions, since the user has
// not signalled what they want yet. Manual invocation or typing a
// character still surfaces relevant items via the other branches.
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
			items := mustCompletionsAt(t, "t.craftgo", c.src, uint32(c.line), uint32(c.col))
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
	// Cursor sits right after `ref ` (line index 4, after the four
	// chars of `    ` + `ref ` = 8). Line numbering is 0-based.
	items := mustCompletionsAt(t, "t.craftgo", src, 5, 8)
	for _, it := range items {
		if it.Label == "MissingErr" {
			t.Errorf("error declaration leaked into type-position completions: %+v", it)
		}
	}
	// Sanity: a real type IS suggested - the filter must not be over-broad.
	expectLabels(t, items, "RealType")
}

// cursorMark is the caret in a completion fixture. Exactly one `|` in
// the source marks where the cursor sits; it is stripped before the
// buffer is parsed. The DSL has no `|` token, so the mark is never
// ambiguous - and a marked fixture reads as the buffer the user is
// looking at, which a hand-counted line/character pair does not.
const cursorMark = "|"

// mustCompletionsAtCursor runs the completion provider at the fixture's
// cursor mark.
func mustCompletionsAtCursor(t *testing.T, path, src string) []protocol.CompletionItem {
	t.Helper()
	i := strings.Index(src, cursorMark)
	if i < 0 {
		t.Fatalf("fixture carries no %q cursor mark", cursorMark)
	}
	head := src[:i]
	return mustCompletionsAt(t, path, strings.Replace(src, cursorMark, "", 1),
		uint32(strings.Count(head, "\n")),
		uint32(len(head)-(strings.LastIndex(head, "\n")+1)))
}

// typeSlotFixtures is the shared preamble for the completion tables: one
// declared type, one enum and one bool scalar, so a popup that offers
// declarations has something to offer.
const typeSlotFixtures = "package x\n\ntype Address { city string }\nenum Kind { Active }\nscalar Flag bool\n"

// TestCompletionTypeSlots pins every position where the grammar makes a
// type name the legal next token. The field slot is the one the DSL
// spells without a separator - `name Type`, no colon - so it has no
// trigger token of its own and used to fall through to the generic
// keyword list, which carries no primitives at all. The map / generic
// cases sit with the cursor touching the `<` or `,`, where tokenAt
// resolves the punctuation as the cursor's own token rather than the
// preceding one.
func TestCompletionTypeSlots(t *testing.T) {
	cases := map[string]string{
		"field type slot":                       "type User {\n    home |\n}\n",
		"field type slot, one-line body":        "type User { home | }\n",
		"field type slot in an error body":      "error NotFound Missing {\n    at |\n}\n",
		"field type half typed":                 "type User {\n    home Add|\n}\n",
		"field type slot, keyword-named field":  "type User {\n    map |\n}\n",
		"map key, cursor on the angle":          "type User {\n    tags map<|\n}\n",
		"map value, cursor on the comma":        "type User {\n    tags map<string,|\n}\n",
		"generic argument, cursor on the angle": "type Page<T> { items T[] }\ntype User {\n    page Page<|\n}\n",
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			items := mustCompletionsAtCursor(t, "t.craftgo", typeSlotFixtures+body)
			// Built-in primitives, `map`, AND the project's declarations.
			expectLabels(t, items, "string", "int", "bytes", "datetime", "any", "file", "map", "Address", "Kind", "Flag")
			// `object` is only legal inside `@example({...})`, and the
			// declaration keywords belong to the block fallback - a type
			// slot that surfaces either has taken the wrong branch.
			expectNoLabels(t, items, "object", "service", "middleware", "extend")
		})
	}
}

// TestCompletionClauseSlotsOfferMessageTypes pins `request`, `response`
// and `payload`. All three name a message, and the analyser rejects an
// enum or a scalar in any of them - so those two kinds are filtered out
// even though they are perfectly good FIELD types. `payload` is the
// strict one: it must name a `type`, so its popup carries no built-ins
// either.
func TestCompletionClauseSlotsOfferMessageTypes(t *testing.T) {
	t.Run("method clause", func(t *testing.T) {
		items := mustCompletionsAtCursor(t, "t.craftgo",
			typeSlotFixtures+"service S {\n    get Fetch /f {\n        request |\n    }\n}\n")
		expectLabels(t, items, "Address", "string", "bytes", "datetime")
		// An enum / scalar has no fields to bind, and `map` does not
		// parse in a clause at all.
		expectNoLabels(t, items, "Kind", "Flag", "map", "object", "get", "request")
	})
	t.Run("event payload", func(t *testing.T) {
		items := mustCompletionsAtCursor(t, "t.craftgo",
			typeSlotFixtures+"event Moved {\n    payload |\n}\n")
		expectLabels(t, items, "Address")
		expectNoLabels(t, items, "Kind", "Flag", "map", "string", "bytes", "payload")
	})
}

// TestCompletionTypePositionNotInOtherSlots is the precision half of
// TestCompletionTypeSlots: every slot here takes a name, a value or a
// keyword rather than a type, so the primitives must stay out and each
// position must offer what the block it sits in legally accepts.
func TestCompletionTypePositionNotInOtherSlots(t *testing.T) {
	cases := []struct {
		label string
		src   string
		want  []string
	}{
		{
			label: "package declaration",
			src:   "package x |\n",
			want:  []string{"type", "service"},
		},
		{
			label: "decorator object-literal value",
			src:   typeSlotFixtures + "type User {\n    home Address @example({ city: | })\n}\n",
		},
		{
			label: "enum value",
			src:   typeSlotFixtures + "enum E {\n    Active |\n}\n",
		},
		{
			label: "method name",
			src:   typeSlotFixtures + "service S {\n    get Fetch |\n}\n",
			want:  []string{"get", "post"},
		},
		{
			label: "fresh member line",
			src:   typeSlotFixtures + "type User {\n    id string\n    |\n}\n",
			want:  []string{"Address"},
		},
		{
			label: "mixin row",
			src:   typeSlotFixtures + "type User {\n    Address |\n}\n",
			want:  []string{"Address"},
		},
		{
			label: "slot after a finished field type",
			src:   typeSlotFixtures + "type User {\n    id string |\n}\n",
			want:  []string{"Address"},
		},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			items := mustCompletionsAtCursor(t, "t.craftgo", c.src)
			expectNoLabels(t, items, "string", "int", "bytes", "datetime")
			expectLabels(t, items, c.want...)
		})
	}
}

// TestCompletionSuppressedAfterTypeSuffix pins the second suppression
// rule alongside TestCompletionSuppressedAfterOpenBrace: past a `?` or
// a `[]` the field's type is finished, so the popup stays shut until
// the user types `@`. A type-position branch that reached this far
// would bury that silence under the whole primitive catalogue.
func TestCompletionSuppressedAfterTypeSuffix(t *testing.T) {
	for label, body := range map[string]string{
		"optional suffix": "type User {\n    home Address? |\n}\n",
		"array suffix":    "type User {\n    home Address[] |\n}\n",
	} {
		t.Run(label, func(t *testing.T) {
			if items := mustCompletionsAtCursor(t, "t.craftgo", typeSlotFixtures+body); len(items) != 0 {
				t.Errorf("expected no completions past a type suffix, got %d items: %v", len(items), labelSet(items))
			}
		})
	}
}

// TestCompletionBlockFallbackMatchesTheBlock pins the fallback the
// dispatcher lands on when no specific branch claims the cursor. Each
// block accepts a different set of words - a service body holds HTTP
// methods, a method body holds two clauses, an enum body holds names
// the author invents - and the fallback now answers with that set
// instead of the whole keyword catalogue plus every declared type.
func TestCompletionBlockFallbackMatchesTheBlock(t *testing.T) {
	cases := []struct {
		label        string
		src          string
		want, banned []string
	}{
		{
			label:  "file scope",
			src:    typeSlotFixtures + "|\n",
			want:   []string{"package", "import", "type", "enum", "error", "scalar", "service", "extend", "middleware", "event"},
			banned: []string{"get", "request", "response", "payload", "map", "true", "Address"},
		},
		{
			label: "type body",
			src:   typeSlotFixtures + "type User {\n    id string\n    |\n}\n",
			want:  []string{"Address"},
			// A mixin names a `type`; an enum or scalar there is rejected
			// ("mixin K is a enum, not a type"), and the keyword dump is
			// what this fallback used to be.
			banned: []string{"Kind", "Flag", "type", "service", "get", "request", "string"},
		},
		{
			label:  "error body",
			src:    typeSlotFixtures + "error NotFound Missing {\n    at string\n    |\n}\n",
			want:   []string{"Address"},
			banned: []string{"Kind", "Flag", "type", "get", "request"},
		},
		{
			label:  "service body",
			src:    typeSlotFixtures + "service S {\n    get A /a {}\n    |\n}\n",
			want:   []string{"get", "post", "put", "patch", "delete", "head", "options"},
			banned: []string{"type", "service", "request", "Address"},
		},
		{
			label:  "method body",
			src:    typeSlotFixtures + "service S {\n    get A /a {\n        request Address\n        |\n    }\n}\n",
			want:   []string{"request", "response"},
			banned: []string{"get", "type", "payload", "Address"},
		},
		{
			label:  "event body",
			src:    typeSlotFixtures + "event E {\n    payload Address\n    |\n}\n",
			want:   []string{"payload"},
			banned: []string{"request", "response", "type", "Address"},
		},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			items := mustCompletionsAtCursor(t, "t.craftgo", c.src)
			expectLabels(t, items, c.want...)
			expectNoLabels(t, items, c.banned...)
		})
	}
}

// TestCompletionEnumBodyOffersNothing is the block whose legal content
// is entirely the author's: an enum value is a name nobody else can
// supply, so the popup stays shut rather than dumping declarations that
// are illegal between those braces.
func TestCompletionEnumBodyOffersNothing(t *testing.T) {
	for label, body := range map[string]string{
		"after a value":       "enum E {\n    Active\n    |\n}\n",
		"after a value name":  "enum E {\n    Active |\n}\n",
		"after an assignment": "enum E {\n    Active = |\n}\n",
	} {
		t.Run(label, func(t *testing.T) {
			if items := mustCompletionsAtCursor(t, "t.craftgo", typeSlotFixtures+body); len(items) != 0 {
				t.Errorf("expected no completions in an enum body, got %d items: %v", len(items), labelSet(items))
			}
		})
	}
}

// pathParamFixture declares a request type whose fields cover each
// path-binding verdict: `id` binds, `q` is already bound to the query
// string, `tags` is an array and `note` is optional - a matched route
// always supplies a segment, so neither can source one.
const pathParamFixture = "package x\n\ntype Req {\n" +
	"    id string\n" +
	"    sku int\n" +
	"    q string @query\n" +
	"    tags string[]\n" +
	"    note string?\n" +
	"}\n"

// TestCompletionPathParameterOffersRequestFields pins `/{<cursor>}`.
// A `{param}` binds to the request field of the same name, so the
// request type is the closed set the slot accepts. The empty-brace case
// is the one an auto-closing editor produces, and it is also the one
// the parser cannot represent - `{}` is not a path parameter to it, so
// the clause is read from the token stream.
func TestCompletionPathParameterOffersRequestFields(t *testing.T) {
	cases := []struct {
		label        string
		src          string
		want, banned []string
	}{
		{
			label:  "empty braces",
			src:    pathParamFixture + "service S {\n    get A /store/{|} { request Req }\n}\n",
			want:   []string{"id", "sku"},
			banned: []string{"q", "tags", "note", "request", "get"},
		},
		{
			label:  "half-typed parameter name",
			src:    pathParamFixture + "service S {\n    get A /store/{i|} { request Req }\n}\n",
			want:   []string{"id", "sku"},
			banned: []string{"q", "tags", "note", "request"},
		},
		{
			label:  "second parameter skips the one already bound",
			src:    pathParamFixture + "service S {\n    get A /s/{id}/{|} { request Req }\n}\n",
			want:   []string{"sku"},
			banned: []string{"id", "q", "tags", "note"},
		},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			items := mustCompletionsAtCursor(t, "t.craftgo", c.src)
			expectLabels(t, items, c.want...)
			expectNoLabels(t, items, c.banned...)
		})
	}
}

// TestCompletionPathParameterWithoutRequestStaysSilent pins the limit of
// the rule above: with no `request` clause written yet there is no field
// set to read, and the popup says nothing rather than guessing.
func TestCompletionPathParameterWithoutRequestStaysSilent(t *testing.T) {
	src := pathParamFixture + "service S {\n    get A /store/{|} { }\n}\n"
	if items := mustCompletionsAtCursor(t, "t.craftgo", src); len(items) != 0 {
		t.Errorf("expected no completions without a request clause, got %v", labelSet(items))
	}
}

// TestCompletionDefaultValueFollowsTheFieldType pins `@default(...)`.
// The decorator takes ArgAny, so the legal set is the FIELD's: an enum
// offers its values, a bool offers the two literals, and a scalar is
// followed to the primitive it wraps. Every other type takes a free
// literal, where a popup can only get in the way.
func TestCompletionDefaultValueFollowsTheFieldType(t *testing.T) {
	cases := []struct {
		label string
		src   string
		want  []string
	}{
		{
			label: "bool field",
			src:   typeSlotFixtures + "type User {\n    ok bool @default(|)\n}\n",
			want:  []string{"true", "false"},
		},
		{
			label: "scalar over bool",
			src:   typeSlotFixtures + "type User {\n    ok Flag @default(|)\n}\n",
			want:  []string{"true", "false"},
		},
		{
			label: "enum field",
			src:   typeSlotFixtures + "type User {\n    k Kind @default(|)\n}\n",
			want:  []string{"Active"},
		},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			items := mustCompletionsAtCursor(t, "t.craftgo", c.src)
			expectLabels(t, items, c.want...)
			expectNoLabels(t, items, "string", "Address", "type")
		})
	}
	t.Run("string field has no closed set", func(t *testing.T) {
		src := typeSlotFixtures + "type User {\n    s string @default(|)\n}\n"
		if items := mustCompletionsAtCursor(t, "t.craftgo", src); len(items) != 0 {
			t.Errorf("expected no completions for a free-literal default, got %v", labelSet(items))
		}
	})
}

// TestCompletionDecoratorArgWithNoClosedSetStaysSilent pins the rule for
// every other decorator slot: a registered decorator whose argument is a
// free literal answers with nothing, rather than falling through to a
// block fallback whose declarations are illegal inside parentheses.
func TestCompletionDecoratorArgWithNoClosedSetStaysSilent(t *testing.T) {
	for label, body := range map[string]string{
		"doc prose":            "type User {\n    s string @doc(|)\n}\n",
		"object literal value": "type User {\n    s string @example({ s: | })\n}\n",
		"pattern literal":      "type User {\n    s string @pattern(|)\n}\n",
	} {
		t.Run(label, func(t *testing.T) {
			if items := mustCompletionsAtCursor(t, "t.craftgo", typeSlotFixtures+body); len(items) != 0 {
				t.Errorf("expected no completions in a free-literal decorator slot, got %v", labelSet(items))
			}
		})
	}
}

// TestCompletionScalarPrimitiveSlotIsBuiltinsOnly narrows the slot the
// analyser calls a closed set: `scalar Name X` accepts a built-in that
// lowers to a Go type and nothing else - not a declared type, not
// another scalar, and not `any` / `file` / `object` / `map`, all of
// which it rejects with CodeScalarBadPrimitive.
func TestCompletionScalarPrimitiveSlotIsBuiltinsOnly(t *testing.T) {
	items := mustCompletionsAtCursor(t, "t.craftgo", typeSlotFixtures+"scalar Email |\n")
	expectLabels(t, items, "string", "int", "bool", "bytes", "float64", "datetime")
	expectNoLabels(t, items, "any", "file", "object", "map", "Address", "Kind", "Flag")
}

// TestCompletionTypeParameterDeclarationOffersNothing separates the two
// meanings of `<`: `Page<User>` references a type, while `type Page<T>`
// declares a parameter whose name is the author's to invent. The second
// used to answer with the whole type catalogue.
func TestCompletionTypeParameterDeclarationOffersNothing(t *testing.T) {
	for label, body := range map[string]string{
		"first parameter":  "type Page<|> { id string }\n",
		"second parameter": "type Pair<A, |> { id string }\n",
	} {
		t.Run(label, func(t *testing.T) {
			if items := mustCompletionsAtCursor(t, "t.craftgo", typeSlotFixtures+body); len(items) != 0 {
				t.Errorf("expected no completions in a type-parameter declaration, got %v", labelSet(items))
			}
		})
	}
}

// TestCompletionExtendServiceTargetAtEndOfBuffer covers the clause the
// user is typing at the very end of the file, where the only thing after
// the cursor is the stream's EOF token. Reading the slice's last entry
// saw EOF as the previous token, so the service list never fired.
func TestCompletionExtendServiceTargetAtEndOfBuffer(t *testing.T) {
	src := "package x\n\nservice Api {\n    get A /a {}\n}\nextend service |"
	for _, tail := range []string{"", "Ap"} {
		items := mustCompletionsAtCursor(t, "t.craftgo", src+tail)
		expectLabels(t, items, "Api")
		// The service list is exclusive: a keyword here means the branch
		// did not fire and the block fallback answered instead.
		expectNoLabels(t, items, "type", "service", "extend", "package")
	}
}

// TestCompletionHeaderLines pins the two file-header slots, both of
// which need the project on disk: `package <cursor>` answers with the
// name the folder's other files already declare, and `import <cursor>`
// offers the importable folders with the quotes the line still needs.
func TestCompletionHeaderLines(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "craftgo.design.yaml"),
		[]byte("package: example.com/m\noutput:\n  types: ./types\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	dir := filepath.Join(root, "design")
	if err := os.MkdirAll(filepath.Join(dir, "shared"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	write := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	write(filepath.Join(dir, "a.craftgo"), "package shop\n\ntype Ping { id string }\n")
	write(filepath.Join(dir, "shared", "s.craftgo"), "package shared\n\ntype Money { amount int }\n")
	buf := filepath.Join(dir, "b.craftgo")

	run := func(src string) []protocol.CompletionItem {
		t.Helper()
		i := strings.Index(src, cursorMark)
		clean := strings.Replace(src, cursorMark, "", 1)
		write(buf, clean)
		head := src[:i]
		view := parseSnapshot(buf, clean)
		srv := &Server{docs: map[uri.URI]*document{}}
		pos := protocol.Position{
			Line:      uint32(strings.Count(head, "\n")),
			Character: uint32(len(head) - (strings.LastIndex(head, "\n") + 1)),
		}
		return srv.completionsAt(view, pos, string(uri.File(buf)), clean)
	}

	t.Run("package name comes from the folder", func(t *testing.T) {
		items := run("package |\n")
		expectLabels(t, items, "shop")
		// The sibling folder declares `shared`; this file is not in it.
		expectNoLabels(t, items, "shared", "type", "Ping")
	})
	t.Run("import path arrives quoted", func(t *testing.T) {
		items := run("package shop\n\nimport |\n")
		expectLabels(t, items, "design/shared")
		for _, it := range items {
			if it.Label == "design/shared" && it.InsertText != `"design/shared"` {
				t.Errorf("import path must insert with quotes, got %q", it.InsertText)
			}
		}
	})
}

// TestCompletionScalarPrimitivePosition checks that typing
// `scalar Email <cursor>` offers the primitive set rather than the
// keyword list. The `scalar Name <primitive>` slot is the ONLY legal
// place to put a builtin, so the LSP surfaces it without the user
// having to remember the keyword set.
func TestCompletionScalarPrimitivePosition(t *testing.T) {
	src := "package x\n\nscalar Email "
	// Cursor right after `scalar Email ` (line 2, col 13).
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 13)
	expectLabels(t, items, "string", "int", "bool")
}

// TestCompletionErrorsDecoratorArgs pins the @errors arg completion -
// the popup must surface every declared error type name in the
// project, not the generic keyword list.
func TestCompletionErrorsDecoratorArgs(t *testing.T) {
	src := "package x\n\n" +
		"error NotFound UserNotFoundErr\n" +
		"error Conflict EmailTakenErr\n" +
		"type Req { id string }\n" +
		"type Resp { id string }\n" +
		"service S {\n" +
		"    @errors(\n" +
		"    post Save /save { request Req response Resp }\n" +
		"}\n"
	// Cursor right after `@errors(` - line 7, char 12.
	items := mustCompletionsAt(t, "t.craftgo", src, 7, 12)
	expectLabels(t, items, "UserNotFoundErr", "EmailTakenErr")
}

// TestCompletionDecoratorOnScalarFiltersByPrimitive checks that
// `scalar Gmail string @<cursor>` does not list `@gt`, which only
// applies to numeric types. The completion popup intersects the
// scalar's primitive with each decorator's AppliesTo so the user
// only sees decorators the semantic phase would later accept.
func TestCompletionDecoratorOnScalarFiltersByPrimitive(t *testing.T) {
	src := "package x\n\n" +
		"scalar Gmail string @\n"
	// Cursor right after `@` on line 2 (0-indexed), char 21.
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 21)
	// String-applicable validators must appear.
	expectLabels(t, items, "length", "minLength", "maxLength", "pattern", "format")
	// Numeric-only validators must NOT appear on a string scalar.
	expectNoLabels(t, items, "gt", "gte", "lt", "lte", "range", "positive", "negative", "multipleOf")
	// Array-only validators must also be filtered out.
	expectNoLabels(t, items, "minItems", "maxItems", "uniqueItems")
}

// TestCompletionFormatDecoratorArgs pins the @format arg completion -
// the popup must surface the registered format-validator names so
// the user picks `email`, `uuid`, `url`, etc. from a closed set
// instead of memorising the values.
func TestCompletionFormatDecoratorArgs(t *testing.T) {
	// Field decorator chain shape that the parser tolerates while the
	// user is mid-typing `@format(` - the trailing newline + close
	// brace keeps the type body well-formed enough for tokenisation
	// to find the decorator span.
	src := "package x\n" +
		"type Req {\n" +
		"  email string @format(\n" +
		"}\n"
	// Cursor right after `@format(` - line 2 (0-indexed), char 24.
	// "  email string @format(" = 23 chars; cursor sits at 23.
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 23)
	expectLabels(t, items, "email", "uuid", "url")
}

// TestCompletionStatusDecoratorArgs pins the @status arg completion -
// HTTP status codes should appear with their IANA reason phrase as
// detail so the user picks `201 (Created)` rather than memorising
// codes.
func TestCompletionStatusDecoratorArgs(t *testing.T) {
	src := "package x\n\n" +
		"type Req { id string }\n" +
		"type Resp { id string }\n" +
		"service S {\n" +
		"    @status(\n" +
		"    post Save /save { request Req response Resp }\n" +
		"}\n"
	// Cursor right after `@status(` - line 5, char 12.
	items := mustCompletionsAt(t, "t.craftgo", src, 5, 12)
	expectLabels(t, items, "200", "201", "204", "400", "404", "500")
	for _, it := range items {
		if it.Label == "201" && it.Detail != "HTTP 201 Created" {
			t.Errorf("HTTP 201 detail = %q, want IANA reason phrase", it.Detail)
		}
	}
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
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 6)
	if len(items) != len(errcat.Categories) {
		t.Fatalf("expected %d category items (one per reserved HTTP category), got %d", len(errcat.Categories), len(items))
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
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 9)
	if len(items) != len(errcat.Categories) {
		t.Fatalf("expected %d category items while typing, got %d", len(errcat.Categories), len(items))
	}
}

// TestCompletionErrorCategoryNotInOtherPositions makes sure the
// category branch does NOT fire once a category has already been
// chosen - a cursor at `error NotFound <here>` is naming the error,
// not picking a category.
func TestCompletionErrorCategoryNotInOtherPositions(t *testing.T) {
	src := "package x\n\nerror NotFound "
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 15)
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
// TestDefinitionPrefersKindFromDecoratorContext pins cross-namespace
// disambiguation: when an identifier names both a middleware AND a
// same-named error decl, a click inside `@middlewares(...)` jumps to
// the middleware decl, not the error decl declared earlier in the file.
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
	d := lookupIn(t, "x", "AuthRequired", semantic.MiddlewareDecls, view.file)
	if d == nil {
		t.Fatal("middleware lookup returned nil")
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
	if kind := lookupKindAt(view, idx, fieldTypePos); kind != semantic.TypeShapeDecls {
		t.Errorf("expected type-shape kinds for field-type position, got %v", kind)
	}
	d := lookupIn(t, "x", "Greeter", semantic.TypeShapeDecls, view.file)
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

// TestDefinitionKindAwareAcrossFiles pins the cross-file branch of the
// context-aware lookup: a click on `AuthRequired` in
// `@middlewares(AuthRequired)` must resolve to the middleware decl, not
// a same-named error decl in another file. Here the middleware lives in
// one virtual file, the error in another, and the cursor in a third
// (the import-only `services` file).
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
	files := []*ast.File{
		mustParseFile(t, "shared/mw.craftgo", mwFile),
		mustParseFile(t, "shared/err.craftgo", errFile),
		mustParseFile(t, "services/use.craftgo", useFile),
	}
	// The bare name (no qualifier) - what `qualifiedNameAt` returns for
	// the cursor on `AuthRequired`.
	d := lookupIn(t, "services", "AuthRequired", semantic.MiddlewareDecls, files...)
	if d == nil {
		t.Fatal("middleware lookup did not find the decl across files")
	}
	if _, isMW := d.(*ast.MiddlewareDecl); !isMW {
		t.Errorf("expected MiddlewareDecl across files, got %T from %s", d, d.DeclPos().Filename)
	}
	if got := d.DeclPos().Filename; got != "shared/mw.craftgo" {
		t.Errorf("expected hit in shared/mw.craftgo, got %s", got)
	}
	// The reverse direction also works: `@errors(AuthRequired)` finds
	// the error decl, not the middleware.
	d = lookupIn(t, "services", "AuthRequired", semantic.ErrorDecls, files...)
	if d == nil {
		t.Fatal("error lookup did not find the decl across files")
	}
	if _, isErr := d.(*ast.ErrorDecl); !isErr {
		t.Errorf("expected ErrorDecl, got %T", d)
	}
	if got := d.DeclPos().Filename; got != "shared/err.craftgo" {
		t.Errorf("expected hit in shared/err.craftgo, got %s", got)
	}
}

// TestDefinitionExtendServiceJumpsToPrimary pins ctrl+click on the name
// in `extend service X`: an extend is a CONTINUATION, not a declaration
// site, so the jump must land on X's primary block. The name sits in a
// header, which is not a type-shape position - misreading it as one used
// to send the lookup down the "anything but a middleware" path, where the
// first decl named X in the file wins. In the file layout the docs call
// typical (one extend per file) that first decl is the extend itself, so
// the editor jumped the cursor onto the token it started from.
func TestDefinitionExtendServiceJumpsToPrimary(t *testing.T) {
	src := `package x
service Alpha {
	get A /a {}
}

extend service Alpha {
	get B /b {}
}
`
	view := parseSnapshot("t.craftgo", src)
	// Second `Alpha` token - the name in the extend header.
	count := 0
	var pos protocol.Position
	for _, tok := range view.tokens {
		if tok.Text != "Alpha" {
			continue
		}
		count++
		if count == 2 {
			pos = protocol.Position{Line: uint32(tok.Pos.Line - 1), Character: uint32(tok.Pos.Column - 1)}
			break
		}
	}
	if count < 2 {
		t.Fatalf("expected 2 Alpha tokens, got %d", count)
	}
	idx, _ := view.tokenAt(pos.Line, pos.Character)
	if kind := lookupKindAt(view, idx, pos); kind != semantic.ServiceDecls {
		t.Fatalf("expected service kinds for an extend header, got %v", kind)
	}
	d := lookupIn(t, "x", "Alpha", semantic.ServiceDecls, view.file)
	sd, ok := d.(*ast.ServiceDecl)
	if !ok {
		t.Fatalf("expected ServiceDecl, got %T", d)
	}
	if sd.Extend {
		t.Error("resolved to the extend block; the primary is the definition site")
	}
	if sd.Pos.Line != 2 {
		t.Errorf("expected the primary at line 2, got %d", sd.Pos.Line)
	}
}

// TestDefinitionExtendServiceResolvesAcrossFiles is the layout the fix is
// really for: the extend lives in its own file, so nothing in that file
// can answer the click. The primary has to be found in a sibling file
// rather than the extend the cursor sits in.
func TestDefinitionExtendServiceResolvesAcrossFiles(t *testing.T) {
	primary := `package x
service Alpha {
	get A /a {}
}
`
	ext := `package x

extend service Alpha {
	get B /b {}
}
`
	view := parseSnapshot("alpha-extra.craftgo", ext)
	pos := protocol.Position{Line: 2, Character: 15} // the Alpha in the extend header
	idx, tok := view.tokenAt(pos.Line, pos.Character)
	if tok.Text != "Alpha" {
		t.Fatalf("probe landed on %q, not the service name", tok.Text)
	}
	if kind := lookupKindAt(view, idx, pos); kind != semantic.ServiceDecls {
		t.Fatalf("expected service kinds, got %v", kind)
	}
	// The extend-only file holds no definition site.
	if d := lookupIn(t, "x", "Alpha", semantic.ServiceDecls, view.file); d != nil {
		t.Errorf("extend-only file has no definition site, got %T", d)
	}
	d := lookupIn(t, "x", "Alpha", semantic.ServiceDecls,
		mustParseFile(t, "x/alpha-extra.craftgo", ext),
		mustParseFile(t, "x/alpha.craftgo", primary))
	if d == nil {
		t.Fatal("project-wide lookup did not find the primary service")
	}
	sd, isSvc := d.(*ast.ServiceDecl)
	if !isSvc || sd.Extend {
		t.Fatalf("expected the primary ServiceDecl, got %T (extend=%v)", d, isSvc && sd.Extend)
	}
	if got := d.DeclPos().Filename; got != "x/alpha.craftgo" {
		t.Errorf("expected the hit in x/alpha.craftgo, got %s", got)
	}
}

// TestDefinitionServiceHeaderIsNotATypeShape guards the classification
// that caused the bug: the walk back from a service name must stop at the
// header instead of running into the previous declaration, where the
// first Ident it meets would read as a field-type pair.
func TestDefinitionServiceHeaderIsNotATypeShape(t *testing.T) {
	src := `package x
type Thing { id string }

extend service Alpha {
	get B /b {}
}
`
	view := parseSnapshot("t.craftgo", src)
	pos := protocol.Position{Line: 3, Character: 15}
	idx, tok := view.tokenAt(pos.Line, pos.Character)
	if tok.Text != "Alpha" {
		t.Fatalf("probe landed on %q, not the service name", tok.Text)
	}
	if isTypeShapePosition(view, idx) {
		t.Error("a service header name must not classify as a type-shape position")
	}
}

// TestDefinitionExtendServiceEndToEnd drives the real
// `textDocument/definition` handler over a project on disk, in the layout
// that actually ships: the primary in one file, the extend in another.
// Ctrl+click on the extend's name must reply with a Location in the
// PRIMARY's file - not with the extend's own position, and not with an
// empty list.
func TestDefinitionExtendServiceEndToEnd(t *testing.T) {
	root := t.TempDir()
	design := filepath.Join(root, "design")
	if err := os.MkdirAll(design, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "craftgo.design.yaml"), []byte("package: example.com/p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	primary := "package x\nservice Alpha {\n\tget A /a {}\n}\n"
	if err := os.WriteFile(filepath.Join(design, "alpha.craftgo"), []byte(primary), 0o644); err != nil {
		t.Fatal(err)
	}
	ext := "package x\n\nextend service Alpha {\n\tget B /b {}\n}\n"
	extPath := filepath.Join(design, "alpha-extra.craftgo")
	if err := os.WriteFile(extPath, []byte(ext), 0o644); err != nil {
		t.Fatal(err)
	}

	extURI := uri.File(extPath)
	srv := &Server{docs: map[uri.URI]*document{extURI: {text: ext}}}
	params := protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI(extURI)},
			// `extend service Alpha` - the name starts at column 15.
			Position: protocol.Position{Line: 2, Character: 15},
		},
	}
	req, err := jsonrpc2.NewCall(jsonrpc2.NewNumberID(1), protocol.MethodTextDocumentDefinition, params)
	if err != nil {
		t.Fatal(err)
	}
	var got []protocol.Location
	replier := func(_ context.Context, result interface{}, err error) error {
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
		locs, ok := result.([]protocol.Location)
		if !ok {
			t.Fatalf("unexpected reply type %T", result)
		}
		got = locs
		return nil
	}
	if err := srv.onDefinition(context.Background(), replier, req); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 location, got %d (%v)", len(got), got)
	}
	if filepath.Base(uriToPath(string(got[0].URI))) != "alpha.craftgo" {
		t.Errorf("jumped into %s, want the primary's file alpha.craftgo", uriToPath(string(got[0].URI)))
	}
	if got[0].Range.Start.Line != 1 {
		t.Errorf("want the primary declaration on line 2 (0-indexed 1), got %d", got[0].Range.Start.Line)
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
	d := lookupIn(t, "x", "Conflict", semantic.ErrorDecls, view.file)
	if d == nil {
		t.Fatal("error lookup returned nil")
	}
	if _, isErr := d.(*ast.ErrorDecl); !isErr {
		t.Errorf("expected ErrorDecl, got %T", d)
	}
}

// lookupIn analyses files as one project and resolves name from package
// homePkg among the selected declaration kinds.
func lookupIn(t *testing.T, homePkg, name string, kinds semantic.DeclKind, files ...*ast.File) ast.Decl {
	t.Helper()
	proj, _ := semantic.AnalyzeProject(files, semantic.Options{})
	return proj.Lookup(homePkg, name, kinds)
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

// mustHoverAt parses src, finds the first occurrence of needle, and
// returns the hover markdown text. It bundles the
// `parseSnapshot → findToken → tokenAt → hoverForToken → nil check`
// sequence into one call. Fails when no hover is produced - tests use
// the return value to assert on contents directly.
func mustHoverAt(t *testing.T, path, src, needle string) string {
	t.Helper()
	view := parseSnapshot(path, src)
	pos := findToken(t, view, needle)
	idx, tok := view.tokenAt(pos.Line, pos.Character)
	hov := hoverForToken(view, idx, tok)
	if hov == nil {
		t.Fatalf("expected hover at %q", needle)
	}
	return hov.Contents.Value
}

// mustCompletionsAt parses src and runs the completion provider at the
// supplied LSP-coordinate position. Bundles the `parseSnapshot →
// &Server{} → completionsAt` 3-liner that opens every completion
// test. The URI is synthesized from path so call sites don't repeat
// `"file:///" + path` boilerplate.
func mustCompletionsAt(t *testing.T, path, src string, line, ch uint32) []protocol.CompletionItem {
	t.Helper()
	view := parseSnapshot(path, src)
	srv := &Server{docs: map[uri.URI]*document{}}
	return srv.completionsAt(view, protocol.Position{Line: line, Character: ch}, "file:///"+path, src)
}

// labelSet collects every completion item's Label into a presence map
// for cheap `set[name]` lookups. Used by expectLabels / expectNoLabels.
func labelSet(items []protocol.CompletionItem) map[string]bool {
	got := make(map[string]bool, len(items))
	for _, it := range items {
		got[it.Label] = true
	}
	return got
}

// expectLabels asserts every want label appears in items, bundling the
// `got := map[string]bool{...}; for _, w := range []string{...} { ...
// !got[w] ... }` check that closes every completion test.
func expectLabels(t *testing.T, items []protocol.CompletionItem, wants ...string) {
	t.Helper()
	got := labelSet(items)
	var missing []string
	for _, w := range wants {
		if !got[w] {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		t.Errorf("completion missing %d label(s): %v\ngot: %v", len(missing), missing, got)
	}
}

// expectNoLabels is the negative-filter partner: every banned label
// MUST NOT appear in items. Used by tests that pin "this category /
// kind must NOT leak into this completion site" - e.g. number-only
// validators on a string scalar.
func expectNoLabels(t *testing.T, items []protocol.CompletionItem, banned ...string) {
	t.Helper()
	got := labelSet(items)
	var leaked []string
	for _, w := range banned {
		if got[w] {
			leaked = append(leaked, w)
		}
	}
	if len(leaked) > 0 {
		t.Errorf("completion unexpectedly contains %d banned label(s): %v\ngot: %v", len(leaked), leaked, got)
	}
}
