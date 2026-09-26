package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/lexer"
)

// TestCaptureDocPropagatesDoc pins that a declaration takes the comment lines
// above it as its Doc.
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

// TestIsPathWordTokenBranches pins that identifiers and reserved words are
// path words and nothing else is.
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

func TestFileDecorators(t *testing.T) {
	f := mustParse(t, `@version("1.0")
@doc("file-level doc")
package design`)
	if len(f.Decorators) != 2 {
		t.Errorf("count: %d", len(f.Decorators))
	}
}

// TestParseTypeShapes pins the fields and mixins each type-body form parses to.
func TestParseTypeShapes(t *testing.T) {
	cases := []struct {
		name       string
		src        string
		wantName   string
		wantParams []string
		wantBody   []ast.TypeMember
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
			td := firstDecl[*ast.TypeDecl](t, c.src)
			if td.Name != c.wantName {
				t.Errorf("name = %q, want %q", td.Name, c.wantName)
			}
			if !slices.Equal(td.TypeParams, c.wantParams) {
				t.Errorf("type params = %v, want %v", td.TypeParams, c.wantParams)
			}
			if !ast.MembersEqual(td.Body, c.wantBody) {
				t.Errorf("body mismatch\n got: %s\nwant: %s",
					renderMembers(td.Body), renderMembers(c.wantBody))
			}
		})
	}
}

