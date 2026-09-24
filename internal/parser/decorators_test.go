package parser

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// TestParseEveryDocumentedDecorator pins that every built-in decorator parses
// at the level it belongs to.
func TestParseEveryDocumentedDecorator(t *testing.T) {
	src := `@version("1.0.0")
@doc("file-level doc")
@deprecated
package design

import "shared"

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
    @enum("a", "b")
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
    @deprecated
    @passthrough
    @timeout(5s)
    @maxBodySize(1MB)
    get GetX /x {
    }
    @rawRequest
    @rawResponse
    get GetY /y {
    }
}

middleware Auth
middleware RateLimit
`
	p := New("decorators.craftgo", src)
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("decorators failed to parse: %v", d)
	}
	if len(f.Decorators) < 3 {
		t.Errorf("file decorators count = %d, want >= 3", len(f.Decorators))
	}

	want := []string{
		"version", "doc", "deprecated",
		"example", "requiresOneOf", "mutuallyExclusive",
		"length", "minLength", "maxLength", "pattern", "format", "enum",
		"gt", "gte", "lt", "lte", "range", "positive", "negative", "multipleOf",
		"minItems", "maxItems", "uniqueItems", "maxSize", "mimeTypes",
		"default", "nullable",
		"path", "query", "body", "header", "cookie", "form",
		"prefix", "middlewares", "group", "tags", "security",
		"ignoreSecurity",
		"summary", "operationId", "errors", "status",
		"passthrough", "rawRequest", "rawResponse",
		"timeout", "maxBodySize",
	}
	seen := collectAllDecoratorNames(f)
	for _, w := range want {
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
			for _, a := range d.Args {
				if a.Nested != nil {
					out[a.Nested.Name] = true
				}
			}
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
		}
	}
	return out
}

// TestParseMultiLineDecoratorChain pins that a field's trailing decorators may
// span several lines.
func TestParseMultiLineDecoratorChain(t *testing.T) {
	f := parseSrc(t, `package design
type T {
    name string
        @doc("the name")
        @length(3, 20)
        @pattern("^[a-z]+$")
}`)
	td := f.Decls[0].(*ast.TypeDecl)
	field := td.Body[0].(*ast.Field)
	want := []string{"doc", "length", "pattern"}
	if len(field.Decorators) != len(want) {
		t.Fatalf("expected %d trailing decorators, got %d", len(want), len(field.Decorators))
	}
	for i, w := range want {
		if got := field.Decorators[i].Name; got != w {
			t.Errorf("decorator[%d] = %q, want %q", i, got, w)
		}
	}
}

// TestParseScalarDoesNotStealNextDecorator pins that a scalar takes only the
// decorators on its own line.
func TestParseScalarDoesNotStealNextDecorator(t *testing.T) {
	f := parseSrc(t, `package design
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
	f2 := parseSrc(t, `package design
scalar Email string @format("email")`)
	sd2 := f2.Decls[0].(*ast.ScalarDecl)
	if len(sd2.Decorators) != 1 || sd2.Decorators[0].Name != "format" {
		t.Fatalf("scalar Email should carry @format, got %v", sd2.Decorators)
	}
}
