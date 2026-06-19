package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// TestCaptureDocPropagatesDoc covers the if-true branch of
// [Parser.captureDoc]: when the peeked token carries doc-comment
// lines, captureDoc must copy them onto pendingDoc so the next decl
// picks them up.
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

// mustParseTypeDecl is the parser-test convenience wrapper for
// "parse this DSL and give me the first TypeDecl". Fatal if the source
// doesn't parse OR doesn't start with a TypeDecl. Used heavily by the
// table-driven type-shape tests so each case stays focused on
// assertion shape, not the cast-and-extract dance.
func mustParseTypeDecl(t *testing.T, src string) *ast.TypeDecl {
	t.Helper()
	f := mustParse(t, src)
	if len(f.Decls) == 0 {
		t.Fatalf("expected at least one decl, got none\nsrc: %s", src)
	}
	td, ok := f.Decls[0].(*ast.TypeDecl)
	if !ok {
		t.Fatalf("Decls[0] = %T, want *ast.TypeDecl\nsrc: %s", f.Decls[0], src)
	}
	return td
}

// stringsEqual is a small helper for slice-of-string equality where
// nil and empty should both be treated as "no entries". Test cases
// that don't supply wantParams should compare equal to a parser that
// emits a nil slice for non-generic type decls.
func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// renderMembers gives a compact dump of a type body for failure
// messages - `[id string; name string]` is easier to scan than the
// raw `%+v` of nested AST nodes.
func renderMembers(ms []ast.TypeMember) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		switch v := m.(type) {
		case *ast.Field:
			parts[i] = v.Name + " " + renderTypeRef(v.Type)
		case *ast.Mixin:
			if v.Ref != nil {
				parts[i] = v.Ref.Name.String()
				if len(v.Ref.Args) > 0 {
					inner := make([]string, len(v.Ref.Args))
					for j, a := range v.Ref.Args {
						inner[j] = renderTypeRef(a)
					}
					parts[i] += "<" + strings.Join(inner, ", ") + ">"
				}
			}
		default:
			parts[i] = "?"
		}
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

func renderTypeRef(t *ast.TypeRef) string {
	if t == nil {
		return "?"
	}
	if t.Map != nil {
		return "map<" + renderTypeRef(t.Map.Key) + ", " + renderTypeRef(t.Map.Value) + ">"
	}
	out := ""
	if t.Named != nil {
		out = t.Named.Name.String()
		if len(t.Named.Args) > 0 {
			inner := make([]string, len(t.Named.Args))
			for i, a := range t.Named.Args {
				inner[i] = renderTypeRef(a)
			}
			out += "<" + strings.Join(inner, ", ") + ">"
		}
	}
	if t.Array {
		out += "[]"
	}
	if t.Optional {
		out += "?"
	}
	return out
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
	f := mustParse(t, `@version("1.0")
@doc("file-level doc")
package design`)
	if len(f.Decorators) != 2 {
		t.Errorf("count: %d", len(f.Decorators))
	}
}

// ---------- type ----------

