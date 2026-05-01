package semantic

import (
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/internal/lexer"
)

// TestDeclCollisionTypeVsErrorErr pins the canonical error case:
// `type FooErr` competes with the `<Name>Err` struct generated for
// `error Foo`. Both end up emitting `type FooErr struct{...}` in
// the same Go package — a hard compile failure that we catch at
// the design layer instead of letting `go build` discover.
func TestDeclCollisionTypeVsErrorErr(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
type FooErr { code string }
error Conflict Foo { reason string }`))
	d := findCode(diags, CodeDeclGoNameCollision)
	if d == nil {
		t.Fatalf("expected %s error, got %v", CodeDeclGoNameCollision, codes(diags))
	}
	if d.Severity != lexer.SeverityError {
		t.Errorf("expected ERROR severity, got %v", d.Severity)
	}
	if !strings.Contains(d.Msg, "FooErr") {
		t.Errorf("message must mention the colliding Go name; got %q", d.Msg)
	}
}

// TestDeclCollisionTypeVsErrorBody pins the body-side collision:
// when an error decl carries a body, codegen also emits a
// `<Name>Body` struct, so `type FooBody` clashes with
// `error Conflict Foo { reason string }`.
func TestDeclCollisionTypeVsErrorBody(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
type FooBody { extra string }
error Conflict Foo { reason string }`))
	d := findCode(diags, CodeDeclGoNameCollision)
	if d == nil {
		t.Fatalf("expected %s error, got %v", CodeDeclGoNameCollision, codes(diags))
	}
	if !strings.Contains(d.Msg, "FooBody") {
		t.Errorf("message must mention the colliding Go name; got %q", d.Msg)
	}
}

// TestDeclCollisionMiddlewareSeparatePackage confirms the namespace
// split: middleware aliases live in svccontext (not the types
// package), so `type AuthMiddleware` and `middleware Auth` do NOT
// collide despite the suffix-mangling. Each lives in a different
// Go file and resolver namespace.
func TestDeclCollisionMiddlewareSeparatePackage(t *testing.T) {
	mustClean(t, `package x
type AuthMiddleware { token string }
middleware Auth`)
}

// TestDeclCollisionErrorWithoutBodySkipsBodyEmit confirms the body
// suffix is only counted when the error actually has a body —
// `error Foo` (bodyless) emits only `FooErr`, NOT `FooBody`, so a
// `type FooBody` next to it does NOT collide.
func TestDeclCollisionErrorWithoutBodySkipsBodyEmit(t *testing.T) {
	mustClean(t, `package x
type FooBody { extra string }
error NotFound Foo`)
}

// TestDeclCollisionEnumScalarSameName pins that two decls producing
// the same verbatim Go name from different DSL kinds also collide
// (enum + type, scalar + type, etc.). The CodeDuplicateDecl pass
// would actually catch this earlier because the DSL names match
// too — this test exercises the second guard via different DSL
// names that still mangle to the same Go name.
//
// `type Foo` and `enum Foo` both emit `type Foo …` so the older
// duplicate-decl rule fires first; we don't need a fresh signal
// from this pass for that case. The scenario the new rule
// uniquely catches is the SUFFIX-mangled one (Err / Body /
// Middleware) where the DSL names diverge.
func TestDeclCollisionEnumScalarSameName(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
type Foo { id string }
enum Foo { Red Blue }`))
	// Either CodeDuplicateDecl (DSL-name level) or
	// CodeDeclGoNameCollision is acceptable here — the contract is
	// just that the user gets an error.
	if findCode(diags, CodeDuplicateDecl) == nil && findCode(diags, CodeDeclGoNameCollision) == nil {
		t.Fatalf("expected duplicate or go-name collision, got %v", codes(diags))
	}
}

// TestDeclCollisionNoFalsePositive confirms a clean project with
// distinct names produces no collision diagnostic.
func TestDeclCollisionNoFalsePositive(t *testing.T) {
	mustClean(t, `package x
type User { id string }
error NotFound UserMissing { reason string }
enum Role { Admin User_ }
scalar UserID string
middleware Auth`)
}
