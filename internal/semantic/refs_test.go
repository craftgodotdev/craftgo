package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

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
	// A plain field has no present/absent state for the group to test.
	d := expectDiag(t, `@requiresOneOf(email, phone)
type Contact { email string?  phone string }`, CodeCrossFieldNotOptional)
	expectMessage(t, d, "phone")
}

func TestCrossFieldNullableFieldAccepted(t *testing.T) {
	// A @nullable field is pointer-backed, so it has a presence like `?`.
	mustClean(t, `@mutuallyExclusive(a, b)
type T { a string @nullable  b string? }`)
}

func TestCrossFieldRejectsWireBoundMember(t *testing.T) {
	// A @query member is not part of the JSON body the group constrains.
	d := expectDiag(t, `@requiresOneOf(q1, q2)
type Req { q1 string? @query  q2 string? @query }`, CodeCrossFieldNotOptional)
	expectMessage(t, d, "q1")
}

func TestCrossFieldRejectsDefaultMember(t *testing.T) {
	// A @default member is always present.
	d := expectDiag(t, `@requiresOneOf(a, b)
type Req { a string? @default("x")  b string? }`, CodeCrossFieldNotOptional)
	expectMessage(t, d, "a")
}

func TestCrossFieldRejectsNilableNonPointerMember(t *testing.T) {
	// Slice, map, bytes and any members are nilable but not pointers, so they have no clean presence.
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
	// A @sensitive member never appears in the JSON body.
	d := expectDiag(t, `@mutuallyExclusive(secret, b)
type Req { secret string? @sensitive  b string? }`, CodeCrossFieldNotOptional)
	expectMessage(t, d, "secret")
}

func TestSecurityRefSkippedWithoutOptions(t *testing.T) {
	mustClean(t, `@security(unknown)
service S {}`)
}

// expectRefWithOptions analyzes src with opts and returns its CodeDecoratorRef diagnostic.
func expectRefWithOptions(t *testing.T, src string, opts Options) *Diagnostic {
	t.Helper()
	_, diags := AnalyzeWith(parseFiles(t, src), opts)
	d := findCode(diags, CodeDecoratorRef)
	if d == nil {
		t.Fatalf("expected %s, got %v", CodeDecoratorRef, codes(diags))
	}
	return d
}

// expectNoRefWithOptions fails if analyzing src with opts reports CodeDecoratorRef.
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

// @ignoreSecurity is not checked as a security scheme reference.
func TestSecurityRefAcceptsIgnoreSecurity(t *testing.T) {
	expectNoRefWithOptions(t, `service S {
	@ignoreSecurity
	get Public /p {}
}`, Options{SecuritySchemes: []string{"bearerAuth"}})
}

func TestSecurityRefSkipsNonIdentArg(t *testing.T) {
	// The args pass reports the kind; the refs pass skips it.
	expectNoRefWithOptions(t, `@security(123)
service S {}`, Options{SecuritySchemes: []string{"bearerAuth"}})
}

func TestSecurityRefSkipsZeroArgs(t *testing.T) {
	// The args pass reports the arity; the refs pass has no name to resolve.
	expectNoRefWithOptions(t, `@security
service S {}`, Options{SecuritySchemes: []string{"bearerAuth"}})
}

func TestExtendServiceMiddlewareIsChecked(t *testing.T) {
	expectDiag(t, `service S {}
extend service S {
	@middlewares(Bogus)
	get Op /x {}
}`, CodeDecoratorRef)
}

func TestExtendServiceDecoratorCheckedWithoutMethods(t *testing.T) {
	expectDiag(t, `service S {}
@middlewares(Bogus)
extend service S {}`, CodeDecoratorRef)
	expectDiag(t, `service S {}
@errors(Bogus)
extend service S {}`, CodeDecoratorRef)
}

func TestExtendServiceDecoratorDiagnosedOnce(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `service S {}
@middlewares(Bogus)
extend service S {
	get A /a {}
	get B /b {}
}`))
	n := 0
	for _, d := range diags {
		if d.Code == CodeDecoratorRef {
			n++
		}
	}
	if n != 1 {
		t.Errorf("expected 1 %s diagnostic, got %d: %v", CodeDecoratorRef, n, diags)
	}
}

func TestRefsNilDecoratorTolerated(t *testing.T) {
	a := newTestAnalyzer(&Package{
		Errors:      map[string]*ast.ErrorDecl{},
		Middlewares: map[string]*ast.MiddlewareDecl{},
	})
	a.checkFieldGroupRefs("X", []*ast.Decorator{nil}, nil)
	a.checkServiceLevelRefs([]*ast.Decorator{nil})
	a.checkMemberLevelRefs([]*ast.Decorator{nil}, LvlMethod)
	if len(a.diags) != 0 {
		t.Errorf("nil decorator entries should not diag, got %v", a.diags)
	}
}

// A cross-field group may name a field promoted from a cross-package mixin.
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

// A typo in a cross-field group over a cross-package mixin is rejected.
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

// A typo in a cross-field group over a nested cross-package mixin is rejected.
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

// A cross-field group may name a field promoted through two cross-package mixin levels.
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

// A cross-field group rejects a non-optional member promoted from a cross-package mixin.
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

// A cross-field group rejects a @default member promoted from a cross-package mixin.
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

// Optional members, one local and one from a cross-package mixin, raise no diagnostic.
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

// The field type `object` is rejected.
func TestObjectFieldTypeRejected(t *testing.T) {
	expectError(t, `type X { f object }`, CodeRefUnknownSymbol)
	expectError(t, `type X { f object[] }`, CodeRefUnknownSymbol)
	// `any` is the valid arbitrary type.
	mustClean(t, `type X { f any  g any[] }`)
}
