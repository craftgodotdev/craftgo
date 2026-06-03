package semantic

import "testing"

// A request type can embed a mixin from a SIBLING package whose fields
// supply the @path binding. The per-package pass can't expand that mixin,
// so the project-level check ([refResolver.checkProjectPathParams]) owns
// the verdict — matching the codegen binder's cross-package flattening.

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
// field — the segment genuinely has nothing to bind to.
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
// field carrying @nullable is rejected by the project twin — the per-package
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