// TestParseTypeFieldDecorators pins that a field keeps its trailing
// decorators in order.
func TestParseTypeFieldDecorators(t *testing.T) {
	field := firstDecl[*ast.TypeDecl](t, `type X { name string @doc("the name") @length(1, 100) }`).
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
	td := firstDecl[*ast.TypeDecl](t, `type X {
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

func TestEnumNegativeInt(t *testing.T) {
	f := mustParse(t, `enum Direction { Left = -1  Right = 1 }`)
	vs := f.Decls[0].(*ast.EnumDecl).EnumValues()
	if len(vs) != 2 {
		t.Fatalf("want 2 enum values, got %d", len(vs))
	}
	if vs[0].Kind != ast.EnumInt || vs[0].IntValue != -1 {
		t.Errorf("Left: want EnumInt -1, got kind=%v value=%d", vs[0].Kind, vs[0].IntValue)
	}
	if vs[1].Kind != ast.EnumInt || vs[1].IntValue != 1 {
		t.Errorf("Right: want EnumInt 1, got kind=%v value=%d", vs[1].Kind, vs[1].IntValue)
	}
}

func TestEnumDashWithoutInt(t *testing.T) {
	_, errs := parseWithErrors(t, `enum X { A = - }`)
	if len(errs) == 0 {
		t.Error("expected an error for '-' with no integer")
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

// A decorator with no enum value before it is one error at its `@`, and the
// value below it still parses; so is a stray `@` before `Name =`.
func TestEnumDecoratorBeforeAnyValue(t *testing.T) {
	const rule = "has no enum value before it; an enum value's decorators follow it"
	for _, c := range []struct {
		src    string
		want   []string
		values []string
	}{
		{"enum E {\n\t@doc(\"a\")\n\tA\n\tB\n}", []string{"4:2: decorator @doc " + rule}, []string{"A", "B"}},
		{"enum E { @doc(\"a\") @deprecated A }", []string{"3:10: decorator @doc " + rule, "3:20: decorator @deprecated " + rule}, []string{"A"}},
		{"enum E {\n\t@doc(\"a\")\n}", []string{"4:2: decorator @doc " + rule}, nil},
		{"enum M {\n\t@Card = \"card\"\n\tCash  = \"cash\"\n}", []string{"4:2: expected enum value name, got @"}, []string{"Card", "Cash"}},
	} {
		p := New("t.craftgo", "package p\n\n"+c.src+"\n")
		f := p.Parse()
		var got []string
		for _, d := range p.Diagnostics() {
			got = append(got, fmt.Sprintf("%d:%d: %s", d.Pos.Line, d.Pos.Column, d.Msg))
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%q: diagnostics = %q, want %q", c.src, got, c.want)
		}
		var values []string
		for _, v := range f.Decls[0].(*ast.EnumDecl).EnumValues() {
			values = append(values, v.Name)
		}
		if !slices.Equal(values, c.values) {
			t.Errorf("%q: values = %q, want %q", c.src, values, c.values)
		}
	}
}

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

func TestScalar(t *testing.T) {
	f := mustParse(t, `scalar Email string @format("email")`)
	sd := f.Decls[0].(*ast.ScalarDecl)
	if sd.Name != "Email" || sd.Primitive != "string" || len(sd.Decorators) != 1 {
		t.Errorf("got %+v", sd)
	}
}

func TestMiddlewareNoParams(t *testing.T) {
	f := mustParse(t, `middleware Auth`)
	if f.Decls[0].(*ast.MiddlewareDecl).Name != "Auth" {
		t.Error()
	}
}

// A middleware's parameter list is one error, and the declaration after it
// still parses.
func TestMiddlewareRejectsParams(t *testing.T) {
	f, msgs := parseWithErrors(t, "middleware RateLimit(rps: int = (100))\ntype T { a string }")
	if len(msgs) != 1 || !strings.Contains(msgs[0], "no parameters") {
		t.Fatalf("diagnostics = %v, want one saying a middleware takes no parameters", msgs)
	}
	if len(f.Decls) != 2 || f.Decls[1].DeclName() != "T" {
		t.Errorf("declarations = %v, want the middleware and type T", f.Decls)
	}
}

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

// Every declaration and method records where its name starts, however far
// from its keyword.
func TestDeclarationNamePositions(t *testing.T) {
	src := "type  T {}\nenum   E { A }\nerror NotFound    Err\nscalar  S string\nmiddleware M\n" +
		"service  Svc {\n\tget   Op /x {}\n}\nextend service   Svc {}\nevent    Ev { payload T }\n"
	f := mustParse(t, src)
	at := func(what string, got ast.Pos, name string) {
		t.Helper()
		if got.Offset < 0 || !strings.HasPrefix(src[got.Offset:], name) || got.Line == 0 {
			t.Errorf("%s: name position %v does not start %q", what, got, name)
		}
	}
	for _, d := range f.Decls {
		at(d.DeclName(), d.DeclNamePos(), d.DeclName())
		if sd, ok := d.(*ast.ServiceDecl); ok {
			for _, m := range sd.Methods() {
				at(m.Name, m.NamePos, m.Name)
			}
		}
	}
	if len(f.Decls) != 8 {
		t.Fatalf("parsed %d declarations, want 8", len(f.Decls))
	}
}

func TestExtendNotService(t *testing.T) {
	_, errs := parseWithErrors(t, `extend type S {}`)
	if len(errs) == 0 {
		t.Error("expected error")
	}
}

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
	// Any decorator name parses; semantic checks names and arguments.
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

// A decorator in a decorator's arguments, arguments and all, is one error at
// its `@` that names the decorator it is in; what follows it still parses.
func TestDecoratorArgumentIsNoDecorator(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"@doc(@x)\ntype X {}", "3:6: a decorator cannot be an argument of @doc"},
		{"@wrap(@length(1, 20))\ntype X {}", "3:7: a decorator cannot be an argument of @wrap"},
		{"@doc(@a(@b(\"c\")))\ntype X {}", "3:6: a decorator cannot be an argument of @doc"},
		{"type X {\n\ta string @minLength(@x(1), 2)\n\tb int\n}", "4:22: a decorator cannot be an argument of @minLength"},
		{"@doc(text: @x)\ntype X {}", "3:12: a decorator cannot be an argument of @doc"},
		{"@errors([A, @x(B)])\ntype X {}", "3:13: a decorator cannot be an argument of @errors"},
		{"@example({k: @x({v: 1})})\ntype X {}", "3:14: a decorator cannot be an argument of @example"},
		{"@doc(@)\ntype X {}", "3:6: a decorator cannot be an argument of @doc"},
	} {
		p := New("t.craftgo", "package p\n\n"+c.src+"\n")
		f := p.Parse()
		var got []string
		for _, d := range p.Diagnostics() {
			got = append(got, fmt.Sprintf("%d:%d: %s", d.Pos.Line, d.Pos.Column, d.Msg))
		}
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%q: diagnostics = %q, want [%q]", c.src, got, c.want)
		}
		if len(f.Decls) != 1 || f.Decls[0].DeclName() != "X" {
			t.Errorf("%q: declarations = %v, want the one type X", c.src, f.Decls)
		}
	}
}

// The arguments around a decorator argument keep their places.
func TestDecoratorArgumentKeepsItsNeighbours(t *testing.T) {
	f, _ := parseWithErrors(t, "package p\n\ntype X {\n\ta string @minLength(@x(1), 2)\n\tb int\n}\n")
	td := f.Decls[0].(*ast.TypeDecl)
	if got := renderMembers(td.Body); got != "[a string; b int]" {
		t.Fatalf("members = %s, want [a string; b int]", got)
	}
	args := td.Body[0].(*ast.Field).Decorators[0].Args
	if len(args) != 2 {
		t.Fatalf("got %d arguments, want 2", len(args))
	}
	if n, ok := args[1].Value.(*ast.IntLit); !ok || n.Value != 2 {
		t.Errorf("second argument = %#v, want 2", args[1].Value)
	}
}

// An argument the parser cannot read is an ast.BadExpr, and an argument list
// with a parse error ends in one, beside the arguments it read.
func TestUnreadableArgumentIsABadExpr(t *testing.T) {
	for _, c := range []struct {
		src  string
		want string
	}{
		{"@doc(@x)\ntype X {}", "[bad]"},
		{"@doc(=)\ntype X {}", "[bad]"},
		{"@doc(-)\ntype X {}", "[bad]"},
		{"@range(1, @x)\ntype X {}", "[int bad]"},
		{"@range(@x, 5)\ntype X {}", "[bad int]"},
		{"@errors([A, @x])\ntype X {}", "[[ident bad]]"},
		{"type X {\n\ta string @range(1\n}", "[int bad]"},
		{"@range(1 5)\ntype X {}", "[int int bad]"},
	} {
		f, msgs := parseWithErrors(t, "package p\n\n"+c.src+"\n")
		if len(msgs) == 0 {
			t.Errorf("%q: no parse error", c.src)
			continue
		}
		var d *ast.Decorator
		switch x := f.Decls[0].(type) {
		case *ast.TypeDecl:
			if len(x.Decorators) > 0 {
				d = x.Decorators[0]
			} else {
				d = x.Body[0].(*ast.Field).Decorators[0]
			}
		}
		var kinds []string
		for _, a := range d.Args {
			kinds = append(kinds, argKind(a.Value))
		}
		if got := "[" + strings.Join(kinds, " ") + "]"; got != c.want {
			t.Errorf("%q: arguments = %s, want %s", c.src, got, c.want)
		}
	}
}

// argKind names e's kind for TestUnreadableArgumentIsABadExpr.
func argKind(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.BadExpr:
		return "bad"
	case *ast.IntLit:
		return "int"
	case *ast.IdentExpr:
		return "ident"
	case *ast.ArrayLit:
		kinds := make([]string, len(v.Elements))
		for i, el := range v.Elements {
			kinds[i] = argKind(el)
		}
		return "[" + strings.Join(kinds, " ") + "]"
	}
	return fmt.Sprintf("%T", e)
}

// An argument list left open ends at an inner decorator's `)` that ends its
// line, or at the body's `}`, so the declarations below still parse.
func TestUnclosedArgumentsCloseAtAnInnerDecorator(t *testing.T) {
	for _, c := range []struct {
		src   string
		want  []string
		decls []string
	}{
		{
			"type User {\n\t@doc(\n\temail string @format(email)\n\tname  string\n}\n\ntype Other {\n\tid string\n}\n",
			[]string{"5:8: expected ',' or ')' after decorator argument, got Ident", "5:15: expected ',' or ')' after decorator argument, got @"},
			[]string{"User", "Other"},
		},
		{
			"scalar Email string @format(email) @maxLength(@x(254)\n\nscalar Other string\n\ntype T {\n\te Email\n}\n",
			[]string{"3:47: a decorator cannot be an argument of @maxLength"},
			[]string{"Email", "Other", "T"},
		},
		{
			"type User {\n\tname string\n\t@doc(\n\temail string\n}\n\ntype Other {\n\tid string\n}\n",
			[]string{"6:8: expected ',' or ')' after decorator argument, got Ident", "7:1: expected ',' or ')' after decorator argument, got }"},
			[]string{"User", "Other"},
		},
		{
			"type User {\n\t@doc(@example(\n\temail string @format(email)\n\tname  string\n}\n\ntype Other {\n\tid string\n}\n",
			[]string{"4:7: a decorator cannot be an argument of @doc", "7:1: expected ',' or ')' after decorator argument, got }"},
			[]string{"User", "Other"},
		},
		{
			"enum Status {\n\t@doc(\n\tActive\n\tInactive\n}\n\ntype Other {\n\tid string\n}\n",
			[]string{"6:2: expected ',' or ')' after decorator argument, got Ident", "7:1: expected ',' or ')' after decorator argument, got }", "4:2: decorator @doc has no enum value before it; an enum value's decorators follow it"},
			[]string{"Status", "Other"},
		},
		{
			"type T {\n\ta string @length(@x(1) 5)\n}\n",
			[]string{"4:19: a decorator cannot be an argument of @length", "4:25: expected ',' or ')' after decorator argument, got Int"},
			[]string{"T"},
		},
		{
			"@errors([@x(1) Nf])\ntype X {}\n",
			[]string{"3:10: a decorator cannot be an argument of @errors", "3:16: expected ',' or ']' after array element, got Ident"},
			[]string{"X"},
		},
	} {
		p := New("t.craftgo", "package p\n\n"+c.src)
		f := p.Parse()
		var got, decls []string
		for _, d := range p.Diagnostics() {
			got = append(got, fmt.Sprintf("%d:%d: %s", d.Pos.Line, d.Pos.Column, d.Msg))
		}
		for _, d := range f.Decls {
			decls = append(decls, d.DeclName())
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%q: diagnostics = %q, want %q", c.src, got, c.want)
		}
		if !slices.Equal(decls, c.decls) {
			t.Errorf("%q: declarations = %q, want %q", c.src, decls, c.decls)
		}
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

func TestUnknownTopLevel(t *testing.T) {
	_, errs := parseWithErrors(t, `foobar`)
	if len(errs) == 0 {
		t.Error()
	}
}

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

// peekAt past the end returns the final EOF token.
func TestPeekAtOutOfRange(t *testing.T) {
	if tok := New("", "x").peekAt(100); tok.Kind != lexer.EOF {
		t.Errorf("peekAt past the end = %v %q, want EOF", tok.Kind, tok.Text)
	}
}

func TestExpectFailure(t *testing.T) {
	// A `package` keyword with no name fails expect.
	_, errs := parseWithErrors(t, "package")
	if len(errs) == 0 {
		t.Error()
	}
}

func TestQualifiedIdentBadContinuation(t *testing.T) {
	_, errs := parseWithErrors(t, "import alias")
	if len(errs) == 0 {
		t.Error()
	}
}

func TestMapBad(t *testing.T) {
	_, errs := parseWithErrors(t, `type X { m map<string, > }`)
	if len(errs) == 0 {
		t.Error("expected error")
	}
}

func TestNamedTypeRefGenericMulti(t *testing.T) {
	f := mustParse(t, `type X { p Pair<A, B> }`)
	field := f.Decls[0].(*ast.TypeDecl).Body[0].(*ast.Field)
	if len(field.Type.Named.Args) != 2 {
		t.Error()
	}
}

func TestTypeParamsEmpty(t *testing.T) {
	_, errs := parseWithErrors(t, `type X<> {}`)
	if len(errs) == 0 {
		t.Error()
	}
}

// TestTypeParamsDuplicate pins that a repeated type-parameter name is an error.
func TestTypeParamsDuplicate(t *testing.T) {
	for _, src := range []string{`type Pair<T, T> { a T  b T }`, `type Triple<T, T, T> { a T }`} {
		_, errs := parseWithErrors(t, src)
		if len(errs) == 0 {
			t.Errorf("expected duplicate-type-parameter error for %q", src)
		}
	}
	if _, errs := parseWithErrors(t, `type OK<K, V> { k K  v V }`); len(errs) != 0 {
		t.Errorf("distinct type params should parse clean, got %v", errs)
	}
}

func TestMethodMissingRequestType(t *testing.T) {
	_, errs := parseWithErrors(t, `service S { get Op { request } }`)
	if len(errs) == 0 {
		t.Error()
	}
}

// TestMethodDuplicateClause pins that a second request or response clause is
// an error.
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

func TestPathBadDash(t *testing.T) {
	_, errs := parseWithErrors(t, `service S { get Op /api- {} }`)
	if len(errs) == 0 {
		t.Error()
	}
}

func TestDecoratorBadName(t *testing.T) {
	_, errs := parseWithErrors(t, `@123
