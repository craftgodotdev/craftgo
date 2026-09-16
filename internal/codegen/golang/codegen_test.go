package golang

import (
	goast "go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	gopath "path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/idents"
	"github.com/craftgodotdev/craftgo/internal/lexer"
	craftparser "github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

func analyze(t *testing.T, src string) *semantic.Package {
	t.Helper()
	p := craftparser.New("test.craftgo", src)
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("parse errors: %v", d)
	}
	pkg, diags := semantic.Analyze([]*ast.File{f})
	// Treat only error-severity diags as fatal - warnings (e.g. the
	// @nullable-on-T? hint) shouldn't fail codegen tests because the
	// generated code is still well-defined.
	var fatal []semantic.Diagnostic
	for _, d := range diags {
		if d.Severity == lexer.SeverityError {
			fatal = append(fatal, d)
		}
	}
	if len(fatal) > 0 {
		t.Fatalf("semantic errors: %v", fatal)
	}
	return pkg
}

func mustParseGo(t *testing.T, src string) {
	t.Helper()
	file, err := parser.ParseFile(gotoken.NewFileSet(), "out.go", src, parser.AllErrors)
	if err != nil {
		t.Fatalf("generated Go does not parse: %v\n--- source ---\n%s", err, src)
	}
	mustImportsMatchUsage(t, file, src)
}

// stdlibQualifiers are the standard-library packages the emitters render
// into generated source. A qualifier from this list appearing in the code
// must have a matching import, and vice versa.
//
// Parsing alone cannot catch either half: `fmt.Errorf` with no import
// block is valid syntax and only fails at compile time, which the unit
// tests never reach. The list is closed on purpose - a new stdlib
// dependency belongs here so the check keeps covering it.
var stdlibQualifiers = map[string]string{
	"fmt":       "fmt",
	"errors":    "errors",
	"strconv":   "strconv",
	"strings":   "strings",
	"time":      "time",
	"regexp":    "regexp",
	"utf8":      "unicode/utf8",
	"reflect":   "reflect",
	"json":      "encoding/json",
	"base64":    "encoding/base64",
	"http":      "net/http",
	"url":       "net/url",
	"io":        "io",
	"os":        "os",
	"sort":      "sort",
	"sync":      "sync",
	"context":   "context",
	"multipart": "mime/multipart",
	"mail":      "net/mail",
	"netip":     "net/netip",
	"slices":    "slices",
	"maps":      "maps",
}

