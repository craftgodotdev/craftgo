package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// ---------- Level rendering ----------

func TestLevelName(t *testing.T) {
	cases := []struct {
		l    Level
		want string
	}{
		{LvlFile, "file"},
		{LvlField, "field"},
		{LvlMethod, "method"},
		{LvlEnumValue, "enum value"},
		{0, "unknown"},
		{LvlField | LvlScalar, "unknown"}, // multi-bit -> unknown
	}
	for _, c := range cases {
		if got := c.l.Name(); got != c.want {
			t.Errorf("Level(%d).Name() = %q, want %q", c.l, got, c.want)
		}
	}
}

func TestLevelString(t *testing.T) {
	cases := []struct {
		l    Level
		want string
	}{
		{0, "(none)"},
		{LvlField, "field"},
		{LvlField | LvlScalar, "field, scalar"},
		// ordering follows levelNames (file < type < field < ...)
		{LvlMethod | LvlField, "field, method"},
	}
	for _, c := range cases {
		if got := c.l.String(); got != c.want {
			t.Errorf("Level(%d).String() = %q, want %q", c.l, got, c.want)
		}
	}
}

// ---------- Registry sanity ----------

func TestRegistryLookup(t *testing.T) {
	if _, ok := Lookup("doc"); !ok {
		t.Error("@doc should be registered")
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("@nope must not be registered")
	}
}

func TestRegistrySpecLevels(t *testing.T) {
	cases := []struct {
		name        string
		mustContain Level
	}{
		{"doc", LvlFile | LvlField | LvlScalar | LvlEnumValue},
		{"deprecated", LvlField | LvlMethod},
		{"prefix", LvlService},
		{"summary", LvlMethod},
		{"requiresOneOf", LvlType},
		{"passthrough", LvlMethod},
		{"path", LvlField},
		{"length", LvlField | LvlScalar},
		{"format", LvlField | LvlScalar},
	}
	for _, c := range cases {
		s, ok := Lookup(c.name)
		if !ok {
			t.Errorf("@%s missing from registry", c.name)
			continue
		}
		if s.Levels&c.mustContain != c.mustContain {
			t.Errorf("@%s levels = %v, expected to contain %v", c.name, s.Levels, c.mustContain)
		}
	}
}

func TestRegistrySpecsHaveDocs(t *testing.T) {
	// A blank Doc would surface as an empty hover tooltip in the LSP -
	// guard against accidental omissions when the registry grows.
	for name, s := range Registry {
		if s.Name != name {
			t.Errorf("registry key %q != Spec.Name %q", name, s.Name)
		}
		if s.Levels == 0 {
			t.Errorf("@%s has no Levels - must be placed somewhere", name)
		}
		if s.Doc == "" {
			t.Errorf("@%s has empty Doc", name)
		}
	}
}

// ---------- Placement: unknown decorator ----------

func TestPlacementUnknownDecorator(t *testing.T) {
	d := expectDiag(t, `type X { name string @nope }`, CodeDecoratorUnknown)
	expectMessage(t, d, "unknown decorator @nope")
}

// ---------- Placement: misplaced known decorator ----------

func TestPlacementPrefixOnField(t *testing.T) {
	d := expectDiag(t, `type X { name string @prefix("/x") }`, CodeDecoratorPlacement)
	expectMessage(t, d, "@prefix is not allowed on field")
}

func TestPlacementBindingOnMethod(t *testing.T) {
	d := expectDiag(t, `service S {
		@path
		get GetUser /u {}
	}`, CodeDecoratorPlacement)
	expectMessage(t, d, "@path is not allowed on method")
}

func TestPlacementSummaryOnType(t *testing.T) {
	d := expectDiag(t, `@summary("x")
type X {}`, CodeDecoratorPlacement)
	expectMessage(t, d, "@summary is not allowed on type X")
}

func TestPlacementPassthroughOnField(t *testing.T) {
	d := expectDiag(t, `type X { body string @passthrough }`, CodeDecoratorPlacement)
	expectMessage(t, d, "@passthrough is not allowed on field")
}

func TestPlacementRequiresOneOfOnField(t *testing.T) {
	d := expectDiag(t, `type X { name string @requiresOneOf(a, b) }`, CodeDecoratorPlacement)
	expectMessage(t, d, "@requiresOneOf is not allowed on field")
}

