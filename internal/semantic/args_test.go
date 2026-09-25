package semantic

import (
	"slices"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

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
		{ArgKind(99), "any"}, // unknown -> any
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("ArgKind(%d).String() = %q, want %q", c.k, got, c.want)
		}
	}
}

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

func TestArgTypeStringExpected(t *testing.T) {
	d := expectDiag(t, `@doc(123)
type X {}`, CodeDecoratorArgType)
	expectMessage(t, d, "expected string")
}

func TestArgTypeIntExpected(t *testing.T) {
	expectDiag(t, `type X { name string @minLength("3") }`, CodeDecoratorArgType)
}

func TestArgTypeDurationAcceptsBareInt(t *testing.T) {
	// A bare number is seconds for a duration and bytes for a size.
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
	// @default's argument is ArgAny.
	mustClean(t, `type X {
		s string?  @default("a")
		i int?     @default(0)
		f float64? @default(0.5)
		b bool?    @default(true)
	}`)
}

// The argument check leaves an unknown decorator to the placement check.
func TestDecoratorArgsSkipUnknownName(t *testing.T) {
	a := newTestAnalyzer(&Package{})
	a.checkDecoratorArgs(decoratorSite{level: LvlField, decs: []*ast.Decorator{{Name: "doesNotExist", HasParens: true}}})
	if len(a.diags) != 0 {
		t.Errorf("unknown name should not diag, got %v", a.diags)
	}
}

// An object literal argument to @example is rejected.
func TestExampleRejectsObject(t *testing.T) {
	expectError(t, `type X { meta any? @example({a: 1, b: "x"}) }`, CodeDecoratorArgType)
}

func TestPatternRejectsInvalidRegex(t *testing.T) {
	expectError(t, `type X { bad string @pattern("(unclosed") }`, CodeDecoratorArgType)
}

func TestPatternAcceptsValidRegex(t *testing.T) {
	mustClean(t, `type X { ok string @pattern("^[a-z]+$") }`)
}

func TestPatternRejectsEmpty(t *testing.T) {
	// An empty pattern compiles but constrains nothing.
	expectError(t, `type X { bad string @pattern("") }`, CodeDecoratorArgType)
}

// @example accepts scalar literals and arrays of them.
func TestExampleAcceptsLiteralsAndArrays(t *testing.T) {
	mustClean(t, `type X {
		s   string   @example("alice")
		i   int      @example(30)
		f   float64  @example(0.5)
		b   bool     @example(true)
		arr string[] @example(["GET", "POST"])
	}`)
}

func TestArgValueFormatAccepted(t *testing.T) {
	mustClean(t, `type X { email string @format(email) }`)
	// The string spelling is accepted with a warning that prefers the bare identifier.
	expectDiag(t, `type Y { email string @format("email") }`, CodeArgPreferIdent)
}

func TestArgValueFormatRejected(t *testing.T) {
	d := expectDiag(t, `type X { x string @format(garbage) }`, CodeDecoratorArgValue)
	expectMessage(t, d, "garbage")
}

func TestArgValueEnumSkipsWhenWrongShape(t *testing.T) {
	// An int argument reports the kind mismatch only, not an unknown value as well.
	src := `type X { x string @format(123) }`
	expectDiag(t, src, CodeDecoratorArgType)
	expectNoCode(t, src, CodeDecoratorArgValue)
}

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
	// No registered variadic decorator has a Max, so the rule is built by hand.
	a := newTestAnalyzer(&Package{})
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
	a := newTestAnalyzer(&Package{})
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

func TestBindingArgOptional(t *testing.T) {
	mustClean(t, `type Q { id string @path }`)
	mustClean(t, `type Q { id string @path("user-id") }`)
	expectDiag(t, `type Q { id string @path(123) }`, CodeDecoratorArgType)
}

func TestSecuritySingleIdent(t *testing.T) {
	mustClean(t, `@security(bearerAuth)
service S {}`)
}

func TestSecurityMultipleIdents(t *testing.T) {
	// `@security(A, B)` requires both schemes; separate @security decorators are alternatives.
	mustClean(t, `@security(bearerAuth, oauth2)
service S {}`)
}

func TestSecurityRejectsString(t *testing.T) {
	expectDiag(t, `@security("bearerAuth")
service S {}`, CodeDecoratorArgType)
}

func TestSecurityRejectsNamedArg(t *testing.T) {
	expectDiag(t, `@security(oauth2, scopes: ["read"])
service S {}`, CodeDecoratorArgType)
}

func TestSecurityArrayShortcut(t *testing.T) {
	// `@security([A, B])` is the same as `@security(A, B)`.
	mustClean(t, `@security([bearerAuth, oauth2])
service S {}`)
}

func TestSecurityDuplicateIdentAccepted(t *testing.T) {
	mustClean(t, `@security(bearerAuth, bearerAuth)
service S {}`)
}

