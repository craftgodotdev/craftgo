package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// ---------- @errors ----------

func TestErrorsRefResolved(t *testing.T) {
	mustClean(t, `error NotFound UserNotFound
service S {
	@errors(UserNotFound)
	get GetUser /u {}
}`)
}

func TestErrorsRefUnknown(t *testing.T) {
	d := expectDiag(t, `service S {
	@errors(MysteryError)
	get GetUser /u {}
}`, CodeDecoratorRef)
	expectMessage(t, d, "MysteryError")
}

func TestErrorsRefArrayShortcut(t *testing.T) {
	d := expectDiag(t, `error NotFound UserNotFound
service S {
	@errors([UserNotFound, MysteryError])
	get GetUser /u {}
}`, CodeDecoratorRef)
	expectMessage(t, d, "MysteryError")
}

// ---------- @middlewares ----------

func TestMiddlewareRefResolved(t *testing.T) {
	mustClean(t, `middleware Auth
@middlewares(Auth)
service S {}`)
}

func TestMiddlewareRefUnknown(t *testing.T) {
	expectDiag(t, `@middlewares(Auth)
service S {}`, CodeDecoratorRef)
}

func TestMiddlewareRefOnMethod(t *testing.T) {
	expectDiag(t, `service S {
	@middlewares(Bogus)
	get GetUser /u {}
}`, CodeDecoratorRef)
}

// ---------- @requiresOneOf / @mutuallyExclusive ----------

func TestRequiresOneOfFieldExists(t *testing.T) {
	mustClean(t, `@requiresOneOf(email, phone)
type Contact { email string?  phone string? }`)
}

func TestRequiresOneOfFieldMissing(t *testing.T) {
	d := expectDiag(t, `@requiresOneOf(email, fax)
type Contact { email string? }`, CodeDecoratorRef)
	expectMessage(t, d, "fax")
}

func TestMutuallyExclusiveFieldMissing(t *testing.T) {
	expectDiag(t, `@mutuallyExclusive(["a", "missing"])
type T { a string? }`, CodeDecoratorRef)
}

func TestCrossFieldRequiresOptionalField(t *testing.T) {
	// A plain (non-optional, non-nullable) field has no unambiguous
	// present/absent state for the cross-field check: OpenAPI uses
	// key-presence, the runtime uses zero-value emptiness.
	d := expectDiag(t, `@requiresOneOf(email, phone)
type Contact { email string?  phone string }`, CodeCrossFieldNotOptional)
	expectMessage(t, d, "phone")
}

func TestCrossFieldNullableFieldAccepted(t *testing.T) {
	// `@nullable` is pointer-backed too, so it has a well-defined presence
	// and satisfies the cross-field requirement alongside `?`.
	mustClean(t, `@mutuallyExclusive(a, b)
type T { a string @nullable  b string? }`)
}

func TestCrossFieldRejectsWireBoundMember(t *testing.T) {
	// A @query member is excluded from the JSON body, so a body-level group
	// referencing it would advertise a property the body never carries.
	d := expectDiag(t, `@requiresOneOf(q1, q2)
type Req { q1 string? @query  q2 string? @query }`, CodeCrossFieldNotOptional)
	expectMessage(t, d, "q1")
}

func TestCrossFieldRejectsDefaultMember(t *testing.T) {
	// A @default member is always present at runtime (pre-filled), so the
	// group is a no-op the OpenAPI contradicts.
	d := expectDiag(t, `@requiresOneOf(a, b)
type Req { a string? @default("x")  b string? }`, CodeCrossFieldNotOptional)
	expectMessage(t, d, "a")
}

func TestCrossFieldRejectsNilableNonPointerMember(t *testing.T) {
	// A slice / map member's runtime presence is emptiness (`len() > 0`), and a
	// raw `bytes` / `any` member is always treated as present - neither matches
	// the group's OpenAPI present-and-non-null, which counts an empty `[]` as
	// present and a null as absent. All four are nilable-but-not-pointer in Go,
	// so they are rejected.
	d := expectDiag(t, `@requiresOneOf(tags, name)
type Req { tags string[]?  name string? }`, CodeCrossFieldNotOptional)
	expectMessage(t, d, "tags")

	d = expectDiag(t, `@mutuallyExclusive(meta, name)
type Req { meta map<string, string>?  name string? }`, CodeCrossFieldNotOptional)
	expectMessage(t, d, "meta")

	d = expectDiag(t, `@requiresOneOf(blob, name)
type Req { blob bytes?  name string? }`, CodeCrossFieldNotOptional)
	expectMessage(t, d, "blob")

	d = expectDiag(t, `@mutuallyExclusive(payload, name)
type Req { payload any?  name string? }`, CodeCrossFieldNotOptional)
	expectMessage(t, d, "payload")
}