// mustImportsMatchUsage asserts the file imports exactly the standard
// library it uses. It walks selector expressions rather than the raw
// text, so a package name inside a string literal or a comment does not
// count as a use.
//
// The missing half is the one that bites: an emitter that renders
// `fmt.Errorf` without registering the import produces a file that parses
// and does not compile, and the generator exits 0.
func mustImportsMatchUsage(t *testing.T, file *goast.File, src string) {
	t.Helper()

	imported := map[string]bool{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if spec.Name != nil {
			imported[spec.Name.Name] = true
			continue
		}
		imported[gopath.Base(path)] = true
	}

	used := map[string]bool{}
	goast.Inspect(file, func(n goast.Node) bool {
		sel, ok := n.(*goast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := sel.X.(*goast.Ident); ok {
			used[ident.Name] = true
		}
		return true
	})

	for name := range used {
		path, std := stdlibQualifiers[name]
		if std && !imported[name] {
			t.Errorf("generated Go uses %s.* but does not import %q\n--- source ---\n%s", name, path, src)
		}
	}
	for name := range imported {
		if name == "_" || name == "." {
			continue
		}
		if _, std := stdlibQualifiers[name]; std && !used[name] {
			t.Errorf("generated Go imports %q but never uses it\n--- source ---\n%s", name, src)
		}
	}
}

// mustContainAll asserts every want substring appears somewhere in got.
// Reports ALL misses at once (not the first only) so a generator change
// that drops 5 lines surfaces as one diagnostic instead of 5 sequential
// edit-test-edit cycles. Use it instead of stacking N
// if-Contains-Errorf blocks per test.
func mustContainAll(t *testing.T, got string, wants ...string) {
	t.Helper()
	var missing []string
	for _, w := range wants {
		if !strings.Contains(got, w) {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		t.Errorf("output missing %d expected substring(s):\n  - %s\n--- got ---\n%s",
			len(missing), strings.Join(missing, "\n  - "), got)
	}
}

// mustContainNone is the inverse of mustContainAll: every needle MUST
// NOT appear in got. Used by tests that pin "this line should have
// been dropped" assertions where the regression risk is accidental
// re-inclusion.
func mustContainNone(t *testing.T, got string, unwanted ...string) {
	t.Helper()
	var present []string
	for _, w := range unwanted {
		if strings.Contains(got, w) {
			present = append(present, w)
		}
	}
	if len(present) > 0 {
		t.Errorf("output unexpectedly contains %d forbidden substring(s):\n  - %s\n--- got ---\n%s",
			len(present), strings.Join(present, "\n  - "), got)
	}
}

// ---------- types ----------

func TestGenerateTypesSensitiveJSONDash(t *testing.T) {
	// `@sensitive` fields are server-internal: they get json:"-" so
	// neither the request decoder nor the response encoder touches
	// them. Logic populates the field directly via Go assignment.
	pkg := analyze(t, `package design
type User { id string  internal string @sensitive }`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(out)
	mustParseGo(t, src)
	norm := strings.Join(strings.Fields(src), " ")
	if !strings.Contains(norm, `Internal string `+"`"+`json:"-"`+"`") {
		t.Errorf("expected Internal string `json:\"-\"`, got:\n%s", src)
	}
	if !strings.Contains(norm, `ID string `+"`"+`json:"id"`+"`") {
		t.Errorf("non-sensitive field should keep its tag, got:\n%s", src)
	}
}

// A field bound to @path / @query / @header / @cookie carries json:"-" (off
// the JSON body) plus a binding key naming the wire location, so the
// generated struct documents where the value rides. The key's value is the
// decorator's wire-name override when present, else the field name.
func TestGenerateTypesNonBodyBindingTag(t *testing.T) {
	pkg := analyze(t, `package design
type LookupReq {
    id    string @path
    page  int    @query("page_num")
    trace string @header("X-Trace-Id")
    token string @cookie("session")
    name  string
}
service Lookups {
    get Lookup /items/{id} {
        request LookupReq
    }
}`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(out)
	mustParseGo(t, src)
	norm := strings.Join(strings.Fields(src), " ")
	for _, want := range []string{
		`ID string ` + "`json:\"-\" path:\"id\"`",              // no override → field name
		`Page int ` + "`json:\"-\" query:\"page_num\"`",        // override wins
		`Trace string ` + "`json:\"-\" header:\"X-Trace-Id\"`", // header name kept verbatim
		`Token string ` + "`json:\"-\" cookie:\"session\"`",    // override wins
		`Name string ` + "`json:\"name\"`",                     // plain body field unchanged
	} {
		if !strings.Contains(norm, want) {
			t.Errorf("missing struct tag %q in:\n%s", want, src)
		}
	}
}

func TestGenerateTypesBasic(t *testing.T) {
	pkg := analyze(t, `package design
type User { id string  name string  age int? }`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(out)
	mustParseGo(t, src)
	// gofmt aligns struct fields/tags; collapse whitespace before matching.
	norm := strings.Join(strings.Fields(src), " ")
	if !strings.Contains(norm, "type User struct") {
		t.Error("missing User")
	}
	if !strings.Contains(norm, "ID string") {
		t.Errorf("expected ID field with initialism rule:\n%s", src)
	}
	if !strings.Contains(norm, "Age *int") {
		t.Error("expected pointer for optional age")
	}
}

func TestGenerateTypesArrayMap(t *testing.T) {
	pkg := analyze(t, `package design
type X { tags string[]  meta map<string, string> }`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)
	norm := strings.Join(strings.Fields(src), " ")
	if !strings.Contains(norm, "Tags []string") {
		t.Errorf("missing Tags []string in:\n%s", src)
	}
	if !strings.Contains(norm, "Meta map[string]string") {
		t.Errorf("missing Meta map[string]string in:\n%s", src)
	}
}

func TestGenerateTypesJSONKeyOverride(t *testing.T) {
	pkg := analyze(t, `package design
type Item { id string }
type Order { items Item[] @json("OrderItem")  storeId string? @json("store_id") }`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)
	mustContainAll(t, src,
		"Items   []Item  `json:\"OrderItem\"`",
		"StoreID *string `json:\"store_id,omitempty\"`",
	)
}

// The three shapes a `bytes @format(raw)` field takes. It is NOT treated
// as nilable even though wire.Raw is a []byte: `?` and `@nullable` both
// wrap to *wire.Raw, so "the key was absent" stays distinguishable from
// the four bytes `null`, which an encoded value may legitimately be.
func TestGenerateTypesRawBytesShapes(t *testing.T) {
	pkg := analyze(t, `package design
type X { payload bytes @format(raw)  meta bytes? @format(raw)  trace bytes @format(raw) @nullable }`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)
	mustContainAll(t, src,
		`"github.com/craftgodotdev/craftgo/pkg/wire"`,
		"Payload wire.Raw  `json:\"payload\"`",
		"Meta    *wire.Raw `json:\"meta,omitempty\"`",
		"Trace   *wire.Raw `json:\"trace\"`",
	)
}

// A scalar over raw bytes is an ALIAS: the runtime type carries the
// bytes through the codec on its own methods, and a defined type would
// leave those behind and silently base64 the value instead. A field of
// that scalar lowers to the same type it aliases.
func TestGenerateTypesRawBytesScalar(t *testing.T) {
	pkg := analyze(t, `package design
scalar RawDoc bytes @format(raw)
type X { photos RawDoc?  cover RawDoc }`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)
	mustContainAll(t, src,
		`"github.com/craftgodotdev/craftgo/pkg/wire"`,
		"type RawDoc = wire.Raw",
		"Photos *wire.Raw `json:\"photos,omitempty\"`",
		"Cover  wire.Raw  `json:\"cover\"`",
	)
}

// A required raw field gets a length check, not a nil compare: wire.Raw
// is a slice the codec leaves empty when the key is absent. An explicit
// null is NOT absent for it - it decodes to the four bytes `null` - which
// is the difference the shape is for.
func TestValidateRequiredRawBytesChecksLen(t *testing.T) {
	src := runValidateGen(t, `package design
type X { payload bytes @format(raw)  meta bytes? @format(raw) }`)
	mustContainAll(t, src, "if len(v.Payload) == 0 {", `"payload: required"`)
	if strings.Contains(src, "v.Meta == nil") {
		t.Errorf("an optional raw field needs no presence check:\n%s", src)
	}
}

// A raw scalar declares no validator, so no Validate() method is emitted
// for it and no field calls one: the scalar is an alias for a type this
// package cannot define a method on, and either half alone would not
// compile.
func TestValidateRawBytesScalarHasNoMethod(t *testing.T) {
	src := runValidateGen(t, `package design
scalar RawDoc bytes @format(raw)
type X { photos RawDoc }`)
	mustContainAll(t, src, "if len(v.Photos) == 0 {")
	for _, banned := range []string{"func (v RawDoc) Validate()", "v.Photos.Validate()"} {
		if strings.Contains(src, banned) {
			t.Errorf("a raw scalar is an alias; %q would not compile:\n%s", banned, src)
		}
	}
}

func TestGenerateTypesBuiltins(t *testing.T) {
	pkg := analyze(t, `package design
type X { blob bytes  raw any  upload file  at datetime  seen datetime? }`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)
	mustContainAll(t, src,
		"[]byte",
		`"mime/multipart"`,
		`"time"`,
		"time.Time             `json:\"at\"`",
		"*time.Time            `json:\"seen,omitempty\"`",
	)
}

// TestGenerateTypesGenericInstance checks the Go-1.18-generics output
// shape: a generic decl renders with type-parameter brackets, and
// references to it use Go generic syntax `Name[Arg1, Arg2]`.
func TestGenerateTypesGenericInstance(t *testing.T) {
	pkg := analyze(t, `package design
type User {}
type Org {}
type Pair<A, B> { left A  right B }
type UserOrgPair { p Pair<User, Org> }`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)
	// Generic decl lands as a Go generic struct.
	if !strings.Contains(src, "type Pair[A any, B any] struct") {
		t.Errorf("expected `type Pair[A any, B any] struct` in:\n%s", src)
	}
	// Instance reference uses Go generic syntax.
	if !strings.Contains(src, "Pair[User, Org]") {
		t.Errorf("expected `Pair[User, Org]` instance reference in:\n%s", src)
	}
}

func TestResolveTypeRefGenericArgs(t *testing.T) {
	// Generic instantiation must propagate through resolveTypeRef so
	// service / handler signatures land as `Page[types.User]` rather
	// than bare `Page` (which Go rejects with "cannot use generic
	// type without instantiation"). Coverage:
	//
	//   - local generic with local arg → both qualified via `types`
	//   - local generic with cross-pkg arg → arg keeps its qualifier
	//   - cross-pkg generic with local arg → arg picks up local alias
	//   - nested generic (`Page<Envelope<User>>`)
	//   - multi-arg generic
	cross := crossPkg{"shared": "github.com/x/internal/types/shared"}
	cases := []struct {
		name      string
		ref       *ast.NamedTypeRef
		wantAlias string
		wantBare  string
	}{
		{
			name:      "local generic local arg",
			ref:       &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"Page"}}, Args: []*ast.TypeRef{{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"User"}}}}}},
			wantAlias: "types",
			wantBare:  "Page[types.User]",
		},
		{
			name:      "local generic cross-pkg arg",
			ref:       &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"Page"}}, Args: []*ast.TypeRef{{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"shared", "User"}}}}}},
			wantAlias: "types",
			wantBare:  "Page[shared.User]",
		},
		{
			name:      "cross-pkg generic local arg",
			ref:       &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"shared", "Page"}}, Args: []*ast.TypeRef{{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"User"}}}}}},
			wantAlias: "shared",
			wantBare:  "Page[types.User]",
		},
		{
			name: "nested generic local",
			ref: &ast.NamedTypeRef{
				Name: &ast.QualifiedIdent{Parts: []string{"Page"}},
				Args: []*ast.TypeRef{{Named: &ast.NamedTypeRef{
					Name: &ast.QualifiedIdent{Parts: []string{"Envelope"}},
					Args: []*ast.TypeRef{{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"User"}}}}},
				}}},
			},
			wantAlias: "types",
			wantBare:  "Page[types.Envelope[types.User]]",
		},
		{
			name: "multi-arg generic",
			ref: &ast.NamedTypeRef{
				Name: &ast.QualifiedIdent{Parts: []string{"Pair"}},
				Args: []*ast.TypeRef{
					{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"User"}}}},
					{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"shared", "Email"}}}},
				},
			},
			wantAlias: "types",
			wantBare:  "Pair[types.User, shared.Email]",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			alias, bare, _, _ := resolveTypeRef(c.ref, cross)
			if alias != c.wantAlias || bare != c.wantBare {
				t.Errorf("got (alias=%q, bare=%q), want (alias=%q, bare=%q)", alias, bare, c.wantAlias, c.wantBare)
			}
		})
	}
}