func TestPlacementMaxBodySizeOnService(t *testing.T) {
	d := expectDiag(t, `@maxBodySize(1MB)
service S {}`, CodeDecoratorPlacement)
	expectMessage(t, d, "@maxBodySize is not allowed on service S")
}

func TestPlacementValidatorsOnEnum(t *testing.T) {
	d := expectDiag(t, `@length(1, 5)
enum E { A B }`, CodeDecoratorPlacement)
	expectMessage(t, d, "@length is not allowed on enum E")
}

// ---------- Placement: happy path ----------

func TestPlacementHappyPath(t *testing.T) {
	mustClean(t, `@title("X")
@version("1.0")
@deprecated
package design

@doc("doc")
type User {
	id   string  @doc("user id")
	name string  @length(1, 20) @pattern("^[a-z]+$")
}

@requiresOneOf(email, phone)
type Contact {
	email string?
	phone string?
}

@prefix("/v1")
@middlewares(Auth)
@tags(users)
service Users {
	@summary("get user")
	@operationId("getUser")
	@maxBodySize(1MB)
	get GetUser /users/{id} { request GetUserReq }
}

type GetUserReq { id string }

extend service Users {
	@passthrough
	get Live /live {}
}

enum Status { Active @doc("ok")  Inactive @deprecated }

error NotFound UserNotFound
middleware Auth
scalar Email string`)
}

// ---------- Placement: diagnostic shape ----------

func TestPlacementEmitsEndPosition(t *testing.T) {
	src := `type X { name string @nope }`
	_, diags := Analyze(parseFiles(t, src))
	d := findCode(diags, CodeDecoratorUnknown)
	if d == nil {
		t.Fatalf("expected unknown-decorator diag, got %v", diags)
	}
	// `@nope` is 5 columns wide; End must point past the last char.
	if d.End.Line != d.Pos.Line {
		t.Errorf("End line %d != Pos line %d", d.End.Line, d.Pos.Line)
	}
	if d.End.Column-d.Pos.Column != 5 {
		t.Errorf("End-Pos column delta = %d, want 5 (covers @nope)", d.End.Column-d.Pos.Column)
	}
	if d.Severity != lexer.SeverityError {
		t.Errorf("severity = %v, want error", d.Severity)
	}
}

func TestPlacementListsValidSitesInMessage(t *testing.T) {
	d := expectDiag(t, `type X { name string @prefix("/x") }`, CodeDecoratorPlacement)
	expectMessage(t, d, "service")
}

// ---------- Placement: nil-decorator defensive guard ----------

func TestPlacementNilEntry(t *testing.T) {
	// Defensive guard: parser doesn't emit nil decorator entries, but
	// checkPlacement tolerates them so a future regression doesn't
	// crash the analyser. We feed the slice both shapes (nil + valid)
	// so the loop body exercises the nil branch and continues.
	a := &analyzer{pkg: &Package{}}
	a.checkPlacement(LvlField, "field X.y", nil)
	a.checkPlacement(LvlField, "field X.y", []*ast.Decorator{nil, {Name: "doc"}})
	if len(a.diags) != 0 {
		t.Errorf("nil entries + a valid @doc on field should not diag, got %v", a.diags)
	}
}

// ---------- Existing diags now carry Code + Related ----------

// Each test below asserts that an LSP-consumed diagnostic carries
// the structured Code + Related fields, in addition to its message
// string. Substring assertions on Msg live in semantic_test.go;
// these tests are the IDE-side contract.

func TestCodeOnDuplicateDecl(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X {}
type X {}`))
	d := findCode(diags, CodeDuplicateDecl)
	if d == nil {
		t.Fatalf("missing %s code, got %v", CodeDuplicateDecl, codes(diags))
	}
	if len(d.Related) != 1 || d.Related[0].Msg != "first declared here" {
		t.Errorf("related = %+v", d.Related)
	}
}

func TestCodeOnDuplicateField(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string  name int }`))
	d := findCode(diags, CodeDuplicateField)
	if d == nil || len(d.Related) != 1 {
		t.Fatalf("want field/duplicate with related; got %v", diags)
	}
}

func TestCodeOnEnumDuplicateName(t *testing.T) {
	expectDiag(t, `enum X { A  A }`, CodeEnumDuplicateName)
}

func TestCodeOnEnumMixedTypes(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `enum X { A  B = 1 }`))
	d := findCode(diags, CodeEnumMixedTypes)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if len(d.Related) != 1 {
		t.Errorf("expected related to first value, got %+v", d.Related)
	}
}

