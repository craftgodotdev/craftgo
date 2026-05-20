package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// ---------- ArgKind rendering ----------

func TestArgKindString(t *testing.T) {
	cases := []struct {
		k    ArgKind
		want string
	}{
		{ArgString, "string"},
		{ArgInt, "int"},
		{ArgNumber, "int or float"},
		{ArgBool, "bool"},
		{ArgIdent, "identifier"},
		{ArgDuration, "duration"},
		{ArgSize, "size"},
		{ArgStringOrIdent, "string or identifier"},
		{ArgAny, "any"},
		{ArgKind(99), "any"}, // unknown -> any (the loose default)
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("ArgKind(%d).String() = %q, want %q", c.k, got, c.want)
		}
	}
}

// ---------- Arity ----------

func TestArityTooFew(t *testing.T) {
	expectDiag(t, `@doc()
type X {}`, CodeDecoratorArity)
}

func TestArityTooMany(t *testing.T) {
	d := expectDiag(t, `@doc("a", "b")
type X {}`, CodeDecoratorArity)
	expectMessage(t, d, "at most 1")
}

func TestArityZeroOK(t *testing.T) {
	mustClean(t, `@deprecated
type X { name string }`)
}

// ---------- Type ----------

func TestArgTypeStringExpected(t *testing.T) {
	d := expectDiag(t, `@doc(123)
type X {}`, CodeDecoratorArgType)
	expectMessage(t, d, "expected string")
}

func TestArgTypeIntExpected(t *testing.T) {
	expectDiag(t, `type X { name string @minLength("3") }`, CodeDecoratorArgType)
}

func TestArgTypeDurationAcceptsBareInt(t *testing.T) {
	// README convention: bare number → seconds for durations, bytes for sizes.
	mustClean(t, `service S {
		@timeout(5)
		get GetUser /u {}
	}`)
}

func TestArgTypeSizeAcceptsBareInt(t *testing.T) {
	mustClean(t, `service S {
		@maxBodySize(1024)
		get GetUser /u {}
	}`)
}

func TestArgTypeNumberAcceptsBoth(t *testing.T) {
	mustClean(t, `type X {
		score int @gte(1) @lte(100)
		ratio float64 @gte(0.1) @lte(0.9)
	}`)
}

func TestArgTypeArgAnyAcceptsAnything(t *testing.T) {
	// `@default(value)` uses ArgAny - every literal kind accepted.
	mustClean(t, `type X {
		s string?  @default("a")
		i int?     @default(0)
		f float64? @default(0.5)
		b bool?    @default(true)
	}`)
}

func TestArgsScopeNilEntry(t *testing.T) {
	// Defensive: nil decorator entries silently skip.
	a := &analyzer{pkg: &Package{}}
	a.checkArgsScope(LvlField, []*ast.Decorator{nil})
	a.checkArgsScope(LvlField, []*ast.Decorator{{Name: "doesNotExist"}})
	if len(a.diags) != 0 {
		t.Errorf("nil entry / unknown name should not diag, got %v", a.diags)
	}
}

// ---------- Enum value-set ----------

func TestArgValueFormatAccepted(t *testing.T) {
	mustClean(t, `type X { email string @format(email) }`)
	// String spelling is still ACCEPTED (semantic value lookup
	// works), but the analyzer emits a CodeArgPreferIdent warning
	// nudging the user toward the bare-ident canonical form.
	expectDiag(t, `type Y { email string @format("email") }`, CodeArgPreferIdent)
}

func TestArgValueFormatRejected(t *testing.T) {
	d := expectDiag(t, `type X { x string @format(garbage) }`, CodeDecoratorArgValue)
	expectMessage(t, d, "garbage")
}

func TestArgValueEnumSkipsWhenWrongShape(t *testing.T) {
	// @format gets an int arg. The kind check fires; the enum check
	// must skip silently because identOrStringValue returns false on
	// non-textual literals (we'd otherwise stack two diags on one
	// arg).
	src := `type X { x string @format(123) }`
	expectDiag(t, src, CodeDecoratorArgType)
	expectNoCode(t, src, CodeDecoratorArgValue)
}

// ---------- Variadic + array shortcut ----------

func TestVariadicAcceptsMultiple(t *testing.T) {
	mustClean(t, `type Page {
		email string?
		phone string?
}
@requiresOneOf(email, phone)
type Contact { email string?  phone string? }`)
}

func TestArrayShortcut(t *testing.T) {
	mustClean(t, `@requiresOneOf(["email", "phone"])
type Contact { email string?  phone string? }`)
}

