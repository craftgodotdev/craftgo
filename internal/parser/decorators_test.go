package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/semantic"
)

// TestParseEveryDocumentedDecorator pins that every built-in decorator parses
// at the level it belongs to.
func TestParseEveryDocumentedDecorator(t *testing.T) {
	src := `@version("1.0.0")
@doc("file-level doc")
@deprecated
package design

@doc("type-level doc")
@deprecated
@requiresOneOf("a", "b")
@mutuallyExclusive("a", "b")
type T {
   
    @length(1, 100)
    @minLength(1)
    @maxLength(100)
    @pattern("^[a-z]+$")
    @format("email")
    @json("full_name")
    @example("alice")
    @doc("field doc")
    @default("d")
    @deprecated
    name  string

    @gt(-1)
    @gte(0)
    @lt(200)
    @lte(150)
    @range(0, 150)
    @positive
    @negative
    @multipleOf(2)
    age  int

    @minItems(1)
    @maxItems(10)
    @uniqueItems
    tags  string[]

    @maxSize(5MB)
    @mimeTypes("image/png", "image/jpeg")
    @form
    upload  file

    @path
    pathField  string

    @query
    queryField  string

    @header
    headerField  string

    @cookie
    @sensitive
    cookieField  string

    @body
    @nullable
    bodyField  string?
}

@doc("enum doc")
enum E {
    A
    B
}

@doc("scalar")
scalar Email string @format("email")

@doc("err doc")
@example({code: "X"})
error NotFound MyErr {
    fields  string[]
}

@prefix("/api")
@middlewares(Auth)
@group("admin")
@tags("v1")
@security(Bearer)
@deprecated
@doc("svc doc")
service S {
    @doc("method doc")
    @summary("get")
    @operationId("getX")
    @tags("v1")
    @middlewares(RateLimit)
    @errors(MyErr)
    @status(200)
    @ignoreSecurity
    @ignoreMiddleware
    @ignoreTags
    @deprecated
    @passthrough
    @timeout(5s)
    @maxBodySize(1MB)
    get GetX /x {
    }
    @rawRequest
    @rawResponse
    @hidden
    get GetY /y {
    }
}

middleware Auth
middleware RateLimit

@contract("design.placed.v1")
event Placed {
    payload T
}
`
	f := mustParse(t, src)
	if len(f.Decorators) < 3 {
		t.Errorf("file decorators count = %d, want >= 3", len(f.Decorators))
	}

	seen := collectAllDecoratorNames(f)
	for _, w := range semantic.Names() {
		if !seen[w] {
			t.Errorf("expected decorator %q to be parsed somewhere", w)
		}
	}
}

// collectAllDecoratorNames returns the names of the decorators in f.
func collectAllDecoratorNames(f *ast.File) map[string]bool {
	out := map[string]bool{}
	add := func(ds []*ast.Decorator) {
		for _, d := range ds {
			out[d.Name] = true
		}
	}
	add(f.Decorators)
	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.TypeDecl:
			add(v.Decorators)
			for _, m := range v.Body {
				if fd, ok := m.(*ast.Field); ok {
					add(fd.Decorators)
				}
			}
		case *ast.EnumDecl:
			add(v.Decorators)
			for _, val := range v.EnumValues() {
				add(val.Decorators)
			}
		case *ast.ErrorDecl:
			add(v.Decorators)
			for _, m := range v.Body {
				if fd, ok := m.(*ast.Field); ok {
					add(fd.Decorators)
				}
			}
		case *ast.ScalarDecl:
			add(v.Decorators)
		case *ast.MiddlewareDecl:
			add(v.Decorators)
		case *ast.ServiceDecl:
			add(v.Decorators)
			for _, mtd := range v.Methods() {
				add(mtd.Decorators)
			}
		case *ast.EventDecl:
			add(v.Decorators)
		}
	}
	return out
}