// resolveTypeRef reports whether the rendered reference reaches the local
// types package. Callers import `types` from that rather than searching the
// rendered text: a design package whose name ends in `types` puts the
// substring `types.` into a reference that touches nothing local.
func TestResolveTypeRefReportsLocalUse(t *testing.T) {
	cross := crossPkg{
		"shared":   "example.com/app/internal/types/shared",
		"paytypes": "example.com/app/internal/types/paytypes",
		"genpkg":   "example.com/app/internal/types/genpkg",
	}
	named := func(parts ...string) *ast.NamedTypeRef {
		return &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: parts}}
	}
	generic := func(outer *ast.NamedTypeRef, args ...*ast.NamedTypeRef) *ast.NamedTypeRef {
		for _, a := range args {
			outer.Args = append(outer.Args, &ast.TypeRef{Named: a})
		}
		return outer
	}
	cases := []struct {
		name string
		ref  *ast.NamedTypeRef
		want bool
	}{
		{"local type", named("User"), true},
		{"cross-pkg type", named("shared", "User"), false},
		{"cross-pkg generic, local arg", generic(named("shared", "Page"), named("User")), true},
		{"cross-pkg generic, cross-pkg arg", generic(named("shared", "Page"), named("shared", "User")), false},
		{"cross-pkg generic over a package named ...types", generic(named("genpkg", "GBox"), named("paytypes", "PItem")), false},
		{"local generic over a package named ...types", generic(named("Page"), named("paytypes", "PItem")), true},
		{"nested generic reaching a local arg", generic(named("genpkg", "GBox"), generic(named("shared", "Page"), named("User"))), true},
		{"nil ref", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, bare, _, use := resolveTypeRef(c.ref, cross)
			if use.LocalTypes != c.want {
				t.Errorf("rendered %q: LocalTypes = %v, want %v", bare, use.LocalTypes, c.want)
			}
		})
	}
}