func TestArrayShortcutTooFew(t *testing.T) {
	expectDiag(t, `@requiresOneOf([])
type Contact { email string? }`, CodeDecoratorArity)
}

func TestArrayShortcutTooMany(t *testing.T) {
	// Synthetic - bound an ArgsRule via a custom Spec test by going through
	// checkArrayShortcut directly. The real registry has no Max-bounded
	// variadic decorator, so we exercise the branch with a hand-built
	// rule.
	a := &analyzer{pkg: &Package{}}
	a.checkArrayShortcut(
		&ast.Decorator{Name: "x"},
		ArgsRule{Min: 1, Max: 2, Variadic: ArgString},
		&ast.ArrayLit{Elements: []ast.Expr{
			&ast.StringLit{}, &ast.StringLit{}, &ast.StringLit{},
		}},
	)
	if findCode(a.diags, CodeDecoratorArity) == nil {
		t.Fatalf("expected arity diag, got %v", a.diags)
	}
}

func TestArrayShortcutAnyVariadicSkipsKindCheck(t *testing.T) {
	// ArgAny variadic short-circuits the per-element kind check.
	a := &analyzer{pkg: &Package{}}
	a.checkArrayShortcut(
		&ast.Decorator{Name: "x"},
		ArgsRule{Min: 1, Max: -1, Variadic: ArgAny},
		&ast.ArrayLit{Elements: []ast.Expr{&ast.StringLit{}, &ast.IntLit{}}},
	)
	if len(a.diags) != 0 {
		t.Errorf("expected no diag for ArgAny variadic, got %v", a.diags)
	}
}

func TestArrayShortcutWrongElementKind(t *testing.T) {
	d := expectDiag(t, `type X { mime file @mimeTypes([1, 2]) }`, CodeDecoratorArgType)
	expectMessage(t, d, "array[0]")
}

// ---------- Bindings with optional name ----------

func TestBindingArgOptional(t *testing.T) {
	mustClean(t, `type Q { id string @path }`)
	mustClean(t, `type Q { id string @path("user-id") }`)
	expectDiag(t, `type Q { id string @path(123) }`, CodeDecoratorArgType)
}

// ---------- @security ----------

func TestSecuritySingleIdent(t *testing.T) {
	mustClean(t, `@security(bearerAuth)
service S {}`)
}

func TestSecurityMultipleIdents(t *testing.T) {
	// `@security(A, B)` is the AND form: one requirement that needs both
	// schemes. Multiple `@security(...)` decorators OR-combine instead.
	mustClean(t, `@security(bearerAuth, oauth2)
service S {}`)
}

func TestSecurityRejectsString(t *testing.T) {
	expectDiag(t, `@security("bearerAuth")
service S {}`, CodeDecoratorArgType)
}

func TestSecurityRejectsNamedArg(t *testing.T) {
	// `@security` is a variadic ident list - named args (including the
	// historical `scopes: [...]` form) are rejected.
	expectDiag(t, `@security(oauth2, scopes: ["read"])
service S {}`, CodeDecoratorArgType)
}

func TestSecurityArrayShortcut(t *testing.T) {
	// Symmetric with other variadics: `@security([A, B])` parses the
	// same as `@security(A, B)`.
	mustClean(t, `@security([bearerAuth, oauth2])
service S {}`)
}

func TestSecurityArityZero(t *testing.T) {
	expectDiag(t, `@security
service S {}`, CodeDecoratorArity)
}

// ---------- Flag decorators ----------

func TestFlagDecoratorEmptyParensWarn(t *testing.T) {
	// @positive is a Flag decorator (never takes args). Writing `()`
	// after it emits a warning; `craftgo fmt` strips it on save.
	expectDiag(t, `type X { age int @positive() }`, CodeFlagEmptyParens)
	expectDiag(t, `type X { tag string[] @uniqueItems() }`, CodeFlagEmptyParens)
	expectDiag(t, `type X { nick string @nullable() }`, CodeFlagEmptyParens)
}

func TestFlagDecoratorBareForm(t *testing.T) {
	// Bare form (no parens) is canonical and clean.
	mustClean(t, `type X { age int @positive }`)
	mustClean(t, `type X { tag string[] @uniqueItems }`)
	mustClean(t, `type X { nick string @nullable }`)
}

// ---------- @example ----------

func TestExampleSingleArg(t *testing.T) {
	mustClean(t, `type X { name string @example("foo") }`)
	mustClean(t, `type X { user object? @example({a: 1, b: 2}) }`)
}

