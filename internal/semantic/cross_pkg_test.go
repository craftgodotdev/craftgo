package semantic

import (
	"strings"
	"testing"
)

// ---------- qualified generic arity (cross-package) ----------

// TestQualifiedGenericRefMissingArgs pins that a qualified reference
// to a generic type without `<…>` fires a generic-arity diagnostic.
// checkGenerics skips qualified refs (the project resolver owns them),
// so the project resolver is the single site that checks arity for a
// qualified generic ref.
func TestQualifiedGenericRefMissingArgs(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/types.craftgo": `package shared
type Page<T> { items T[]  cursor string? }`,
		"app/types.craftgo": `package app
import "shared"
type Product { id string  page shared.Page }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeGenericArity)
	if d == nil {
		t.Fatalf("expected %s, got %v", CodeGenericArity, codes(diags))
	}
	if !strings.Contains(d.Msg, "shared.Page") || !strings.Contains(d.Msg, "1") {
		t.Errorf("msg should name shared.Page + expected arg count, got %q", d.Msg)
	}
}

func TestQualifiedGenericRefWrongArgCount(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/types.craftgo": `package shared
type Pair<A, B> { left A  right B }`,
		"app/types.craftgo": `package app
import "shared"
type User {}
type Holder { p shared.Pair<User> }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeGenericArity) == nil {
		t.Fatalf("wrong arity (1 of 2) must fire %s, got %v", CodeGenericArity, codes(diags))
	}
}

func TestQualifiedNonGenericWithArgs(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/types.craftgo": `package shared
type Plain { id string }`,
		"app/types.craftgo": `package app
import "shared"
type User {}
type Holder { p shared.Plain<User> }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeGenericNonGeneric) == nil {
		t.Fatalf("non-generic with args must fire %s, got %v", CodeGenericNonGeneric, codes(diags))
	}
}

func TestQualifiedGenericRefCorrectArgsClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/types.craftgo": `package shared
type Page<T> { items T[]  cursor string? }`,
		"app/types.craftgo": `package app
import "shared"
type Product { id string }
type Catalog { page shared.Page<Product> }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeGenericArity) != nil {
		t.Errorf("correct arity should not fire %s, got %v", CodeGenericArity, codes(diags))
	}
	if findCode(diags, CodeGenericNonGeneric) != nil {
		t.Errorf("correct usage should not fire %s, got %v", CodeGenericNonGeneric, codes(diags))
	}
}

// ---------- service collision (cross-package duplicate primary) ----------

func TestServiceCollisionAcrossPackages(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"a/svc.craftgo": `package a
type R { ok bool }
service Foo { get X /x { response R } }`,
		"b/svc.craftgo": `package b
type R { ok bool }
service Foo { get Y /y { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeServiceCollision)
	if d == nil {
		t.Fatalf("expected %s, got %v", CodeServiceCollision, diags)
	}
	if !strings.Contains(d.Msg, "multiple packages") {
		t.Errorf("message missing collision hint: %q", d.Msg)
	}
	// Both sites must fire so the IDE underlines each declaration.
	hits := 0
	for _, dd := range diags {
		if dd.Code == CodeServiceCollision {
			hits++
		}
	}
	if hits != 2 {
		t.Errorf("want 2 collision diagnostics, got %d", hits)
	}
}

func TestServiceCollisionSinglePackageStillUsesDuplicateCode(t *testing.T) {
	// In-package duplicates fire CodeServiceDuplicate, not the
	// cross-package collision code.
	root, files := projectFixture(t, map[string]string{
		"svc.craftgo": `package x
type R { ok bool }
service Foo { get A /a { response R } }
service Foo { get B /b { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeServiceDuplicate) == nil {
		t.Errorf("expected %s for in-package duplicate", CodeServiceDuplicate)
	}
	if findCode(diags, CodeServiceCollision) != nil {
		t.Errorf("did not expect %s for in-package case", CodeServiceCollision)
	}
}

// ---------- middleware collision (global names) ----------

func TestMiddlewareCollisionAcrossPackages(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"a/mw.craftgo": `package a
middleware AuthRequired`,
		"b/mw.craftgo": `package b
middleware AuthRequired`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeMiddlewareCollision) == nil {
		t.Fatalf("expected %s, got %v", CodeMiddlewareCollision, diags)
	}
}

