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

// mustParseFile parses src, failing on any parse diagnostic.
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

// Hovering `@length` shows the decorator and its field level.
func TestHoverDecorator(t *testing.T) {
	v := mustHoverAt(t, "test.craftgo", testDSL, "length")
	if !strings.Contains(v, "@length") {
		t.Errorf("hover should mention: %q", v)
	}
	if !strings.Contains(v, "field") {
		t.Errorf("hover should mention legal level 'field': %q", v)
	}
}

// Hovering `string` shows the built-in's doc.
func TestHoverBuiltinType(t *testing.T) {
	v := mustHoverAt(t, "test.craftgo", testDSL, "string")
	if !strings.Contains(v, "UTF-8") {
		t.Errorf("string hover should mention UTF-8: %q", v)
	}
}

// Hovering `raw` in `@format(raw)` explains the raw shape, and hovering
// `bytes` points at it.
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

// Hovering a type reference shows the declaration line and doc.
func TestHoverUserType(t *testing.T) {
	view := parseSnapshot("test.craftgo", testDSL)
	// The second Greeter is a reference.
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

// `@default(|)` on an enum field offers exactly the enum's values.
func TestCompletionDefaultOnEnumField(t *testing.T) {
	src := `package x
enum Status { Active  Inactive  Pending }
type T {
	st Status? @default()
}
`
	// Cursor at the `(` of `@default()`.
	items := mustCompletionsAt(t, "t.craftgo", src, 3, 20)
	if len(items) != 3 {
		t.Fatalf("expected 3 enum-value completions, got %d: %+v", len(items), items)
	}
	expectLabels(t, items, "Active", "Inactive", "Pending")
}

// An empty duration argument offers the presets.
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

// Digits typed in a duration argument get each unit, as edits replacing them.
func TestCompletionDurationPartialNumber(t *testing.T) {
	src := `package x
service S {
	@timeout(10)
	get G /g {}
}
`
	// `@timeout(10|)`: column 12.
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

// Digits typed in a size argument get each unit.
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

// `@` on a field row offers field decorators.
func TestCompletionDecoratorAfterAt(t *testing.T) {
	src := `package x

type T {
	id string @
}
`
	// Cursor right after the `@` on line 3.
	items := mustCompletionsAt(t, "t.craftgo", src, 3, 12)
	if len(items) == 0 {
		t.Fatal("expected completion items after @ at field site")
	}
	expectLabels(t, items, "length", "sensitive")
}

// A `bytes @format(raw)` field offers no validator, only @format and the
// decorators that apply to every type.
func TestCompletionOnARawBytesFieldOffersNoValidator(t *testing.T) {
	src := "package x\n\ntype T {\n\tpayload bytes @format(raw) @\n}\n"
	items := mustCompletionsAt(t, "t.craftgo", src, 3, 28)
	expectNoLabels(t, items,
		"length", "minLength", "maxLength", "pattern",
		"gt", "gte", "lt", "lte", "range", "positive", "negative", "multipleOf",
		"minItems", "maxItems", "uniqueItems", "maxSize", "mimeTypes")
	expectLabels(t, items, "format", "json", "nullable", "doc", "sensitive")
}

// A plain `bytes` field still offers the text validators.
func TestCompletionOnAPlainBytesFieldStillOffersTextValidators(t *testing.T) {
	src := "package x\n\ntype T {\n\tpayload bytes @\n}\n"
	items := mustCompletionsAt(t, "t.craftgo", src, 3, 16)
	expectLabels(t, items, "format", "minLength", "maxLength")
}

// `@format(|)` offers `raw` beside the string formats.
func TestCompletionFormatArgOffersRaw(t *testing.T) {
	src := "package x\n\ntype T {\n\tpayload bytes @format(\n}\n"
	items := mustCompletionsAt(t, "t.craftgo", src, 3, 23)
	expectLabels(t, items, "raw", "email", "uuid")
}

// A field's type slot offers every built-in, `datetime` included.
func TestCompletionTypePositionOffersBuiltins(t *testing.T) {
	items := mustCompletionsAtCursor(t, "t.craftgo", "package x\n\ntype User {\n    home |\n}\n")
	expectLabels(t, items, "datetime", "bytes", "any", "string")
}

// A half-typed `@` above a service offers the service decorators; above an
// extend it drops @prefix and keeps @group.
func TestCompletionServiceDecoratorSite(t *testing.T) {
	primary := "package x\n\n@\nservice S {\n  get A /a {}\n}\n"
	items := mustCompletionsAt(t, "t.craftgo", primary, 2, 1)
	expectLabels(t, items, "prefix", "group", "middlewares", "tags", "security")

	extend := "package x\n\nservice S { get A /a {} }\n\n@\nextend service S {\n  get B /b {}\n}\n"
	eitems := mustCompletionsAt(t, "t.craftgo", extend, 4, 1)
	expectLabels(t, eitems, "group", "middlewares", "tags", "security")
	expectNoLabels(t, eitems, "prefix")
}

// Half-typed declarations panic neither the outline nor the analyser.
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
			_ = documentSymbols(view)
			// The analyser runs on the same partial AST.
			if view.file != nil {
				_, _ = semantic.Analyze([]*ast.File{view.file})
			}
		})
	}
}