func TestExampleArityWrong(t *testing.T) {
	expectDiag(t, `type X { name string @example("a", "b") }`, CodeDecoratorArity)
}

func TestExampleRejectsNamedArg(t *testing.T) {
	// Named args are no longer accepted on any decorator.
	expectDiag(t, `type X { name string @example(value: "a") }`, CodeDecoratorArgType)
}

func TestExampleRejectsTypeLevel(t *testing.T) {
	// @example is field-only; the codegen never emits anywhere else,
	// so semantic rejects misplaced uses with a placement diagnostic.
	expectDiag(t, `@example("X")
type T {}`, CodeDecoratorPlacement)
}

// ---------- exprKindName / inSet / joinQuoted ----------

func TestExprKindName(t *testing.T) {
	cases := []struct {
		name string
		e    ast.Expr
		want string
	}{
		{"nil", nil, "(no value)"},
		{"string", &ast.StringLit{}, "string"},
		{"int", &ast.IntLit{}, "int"},
		{"float", &ast.FloatLit{}, "float"},
		{"bool", &ast.BoolLit{}, "bool"},
		{"null", &ast.NullLit{}, "null"},
		{"ident", &ast.IdentExpr{}, "identifier"},
		{"duration", &ast.DurationLit{}, "duration"},
		{"size", &ast.SizeLit{}, "size"},
		{"array", &ast.ArrayLit{}, "array"},
	}
	for _, c := range cases {
		if got := exprKindName(c.e); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestExprMatchesKindMatrix(t *testing.T) {
	str := &ast.StringLit{}
	in := &ast.IntLit{}
	fl := &ast.FloatLit{}
	bo := &ast.BoolLit{}
	id := &ast.IdentExpr{}
	du := &ast.DurationLit{}
	sz := &ast.SizeLit{}

	cases := []struct {
		name string
		e    ast.Expr
		k    ArgKind
		ok   bool
	}{
		{"any nil", nil, ArgAny, true},
		{"string nil", nil, ArgString, false},
		{"string hit", str, ArgString, true},
		{"string miss", in, ArgString, false},
		{"int hit", in, ArgInt, true},
		{"int miss", str, ArgInt, false},
		{"number int", in, ArgNumber, true},
		{"number float", fl, ArgNumber, true},
		{"number string", str, ArgNumber, false},
		{"bool hit", bo, ArgBool, true},
		{"bool miss", in, ArgBool, false},
		{"ident hit", id, ArgIdent, true},
		{"ident miss", str, ArgIdent, false},
		{"duration native", du, ArgDuration, true},
		{"duration bare int", in, ArgDuration, true},
		{"duration miss", str, ArgDuration, false},
		{"size native", sz, ArgSize, true},
		{"size bare int", in, ArgSize, true},
		{"size miss", str, ArgSize, false},
		{"stringorident str", str, ArgStringOrIdent, true},
		{"stringorident id", id, ArgStringOrIdent, true},
		{"stringorident miss", in, ArgStringOrIdent, false},
		{"unknown kind", in, ArgKind(99), false},
	}
	for _, c := range cases {
		if got := exprMatchesKind(c.e, c.k); got != c.ok {
			t.Errorf("%s: exprMatchesKind = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestIdentOrStringValue(t *testing.T) {
	if v, ok := identOrStringValue(&ast.IdentExpr{Name: &ast.QualifiedIdent{Parts: []string{"x"}}}); !ok || v != "x" {
		t.Errorf("ident: %q %v", v, ok)
	}
	// Nil Name on IdentExpr returns false (defensive).
	if _, ok := identOrStringValue(&ast.IdentExpr{Name: nil}); ok {
		t.Error("nil Name should return ok=false")
	}
	if v, ok := identOrStringValue(&ast.StringLit{Value: "y"}); !ok || v != "y" {
		t.Errorf("string: %q %v", v, ok)
	}
	if _, ok := identOrStringValue(&ast.IntLit{}); ok {
		t.Error("int should return ok=false")
	}
}

func TestInSet(t *testing.T) {
	if !inSet("b", []string{"a", "b", "c"}) {
		t.Error("hit")
	}
	if inSet("z", []string{"a", "b", "c"}) {
		t.Error("miss")
	}
}

func TestJoinQuoted(t *testing.T) {
	if got := joinQuoted([]string{"a", "b"}); got != `"a", "b"` {
		t.Errorf("got %q", got)
	}
	if got := joinQuoted(nil); got != "" {
		t.Errorf("empty got %q", got)
	}
}
