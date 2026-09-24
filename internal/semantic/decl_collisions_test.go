package semantic

import "testing"

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

// Distinct declaration names produce no collision.
func TestDeclCollisionNoFalsePositive(t *testing.T) {
	expectClean(t, `package x
type User { id string }
error NotFound UserMissing { reason string }
enum Role { Admin User_ }
scalar UserID string
middleware Auth`)
}
