package semantic

import "testing"

// A local @query and a cross-package-mixin @query sharing a wire name collide.
func TestProjectDuplicateWireNameCrossPkgMixin(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/sort.craftgo": `package shared
type SortOpt { order string @query("sort") }`,
		"api.craftgo": `package design
import "shared"
type ListReq {
	sort string @query("sort")
	shared.SortOpt
}
service S {
	get List /items { request ListReq }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDuplicateWireName) == nil {
		t.Errorf(`local @query("sort") + cross-pkg mixin @query("sort") should collide; got %v`, codes(diags))
	}
}

// Two cross-package mixins binding one wire name collide.
func TestProjectDuplicateWireNameTwoCrossPkgMixins(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/opts.craftgo": `package shared
type A { x string @query("q") }
type B { y string @query("q") }`,
		"api.craftgo": `package design
import "shared"
type ListReq {
	shared.A
	shared.B
}
service S {
	get List /items { request ListReq }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDuplicateWireName) == nil {
		t.Errorf(`two cross-pkg mixins binding @query("q") should collide; got %v`, codes(diags))
	}
}

// Header names fold case across packages: `X-Trace` and a mixin's `x-trace` collide.
func TestProjectDuplicateWireNameCrossPkgHeaderCaseFold(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/meta.craftgo": `package shared
type Meta { trace string @header("x-trace") }`,
		"api.craftgo": `package design
import "shared"
type ListReq {
	traceId string @header("X-Trace")
	shared.Meta
}
service S {
	get List /items { request ListReq }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDuplicateWireName) == nil {
		t.Errorf(`X-Trace and cross-pkg @header("x-trace") should collide (case-folded); got %v`, codes(diags))
	}
}

// Distinct wire names in the host and a cross-package mixin do not collide.
func TestProjectDuplicateWireNameCrossPkgClean(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/sort.craftgo": `package shared
type SortOpt { order string @query("order") }`,
		"api.craftgo": `package design
import "shared"
type ListReq {
	sort string @query("sort")
	shared.SortOpt
}
service S {
	get List /items { request ListReq }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if d := findCode(diags, CodeDuplicateWireName); d != nil {
		t.Errorf("distinct wire names sort/order must not collide; got: %s", d.Msg)
	}
}

// A same-package duplicate wire name is reported once.
func TestProjectDuplicateWireNameLocalReportedOnce(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/x.craftgo": `package shared
type Unused { note string }`,
		"api.craftgo": `package design
type ListReq {
	a string @query("dup")
	b string @query("dup")
}
service S {
	get List /items { request ListReq }
}`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	n := 0
	for _, c := range codes(diags) {
		if c == CodeDuplicateWireName {
			n++
		}
	}
	if n != 1 {
		t.Errorf("same-package duplicate wire name should report exactly once, got %d: %v", n, codes(diags))
	}
}

// An empty `@path("")` falls back to the field name.
func TestEmptyPathWireNameClean(t *testing.T) {
	mustClean(t, `type Req { foo string @path("") }
service S { get G /users/{foo} { request Req } }`)
}

// Header names that differ only in case collide; net/http canonicalises both.
func TestDuplicateWireNameHeaderCase(t *testing.T) {
	expectError(t, `type R { a string @header("X-Trace")  b string @header("x-trace") }`, CodeDuplicateWireName)
}

// A wire name promoted from a same-package mixin collides with the host's re-bind.
func TestDuplicateWireNameMixin(t *testing.T) {
	expectError(t, `type Base { a string @query("q") }
type R { Base  b string @query("q") }`, CodeDuplicateWireName)
}

// An auto-@path field collides with a sibling's explicit @path of the same name.
func TestDuplicateAutoPathWireName(t *testing.T) {
	expectError(t, `type R { id string  other string @path("id") }
service S { get G /g/{id} { request R } }`, CodeDuplicateWireName)
}

// An auto-@query field collides with a sibling's explicit @query of the same name.
func TestDuplicateAutoQueryWireName(t *testing.T) {
	expectError(t, `type R { sort string  order string @query("sort") }
type Resp { ok bool }
service S { get G /g { request R  response Resp } }`, CodeDuplicateWireName)
}

// Distinct wire names do not collide.
func TestDistinctWireNamesClean(t *testing.T) {
	expectNoCode(t, `type R { sortBy string @query("sortBy")  order string @query("order") }
type Resp { ok bool }
service S { get G /g { request R  response Resp } }`, CodeDuplicateWireName)
}
