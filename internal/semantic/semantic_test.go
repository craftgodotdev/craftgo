package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/parser"
)

func parseFiles(t *testing.T, sources ...string) []*ast.File {
	t.Helper()
	var files []*ast.File
	for i, src := range sources {
		p := parser.New("test"+itoa(i)+".craftgo", src)
		f := p.Parse()
		if d := p.Diagnostics(); len(d) > 0 {
			t.Fatalf("parse error in source %d: %v", i, d)
		}
		files = append(files, f)
	}
	return files
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	const digits = "0123456789"
	var sb strings.Builder
	if n < 0 {
		sb.WriteByte('-')
		n = -n
	}
	var stack []byte
	for n > 0 {
		stack = append(stack, digits[n%10])
		n /= 10
	}
	for i := len(stack) - 1; i >= 0; i-- {
		sb.WriteByte(stack[i])
	}
	return sb.String()
}

func diagsContain(diags []Diagnostic, substr string) bool {
	for _, d := range diags {
		if strings.Contains(d.Msg, substr) {
			return true
		}
	}
	return false
}

func mustClean(t *testing.T, sources ...string) *Package {
	t.Helper()
	pkg, diags := Analyze(parseFiles(t, sources...))
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	return pkg
}

// ---------- happy path ----------

func TestAnalyzeBasic(t *testing.T) {
	pkg := mustClean(t, `package design
type User { id string  name string }
enum Status { Active  Inactive }
error NotFound UserNotFound
scalar Email string
middleware Auth
service S { get GetUser /u {} }`)
	if pkg.Name != "design" {
		t.Errorf("name: %s", pkg.Name)
	}
	if len(pkg.Types) != 1 || pkg.Types["User"] == nil {
		t.Error("type")
	}
	if len(pkg.Enums) != 1 {
		t.Error("enum")
	}
	if len(pkg.Errors) != 1 {
		t.Error("error")
	}
	if len(pkg.Scalars) != 1 {
		t.Error("scalar")
	}
	if len(pkg.Middlewares) != 1 {
		t.Error("middleware")
	}
	if len(pkg.Services) != 1 {
		t.Error("service")
	}
}

// ---------- package name ----------

func TestPackageNameMismatch(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package a
type X {}`, `package b
type Y {}`))
	if !diagsContain(diags, "conflicts") {
		t.Errorf("expected conflict, got %v", diags)
	}
}

func TestPackageNameMissing(t *testing.T) {
	pkg := mustClean(t, `type X {}`)
	if pkg.Name != "" {
		t.Error("expected empty name")
	}
}

// ---------- duplicate decls ----------

// TestDuplicateDecl pins the type/enum/scalar/error shared namespace -
// they all emit into the same Go types package, so a DSL-name match
// across kinds is a hard collision. Middleware lives in its own Go
// package (svccontext aliases) and uses a separate seen map; see
// [TestMiddlewareSeparateNamespace] for the parity expectation.
func TestDuplicateDecl(t *testing.T) {
	cases := []string{
		`type X {}
type X {}`,
		`type X {}
enum X {}`,
		`type X {}
error NotFound X`,
		`type X {}
scalar X string`,
	}
	for _, src := range cases {
		_, diags := Analyze(parseFiles(t, src))
		if !diagsContain(diags, "duplicate top-level") {
			t.Errorf("expected duplicate error: %s", src)
		}
	}
}

// TestMiddlewareSeparateNamespace pins the namespace split: a
// middleware named the same as a type does NOT clash, because their
// codegen output lives in different Go packages (types vs svccontext).
// Middleware-vs-middleware duplicates still error.
func TestMiddlewareSeparateNamespace(t *testing.T) {
	// type Foo + middleware Foo - no collision.
	mustClean(t, `type Foo {}
middleware Foo`)

	// middleware Foo + middleware Foo - duplicate within the
	// middleware namespace.
	_, diags := Analyze(parseFiles(t, `middleware Foo
middleware Foo`))
	if !diagsContain(diags, "duplicate top-level") {
		t.Errorf("expected duplicate within middleware namespace, got %v", codes(diags))
	}
}

// ---------- service merge ----------

func TestServicePrimaryDuplicate(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {}
service S {}`))
	if !diagsContain(diags, "duplicate primary service") {
		t.Errorf("got %v", diags)
	}
}

func TestServiceExtendWithoutPrimary(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `extend service S { get Op /x {} }`))
	if !diagsContain(diags, "no primary declaration") {
		t.Errorf("got %v", diags)
	}
}

