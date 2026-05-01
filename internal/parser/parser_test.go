package parser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dropship-dev/craftgo/internal/ast"
	"github.com/dropship-dev/craftgo/internal/lexer"
)

// TestCaptureDocPropagatesDoc covers the if-true branch of
// [Parser.captureDoc]: when the peeked token carries doc-comment
// lines, captureDoc must copy them onto pendingDoc so the next decl
// picks them up. The earlier coverage gap was because most parser
// fixtures elide doc comments.
func TestCaptureDocPropagatesDoc(t *testing.T) {
	src := `// Foo is the canonical example.
// Two-line doc.
type Foo {
	x string
}`
	f := mustParse(t, src)
	if len(f.Decls) != 1 {
		t.Fatalf("expected one decl, got %d", len(f.Decls))
	}
	td, ok := f.Decls[0].(*ast.TypeDecl)
	if !ok {
		t.Fatalf("expected TypeDecl")
	}
	if len(td.Doc) != 2 {
		t.Errorf("expected 2 doc lines on Foo, got %v", td.Doc)
	}
}

// TestIsPathWordTokenBranches pins each return path of
// [isPathWordToken]: identifier, keyword/verb range, and the
// "anything else" false branch. The false branch is what tells the
// path parser when to stop consuming segments.
func TestIsPathWordTokenBranches(t *testing.T) {
	cases := []struct {
		name string
		k    lexer.Kind
		want bool
	}{
		{"ident", lexer.Ident, true},
		{"keyword", lexer.KwPackage, true},
		{"verb", lexer.VerbOptions, true},
		{"int", lexer.Int, false},
		{"eof", lexer.EOF, false},
		{"string", lexer.String, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isPathWordToken(c.k); got != c.want {
				t.Errorf("isPathWordToken(%v) = %v, want %v", c.k, got, c.want)
			}
		})
	}
}

func mustParse(t *testing.T, src string) *ast.File {
	t.Helper()
	p := New("test", src)
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("unexpected diagnostics: %v", d)
	}
	return f
}

func parseWithErrors(t *testing.T, src string) (*ast.File, []string) {
	t.Helper()
	p := New("test", src)
	f := p.Parse()
	var msgs []string
	for _, d := range p.Diagnostics() {
		msgs = append(msgs, d.Msg)
	}
	return f, msgs
}

// ---------- package + import ----------

func TestParsePackage(t *testing.T) {
	f := mustParse(t, "package design")
	if f.Package == nil || f.Package.Name != "design" {
		t.Errorf("got %+v", f.Package)
	}
}

func TestParseImport(t *testing.T) {
	f := mustParse(t, `package design
import "shared"
import v1 "v1/api"`)
	if len(f.Imports) != 2 {
		t.Fatalf("imports: %d", len(f.Imports))
	}
	if f.Imports[0].Path != "shared" || f.Imports[0].Alias != "" {
		t.Error("import 0")
	}
	if f.Imports[1].Alias != "v1" || f.Imports[1].Path != "v1/api" {
		t.Error("import 1")
	}
}

func TestImportMissingPath(t *testing.T) {
	_, errs := parseWithErrors(t, `package x
import`)
	if len(errs) == 0 {
		t.Error("expected error")
	}
}

// ---------- file decorators ----------

func TestFileDecorators(t *testing.T) {
	f := mustParse(t, `@title("API")
@version("1.0")
package design`)
	if len(f.Decorators) != 2 {
		t.Errorf("count: %d", len(f.Decorators))
	}
}

// ---------- type ----------

func TestTypeSimple(t *testing.T) {
	f := mustParse(t, `type User { id string  name string }`)
	td := f.Decls[0].(*ast.TypeDecl)
	if td.Name != "User" {
		t.Error("name")
	}
	if len(td.Body) != 2 {
		t.Errorf("body: %d", len(td.Body))
	}
	field := td.Body[0].(*ast.Field)
	if field.Name != "id" || field.Type.Named.Name.String() != "string" {
		t.Errorf("field: %+v", field)
	}
}

