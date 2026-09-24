package semantic

import (
	"strings"
	"testing"
)

// A @path field from a cross-package mixin binds the {id} segment.
func TestProjectPathParamCrossPkgMixinBinds(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/holder.craftgo": `package shared
type IdHolder { id string @path }`,
		"api.craftgo": `package design
import "shared"
type Req { shared.IdHolder }
service S {
	get G /users/{id} { request Req }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodePathParamMissing); d != nil {
		t.Errorf("cross-pkg mixin @path should bind {id}, got: %s", d.Msg)
	}
}

// A segment that no field of the cross-package mixin binds is reported missing.
func TestProjectPathParamCrossPkgMixinMissing(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/holder.craftgo": `package shared
type Other { other string @path }`,
		"api.craftgo": `package design
import "shared"
type Req { shared.Other }
service S {
	get G /users/{id} { request Req }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodePathParamMissing) == nil {
		t.Errorf("segment {id} with no matching field should report missing; got %v", codes(diags))
	}
}

// A @path field from a cross-package mixin with no route segment is an orphan.
func TestProjectPathParamCrossPkgMixinOrphan(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/holder.craftgo": `package shared
type IdHolder { id string @path }`,
		"api.craftgo": `package design
import "shared"
type Req { shared.IdHolder }
service S {
	get G /users { request Req }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodePathParamOrphan) == nil {
		t.Errorf("cross-pkg @path field with no segment should orphan; got %v", codes(diags))
	}
}

// A @path field nested two mixin levels deep in another package binds {pk}.
func TestProjectPathParamNestedCrossPkgMixin(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/holder.craftgo": `package shared
type Inner { pk string @path }
type Outer { Inner }`,
		"api.craftgo": `package design
import "shared"
type Req { shared.Outer }
service S {
	get G /by/{pk} { request Req }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodePathParamMissing); d != nil {
		t.Errorf("nested cross-pkg @path should bind {pk}, got: %s", d.Msg)
	}
}

// A cross-package request whose auto-path field is @nullable is rejected.
func TestProjectAutoPathFieldCrossPkgNullableRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
type R { id string @nullable  name string }`,
		"api/api.craftgo": `package api
import "shared"
type Resp { ok bool }
service S { get G /u/{id} { request shared.R  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorConflict) == nil {
		t.Errorf("cross-pkg auto-path @nullable should be rejected; got %v", codes(diags))
	}
}

// A cross-package request whose auto-path field is not @nullable is accepted.
func TestProjectAutoPathFieldCrossPkgClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
type R { id string  name string }`,
		"api/api.craftgo": `package api
import "shared"
type Resp { ok bool }
service S { get G /u/{id} { request shared.R  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorConflict) != nil {
		t.Errorf("clean cross-pkg auto-path field wrongly rejected: %v", codes(diags))
	}
}

// A cross-package request on a body-less verb rejects a @nullable auto-@query field.
func TestProjectBodyBindingVerbCrossPkgNullableRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
type R { q string @nullable }`,
		"api/api.craftgo": `package api
import "shared"
type Resp { ok bool }
service S { get G /g { request shared.R  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorConflict) == nil {
		t.Errorf("cross-pkg auto-@query @nullable should be rejected; got %v", codes(diags))
	}
}

// A bare cross-package scalar is rejected as a request type.
func TestProjectBareCrossPkgScalarRequestRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
scalar Email string`,
		"api/api.craftgo": `package api
import "shared"
type Resp { ok bool }
service S { post Do /do { request shared.Email  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeBindingType) == nil {
		t.Errorf("bare cross-pkg scalar request should be rejected; got %v", codes(diags))
	}
}

// A cross-package request rejects a struct field that auto-binds to a path segment.
func TestProjectAutoPathFieldCrossPkgStructRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"base/base.craftgo": `package base
type Nested { a string }
type R { id Nested  name string }`,
		"app/app.craftgo": `package app
import "base"
type Resp { ok bool }
service S { get G /u/{id} { request base.R  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeBindingType) == nil {
		t.Errorf("cross-pkg struct auto-path field should be rejected; got %v", codes(diags))
	}
}

// A cross-package scalar over a wire primitive binds to a path segment.
func TestProjectAutoPathFieldCrossPkgScalarClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"base/base.craftgo": `package base
scalar Id string
type R { id Id  name string }`,
		"app/app.craftgo": `package app
import "base"
type Resp { ok bool }
service S { get G /u/{id} { request base.R  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeBindingType) != nil {
		t.Errorf("cross-pkg scalar auto-path field wrongly rejected: %v", codes(diags))
	}
}

// A cross-package struct field that auto-binds to @query on a body-less verb is rejected.
func TestProjectAutoQueryFieldCrossPkgStructRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"base/base.craftgo": `package base
type Nested { a string }
type R { filter Nested  name string }`,
		"app/app.craftgo": `package app
import "base"
type Resp { ok bool }
service S { get G /g { request base.R  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeBindingType) == nil {
		t.Errorf("cross-pkg struct auto-@query field should be rejected; got %v", codes(diags))
	}
}

// A cross-package scalar and a 1-D primitive array both bind to @query.
func TestProjectAutoQueryFieldCrossPkgBindableClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"base/base.craftgo": `package base
scalar Tag string
type R { t Tag  tags string[]  name string }`,
		"app/app.craftgo": `package app