type X {}`)
	if len(errs) == 0 {
		t.Error()
	}
}

func TestTypeParamsExpectFails(t *testing.T) {
	_, errs := parseWithErrors(t, `type X<,> {}`)
	if len(errs) == 0 {
		t.Error()
	}
}

// A type without a body is reported once and leaves the next declaration
// intact.
func TestTypeNoBody(t *testing.T) {
	for src, want := range map[string]string{
		"type X\ntype Y {}":                 "expected {, got type",
		"type X<T>\n@doc(\"y\")\ntype Y {}": "expected {, got @",
		"type X":                            "expected {, got EOF",
	} {
		f, errs := parseWithErrors(t, src)
		if len(errs) != 1 || errs[0] != want {
			t.Errorf("%q: diagnostics %q, want [%q]", src, errs, want)
		}
		if strings.Contains(src, "Y") && len(f.Decls) != 2 {
			t.Errorf("%q: %d declarations, want 2", src, len(f.Decls))
		}
	}
}

func TestQualifiedIdentBadAfterDot(t *testing.T) {
	// `pkg.` with no name after the dot is an error.
	_, errs := parseWithErrors(t, `type X { name pkg. }`)
	if len(errs) == 0 {
		t.Error()
	}
}

func TestQualifiedIdentMissingFirstIdent(t *testing.T) {
	// A field without a type is an error.
	_, errs := parseWithErrors(t, `type X { name }`)
	if len(errs) == 0 {
		t.Error()
	}
}

func TestMethodResponseRejectsBareArray(t *testing.T) {
	_, msgs := parseWithErrors(t, `package design
service S { get H /h { response Order[] } }`)
	if len(msgs) == 0 || !strings.Contains(msgs[0], "bare array") {
		t.Fatalf("expected bare-array diagnostic, got %v", msgs)
	}
}

func TestMethodResponseRejectsOptionalMarker(t *testing.T) {
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

func TestGoldenSample(t *testing.T) {
	path, err := filepath.Abs("testdata/sample.craftgo")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f := mustParse(t, string(src))
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

// TestParseNestedArrayLiteral pins that an array literal may hold arrays.
func TestParseNestedArrayLiteral(t *testing.T) {
	td := firstDecl[*ast.TypeDecl](t, `package design
type X { f string @example([["a", "b"], ["c"]]) }`)
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