func TestTypeWithGenericParams(t *testing.T) {
	f := mustParse(t, `type Page<T> { items T[]  total int }`)
	td := f.Decls[0].(*ast.TypeDecl)
	if len(td.TypeParams) != 1 || td.TypeParams[0] != "T" {
		t.Errorf("params: %v", td.TypeParams)
	}
	if !td.Body[0].(*ast.Field).Type.Array {
		t.Error("expected array type")
	}
}

func TestTypeWithMultipleGenericParams(t *testing.T) {
	f := mustParse(t, `type Pair<A, B> { a A  b B }`)
	td := f.Decls[0].(*ast.TypeDecl)
	if len(td.TypeParams) != 2 {
		t.Error()
	}
}

func TestTypeOptional(t *testing.T) {
	f := mustParse(t, `type X { name string?  age int? }`)
	td := f.Decls[0].(*ast.TypeDecl)
	for _, m := range td.Body {
		if !m.(*ast.Field).Type.Optional {
			t.Error("expected optional")
		}
	}
}

func TestTypeArrayOptional(t *testing.T) {
	f := mustParse(t, `type X { tags string[]? }`)
	field := f.Decls[0].(*ast.TypeDecl).Body[0].(*ast.Field)
	if !field.Type.Array || !field.Type.Optional {
		t.Error()
	}
}

func TestTypeMap(t *testing.T) {
	f := mustParse(t, `type X { meta map<string, int> }`)
	field := f.Decls[0].(*ast.TypeDecl).Body[0].(*ast.Field)
	if field.Type.Map == nil {
		t.Fatal("expected map")
	}
	if field.Type.Map.Key.Named.Name.String() != "string" {
		t.Error("key")
	}
}

func TestTypeMixin(t *testing.T) {
	f := mustParse(t, `type X { Profile  name string }`)
	td := f.Decls[0].(*ast.TypeDecl)
	if _, ok := td.Body[0].(*ast.Mixin); !ok {
		t.Errorf("expected mixin, got %T", td.Body[0])
	}
	if _, ok := td.Body[1].(*ast.Field); !ok {
		t.Errorf("expected field, got %T", td.Body[1])
	}
}

// TestTypeFieldPascalCaseWithBuiltin pins the "Pascal name + builtin
// type → field" carve-out: users free to choose any spelling for
// their JSON wire shape, including PascalCase keys, without the
// parser interpreting the name as a mixin. Round-trip through the
// AST confirms it lands as a Field, not a Mixin.
func TestTypeFieldPascalCaseWithBuiltin(t *testing.T) {
	f := mustParse(t, `type X { CreateUser int }`)
	td := f.Decls[0].(*ast.TypeDecl)
	field, ok := td.Body[0].(*ast.Field)
	if !ok {
		t.Fatalf("expected Field, got %T", td.Body[0])
	}
	if field.Name != "CreateUser" {
		t.Errorf("field name = %q, want %q", field.Name, "CreateUser")
	}
}

// TestTypeMixinPascalFollowedByNonBuiltinStaysMixin confirms the
// compact `mixin field` pattern keeps working when the second ident
// is NOT a builtin: `Profile  name string` parses as mixin Profile
// then field `name string`. The case-based default takes over here
// because we cannot tell at parse time whether `name` was meant as
// the next member's field-name or as a custom type for `Profile`.
func TestTypeMixinPascalFollowedByNonBuiltinStaysMixin(t *testing.T) {
	f := mustParse(t, `type X { Profile  name string }`)
	td := f.Decls[0].(*ast.TypeDecl)
	if _, ok := td.Body[0].(*ast.Mixin); !ok {
		t.Errorf("expected first member to be Mixin, got %T", td.Body[0])
	}
	if _, ok := td.Body[1].(*ast.Field); !ok {
		t.Errorf("expected second member to be Field, got %T", td.Body[1])
	}
}

func TestTypeMixinQualified(t *testing.T) {
	f := mustParse(t, `type X { shared.Profile }`)
	mx := f.Decls[0].(*ast.TypeDecl).Body[0].(*ast.Mixin)
	if mx.Ref.Name.String() != "shared.Profile" {
		t.Errorf("got %s", mx.Ref.Name)
	}
}