import "base"
type Resp { ok bool }
service S { get G /g { request base.R  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeBindingType) != nil {
		t.Errorf("cross-pkg scalar / 1-D array auto-@query wrongly rejected: %v", codes(diags))
	}
}

// An @example whose kind mismatches a cross-package scalar's primitive is rejected.
func TestProjectCrossPkgExampleKindMismatchRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
scalar Count int`,
		"app/app.craftgo": `package app
import "shared"
type R { n shared.Count? @example("not-an-int") }
type Resp { ok bool }
service S { post C /c { request R  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorArgType) == nil {
		t.Errorf("cross-pkg @example kind mismatch should be rejected; got %v", codes(diags))
	}
}

// A valid @example on a cross-package scalar is accepted.
func TestProjectCrossPkgExampleValidClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
scalar Count int`,
		"app/app.craftgo": `package app
import "shared"
type R { n shared.Count? @example(5) }
type Resp { ok bool }
service S { post C /c { request R  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorArgType) != nil {
		t.Errorf("valid cross-pkg @example wrongly rejected: %v", codes(diags))
	}
}

// A @default that overflows a cross-package scalar's primitive is rejected.
func TestProjectCrossPkgDefaultOverflowRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
scalar Tiny int8`,
		"app/app.craftgo": `package app
import "shared"
type R { n shared.Tiny? @default(200) }
type Resp { ok bool }
service S { post C /c { request R  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeBoundOverflow) == nil {
		t.Errorf("cross-pkg int8 @default(200) should overflow-reject; got %v", codes(diags))
	}
}

// An in-range @default on a cross-package scalar is accepted.
func TestProjectCrossPkgDefaultInRangeClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
scalar Tiny int8`,
		"app/app.craftgo": `package app
import "shared"
type R { n shared.Tiny? @default(100) }
type Resp { ok bool }
service S { post C /c { request R  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeBoundOverflow) != nil {
		t.Errorf("in-range cross-pkg default wrongly rejected: %v", codes(diags))
	}
}

// A cross-field group rejects a direct member typed as a cross-package scalar over bytes.
func TestProjectCrossFieldDirectCrossPkgScalarBytesRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
scalar Blob bytes`,
		"app/app.craftgo": `package app
import "shared"
@requiresOneOf(rawData, other)
type Pick { rawData shared.Blob?  other string? }
type Resp { ok bool }
service S { post C /c { request Pick  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeCrossFieldNotOptional) == nil {
		t.Errorf("cross-pkg scalar-over-bytes direct member should be rejected; got %v", codes(diags))
	}
}

// A cross-field group accepts a direct member typed as a cross-package scalar over string.
func TestProjectCrossFieldDirectCrossPkgScalarStringClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
scalar Code string`,
		"app/app.craftgo": `package app
import "shared"
@requiresOneOf(code, other)
type Pick { code shared.Code?  other string? }
type Resp { ok bool }
service S { post C /c { request Pick  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeCrossFieldNotOptional) != nil {
		t.Errorf("cross-pkg scalar-over-string direct member wrongly rejected: %v", codes(diags))
	}
}

// An error field that binds a cross-package struct to @header is rejected.
func TestProjectErrorFieldCrossPkgStructHeaderRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
type Point { x int  y int }`,
		"app/app.craftgo": `package app
import "shared"
error NotFound NF { detail shared.Point @header("X-Detail")  note string }
type Resp { ok bool }
service S { @errors(NF) post C /c { request Resp  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeBindingType) == nil {
		t.Errorf("cross-pkg struct @header on an error field should be rejected; got %v", codes(diags))
	}
}

// An error field that binds a cross-package scalar to @header is accepted.
func TestProjectErrorFieldCrossPkgScalarHeaderClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/shared.craftgo": `package shared
scalar Reason string`,
		"app/app.craftgo": `package app
import "shared"
error NotFound NF { reason shared.Reason @header("X-Reason")  note string }
type Resp { ok bool }
service S { @errors(NF) post C /c { request Resp  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeBindingType) != nil {
		t.Errorf("cross-pkg scalar @header on an error field wrongly rejected: %v", codes(diags))
	}
}

// Mixins from two packages whose fields share a Go name (`userId`, `user_id` → `UserID`) conflict.
func TestProjectMixinCrossPkgGoNameCollision(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"m1/m1.craftgo": `package m1
type A { userId string? }`,
		"m2/m2.craftgo": `package m2
type B { user_id string? }`,
		"app/app.craftgo": `package app
import "m1"
import "m2"
type C { m1.A  m2.B }
type Resp { ok bool }
service S { post C /c { request C  response Resp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeMixinConflict) == nil {
		t.Errorf("cross-pkg mixin Go-name collision should be rejected; got %v", codes(diags))
	}
}

// A cross-field group rejects a cross-package mixin member whose scalar is over bytes.
func TestProjectCrossFieldCrossPkgScalarOverBytesRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"base/base.craftgo": `package base
scalar Blob bytes
type Carrier { blob Blob?  name string? }`,
		"app/app.craftgo": `package app
import "base"
@requiresOneOf(blob, name)
type Req { base.Carrier }
service S { post C /c { request Req  response Req } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeCrossFieldNotOptional) == nil {
		t.Errorf("cross-pkg-promoted scalar-over-bytes member should be rejected; got %v", codes(diags))
	}
}