// ---------- middleware refs cross-package ----------

func TestMiddlewareRefBareCrossPackage(t *testing.T) {
	// Bare reference resolves through the global union - no `import`
	// is required for middleware refs (unlike type refs).
	root, files := projectFixture(t, map[string]string{
		"shared/mw.craftgo": `package shared
middleware AuthRequired`,
		"users/svc.craftgo": `package users
type R { ok bool }
@middlewares(AuthRequired)
service U { get A /a { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeDecoratorRef); d != nil {
		t.Fatalf("unexpected ref error: %v", d)
	}
}

func TestMiddlewareRefQualifiedCrossPackage(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/mw.craftgo": `package shared
middleware AuthRequired`,
		"users/svc.craftgo": `package users
type R { ok bool }
@middlewares(shared.AuthRequired)
service U { get A /a { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeDecoratorRef); d != nil {
		t.Fatalf("unexpected ref error: %v", d)
	}
}

func TestMiddlewareRefUnknownProjectWide(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"users/svc.craftgo": `package users
type R { ok bool }
@middlewares(NotDeclared)
service U { get A /a { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeDecoratorRef)
	if d == nil {
		t.Fatalf("expected %s for unknown middleware, got %v", CodeDecoratorRef, diags)
	}
	if !strings.Contains(d.Msg, "NotDeclared") {
		t.Errorf("message should mention NotDeclared: %q", d.Msg)
	}
}

func TestMiddlewareRefQualifiedUnknownPackage(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"users/svc.craftgo": `package users
type R { ok bool }
@middlewares(missing.X)
service U { get A /a { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorRef) == nil {
		t.Fatalf("expected %s for missing.X, got %v", CodeDecoratorRef, diags)
	}
}

// ---------- extend orphan: cross-package primary ----------

func TestExtendOrphanCrossPackageHint(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"a/svc.craftgo": `package a
type R { ok bool }
service Real { get X /x { response R } }`,
		"b/extend.craftgo": `package b
type R { ok bool }
extend service Real { get Y /y { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeServiceExtendOrphan)
	if d == nil {
		t.Fatalf("expected %s, got %v", CodeServiceExtendOrphan, diags)
	}
	if !strings.Contains(d.Msg, "package \"a\"") {
		t.Errorf("expected message to name the owning package, got %q", d.Msg)
	}
	if len(d.Related) == 0 {
		t.Errorf("expected related entry pointing at the primary, got none")
	}
}

func TestExtendOrphanNoPrimaryAnywhere(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"x/svc.craftgo": `package x
type R { ok bool }
extend service Ghost { get Y /y { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeServiceExtendOrphan)
	if d == nil {
		t.Fatalf("expected %s, got %v", CodeServiceExtendOrphan, diags)
	}
	if !strings.Contains(d.Msg, "no primary declaration") {
		t.Errorf("expected fallback message, got %q", d.Msg)
	}
}

// ---------- import duplicate / alias conflict ----------

func TestImportDuplicate(t *testing.T) {
	pkg, diags := Analyze(parseFiles(t, `package x
import "shared"
import "shared"
type T { id string }`))
	_ = pkg
	if findCode(diags, CodeImportDuplicate) == nil {
		t.Fatalf("expected %s, got %v", CodeImportDuplicate, diags)
	}
}

func TestImportAliasConflictImplicit(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
import "shared/ok"
import "xyz/ok"
type T { id string }`))
	if findCode(diags, CodeImportAliasConflict) == nil {
		t.Fatalf("expected %s, got %v", CodeImportAliasConflict, diags)
	}
}

func TestImportAliasConflictExplicit(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
import s "shared"
import s "users"
type T { id string }`))
	if findCode(diags, CodeImportAliasConflict) == nil {
		t.Fatalf("expected %s, got %v", CodeImportAliasConflict, diags)
	}
}

// ---------- @passthrough body checks ----------

func TestPassthroughRejectsRequest(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
type Req { name string }
service S {
    @passthrough
    post Tail /t {
        request Req
    }
}`))
	d := findCode(diags, CodePassthroughBody)
	if d == nil {
		t.Fatalf("expected %s for @passthrough + request, got %v", CodePassthroughBody, diags)
	}
	if !strings.Contains(d.Msg, "@passthrough method must not declare request or response") {
		t.Errorf("expected canonical message, got %q", d.Msg)
	}
}

func TestPassthroughRejectsResponse(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
type Resp { ok bool }
service S {
    @passthrough
    get Tail /t {
        response Resp
    }
}`))
	d := findCode(diags, CodePassthroughBody)
	if d == nil {
		t.Fatalf("expected %s for @passthrough + response, got %v", CodePassthroughBody, diags)
	}
	if len(d.Related) != 1 {
		t.Errorf("expected related to point at @passthrough decorator, got %+v", d.Related)
	}
}

func TestPassthroughCleanShape(t *testing.T) {
	mustClean(t, `package x
service S {
    @passthrough get Tail /t {}
    @passthrough get UserTail /users/{id}/tail {}
}`)
}

// ---------- local type ref unknown ----------

func TestLocalTypeRefUnknown(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
type T { name strg }`))
	d := findCode(diags, CodeRefUnknownSymbol)
	if d == nil {
		t.Fatalf("expected %s for typo, got %v", CodeRefUnknownSymbol, diags)
	}
	if !strings.Contains(d.Msg, "strg") {
		t.Errorf("expected message to name the typo, got %q", d.Msg)
	}
}

func TestLocalTypeRefImportedAliasAsType(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
import "shared"
type T { user shared }`))
	d := findCode(diags, CodeRefUnknownSymbol)
	if d == nil {
		t.Fatalf("expected %s when bare import alias used as type, got %v", CodeRefUnknownSymbol, diags)
	}
	if !strings.Contains(d.Msg, "imported package") {
		t.Errorf("expected package-as-type hint, got %q", d.Msg)
	}
}