func TestServiceExtendWithDecorators(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {}
@prefix("/x")
extend service S { get Op /x {} }`))
	if !diagsContain(diags, "must not have service-level decorators") {
		t.Errorf("got %v", diags)
	}
}

func TestServiceMethodsMerged(t *testing.T) {
	pkg := mustClean(t, `service S { get A /a {} }
extend service S { post B /b {} }`)
	if len(pkg.Services["S"].Methods) != 2 {
		t.Errorf("methods: %d", len(pkg.Services["S"].Methods))
	}
}

func TestDuplicateMethodAcrossExtends(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S { get A /a {} }
extend service S { post A /b {} }`))
	if !diagsContain(diags, "duplicate method") {
		t.Errorf("got %v", diags)
	}
}

func TestDuplicateRoute(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S { get A /x {} get B /x {} }`))
	if !diagsContain(diags, "duplicate route") {
		t.Errorf("got %v", diags)
	}
}

// ---------- field uniqueness ----------

func TestFieldUniquenessType(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string  name int }`))
	if !diagsContain(diags, "duplicate field") {
		t.Errorf("got %v", diags)
	}
}

func TestFieldUniquenessError(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `error BadRequest E { code string  code string }`))
	if !diagsContain(diags, "duplicate field") {
		t.Errorf("got %v", diags)
	}
}

func TestFieldUniquenessSkipsMixin(t *testing.T) {
	// Type with a mixin + field - exercises the `if !ok { continue }` branch.
	// Profile is declared so the mixin pass resolves it cleanly; the
	// uniqueness pass under test is the `if !ok { continue }` skip on
	// the embedded reference, independent of mixin resolution.
	pkg := mustClean(t, `type Profile { id string }
type X { Profile  name string }`)
	if pkg.Types["X"] == nil || len(pkg.Types["X"].Body) != 2 {
		t.Error()
	}
}

// ---------- enum validation ----------

func TestEnumDuplicateName(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `enum X { A  A }`))
	if !diagsContain(diags, "duplicate enum value name") {
		t.Errorf("got %v", diags)
	}
}

func TestEnumMixedTypes(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `enum X { A  B = 1 }`))
	if !diagsContain(diags, "mixed value types") {
		t.Errorf("got %v", diags)
	}
}

func TestEnumDuplicateInt(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `enum X { A = 1  B = 1 }`))
	if !diagsContain(diags, "duplicate int value") {
		t.Errorf("got %v", diags)
	}
}

func TestEnumDuplicateString(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `enum X { A = "x"  B = "x" }`))
	if !diagsContain(diags, "duplicate string value") {
		t.Errorf("got %v", diags)
	}
}

// TestCheckDecoratorScopeNilEntry exercises the defensive nil-decorator
// branch of [analyzer.checkDecoratorScope]. The parser doesn't produce
// nil entries today, so the only way to reach the branch is via a
// hand-built decorator slice - kept defensive so a future parser
// regression doesn't crash the analyser.
func TestCheckDecoratorScopeNilEntry(t *testing.T) {
	a := &analyzer{pkg: &Package{}}
	a.checkDecoratorScope("test", []*ast.Decorator{nil, {Name: "doc"}, nil})
	if len(a.diags) != 0 {
		t.Errorf("expected no diags from nil-only chain, got %v", a.diags)
	}
}

// TestWalkTypeRefShapes covers every shape branch of walkTypeRef:
// nil ref (early return), map ref (recurses into key+value), and
// named ref (delegates to checkNamedRef). The ast.Field comes from
// the parser today, so we hand-construct a TypeRef directly.
func TestWalkTypeRefShapes(t *testing.T) {
	a := &analyzer{pkg: &Package{}}
	a.walkTypeRef("nil-ref", nil)
	if len(a.diags) != 0 {
		t.Errorf("nil ref should produce no diag, got %v", a.diags)
	}

	mapRef := &ast.TypeRef{Map: &ast.MapType{
		Key:   &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"string"}}}},
		Value: &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"shared", "User"}}}},
	}}
	a.walkTypeRef("map-ref", mapRef)
	if !diagsContain(a.diags, "cross-package qualified reference") {
		t.Errorf("expected qualified-ref diag from map value, got %v", a.diags)
	}
}

