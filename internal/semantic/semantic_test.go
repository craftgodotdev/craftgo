package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/parser"
	"github.com/craftgodotdev/craftgo/internal/route"
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

func mustClean(t *testing.T, sources ...string) *Package {
	t.Helper()
	pkg, diags := Analyze(parseFiles(t, sources...))
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	return pkg
}

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

func TestPackageNameMissing(t *testing.T) {
	pkg := mustClean(t, `type X {}`)
	if pkg.Name != "" {
		t.Error("expected empty name")
	}
}

// Types, enums, scalars and errors share one namespace.
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
		expectMsg(t, "duplicate top-level", src)
	}
}

// A middleware may share a type's name, but not another middleware's.
func TestMiddlewareSeparateNamespace(t *testing.T) {
	mustClean(t, `type Foo {}
middleware Foo`)

	expectMsg(t, "duplicate top-level", `middleware Foo
middleware Foo`)
}

func TestServicePrimaryDuplicate(t *testing.T) {
	expectMsg(t, "duplicate primary service", `service S {}
service S {}`)
}

func TestServiceExtendWithoutPrimary(t *testing.T) {
	expectMsg(t, "no primary declaration", `extend service S { get Op /x {} }`)
}

// A service-only decorator such as @prefix is rejected on an `extend service` block.
func TestServiceExtendWithServiceOnlyDecorator(t *testing.T) {
	expectMsg(t, "not valid on a method", `service S {}
@prefix("/x")
extend service S { get Op /x {} }`)
}