func TestCodeOnEnumDuplicateLiteral(t *testing.T) {
	expectDiag(t, `enum X { A = 1  B = 1 }`, CodeEnumDuplicateLiteral)
	expectDiag(t, `enum Y { A = "x"  B = "x" }`, CodeEnumDuplicateLiteral)
}

func TestCodeOnDuplicateService(t *testing.T) {
	expectDiag(t, `service S {}
service S {}`, CodeServiceDuplicate)
}

func TestCodeOnExtendOrphan(t *testing.T) {
	expectDiag(t, `extend service S { get Op /x {} }`, CodeServiceExtendOrphan)
}

func TestCodeOnExtendDecorators(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {}
@prefix("/x")
extend service S { get Op /x {} }`))
	d := findCode(diags, CodeServiceExtendDecorators)
	if d == nil {
		t.Fatalf("got %v", codes(diags))
	}
	if len(d.Related) != 1 {
		t.Errorf("expected related to primary, got %+v", d.Related)
	}
}

func TestCodeOnDuplicateMethod(t *testing.T) {
	expectDiag(t, `service S { get A /a {} }
extend service S { post A /b {} }`, CodeServiceDuplicateMethod)
}

func TestCodeOnDuplicateRoute(t *testing.T) {
	expectDiag(t, `service S { get A /x {}  get B /x {} }`, CodeServiceDuplicateRoute)
}

func TestCodeOnDuplicateDecorator(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { name string @doc("a") @doc("b") }`))
	d := findCode(diags, CodeDecoratorDuplicate)
	if d == nil || len(d.Related) != 1 {
		t.Fatalf("want decorator/duplicate with related; got %v", diags)
	}
}

func TestCodeOnQualifiedRef(t *testing.T) {
	expectDiag(t, `type X { user shared.User }`, CodeQualifiedRef)
}

func TestCodeOnBindingConflict(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `type X { id string @path @query }`))
	d := findCode(diags, CodeBindingConflict)
	if d == nil || len(d.Related) != 1 {
		t.Fatalf("want binding/conflict with related; got %v", diags)
	}
}

func TestCodeOnBindingType(t *testing.T) {
	cases := []struct {
		label string
		src   string
		want  string
	}{
		{"non-string on @path", `type X { id int @path }`, "@path requires"},
		{"non-string on @header", `type X { auth int @header }`, "@header requires"},
		{"non-string on @cookie", `type X { sid int @cookie }`, "@cookie requires"},
		{"optional string on @path", `type X { id string? @path }`, "@path requires"},
		{"array string on @header", `type X { trace string[] @header }`, "@header requires"},
		{"non-string @header on error", `error NotFound E { auth int @header }`, "@header requires"},
		{"non-string @cookie on error", `error NotFound E { sid int @cookie }`, "@cookie requires"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			d := expectDiag(t, c.src, CodeBindingType)
			expectMessage(t, d, c.want)
		})
	}
}

func TestCodeOnBindingTypeAcceptsPlainString(t *testing.T) {
	// Sanity: the new check must NOT fire for the well-formed shapes
	// codegen has always accepted.
	mustClean(t, `type X { id string @path  auth string @header  sid string @cookie }`)
	mustClean(t, `error NotFound E { token string @header  sess string @cookie }`)
}

func TestErrorBodyAllowsCodeAndMessageAsWireFields(t *testing.T) {
	// `code` / `message` are no longer reserved DSL names - they
	// coexist with the framework's unexported `code` / `message`
	// metadata via Go's case-sensitive identifier rule (DSL `code` →
	// exported `Code`, distinct from the lowercase framework field).
	mustClean(t, `error NotFound E {
    code string? @default("E_404")
    message string? @default("Gone")
}`)
	mustClean(t, `error TooManyRequests RateLimited {
    retryAfter int @gte(1)
    bucket     string?
}`)
}

func TestCodeOnPassthroughBody(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
type Req { name string }
service S {
	@passthrough
	post Echo /e {
		request Req
	}
}`))
	d := findCode(diags, CodePassthroughBody)
	if d == nil || len(d.Related) != 1 {
		t.Fatalf("want passthrough/has-body with related; got %v", diags)
	}
}

func TestCodeOnPackageMismatch(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package a
type X {}`, `package b
type Y {}`))
	if findCode(diags, CodePackageMismatch) == nil {
		t.Fatalf("got %v", codes(diags))
	}
}

