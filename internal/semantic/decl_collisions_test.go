package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
	"github.com/craftgodotdev/craftgo/internal/parser"
)

// A type named FooErr collides with the struct generated for `error Conflict Foo`.
func TestDeclCollisionTypeVsErrorErr(t *testing.T) {
	d := expectError(t, `package x
type FooErr { code string }
error Conflict Foo { reason string }`, CodeDeclGoNameCollision)
	expectMessage(t, d, "FooErr")
}

// A type named FooBody collides with the body struct of an error Foo that has a body.
func TestDeclCollisionTypeVsErrorBody(t *testing.T) {
	d := expectError(t, `package x
type FooBody { extra string }
error Conflict Foo { reason string }`, CodeDeclGoNameCollision)
	expectMessage(t, d, "FooBody")
}

// A type named AuthMiddleware does not collide with `middleware Auth`.
func TestDeclCollisionMiddlewareSeparatePackage(t *testing.T) {
	expectClean(t, `package x
type AuthMiddleware { token string }
middleware Auth`)
}

// A type named FooBody beside a body-less error Foo is accepted.
func TestDeclCollisionErrorWithoutBodySkipsBodyEmit(t *testing.T) {
	expectClean(t, `package x
type FooBody { extra string }
error NotFound Foo`)
}

// A type and an enum of one name are rejected.
func TestDeclCollisionEnumScalarSameName(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
type Foo { id string }
enum Foo { Red Blue }`))
	if findCode(diags, CodeDuplicateDecl) == nil && findCode(diags, CodeDeclGoNameCollision) == nil {
		t.Fatalf("expected duplicate or go-name collision, got %v", codes(diags))
	}
}

// Every Go identifier an error, an enum value or an event emits collides
// with a declaration of that name.
func TestDeclCollisionEmittedNames(t *testing.T) {
	for _, c := range []struct{ label, src, emitted, role string }{
		{"error code constant", "error NotFound UserGone\ntype ErrCodeUserGone { a string }", "ErrCodeUserGone", "its code constant"},
		{"error constructor", "error Conflict Taken\ntype NewTakenErr { a string }", "NewTakenErr", "its constructor"},
		{"error type named like its suffix", "error Conflict DupError\ntype DupError { a string }", "DupError", "its type"},
		{"enum value constant", "enum Status { Active = \"a\" }\ntype StatusActive { a string }", "StatusActive", "its constant"},
		{"deduplicated enum value constant", "enum Status { Active  active }\nscalar StatusActive_2 string", "StatusActive_2", "its constant"},
		{"event contract constant", "type P { a string }\nevent Foo { payload P }\nevent FooContract { payload P }", "FooContract", "its contract constant"},
	} {
		t.Run(c.label, func(t *testing.T) {
			d := expectError(t, "package x\n"+c.src, CodeDeclGoNameCollision)
			expectMessage(t, d, `"`+c.emitted+`"`, c.role)
		})
	}
}

// An error whose body holds only a comment emits no body struct.
func TestDeclCollisionCommentOnlyErrorBody(t *testing.T) {
	expectClean(t, `package x
error Conflict Noted {
    // a note
}
type NotedBody { d string }`)
}

// The first emitter across files is the one declared first by file, then offset.
func TestDeclCollisionFirstAcrossFiles(t *testing.T) {
	a := parser.New("a.craftgo", "package x\n\n\n\ntype FooErr { a string }").Parse()
	b := parser.New("b.craftgo", "package x\nerror Conflict Foo").Parse()
	_, diags := AnalyzeProject([]*ast.File{b, a}, Options{})
	d := findCode(diags, CodeDeclGoNameCollision)
	if d == nil || d.Pos.Filename != "b.craftgo" || len(d.Related) != 1 || d.Related[0].Pos.Filename != "a.craftgo" {
		t.Fatalf("want the error in b.craftgo reported against the type in a.craftgo, got %v", diags)
	}
}

// Distinct declaration names produce no collision.
func TestDeclCollisionNoFalsePositive(t *testing.T) {
	expectClean(t, `package x
type User { id string }
error NotFound UserMissing { reason string }
enum Role { Admin User_ }
scalar UserID string
middleware Auth`)
}