func TestRenderMixinQualifiedRef(t *testing.T) {
	// Same-package mixin renders as bare ident (Go would refuse a
	// qualified ref to itself). Cross-package mixin keeps the
	// qualifier - Go requires `pkg.Type` for an embedded type
	// declared elsewhere, and the consuming file already carries
	// the matching import.
	cases := []struct {
		name  string
		parts []string
		want  string
	}{
		{name: "same-package", parts: []string{"Pagination"}, want: "\tPagination\n"},
		{name: "cross-package", parts: []string{"shared", "Pagination"}, want: "\tshared.Pagination\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &ast.Mixin{Ref: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: c.parts}}}
			if got := renderMixin(m); got != c.want {
				t.Errorf("renderMixin(%v) = %q, want %q", c.parts, got, c.want)
			}
		})
	}
}

func TestGenerateTypesMixin(t *testing.T) {
	pkg := analyze(t, `package design
type Profile { id string }
type User { Profile  name string }`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)
	norm := strings.Join(strings.Fields(src), " ")
	if !strings.Contains(norm, "type User struct { Profile Name") {
		t.Errorf("mixin embed not emitted:\n%s", src)
	}
}

// TestGenerateTypesDeprecated covers `@deprecated` on both type and
// field. The Go-side emission must use the canonical `// Deprecated: …`
// line so `go vet` and `staticcheck` flag callers; per-field
// deprecation lives in the field's own doc block.
func TestGenerateTypesDeprecated(t *testing.T) {
	pkg := analyze(t, `package design
@deprecated("use NewBook instead")
type LegacyBook {
    title    string
    sku      string @deprecated
    priceUsd int    @deprecated("use priceCents instead")
}`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)

	// Type-level deprecation message.
	if !strings.Contains(src, "// Deprecated: use NewBook instead") {
		t.Errorf("expected type-level Deprecated comment:\n%s", src)
	}
	// Field-level deprecation with explicit reason.
	if !strings.Contains(src, "// Deprecated: use priceCents instead") {
		t.Errorf("expected field-level Deprecated comment with reason:\n%s", src)
	}
	// Field-level deprecation without reason gets the generic fallback.
	if !strings.Contains(src, "// Deprecated: this entity is deprecated") {
		t.Errorf("expected fallback Deprecated comment for bare @deprecated:\n%s", src)
	}
}

// TestGenerateTypesNullable pins the Go-side wiring for `@nullable`:
//
//   - value type (string) → `*T` field, no omitempty (null-emitting).
//   - already-nilable ([]byte slice / `*FileHeader`) → no extra wrap,
//     just drop omitempty.
//   - combined `T? @nullable` → still `*T` (no double-wrap), omitempty
//     applies because the `?` suffix takes precedence: a nil pointer
//     must round-trip as absent on the wire, not as explicit JSON
//     `null`. The combined form is also flagged redundant at semantic
//     time so users converge on plain `T?`.
//   - non-nullable required field → unchanged (`T` value, no omitempty).
func TestGenerateTypesNullable(t *testing.T) {
	pkg := analyze(t, `package design
type T {
    plain    string
    nullStr  string @nullable
    nullBlob bytes  @nullable
    optNull  string? @nullable
}`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)

	want := []struct{ ident, typ, tag string }{
		{"Plain", "string", `json:"plain"`},
		{"NullStr", "*string", `json:"nullStr"`},           // @nullable only → no omitempty
		{"NullBlob", "[]byte", `json:"nullBlob"`},          // already nilable + no omitempty
		{"OptNull", "*string", `json:"optNull,omitempty"`}, // `?` dominates → omitempty
	}
	for _, w := range want {
		if !lineHasField(src, w.ident, w.typ) || !lineHasField(src, w.ident, w.tag) {
			t.Errorf("expected %s %s with tag %s in:\n%s", w.ident, w.typ, w.tag, src)
		}
	}
}

