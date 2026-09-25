package format

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/parser"
)

// TestFormatRoundTrip pins that formatted output parses cleanly and formats to itself.
func TestFormatRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "minimal",
			src: `package design

type User {
	id string
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
	code string? @default("VALIDATION_FAILED")
	message string
	fields string[]
}
`,
		},
		{
			name: "scalar inline decorators",
			src: `package x

scalar Email string @format("email") @maxLength(254)

scalar Cents int @gte(0) @multipleOf(1)
`,
		},
		{
			// A comment after a decorator must not double across passes.
			name: "decorated trailing comments",
			src: `package x

scalar Tag string @minLength(1) // a tag

enum Color {
	Red = 1 @deprecated // legacy red
	Blue = 2
}

type T {
	name string @length(1, 80) // the display name
}
`,
		},
		{
			name: "middleware declarations",
			src: `package x

middleware AuthRequired

middleware RateLimit
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
		t.Run(c.name, func(t *testing.T) { formatStable(t, c.src) })
	}
}

// TestFormatDecoratedTrailingCommentSingle pins that one format pass writes
// the comment after a scalar's or enum value's decorator once.
func TestFormatDecoratedTrailingCommentSingle(t *testing.T) {
	src := `package x

scalar Tag string @minLength(1) // a tag

enum Color {
	Red = 1 @deprecated // legacy red
}
`
	out := formatStable(t, src)
	if got := strings.Count(out, "// a tag"); got != 1 {
		t.Errorf("scalar trailing comment must appear exactly once, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, "// legacy red"); got != 1 {
		t.Errorf("enum-value trailing comment must appear exactly once, got %d:\n%s", got, out)
	}
}

// TestFormatPreservesComments pins that a comment in each of these positions
// survives formatting.
func TestFormatPreservesComments(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string // substrings that must survive
	}{
		{
			name: "trailing comment on non-last decorator",
			src: `package demo

type T {
  x string @minLength(1) // note
           @maxLength(5)
}
`,
			want: []string{"\tx string @minLength(1) // note\n\t\t@maxLength(5)\n"},
		},
		{
			name: "end-of-file comment block",
			src: `package demo

type User {
	id string
}

// rationale recorded at end of file
`,
			want: []string{"// rationale recorded at end of file"},
		},
		{
			name: "blank-isolated separator comment in body",
			src: `package demo

type User {
	id string

	// section separator comment

	name string
}
`,
			want: []string{"// section separator comment"},
		},
		{
			name: "comment between service decorators",
			src: `package demo

@prefix("/v1")
// only the v1 surface is admin-gated
@tags(admin)
service Users {
	get GetUser /users/{id} {
		request GetUserReq
		response User
	}
}
`,
			want: []string{`@prefix("/v1")`, "@tags(admin)", "// only the v1 surface is admin-gated"},
		},
		{
			name: "comment between method decorators",
			src: `package demo

service Users {
	@doc("get user")
	// 200 because a soft-deleted user still resolves
	@status(200)
	get GetUser /users/{id} {
		request GetUserReq
		response User
	}
}
`,
			want: []string{`@doc("get user")`, "@status(200)", "// 200 because a soft-deleted user still resolves"},
		},
		{
			name: "comment between last decorator and keyword",
			src: `package demo

@minLength(1)
// names are interned downstream
type Name {
	v string
}
`,
			want: []string{"@minLength(1)", "// names are interned downstream", "type Name"},
		},
		{
			name: "leading comment above an enum value",
			src: `package demo

enum Status {
	// active is the default
	Active
	Inactive
}
`,
			want: []string{"// active is the default", "Active", "Inactive"},
		},
		{
			name: "blank-isolated section comment between enum values",
			src: `package demo

enum Status {
	Active

	// terminal states

	Done
	Cancelled
}
`,
			want: []string{"// terminal states", "Done", "Cancelled"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := formatStable(t, c.src)
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("formatted output dropped %q:\n%s", w, out)
				}
			}
		})
	}
}

// TestFormatCommentOnlyFilePreserved pins that a file holding only comments keeps them.
func TestFormatCommentOnlyFilePreserved(t *testing.T) {
	src := "// just a note\n// another line\n"
	out := formatStable(t, src)
	if strings.TrimSpace(out) == "" {
		t.Fatalf("comment-only file was blanked: %q", out)
	}
	for _, w := range []string{"// just a note", "// another line"} {
		if !strings.Contains(out, w) {
			t.Errorf("dropped %q:\n%q", w, out)
		}
	}
}

// TestFormatAddsOptionalToDefault pins that fmt adds `?` to a @default field
// unless it is already optional or a @path field.
func TestFormatAddsOptionalToDefault(t *testing.T) {
	src := `package p

type T {
	flag bool @default(true)
	sort string? @default("asc")
	id string @path @default("x")
}
`
	out := formatStable(t, src)
	if !strings.Contains(out, "flag bool?") {
		t.Errorf("fmt should add ? to a @default field:\n%s", out)
	}
	if !strings.Contains(out, "sort string?") {
		t.Errorf("already-optional @default field must stay optional:\n%s", out)
	}
	if !strings.Contains(out, "id   string  @path") {
		t.Errorf("@path @default field must NOT get ? (path is always present):\n%s", out)
	}
}

// TestFormatPreservesParse pins that formatted output re-parses with its
// package name and file-level decorator intact.
func TestFormatPreservesParse(t *testing.T) {
	src := `@version("1.0")
package design

type Foo {
	x string
}
`
	formatted := formatStable(t, src)
	p := parser.New("t.craftgo", formatted)
	f := p.Parse()
	if len(p.Diagnostics()) > 0 {
		t.Fatalf("formatted output failed to parse: %v\nformatted:\n%s", p.Diagnostics(), formatted)
	}
	if f.Package == nil || f.Package.Name != "design" {
		t.Errorf("expected package design, got %+v", f.Package)
	}
	if !strings.Contains(formatted, `@version("1.0")`) {
		t.Errorf("file decorator missing in output:\n%s", formatted)
	}
}

// TestFormatStripsEmptyParens pins that a decorator written with empty `()`
// prints without them.
func TestFormatStripsEmptyParens(t *testing.T) {
	src := `package design

type X {
	age int @positive()
	tags string[] @uniqueItems()
	nick string @nullable()
}
`
	formatted := formatStable(t, src)
	for _, bad := range []string{"@positive()", "@uniqueItems()", "@nullable()"} {
		if strings.Contains(formatted, bad) {
			t.Errorf("empty parens not stripped: %q remained\nformatted:\n%s", bad, formatted)
		}
	}
	for _, want := range []string{"@positive", "@uniqueItems", "@nullable"} {
		if !strings.Contains(formatted, want) {
			t.Errorf("missing canonical form %q:\n%s", want, formatted)
		}
	}
}

// TestFormatRewritesFormatStringToIdent pins that `@format("email")` prints as
// `@format(email)`.
func TestFormatRewritesFormatStringToIdent(t *testing.T) {
	src := `package design

type X {
	email string @format("email")
	url   string @format("url")
	uid   string @format(uuid)
}
`
	formatted := formatStable(t, src)
	if !strings.Contains(formatted, "@format(email)") || strings.Contains(formatted, `@format("email")`) {
		t.Errorf(`@format("email") not rewritten to @format(email):`+"\n%s", formatted)
	}
	if !strings.Contains(formatted, "@format(url)") || strings.Contains(formatted, `@format("url")`) {
		t.Errorf("@format(\"url\") not rewritten:\n%s", formatted)
	}
	if !strings.Contains(formatted, "@format(uuid)") {
		t.Errorf("bare-ident form should stay as-is:\n%s", formatted)
	}
}

// A `@format` string spelled like a reserved word stays quoted: bare, it would
// read as that word's literal.
func TestFormatKeepsAReservedWordFormatQuoted(t *testing.T) {
	src := "package design\n\ntype X {\n\ta string @format(\"null\")\n\tb string @format(\"true\")\n\tc string @format(\"service\")\n}\n"
	formatExact(t, src, src)
}

// TestClosingNoteInBody pins that a comment above a closing brace stays inside
// the body.
func TestClosingNoteInBody(t *testing.T) {
	src := `package x

error ServiceUnavailable MaintenanceWindow {
	code string? @default("X")
	message string
	// todo: add reason field
}

type Other {
	id string
}
`
	formatted := formatStable(t, src)
	if !strings.Contains(formatted, "\t// todo: add reason field\n}") {
		t.Errorf("// todo should sit inside body before }, got:\n%s", formatted)
	}
	if strings.Contains(formatted, "// todo: add reason field\ntype Other") {
		t.Errorf("// todo must not drift above next decl, got:\n%s", formatted)
	}
}

// TestImportAndDecoratorComments pins that import docs and the trailing
// comments of imports and decorators format to themselves.
func TestImportAndDecoratorComments(t *testing.T) {
	src := `package users

// shared types for cross-package nesting
import "shared"
// internal-only utils
import "util"
import "auth" // for AuthRequired middleware

@deprecated // remove in v2 release
@doc("legacy")
service AdminAPI {
	get Health {
		response Pong
	}
}
`
	formatExact(t, src, src)
}

// TestCloseBraceTrailing pins that the comment after a closing brace formats
// to itself.
func TestCloseBraceTrailing(t *testing.T) {
	src := `package x

type User {
	id string
} // end of User

enum Status {
	Active
	Inactive
} // closed set

service Svc {
	get Health {
		response Pong
	} // health-check returns 200 always
} // public surface
`
	formatExact(t, src, src)
}

// TestFreeCommentRender pins that the comment blocks between the members of a
// type, an enum and a service body format to themselves.
func TestFreeCommentRender(t *testing.T) {
	canonical := `package x

type User {
	id    string

	// section: contact info

	email string
}

enum Status {
	Active

	// deprecated values below

	Inactive
}

service Svc {
	get Health /health {}

	// admin endpoints

	delete Purge /purge {}
}
`
	formatExact(t, canonical, canonical)
}