// A cross-field group accepts a promoted cross-package scalar over string.
func TestProjectCrossFieldCrossPkgScalarOverStringClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"base/base.craftgo": `package base
scalar Code string
type Carrier { code Code?  name string? }`,
		"app/app.craftgo": `package app
import "base"
@requiresOneOf(code, name)
type Req { base.Carrier }
service S { post C /c { request Req  response Req } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeCrossFieldNotOptional) != nil {
		t.Errorf("cross-pkg-promoted scalar-over-string member wrongly rejected: %v", codes(diags))
	}
}

// The same verb and route in two packages collide, and the report points at the first.
func TestProjectPathCollisionCrossPkg(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"alpha/a.craftgo": `package alpha
type AResp { ok bool }
@prefix("/things")
service AlphaService { get List /items { response AResp } }`,
		"beta/b.craftgo": `package beta
type BResp { ok bool }
@prefix("/things")
service BetaService { get Fetch /items { response BResp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodePathCollision)
	if d == nil {
		t.Fatalf("cross-package route duplicate should collide; got %v", codes(diags))
	}
	if len(d.Related) == 0 {
		t.Errorf("collision should point at the first declaration via Related")
	}
}

// Routes that differ only in a path variable's name (`{id}`, `{uid}`) collide.
func TestProjectPathCollisionCrossPkgShape(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"alpha/a.craftgo": `package alpha
type AReq { id string @path }
type AResp { ok bool }
service AlphaService { get Get /users/{id} { request AReq  response AResp } }`,
		"beta/b.craftgo": `package beta
type BReq { uid string @path }
type BResp { ok bool }
service BetaService { get Read /users/{uid} { request BReq  response BResp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodePathCollision) == nil {
		t.Errorf("same-shape cross-package routes should collide; got %v", codes(diags))
	}
}

// Routes that differ in verb or path do not collide.
func TestProjectPathCollisionCleanCases(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"alpha/a.craftgo": `package alpha
type AResp { ok bool }
service AlphaService { get List /items { response AResp } }`,
		"beta/b.craftgo": `package beta
type BReq { name string }
type BResp { ok bool }
service BetaService {
	post Create /items { request BReq  response BResp }
	get Others /entries { response BResp }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodePathCollision); d != nil {
		t.Errorf("distinct verb/path must not collide: %s", d.Msg)
	}
}

// A same-package duplicate route is reported once.
func TestProjectPathCollisionSamePkgNoDoubleFire(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"alpha/a.craftgo": `package alpha
type AResp { ok bool }
service FirstService { get List /items { response AResp } }
service SecondService { get Fetch /items { response AResp } }`,
		"beta/b.craftgo": `package beta
type BResp { ok bool }
service BetaService { get Other /entries { response BResp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	n := 0
	for _, d := range diags {
		if d.Code == CodePathCollision {
			n++
		}
	}
	if n != 1 {
		t.Errorf("same-package duplicate should report exactly once (per-package pass), got %d: %v", n, codes(diags))
	}
}

// Same-verb routes that overlap with neither more specific are reported once, naming both.
func TestProjectPathOverlapReported(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"m/x.craftgo": `package m
type IDReq { id string @path }
type StatusReq { status string @path }
type Resp { ok bool }
@prefix("/orders")
service OrderService {
    get Track /{id}/track { request IDReq  response Resp }
    get Filter /by-status/{status} { request StatusReq  response Resp }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root, BasePath: "/api"})
	if len(diags) != 1 {
		t.Fatalf("want exactly one diagnostic, got %v", diags)
	}
	d := diags[0]
	if d.Code != CodePathCollision {
		t.Fatalf("code = %s, want %s", d.Code, CodePathCollision)
	}
	for _, want := range []string{"Filter", "Track", "overlaps", "/api/orders/{id}/track"} {
		if !strings.Contains(d.Msg, want) {
			t.Errorf("message should mention %q: %s", want, d.Msg)
		}
	}
}

// The same routes with the filter under a literal sub-path report nothing.
func TestProjectPathOverlapClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"m/x.craftgo": `package m
type IDReq { id string @path }
type Resp { ok bool }
@prefix("/orders")
service OrderService {
    get Track /{id}/track { request IDReq  response Resp }
    get Filter /by-status { response Resp }
    get Get /{id} { request IDReq  response Resp }
}`,
	})
	if _, diags := AnalyzeProject(files, Options{DesignRoot: root, BasePath: "/api"}); len(diags) != 0 {
		t.Errorf("clean routes should not conflict, got %v", diags)
	}
}
