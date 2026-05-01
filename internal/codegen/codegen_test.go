package codegen

import (
	"go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
	craftparser "github.com/dropship-dev/craftgo/internal/parser"
	"github.com/dropship-dev/craftgo/internal/semantic"
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
	if _, err := parser.ParseFile(gotoken.NewFileSet(), "out.go", src, parser.AllErrors); err != nil {
		t.Fatalf("generated Go does not parse: %v\n--- source ---\n%s", err, src)
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
	if err := GenerateTypes(pkg, dir); err != nil {
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

func TestGenerateTypesBasic(t *testing.T) {
	pkg := analyze(t, `package design
type User { id string  name string  age int? }`)
	dir := t.TempDir()
	if err := GenerateTypes(pkg, dir); err != nil {
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
	if err := GenerateTypes(pkg, dir); err != nil {
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

func TestGenerateTypesBuiltins(t *testing.T) {
	pkg := analyze(t, `package design
type X { blob bytes  raw any  upload file }`)
	dir := t.TempDir()
	if err := GenerateTypes(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)
	for _, want := range []string{"[]byte", "any", "*multipart.FileHeader",
		`"mime/multipart"`} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q in:\n%s", want, src)
		}
	}
}

// TestGenerateTypesGenericInstance pins the Go-1.18-generics output
// shape: a generic decl renders with type-parameter brackets, and
// references to it use Go generic syntax `Name[Arg1, Arg2]` rather
// than the legacy "OfArg1AndArg2" rename convention.
func TestGenerateTypesGenericInstance(t *testing.T) {
	pkg := analyze(t, `package design
type User {}
type Org {}
type Pair<A, B> { left A  right B }
type UserOrgPair { p Pair<User, Org> }`)
	dir := t.TempDir()
	if err := GenerateTypes(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)
	// Generic decl now lands as a Go generic struct.
	if !strings.Contains(src, "type Pair[A any, B any] struct") {
		t.Errorf("expected `type Pair[A any, B any] struct` in:\n%s", src)
	}
	// Instance reference uses Go generic syntax.
	if !strings.Contains(src, "Pair[User, Org]") {
		t.Errorf("expected `Pair[User, Org]` instance reference in:\n%s", src)
	}
}

func TestGenerateTypesMixin(t *testing.T) {
	pkg := analyze(t, `package design
type Profile { id string }
type User { Profile  name string }`)
	dir := t.TempDir()
	if err := GenerateTypes(pkg, dir); err != nil {
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
	if err := GenerateTypes(pkg, dir); err != nil {
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
//   - combined `T? @nullable` → still `*T` (no double-wrap), no omitempty.
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
	if err := GenerateTypes(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)

	want := []struct{ ident, typ, tag string }{
		{"Plain", "string", `json:"plain"`},
		{"NullStr", "*string", `json:"nullStr"`},   // pointer + no omitempty
		{"NullBlob", "[]byte", `json:"nullBlob"`},  // already nilable + no omitempty
		{"OptNull", "*string", `json:"optNull"`},   // single pointer, no omitempty
	}
	for _, w := range want {
		if !lineHasField(src, w.ident, w.typ) || !lineHasField(src, w.ident, w.tag) {
			t.Errorf("expected %s %s with tag %s in:\n%s", w.ident, w.typ, w.tag, src)
		}
	}
	// `omitempty` must NOT appear anywhere - every field is either
	// required (no omitempty) or @nullable (no omitempty).
	if strings.Contains(src, "omitempty") {
		t.Errorf("@nullable fields should not carry omitempty:\n%s", src)
	}
}

func TestGenerateTypesNoPackageName(t *testing.T) {
	pkg := &semantic.Package{Types: map[string]*ast.TypeDecl{}}
	if err := GenerateTypes(pkg, t.TempDir()); err == nil {
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
			if err := GenerateEnums(pkg, dir); err != nil {
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
	if err := GenerateEnums(pkg, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "design", "enums.go")); !os.IsNotExist(err) {
		t.Error("expected no enums.go for empty enum set")
	}
}

func TestGenerateEnumsNoPackageName(t *testing.T) {
	pkg := &semantic.Package{Enums: map[string]*ast.EnumDecl{"X": {Name: "X"}}}
	if err := GenerateEnums(pkg, t.TempDir()); err == nil {
		t.Error("expected error")
	}
}

// ---------- errors ----------

func TestGenerateErrorsShort(t *testing.T) {
	pkg := analyze(t, `package design
error NotFound UserNotFound`)
	dir := t.TempDir()
	if err := GenerateErrors(pkg, dir); err != nil {
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
	if err := GenerateErrors(pkg, dir); err != nil {
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
    code     string @default("BOOM_500")
    message  string @default("kaboom")
}`)
	dir := t.TempDir()
	if err := GenerateErrors(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "errors.go"))
	src := string(out)
	mustParseGo(t, src)
	norm := strings.Join(strings.Fields(src), " ")

	// User wire fields surface on the body struct as exported Go names
	// with the DSL JSON tag.
	if !strings.Contains(norm, `Code string `+"`json:\"code\"`") {
		t.Errorf("user `code` field should appear on body struct as exported Code:\n%s", src)
	}
	if !strings.Contains(norm, `Message string `+"`json:\"message\"`") {
		t.Errorf("user `message` field should appear on body struct as exported Message:\n%s", src)
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
	if err := GenerateErrors(pkg, dir); err != nil {
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
	if err := GenerateErrors(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "errors.go"))
	src := string(out)
	mustParseGo(t, src)
	norm := strings.Join(strings.Fields(src), " ")

	// Header / cookie fields must carry json:"-" so they don't double up
	// in the JSON body alongside the response-header / cookie write.
	if !strings.Contains(norm, `RetryAfter string `+"`json:\"-\"`") {
		t.Errorf("retryAfter should have json:\"-\":\n%s", src)
	}
	if !strings.Contains(norm, `SessionToken string `+"`json:\"-\"`") {
		t.Errorf("sessionToken should have json:\"-\":\n%s", src)
	}
	// Optional non-bound field keeps its real JSON tag.
	if !strings.Contains(norm, `Bucket *string `+"`json:\"bucket\"`") {
		t.Errorf("bucket should keep its DSL JSON tag:\n%s", src)
	}
	// WriteResponseHeaders must be emitted with the expected wire writes.
	for _, want := range []string{
		"func (e *RateLimitedErr) WriteResponseHeaders(w http.ResponseWriter)",
		`w.Header().Set("retryAfter", e.RetryAfter)`,
		`http.SetCookie(w, &http.Cookie{Name: "sessionToken", Value: e.SessionToken})`,
		`"net/http"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q:\n%s", want, src)
		}
	}
}

func TestGenerateErrorsNoBindingsNoHTTPImport(t *testing.T) {
	// Without @header / @cookie fields, errors.go must NOT carry a
	// `net/http` import or a WriteResponseHeaders method.
	pkg := analyze(t, `package design
error NotFound UserNotFound`)
	dir := t.TempDir()
	if err := GenerateErrors(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "errors.go"))
	src := string(out)
	mustParseGo(t, src)
	if strings.Contains(src, `"net/http"`) {
		t.Errorf("unexpected net/http import for binding-free errors:\n%s", src)
	}
	if strings.Contains(src, "WriteResponseHeaders") {
		t.Errorf("unexpected WriteResponseHeaders method:\n%s", src)
	}
}

func TestGenerateErrorsEmpty(t *testing.T) {
	pkg := analyze(t, `package design
type X {}`)
	dir := t.TempDir()
	if err := GenerateErrors(pkg, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "design", "errors.go")); !os.IsNotExist(err) {
		t.Error("expected no errors.go")
	}
}

func TestGenerateErrorsNoPackageName(t *testing.T) {
	pkg := &semantic.Package{Errors: map[string]*ast.ErrorDecl{"X": {Name: "X", Category: "NotFound"}}}
	if err := GenerateErrors(pkg, t.TempDir()); err == nil {
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
		if got := GoFieldName(in); got != want {
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
	if err := GenerateTypes(pkg, dir); err != nil {
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
	for _, bad := range []string{"**", "*[]byte", "*[]string", "*map[", "*any"} {
		if strings.Contains(src, bad) {
			t.Errorf("found redundant %q in generated source:\n%s", bad, src)
		}
	}
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
	if GoTypeRef(nil) != "" {
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
		if got := errSuffix(in); got != want {
			t.Errorf("%q → %q want %q", in, got, want)
		}
	}
}