// Note: TestCodeOnPackageMismatch keeps the inline pattern because it
// requires TWO source files (multi-package fixture); [expectDiag]
// takes a single string and would lose the file split.

// ---------- @sensitive: standalone is fine ----------

func TestSensitiveAlone(t *testing.T) {
	mustClean(t, `package design
type User {
	id        string
	internal  string @sensitive
}`)
}

func TestSensitiveWithDocAndDeprecatedAllowed(t *testing.T) {
	// Metadata decorators don't shape wire behaviour so they coexist
	// fine with @sensitive.
	mustClean(t, `package design
type User {
	id        string
	internal  string @sensitive @doc("server-only") @deprecated
}`)
}

// ---------- @sensitive + validators ----------

func TestSensitiveConflictsLength(t *testing.T) {
	d := expectError(t, `package design
type User { secret string @sensitive @length(1, 80) }`, CodeDecoratorConflict)
	expectMessage(t, d, "@length cannot be combined with @sensitive")
}

func TestSensitiveConflictsPattern(t *testing.T) {
	d := expectError(t, `package design
type User { secret string @sensitive @pattern("^x") }`, CodeDecoratorConflict)
	expectMessage(t, d, "@pattern cannot be combined with @sensitive")
}

func TestSensitiveConflictsFormat(t *testing.T) {
	d := expectError(t, `package design
type User { secret string @sensitive @format(email) }`, CodeDecoratorConflict)
	expectMessage(t, d, "@format cannot be combined with @sensitive")
}

func TestSensitiveConflictsMinMax(t *testing.T) {
	d := expectError(t, `package design
type User { age int @sensitive @gte(0) }`, CodeDecoratorConflict)
	expectMessage(t, d, "@gte cannot be combined with @sensitive")
}

// ---------- @sensitive + nullability / default ----------

func TestSensitiveConflictsNullable(t *testing.T) {
	d := expectError(t, `package design
type User { secret string @sensitive @nullable }`, CodeDecoratorConflict)
	expectMessage(t, d, "@nullable cannot be combined with @sensitive")
}

func TestSensitiveConflictsDefault(t *testing.T) {
	d := expectError(t, `package design
type User { tier string @sensitive @default("free") }`, CodeDecoratorConflict)
	expectMessage(t, d, "@default cannot be combined with @sensitive")
}

// ---------- @sensitive + binding decorators ----------

func TestSensitiveConflictsBody(t *testing.T) {
	d := expectError(t, `package design
type Req { secret string @sensitive @body }`, CodeDecoratorConflict)
	expectMessage(t, d, "@body cannot be combined with @sensitive")
}

func TestSensitiveConflictsPath(t *testing.T) {
	d := expectError(t, `package design
type Req { id string @sensitive @path }`, CodeDecoratorConflict)
	expectMessage(t, d, "@path cannot be combined with @sensitive")
}

func TestSensitiveConflictsQuery(t *testing.T) {
	d := expectError(t, `package design
type Req { token string @sensitive @query }`, CodeDecoratorConflict)
	expectMessage(t, d, "@query cannot be combined with @sensitive")
}

func TestSensitiveConflictsHeader(t *testing.T) {
	d := expectError(t, `package design
type Req { token string @sensitive @header }`, CodeDecoratorConflict)
	expectMessage(t, d, "@header cannot be combined with @sensitive")
}

func TestSensitiveConflictsCookie(t *testing.T) {
	d := expectError(t, `package design
type Req { sid string @sensitive @cookie }`, CodeDecoratorConflict)
	expectMessage(t, d, "@cookie cannot be combined with @sensitive")
}

func TestSensitiveConflictsForm(t *testing.T) {
	d := expectError(t, `package design
type Req { secret string @sensitive @form }`, CodeDecoratorConflict)
	expectMessage(t, d, "@form cannot be combined with @sensitive")
}

// ---------- @sensitive on error fields ----------

func TestSensitiveOnErrorFieldAlone(t *testing.T) {
	mustClean(t, `package design
error ServiceUnavailable Maintenance {
	msg      string
	internal string @sensitive
}`)
}

func TestSensitiveConflictsOnErrorField(t *testing.T) {
	d := expectError(t, `package design
error BadRequest Bad {
	internal string @sensitive @length(1, 10)
}`, CodeDecoratorConflict)
	expectMessage(t, d, "@length cannot be combined with @sensitive")
}

