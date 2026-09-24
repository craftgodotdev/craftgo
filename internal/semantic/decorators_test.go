package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

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
		// rendered in levelNames order, not operand order
		{LvlMethod | LvlField, "field, method"},
	}
	for _, c := range cases {
		if got := c.l.String(); got != c.want {
			t.Errorf("Level(%d).String() = %q, want %q", c.l, got, c.want)
		}
	}
}

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
		{"rawRequest", LvlMethod},
		{"rawResponse", LvlMethod},
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

func TestPlacementUnknownDecorator(t *testing.T) {
	d := expectDiag(t, `type X { name string @nope }`, CodeDecoratorUnknown)
	expectMessage(t, d, "unknown decorator @nope")
}

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

func TestPlacementHappyPath(t *testing.T) {
	mustClean(t, `@version("1.0")
@doc("file doc")
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

func TestPlacementEmitsEndPosition(t *testing.T) {
	src := `type X { name string @nope }`
	_, diags := Analyze(parseFiles(t, src))
	d := findCode(diags, CodeDecoratorUnknown)
	if d == nil {
		t.Fatalf("expected unknown-decorator diag, got %v", diags)
	}
	// `@nope` is 5 columns wide.
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

// checkPlacement skips nil decorator entries.
func TestPlacementNilEntry(t *testing.T) {
	a := newTestAnalyzer(&Package{})
	a.checkPlacement(LvlField, "field X.y", nil)
	a.checkPlacement(LvlField, "field X.y", []*ast.Decorator{nil, {Name: "doc"}})
	if len(a.diags) != 0 {
		t.Errorf("nil entries + a valid @doc on field should not diag, got %v", a.diags)
	}
}

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

func TestCodeOnEnumEmpty(t *testing.T) {
	expectDiag(t, `enum Empty {}`, CodeEnumEmpty)
}

func TestCodeOnDuplicateService(t *testing.T) {
	expectDiag(t, `service S {}
service S {}`, CodeServiceDuplicate)
}

func TestCodeOnExtendOrphan(t *testing.T) {
	expectDiag(t, `extend service S { get Op /x {} }`, CodeServiceExtendOrphan)
}

// Decorators on an `extend service` block apply only to that block's methods.
func TestExtendServiceDecoratorsPropagate(t *testing.T) {
	pkg, diags := Analyze(parseFiles(t, `middleware Auth
service S { get Pub /pub {} }
@middlewares(Auth) @tags("priv")
extend service S { get Priv /priv {} }`))
	if len(diags) > 0 {
		t.Fatalf("expected no diagnostics, got %v", codes(diags))
	}
	si := pkg.Services["S"]
	if si == nil {
		t.Fatal("service S not merged")
	}
	var pub, priv *ast.Method
	for _, m := range si.Methods {
		switch m.Name {
		case "Pub":
			pub = m
		case "Priv":
			priv = m
		}
	}
	if pub == nil || priv == nil {
		t.Fatalf("methods missing: pub=%v priv=%v", pub, priv)
	}
	if len(pub.Decorators) != 0 {
		t.Errorf("Pub picked up unexpected decorators: %+v", pub.Decorators)
	}
	var sawMW, sawTags bool
	for _, d := range priv.Decorators {
		switch d.Name {
		case "middlewares":
			sawMW = true
		case "tags":
			sawTags = true
		}
	}
	if !sawMW || !sawTags {
		t.Errorf("Priv missing inherited decorators: %+v", priv.Decorators)
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
	expectDiag(t, `type X { user shared.User }`, CodeRefUnknownPackage)
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
		// @path takes no optional (a route always has the segment), array or map.
		{"map on @path", `type X { id map<string, int> @path }`, "@path requires"},
		{"optional on @path", `type X { id string? @path }`, "@path requires"},
		{"array on @path", `type X { id string[] @path }`, "@path requires"},
		// A cookie carries one value per name.
		{"array on @cookie", `type X { ids string[] @cookie }`, "@cookie cannot bind to an array"},
		// A wire-string array is repeated values (`?x=1&x=2`); a nested array has no wire form.
		{"multi-dim array on @query", `type X { grid int[][] @query }`, "@query cannot bind to a multi-dimensional array"},
		{"multi-dim array on @header", `type X { tags string[][] @header }`, "@header cannot bind to a multi-dimensional array"},
		{"multi-dim array on @form", `type X { grid int[][] @form }`, "@form cannot bind to a multi-dimensional array"},
		// Maps / structs / generic instantiations never bind to wire-string sources.
		{"map on @query", `type X { meta map<string, string> @query }`, "@query requires"},
		{"map on @header", `type X { meta map<string, string> @header }`, "@header requires"},
		{"struct on @query", `type P { x int }
type X { p P @query }`, "@query requires"},
		{"array struct on @query", `type P { x int }
type X { ps P[] @query }`, "@query requires"},
		{"generic instance on @query", `type Page<T> { items T[] }
type X { p Page<string> @query }`, "@query requires"},
		// `file` binds only to @form.
		{"file on @query", `type X { upload file @query }`, "@query requires"},
		{"file on @header", `type X { upload file @header }`, "@header requires"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			d := expectDiag(t, c.src, CodeBindingType)
			expectMessage(t, d, c.want)
		})
	}
}

func TestCodeOnBindingTypeAcceptsPlainString(t *testing.T) {
	mustClean(t, `type X { id string @path  auth string @header  sid string @cookie }`)
	// A numeric, scalar or enum @path field parses like @query.
	mustClean(t, `scalar UserId int @gte(1)
enum Kind { A B }
type Y { id int @path  uid UserId @path  k Kind @path }`)
	mustClean(t, `error NotFound E { token string @header  sess string @cookie }`)
	// A 1-D array binds to @query, @header and @form as repeated values.
	mustClean(t, `type Z { tags string[] @query  ids int[] @header  vals string[] @form }`)
}

// A multi-dimensional @query array of a cross-package scalar is rejected.
func TestMultiDimArrayCrossPkgQueryRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
scalar Tag string @minLength(1)`,
		"api.craftgo": `package design
import "shared"
type Req { grid shared.Tag[][] @query }
service S { get List /list { request Req } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if !hasCode(diags, CodeBindingType) {
		t.Fatalf("expected multi-dim cross-pkg @query rejection; got %v", codes(diags))
	}
}

// @body and @form request fields are rejected on verbs without a request body.
func TestBodyFormOnNonBodyVerbRejected(t *testing.T) {
	expectError(t, `type Req { raw string @body }
service S { get Fetch /things { request Req } }`, CodeBindingVerb)
	expectError(t, `type Req { upload file @form }
service S { delete Remove /things { request Req } }`, CodeBindingVerb)
}

// @body and @form request fields are accepted on body verbs.
func TestBodyFormOnBodyVerbOK(t *testing.T) {
	mustClean(t, `type Req { raw string @body }
service S { post Make /things { request Req } }`)
	mustClean(t, `type Req { upload file @form }
service S { put Replace /things { request Req } }`)
}

// A @nullable field that auto-binds to @query on a body-less verb is rejected.
func TestNullableAutoQueryRejected(t *testing.T) {
	expectError(t, `scalar Cents int @gte(0)
type Req { c Cents @nullable }
service S { get C /x { request Req } }`, CodeDecoratorConflict)
	// On a body verb the field binds to @body and is accepted.
	mustClean(t, `scalar Cents int @gte(0)
type Req { c Cents @nullable }
service S { post C /x { request Req } }`)
}

// A route that repeats a path variable is rejected; ServeMux panics on a duplicate wildcard.
func TestDuplicatePathVarRejected(t *testing.T) {
	expectDiag(t, `type Resp { ok bool }
type Req { id int @path }
service S { get Get /items/{id}/x/{id} { request Req  response Resp } }`, CodeDuplicatePathVar)
	mustClean(t, `type Resp { ok bool }
type Req { id int @path  sub int @path }
service S { get Get /items/{id}/x/{sub} { request Req  response Resp } }`)

	// A method path that repeats a variable of the service @prefix is rejected.
	expectDiag(t, `type Resp { items string[] }
type Req { tenantID string @path }
@prefix("/tenant/{tenantID}")
service S { get List /{tenantID}/items { request Req  response Resp } }`, CodeDuplicatePathVar)
	// Distinct prefix and method variables are accepted.
	mustClean(t, `type Resp { ok bool }
type Req { tenantID string @path  id string @path }
@prefix("/tenant/{tenantID}")
service S { get Get /{id} { request Req  response Resp } }`)
}

// Two fields binding one wire name on one source are rejected; on two sources they are not.
func TestDuplicateWireNameRejected(t *testing.T) {
	expectDiag(t, `type Req { a string @query("x")  b string @query("x") }
type Resp { ok bool }
service S { get Do /items { request Req  response Resp } }`, CodeDuplicateWireName)
	mustClean(t, `type Req { a string @query("x")  b string @header("x") }
type Resp { ok bool }
service S { get Do /items { request Req  response Resp } }`)
}

// The method binding checks cover fields a request promotes from a mixin.
func TestMixinPromotedBindingChecked(t *testing.T) {
	expectError(t, `type Thing { x int }
type Meta { data Thing }
type Req { Meta }
service S { get G /g { request Req } }`, CodeBindingType)

	expectError(t, `type Meta { raw string @body }
type Req { Meta }
service S { get G /g { request Req } }`, CodeBindingVerb)

	// A promoted string auto-binds to @query.
	mustClean(t, `type Meta { q string }
type Req { Meta }
service S { get G /g { request Req } }`)
}

// @query, @header, @cookie and @form accept the same primitive, scalar, enum and array shapes.
func TestBindingTypeWireAccepts(t *testing.T) {
	cases := []struct {
		label string
		src   string
	}{
		{"plain string", `type X { x string @query  y string @header  z string @cookie  w string @form }`},
		{"int across wire", `type X { a int @query  b int @header  c int @cookie  d int @form }`},
		{"float across wire", `type X { a float64 @query  b float64 @header  c float64 @cookie  d float64 @form }`},
		{"bool across wire", `type X { a bool @query  b bool @header  c bool @cookie  d bool @form }`},
		{"optional string", `type X { a string? @query  b string? @header  c string? @cookie  d string? @form }`},
		{"optional int across wire", `type X { a int? @query  b int? @header  c int? @cookie  d int? @form }`},
		{"optional float/bool/wide", `type X { a float64? @query  b bool? @header  c uint32? @cookie  d int64? @query }`},
		{"optional int scalar", `scalar Cents int
type X { a Cents? @query  b Cents? @header  c Cents? @cookie }`},
		{"optional int enum", `enum Priority { Low = 1  High = 2 }
type X { a Priority? @query  b Priority? @cookie }`},
		{"array of primitive", `type X { a string[] @query  b string[] @header  c int[] @query  d string[] @form }`},
		{"string scalar", `scalar Email string @format(email)
type X { a Email @query  b Email @header  c Email @cookie  d Email @form  e Email? @header  f Email? @form }`},
		{"int scalar", `scalar Cents int
type X { a Cents @query  b Cents @header  c Cents @cookie  d Cents @form }`},
		{"string enum", `enum Color { Red Blue }
type X { a Color @query  b Color @header  c Color @cookie  d Color @form  e Color? @cookie }`},
		{"int enum", `enum Priority { Low = 1  High = 2 }
type X { a Priority @query  b Priority @header  c Priority @cookie  d Priority @form }`},
		{"file form", `type X { upload file @form  optUpload file? @form }`},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) { mustClean(t, c.src) })
	}
}

func TestErrorBodyAllowsCodeAndMessageAsWireFields(t *testing.T) {
	// DSL `code` lowers to `Code`, distinct from the error's unexported `code` field.
	mustClean(t, `error NotFound E {
    code string? @default("E_404")
    message string? @default("Gone")
}`)
	mustClean(t, `error TooManyRequests RateLimited {
    retryAfter int @gte(1)
    bucket     string?
}`)
}

func TestSensitiveAlone(t *testing.T) {
	mustClean(t, `package design
type User {
	id        string
	internal  string @sensitive
}`)
}

func TestSensitiveWithDocAndDeprecatedAllowed(t *testing.T) {
	mustClean(t, `package design
type User {
	id        string
	internal  string @sensitive @doc("server-only") @deprecated
}`)
}

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
	// An enum @default names the value, even when a string matches its wire value.
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
	mustClean(t, `package design
type User { name string? @default("alice") }`)
}

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

func TestDefaultOptionalFieldAccepted(t *testing.T) {
	mustClean(t, `package design
type User { name string? @default("anon") }`)
}

func TestDefaultScalarFieldAccepted(t *testing.T) {
	mustClean(t, `package design
scalar Email string
type User { addr Email? @default("alice@example.com") }`)
}

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

// A @default on a non-optional field warns; `craftgo fmt` adds the `?`.
func TestDefaultWithoutOptionalWarns(t *testing.T) {
	expectWarning(t, `package design
type ListReq { page int @default(1) }`, CodeDefaultNeedsOptional)
	// With the `?` already present, nothing warns.
	mustClean(t, `package design
type ListReq { page int? @default(1) }`)
}

func TestDefaultOnOptionalFieldClean(t *testing.T) {
	mustClean(t, `package design
type ListReq { page int? @default(1) }`)
}

// An `extend service` block rejects a service-only decorator such as @prefix.
func TestExtendServiceRejectsServiceLevelDecorator(t *testing.T) {
	expectDiag(t, `package design
service S {}

middleware Auth

@prefix("/v1")
extend service S {
    get GetX /x {}
}`, CodeExtendDecoratorNotMethod)
}

func TestExtendServiceAcceptsMethodLevelDecorator(t *testing.T) {
	mustClean(t, `package design
service S {}

middleware Auth

@middlewares(Auth)
extend service S {
    get GetX /x {}
}`)
}

func TestLengthOverlapsMinLengthWarning(t *testing.T) {
	expectWarning(t, `package design
type T { name string @length(1, 80) @minLength(3) }`, CodeDecoratorRedundant)
}

func TestRangeOverlapsLteWarning(t *testing.T) {
	expectWarning(t, `package design
type T { age int @range(0, 150) @lte(120) }`, CodeDecoratorRedundant)
}

func TestLengthAloneClean(t *testing.T) {
	mustClean(t, `package design
type T { name string @length(1, 80) }`)
}

func TestMinLengthAloneClean(t *testing.T) {
	mustClean(t, `package design
type T { name string @minLength(1) @maxLength(80) }`)
}

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

// @maxSize and @mimeTypes conflict with @sensitive.
func TestSensitiveConflictsFileValidators(t *testing.T) {
	expectError(t, `type Req { secret file @sensitive @maxSize(1000) }`, CodeDecoratorConflict)
	expectError(t, `type Req { secret file @sensitive @mimeTypes(["image/png"]) }`, CodeDecoratorConflict)
}

// Repeating @errors on a method is not a duplicate decorator.
func TestRepeatedErrorsNotDuplicate(t *testing.T) {
	src := `package p
type Resp { ok bool }
error NotFound Gone {}
error Conflict Taken {}
service S {
  @errors(Gone)
  @errors(Taken)
  get X /x { response Resp }
}`
	diags := analyzeOneFile(t, src)
	if hasDiagContaining(diags, "duplicate decorator") {
		t.Errorf("repeated @errors wrongly rejected as duplicate: %v", diags)
	}
}