// TestParseTypeShapes table-drives every type-body shape variant the
// parser produces. Each row is one DSL source line + the expected
// TypeDecl shape; the assertion goes through [ast.Equal] / [ast.MembersEqual]
// so adding a new variant is one row, not one function.
//
// Decorator presence, doc, comments are NOT asserted here - they have
// their own dedicated table below ([TestParseTypeDecorators]) so a
// failure in shape parsing doesn't drown out decorator regressions
// and vice versa.
func TestParseTypeShapes(t *testing.T) {
	cases := []struct {
		name       string
		src        string
		wantName   string
		wantParams []string         // generic type params
		wantBody   []ast.TypeMember // expected body in source order
	}{
		{
			name:     "simple two fields",
			src:      `type User { id string  name string }`,
			wantName: "User",
			wantBody: []ast.TypeMember{
				ast.FieldOf("id", "string"),
				ast.FieldOf("name", "string"),
			},
		},
		{
			name:       "generic single param + array field",
			src:        `type Page<T> { items T[]  total int }`,
			wantName:   "Page",
			wantParams: []string{"T"},
			wantBody: []ast.TypeMember{
				ast.FieldT("items", ast.NamedArr("T")),
				ast.FieldOf("total", "int"),
			},
		},
		{
			name:       "generic multi-param",
			src:        `type Pair<A, B> { a A  b B }`,
			wantName:   "Pair",
			wantParams: []string{"A", "B"},
			wantBody: []ast.TypeMember{
				ast.FieldOf("a", "A"),
				ast.FieldOf("b", "B"),
			},
		},
		{
			name:     "optional fields",
			src:      `type X { name string?  age int? }`,
			wantName: "X",
			wantBody: []ast.TypeMember{
				ast.FieldT("name", ast.NamedOpt("string")),
				ast.FieldT("age", ast.NamedOpt("int")),
			},
		},
		{
			name:     "array optional combo",
			src:      `type X { tags string[]? }`,
			wantName: "X",
			wantBody: []ast.TypeMember{
				ast.FieldT("tags", ast.NamedArrOpt("string")),
			},
		},
		{
			name:     "map field",
			src:      `type X { meta map<string, int> }`,
			wantName: "X",
			wantBody: []ast.TypeMember{
				ast.FieldT("meta", ast.MapOf("string", "int")),
			},
		},
		{
			name:     "bare mixin then field",
			src:      `type X { Profile  name string }`,
			wantName: "X",
			wantBody: []ast.TypeMember{
				ast.MixinOf("Profile"),
				ast.FieldOf("name", "string"),
			},
		},
		{
			// PascalCase + builtin must land as a Field, not a Mixin -
			// users are free to spell JSON keys however they want.
			name:     "PascalCase + builtin = field",
			src:      `type X { CreateUser int }`,
			wantName: "X",
			wantBody: []ast.TypeMember{
				ast.FieldOf("CreateUser", "int"),
			},
		},
		{
			name:     "qualified mixin",
			src:      `type X { shared.Profile }`,
			wantName: "X",
			wantBody: []ast.TypeMember{
				ast.MixinQualified("shared", "Profile"),
			},
		},
		{
			name:     "generic mixin",
			src:      `type X { Page<User> }`,
			wantName: "X",
			wantBody: []ast.TypeMember{
				&ast.Mixin{Ref: &ast.NamedTypeRef{
					Name: &ast.QualifiedIdent{Parts: []string{"Page"}},
					Args: []*ast.TypeRef{ast.Named("User")},
				}},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			td := mustParseTypeDecl(t, c.src)
			if td.Name != c.wantName {
				t.Errorf("name = %q, want %q", td.Name, c.wantName)
			}
			if !stringsEqual(td.TypeParams, c.wantParams) {
				t.Errorf("type params = %v, want %v", td.TypeParams, c.wantParams)
			}
			if !ast.MembersEqual(td.Body, c.wantBody) {
				t.Errorf("body mismatch\n got: %s\nwant: %s",
					renderMembers(td.Body), renderMembers(c.wantBody))
			}
		})
	}
}

// TestParseTypeFieldDecorators stays separate from shape tests so a
// decorator regression surfaces with a focused failure label.
func TestParseTypeFieldDecorators(t *testing.T) {
	field := mustParseTypeDecl(t, `type X { name string @doc("the name") @length(1, 100) }`).
		Body[0].(*ast.Field)
	if got, want := len(field.Decorators), 2; got != want {
		t.Errorf("decorator count = %d, want %d", got, want)
	}
	if field.Decorators[0].Name != "doc" || field.Decorators[1].Name != "length" {
		t.Errorf("decorator names = %v, want [doc length]",
			[]string{field.Decorators[0].Name, field.Decorators[1].Name})
	}
}

func TestTypeMemberNonIdent(t *testing.T) {
	_, errs := parseWithErrors(t, `type X { 42 }`)
	if len(errs) == 0 {
		t.Error("expected error for non-ident in body")
	}
}

func TestKeywordFieldNames(t *testing.T) {
	// Reserved words are field names in a type body (contextual keywords).
	td := mustParseTypeDecl(t, `type X {
		type   string
		error  string
		map    string
		delete bool
	}`)
	want := []string{"type", "error", "map", "delete"}
	if len(td.Body) != len(want) {
		t.Fatalf("field count = %d, want %d", len(td.Body), len(want))
	}
	for i, w := range want {
		f, ok := td.Body[i].(*ast.Field)
		if !ok {
			t.Fatalf("member %d is %T, want *ast.Field", i, td.Body[i])
		}
		if f.Name != w {
			t.Errorf("field %d name = %q, want %q", i, f.Name, w)
		}
	}
}