// A decorator on a line of its own decorates the field below it, one on a
// field's line after its type that field, and a chain whose arguments run
// onto a later line still ends on the field's line.
func TestDecoratorsOnTheirOwnLineDecorateTheFieldBelow(t *testing.T) {
	f := mustParse(t, `package design
type T {
    id string

    @format(email)
    @maxLength(254)
    email string @minLength(3)
    name string @length(3,
        20) @pattern("^[a-z]+$")
        @doc("the nick")
    nick string
    @deprecated old string @doc("o")
}`)
	want := map[string]string{
		"id":    "",
		"email": "format maxLength minLength",
		"name":  "length pattern",
		"nick":  "doc",
		"old":   "deprecated doc",
	}
	for _, fl := range ast.Fields(f.Decls[0].(*ast.TypeDecl).Body) {
		if got := decoratorNames(fl.Decorators); got != want[fl.Name] {
			t.Errorf("field %s decorators = %q, want %q", fl.Name, got, want[fl.Name])
		}
	}
}

// Decorators on lines of their own that close a body decorate nothing.
func TestDecoratorsWithNoMemberBelowAreReported(t *testing.T) {
	for src, want := range map[string]string{
		"type T {\n\tid string\n\t@doc(\"x\")\n}\n":             "3:2: decorator @doc has no field below it",
		"type T {\n\tid string\n\t@doc(\"x\") @deprecated\n}\n": "3:2: decorator @doc has no field below it",
		"enum E {\n\tA\n\t@doc(\"x\")\n}\n":                     "3:2: decorator @doc has no enum value below it",
		"error NotFound Gone {\n\tid string\n\t@header\n}\n":    "3:2: decorator @header has no field below it",
	} {
		p := New("t.craftgo", "package p\n\n"+src)
		p.Parse()
		var got []string
		for _, d := range p.Diagnostics() {
			got = append(got, fmt.Sprintf("%d:%d: %s", d.Pos.Line-2, d.Pos.Column, d.Msg))
		}
		if len(got) != 1 || !strings.HasPrefix(got[0], want) {
			t.Errorf("%q: diagnostics = %q, want one starting %q", src, got, want)
		}
	}
}

// An `@` whose name is not on its line is reported, and the member below it
// still parses.
func TestDecoratorNameOnTheLineOfItsAt(t *testing.T) {
	p := New("t.craftgo", "package p\n\ntype T {\n\tid string\n\t@\n\temail string\n}\n")
	f := p.Parse()
	if d := p.Diagnostics(); len(d) != 1 || d[0].Msg != "expected decorator name after @ on its line" {
		t.Fatalf("diagnostics = %v", d)
	}
	if body := ast.Fields(f.Decls[0].(*ast.TypeDecl).Body); len(body) != 2 || body[1].Name != "email" {
		t.Errorf("fields = %v, want id and email", body)
	}
}

// TestParseScalarDoesNotStealNextDecorator pins that a scalar takes only the
// decorators on its own line.
func TestParseScalarDoesNotStealNextDecorator(t *testing.T) {
	f := mustParse(t, `package design
scalar Email string
@requiresOneOf(primary, fallback)
type Pair { primary string?  fallback string? }`)
	sd := f.Decls[0].(*ast.ScalarDecl)
	if len(sd.Decorators) != 0 {
		t.Fatalf("scalar Email should carry no decorators, got %d (%v)", len(sd.Decorators), sd.Decorators)
	}
	td := f.Decls[1].(*ast.TypeDecl)
	if len(td.Decorators) != 1 || td.Decorators[0].Name != "requiresOneOf" {
		t.Fatalf("type Pair should carry @requiresOneOf, got %v", td.Decorators)
	}
	// A same-line trailing decorator still belongs to the scalar.
	f2 := mustParse(t, `package design
scalar Email string @format("email")`)
	sd2 := f2.Decls[0].(*ast.ScalarDecl)
	if len(sd2.Decorators) != 1 || sd2.Decorators[0].Name != "format" {
		t.Fatalf("scalar Email should carry @format, got %v", sd2.Decorators)
	}
}
