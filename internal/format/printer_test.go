package format

import (
	"bytes"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/parser"
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
	x string
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

// TestFormatStripsEmptyParens: empty `()` is canonicalised away
// — `@positive()` → `@positive`, `@nullable()` → `@nullable`,
// `@deprecated()` → `@deprecated`. Applies to Flag decorators
// (which never take args) AND non-Flag decorators authored with
// no args.
func TestFormatStripsEmptyParens(t *testing.T) {
	src := `package design

type X {
	age int @positive()
	tags string[] @uniqueItems()
	nick string @nullable()
}
`
	formatted, _ := Format("t.craftgo", src)
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

// TestFormatMigratesMinMax: `@min(N)` is rewritten to `@gte(N)`
// and `@max(N)` to `@lte(N)` on format. The semantic registry
// doesn't recognise @min/@max; the formatter normalises them so the
// next lint pass sees the canonical names. Existing @gte/@lte stay
// intact, idempotently.
func TestFormatMigratesMinMax(t *testing.T) {
	src := `package design

type X {
	age int @min(0) @max(150)
	bps int @gte(0) @lte(10000)
}
`
	formatted, _ := Format("t.craftgo", src)
	if !strings.Contains(formatted, "@gte(0)") || !strings.Contains(formatted, "@lte(150)") {
		t.Errorf("@min/@max not migrated to @gte/@lte:\n%s", formatted)
	}
	for _, bad := range []string{"@min(", "@max("} {
		if strings.Contains(formatted, bad) {
			t.Errorf("legacy form %q leaked through format:\n%s", bad, formatted)
		}
	}
	// Idempotent.
	formatted2, _ := Format("t.craftgo", formatted)
	if formatted != formatted2 {
		t.Errorf("not idempotent:\n--first--\n%s\n--second--\n%s", formatted, formatted2)
	}
}

// TestFormatRewritesFormatStringToIdent: `@format("email")` is
// rewritten to `@format(email)` on save. Rule — when a decorator
// argument names a registered identifier (format name, security
// scheme, ...), bare ident is canonical. Strings with non-ident
// characters (hyphens, dots) stay quoted so the rewrite doesn't
// produce un-parseable output.
func TestFormatRewritesFormatStringToIdent(t *testing.T) {
	src := `package design

type X {
	email string @format("email")
	url   string @format("url")
	uid   string @format(uuid)
}
`
	formatted, _ := Format("t.craftgo", src)
	if !strings.Contains(formatted, "@format(email)") || strings.Contains(formatted, `@format("email")`) {
		t.Errorf(`@format("email") not rewritten to @format(email):`+"\n%s", formatted)
	}
	if !strings.Contains(formatted, "@format(url)") || strings.Contains(formatted, `@format("url")`) {
		t.Errorf("@format(\"url\") not rewritten:\n%s", formatted)
	}
	if !strings.Contains(formatted, "@format(uuid)") {
		t.Errorf("bare-ident form should stay as-is:\n%s", formatted)
	}
	// Idempotent.
	formatted2, _ := Format("t.craftgo", formatted)
	if formatted != formatted2 {
		t.Errorf("not idempotent:\n--first--\n%s\n--second--\n%s", formatted, formatted2)
	}
}

// TestClosingNoteInBody verifies that a `// note` sitting on its own
// line right above the closing `}` of a body stays inside the body
// after format - it must not drift to the next decl as a loose
// comment. The parser captures `}.Doc` into a [*ast.FreeComment]
// body member; the loose builder then suppresses the same comment
// to avoid double-emission.
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
	formatted, _ := Format("close.craftgo", src)
	if !strings.Contains(formatted, "\t// todo: add reason field\n}") {
		t.Errorf("// todo should sit inside body before }, got:\n%s", formatted)
	}
	if strings.Contains(formatted, "// todo: add reason field\ntype Other") {
		t.Errorf("// todo must not drift above next decl, got:\n%s", formatted)
	}
	formatted2, _ := Format("close.craftgo", formatted)
	if formatted != formatted2 {
		t.Errorf("not idempotent\n--first--\n%s\n--second--\n%s", formatted, formatted2)
	}
}

// TestImportAndDecoratorComments: leading and trailing comments on
// `import` lines and trailing comments on decorators round-trip
// through Format intact.
func TestImportAndDecoratorComments(t *testing.T) {
	src := `package users

// shared types for cross-package nesting
import "shared"

// internal-only utils
import "util"

import "auth"  // for AuthRequired middleware

@deprecated  // remove in v2 release
@doc("legacy")
service AdminAPI {
	get Health {
		response Pong
	}
}
`
	p := parser.New("imports.craftgo", src)
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("parse: %v", d)
	}
	if got := strings.Join(f.Imports[0].Doc, ""); got != "shared types for cross-package nesting" {
		t.Errorf("Imports[0].Doc = %q", got)
	}
	if got := f.Imports[2].TrailingDoc; got != "for AuthRequired middleware" {
		t.Errorf("Imports[2].TrailingDoc = %q", got)
	}
	sd := f.Decls[0].(*ast.ServiceDecl)
	if got := sd.Decorators[0].TrailingDoc; got != "remove in v2 release" {
		t.Errorf("Decorators[0].TrailingDoc = %q", got)
	}
	formatted, _ := Format("imports.craftgo", src)
	for _, want := range []string{
		"// shared types for cross-package nesting\nimport \"shared\"",
		"// internal-only utils\nimport \"util\"",
		"import \"auth\"  // for AuthRequired middleware",
		"@deprecated  // remove in v2 release",
	} {
		if !strings.Contains(formatted, want) {
			t.Errorf("formatted output missing %q:\n%s", want, formatted)
		}
	}
	formatted2, _ := Format("imports.craftgo", formatted)
	if formatted != formatted2 {
		t.Errorf("not idempotent\n--first--\n%s\n--second--\n%s", formatted, formatted2)
	}
}