// A package that declares no type and no scalar - one holding only
// services, or only consume middleware - puts nothing in types.go, so
// neither the file nor the directory it would sit in is written.
func TestGenerateTypesServiceOnlyPackageWritesNothing(t *testing.T) {
	pkg := analyze(t, `package design
service Health {
	get Ping /ping {}
}`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := generateValidators(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "design")); !os.IsNotExist(err) {
		t.Errorf("expected no package directory, stat returned %v", err)
	}
}

// A scalar is a Go defined type, so a package declaring one still writes
// types.go even with no `type` in it.
func TestGenerateTypesScalarWithoutTypeStillWrites(t *testing.T) {
	pkg := analyze(t, `package design
scalar Email string @format(email)`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(out)
	mustParseGo(t, src)
	if !strings.Contains(src, "type Email string") {
		t.Errorf("expected the scalar defined type in:\n%s", src)
	}
}

// The design dropping its last type writes no types.go and no
// validate.go: the files would carry a package clause and nothing else.
// What an earlier run put there is the sweep's to take, not the
// emitter's.
func TestGenerateTypesWritesNothingWithoutTypes(t *testing.T) {
	pkg := analyze(t, `package design
service Health {
	get Ping /ping {}
}`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := generateValidators(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "design")); !os.IsNotExist(err) {
		t.Errorf("the package directory must be left uncreated, stat returned %v", err)
	}
}

func TestGenerateTypesNoPackageName(t *testing.T) {
	pkg := &semantic.Package{Types: map[string]*ast.TypeDecl{}}
	if err := generateTypes(pkg, t.TempDir(), nil); err == nil {
		t.Error("expected error for missing pkg name")
	}
}

// ---------- enums ----------

// TestGenerateEnums covers all three enum kinds (bare / int /
// string) via golden snapshot files. Each case generates the enum
// and compares against the reference at testdata/golden/enums-<kind>.go;
// run `go test -update` to refresh after an intentional emit
// change. The snapshot beats hand-written `strings.Contains`
// chains because a regression shows the entire offending hunk
// inline, not a single missing substring.
func TestGenerateEnums(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		golden string
	}{
		{
			name:   "bare",
			src:    `package design` + "\n" + `enum Color { Red  Green  Blue }`,
			golden: "enums-bare.go",
		},
		{
			name:   "int",
			src:    `package design` + "\n" + `enum Priority { Low = 1  High = 99 }`,
			golden: "enums-int.go",
		},
		{
			name:   "string",
			src:    `package design` + "\n" + `enum Status { Active = "active"  Pending = "pending" }`,
			golden: "enums-string.go",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pkg := analyze(t, c.src)
			dir := t.TempDir()
			if err := generateEnums(pkg, dir); err != nil {
				t.Fatal(err)
			}
			out, err := os.ReadFile(filepath.Join(dir, "design", "enums.go"))
			if err != nil {
				t.Fatal(err)
			}
			mustParseGo(t, string(out))
			expectGolden(t, c.golden, string(out))
		})
	}
}

func TestGenerateEnumsEmpty(t *testing.T) {
	pkg := analyze(t, `package design
type X {}`)
	dir := t.TempDir()
	if err := generateEnums(pkg, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "design", "enums.go")); !os.IsNotExist(err) {
		t.Error("expected no enums.go for empty enum set")
	}
}

func TestGenerateEnumsNoPackageName(t *testing.T) {
	pkg := &semantic.Package{Enums: map[string]*ast.EnumDecl{"X": {Name: "X"}}}
	if err := generateEnums(pkg, t.TempDir()); err == nil {
		t.Error("expected error")
	}
}

// ---------- errors ----------

func TestGenerateErrorsShort(t *testing.T) {
	pkg := analyze(t, `package design
error NotFound UserNotFound`)
	dir := t.TempDir()
	if err := generateErrors(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "errors.go"))
	src := string(out)
	mustParseGo(t, src)
	// gofmt aligns struct-literal field assignments and tags with extra
	// whitespace; collapse to single spaces before substring matching.
	norm := strings.Join(strings.Fields(src), " ")
	for _, want := range []string{
		`const ErrCodeUserNotFound = "USER_NOT_FOUND"`,
		"type UserNotFoundErr struct {",
		"func NewUserNotFoundErr() *UserNotFoundErr",
		"code: ErrCodeUserNotFound",
		`message: "Not found"`,
		"return e.message",
		"return e.code", // ErrCode() accessor
		"func (e *UserNotFoundErr) ErrCode() string",
		"return 404",
	} {
		if !strings.Contains(norm, want) {
			t.Errorf("missing %q in:\n%s", want, src)
		}
	}
	// Internal-only contract: code/message live as unexported struct
	// fields, never on the wire. No body struct should be emitted for
	// a body-less error.
	for _, forbidden := range []string{
		`Code string`,    // would be exported
		`Message string`, // would be exported
		`json:"code"`,
		`json:"message"`,
		"WithMessage",
		"WithCode",
		"UserNotFoundBody", // no body struct without user fields
	} {
		if strings.Contains(norm, forbidden) {
			t.Errorf("forbidden token %q present:\n%s", forbidden, src)
		}
	}
}

