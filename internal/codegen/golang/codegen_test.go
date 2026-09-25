package golang

import (
	goast "go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	gopath "path"
	"path/filepath"
	"slices"
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
	// A warning still generates valid code, so only errors fail the test.
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

// stdlibQualifiers maps the standard-library qualifiers the import check covers to their paths.
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

// mustImportsMatchUsage asserts file imports exactly the stdlibQualifiers packages it uses, binds
// each import name once and uses every package it imports under an alias.
func mustImportsMatchUsage(t *testing.T, file *goast.File, src string) {
	t.Helper()

	imported := map[string]bool{}
	aliased := map[string]bool{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := gopath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
			aliased[name] = true
		}
		if imported[name] {
			t.Errorf("generated Go imports two packages as %s\n--- source ---\n%s", name, src)
		}
		imported[name] = true
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
		if _, std := stdlibQualifiers[name]; (std || aliased[name]) && !used[name] {
			t.Errorf("generated Go imports %q but never uses it\n--- source ---\n%s", name, src)
		}
	}
}

// mustContainAll asserts every want substring appears in got, reporting all misses at once.
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

// mustContainNone asserts no unwanted substring appears in got.
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

// A @sensitive field is tagged json:"-" while the others keep their tags.
func TestGenerateTypesSensitiveJSONDash(t *testing.T) {
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

// A @path/@query/@header/@cookie field is tagged json:"-" plus a tag carrying its wire name.
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
	// gofmt aligns fields and tags; collapse whitespace before matching.
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

// A bytes @format(raw) field is wire.Raw in every shape, with omitempty only when it is `?`.
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
		"Payload wire.Raw `json:\"payload\"`",
		"Meta    wire.Raw `json:\"meta,omitempty\"`",
		"Trace   wire.Raw `json:\"trace\"`",
	)
}

// A scalar over raw bytes is an alias of wire.Raw, so it keeps wire.Raw's codec methods.
func TestGenerateTypesRawBytesScalar(t *testing.T) {
	pkg := analyze(t, `package design
scalar RawDoc bytes @format(raw)
type X { photos RawDoc?  cover RawDoc  audit RawDoc @nullable }`)
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
		"Photos wire.Raw `json:\"photos,omitempty\"`",
		"Cover  wire.Raw `json:\"cover\"`",
		"Audit  wire.Raw `json:\"audit\"`",
	)
}

// A required raw field gets a length presence check; a `?` or @nullable one gets none.
func TestValidateRequiredRawBytesChecksLen(t *testing.T) {
	src := runValidateGen(t, `package design
type X { payload bytes @format(raw)  meta bytes? @format(raw)  trace bytes @format(raw) @nullable }`)
	mustContainAll(t, src, "if len(v.Payload) == 0 {", `"payload: required"`)
	for _, banned := range []string{"v.Meta", "v.Trace"} {
		if strings.Contains(src, banned) {
			t.Errorf("an optional / @nullable raw field needs no presence check, got %q:\n%s", banned, src)
		}
	}
}

// A raw scalar aliases a non-local type, so it gets no Validate method and no call to one.
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

// A generic decl renders as a Go generic struct and its instances as Name[Arg1, Arg2].
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
	if !strings.Contains(src, "type Pair[A any, B any] struct") {
		t.Errorf("expected `type Pair[A any, B any] struct` in:\n%s", src)
	}
	if !strings.Contains(src, "Pair[User, Org]") {
		t.Errorf("expected `Pair[User, Org]` instance reference in:\n%s", src)
	}
}

// The import set spells generic arguments, each under its own package's alias.
func TestImportSetSpellsGenericArgs(t *testing.T) {
	cross := crossPkg{"shared": "github.com/x/internal/types/shared"}
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
		want string
	}{
		{"local generic local arg", generic(named("Page"), named("User")), "types.Page[types.User]"},
		{"local generic cross-pkg arg", generic(named("Page"), named("shared", "User")), "types.Page[shared.User]"},
		{"cross-pkg generic local arg", generic(named("shared", "Page"), named("User")), "shared.Page[types.User]"},
		{"nested generic local", generic(named("Page"), generic(named("Envelope"), named("User"))), "types.Page[types.Envelope[types.User]]"},
		{"multi-arg generic", generic(named("Pair"), named("User"), named("shared", "Email")), "types.Pair[types.User, shared.Email]"},
		{"builtin arg", generic(named("Page"), named("datetime")), "types.Page[time.Time]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			set := newImportSet(&projectResolver{CrossPkg: cross}, goImport{Alias: localAlias, Path: "github.com/x/internal/types/app"}, nil)
			if got := set.named(c.ref); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// The import set imports exactly the packages a spelled reference reaches.
func TestImportSetImportsWhatItSpells(t *testing.T) {
	const local = "example.com/app/internal/types/app"
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
		want []string
	}{
		{"local type", named("User"), []string{local}},
		{"cross-pkg type", named("shared", "User"), []string{cross["shared"]}},
		{"cross-pkg generic, local arg", generic(named("shared", "Page"), named("User")), []string{local, cross["shared"]}},
		{"cross-pkg generic, cross-pkg arg", generic(named("shared", "Page"), named("shared", "User")), []string{cross["shared"]}},
		{"cross-pkg generic over a package named ...types", generic(named("genpkg", "GBox"), named("paytypes", "PItem")), []string{cross["genpkg"], cross["paytypes"]}},
		{"nested generic reaching a local arg", generic(named("genpkg", "GBox"), generic(named("shared", "Page"), named("User"))), []string{local, cross["genpkg"], cross["shared"]}},
		{"builtin arg", generic(named("Page"), named("file")), []string{local, "mime/multipart"}},
		{"nil ref", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			set := newImportSet(&projectResolver{CrossPkg: cross}, goImport{Alias: localAlias, Path: local}, nil)
			spelled := set.named(c.ref)
			var got []string
			for _, imp := range set.imports() {
				got = append(got, imp.Path)
			}
			if !slices.Equal(got, slices.Sorted(slices.Values(c.want))) {
				t.Errorf("spelling %q imports %v, want %v", spelled, got, c.want)
			}
		})
	}
}