// TestCheckNamedRefNilGuards covers the nil + nil-Name early returns
// of [analyzer.checkNamedRef]. Both branches are defensive, but the
// coverage gate refuses anything below 100%.
func TestCheckNamedRefNilGuards(t *testing.T) {
	a := &analyzer{pkg: &Package{}}
	a.checkNamedRef("nil-named", nil)
	a.checkNamedRef("nil-name-field", &ast.NamedTypeRef{})
	if len(a.diags) != 0 {
		t.Errorf("expected no diags from nil-shaped refs, got %v", a.diags)
	}
}

// ---------- duplicate decorators ----------

func TestDuplicateDecoratorOnField(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string @doc("a") @doc("b") }`))
	if !diagsContain(diags, "duplicate decorator") {
		t.Errorf("got %v", diags)
	}
}

func TestDuplicateDecoratorOnType(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@deprecated
@deprecated
type X { name string }`))
	if !diagsContain(diags, "duplicate decorator @deprecated on type X") {
		t.Errorf("got %v", diags)
	}
}

func TestDuplicateDecoratorOnMethod(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {
		@tags("a")
		@tags("b")
		get GetUser /u {}
	}`))
	if !diagsContain(diags, "duplicate decorator @tags on method S.GetUser") {
		t.Errorf("got %v", diags)
	}
}

func TestDuplicateDecoratorOnService(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@prefix("/a")
@prefix("/b")
service S {}`))
	if !diagsContain(diags, "duplicate decorator @prefix on service S") {
		t.Errorf("got %v", diags)
	}
}

func TestDuplicateDecoratorOnEnumValue(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `enum X { A @doc("a") @doc("b") }`))
	if !diagsContain(diags, "duplicate decorator @doc on enum value X.A") {
		t.Errorf("got %v", diags)
	}
}

func TestDuplicateDecoratorOnError(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@doc("a")
@doc("b")
error NotFound UserNotFound`))
	if !diagsContain(diags, "duplicate decorator @doc on error UserNotFound") {
		t.Errorf("got %v", diags)
	}
}

func TestDuplicateDecoratorOnErrorField(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `error BadRequest E { code string @doc("a") @doc("b") }`))
	if !diagsContain(diags, "duplicate decorator @doc on field E.code") {
		t.Errorf("got %v", diags)
	}
}

func TestDuplicateDecoratorPreservesFirst(t *testing.T) {
	// First decorator stays in the AST untouched; only the second is reported.
	_, diags := Analyze(parseFiles(t, `type X { name string @doc("a") @doc("b") @length(1, 10) }`))
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d: %v", len(diags), diags)
	}
}

func TestDecoratorUnique_NoFalsePositive(t *testing.T) {
	mustClean(t, `@deprecated
@doc("ok")
type X { name string @length(1, 10) @pattern("^[a-z]+$") }`)
}

// ---------- qualified refs ----------

func TestQualifiedRefInField(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { user shared.User }`))
	if !diagsContain(diags, "cross-package qualified reference") {
		t.Errorf("got %v", diags)
	}
}

func TestQualifiedRefInMethodResponse(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S { get GetUser /u { response shared.User } }`))
	if !diagsContain(diags, "cross-package qualified reference") {
		t.Errorf("got %v", diags)
	}
}

func TestQualifiedRefInGenericArg(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { items Page<shared.User> }`))
	if !diagsContain(diags, "cross-package qualified reference") {
		t.Errorf("got %v", diags)
	}
}

func TestUnqualifiedRefAccepted(t *testing.T) {
	mustClean(t, `type Page { total int }
type X { items Page }`)
}

// ---------- combination rules ----------

func TestCombinationMultipleBindings(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { id string @path @query }`))
	if !diagsContain(diags, "@query conflicts with @path") {
		t.Errorf("got %v", diags)
	}
}

func TestCombinationBodyAndForm(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { payload string @body @form }`))
	if !diagsContain(diags, "@form conflicts with @body") {
		t.Errorf("got %v", diags)
	}
}

func TestCombinationPassthroughAccepted(t *testing.T) {
	mustClean(t, `service S {
		@passthrough
		get Live /l {}
	}`)
}

// ---------- PathString ----------

func TestPathString(t *testing.T) {
	if PathString(nil) != "" {
		t.Error("nil path")
	}
	pkg := mustClean(t, `type R { id string }
service S { get A /users/{id}/posts { request R } }`)
	got := PathString(pkg.Services["S"].Methods[0].Path)
	if got != "/users/{id}/posts" {
		t.Errorf("got %q", got)
	}
}