func TestCrossFieldRejectsSensitiveMember(t *testing.T) {
	// A @sensitive member is server-only (json:"-"), so it can't ride a
	// body-level cross-field group - the OpenAPI would name a property the
	// public schema never carries.
	d := expectDiag(t, `@mutuallyExclusive(secret, b)
type Req { secret string? @sensitive  b string? }`, CodeCrossFieldNotOptional)
	expectMessage(t, d, "secret")
}

// ---------- @security with Options ----------

func TestSecurityRefSkippedWithoutOptions(t *testing.T) {
	// No SecuritySchemes set on Options → check is skipped.
	mustClean(t, `@security(unknown)
service S {}`)
}

// expectRefWithOptions runs the analyzer with explicit Options and
// asserts a CodeDecoratorRef diag fires; returns the diagnostic so
// callers can chain message assertions. Wraps the AnalyzeWith path
// the security-scheme tests need without rebuilding the boilerplate.
func expectRefWithOptions(t *testing.T, src string, opts Options) *Diagnostic {
	t.Helper()
	_, diags := AnalyzeWith(parseFiles(t, src), opts)
	d := findCode(diags, CodeDecoratorRef)
	if d == nil {
		t.Fatalf("expected %s, got %v", CodeDecoratorRef, codes(diags))
	}
	return d
}

// expectNoRefWithOptions is the negative twin - checks that no
// CodeDecoratorRef fires under the supplied Options, so positive
// security/scheme tests stay similarly compact.
func expectNoRefWithOptions(t *testing.T, src string, opts Options) {
	t.Helper()
	_, diags := AnalyzeWith(parseFiles(t, src), opts)
	if d := findCode(diags, CodeDecoratorRef); d != nil {
		t.Fatalf("did not expect %s, got %q", CodeDecoratorRef, d.Msg)
	}
}

func TestSecurityRefValidatedWithOptions(t *testing.T) {
	d := expectRefWithOptions(t, `@security(unknown)
service S {}`, Options{SecuritySchemes: []string{"bearerAuth"}})
	expectMessage(t, d, "bearerAuth")
}

func TestSecurityRefAcceptsKnown(t *testing.T) {
	expectNoRefWithOptions(t, `@security(bearerAuth)
service S {}`, Options{SecuritySchemes: []string{"bearerAuth", "apiKey"}})
}

// TestSecurityRefAcceptsIgnoreSecurity covers the public-endpoint
// pattern: rather than threading a sentinel scheme name through
// `@security(...)`, the method opts out via `@ignoreSecurity`. The
// ref-pass should never see `@ignoreSecurity` as a scheme reference,
// so the SecuritySchemes manifest list is irrelevant for that method.
func TestSecurityRefAcceptsIgnoreSecurity(t *testing.T) {
	expectNoRefWithOptions(t, `service S {
	@ignoreSecurity
	get Public /p {}
}`, Options{SecuritySchemes: []string{"bearerAuth"}})
}

func TestSecurityRefSkipsNonIdentArg(t *testing.T) {
	// Args pass already flagged the type - refs pass should silently skip.
	expectNoRefWithOptions(t, `@security(123)
service S {}`, Options{SecuritySchemes: []string{"bearerAuth"}})
}

func TestSecurityRefSkipsZeroArgs(t *testing.T) {
	// `@security` with no args - args pass diags arity. Refs pass silently
	// skips because there's no name to resolve.
	expectNoRefWithOptions(t, `@security
service S {}`, Options{SecuritySchemes: []string{"bearerAuth"}})
}

// ---------- collect / structure ----------

func TestExtendServiceMiddlewareIsChecked(t *testing.T) {
	// `extend service` body methods are walked too.
	expectDiag(t, `service S {}
extend service S {
	@middlewares(Bogus)
	get Op /x {}
}`, CodeDecoratorRef)
}

func TestRefsNilDecoratorTolerated(t *testing.T) {
	// Defensive guard - parser doesn't emit nil entries today.
	a := &analyzer{pkg: &Package{
		Errors:      map[string]*ast.ErrorDecl{},
		Middlewares: map[string]*ast.MiddlewareDecl{},
	}}
	// Empty body decorators slice with a nil entry.
	a.checkFieldGroupRefs("X", []*ast.Decorator{nil}, nil)
	a.checkServiceLevelRefs([]*ast.Decorator{nil})
	// Build a synthetic method with a nil decorator.
	a.checkMethodLevelRefs(&ast.Method{Decorators: []*ast.Decorator{nil}})
	if len(a.diags) != 0 {
		t.Errorf("nil decorator entries should not diag, got %v", a.diags)
	}
}

// A cross-field group (@requiresOneOf) may reference a field promoted by a
// CROSS-PACKAGE mixin - the per-package pass can't expand it, but codegen
// resolves it via the project resolver, so it must not false-reject.
func TestCrossFieldOverCrossPkgMixinClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type Contactable { email string?  phone string? }`,
		"api.craftgo": `package design
