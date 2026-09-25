package lsp

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/craftgodotdev/craftgo/internal/errcat"
)

// `@default(|)` on an enum field offers exactly the enum's values.
func TestCompletionDefaultOnEnumField(t *testing.T) {
	src := `package x
enum Status { Active  Inactive  Pending }
type T {
	st Status? @default()
}
`
	// Cursor between the parens of `@default()`.
	items := mustCompletionsAt(t, "t.craftgo", src, 3, 21)
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
// extend it drops @prefix and @operationId and keeps @group.
func TestCompletionServiceDecoratorSite(t *testing.T) {
	primary := "package x\n\n@\nservice S {\n  get A /a {}\n}\n"
	items := mustCompletionsAt(t, "t.craftgo", primary, 2, 1)
	expectLabels(t, items, "prefix", "group", "middlewares", "tags", "security")

	extend := "package x\n\nservice S { get A /a {} }\n\n@\nextend service S {\n  get B /b {}\n}\n"
	eitems := mustCompletionsAt(t, "t.craftgo", extend, 4, 1)
	expectLabels(t, eitems, "group", "middlewares", "tags", "security")
	expectNoLabels(t, eitems, "prefix", "operationId")
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
	fileURI := uri.File(srcPath)
	srv := &server{docs: map[uri.URI]string{fileURI: src}}
	// Cursor right after `@security(`.
	items := completionItems(t, srv, fileURI, protocol.Position{Line: 2, Character: 10})
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
	fileURI := uri.File(srcPath)
	srv := &server{docs: map[uri.URI]string{fileURI: src}}
	_ = completionItems(t, srv, fileURI, protocol.Position{Line: 2, Character: 10})
}

// keys returns the keys of m in map order.
func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A declaration completes as one item in every slot that offers it; a type
// slot only qualifies the label with the package.
func TestCompletionItemIsOnePerDeclaration(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "design", "craftgo.design.yaml"), layoutOnly)
	mustWrite(t, filepath.Join(root, "design", "shared", "s.craftgo"),
		"package shared\n\n// Auth checks the token.\nmiddleware Auth\n\n// Money is an amount.\ntype Money { amount int }\n")
	path := filepath.Join(root, "design", "app", "a.craftgo")
	complete := func(src string) map[string]protocol.CompletionItem {
		t.Helper()
		clean, pos := markCursor(t, src)
		mustWrite(t, path, clean)
		u := uri.File(path)
		byLabel := map[string]protocol.CompletionItem{}
		for _, it := range completionItems(t, &server{docs: map[uri.URI]string{u: clean}}, u, pos) {
			byLabel[it.Label] = it
		}
		return byLabel
	}
	head := "package app\n\nimport \"shared\"\n\n"
	member := complete(head + "type T {\n\tm shared.|\n}\n")
	typeSlot := complete(head + "type T {\n\tm |\n}\n")
	mixin := complete(head + "type T {\n\ta int\n\t|\n}\n")
	for _, c := range []struct{ a, b protocol.CompletionItem }{
		{member["Money"], typeSlot["shared.Money"]},
		{typeSlot["shared.Money"], mixin["shared.Money"]},
	} {
		if c.a.Label == "" || c.b.Label == "" || c.a.Kind != c.b.Kind || c.a.Detail != c.b.Detail || c.a.Documentation != c.b.Documentation {
			t.Errorf("items differ:\n%+v\n%+v", c.a, c.b)
		}
	}
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

// `/{|}` offers a route variable exactly when the analyser binds it to a
// request field, a mixin's included, without an error.
func TestCompletionPathParameterAgreesWithTheAnalyser(t *testing.T) {
	cases := []struct {
		types  string
		params []string
	}{
		{
			types: "enum Kind { A }\nscalar Blob bytes\ntype IDs { tenant string }\ntype Req {\n\tIDs\n\tid string @nullable\n" +
				"\tsku string @sensitive\n\tn int @default(3)\n\tok string\n\tkind Kind\n\tblob Blob\n\topt string?\n" +
				"\ttags string[]\n\tq string @query\n}\n",
			params: []string{"tenant", "id", "sku", "n", "ok", "kind", "blob", "opt", "tags", "q"},
		},
		{
			types:  "type Req {\n\tcode string @path(\"sku\")\n}\n",
			params: []string{"sku", "code"},
		},
	}
	for _, c := range cases {
		route := func(param string) string {
			return "package x\n\n" + c.types + "service S {\n\tpost A /store/{" + param + "} { request Req }\n}\n"
		}
		offered := labelSet(mustCompletionsAtCursor(t, "t.craftgo", route(cursorMark)))
		for _, param := range c.params {
			clean := true
			for _, d := range bufferDiagnostics(route(param)) {
				if d.Severity == protocol.DiagnosticSeverityError {
					clean = false
				}
			}
			if offered[param] != clean {
				t.Errorf("{%s}: offered %v, but the analyser binds it cleanly: %v", param, offered[param], clean)
			}
		}
	}
}