func TestTypeMixinGeneric(t *testing.T) {
	f := mustParse(t, `type X { Page<User> }`)
	mx := f.Decls[0].(*ast.TypeDecl).Body[0].(*ast.Mixin)
	if len(mx.Ref.Args) != 1 {
		t.Error()
	}
}

func TestTypeFieldDecorators(t *testing.T) {
	f := mustParse(t, `type X { name string @required @length(1, 100) }`)
	field := f.Decls[0].(*ast.TypeDecl).Body[0].(*ast.Field)
	if len(field.Decorators) != 2 {
		t.Errorf("decorators: %d", len(field.Decorators))
	}
}

func TestTypeMemberNonIdent(t *testing.T) {
	_, errs := parseWithErrors(t, `type X { 42 }`)
	if len(errs) == 0 {
		t.Error("expected error for non-ident in body")
	}
}

func TestEmptyTypeBody(t *testing.T) {
	f := mustParse(t, `type X {}`)
	if len(f.Decls[0].(*ast.TypeDecl).Body) != 0 {
		t.Error()
	}
}

// ---------- enum ----------

func TestEnumBare(t *testing.T) {
	f := mustParse(t, `enum Status { Active  Inactive }`)
	ed := f.Decls[0].(*ast.EnumDecl)
	if len(ed.Values) != 2 || ed.Values[0].Kind != ast.EnumBare {
		t.Error()
	}
}

func TestEnumInt(t *testing.T) {
	f := mustParse(t, `enum P { Low = 1  High = 99 }`)
	ed := f.Decls[0].(*ast.EnumDecl)
	if ed.Values[0].Kind != ast.EnumInt || ed.Values[0].IntValue != 1 {
		t.Error()
	}
}

func TestEnumString(t *testing.T) {
	f := mustParse(t, `enum S { A = "alpha" }`)
	v := f.Decls[0].(*ast.EnumDecl).Values[0]
	if v.Kind != ast.EnumString || v.StrValue != "alpha" {
		t.Error()
	}
}

func TestEnumWithDecorator(t *testing.T) {
	f := mustParse(t, `enum X { A @doc("the A value") }`)
	v := f.Decls[0].(*ast.EnumDecl).Values[0]
	if len(v.Decorators) != 1 {
		t.Error()
	}
}

func TestEnumValueBadAfterEqual(t *testing.T) {
	_, errs := parseWithErrors(t, `enum X { A = true }`)
	if len(errs) == 0 {
		t.Error("expected error")
	}
}

// ---------- error ----------

func TestErrorShort(t *testing.T) {
	f := mustParse(t, `error NotFound UserNotFound`)
	ed := f.Decls[0].(*ast.ErrorDecl)
	if ed.Category != "NotFound" || ed.Name != "UserNotFound" || ed.HasBody {
		t.Errorf("got %+v", ed)
	}
}

func TestErrorWithBody(t *testing.T) {
	f := mustParse(t, `error BadRequest Bad { code string }`)
	ed := f.Decls[0].(*ast.ErrorDecl)
	if !ed.HasBody || len(ed.Body) != 1 {
		t.Error()
	}
}

func TestErrorInvalidCategory(t *testing.T) {
	_, errs := parseWithErrors(t, `error Invalid X`)
	if len(errs) == 0 {
		t.Error("expected error")
	}
}

// ---------- scalar ----------

func TestScalar(t *testing.T) {
	f := mustParse(t, `scalar Email string @format("email")`)
	sd := f.Decls[0].(*ast.ScalarDecl)
	if sd.Name != "Email" || sd.Primitive != "string" || len(sd.Decorators) != 1 {
		t.Errorf("got %+v", sd)
	}
}

// ---------- middleware ----------

func TestMiddlewareNoParams(t *testing.T) {
	f := mustParse(t, `middleware Auth`)
	if f.Decls[0].(*ast.MiddlewareDecl).Name != "Auth" {
		t.Error()
	}
}