// The outline has no symbol with an empty name, which VS Code rejects.
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

// `@security(|)` offers the manifest's security schemes, detailed by type.
func TestCompletionSecuritySchemeAtArgOne(t *testing.T) {
	t.Helper()
	// A manifest declaring two security schemes.
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
	// Cursor right after `@security(`.
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

// `@security(|)` completion without a manifest does not panic.
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
	_ = srv.completionsAt(view, pos, string(uri.File(srcPath)), src)
}

// keys returns the keys of m in map order.
func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A just-opened type, error or enum body offers nothing.
func TestCompletionSuppressedAfterOpenBrace(t *testing.T) {
	cases := []struct {
		label string
		src   string
		// line, col: the cursor after `{`.
		line int
		col  int
	}{
		{
			label: "type body just opened",
			src:   typeSlotFixtures + "type T {}",
			line:  5, col: 8, // between `{` and `}`
		},
		{
			label: "type body with whitespace",
			src:   typeSlotFixtures + "type T {\n  \n}",
			line:  6, col: 2, // blank indented line
		},
		{
			label: "error body just opened",
			src:   typeSlotFixtures + "error NotFound Missing {}",
			line:  5, col: 24,
		},
		{
			label: "enum body just opened",
			src:   typeSlotFixtures + "enum E {}",
			line:  5, col: 8,
		},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			items := mustCompletionsAt(t, "t.craftgo", c.src, uint32(c.line), uint32(c.col))
			if len(items) != 0 {
				t.Errorf("expected no completions right after `{`, got %d items: %v", len(items), labelSet(items))
			}
		})
	}
}

// A just-opened service, method or event body offers its keys.
func TestCompletionJustOpenedBlockOffersItsKeys(t *testing.T) {
	cases := []struct {
		label        string
		src          string
		line, col    int
		want, banned []string
	}{
		{
			label: "service body just opened",
			src:   typeSlotFixtures + "service S {}",
			line:  5, col: 11,
			want:   []string{"get", "post", "put", "patch", "delete", "head", "options"},
			banned: []string{"type", "service", "request", "Address"},
		},
		{
			label: "extend service body just opened",
			src:   typeSlotFixtures + "extend service S {}",
			line:  5, col: 18,
			want:   []string{"get", "post", "put", "patch", "delete", "head", "options"},
			banned: []string{"type", "service", "request", "Address"},
		},
		{
			label: "service body with whitespace",
			src:   typeSlotFixtures + "service S {\n  \n}",
			line:  6, col: 2,
			want:   []string{"get", "post", "put", "patch", "delete", "head", "options"},
			banned: []string{"type", "request", "Address"},
		},
		{
			label: "method body just opened",
			src:   typeSlotFixtures + "service S {\n    get A /a {}\n}\n",
			line:  6, col: 14,
			want:   []string{"request", "response"},
			banned: []string{"get", "type", "payload", "Address"},
		},
		{
			label: "event body just opened",
			src:   typeSlotFixtures + "event E {}",
			line:  5, col: 9,
			want:   []string{"payload"},
			banned: []string{"request", "response", "type", "Address"},
		},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			items := mustCompletionsAt(t, "t.craftgo", c.src, uint32(c.line), uint32(c.col))
			expectLabels(t, items, c.want...)
			expectNoLabels(t, items, c.banned...)
		})
	}
}

