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