import "shared"
@requiresOneOf(email, phone)
type Contact { shared.Contactable  note string? }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeDecoratorRef); d != nil {
		t.Errorf("cross-field over cross-pkg mixin must not false-reject; got: %s", d.Msg)
	}
}

// A cross-field group naming a member NO field provides must be rejected
// even when the type embeds a cross-package mixin. The per-package pass
// can't expand the foreign mixin so it defers; the project pass resolves
// the full field set and catches the typo. Without the project re-check
// the typo reaches codegen, which substitutes a literal `false` - a
// validator that silently never fires.
func TestCrossFieldTypoOverCrossPkgMixinRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type Contactable { email string?  phone string? }`,
		"api.craftgo": `package design
import "shared"
@requiresOneOf(email, zzz)
type Contact { shared.Contactable  note string? }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorRef) == nil {
		t.Fatalf("expected typo rejection over cross-pkg mixin; got %v", codes(diags))
	}
}

// The project re-check resolves NESTED cross-package mixins too: a member
// promoted two mixin levels deep is a real field (no false-reject), while
// a typo alongside it is still caught.
func TestCrossFieldTypoOverNestedCrossPkgMixinRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type Inner { email string?  phone string? }
type Outer { Inner  label string? }`,
		"api.craftgo": `package design
import "shared"
@requiresOneOf(email, nope)
type Contact { shared.Outer }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorRef) == nil {
		t.Fatalf("expected typo rejection over nested cross-pkg mixin; got %v", codes(diags))
	}
}

// A deeply-promoted member (two cross-package mixin levels) is a genuine
// field and must NOT be false-rejected - the control for the typo test.
func TestCrossFieldOverNestedCrossPkgMixinClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
type Inner { email string?  phone string? }
type Outer { Inner  label string? }`,
		"api.craftgo": `package design
import "shared"
@requiresOneOf(email, phone)
type Contact { shared.Outer }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeDecoratorRef); d != nil {
		t.Errorf("nested cross-pkg promoted member must not false-reject; got: %s", d.Msg)
	}
}

// The project re-check must re-apply the per-field quality rules to a
// member promoted from a cross-package mixin - not only check the name
// exists. A PLAIN (non-optional) promoted member has no clean present/
// absent state, so a cross-field group referencing it is rejected exactly
// as a local plain member is. (Per-package can't see the foreign field, so
// without the re-check the rule was silently skipped.)
func TestCrossFieldPlainMemberOverCrossPkgMixinRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"base/b.craftgo": `package base
type BaseMix { gamma string }`,
		"api.craftgo": `package design
import "base"
@requiresOneOf(alpha, gamma)
type Host { base.BaseMix  alpha string? }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeCrossFieldNotOptional) == nil {
		t.Fatalf("expected plain cross-pkg-promoted member rejection; got %v", codes(diags))
	}
}

// A @default member promoted from a cross-package mixin is rejected too
// (a defaulted field is always present, making the group a no-op the
// OpenAPI contradicts) - the same rule the local case enforces.
func TestCrossFieldDefaultMemberOverCrossPkgMixinRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"base/b.craftgo": `package base
type BaseMix { gamma string? @default("x") }`,
		"api.craftgo": `package design
import "base"
@requiresOneOf(alpha, gamma)
type Host { base.BaseMix  alpha string? }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeCrossFieldNotOptional) == nil {
		t.Fatalf("expected @default cross-pkg-promoted member rejection; got %v", codes(diags))
	}
}

// A clean optional member promoted from a cross-package mixin alongside a
// clean local one must NOT double-report or false-reject - the control.
func TestCrossFieldCleanMembersOverCrossPkgMixinClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"base/b.craftgo": `package base
type BaseMix { gamma string? }`,
		"api.craftgo": `package design
import "base"
@requiresOneOf(alpha, gamma)
type Host { base.BaseMix  alpha string? }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeCrossFieldNotOptional); d != nil {
		t.Errorf("clean cross-pkg-promoted member must not be rejected; got: %s", d.Msg)
	}
	if d := findCode(diags, CodeDecoratorRef); d != nil {
		t.Errorf("clean members must not false-reject; got: %s", d.Msg)
	}
}

// TestObjectFieldTypeRejected: `object` is a broken half-alias (its Go renderer
// emits an undefined type + dangling $ref); reject it and point at `any`.
func TestObjectFieldTypeRejected(t *testing.T) {
	expectError(t, `type X { f object }`, CodeRefUnknownSymbol)
	expectError(t, `type X { f object[] }`, CodeRefUnknownSymbol)
	// `any` is the valid arbitrary type.
	mustClean(t, `type X { f any  g any[] }`)
}