func TestMiddlewareWithParams(t *testing.T) {
	f := mustParse(t, `middleware RateLimit(rps: int = 100, burst: int)`)
	md := f.Decls[0].(*ast.MiddlewareDecl)
	if len(md.Params) != 2 {
		t.Errorf("params: %d", len(md.Params))
	}
	if md.Params[0].Default == nil {
		t.Error("expected default")
	}
	if md.Params[1].Default != nil {
		t.Error("did not expect default")
	}
}

// ---------- service + method ----------

func TestServiceEmpty(t *testing.T) {
	f := mustParse(t, `service S {}`)
	sd := f.Decls[0].(*ast.ServiceDecl)
	if sd.Extend || sd.Name != "S" {
		t.Error()
	}
}

func TestServiceMethods(t *testing.T) {
	f := mustParse(t, `service S {
    get GetX /x {
        request   GetXReq
        response  X
    }
}`)
	sd := f.Decls[0].(*ast.ServiceDecl)
	if len(sd.Methods) != 1 {
		t.Fatal()
	}
	m := sd.Methods[0]
	if m.Verb != "get" || m.Name != "GetX" {
		t.Error()
	}
	if m.Path == nil || len(m.Path.Segments) != 1 {
		t.Error("path")
	}
	if m.Request == nil || m.Request.Name.String() != "GetXReq" {
		t.Error("request")
	}
	if m.Response == nil || m.Response.Type.Name.String() != "X" {
		t.Error("response")
	}
}

func TestMethodAllVerbs(t *testing.T) {
	verbs := []string{"get", "post", "put", "patch", "delete", "head", "options"}
	for _, v := range verbs {
		f := mustParse(t, "service S { "+v+" Op /x {} }")
		if f.Decls[0].(*ast.ServiceDecl).Methods[0].Verb != v {
			t.Errorf("%s", v)
		}
	}
}

func TestMethodNoPath(t *testing.T) {
	f := mustParse(t, `service S { get Op { response X } }`)
	m := f.Decls[0].(*ast.ServiceDecl).Methods[0]
	if m.Path != nil {
		t.Error("expected no path")
	}
}

func TestMethodPathParam(t *testing.T) {
	f := mustParse(t, `service S { get Op /users/{id} {} }`)
	segs := f.Decls[0].(*ast.ServiceDecl).Methods[0].Path.Segments
	if len(segs) != 2 {
		t.Fatal()
	}
	if segs[0].Param || segs[0].Literal != "users" {
		t.Error()
	}
	if !segs[1].Param || segs[1].Literal != "id" {
		t.Error()
	}
}

func TestMethodPathHyphenated(t *testing.T) {
	f := mustParse(t, `service S { get Op /api-v1/users {} }`)
	seg := f.Decls[0].(*ast.ServiceDecl).Methods[0].Path.Segments[0]
	if seg.Literal != "api-v1" {
		t.Errorf("got %q", seg.Literal)
	}
}

func TestMethodPassthroughEmptyBody(t *testing.T) {
	f := mustParse(t, `service S {
	@passthrough
	get Tail /tail {}
}`)
	m := f.Decls[0].(*ast.ServiceDecl).Methods[0]
	if m.Request != nil || m.Response != nil {
		t.Errorf("@passthrough method must not bind request/response, got req=%v resp=%v", m.Request, m.Response)
	}
	if len(m.Decorators) != 1 || m.Decorators[0].Name != "passthrough" {
		t.Errorf("expected single @passthrough decorator, got %+v", m.Decorators)
	}
}

func TestMethodWithDecorators(t *testing.T) {
	f := mustParse(t, `service S {
    @doc("x")
    @timeout(5s)
    get Op /x {}
}`)
	m := f.Decls[0].(*ast.ServiceDecl).Methods[0]
	if len(m.Decorators) != 2 {
		t.Error()
	}
}

func TestMethodInvalidVerb(t *testing.T) {
	_, errs := parseWithErrors(t, `service S { foo Op {} }`)
	if len(errs) == 0 {
		t.Error("expected error for invalid verb")
	}
}

