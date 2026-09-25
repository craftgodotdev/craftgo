package semantic

import (
	"slices"
	"strings"
	"testing"
)

// A qualified reference to a generic type without type arguments reports its arity.
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

// HTTP services of one name in two packages collide on their output directory, at both declarations.
func TestHTTPServiceOfOneNameInTwoPackagesCollidesOnItsDirectory(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"a/svc.craftgo": `package a
type R { ok bool }
service Foo { get X /x { response R } }`,
		"b/svc.craftgo": `package b
type R { ok bool }
service Foo { get Y /y { response R } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeGroupPackageStraddle)
	if d == nil {
		t.Fatalf("expected %s, got %v", CodeGroupPackageStraddle, diags)
	}
	if !strings.Contains(d.Msg, "output directory") {
		t.Errorf("message missing collision hint: %q", d.Msg)
	}
	hits := 0
	for _, dd := range diags {
		if dd.Code == CodeGroupPackageStraddle {
			hits++
		}
	}
	if hits != 2 {
		t.Errorf("want 2 collision diagnostics, got %d", hits)
	}
}

// Method-less services of one name in two packages claim no directory and do not collide.
func TestMethodlessServiceOfOneNameInTwoPackagesIsFine(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"a/svc.craftgo": `package a
type P { ok bool }
event Placed { payload P }
service Store {}`,
		"b/svc.craftgo": `package b
type P { ok bool }
event Shipped { payload P }
service Store {}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	for _, d := range diags {
		if d.IsError() {
			t.Fatalf("two packages declaring one method-less service diagnosed: %v", diags)
		}
	}
}

func TestServiceCollisionSinglePackageStillUsesDuplicateCode(t *testing.T) {
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
}

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

// A bare middleware name resolves across packages without an import.
func TestMiddlewareRefBareCrossPackage(t *testing.T) {
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

// A @passthrough method may declare request and response blocks.
func TestPassthroughAcceptsBlocks(t *testing.T) {
	mustClean(t, `package x
type Req { id string @path  name string }
type Resp { ok bool }
service S {
    @passthrough
    post Tail /t/{id} {
        request  Req
        response Resp
    }
}`)
}

func TestPassthroughCleanShape(t *testing.T) {
	mustClean(t, `package x
service S {
    @passthrough get Tail /t {}
    @passthrough get UserTail /users/{id}/tail {}
}`)
}

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

// A type parameter inside a generic body is a known type.
func TestLocalTypeRefGenericParam(t *testing.T) {
	mustClean(t, `package x
type Page<T> { items T[] cursor string? }`)
}

// A scalar over an unknown primitive is reported at the scalar declaration.
func TestLocalTypeRefScalarPrimitiveRejected(t *testing.T) {
	expectDiag(t, `package x
scalar Weird unknownPrim`, CodeScalarBadPrimitive)
}

// A method-level @middlewares resolves a middleware from another package.
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

// The middleware reference check ignores other service decorators.
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

// A file may mix aliased and unaliased imports.
func TestImportsWithAliasAndImplicit(t *testing.T) {
	mustClean(t, `package x
import alias "shared"
import "users"
type T { id string }`)
}

// A qualified reference to another package's type is not reported as unknown.
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

// An unknown map value type is reported.
func TestLocalTypeRefMapTypeRecurses(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
type T { kv map<string, mistype> }`))
	if findCode(diags, CodeRefUnknownSymbol) == nil {
		t.Fatalf("expected unknown-symbol on map value, got %v", diags)
	}
}

// A middleware name collision is reported at every declaration.
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

// @nullable on a `T?` field warns as redundant.
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

// Local scalars and enums resolve as field types.
func TestLocalSymbolEveryTypePositionKind(t *testing.T) {
	mustClean(t, `package x
scalar ID string
enum Color { Red Blue }
type T {
    id  ID
    hue Color
}`)
}

// An error name used as a field type is rejected with a hint at @errors.
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

// A qualified error name in a type position gets the bare name's hint.
func TestQualifiedErrorNameRejectedAsFieldType(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
error NotFound Gone`,
		"app/a.craftgo": `package app
type T { remote shared.Gone }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeRefUnknownSymbol)
	if d == nil {
		t.Fatalf("expected %s for an error used as a field type, got %v", CodeRefUnknownSymbol, codes(diags))
	}
	expectMessage(t, d, "shared.Gone", "error declaration", "@errors")
}

// A qualified generic mixin with the wrong arity reports one mixin diagnostic.
func TestQualifiedMixinArityReportedOnce(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/types.craftgo": `package shared
type Page<T> { items T[] }`,
		"app/types.craftgo": `package app
type List { shared.Page  total int }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if got := codes(diags); !slices.Equal(got, []string{CodeMixinArity}) {
		t.Errorf("want one %s, got %v", CodeMixinArity, diags)
	}
}

// A qualified enum takes no generic arguments.
func TestQualifiedArgsOnEnum(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/types.craftgo": `package shared
enum Color { Red Blue }`,
		"app/types.craftgo": `package app
type T { c shared.Color<int> }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeGenericNonGeneric)
	if d == nil {
		t.Fatalf("want %s, got %v", CodeGenericNonGeneric, diags)
	}
	expectMessage(t, d, "shared.Color")
}

// Naming a reference's own package is a self-qualification.
func TestSelfQualifiedRef(t *testing.T) {
	files := parseFiles(t, `package app
type A { id string }`, `package app
type B { a app.A }`)
	_, diags := AnalyzeProject(files, Options{})
	d := findCode(diags, CodeQualifiedRef)
	if d == nil {
		t.Fatalf("want %s, got %v", CodeQualifiedRef, diags)
	}
	expectMessage(t, d, "redundant self-qualification")
}
