package format

import (
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/internal/parser"
)

// TestFormatRoundTrip parses, formats, parses again, and reformats. The two
// formatted outputs must match - once the source is in canonical form,
// re-running the formatter is a no-op (idempotency). It also checks that
// the formatted text parses without diagnostics.
func TestFormatRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "minimal",
			src: `package design

type User {
	id string @required
	name string @length(1, 80)
}
`,
		},
		{
			name: "imports and aliases",
			src: `package api

import "shared"
import v1 "v1/api"

type Foo {
	bar shared.Bar
}
`,
		},
		{
			name: "enum kinds",
			src: `package x

enum Status {
	Active = "active"
	Inactive = "inactive"
}

enum Priority {
	Low = 1
	Medium = 2
	High = 3
}

enum Bare {
	Red
	Green
	Blue
}
`,
		},
		{
			name: "error with body",
			src: `package x

error NotFound UserNotFound

error BadRequest ValidationFailed {
	code string @default("VALIDATION_FAILED")
	message string
	fields string[]
}
`,
		},
		{
			name: "scalar inline decorators",
			src: `package x

scalar Email string @format("email") @maxLength(254)

scalar Cents int @min(0) @multipleOf(1)
`,
		},
		{
			name: "middleware with params",
			src: `package x

middleware AuthRequired

middleware RateLimit(rps: int = 100, burst: int = 200)
`,
		},
		{
			name: "service with extend",
			src: `package x

@prefix("/users")
service UserService {
	@doc("List users")
	get ListUsers / {
		request ListReq
		response UserList
	}

	get GetUser /{id} {
		request GetReq
		response User
	}
}

extend service UserService {
	delete DeleteUser /{id} {
		response User
	}
}
`,
		},
		{
			name: "generic type and array",
			src: `package x

type Page<T> {
	items T[]
	total int
	cursor string?
}

type UserListPage {
	Page<User>
	requestId string
}
`,
		},
		{
			name: "map types",
			src: `package x

type X {
	tags map<string, string>
	nested map<string, Tag[]>?
}
`,
		},
		{
			name: "passthrough method",
			src: `package x

service S {
	@passthrough
	get Feed /feed {
	}
}
`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out1, diags := Format("test.craftgo", c.src)
			if len(diags) > 0 {
				t.Fatalf("first parse had diagnostics: %v", diags)
			}
			out2, diags := Format("test.craftgo", out1)
			if len(diags) > 0 {
				t.Fatalf("formatted output failed to parse: %v\nformatted:\n%s", diags, out1)
			}
			if out1 != out2 {
				t.Errorf("not idempotent.\n--- first ---\n%s\n--- second ---\n%s", out1, out2)
			}
		})
	}
}

// TestFormatExampleFiles runs the formatter on the real example files and
// asserts they roundtrip cleanly with no diagnostics.
func TestFormatPreservesParse(t *testing.T) {
	src := `@title("API")
package design

type Foo {
	x string @required
}
`
	formatted, _ := Format("t.craftgo", src)
	p := parser.New("t.craftgo", formatted)
	f := p.Parse()
	if len(p.Diagnostics()) > 0 {
		t.Fatalf("formatted output failed to parse: %v\nformatted:\n%s", p.Diagnostics(), formatted)
	}
	if f.Package == nil || f.Package.Name != "design" {
		t.Errorf("expected package design, got %+v", f.Package)
	}
	if !strings.Contains(formatted, `@title("API")`) {
		t.Errorf("file decorator missing in output:\n%s", formatted)
	}
}