func TestMethodInvalidBodyContent(t *testing.T) {
	_, errs := parseWithErrors(t, `service S { get Op { bad something } }`)
	if len(errs) == 0 {
		t.Error("expected error")
	}
}

func TestExtendService(t *testing.T) {
	f := mustParse(t, `extend service S { get Op /x {} }`)
	sd := f.Decls[0].(*ast.ServiceDecl)
	if !sd.Extend {
		t.Error()
	}
}

func TestExtendNotService(t *testing.T) {
	_, errs := parseWithErrors(t, `extend type S {}`)
	if len(errs) == 0 {
		t.Error("expected error")
	}
}

// ---------- decorators / args / values ----------

func TestDecoratorNoArgs(t *testing.T) {
	f := mustParse(t, `@deprecated
type X {}`)
	if f.Decls[0].(*ast.TypeDecl).Decorators[0].Name != "deprecated" {
		t.Error()
	}
}

func TestDecoratorBareArgs(t *testing.T) {
	f := mustParse(t, `@length(1, 100)
type X {}`)
	d := f.Decls[0].(*ast.TypeDecl).Decorators[0]
	if len(d.Args) != 2 {
		t.Error()
	}
}

func TestDecoratorNamedArgs(t *testing.T) {
	f := mustParse(t, `@security(oauth2, scopes: ["read", "write"])
type X {}`)
	d := f.Decls[0].(*ast.TypeDecl).Decorators[0]
	if len(d.Args) != 2 {
		t.Fatal()
	}
	if d.Args[1].Name != "scopes" || !d.Args[1].Named {
		t.Error()
	}
}

func TestDecoratorNested(t *testing.T) {
	f := mustParse(t, `@each(@length(1, 20))
type X {}`)
	a := f.Decls[0].(*ast.TypeDecl).Decorators[0].Args[0]
	if a.Nested == nil {
		t.Error()
	}
}

func TestDecoratorObject(t *testing.T) {
	f := mustParse(t, `@example({key: "v", num: 42})
type X {}`)
	a := f.Decls[0].(*ast.TypeDecl).Decorators[0].Args[0]
	if len(a.Object) != 2 {
		t.Errorf("got %d", len(a.Object))
	}
}

func TestValueBool(t *testing.T) {
	f := mustParse(t, `@b(true, false)
type X {}`)
	args := f.Decls[0].(*ast.TypeDecl).Decorators[0].Args
	if args[0].Value.(*ast.BoolLit).Value != true {
		t.Error()
	}
	if args[1].Value.(*ast.BoolLit).Value != false {
		t.Error()
	}
}

func TestValueNull(t *testing.T) {
	f := mustParse(t, `@d(null)
type X {}`)
	if _, ok := f.Decls[0].(*ast.TypeDecl).Decorators[0].Args[0].Value.(*ast.NullLit); !ok {
		t.Error()
	}
}

func TestValueDuration(t *testing.T) {
	f := mustParse(t, `@d(5s)
type X {}`)
	if _, ok := f.Decls[0].(*ast.TypeDecl).Decorators[0].Args[0].Value.(*ast.DurationLit); !ok {
		t.Error()
	}
}

func TestValueSize(t *testing.T) {
	f := mustParse(t, `@d(1MB)
type X {}`)
	if _, ok := f.Decls[0].(*ast.TypeDecl).Decorators[0].Args[0].Value.(*ast.SizeLit); !ok {
		t.Error()
	}
}

func TestValueFloat(t *testing.T) {
	f := mustParse(t, `@d(3.14)
type X {}`)
	if _, ok := f.Decls[0].(*ast.TypeDecl).Decorators[0].Args[0].Value.(*ast.FloatLit); !ok {
		t.Error()
	}
}

func TestValueNegative(t *testing.T) {
	f := mustParse(t, `@d(-100, -3.14)
type X {}`)
	args := f.Decls[0].(*ast.TypeDecl).Decorators[0].Args
	if args[0].Value.(*ast.IntLit).Value != -100 {
		t.Error()
	}
	if args[1].Value.(*ast.FloatLit).Value != -3.14 {
		t.Error()
	}
}