// A package named like a name the template binds, or like another import, takes a numbered alias.
func TestImportSetAvoidsBoundNames(t *testing.T) {
	cross := crossPkg{"server": "example.com/app/internal/types/server", "types": "example.com/app/internal/types/types"}
	set := newImportSet(&projectResolver{CrossPkg: cross}, goImport{Alias: localAlias, Path: "example.com/app/internal/types/app"}, transportNames)
	if got := set.named(&ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"server", "Cred"}}}); got != "server2.Cred" {
		t.Errorf("a package named like a template import: got %q", got)
	}
	if got := set.named(&ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"types", "Page"}}, Args: []*ast.TypeRef{{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"User"}}}}}}); got != "types2.Page[types.User]" {
		t.Errorf("a package named like the file's own types: got %q", got)
	}
	if got := set.named(&ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"datetime"}}}); got != "time.Time" {
		t.Errorf("a builtin keeps its package's own name: got %q", got)
	}
}

// A same-package mixin embeds by bare name; a cross-package one keeps its qualifier.
func TestRenderMixinQualifiedRef(t *testing.T) {
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
			imports := newImportSet(&projectResolver{CrossPkg: crossPkg{"shared": "x/types/shared"}}, goImport{}, typesNames)
			if got := renderTypeBody([]ast.TypeMember{m}, &semantic.Package{}, &projectResolver{}, imports); got != c.want {
				t.Errorf("mixin %v renders %q, want %q", c.parts, got, c.want)
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

// @deprecated on a type or field emits a `Deprecated:` doc line, with a fallback reason when bare.
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

	if !strings.Contains(src, "// Deprecated: use NewBook instead") {
		t.Errorf("expected type-level Deprecated comment:\n%s", src)
	}
	if !strings.Contains(src, "// Deprecated: use priceCents instead") {
		t.Errorf("expected field-level Deprecated comment with reason:\n%s", src)
	}
	if !strings.Contains(src, "// Deprecated: this entity is deprecated") {
		t.Errorf("expected fallback Deprecated comment for bare @deprecated:\n%s", src)
	}
}

// A @nullable field has no omitempty unless also `?`, and gets a pointer only for a value type.
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

// A package with no type or scalar writes neither types.go nor its directory.
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

// A package declaring only a scalar still writes types.go with the scalar's defined type.
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

// A package without types writes neither types.go nor validate.go.
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

// ---------- enums ----------

// Each enum kind matches its testdata/golden/enums-<kind>.go snapshot.
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
	// gofmt aligns fields and tags; collapse whitespace before matching.
	norm := strings.Join(strings.Fields(src), " ")
	for _, want := range []string{
		`const ErrCodeUserNotFound = "USER_NOT_FOUND"`,
		"type UserNotFoundErr struct{}",
		"func NewUserNotFoundErr() *UserNotFoundErr { return &UserNotFoundErr{} }",
		`func (e *UserNotFoundErr) Error() string { return "Not found" }`,
		"func (e *UserNotFoundErr) ErrCode() string { return ErrCodeUserNotFound }",
		"func (e *UserNotFoundErr) HTTPStatus() int { return 404 }",
		`return json.Marshal(map[string]string{"code": ErrCodeUserNotFound, "message": e.Error()})`,
	} {
		if !strings.Contains(norm, want) {
			t.Errorf("missing %q in:\n%s", want, src)
		}
	}
	// The code and message are constants the methods return, never fields.
	for _, forbidden := range []string{
		"code string",
		"message string",
		"e.code",
		"e.message",
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
	// gofmt aligns fields and tags; collapse whitespace before matching.
	norm := strings.Join(strings.Fields(src), " ")
	if !strings.Contains(norm, "type ValidationBody struct") {
		t.Errorf("missing body struct:\n%s", src)
	}
	if !strings.Contains(norm, `Fields []string `+"`json:\"fields\"`") {
		t.Errorf("missing custom field on body struct:\n%s", src)
	}
	if !strings.Contains(src, "func NewValidationErr(body ValidationBody) *ValidationErr") {
		t.Errorf("constructor must take a body struct:\n%s", src)
	}
	if !strings.Contains(norm, "func (e *ValidationErr) MarshalJSON() ([]byte, error) { return json.Marshal(e.ValidationBody) }") {
		t.Errorf("an error with a body must marshal the body alone:\n%s", src)
	}
}

// User-declared code and message become exported body fields; the type holds nothing else.
func TestGenerateErrorsUserDeclaresCodeAndMessage(t *testing.T) {
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

	if !strings.Contains(norm, `Code *string `+"`json:\"code,omitempty\"`") {
		t.Errorf("user `code?` field should appear on body struct as *Code:\n%s", src)
	}
	if !strings.Contains(norm, `Message *string `+"`json:\"message,omitempty\"`") {
		t.Errorf("user `message?` field should appear on body struct as *Message:\n%s", src)
	}
	if !strings.Contains(norm, "type BoomErr struct { BoomBody }") {
		t.Errorf("err type must hold only its body:\n%s", src)
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

// Error @header/@cookie fields leave the body and are written by WriteResponseHeaders.
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

	if !strings.Contains(norm, `RetryAfter string `+"`json:\"-\" header:\"retryAfter\"`") {
		t.Errorf("retryAfter should be json:\"-\" header:\"retryAfter\":\n%s", src)
	}
	if !strings.Contains(norm, `SessionToken string `+"`json:\"-\" cookie:\"sessionToken\"`") {
		t.Errorf("sessionToken should be json:\"-\" cookie:\"sessionToken\":\n%s", src)
	}
	if !strings.Contains(norm, `Bucket *string `+"`json:\"bucket,omitempty\"`") {
		t.Errorf("bucket should keep its DSL JSON tag:\n%s", src)
	}
	mustContainAll(t, src,
		"func (e *RateLimitedErr) WriteResponseHeaders(w http.ResponseWriter)",
		`w.Header().Set("retryAfter", e.RetryAfter)`,
		`http.SetCookie(w, &http.Cookie{Name: "sessionToken", Value: e.SessionToken})`,
		`"net/http"`,
	)
}

// A @sensitive error-body field is tagged json:"-".
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

// Non-string error @header/@cookie values are formatted with strconv.
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
		// A local int scalar converts to int64 for strconv.FormatInt.
		`w.Header().Set("X-Cost", strconv.FormatInt(int64(e.Cost), 10))`,
		`http.SetCookie(w, &http.Cookie{Name: "throttled", Value: strconv.FormatBool(e.Throttled)})`,
	)
}