func TestSecurityArityZero(t *testing.T) {
	expectDiag(t, `@security
service S {}`, CodeDecoratorArity)
}

// Empty parens on a flag decorator warn; `craftgo fmt` strips them.
func TestFlagDecoratorEmptyParensWarn(t *testing.T) {
	expectDiag(t, `type X { age int @positive() }`, CodeFlagEmptyParens)
	expectDiag(t, `type X { tag string[] @uniqueItems() }`, CodeFlagEmptyParens)
	expectDiag(t, `type X { nick string @nullable() }`, CodeFlagEmptyParens)
	for _, name := range []string{"ignoreTags", "ignoreMiddleware", "ignoreSecurity"} {
		expectDiag(t, "type R { ok bool }\nservice S {\n  @"+name+"()\n  get A /a { response R }\n}", CodeFlagEmptyParens)
	}
}

func TestFlagDecoratorBareForm(t *testing.T) {
	mustClean(t, `type X { age int @positive }`)
	mustClean(t, `type X { tag string[] @uniqueItems }`)
	mustClean(t, `type X { nick string @nullable }`)
}

func TestExampleSingleArg(t *testing.T) {
	mustClean(t, `type X { name string @example("foo") }`)
}

func TestExampleArityWrong(t *testing.T) {
	expectDiag(t, `type X { name string @example("a", "b") }`, CodeDecoratorArity)
}

func TestExampleRejectsNamedArg(t *testing.T) {
	// Named args are rejected on every decorator.
	expectDiag(t, `type X { name string @example(value: "a") }`, CodeDecoratorArgType)
}

func TestExampleRejectsTypeLevel(t *testing.T) {
	expectDiag(t, `@example("X")
type T {}`, CodeDecoratorPlacement)
}

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

func TestJoinQuoted(t *testing.T) {
	if got := joinQuoted([]string{"a", "b"}); got != `"a", "b"` {
		t.Errorf("got %q", got)
	}
	if got := joinQuoted(nil); got != "" {
		t.Errorf("empty got %q", got)
	}
}

// A @default outside the values the field primitive holds is rejected, as a bound is.
func TestDefaultOutOfRangeRejected(t *testing.T) {
	expectError(t, `type Req { u uint? @default(-5) }`, CodeBoundOverflow)
	d := expectError(t, `type Req { b int8? @default(200) }`, CodeBoundOverflow)
	expectMessage(t, d, "@default 200 exceeds int8 range [-128, 127]")
	expectError(t, `type Req { r float32? @default(400000000000000000000000000000000000000.0) }`, CodeBoundOverflow)
}

// An in-range @default on a narrow int is accepted.
func TestDefaultInRangeClean(t *testing.T) {
	mustClean(t, `type Req { b int8? @default(100)  u uint8? @default(0) }`)
}

// One rule decides what @default may target, reported once at the decorator:
// a primitive, enum or scalar with a literal form, or a single-level array of one.
func TestDefaultTargets(t *testing.T) {
	for _, c := range []struct{ decls, field, msg string }{
		{"", `b bytes? @default("x")`, "@default is not supported on a `bytes` field"},
		{"scalar Blob bytes", `b Blob? @default("x")`, "@default is not supported on a `bytes` field"},
		{"", `at datetime[]? @default(["2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z"])`, "@default is not supported on a `datetime` field"},
		{"", `a any? @default(1)`, "only primitives, enums, scalars"},
		{"", `m map<string, int>? @default(1)`, "only primitives, enums, scalars"},
		{"type In { x int }", `s In? @default(1)`, "only primitives, enums, scalars"},
		{"", `g int[][]? @default([[1]])`, "@default is not supported on a multi-dimensional array"},
	} {
		src := c.decls + "\ntype R { " + c.field + " }"
		d := expectError(t, src, CodeDecoratorConflict)
		expectMessage(t, d, c.msg)
		expectCodeCount(t, src, CodeDecoratorConflict, 1)
	}
	expectError(t, "type Box<T> { v T? @default(1) }", CodeDecoratorConflict)
	mustClean(t, "enum C { A B }\nscalar Email string\ntype R { c C? @default(A)  e Email? @default(\"a@b.c\")  xs int[]? @default([1])  cs C[]? @default([A]) }")
}

// A @default field needs `?` unless @path binds it.
func TestDefaultNeedsOptional(t *testing.T) {
	pkg, diags := Analyze(parseFiles(t, `type R { a int @default(1)  b int? @default(1)  c int  d string @path @default("x") }`))
	want := map[string]bool{"a": true, "b": false, "c": false, "d": false}
	for _, f := range ast.Fields(pkg.Types["R"].Body) {
		if got := DefaultNeedsOptional(f); got != want[f.Name] {
			t.Errorf("%s: DefaultNeedsOptional = %v, want %v", f.Name, got, want[f.Name])
		}
	}
	if n := len(slices.DeleteFunc(diags, func(d Diagnostic) bool { return d.Code != CodeDefaultNeedsOptional })); n != 1 {
		t.Errorf("want one %s warning, for a; got %d", CodeDefaultNeedsOptional, n)
	}
}

