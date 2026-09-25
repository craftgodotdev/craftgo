package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/route"
)

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

// Types, enums, scalars and errors share one namespace.
// A second declaration of a top-level name is reported and related to the first.
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
		d := expectDiag(t, src, CodeDuplicateDecl)
		expectMessage(t, d, "duplicate top-level")
		if len(d.Related) != 1 || d.Related[0].Msg != "first declared here" {
			t.Errorf("%q: related = %+v", src, d.Related)
		}
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
	d := expectDiag(t, `service S {}
service S {}`, CodeServiceDuplicate)
	expectMessage(t, d, "duplicate primary service")
}

func TestServiceExtendWithoutPrimary(t *testing.T) {
	d := expectDiag(t, `extend service S { get Op /x {} }`, CodeServiceExtendOrphan)
	expectMessage(t, d, "no primary declaration")
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
	decs := pkg.Services["S"].Decorators(bMethod)
	if !ast.HasDecorator(decs, "middlewares") {
		t.Errorf("@middlewares did not propagate to method B: %+v", decs)
	}
	if len(bMethod.Decorators) != 0 {
		t.Errorf("the block's decorators were written into B's syntax: %+v", bMethod.Decorators)
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
	d := expectDiag(t, `service S { get A /a {} }
extend service S { post A /b {} }`, CodeServiceDuplicateMethod)
	expectMessage(t, d, "duplicate method")
}

func TestDuplicateRoute(t *testing.T) {
	d := expectDiag(t, `service S { get A /x {} get B /x {} }`, CodeServiceDuplicateRoute)
	expectMessage(t, d, "duplicate route")
}

// A repeated field name is reported and related to the first.
func TestFieldUniquenessType(t *testing.T) {
	d := expectDiag(t, `type X { name string  name int }`, CodeDuplicateField)
	expectMessage(t, d, "duplicate field")
	if len(d.Related) != 1 {
		t.Errorf("related = %+v, want the first field", d.Related)
	}
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
	d := expectDiag(t, `enum X { A  A }`, CodeEnumDuplicateName)
	expectMessage(t, d, "duplicate enum value name")
}

// Bare and valued members in one enum are reported and related to the first value.
func TestEnumMixedTypes(t *testing.T) {
	d := expectDiag(t, `enum X { A  B = 1 }`, CodeEnumMixedTypes)
	expectMessage(t, d, "mixed value types")
	if len(d.Related) != 1 {
		t.Errorf("related = %+v, want the first value", d.Related)
	}
}

func TestEnumDuplicateInt(t *testing.T) {
	d := expectDiag(t, `enum X { A = 1  B = 1 }`, CodeEnumDuplicateLiteral)
	expectMessage(t, d, "duplicate int value")
}

func TestEnumDuplicateString(t *testing.T) {
	d := expectDiag(t, `enum X { A = "x"  B = "x" }`, CodeEnumDuplicateLiteral)
	expectMessage(t, d, "duplicate string value")
}

// Every repeat of an enum value name or literal relates to its first use.
func TestEnumDuplicatesRelateToTheFirst(t *testing.T) {
	for _, src := range []string{
		"enum X {\n A\n A\n A\n}",
		"enum X {\n A = 1\n B = 1\n C = 1\n}",
		"enum X {\n A = \"x\"\n B = \"x\"\n C = \"x\"\n}",
	} {
		_, diags := Analyze(parseFiles(t, src))
		n := 0
		for _, d := range diags {
			if d.Code != CodeEnumDuplicateName && d.Code != CodeEnumDuplicateLiteral {
				continue
			}
			n++
			if len(d.Related) != 1 || d.Related[0].Pos.Line != 2 {
				t.Errorf("%q: %s at line %d relates to %v, want line 2", src, d.Code, d.Pos.Line, d.Related)
			}
		}
		if n != 2 {
			t.Errorf("%q: want 2 duplicate diagnostics, got %v", src, diags)
		}
	}
}

// A repeated decorator is reported and related to the first.
func TestDuplicateDecoratorOnField(t *testing.T) {
	d := expectDiag(t, `type X { name string @doc("a") @doc("b") }`, CodeDecoratorDuplicate)
	expectMessage(t, d, "duplicate decorator")
	if len(d.Related) != 1 {
		t.Errorf("related = %+v, want the first @doc", d.Related)
	}
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
	expectMsg(t, "duplicate decorator @doc on error field E.code", `error BadRequest E { code string @doc("a") @doc("b") }`)
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
	d := expectDiag(t, `type X { user shared.User }`, CodeRefUnknownPackage)
	expectMessage(t, d, "is not declared anywhere in the project")
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

// Two bindings on one field are reported and related to the first.
func TestCombinationMultipleBindings(t *testing.T) {
	d := expectDiag(t, `type X { id string @path @query }`, CodeBindingConflict)
	expectMessage(t, d, "@query conflicts with @path")
	if len(d.Related) != 1 {
		t.Errorf("related = %+v, want the first binding", d.Related)
	}
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
	if _, diags := analyzeWith(parseFiles(t, `middleware int`), Options{}); findCode(diags, CodeDeclBuiltinName) != nil {
		t.Error("middleware named after a builtin should not be a builtin-collision error")
	}
	mustClean(t, `scalar Email string  scalar UserID string`)
}