func TestLocalTypeRefGenericParam(t *testing.T) {
	// Type parameter `T` inside a generic body must NOT be flagged as
	// unknown - the analyser recognises it via the params set.
	mustClean(t, `package x
type Page<T> { items T[] cursor string? }`)
}

func TestLocalTypeRefScalarPrimitiveRejected(t *testing.T) {
	// Scalar primitives must be built-ins - see
	// [TestScalarUnknownPrimitiveRejected]. Verifying here that the
	// local-ref pass surfaces the diagnostic at the scalar's
	// declaration site (not on every downstream usage).
	expectDiag(t, `package x
scalar Weird unknownPrim`, CodeScalarBadPrimitive)
}

// ---------- isLocalSymbol coverage paths ----------

// TestMiddlewareRefAtMethodLevel exercises the @middlewares walker
// against per-method decorators, not just service-level ones.
func TestMiddlewareRefAtMethodLevel(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/mw.craftgo": `package shared
middleware AuthRequired`,
		"users/svc.craftgo": `package users
type R { ok bool }
service U {
    @middlewares(AuthRequired)
    get A /a { response R }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeDecoratorRef); d != nil {
		t.Fatalf("unexpected ref error on method-level @middlewares: %v", d)
	}
}

// TestMiddlewareDecoratorsIgnoresNonMiddleware verifies the project
// walker doesn't mistakenly probe non-middleware decorators in its
// inner loop.
func TestMiddlewareDecoratorsIgnoresNonMiddleware(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"users/svc.craftgo": `package users
type R { ok bool }
@prefix("/u")
@tags(users)
service U { get A /a { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeDecoratorRef); d != nil {
		t.Fatalf("unexpected ref error on non-middleware decorators: %v", d)
	}
}

// TestImportsWithAliasAndImplicit exercises the alias-set builder for
// both explicit and implicit aliases in the same file.
func TestImportsWithAliasAndImplicit(t *testing.T) {
	mustClean(t, `package x
import alias "shared"
import "users"
type T { id string }`)
}

// TestLocalNamedRefMultiPartSkipped covers the early-return path for
// qualified `pkg.Name` references - the cross-package resolver owns
// those, the local-ref pass passes through.
func TestLocalNamedRefMultiPartSkipped(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/u.craftgo": `package shared
type User { id string }`,
		"users/svc.craftgo": `package users
import "shared"
type T { user shared.User }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeRefUnknownSymbol); d != nil {
		t.Fatalf("unexpected unknown-symbol error on qualified ref: %v", d)
	}
}