// @default on a `bytes` field is rejected.
func TestBytesDefaultRejected(t *testing.T) {
	expectError(t, `type Req { p bytes? @default("Ynl0ZXM=") }`, CodeDecoratorConflict)
	expectError(t, `type Req { ps bytes[]? @default(["YQ=="]) }`, CodeDecoratorConflict)
}

// A multi-dimensional array @default is rejected, for primitive and enum elements alike.
func TestMultiDimArrayDefaultRejected(t *testing.T) {
	expectError(t, `type Req { grid int[][]? @default([[1, 2], [3, 4]]) }`, CodeDecoratorConflict)
	expectError(t, `enum Color { Red  Green  Blue }
type Req { swatch Color[][]? @default([[Red, Green], [Blue]]) }`, CodeDecoratorConflict)
}

// A 1-D array @default is accepted.
func TestSingleDimArrayDefaultClean(t *testing.T) {
	mustClean(t, `type Req { arr int[]? @default([1, 2, 3]) }`)
}

// A multi-dimensional array @example is rejected as a conflict, like @default.
func TestMultiDimArrayExampleRejected(t *testing.T) {
	expectError(t, `type Req { rows int[][] @example([[1, 2], [3, 4]]) }`, CodeDecoratorConflict)
}

// A 1-D array @example is accepted.
func TestSingleDimArrayExampleClean(t *testing.T) {
	mustClean(t, `type Req { flat int[] @example([1, 2, 3]) }`)
}

// An explicit @path field rejects @default; the segment is always present.
func TestExplicitPathDefaultRejected(t *testing.T) {
	src := `package p
type R { id string @path @default("x") }
type Resp { x string }
service S { get M /u/{id} { request R  response Resp } }`
	diags := analyzeOneFile(t, src)
	if !hasDiagContaining(diags, "@default cannot be combined with @path") {
		t.Errorf("expected explicit @path @default reject, got: %v", diags)
	}
}

// @default on a `@format(raw)` bytes field is rejected, as on any bytes field.
func TestDefaultOnRawBytesRejected(t *testing.T) {
	d := expectError(t, `type Req { payload bytes? @format(raw) @default("{}") }`, CodeDecoratorConflict)
	expectMessage(t, d, "@default is not supported on a `bytes` field")
}

// @default on a datetime field is rejected.
func TestDefaultOnDateTimeRejected(t *testing.T) {
	expectError(t, `type Req { at datetime? @default("2026-01-01T00:00:00Z") }`, CodeDecoratorConflict)
}

func TestDateTimeTakesNoValidator(t *testing.T) {
	expectError(t, `type Req { at datetime @minLength(1) }`, CodeDecoratorTypeMismatch)
	expectError(t, `type Req { at datetime @gte(1) }`, CodeDecoratorTypeMismatch)
}

func TestDefaultOnFileRejected(t *testing.T) {
	diags := analyzeOneFile(t, "package p\ntype U { blob file @form @default(\"x\") }\nservice S { post Up /up { request U  response U } }")
	if !hasDiagContaining(diags, "@default is not supported on a `file`") {
		t.Errorf("expected @default-on-file reject, got: %v", diags)
	}
}

// @example is type-checked against the field: a kind mismatch or an unknown enum value is rejected.
func TestExampleTypeChecked(t *testing.T) {
	cases := map[string]bool{ // src -> expectReject
		`package p
type T { count int @example("nope") }`: true,
		`package p
enum Color { Red Green }
type T { c Color @example(Purple) }`: true,
		`package p
enum Color { Red Green }
type T { c Color @example(Green) }`: false,
		`package p
type T { name string @example("alice") }`: false,
	}
	for src, expectReject := range cases {
		diags := analyzeOneFile(t, src)
		got := hasDiagContaining(diags, "requires a") || hasDiagContaining(diags, "not a value of enum") || hasDiagContaining(diags, "must reference an enum")
		if got != expectReject {
			t.Errorf("@example type-check: reject=%v want=%v for:\n%s\ndiags: %v", got, expectReject, src, diags)
		}
	}
}

// @default and @example both reject a string literal on an int field.
func TestParityDefaultExampleShareTypeCheck(t *testing.T) {
	defDiags := analyzeOneFile(t, "package p\ntype T { n int @default(\"nope\") }")
	exDiags := analyzeOneFile(t, "package p\ntype T { n int @example(\"nope\") }")
	defRej := hasDiagContaining(defDiags, "requires a")
	exRej := hasDiagContaining(exDiags, "requires a")
	if !defRej || !exRej {
		t.Errorf("@default/@example type-check parity broken: default rejected=%v, example rejected=%v", defRej, exRej)
	}
}
