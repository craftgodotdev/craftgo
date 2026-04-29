package semantic

import (
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/internal/ast"
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
	_, diags := Analyze(parseFiles(t, `@doc()
type X {}`))
	if findCode(diags, CodeDecoratorArity) == nil {
		t.Fatalf("expected arity diag, got %v", codes(diags))
	}
}

func TestArityTooMany(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@doc("a", "b")
type X {}`))
	d := findCode(diags, CodeDecoratorArity)
	if d == nil {
		t.Fatalf("expected arity diag, got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "at most 1") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestArityZeroOK(t *testing.T) {
	mustClean(t, `@deprecated
type X { name string @required }`)
}

// ---------- Type ----------

func TestArgTypeStringExpected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@doc(123)
type X {}`))
	d := findCode(diags, CodeDecoratorArgType)
	if d == nil {
		t.Fatalf("expected argtype diag, got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "expected string") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestArgTypeIntExpected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string @minLength("3") }`))
	if findCode(diags, CodeDecoratorArgType) == nil {
		t.Fatalf("expected argtype diag, got %v", codes(diags))
	}
}

func TestArgTypeDurationAcceptsBareInt(t *testing.T) {
	// README convention: bare number → seconds for durations, bytes for sizes.
	mustClean(t, `service S {
		@readTimeout(5)
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
		score int @min(1) @max(100)
		ratio float64 @min(0.1) @max(0.9)
	}`)
}

func TestArgTypeArgAnyAcceptsAnything(t *testing.T) {
	// `@default(value)` uses ArgAny — every literal kind accepted.
	mustClean(t, `type X {
		s string  @default("a")
		i int     @default(0)
		f float64 @default(0.5)
		b bool    @default(true)
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

func TestArgTypeStreamFormatAcceptsString(t *testing.T) {
	mustClean(t, `service S {
		@stream
		@format("sse")
		get Live /l {}
	}`)
}

// ---------- Enum value-set ----------

func TestArgValueFormatAccepted(t *testing.T) {
	mustClean(t, `type X { email string @format(email) }`)
	mustClean(t, `type Y { email string @format("email") }`)
}

func TestArgValueFormatRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { x string @format(garbage) }`))
	d := findCode(diags, CodeDecoratorArgValue)
	if d == nil {
		t.Fatalf("expected argvalue diag, got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "garbage") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestArgValueEnumSkipsWhenWrongShape(t *testing.T) {
	// @format gets an int arg. The kind check fires; the enum check
	// must skip silently because identOrStringValue returns false on
	// non-textual literals (we'd otherwise stack two diags on one
	// arg).
	_, diags := Analyze(parseFiles(t, `type X { x string @format(123) }`))
	if findCode(diags, CodeDecoratorArgType) == nil {
		t.Fatalf("expected argtype, got %v", codes(diags))
	}
	if findCode(diags, CodeDecoratorArgValue) != nil {
		t.Errorf("did not expect argvalue diag stacked on argtype, got %v", codes(diags))
	}
}

func TestArgValueStreamFormatAccepted(t *testing.T) {
	mustClean(t, `service S {
		@stream
		@format(ndjson)
		get Live /l {}
	}`)
}

func TestArgValueStreamFormatRejected(t *testing.T) {
	// `email` is a string format, not a streaming wire format.
	_, diags := Analyze(parseFiles(t, `service S {
		@stream
		@format(email)
		get Live /l {}
	}`))
	if findCode(diags, CodeDecoratorArgValue) == nil {
		t.Fatalf("expected argvalue diag, got %v", codes(diags))
	}
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
	_, diags := Analyze(parseFiles(t, `@requiresOneOf([])
type Contact { email string? }`))
	if findCode(diags, CodeDecoratorArity) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestArrayShortcutTooMany(t *testing.T) {
	// Synthetic — bound an ArgsRule via a custom Spec test by going through
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
	_, diags := Analyze(parseFiles(t, `type X { mime file @mimeTypes([1, 2]) }`))
	d := findCode(diags, CodeDecoratorArgType)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "array[0]") {
		t.Errorf("msg should reference array index, got %q", d.Msg)
	}
}

// ---------- Bindings with optional name ----------

func TestBindingArgOptional(t *testing.T) {
	mustClean(t, `type Q { id string @path }`)
	mustClean(t, `type Q { id string @path("user-id") }`)
	_, diags := Analyze(parseFiles(t, `type Q { id string @path(123) }`))
	if findCode(diags, CodeDecoratorArgType) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// ---------- @security ----------

func TestSecurityIdentRequired(t *testing.T) {
	mustClean(t, `@security(bearerAuth)
service S {}`)
}

func TestSecurityRejectsString(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@security("bearerAuth")
service S {}`))
	if findCode(diags, CodeDecoratorArgType) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestSecurityScopesArrayOfStrings(t *testing.T) {
	mustClean(t, `@security(oauth2, scopes: ["read:users", "write:users"])
service S {}`)
}

func TestSecurityScopesMustBeStrings(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@security(oauth2, scopes: [foo, 1])
service S {}`))
	if findCode(diags, CodeDecoratorArgType) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestSecurityScopesNotArray(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@security(oauth2, scopes: "read:users")
service S {}`))
	d := findCode(diags, CodeDecoratorArgType)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "expected array") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestSecurityRejectsUnknownNamedArg(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@security(oauth2, mystery: "x")
service S {}`))
	if findCode(diags, CodeDecoratorArgType) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestSecurityArityZero(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@security
service S {}`))
	if findCode(diags, CodeDecoratorArity) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// ---------- @example / @examples ----------

func TestExampleSingleArg(t *testing.T) {
	mustClean(t, `type X { name string @example("foo") }`)
	mustClean(t, `type X { user object? @example({a: 1, b: 2}) }`)
}

func TestExampleArityWrong(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string @example("a", "b") }`))
	if findCode(diags, CodeDecoratorArity) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestExampleRejectsNamedArg(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string @example(value: "a") }`))
	if findCode(diags, CodeDecoratorArgType) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestExamplesArityZero(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string @examples }`))
	if findCode(diags, CodeDecoratorArity) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestExamplesObjectRequired(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string @examples("foo") }`))
	if findCode(diags, CodeDecoratorArgType) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestExamplesAccepted(t *testing.T) {
	mustClean(t, `type X { name string @examples({tiny: "a", huge: "b"}) }`)
}

// ---------- @externalDocs ----------

func TestExternalDocsString(t *testing.T) {
	mustClean(t, `@externalDocs("https://docs.example.com")