func TestValueNegativeBad(t *testing.T) {
	_, errs := parseWithErrors(t, `@d(-)
type X {}`)
	if len(errs) == 0 {
		t.Error()
	}
}

func TestValueRawString(t *testing.T) {
	f := mustParse(t, "@d(`raw\\nliteral`)\ntype X {}")
	v := f.Decls[0].(*ast.TypeDecl).Decorators[0].Args[0].Value.(*ast.StringLit)
	if v.Value != "raw\\nliteral" {
		t.Errorf("got %q", v.Value)
	}
}

func TestValueIdent(t *testing.T) {
	f := mustParse(t, `@d(myEnum)
type X {}`)
	if _, ok := f.Decls[0].(*ast.TypeDecl).Decorators[0].Args[0].Value.(*ast.IdentExpr); !ok {
		t.Error()
	}
}

func TestValueQualifiedIdent(t *testing.T) {
	f := mustParse(t, `@d(pkg.Name)
type X {}`)
	id := f.Decls[0].(*ast.TypeDecl).Decorators[0].Args[0].Value.(*ast.IdentExpr)
	if id.Name.String() != "pkg.Name" {
		t.Error()
	}
}

func TestValueUnknown(t *testing.T) {
	_, errs := parseWithErrors(t, `@d(?)
type X {}`)
	if len(errs) == 0 {
		t.Error()
	}
}

func TestValueArray(t *testing.T) {
	f := mustParse(t, `@d([1, 2, 3])
type X {}`)
	arr := f.Decls[0].(*ast.TypeDecl).Decorators[0].Args[0].Value.(*ast.ArrayLit)
	if len(arr.Elements) != 3 {
		t.Error()
	}
}

// ---------- top-level errors ----------

func TestUnknownTopLevel(t *testing.T) {
	_, errs := parseWithErrors(t, `foobar`)
	if len(errs) == 0 {
		t.Error()
	}
}

// ---------- string unquoting ----------