// ---------- @default on enum field ----------

func TestDefaultEnumValueAccepted(t *testing.T) {
	mustClean(t, `package design
enum Status { Active  Inactive }
type User { st Status? @default(Active) }`)
}

func TestDefaultEnumUnknownValueRejected(t *testing.T) {
	d := expectError(t, `package design
enum Status { Active  Inactive }
type User { st Status? @default(Bogus) }`, CodeDecoratorArgValue)
	expectMessage(t, d, "Bogus", "Status", "Active", "Inactive")
}

func TestDefaultEnumStringLiteralRejected(t *testing.T) {
	// Even when the enum value's StrValue happens to match, the
	// canonical form is the bare identifier so the wire stays
	// stable when codegen renames the underlying value.
	d := expectError(t, `package design
enum Status { Active = "active"  Inactive = "inactive" }
type User { st Status? @default("active") }`, CodeDecoratorArgValue)
	expectMessage(t, d, "enum value by name")
}

func TestDefaultEnumIntLiteralRejected(t *testing.T) {
	d := expectError(t, `package design
enum Tier { Bronze = 1  Silver = 2 }
type User { tr Tier? @default(1) }`, CodeDecoratorArgValue)
	expectMessage(t, d, "enum value by name")
}

func TestDefaultStringFieldUnaffected(t *testing.T) {
	// Non-enum fields skip the enum check; the regular kind rule
	// (string accepts any string literal) applies.
	mustClean(t, `package design
type User { name string? @default("alice") }`)
}

// ---------- @default conflicts ----------

func TestDefaultMapFieldConflict(t *testing.T) {
	d := expectError(t, `package design
type User { meta map<string, string> @default({}) }`, CodeDecoratorConflict)
	expectMessage(t, d, "@default is not supported")
}

func TestDefaultStructFieldConflict(t *testing.T) {
	d := expectError(t, `package design
type Address { city string }
type User { addr Address? @default("x") }`, CodeDecoratorConflict)
	expectMessage(t, d, "@default is not supported")
}

func TestDefaultStructArrayFieldConflict(t *testing.T) {
	d := expectError(t, `package design
type Address { city string }
type User { addrs Address[]? @default([]) }`, CodeDecoratorConflict)
	expectMessage(t, d, "@default is not supported")
}

// ---------- @default optional + scalar ----------

func TestDefaultOptionalFieldAccepted(t *testing.T) {
	mustClean(t, `package design
type User { name string? @default("anon") }`)
}

func TestDefaultScalarFieldAccepted(t *testing.T) {
	// Scalar wraps a primitive (string here) so the field counts as
	// supported by virtue of its underlying primitive.
	mustClean(t, `package design
scalar Email string
type User { addr Email? @default("alice@example.com") }`)
}

// ---------- @default on array of primitives / enums ----------

func TestDefaultEmptyArrayAccepted(t *testing.T) {
	mustClean(t, `package design
type User { tags string[]? @default([]) }`)
}

func TestDefaultStringArrayAccepted(t *testing.T) {
	mustClean(t, `package design
type User { tags string[]? @default(["admin", "ops"]) }`)
}

func TestDefaultEnumArrayAccepted(t *testing.T) {
	mustClean(t, `package design
enum Priority { Low  Normal  High }
type Task { p Priority[]? @default([Low, High]) }`)
}

func TestDefaultArrayMixedKindRejected(t *testing.T) {
	d := expectError(t, `package design
type User { tags string[]? @default(["a", 1]) }`, CodeDecoratorArgType)
	expectMessage(t, d, "string")
}

func TestDefaultEnumArrayUnknownValueRejected(t *testing.T) {
	d := expectError(t, `package design
enum Priority { Low  Normal  High }
type Task { p Priority[]? @default([Low, Bogus]) }`, CodeDecoratorArgValue)
	expectMessage(t, d, "Bogus", "Priority", "Low", "Normal", "High")
}

func TestDefaultArrayLiteralOnPrimitiveRejected(t *testing.T) {
	d := expectError(t, `package design
type User { name string? @default(["x"]) }`, CodeDecoratorArgType)
	expectMessage(t, d, "@default")
}

// ---------- helpers ----------

func codes(diags []Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, d.Code)
	}
	return out
}

func findCode(diags []Diagnostic, code string) *Diagnostic {
	for i := range diags {
		if diags[i].Code == code {
			return &diags[i]
		}
	}
	return nil
}