// A field's type slot offers no error declaration.
func TestCompletionTypePositionExcludesErrors(t *testing.T) {
	src := "package x\n\n" +
		"type RealType { id string }\n" +
		"error NotFound MissingErr\n" +
		"type Holder {\n" +
		"    ref \n" +
		"}\n"
	// Cursor right after `    ref `: line 5, character 8.
	items := mustCompletionsAt(t, "t.craftgo", src, 5, 8)
	for _, it := range items {
		if it.Label == "MissingErr" {
			t.Errorf("error declaration leaked into type-position completions: %+v", it)
		}
	}
	// A type is still offered.
	expectLabels(t, items, "RealType")
}

// cursorMark marks the cursor in a completion fixture; the DSL has no `|` token.
const cursorMark = "|"

// mustCompletionsAtCursor runs completion at the fixture's cursor mark.
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

// typeSlotFixtures declares one type, one enum and one bool scalar.
const typeSlotFixtures = "package x\n\ntype Address { city string }\nenum Kind { Active }\nscalar Flag bool\n"

// Every type slot offers the built-ins, `map` and the project's types, enums
// and scalars.
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
			expectLabels(t, items, "string", "int", "bytes", "datetime", "any", "file", "map", "Address", "Kind", "Flag")
			// `object` is legal only inside `@example({...})`.
			expectNoLabels(t, items, "object", "service", "middleware", "extend")
		})
	}
}

// `request |`, `response |` and `payload |` offer only `type` declarations.
func TestCompletionClauseSlotsOfferMessageTypes(t *testing.T) {
	for label, body := range map[string]string{
		"method request":  "service S {\n    get Fetch /f {\n        request |\n    }\n}\n",
		"method response": "service S {\n    get Fetch /f {\n        response |\n    }\n}\n",
		"event payload":   "event Moved {\n    payload |\n}\n",
	} {
		t.Run(label, func(t *testing.T) {
			items := mustCompletionsAtCursor(t, "t.craftgo", typeSlotFixtures+body)
			expectLabels(t, items, "Address")
			expectNoLabels(t, items, "string", "int", "bytes", "datetime", "any", "file",
				"Kind", "Flag", "map", "object", "get", "request", "response", "payload")
		})
	}
}

// A slot that takes a name, a value or a keyword offers no built-in.
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

// Past a `?` or `[]` suffix nothing is offered.
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

// The fallback offers what the enclosing block accepts.
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
			// A mixin names a `type`, never an enum or a scalar.
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

// An enum body offers nothing: its values are the author's names.
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

// pathParamFixture's Req has fields that can bind a path segment (id, sku)
// and fields that cannot (q is a query field, tags an array, note optional).
const pathParamFixture = "package x\n\ntype Req {\n" +
	"    id string\n" +
	"    sku int\n" +
	"    q string @query\n" +
	"    tags string[]\n" +
	"    note string?\n" +
	"}\n"

// `/{|}` offers the request fields that can bind a path segment and are not
// bound yet.
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

// Without a request clause `/{|}` offers nothing.
func TestCompletionPathParameterWithoutRequestStaysSilent(t *testing.T) {
	src := pathParamFixture + "service S {\n    get A /store/{|} { }\n}\n"
	if items := mustCompletionsAtCursor(t, "t.craftgo", src); len(items) != 0 {
		t.Errorf("expected no completions without a request clause, got %v", labelSet(items))
	}
}

// `@default(|)` offers an enum's values, or true and false for a bool or a
// bool scalar, and nothing for a string.
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

// A decorator argument with no closed set offers nothing.
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