func TestGenerateErrorsCustomFields(t *testing.T) {
	pkg := analyze(t, `package design
error BadRequest Validation {
    fields  string[]
}`)
	dir := t.TempDir()
	if err := generateErrors(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "errors.go"))
	src := string(out)
	mustParseGo(t, src)
	// gofmt may align struct fields with extra spaces; collapse whitespace
	// before substring matching.
	norm := strings.Join(strings.Fields(src), " ")
	// Body struct holds the user-declared field with its JSON tag.
	if !strings.Contains(norm, "type ValidationBody struct") {
		t.Errorf("missing body struct:\n%s", src)
	}
	if !strings.Contains(norm, `Fields []string `+"`json:\"fields\"`") {
		t.Errorf("missing custom field on body struct:\n%s", src)
	}
	// Err type embeds the body struct (no per-field params on the ctor).
	if !strings.Contains(src, "func NewValidationErr(body ValidationBody) *ValidationErr") {
		t.Errorf("constructor must take a body struct:\n%s", src)
	}
}

func TestGenerateErrorsUserDeclaresCodeAndMessage(t *testing.T) {
	// User declaring `code` / `message` in the DSL produces exported
	// wire fields (Go `Code` / `Message`); they coexist with the
	// framework's unexported metadata fields without conflict.
	pkg := analyze(t, `package design
error Internal Boom {
    code string? @default("BOOM_500")
    message string? @default("kaboom")
}`)
	dir := t.TempDir()
	if err := generateErrors(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "errors.go"))
	src := string(out)
	mustParseGo(t, src)
	norm := strings.Join(strings.Fields(src), " ")

	// User wire fields surface on the body struct as exported Go names
	// with the DSL JSON tag; an optional field carries omitempty (the same
	// canonical jsonTag rule regular types use).
	if !strings.Contains(norm, `Code *string `+"`json:\"code,omitempty\"`") {
		t.Errorf("user `code?` field should appear on body struct as *Code:\n%s", src)
	}
	if !strings.Contains(norm, `Message *string `+"`json:\"message,omitempty\"`") {
		t.Errorf("user `message?` field should appear on body struct as *Message:\n%s", src)
	}
	// Internal metadata stays present and unexported on the err type.
	if !strings.Contains(norm, "code string message string") {
		t.Errorf("err type must keep unexported code/message metadata:\n%s", src)
	}
	if !strings.Contains(src, "return 500") {
		t.Errorf("status:\n%s", src)
	}
}

func TestGenerateErrorsSmartSuffix(t *testing.T) {
	pkg := analyze(t, `package design
error NotFound UserNotFoundError
error BadRequest ValidationErr`)
	dir := t.TempDir()
	if err := generateErrors(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "errors.go"))
	src := string(out)
	if strings.Contains(src, "UserNotFoundErrorErr") {
		t.Error("smart suffix should keep ...Error name")
	}
	if strings.Contains(src, "ValidationErrErr") {
		t.Error("smart suffix should keep ...Err name")
	}
}

func TestGenerateErrorsResponseBindings(t *testing.T) {
	pkg := analyze(t, `package design
error TooManyRequests RateLimited {
    retryAfter   string  @header
    sessionToken string  @cookie
    bucket       string?
}`)
	dir := t.TempDir()
	if err := generateErrors(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "errors.go"))
	src := string(out)
	mustParseGo(t, src)
	norm := strings.Join(strings.Fields(src), " ")

	// Header / cookie fields carry json:"-" so they don't double up in the
	// JSON body alongside the response-header / cookie write, plus a binding
	// key naming the wire location (header:"retryAfter", cookie:"sessionToken").
	if !strings.Contains(norm, `RetryAfter string `+"`json:\"-\" header:\"retryAfter\"`") {
		t.Errorf("retryAfter should be json:\"-\" header:\"retryAfter\":\n%s", src)
	}
	if !strings.Contains(norm, `SessionToken string `+"`json:\"-\" cookie:\"sessionToken\"`") {
		t.Errorf("sessionToken should be json:\"-\" cookie:\"sessionToken\":\n%s", src)
	}
	// Optional non-bound field keeps its real JSON tag, with omitempty (the
	// canonical jsonTag rule - an optional error-body field is omittable).
	if !strings.Contains(norm, `Bucket *string `+"`json:\"bucket,omitempty\"`") {
		t.Errorf("bucket should keep its DSL JSON tag:\n%s", src)
	}
	// WriteResponseHeaders must be emitted with the expected wire writes.
	mustContainAll(t, src,
		"func (e *RateLimitedErr) WriteResponseHeaders(w http.ResponseWriter)",
		`w.Header().Set("retryAfter", e.RetryAfter)`,
		`http.SetCookie(w, &http.Cookie{Name: "sessionToken", Value: e.SessionToken})`,
		`"net/http"`,
	)
}