// TestLocalTypeRefMapTypeRecurses ensures the map-type recursion walks
// both key and value through the unknown-symbol check.
func TestLocalTypeRefMapTypeRecurses(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
type T { kv map<string, mistype> }`))
	if findCode(diags, CodeRefUnknownSymbol) == nil {
		t.Fatalf("expected unknown-symbol on map value, got %v", diags)
	}
}

// TestMiddlewareCollisionTouchesAllSites verifies every decl that
// participates in the collision is reported (so editors can underline
// each of them, not just one).
func TestMiddlewareCollisionTouchesAllSites(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"a/m.craftgo": `package a
middleware X`,
		"b/m.craftgo": `package b
middleware X`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	hits := 0
	for _, d := range diags {
		if d.Code == CodeMiddlewareCollision {
			hits++
		}
	}
	if hits != 2 {
		t.Errorf("want 2 collision diagnostics, got %d (%v)", hits, diags)
	}
}

// TestNullableOnOptionalStillWarns confirms `@nullable` on a `T?`
// field produces the redundancy warning.
func TestNullableOnOptionalStillWarns(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
type T { name string? @nullable }`))
	d := findCode(diags, CodeDecoratorRedundant)
	if d == nil {
		t.Fatalf("expected %s for `?` + @nullable, got %v", CodeDecoratorRedundant, diags)
	}
	if !strings.Contains(d.Msg, "redundant") {
		t.Errorf("expected redundancy hint, got %q", d.Msg)
	}
}

// TestLocalSymbolEveryTypePositionKind pins the type-position resolver:
// scalars and enums declared in the same package resolve as field types
// without error; an error declaration name is rejected with a dedicated
// diagnostic pointing the user at `@errors(...)` rather than the generic
// "unknown type" branch.
func TestLocalSymbolEveryTypePositionKind(t *testing.T) {
	// Scalar + enum resolve cleanly.
	mustClean(t, `package x
scalar ID string
enum Color { Red Blue }
type T {
    id  ID
    hue Color
}`)
}

// TestErrorNameRejectedAsFieldType pins that declaring a field whose
// type is an `error` name (e.g. `field ref MissingErr` where
// `error NotFound MissingErr` lives in the same package) raises a
// diagnostic - errors are reserved for `@errors(...)`.
func TestErrorNameRejectedAsFieldType(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
error NotFound MissingErr
type T {
    ref MissingErr
}`))
	d := findCode(diags, CodeRefUnknownSymbol)
	if d == nil {
		t.Fatalf("expected %s when an error name is used as a field type, got %v", CodeRefUnknownSymbol, codes(diags))
	}
	if !strings.Contains(d.Msg, "@errors") {
		t.Errorf("diagnostic must hint at @errors as the correct usage, got %q", d.Msg)
	}
}