// A cross-package int scalar header on an error formats with strconv.FormatInt.
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

// Without @header/@cookie fields, errors.go has no net/http import and no WriteResponseHeaders.
func TestGenerateErrorsNoBindingsNoHTTPImport(t *testing.T) {
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

// ---------- helpers ----------

// An optional field gets a pointer only when its Go type is not already nilable.
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
		// Value types still get a pointer.
		{"StringOpt", "*string"},
		{"StructOpt", "*User"},
	}
	for _, w := range want {
		if !lineHasField(src, w.ident, w.tag) {
			t.Errorf("expected field %s with type %q in:\n%s", w.ident, w.tag, src)
		}
	}
	mustContainNone(t, src,
		"**",
	)
}

// An optional type that holds nil gets no pointer wherever it is nested, as a field of it gets none.
func TestGoTypeNestedOptionalNilable(t *testing.T) {
	pkg := analyze(t, `package design
scalar Blob bytes
type Box<T> { v T }
type Holder {
    field  Blob?
    values map<string, Blob?>
    nested map<string, map<string, Blob?>>
    boxed  Box<map<string, Blob?>>
    counts map<string, int?>
}`)
	dir := t.TempDir()
	if err := generateTypes(pkg, dir, nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "design", "types.go"))
	src := string(out)
	mustParseGo(t, src)
	for _, w := range []struct{ ident, typ string }{
		{"Field", "Blob"},
		{"Values", "map[string]Blob"},
		{"Nested", "map[string]map[string]Blob"},
		{"Boxed", "Box[map[string]Blob]"},
		{"Counts", "map[string]*int"},
	} {
		if !lineHasField(src, w.ident, w.typ) {
			t.Errorf("expected field %s with type %q in:\n%s", w.ident, w.typ, src)
		}
	}
}

// lineHasField reports whether a line of src holds both ident and typ, but not *typ.
func lineHasField(src, ident, typ string) bool {
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, ident) && strings.Contains(line, typ) {
			if strings.Contains(line, "*"+typ) {
				continue
			}
			return true
		}
	}
	return false
}

func TestGoTypeNil(t *testing.T) {
	if goType(nil, nil, nil) != "" {
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

// A mixin in an error body is embedded in the generated body struct.
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

// An optional or @nullable scalar-over-bytes field has no pointer; its validator nil-guards it.
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
	mustContainAll(t, string(val),
		"if v.NulBlob != nil {",
		"if v.OptBlob != nil {",
	)
}