// A @sensitive field on an error body must be tagged json:"-" - the same
// canonical jsonTag rule regular types follow - so a server-only secret
// never rides the error response wire. (Regression: the error emitter once
// re-derived the tag and leaked the value as json:"<name>".)
func TestGenerateErrorsSensitiveFieldExcludedFromBody(t *testing.T) {
	pkg := analyze(t, `package design
error Forbidden Boom {
    secret string @sensitive
    note   string
}`)
	dir := t.TempDir()
	if err := generateErrors(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "errors.go"))
	src := string(out)
	mustParseGo(t, src)
	norm := strings.Join(strings.Fields(src), " ")
	if !strings.Contains(norm, `Secret string `+"`json:\"-\"`") {
		t.Errorf("@sensitive error field must be json:\"-\" (server-only), not leaked:\n%s", src)
	}
	if strings.Contains(norm, `json:\"secret\"`) {
		t.Errorf("@sensitive error field secret leaked onto the wire:\n%s", src)
	}
}

// TestGenerateErrorsResponseBindingsNonString pins the non-string
// @header / @cookie formatting on the error path: int / bool values are
// rendered via strconv (and the strconv import is pulled in), mirroring
// the success-response writer.
func TestGenerateErrorsResponseBindingsNonString(t *testing.T) {
	pkg := analyze(t, `package design
scalar Cents int
error TooManyRequests RateLimited {
    retryAfter int   @header("Retry-After")
    cost       Cents @header("X-Cost")
    throttled  bool  @cookie("throttled")
}`)
	dir := t.TempDir()
	if err := generateErrors(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "errors.go"))
	src := string(out)
	mustParseGo(t, src)
	mustContainAll(t, src,
		`"net/http"`,
		`"strconv"`,
		"func (e *RateLimitedErr) WriteResponseHeaders(w http.ResponseWriter)",
		`w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfter))`,
		// A numeric scalar resolves to its primitive (int) and formats
		// the same as a plain int field - proving local scalar
		// resolution works on the error path too.
		`w.Header().Set("X-Cost", strconv.FormatInt(int64(e.Cost), 10))`,
		`http.SetCookie(w, &http.Cookie{Name: "throttled", Value: strconv.FormatBool(e.Throttled)})`,
	)
}

// TestGenerateErrorsCrossPkgScalarHeader pins the cross-package scalar
// resolution on the error path. A non-string scalar imported from
// another package (shared.Cents → int) must resolve through the
// ProjectResolver and format via strconv.FormatInt - NOT the broken
// string(int) conversion the local-only path would emit (wrong runtime
// value + a go vet diagnostic). This exercises the resolver that
// GenerateErrors threads through; the local-scalar test above
// would pass even without it.
func TestGenerateErrorsCrossPkgScalarHeader(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"shared/types.craftgo": `package shared
scalar Cents int @gte(0)`,
		"app/errors.craftgo": `package app
import "shared"
error TooManyRequests RateLimited {
    cost shared.Cents @header("X-Cost")
}`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	appPkg := proj.Packages["app"]
	if appPkg == nil {
		t.Fatal("app package missing from project")
	}
	dir := t.TempDir()
	r := buildProjectResolver(proj, newFixtureConfig(), "app")
	if err := generateErrors(appPkg, dir, r); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "app", "errors.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(out)
	mustParseGo(t, src)
	if !strings.Contains(src, `w.Header().Set("X-Cost", strconv.FormatInt(int64(e.Cost), 10))`) {
		t.Errorf("cross-pkg int scalar should format via strconv.FormatInt; got:\n%s", src)
	}
	if strings.Contains(src, `string(e.Cost)`) {
		t.Errorf("cross-pkg int scalar must NOT use string(int) (wrong value + go vet flags it):\n%s", src)
	}
}

func TestGenerateErrorsNoBindingsNoHTTPImport(t *testing.T) {
	// Without @header / @cookie fields, errors.go must NOT carry a
	// `net/http` import or a WriteResponseHeaders method.
	pkg := analyze(t, `package design
error NotFound UserNotFound`)
	dir := t.TempDir()
	if err := generateErrors(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "errors.go"))
	src := string(out)
	mustParseGo(t, src)
	if strings.Contains(src, `"net/http"`) {
		t.Errorf("unexpected net/http import for wire.Binding-free errors:\n%s", src)
	}
	if strings.Contains(src, "WriteResponseHeaders") {
		t.Errorf("unexpected WriteResponseHeaders method:\n%s", src)
	}
}

func TestGenerateErrorsEmpty(t *testing.T) {
	pkg := analyze(t, `package design
type X {}`)
	dir := t.TempDir()
	if err := generateErrors(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "design", "errors.go")); !os.IsNotExist(err) {
		t.Error("expected no errors.go")
	}
}

func TestGenerateErrorsNoPackageName(t *testing.T) {
	pkg := &semantic.Package{Errors: map[string]*ast.ErrorDecl{"X": {Name: "X", Category: "NotFound"}}}
	if err := generateErrors(pkg, t.TempDir(), nil); err == nil {
		t.Error("expected error")
	}
}

// ---------- helpers ----------

func TestGoFieldName(t *testing.T) {
	cases := map[string]string{
		"id":        "ID",
		"userId":    "UserID",
		"user_id":   "UserID",
		"http_url":  "HTTPURL",
		"firstName": "FirstName",
		"":          "",
	}
	for in, want := range cases {
		if got := goFieldName(in); got != want {
			t.Errorf("%q → %q want %q", in, got, want)
		}
	}
}