type X {}`)
}

func TestExternalDocsObject(t *testing.T) {
	mustClean(t, `@externalDocs({url: "https://x.io", description: "ref"})
type X {}`)
}

func TestExternalDocsNamed(t *testing.T) {
	// All-named-args form (used by existing fixtures).
	mustClean(t, `@externalDocs(url: "https://x.io", description: "ref")
type X {}`)
}

func TestExternalDocsRejectsUnknownKey(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@externalDocs(url: "x", mystery: "y")
type X {}`))
	if findCode(diags, CodeDecoratorArgType) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestExternalDocsRejectsNonString(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@externalDocs(url: 123)
type X {}`))
	if findCode(diags, CodeDecoratorArgType) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestExternalDocsPositionalNonString(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@externalDocs(123)
type X {}`))
	d := findCode(diags, CodeDecoratorArgType)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

func TestExternalDocsTwoPositionalsRejected(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@externalDocs("a", "b")
type X {}`))
	d := findCode(diags, CodeDecoratorArity)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if !strings.Contains(d.Msg, "positional form") {
		t.Errorf("msg = %q", d.Msg)
	}
}

func TestExternalDocsZeroArgs(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `@externalDocs
type X {}`))
	if findCode(diags, CodeDecoratorArity) == nil {
		t.Fatalf("got %v", codes(diags))
	}
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
