package codegen

import (
	"go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/internal/ast"
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
	if len(diags) > 0 {
		t.Fatalf("semantic errors: %v", diags)
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
type X { blob bytes  raw any  in reader  out writer  upload file }`)
	dir := t.TempDir()
	if err := GenerateTypes(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)
	for _, want := range []string{"[]byte", "json.RawMessage", "io.Reader", "io.Writer", "*multipart.FileHeader",
		`"encoding/json"`, `"io"`, `"mime/multipart"`} {
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
type Page<T> { items T[]  total int }
type UserPage { p Page<User, Org> }`)
	dir := t.TempDir()
	if err := GenerateTypes(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)
	// Generic decl now lands as a Go generic struct.
	if !strings.Contains(src, "type Page[T any] struct") {
		t.Errorf("expected `type Page[T any] struct` in:\n%s", src)
	}
	// Instance reference uses Go generic syntax.
	if !strings.Contains(src, "Page[User, Org]") {
		t.Errorf("expected `Page[User, Org]` instance reference in:\n%s", src)
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

func TestGenerateTypesSensitive(t *testing.T) {
	pkg := analyze(t, `package design
type Login {
    username string
    password string @sensitive
    token    string @sensitive(replacement: "***")
    pin      int    @sensitive
}`)
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
	checks := []string{
		"func (l Login) MarshalJSON()",
		`c.Password = "[REDACTED]"`,
		`c.Token = "***"`,
		"var zeroPin int",
		"c.Pin = zeroPin",
		`"encoding/json"`,
	}
	for _, want := range checks {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q in generated source:\n%s", want, src)
		}
	}
}

func TestGenerateTypesSensitiveSkippedWithoutDecorator(t *testing.T) {
	pkg := analyze(t, `package design
type User { id string  name string }`)
	dir := t.TempDir()
	if err := GenerateTypes(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	if strings.Contains(string(out), "MarshalJSON") {
		t.Errorf("did not expect MarshalJSON in non-sensitive type:\n%s", out)
	}
}

func TestGenerateTypesNoPackageName(t *testing.T) {
	pkg := &semantic.Package{Types: map[string]*ast.TypeDecl{}}
	if err := GenerateTypes(pkg, t.TempDir()); err == nil {
		t.Error("expected error for missing pkg name")
	}
}

// ---------- enums ----------

func TestGenerateEnumsBare(t *testing.T) {
	pkg := analyze(t, `package design
enum Color { Red  Green  Blue }`)
	dir := t.TempDir()
	if err := GenerateEnums(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "enums.go"))
	src := string(out)
	mustParseGo(t, src)
	if !strings.Contains(src, "type Color string") {
		t.Error()
	}
	norm := strings.Join(strings.Fields(src), " ")
	if !strings.Contains(norm, `ColorRed Color = "Red"`) {
		t.Errorf("missing bare value:\n%s", src)
	}
}

func TestGenerateEnumsInt(t *testing.T) {
	pkg := analyze(t, `package design
enum Priority { Low = 1  High = 99 }`)
	dir := t.TempDir()
	if err := GenerateEnums(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "enums.go"))
	src := string(out)
	mustParseGo(t, src)
	if !strings.Contains(src, "type Priority int") {
		t.Error()
	}
	norm := strings.Join(strings.Fields(src), " ")
	if !strings.Contains(norm, "PriorityLow Priority = 1") {
		t.Errorf("missing int value:\n%s", src)
	}
}

func TestGenerateEnumsString(t *testing.T) {
	pkg := analyze(t, `package design
enum Status { Active = "active"  Pending = "pending" }`)
	dir := t.TempDir()
	if err := GenerateEnums(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "enums.go"))
	src := string(out)
	mustParseGo(t, src)
	norm := strings.Join(strings.Fields(src), " ")
	if !strings.Contains(norm, `StatusActive Status = "active"`) {
		t.Errorf("missing string value:\n%s", src)
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
		"Code: ErrCodeUserNotFound",
		`"Not found"`,
		"return e.Message",
		"return 404",
	} {
		if !strings.Contains(norm, want) {
			t.Errorf("missing %q in:\n%s", want, src)
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
	if !strings.Contains(norm, `Fields []string `+"`json:\"fields\"`") {
		t.Errorf("missing custom field:\n%s", src)
	}
	if !strings.Contains(src, "func NewValidationErr(fields []string) *ValidationErr") {
		t.Errorf("constructor signature:\n%s", src)
	}
}

func TestGenerateErrorsCodeOverride(t *testing.T) {
	pkg := analyze(t, `package design
error Internal Boom {
    code     string  @default("BOOM_500")
    message  string  @default("kaboom")
}`)
	dir := t.TempDir()
	if err := GenerateErrors(pkg, dir); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "errors.go"))
	src := string(out)
	mustParseGo(t, src)
	if !strings.Contains(src, `"BOOM_500"`) {
		t.Errorf("code override:\n%s", src)
	}
	if !strings.Contains(src, `"kaboom"`) {
		t.Errorf("message override:\n%s", src)
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
    readerOpt   reader?
    writerOpt   writer?
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
		{"ReaderOpt", "io.Reader"},
		{"WriterOpt", "io.Writer"},
		{"AnyOpt", "json.RawMessage"},
		{"ArrayOpt", "[]string"},
		{"MapOpt", "map[string]int"},
		// Value-type optionals — pointer is still required.
		{"StringOpt", "*string"},
		{"StructOpt", "*User"},
	}
	for _, w := range want {
		if !lineHasField(src, w.ident, w.tag) {
			t.Errorf("expected field %s with type %q in:\n%s", w.ident, w.tag, src)
		}
	}
	// Negative: no double-pointer or pointer-to-slice anywhere.
	for _, bad := range []string{"**", "*[]byte", "*[]string", "*map[", "*io.Reader", "*io.Writer", "*json.RawMessage"} {
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
			// e.g. "*[]byte" contains "[]byte" — we want only the bare form.
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