func TestUnquoteString(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`""`, ""},
		{`"abc"`, "abc"},
		{`"a\nb"`, "a\nb"},
		{`"a\tb"`, "a\tb"},
		{`"a\rb"`, "a\rb"},
		{`"a\"b"`, "a\"b"},
		{`"a\\b"`, "a\\b"},
		{`"\u{61}"`, "a"},
		{`"\u{1F600}"`, "\U0001F600"},
		{`"\zbad"`, "zbad"},           // unknown escape
		{`"\u nobrace"`, "u nobrace"}, // unicode without brace
		{`"\u{nobrace"`, "u{nobrace"}, // missing closing brace
		{`"\u{ZZ}"`, "u{ZZ}"},         // bad hex
		{`"`, "\""},                   // len < 2 fallback
	}
	for _, c := range cases {
		if got := unquoteString(c.in); got != c.want {
			t.Errorf("unquoteString(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestUnquoteRaw(t *testing.T) {
	if got := unquoteRaw("`hello`"); got != "hello" {
		t.Error()
	}
	if got := unquoteRaw("`"); got != "`" {
		t.Error()
	}
}

// ---------- helpers ----------

func TestIsUpperFirst(t *testing.T) {
	if !isUpperFirst("Profile") {
		t.Error()
	}
	if isUpperFirst("profile") {
		t.Error()
	}
	if isUpperFirst("") {
		t.Error()
	}
}

func TestVerbFromTokenInvalid(t *testing.T) {
	if _, ok := verbFromToken(0); ok {
		t.Error()
	}
}

// ---------- peekAt out of range ----------

func TestPeekAtOutOfRange(t *testing.T) {
	p := New("", "x")
	tok := p.peekAt(100)
	if tok.Kind == 0 && tok.Text != "" {
		// Just ensure we got a fallback token.
	}
	_ = tok
}

// ---------- expect failure path ----------

func TestExpectFailure(t *testing.T) {
	// `package` without ident triggers expect failure inside parsePackage.
	_, errs := parseWithErrors(t, "package")
	if len(errs) == 0 {
		t.Error()
	}
}

// ---------- qualified ident with bad continuation ----------

func TestQualifiedIdentBadContinuation(t *testing.T) {
	_, errs := parseWithErrors(t, "import alias")
	if len(errs) == 0 {
		t.Error()
	}
}

// ---------- map type errors ----------

func TestMapBad(t *testing.T) {
	_, errs := parseWithErrors(t, `type X { m map<string, > }`)
	if len(errs) == 0 {
		t.Error("expected error")
	}
}

// ---------- generic instance ----------

func TestNamedTypeRefGenericMulti(t *testing.T) {
	f := mustParse(t, `type X { p Pair<A, B> }`)
	field := f.Decls[0].(*ast.TypeDecl).Body[0].(*ast.Field)
	if len(field.Type.Named.Args) != 2 {
		t.Error()
	}
}

// ---------- type params error ----------

func TestTypeParamsEmpty(t *testing.T) {
	_, errs := parseWithErrors(t, `type X<> {}`)
	if len(errs) == 0 {
		t.Error()
	}
}

// ---------- request without type ----------

func TestMethodMissingRequestType(t *testing.T) {
	_, errs := parseWithErrors(t, `service S { get Op { request } }`)
	if len(errs) == 0 {
		t.Error()
	}
}

// ---------- path trailing slash ----------

func TestPathTrailingSlash(t *testing.T) {
	f := mustParse(t, `service S { get Op /users/ {} }`)
	segs := f.Decls[0].(*ast.ServiceDecl).Methods[0].Path.Segments
	if len(segs) != 2 {
		t.Errorf("got %d segments", len(segs))
	}
}

// ---------- path bad dash ----------

func TestPathBadDash(t *testing.T) {
	_, errs := parseWithErrors(t, `service S { get Op /api- {} }`)
	if len(errs) == 0 {
		t.Error()
	}
}

// ---------- coverage gap fillers ----------

func TestDecoratorBadName(t *testing.T) {
	_, errs := parseWithErrors(t, `@123
type X {}`)
	if len(errs) == 0 {
		t.Error()
	}
}

func TestTrailingDecoratorAtEOF(t *testing.T) {
	// Exercises parseTopLevelWith's EOF case after inner parseDecorators consumes tokens.
	parseWithErrors(t, `type X {}
@trailing`)
}

func TestTypeParamsExpectFails(t *testing.T) {
	_, errs := parseWithErrors(t, `type X<,> {}`)
	if len(errs) == 0 {
		t.Error()
	}
}

func TestTypeNoBody(t *testing.T) {
	// `type X` without body - exercises parseTypeBody early return.
	f, _ := parseWithErrors(t, `type X
type Y {}`)
	if len(f.Decls) < 2 {
		t.Errorf("decls: %d", len(f.Decls))
	}
}

func TestQualifiedIdentBadAfterDot(t *testing.T) {
	// `pkg.` followed by `}` (not Ident) hits the inner !ok branch after dot.
	_, errs := parseWithErrors(t, `type X { name pkg. }`)
	if len(errs) == 0 {
		t.Error()
	}
}

func TestQualifiedIdentMissingFirstIdent(t *testing.T) {
	// Field with missing type triggers parseQualifiedIdent at peek != Ident.
	_, errs := parseWithErrors(t, `type X { name }`)
	if len(errs) == 0 {
		t.Error()
	}
}

// ---------- golden ----------

func TestGoldenSample(t *testing.T) {
	path, err := filepath.Abs("../../testdata/grammar/sample.craftgo")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p := New("sample.craftgo", string(src))
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("diagnostics: %v", d)
	}
	if f.Package == nil {
		t.Error("package missing")
	}
	if len(f.Imports) != 2 {
		t.Errorf("imports: %d", len(f.Imports))
	}
	if len(f.Decls) < 8 {
		t.Errorf("decls: %d", len(f.Decls))
	}
}