// TestGoTypeRefOptionalNilableBase pins the no-redundant-pointer rule:
// optional fields whose base Go type is already nil-zeroable (slice,
// map, pointer-shaped builtin, interface) must NOT receive an extra
// `*`. Value-type optionals (string?, struct?) still get the pointer so
// "absent" remains distinguishable from the zero value.
func TestGoTypeRefOptionalNilableBase(t *testing.T) {
	pkg := analyze(t, `package design
type User { name string }
type T {
    bytesOpt    bytes?
    fileOpt     file?
    anyOpt      any?
    arrayOpt    string[]?
    mapOpt      map<string, int>?
    stringOpt   string?
    structOpt   User?
}`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)

	want := []struct {
		ident, tag string
	}{
		{"BytesOpt", "[]byte"},
		{"FileOpt", "*multipart.FileHeader"},
		{"AnyOpt", "any"},
		{"ArrayOpt", "[]string"},
		{"MapOpt", "map[string]int"},
		// Value-type optionals - pointer is still required.
		{"StringOpt", "*string"},
		{"StructOpt", "*User"},
	}
	for _, w := range want {
		if !lineHasField(src, w.ident, w.tag) {
			t.Errorf("expected field %s with type %q in:\n%s", w.ident, w.tag, src)
		}
	}
	// Negative: no double-pointer or pointer-to-slice anywhere.
	mustContainNone(t, src,
		"**",
	)
}

// lineHasField reports whether `src` has any line containing both the
// identifier and the type expression. Whitespace between them is
// arbitrary so gofmt's column alignment doesn't break the assertion.
func lineHasField(src, ident, typ string) bool {
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, ident) && strings.Contains(line, typ) {
			// Reject pointer-to-typ accidentally matching the substring.
			// e.g. "*[]byte" contains "[]byte" - we want only the bare form.
			if strings.Contains(line, "*"+typ) {
				continue
			}
			return true
		}
	}
	return false
}

func TestGoTypeRefNil(t *testing.T) {
	if goTypeRef(nil) != "" {
		t.Error()
	}
}

func TestScreamingSnake(t *testing.T) {
	cases := map[string]string{
		"UserNotFound": "USER_NOT_FOUND",
		"DBError":      "DB_ERROR",
	}
	for in, want := range cases {
		if got := screamingSnake(in); got != want {
			t.Errorf("%q → %q want %q", in, got, want)
		}
	}
}

func TestErrSuffix(t *testing.T) {
	cases := map[string]string{
		"NotFound":  "NotFoundErr",
		"BoomErr":   "BoomErr",
		"BoomError": "BoomError",
	}
	for in, want := range cases {
		if got := idents.ErrorTypeName(in); got != want {
			t.Errorf("%q → %q want %q", in, got, want)
		}
	}
}

// TestGenerateErrorsEmbedsMixin checks that a mixin embedded in an error
// body is embedded in the generated Go body struct, so the server can
// populate the fields the OpenAPI allOf advertises and produce the
// spec-conformant 4xx response.
func TestGenerateErrorsEmbedsMixin(t *testing.T) {
	pkg := analyze(t, `package design
type Audit { actor string  at string @format(datetime) }
error Forbidden Denied {
    Audit
    reason string
}`)
	dir := t.TempDir()
	if err := generateErrors(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "errors.go"))
	src := string(out)
	mustParseGo(t, src)
	norm := strings.Join(strings.Fields(src), " ")
	if !strings.Contains(norm, "type DeniedBody struct { Audit") {
		t.Errorf("error body must embed the Audit mixin:\n%s", src)
	}
}

// A scalar over the nilable `bytes` primitive lowers to the bare named
// slice, so an optional / @nullable field of it must NOT carry a
// redundant pointer - it renders like a raw `bytes` field, and its
// validator nil-guards before calling the scalar's own Validate().
func TestScalarOverBytesNullableRendersWithoutPointer(t *testing.T) {
	root, files := projectFiles(t, map[string]string{
		"m/m.craftgo": `package m
scalar Blob bytes @minLength(4)
type Doc {
  reqBlob Blob
  nulBlob Blob @nullable
  optBlob Blob?
}`,
	})
	proj, diags := semantic.AnalyzeProject(files, semantic.Options{DesignRoot: root})
	if len(diags) > 0 {
		t.Fatalf("semantic: %v", diags)
	}
	dir := t.TempDir()
	mPkg := proj.Packages["m"]
	if err := generateTypes(mPkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := generateValidators(mPkg, dir, &projectResolver{Resolver: semantic.NewResolver(proj, "m")}); err != nil {
		t.Fatal(err)
	}
	types, _ := os.ReadFile(filepath.Join(dir, "m", "types.go"))
	val, _ := os.ReadFile(filepath.Join(dir, "m", "validate.go"))
	mustParseGo(t, string(types))
	mustParseGo(t, string(val))
	ts := string(types)
	if strings.Contains(ts, "*Blob") {
		t.Errorf("scalar-over-bytes field rendered with a redundant pointer (*Blob):\n%s", ts)
	}
	mustContainAll(t, ts,
		"NulBlob Blob `json:\"nulBlob\"`",
		"OptBlob Blob `json:\"optBlob,omitempty\"`",
	)
	// The nullable/optional scalar's Validate() stays nil-guarded so a
	// null / absent value skips the scalar's own constraint.
	mustContainAll(t, string(val),
		"if v.NulBlob != nil {",
		"if v.OptBlob != nil {",
	)
}
