package semantic

import "testing"

// A request type can embed a mixin from a SIBLING package whose fields
// supply the @path binding. The per-package pass can't expand that mixin,
// so the project-level check ([refResolver.checkProjectPathParams]) owns
// the verdict - matching the codegen binder's cross-package flattening.

// Cross-package mixin supplies the @path field → the {id} segment binds,
// no false "no matching field" error.
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

// The check still fires when the cross-package mixin supplies the wrong
// field - the segment genuinely has nothing to bind to.
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

// An explicit @path field pulled from a cross-package mixin with no
// matching route segment is an orphan, reported cross-package.
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

// A @path field nested two mixin levels deep through a cross-package
// mixin (shared.Outer embeds shared.Inner) must still bind {pk}: the
// flattener qualifies the bare inner `Inner` as `shared.Inner`.
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

// W3 (#16): a cross-package request (request shared.R) with an auto-path
// field carrying @nullable is rejected by the project twin - the per-package
// pass returns early for cross-pkg requests, so without the twin it silently
// emitted non-compiling Go (a plain string written into a *string slot).
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

// Control: a clean cross-package auto-path field (no @nullable) must NOT be
// false-rejected by the project twin.
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

// W3-inc2 (#16 sibling): a cross-package request on a body-less verb whose
// field auto-binds to @query with @nullable is rejected by the project twin
// (non-compiling without it).
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

// W3-inc2: a bare cross-package scalar request type is rejected by the
// project twin.
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

// W5: a cross-package request whose field auto-binds to a path segment but is
// a STRUCT (no path-string form) silently lost its binding - codegen emitted a
// handler that left the field zero with no error. The project twin resolves
// the cross-package type through the IR and rejects it at design time, matching
// the per-package pass.
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

// Control: a cross-package SCALAR (over a wire primitive) auto-binding to a
// path segment is bindable and must NOT be false-rejected - the local table
// can't resolve it, so only the IR-backed twin gets this right.
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

// W5 sibling: a cross-package struct auto-binding to @query on a body-less verb
// was only caught by a position-less codegen error. The project twin now
// rejects it at design time with a position, matching the per-package pass.
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

// Control: a cross-package scalar and a 1-D array of primitives both ride a
// @query string (repeated values), so the twin must NOT false-reject them.
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

// Cross-package @example now has a project twin (shared with @default): an
// @example literal whose kind mismatches a foreign scalar's primitive is
// rejected, where it once slipped through unchecked.
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

// Control: a valid cross-package @example is clean.
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

// A cross-package scalar @default exceeding the underlying primitive's
// capacity must be rejected the SAME as its local twin - the project default
// validator now shares the per-package literal check (kind + capacity), so it
// no longer accepts an out-of-range cross-pkg default that codegen can't cast.
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

// Control: an in-range cross-package scalar default is clean.
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

// A cross-field group member that is a DIRECT field whose TYPE is a
// cross-package scalar over bytes (`rawData shared.Blob?`) must be rejected
// like its local twin - the per-package presence check resolves it with
// proj=nil so the bytes primitive never surfaces; the project pass must run
// (deferred) even though no cross-package MIXIN was traversed.
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

// Control: the same direct member over a VALUE primitive (`shared.Code` over
// string) is pointer-backed and clean - must NOT be false-rejected.
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

// A cross-package qualified struct (or other non-wire type) bound with
// @header on an ERROR body field must be rejected - the per-package pass
// defers qualified refs, and checkProjectBindings once iterated only
// pkg.Types, so the error field slipped past both passes into non-compiling
// `string(e.Detail)` Go. The project binding check now sweeps pkg.Errors too.
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

// Control: a cross-package SCALAR (over a wire primitive) @header on an error
// field is valid and must NOT be false-rejected.
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

// Cross-package twin of the Go-name collision: two mixins from DIFFERENT
// packages each promote a field that lowers to the same Go identifier
// (`userId` from m1.A, `user_id` from m2.B → both `UserID`). The project mixin
// pass must reject it - without this codegen emits an ambiguous selector
// (`v.UserID`) that won't compile.
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

// #6: a cross-field group member promoted from a sibling-package mixin carries
// a bare named type (`Blob`, not `base.Blob`). When that scalar is over `bytes`
// its Go type is nilable, so it has no clean present/absent state and the group
// must reject it. The project flattener requalifies the promoted field to its
// home package so the resolver can see the scalar at all.
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

// Control: the same promotion of a scalar over a VALUE primitive (`string`) is
// pointer-backed and present/absent-clean, so it must NOT be false-rejected.
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

// Two methods in DIFFERENT packages resolving to the same VERB + route
// register the same net/http pattern - the second registration panics at
// boot, so the project pass rejects it with both sides named.
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

// Collisions key on route SHAPE: `{id}` vs `{uid}` is the same net/http
// pattern, so renaming the variable does not dodge the check.
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

// Distinct verbs (or distinct paths) on the same route never collide.
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

// A SAME-package duplicate stays the per-package pass's report - the project
// pass skips it, so the pair yields exactly one diagnostic, not two.
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