// TestCloseBraceTrailing verifies that `// note` on the same line as a
// decl's closing `}` survives the parse → format round-trip via
// TypeDecl.TrailingDoc / EnumDecl.TrailingDoc / ServiceDecl.TrailingDoc /
// Method.TrailingDoc populated by the parser from Token.Trailing.
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
	p := parser.New("trailing.craftgo", src)
	f := p.Parse()
	if d := p.Diagnostics(); len(d) > 0 {
		t.Fatalf("parse: %v", d)
	}
	td := f.Decls[0].(*ast.TypeDecl)
	if got := strings.Join(td.TrailingDoc, ""); got != "end of User" {
		t.Errorf("TypeDecl.TrailingDoc = %q, want %q", got, "end of User")
	}
	ed := f.Decls[1].(*ast.EnumDecl)
	if got := strings.Join(ed.TrailingDoc, ""); got != "closed set" {
		t.Errorf("EnumDecl.TrailingDoc = %q, want %q", got, "closed set")
	}
	sd := f.Decls[2].(*ast.ServiceDecl)
	if got := strings.Join(sd.TrailingDoc, ""); got != "public surface" {
		t.Errorf("ServiceDecl.TrailingDoc = %q, want %q", got, "public surface")
	}
	method := sd.Methods()[0]
	if got := strings.Join(method.TrailingDoc, ""); got != "health-check returns 200 always" {
		t.Errorf("Method.TrailingDoc = %q, want %q", got, "health-check returns 200 always")
	}
	formatted, _ := Format("trailing.craftgo", src)
	for _, want := range []string{
		"}  // end of User",
		"}  // closed set",
		"}  // public surface",
		"}  // health-check returns 200 always",
	} {
		if !strings.Contains(formatted, want) {
			t.Errorf("formatted output missing %q:\n%s", want, formatted)
		}
	}
	// Idempotency: format twice = same.
	formatted2, _ := Format("trailing.craftgo", formatted)
	if formatted != formatted2 {
		t.Errorf("not idempotent\nfirst:\n%s\nsecond:\n%s", formatted, formatted2)
	}
}

// TestFreeCommentRender verifies the printer handles [*ast.FreeComment]
// members inside type, enum, and service bodies. The parser does not
// populate FreeComment members yet (per-comment-line position tracking
// would be needed), so the AST is built directly here to exercise the
// body-iteration code paths.
func TestFreeCommentRender(t *testing.T) {
	file := &ast.File{
		Package: &ast.PackageDecl{Name: "x"},
		Decls: []ast.Decl{
			&ast.TypeDecl{
				Name: "User",
				Body: []ast.TypeMember{
					&ast.Field{Name: "id", Type: &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"string"}}}}},
					&ast.FreeComment{Text: []string{"section: contact info"}},
					&ast.Field{Name: "email", Type: &ast.TypeRef{Named: &ast.NamedTypeRef{Name: &ast.QualifiedIdent{Parts: []string{"string"}}}}},
				},
			},
			&ast.EnumDecl{
				Name: "Status",
				Members: []ast.EnumMember{
					&ast.EnumValue{Name: "Active", Kind: ast.EnumBare},
					&ast.FreeComment{Text: []string{"deprecated values below"}},
					&ast.EnumValue{Name: "Inactive", Kind: ast.EnumBare},
				},
			},
			&ast.ServiceDecl{
				Name: "Svc",
				Members: []ast.ServiceMember{
					&ast.Method{Verb: "get", Name: "Health"},
					&ast.FreeComment{Text: []string{"admin endpoints"}},
					&ast.Method{Verb: "delete", Name: "Purge"},
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := Print(&buf, file); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"// section: contact info",
		"// deprecated values below",
		"// admin endpoints",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}
