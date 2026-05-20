package parser

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// parseSrc is a tiny helper that parses src and fails fast on diagnostics.
func parseSrc(t *testing.T, src string) *ast.File {
	t.Helper()
	p := New("k.craftgo", src)
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("parse errors: %v", d)
	}
	return f
}

func TestParseImportSingleAndAliased(t *testing.T) {
	f := parseSrc(t, `package design

import "shared/types"
import v1 "v1/api"
import _ "side-effect"

type X { id string }
`)
	if len(f.Imports) != 3 {
		t.Fatalf("want 3 imports, got %d", len(f.Imports))
	}
	if f.Imports[0].Path != "shared/types" || f.Imports[0].Alias != "" {
		t.Errorf("unexpected first import: %+v", f.Imports[0])
	}
	if f.Imports[1].Alias != "v1" || f.Imports[1].Path != "v1/api" {
		t.Errorf("aliased import wrong: %+v", f.Imports[1])
	}
	if f.Imports[2].Alias != "_" {
		t.Errorf("blank-import alias wrong: %+v", f.Imports[2])
	}
}

func TestParseAllKeywordsRoundTrip(t *testing.T) {
	// Every reserved keyword + decorator should parse without error.
	src := `@version("1")
package design

import "shared"

type Page<T> {
    items  T[]
    total  int
}

type User {
    Page<User>
    id    string
    name  string?
    tags  string[]
    meta  map<string, string>
}

scalar Email string @format("email")

enum Role {
    Admin = "admin"
    User  = "user"
}

enum Priority {
    Low  = 1
    High = 3
}

enum Bare {
    A
    B
    C
}

error NotFound UserNotFound
error BadRequest Validation {
    fields  string[]
}

middleware AuthRequired
middleware RateLimit

@prefix("/api")
@middlewares(AuthRequired)
service S {
    @doc("get user")
    @errors(UserNotFound)
    @middlewares(RateLimit)
    @timeout(5s)
    @maxBodySize(1MB)
    get GetUser /users/{id} {
        request   User
        response  User
    }

    @passthrough
    get StreamUsers /feed {
    }

}

extend service S {
    delete Delete /users/{id} {
        response  User
    }
}
`
	f := parseSrc(t, src)
	if f.Package == nil || f.Package.Name != "design" {
		t.Errorf("package name lost: %+v", f.Package)
	}
	if len(f.Decorators) != 1 {
		t.Errorf("want 1 file decorator, got %d", len(f.Decorators))
	}
	// Find the service.
	var svcCount int
	var allMethods int
	for _, d := range f.Decls {
		if s, ok := d.(*ast.ServiceDecl); ok {
			svcCount++
			allMethods += len(s.Methods())
		}
	}
	if svcCount != 2 {
		t.Errorf("expected 2 service decls (1 + 1 extend), got %d", svcCount)
	}
	if allMethods < 3 {
		t.Errorf("expected at least 3 methods total, got %d", allMethods)
	}
}

func TestParseDecoratorsOnEveryLevel(t *testing.T) {
	src := `@version("1")
package design

@doc("type")
@deprecated
type T {
   
    @length(1, 100)
    @pattern("^[a-z]+$")
    @format("email")
    @example("alice@example.com")
    name  string

    @gte(0)
    @lte(150)
    age  int?

    @default("default")
    secret  string
}

@doc("enum E")
enum E {
    A
    B
}

@deprecated
service S {
    @summary("get")
    @operationId("getX")
    @consumes("application/json")
    @produces("application/json")
    @tags(api, v1)
    @ignoreSecurity
    get Op /ops {
        response  T
    }
}
`
	f := parseSrc(t, src)
	if f == nil {
		t.Fatal("expected file")
	}
}

func TestParseHyphenatedPathSegments(t *testing.T) {
	f := parseSrc(t, `package design
type Req { id string }
type Resp {}
service S {
    get H /api-v1/users-list/{id} {
        request   Req
        response  Resp
    }
}`)
	for _, d := range f.Decls {
		if s, ok := d.(*ast.ServiceDecl); ok && len(s.Methods()) > 0 {
			if s.Methods()[0].Path == nil {
				t.Fatal("path nil")
			}
			path := pathStr(s.Methods()[0].Path)
			if !strings.Contains(path, "api-v1") || !strings.Contains(path, "users-list") {
				t.Errorf("hyphenated segments lost: %q", path)
			}
		}
	}
}

// pathStr renders a Path back to a string for assertion convenience.
func pathStr(p *ast.Path) string {
	var sb strings.Builder
	for _, s := range p.Segments {
		sb.WriteByte('/')
		if s.Param {
			sb.WriteByte('{')
			sb.WriteString(s.Literal)
			sb.WriteByte('}')
		} else {
			sb.WriteString(s.Literal)
		}
	}
	return sb.String()
}
