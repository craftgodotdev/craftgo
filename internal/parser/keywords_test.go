package parser

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/route"
)

func TestParseImportSingleAndAliased(t *testing.T) {
	f := mustParse(t, `package design

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
	f := mustParse(t, src)
	if f.Package == nil || f.Package.Name != "design" {
		t.Errorf("package name lost: %+v", f.Package)
	}
	if len(f.Decorators) != 1 {
		t.Errorf("want 1 file decorator, got %d", len(f.Decorators))
	}
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

func TestParseHyphenatedPathSegments(t *testing.T) {
	f := mustParse(t, `package design
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
			path := route.PathString(s.Methods()[0].Path)
			if !strings.Contains(path, "api-v1") || !strings.Contains(path, "users-list") {
				t.Errorf("hyphenated segments lost: %q", path)
			}
		}
	}
}

// TestParsePathParamReservedKeyword pins that a reserved word in braces, as in
// `/logs/{service}`, is a path parameter.
func TestParsePathParamReservedKeyword(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{name: "service keyword", path: "/logs/{service}", want: "/logs/{service}"},
		{name: "file keyword", path: "/uploads/{file}", want: "/uploads/{file}"},
		{name: "type keyword", path: "/things/{type}", want: "/things/{type}"},
		{name: "verb get", path: "/by/{get}", want: "/by/{get}"},
		{name: "multiple", path: "/{service}/logs/{type}", want: "/{service}/logs/{type}"},
		{name: "mix with literal", path: "/api/{service}/v1", want: "/api/{service}/v1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := `package design
type Req { x string }
type Resp {}
service S {
    get H ` + c.path + ` {
        request  Req
        response Resp
    }
}`
			f := mustParse(t, src)
			if f == nil {
				t.Fatal("expected file")
			}
			var got string
			for _, d := range f.Decls {
				if s, ok := d.(*ast.ServiceDecl); ok && len(s.Methods()) > 0 {
					if s.Methods()[0].Path == nil {
						t.Fatal("path nil")
					}
					got = route.PathString(s.Methods()[0].Path)
				}
			}
			if got != c.want {
				t.Errorf("path = %q, want %q", got, c.want)
			}
		})
	}
}

// TestParsePathDisambiguationKeepsMethodBody pins that the brace after the
// root path `/` opens the method body, not a `{request}` parameter.
func TestParsePathDisambiguationKeepsMethodBody(t *testing.T) {
	src := `package design
type Req { id string }
type Resp {}
service S {
    get H / {
        request  Req
        response Resp
    }
}`
	f := mustParse(t, src)
	if f == nil {
		t.Fatal("expected file")
	}
	for _, d := range f.Decls {
		s, ok := d.(*ast.ServiceDecl)
		if !ok || len(s.Methods()) == 0 {
			continue
		}
		m := s.Methods()[0]
		if m.Path == nil || len(m.Path.Segments) != 1 {
			t.Fatalf("expected path with 1 segment, got %v", m.Path)
		}
		if m.Path.Segments[0].Param {
			t.Errorf("path-param leaked: method body brace misread as `{request}`")
		}
		if m.Request == nil || m.Request.Name == nil || m.Request.Name.String() != "Req" {
			t.Errorf("expected request Req, got %v", m.Request)
		}
	}
}