// `pkg.|` in a type position offers the types, enums and scalars of pkg.
func TestCompletionPackageMembersInATypePosition(t *testing.T) {
	design := designProject(t, map[string]string{
		"shared/shared.craftgo": "package shared\n\ntype Addr { city string }\nenum Kind { A }\nscalar Email string\n" +
			"middleware Auth\nerror NotFound Gone\nservice Svc { get G /g {} }\nevent Moved { payload Addr }\n",
	})
	src, pos := markCursor(t, "package app\n\nimport \"shared\"\n\ntype User {\n\thome shared.|\n}\n")
	path := filepath.Join(design, "app", "app.craftgo")
	mustWrite(t, path, src)
	u := uri.File(path)
	items := completionItems(t, &server{docs: map[uri.URI]string{u: src}}, u, pos)
	expectLabels(t, items, "Addr", "Kind", "Email")
	expectNoLabels(t, items, "Auth", "Gone", "Svc", "Moved")
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

// A decorator's argument list takes no decorator: `@` inside one offers
// nothing, whatever the decorator.
func TestCompletionNoDecoratorInsideDecoratorArguments(t *testing.T) {
	for _, args := range []string{"@doc(@|)", "@doc(\"a\", @de| )", "@format(@|)", "@length(1, @|)", "@nope(@| )", "@nope(@de| )", "@example({ s: @| })"} {
		t.Run(args, func(t *testing.T) {
			src := typeSlotFixtures + "middleware Auth\n\ntype User {\n\ts string " + args + "\n}\n"
			if items := mustCompletionsAtCursor(t, "t.craftgo", src); len(items) != 0 {
				t.Errorf("completion inside %s = %v, want nothing", args, labelSet(items))
			}
		})
	}
	src := typeSlotFixtures + "middleware Auth\n\nservice S {\n\t@middlewares(@|)\n\tget G /g {}\n}\n"
	if items := mustCompletionsAtCursor(t, "t.craftgo", src); len(items) != 0 {
		t.Errorf("completion inside @middlewares(@) = %v, want nothing", labelSet(items))
	}
}

// `scalar Name |` offers only the built-ins a scalar can wrap.
func TestCompletionScalarPrimitiveSlotIsBuiltinsOnly(t *testing.T) {
	items := mustCompletionsAtCursor(t, "t.craftgo", typeSlotFixtures+"scalar Email |\n")
	expectLabels(t, items, "string", "int", "bool", "bytes", "float64")
	expectNoLabels(t, items, "any", "datetime", "file", "object", "map", "Address", "Kind", "Flag")
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
		clean, pos := markCursor(t, src)
		write(buf, clean)
		u := uri.File(buf)
		return completionItems(t, &server{docs: map[uri.URI]string{u: clean}}, u, pos)
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
	expectLabels(t, items, "200", "201", "204")
	for _, c := range errcat.Categories {
		expectLabels(t, items, strconv.Itoa(c.Status))
	}
	for _, it := range items {
		if it.Label == "201" && it.Detail != "HTTP 201 Created" {
			t.Errorf("HTTP 201 detail = %q, want IANA reason phrase", it.Detail)
		}
	}
}

// The `error` and `scalar` snippets choose among every error category and
// every built-in `scalar Name |` offers.
func TestKeywordSnippetChoices(t *testing.T) {
	snippets := map[string]string{}
	for _, it := range keywordCompletions("error", "scalar") {
		snippets[it.Label] = it.InsertText
	}
	var categories, primitives []string
	for _, c := range errcat.Categories {
		categories = append(categories, c.Name)
	}
	for _, it := range scalarPrimitiveCompletions() {
		primitives = append(primitives, it.Label)
	}
	for kw, want := range map[string][]string{"error": categories, "scalar": primitives} {
		_, choices, _ := strings.Cut(snippets[kw], "|")
		choices, _, _ = strings.Cut(choices, "|}")
		if got := strings.Split(choices, ","); !slices.Equal(got, want) {
			t.Errorf("%s snippet choices = %v, want %v", kw, got, want)
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