// `scalar Name |` offers only the built-ins a scalar can wrap.
func TestCompletionScalarPrimitiveSlotIsBuiltinsOnly(t *testing.T) {
	items := mustCompletionsAtCursor(t, "t.craftgo", typeSlotFixtures+"scalar Email |\n")
	expectLabels(t, items, "string", "int", "bool", "bytes", "float64", "datetime")
	expectNoLabels(t, items, "any", "file", "object", "map", "Address", "Kind", "Flag")
}

// A type parameter being declared (`type Page<|>`) offers nothing.
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

// `extend service |` at the end of the buffer offers the services.
func TestCompletionExtendServiceTargetAtEndOfBuffer(t *testing.T) {
	src := "package x\n\nservice Api {\n    get A /a {}\n}\nextend service |"
	for _, tail := range []string{"", "Ap"} {
		items := mustCompletionsAtCursor(t, "t.craftgo", src+tail)
		expectLabels(t, items, "Api")
		// Only the services are offered.
		expectNoLabels(t, items, "type", "service", "extend", "package")
	}
}

// `package |` offers the folder's package, and `import |` quoted import paths.
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

// `scalar Email |` offers the primitives.
func TestCompletionScalarPrimitivePosition(t *testing.T) {
	src := "package x\n\nscalar Email "
	// Cursor right after `scalar Email `.
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 13)
	expectLabels(t, items, "string", "int", "bool")
}

// `@errors(|)` offers the declared errors.
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
	// Cursor right after `@errors(`.
	items := mustCompletionsAt(t, "t.craftgo", src, 7, 12)
	expectLabels(t, items, "UserNotFoundErr", "EmailTakenErr")
}

// `@` on a string scalar offers the string validators only.
func TestCompletionDecoratorOnScalarFiltersByPrimitive(t *testing.T) {
	src := "package x\n\n" +
		"scalar Gmail string @\n"
	// Cursor right after the `@`.
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 21)
	expectLabels(t, items, "length", "minLength", "maxLength", "pattern", "format")
	expectNoLabels(t, items, "gt", "gte", "lt", "lte", "range", "positive", "negative", "multipleOf")
	expectNoLabels(t, items, "minItems", "maxItems", "uniqueItems")
}

// `@format(|)` offers the registered formats.
func TestCompletionFormatDecoratorArgs(t *testing.T) {
	src := "package x\n" +
		"type Req {\n" +
		"  email string @format(\n" +
		"}\n"
	// Cursor right after `  email string @format(`: line 2, character 23.
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 23)
	expectLabels(t, items, "email", "uuid", "url")
}

// `@status(|)` offers status codes with their reason phrase.
func TestCompletionStatusDecoratorArgs(t *testing.T) {
	src := "package x\n\n" +
		"type Req { id string }\n" +
		"type Resp { id string }\n" +
		"service S {\n" +
		"    @status(\n" +
		"    post Save /save { request Req response Resp }\n" +
		"}\n"
	// Cursor right after `@status(`.
	items := mustCompletionsAt(t, "t.craftgo", src, 5, 12)
	expectLabels(t, items, "200", "201", "204", "400", "404", "500")
	for _, it := range items {
		if it.Label == "201" && it.Detail != "HTTP 201 Created" {
			t.Errorf("HTTP 201 detail = %q, want IANA reason phrase", it.Detail)
		}
	}
}

// `error |` offers exactly the error categories, each with its HTTP status.
func TestCompletionErrorCategoryAfterKeyword(t *testing.T) {
	src := "package x\n\nerror "
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 6)
	if len(items) != len(errcat.Categories) {
		t.Fatalf("expected %d category items (one per reserved HTTP category), got %d", len(errcat.Categories), len(items))
	}
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
	for _, it := range items {
		switch it.Label {
		case "length", "doc", "package", "type", "service":
			t.Errorf("unexpected non-category item leaked into category completions: %q", it.Label)
		}
	}
}

// A partly typed category still gets every category; the client filters.
func TestCompletionErrorCategoryWhileTyping(t *testing.T) {
	src := "package x\n\nerror Not"
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 9)
	if len(items) != len(errcat.Categories) {
		t.Fatalf("expected %d category items while typing, got %d", len(errcat.Categories), len(items))
	}
}