func TestKeywordEnumValues(t *testing.T) {
	vals := mustParse(t, `enum Kind { type  map  delete }`).
		Decls[0].(*ast.EnumDecl).EnumValues()
	want := []string{"type", "map", "delete"}
	if len(vals) != len(want) {
		t.Fatalf("value count = %d, want %d", len(vals), len(want))
	}
	for i, w := range want {
		if vals[i].Name != w {
			t.Errorf("value %d = %q, want %q", i, vals[i].Name, w)
		}
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
	if len(ed.EnumValues()) != 2 || ed.EnumValues()[0].Kind != ast.EnumBare {
		t.Error()
	}
}

func TestEnumInt(t *testing.T) {
	f := mustParse(t, `enum P { Low = 1  High = 99 }`)
	ed := f.Decls[0].(*ast.EnumDecl)
	if ed.EnumValues()[0].Kind != ast.EnumInt || ed.EnumValues()[0].IntValue != 1 {
		t.Error()
	}
}

func TestEnumString(t *testing.T) {
	f := mustParse(t, `enum S { A = "alpha" }`)
	v := f.Decls[0].(*ast.EnumDecl).EnumValues()[0]
	if v.Kind != ast.EnumString || v.StrValue != "alpha" {
		t.Error()
	}
}

func TestEnumWithDecorator(t *testing.T) {
	f := mustParse(t, `enum X { A @doc("the A value") }`)
	v := f.Decls[0].(*ast.EnumDecl).EnumValues()[0]
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

// TestMiddlewareRejectsParams pins the rule that the DSL captures
// only the middleware name. Any `(...)` after the name fails parsing
// because configuration (params, defaults, dependencies) lives in the
// hand-written Go impl file, never in the DSL surface.
func TestMiddlewareRejectsParams(t *testing.T) {
	p := New("t.craftgo", `middleware RateLimit(rps: int = 100)`)
	p.Parse()
	if len(p.Diagnostics()) == 0 {
		t.Fatal("expected a diagnostic for middleware with params")
	}
	got := p.Diagnostics()[0].Msg
	if !strings.Contains(got, "no parameters") {
		t.Errorf("expected 'no parameters' diagnostic, got %q", got)
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
	if len(sd.Methods()) != 1 {
		t.Fatal()
	}
	m := sd.Methods()[0]
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
		if f.Decls[0].(*ast.ServiceDecl).Methods()[0].Verb != v {
			t.Errorf("%s", v)
		}
	}
}

func TestMethodNoPath(t *testing.T) {
	f := mustParse(t, `service S { get Op { response X } }`)
	m := f.Decls[0].(*ast.ServiceDecl).Methods()[0]
	if m.Path != nil {
		t.Error("expected no path")
	}
}

func TestMethodPathParam(t *testing.T) {
	f := mustParse(t, `service S { get Op /users/{id} {} }`)
	segs := f.Decls[0].(*ast.ServiceDecl).Methods()[0].Path.Segments
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
	seg := f.Decls[0].(*ast.ServiceDecl).Methods()[0].Path.Segments[0]
	if seg.Literal != "api-v1" {
		t.Errorf("got %q", seg.Literal)
	}
}

func TestMethodPassthroughEmptyBody(t *testing.T) {
	f := mustParse(t, `service S {
	@passthrough
	get Tail /tail {}
}`)
	m := f.Decls[0].(*ast.ServiceDecl).Methods()[0]
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
	m := f.Decls[0].(*ast.ServiceDecl).Methods()[0]
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
	// Parser-level: confirm `name: value` is captured as a named arg and
	// retains its position in the arg slice. The decorator name is
	// arbitrary - the parser accepts any decorator shape and lets the
	// semantic registry decide which names + arg shapes are valid.
	f := mustParse(t, `@hypothetical(positional, key: "value")
type X {}`)
	d := f.Decls[0].(*ast.TypeDecl).Decorators[0]
	if len(d.Args) != 2 {
		t.Fatal()
	}
	if d.Args[1].Name != "key" || !d.Args[1].Named {
		t.Error()
	}
}

func TestDecoratorNested(t *testing.T) {
	// Parser-level: confirm a decorator-arg in the form `@outer(@inner)`
	// preserves the nested decorator on `arg.Nested`. The semantic
	// registry doesn't recognise this decorator pair - that's
	// intentional, the parser must keep the grammar shape even when no
	// downstream consumer claims it, so future meta-decorators can
	// land without grammar churn.
	f := mustParse(t, `@wrap(@length(1, 20))
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
	// peekAt past EOF returns a zero-value fallback token without panicking.
	tok := p.peekAt(100)
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

// A duplicate type-parameter name lowers to `type X[T any, T any]`, which the
// Go compiler rejects - so it must be rejected at parse time instead of
// producing non-compiling generated Go.
func TestTypeParamsDuplicate(t *testing.T) {
	for _, src := range []string{`type Pair<T, T> { a T  b T }`, `type Triple<T, T, T> { a T }`} {
		_, errs := parseWithErrors(t, src)
		if len(errs) == 0 {
			t.Errorf("expected duplicate-type-parameter error for %q", src)
		}
	}
	// Distinct names are fine.
	if _, errs := parseWithErrors(t, `type OK<K, V> { k K  v V }`); len(errs) != 0 {
		t.Errorf("distinct type params should parse clean, got %v", errs)
	}
}

// ---------- request without type ----------

func TestMethodMissingRequestType(t *testing.T) {
	_, errs := parseWithErrors(t, `service S { get Op { request } }`)
	if len(errs) == 0 {
		t.Error()
	}
}

// A second request/response clause silently discarded the first - reject it.
func TestMethodDuplicateClause(t *testing.T) {
	for _, src := range []string{
		`service S { get G /g { request A  request B  response C } }`,
		`service S { get G /g { response A  response B } }`,
	} {
		if _, errs := parseWithErrors(t, src); len(errs) == 0 {
			t.Errorf("expected duplicate-clause error for %q", src)
		}
	}
	if _, errs := parseWithErrors(t, `service S { get G /g { request A  response B } }`); len(errs) != 0 {
		t.Errorf("single request+response should be clean, got %v", errs)
	}
}

// ---------- path trailing slash ----------

func TestPathTrailingSlash(t *testing.T) {
	f := mustParse(t, `service S { get Op /users/ {} }`)
	segs := f.Decls[0].(*ast.ServiceDecl).Methods()[0].Path.Segments
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

// ---------- method body type suffix rejection ----------

func TestMethodResponseRejectsBareArray(t *testing.T) {
	// `response Order[]` would silently leave the `[` token on the
	// next iteration; instead emit a hint pointing users to wrap the
	// array in a named type.
	_, msgs := parseWithErrors(t, `package design
service S { get H /h { response Order[] } }`)
	if len(msgs) == 0 || !strings.Contains(msgs[0], "bare array") {
		t.Fatalf("expected bare-array diagnostic, got %v", msgs)
	}
}

func TestMethodResponseRejectsOptionalMarker(t *testing.T) {
	// `response User?` would silently leave the `?` and confuse the
	// next-iteration parser. Reject with a clear message.
	_, msgs := parseWithErrors(t, `package design
service S { get H /h { response User? } }`)
	if len(msgs) == 0 || !strings.Contains(msgs[0], "optional") {
		t.Fatalf("expected optional-marker diagnostic, got %v", msgs)
	}
}

func TestMethodRequestRejectsBareArray(t *testing.T) {
	_, msgs := parseWithErrors(t, `package design
service S { post H /h { request Order[] } }`)
	if len(msgs) == 0 || !strings.Contains(msgs[0], "bare array") {
		t.Fatalf("expected bare-array diagnostic, got %v", msgs)
	}
}

// ---------- golden ----------

func TestGoldenSample(t *testing.T) {
	path, err := filepath.Abs("testdata/sample.craftgo")
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

// TestParseNestedArrayLiteral confirms array elements parse via
// parseValueOrArray, so a nested array literal parses cleanly instead of
// erroring on the inner '['.
func TestParseNestedArrayLiteral(t *testing.T) {
	p := New("test", `package design
type X { f string @example([["a", "b"], ["c"]]) }`)
	file := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("nested array literal should parse cleanly, got: %v", d)
	}
	td := file.Decls[0].(*ast.TypeDecl)
	fld := td.Body[0].(*ast.Field)
	outer, ok := fld.Decorators[0].Args[0].Value.(*ast.ArrayLit)
	if !ok {
		t.Fatalf("@example arg is not an ArrayLit: %T", fld.Decorators[0].Args[0].Value)
	}
	if len(outer.Elements) != 2 {
		t.Fatalf("outer array len = %d, want 2", len(outer.Elements))
	}
	if _, ok := outer.Elements[0].(*ast.ArrayLit); !ok {
		t.Errorf("first element should be a nested ArrayLit, got %T", outer.Elements[0])
	}
}
