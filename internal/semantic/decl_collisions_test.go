package semantic

import "testing"

// TestDeclCollisionTypeVsErrorErr pins the canonical case: `type
// FooErr` competes with the `<Name>Err` struct that codegen emits
// for `error Conflict Foo`. Both end up emitting `type FooErr` in
// the same Go package - a hard compile failure caught at the
// design layer before `go build` discovers it.
func TestDeclCollisionTypeVsErrorErr(t *testing.T) {
	d := expectError(t, `package x
type FooErr { code string }
error Conflict Foo { reason string }`, CodeDeclGoNameCollision)
	expectMessage(t, d, "FooErr")
}

// TestDeclCollisionTypeVsErrorBody pins the body-side collision:
// when an error decl carries a body, codegen also emits `<Name>Body`,
// so `type FooBody` clashes with `error Conflict Foo { reason string }`.
func TestDeclCollisionTypeVsErrorBody(t *testing.T) {
	d := expectError(t, `package x
type FooBody { extra string }
error Conflict Foo { reason string }`, CodeDeclGoNameCollision)
	expectMessage(t, d, "FooBody")
}

// TestDeclCollisionMiddlewareSeparatePackage confirms the namespace
// split: middleware aliases live in svccontext (not the types
// package), so `type AuthMiddleware` and `middleware Auth` do NOT
// collide despite the suffix-mangling.
func TestDeclCollisionMiddlewareSeparatePackage(t *testing.T) {
	expectClean(t, `package x
type AuthMiddleware { token string }
middleware Auth`)
}

// TestDeclCollisionErrorWithoutBodySkipsBodyEmit confirms the body
// suffix is only counted when the error actually has a body -
// `error Foo` (bodyless) emits only `FooErr`, so `type FooBody`
// next to it is benign.
func TestDeclCollisionErrorWithoutBodySkipsBodyEmit(t *testing.T) {
	expectClean(t, `package x
type FooBody { extra string }
error NotFound Foo`)
}

// TestDeclCollisionEnumScalarSameName pins that two decls with the
// same DSL name still error - either via [CodeDuplicateDecl] (the
// older shared-namespace check) or [CodeDeclGoNameCollision] (the
// suffix-mangled check). Either signal is acceptable for the user;
// the contract is just "you cannot declare both".
func TestDeclCollisionEnumScalarSameName(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
type Foo { id string }
enum Foo { Red Blue }`))
	if findCode(diags, CodeDuplicateDecl) == nil && findCode(diags, CodeDeclGoNameCollision) == nil {
		t.Fatalf("expected duplicate or go-name collision, got %v", codes(diags))
	}
}

// TestDeclCollisionNoFalsePositive confirms a clean project with
// distinct names produces no collision diagnostic.
func TestDeclCollisionNoFalsePositive(t *testing.T) {
	expectClean(t, `package x
type User { id string }
error NotFound UserMissing { reason string }
enum Role { Admin User_ }
scalar UserID string
middleware Auth`)
}