// A decorator on an `extend service` block reaches every method inside it.
func TestServiceExtendDecoratorPropagatesToMethods(t *testing.T) {
	pkg, diags := Analyze(parseFiles(t, `middleware Auth
service S { get A /a {} }
@middlewares(Auth)
extend service S { get B /b {} }`))
	if len(diags) > 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	var bMethod *ast.Method
	for _, m := range pkg.Services["S"].Methods {
		if m.Name == "B" {
			bMethod = m
		}
	}
	if bMethod == nil {
		t.Fatal("method B missing")
	}
	saw := false
	for _, d := range bMethod.Decorators {
		if d.Name == "middlewares" {
			saw = true
		}
	}
	if !saw {
		t.Errorf("@middlewares did not propagate to method B: %+v", bMethod.Decorators)
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
	expectMsg(t, "duplicate method", `service S { get A /a {} }
extend service S { post A /b {} }`)
}

func TestDuplicateRoute(t *testing.T) {
	expectMsg(t, "duplicate route", `service S { get A /x {} get B /x {} }`)
}

func TestFieldUniquenessType(t *testing.T) {
	expectMsg(t, "duplicate field", `type X { name string  name int }`)
}

func TestFieldUniquenessError(t *testing.T) {
	expectMsg(t, "duplicate field", `error BadRequest E { code string  code string }`)
}

// The duplicate-field check skips a type's mixin members.
func TestFieldUniquenessSkipsMixin(t *testing.T) {
	pkg := mustClean(t, `type Profile { id string }
type X { Profile  name string }`)
	if pkg.Types["X"] == nil || len(pkg.Types["X"].Body) != 2 {
		t.Error()
	}
}

func TestEnumDuplicateName(t *testing.T) {
	expectMsg(t, "duplicate enum value name", `enum X { A  A }`)
}

func TestEnumMixedTypes(t *testing.T) {
	expectMsg(t, "mixed value types", `enum X { A  B = 1 }`)
}

func TestEnumDuplicateInt(t *testing.T) {
	expectMsg(t, "duplicate int value", `enum X { A = 1  B = 1 }`)
}

func TestEnumDuplicateString(t *testing.T) {
	expectMsg(t, "duplicate string value", `enum X { A = "x"  B = "x" }`)
}

// checkDecoratorScope skips nil decorator entries.
func TestCheckDecoratorScopeNilEntry(t *testing.T) {
	a := newTestAnalyzer(&Package{})
	a.checkDecoratorScope("test", []*ast.Decorator{nil, {Name: "doc"}, nil})
	if len(a.diags) != 0 {
		t.Errorf("expected no diags from nil-only chain, got %v", a.diags)
	}
}

func TestDuplicateDecoratorOnField(t *testing.T) {
	expectMsg(t, "duplicate decorator", `type X { name string @doc("a") @doc("b") }`)
}

func TestDuplicateDecoratorOnType(t *testing.T) {
	expectMsg(t, "duplicate decorator @deprecated on type X", `@deprecated
@deprecated
type X { name string }`)
}

// A non-repeatable decorator given twice on a method is a duplicate.
func TestDuplicateDecoratorOnMethod(t *testing.T) {
	expectMsg(t, "duplicate decorator @deprecated on method S.GetUser", `service S {
		@deprecated
		@deprecated
		get GetUser /u {}
	}`)
}

// A repeatable decorator such as @tags may appear more than once on a method.
func TestRepeatableDecoratorAllowedOnMethod(t *testing.T) {
	expectNoMsg(t, "duplicate decorator @tags", `service S {
		@tags("a")
		@tags("b")
		get GetUser /u {}
	}`)
}

func TestDuplicateDecoratorOnService(t *testing.T) {
	expectMsg(t, "duplicate decorator @prefix on service S", `@prefix("/a")
@prefix("/b")
service S {}`)
}

func TestDuplicateDecoratorOnEnumValue(t *testing.T) {
	expectMsg(t, "duplicate decorator @doc on enum value X.A", `enum X { A @doc("a") @doc("b") }`)
}

func TestDuplicateDecoratorOnError(t *testing.T) {
	expectMsg(t, "duplicate decorator @doc on error UserNotFound", `@doc("a")
@doc("b")
error NotFound UserNotFound`)
}

func TestDuplicateDecoratorOnErrorField(t *testing.T) {
	expectMsg(t, "duplicate decorator @doc on field E.code", `error BadRequest E { code string @doc("a") @doc("b") }`)
}

func TestDuplicateDecoratorPreservesFirst(t *testing.T) {
	// Only the second @doc is reported.
	expectCodeCount(t, `type X { name string @doc("a") @doc("b") @length(1, 10) }`, CodeDecoratorDuplicate, 1)
}

func TestDecoratorUnique_NoFalsePositive(t *testing.T) {
	mustClean(t, `@deprecated
@doc("ok")
type X { name string @length(1, 10) @pattern("^[a-z]+$") }`)
}

func TestQualifiedRefInField(t *testing.T) {
	expectMsg(t, "is not declared anywhere in the project", `type X { user shared.User }`)
}

func TestQualifiedRefInMethodResponse(t *testing.T) {
	expectMsg(t, "is not declared anywhere in the project", `service S { get GetUser /u { response shared.User } }`)
}

func TestQualifiedRefInGenericArg(t *testing.T) {
	expectMsg(t, "is not declared anywhere in the project", `type X { items Page<shared.User> }`)
}

func TestUnqualifiedRefAccepted(t *testing.T) {
	mustClean(t, `type Page { total int }
type X { items Page }`)
}

func TestCombinationMultipleBindings(t *testing.T) {
	expectMsg(t, "@query conflicts with @path", `type X { id string @path @query }`)
}

func TestCombinationBodyAndForm(t *testing.T) {
	expectMsg(t, "@form conflicts with @body", `type X { payload string @body @form }`)
}

func TestCombinationPassthroughAccepted(t *testing.T) {
	mustClean(t, `service S {
		@passthrough
		get Live /l {}
	}`)
}

func TestPathString(t *testing.T) {
	if route.PathString(nil) != "" {
		t.Error("nil path")
	}
	pkg := mustClean(t, `type R { id string }
service S { get A /users/{id}/posts { request R } }`)
	got := route.PathString(pkg.Services["S"].Methods[0].Path)
	if got != "/users/{id}/posts" {
		t.Errorf("got %q", got)
	}
}

// A type, enum, scalar or error named after a built-in type is rejected; a middleware is not.
func TestDeclNamedAfterBuiltinRejected(t *testing.T) {
	expectError(t, `scalar int string`, CodeDeclBuiltinName)
	expectError(t, `type string { a int }`, CodeDeclBuiltinName)
	expectError(t, `enum bool { X Y }`, CodeDeclBuiltinName)
	expectError(t, `error NotFound any`, CodeDeclBuiltinName)
	if _, diags := AnalyzeWith(parseFiles(t, `middleware int`), Options{}); findCode(diags, CodeDeclBuiltinName) != nil {
		t.Error("middleware named after a builtin should not be a builtin-collision error")
	}
	mustClean(t, `scalar Email string  scalar UserID string`)
}

// newTestAnalyzer returns an analyzer whose project holds pkg alone.
func newTestAnalyzer(pkg *Package) *analyzer {
	return &analyzer{pkg: pkg, proj: &Project{Packages: map[string]*Package{pkg.Name: pkg}}}
}
