package parser

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// TestParseEveryDocumentedDecorator pins the parser's coverage of every
// decorator listed in the README's "Decorators by level" table. The DSL
// below exercises each decorator at least once; if any of them ever
// stops parsing, this test fails loudly so the regression is obvious.
func TestParseEveryDocumentedDecorator(t *testing.T) {
	src := `@title("API")
@version("1.0.0")
@doc("file-level doc")
@deprecated
package design

import "shared"

@doc("type-level doc")
@title("Type")
@example("simple")
@examples(["a", "b"])
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
    @examples(["a", "b"])
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
@externalDocs("https://docs.example.com")
@security(noauth)
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
    @security(noauth)
    @example("e")
    @examples(["a"])
    @consumes("application/json")
    @produces("application/json")
    @deprecated
    @externalDocs("https://docs.example.com")
    @passthrough
    @accepts("application/json")
    @timeout(5s)
    @maxBodySize(1MB)
    @responseDoc("ok")
    @responseExample("e")
    @responseHeaders("h")
    get GetX /x {
    }
}

middleware Auth
middleware RateLimit(rps: int = 100)
`
	p := New("decorators.craftgo", src)
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("decorators failed to parse: %v", d)
	}
	// Spot-check that high-level decorators landed where expected.
	if len(f.Decorators) < 4 {
		t.Errorf("file decorators count = %d, want >= 4", len(f.Decorators))
	}

	want := []string{
		"title", "version", "doc", "deprecated",
		"example", "examples", "requiresOneOf", "mutuallyExclusive",
		"length", "minLength", "maxLength", "pattern", "format", "enum",
		"gt", "gte", "lt", "lte", "range", "positive", "negative", "multipleOf",
		"minItems", "maxItems", "uniqueItems", "maxSize", "mimeTypes",
		"default", "nullable",
		"path", "query", "body", "header", "cookie", "form",
		"prefix", "middlewares", "group", "tags", "externalDocs", "security",
		"summary", "operationId", "errors", "status",
		"consumes", "produces", "passthrough", "accepts",
		"timeout", "maxBodySize",
		"responseDoc", "responseExample", "responseHeaders",
	}
	seen := collectAllDecoratorNames(f)
	for _, w := range want {
		if !seen[w] {
			t.Errorf("expected decorator %q to be parsed somewhere", w)
		}
	}
}

// collectAllDecoratorNames walks every node carrying a Decorators slice
// and returns the set of decorator names seen. The walker is hand-coded
// instead of using a visitor because the AST is small and stable.
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

// TestParseMultiLineDecoratorChain exercises the readability convention
// where a long decorator chain is split across many lines. The parser
// must accept arbitrary newlines between decorators on the same field.
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
	if len(field.Decorators) != 3 {
		t.Errorf("expected 3 trailing decorators, got %d", len(field.Decorators))
	}
	got := []string{}
	for _, d := range field.Decorators {
		got = append(got, d.Name)
	}
	if strings.Join(got, ",") != "doc,length,pattern" {
		t.Errorf("unexpected order: %v", got)
	}
}