// `error NotFound |` names the error, so no category is offered.
func TestCompletionErrorCategoryNotInOtherPositions(t *testing.T) {
	src := "package x\n\nerror NotFound "
	items := mustCompletionsAt(t, "t.craftgo", src, 2, 15)
	for _, it := range items {
		if it.Detail != "" && strings.HasPrefix(it.Detail, "HTTP ") {
			t.Errorf("category completions leaked into name position: got %q (%s)", it.Label, it.Detail)
		}
	}
}

// The outline has one symbol per declaration, with its kind and a type's
// fields as children.
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
	for _, s := range syms {
		if s.Name == "Greeter" && len(s.Children) < 2 {
			t.Errorf("Greeter should have >=2 field children, got %d", len(s.Children))
		}
	}
}

// The whole-document range starts at line 0, and storeDoc replaces the text
// snapshot returns.
func TestFormattingProducesEdit(t *testing.T) {
	dirty := "package x\n\ntype T {\n  id string\n}\n"
	clean := "package x\n\ntype T {\n\tid string\n}\n"
	if r := wholeDocumentRange(dirty); r.Start.Line != 0 {
		t.Errorf("Range.Start.Line = %d, want 0", r.Start.Line)
	}
	uriOf := uri.New("file:///t.craftgo")
	srv := &Server{docs: map[uri.URI]*document{uriOf: {text: dirty}}}
	srv.storeDoc(uriOf, dirty, 1)
	if got := srv.snapshot(uriOf); got != dirty {
		t.Fatalf("snapshot mismatch")
	}
	srv.storeDoc(uriOf, clean, 1)
	if got := srv.snapshot(uriOf); got != clean {
		t.Fatalf("snapshot mismatch (clean)")
	}
}

// A declaration's name resolves at the cursor and recurs at its uses.
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
	// The first Greeter is the declaration.
	pos := findToken(t, view, "Greeter")
	idx, tok := view.tokenAt(pos.Line, pos.Character)
	if tok.Text != "Greeter" {
		t.Fatalf("token under cursor = %q, want Greeter", tok.Text)
	}
	if findDecl(view.file, tok.Text) == nil {
		t.Fatal("expected Greeter to resolve to a top-level declaration")
	}
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

// Inside `@middlewares(...)` a name resolves to the middleware, not to a
// same-named error.
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
	// The third AuthRequired is the one inside @middlewares(...).
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

// A type position never resolves to a same-named middleware.
func TestDefinitionTypeShapePositionExcludesMiddleware(t *testing.T) {
	src := `package x
middleware Greeter
type Greeter { id string }
type Holder { g Greeter }
`
	view := parseSnapshot("t.craftgo", src)
	// The third Greeter is the field type in `g Greeter`.
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

// Across files a name resolves to the middleware or the error the lookup
// kinds select.
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

// The name in `extend service X` resolves to X's primary block.
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
	// The second Alpha is the name in the extend header.
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

// An extend in a file of its own resolves to the primary in a sibling file.
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

// A service header's name is not a type position, even after a type declaration.
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

// `textDocument/definition` on an extend's name replies with the primary's
// location in its own file.
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

// Inside `@errors(...)` a name resolves to the error, not to a same-named
// middleware.
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

// findToken returns the 0-based LSP position of the first token spelt needle.
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

// mustHoverAt returns the hover text of the first token spelt needle in src.
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

// mustCompletionsAt runs completion at (line, ch) of src, with a URI built
// from path.
func mustCompletionsAt(t *testing.T, path, src string, line, ch uint32) []protocol.CompletionItem {
	t.Helper()
	view := parseSnapshot(path, src)
	srv := &Server{docs: map[uri.URI]*document{}}
	return srv.completionsAt(view, protocol.Position{Line: line, Character: ch}, "file:///"+path, src)
}

// labelSet returns the set of item labels.
func labelSet(items []protocol.CompletionItem) map[string]bool {
	got := make(map[string]bool, len(items))
	for _, it := range items {
		got[it.Label] = true
	}
	return got
}

// expectLabels fails unless every want label is in items.
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

// expectNoLabels fails if any banned label is in items.
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
