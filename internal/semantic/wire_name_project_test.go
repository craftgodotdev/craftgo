package semantic

import "testing"

// The per-package duplicate-wire-name check flattens only same-package
// mixins, so a wire name reused by a field promoted through a CROSS-package
// mixin escapes it. [refResolver.checkProjectDuplicateWireNames] owns that
// verdict, matching the codegen binder's cross-package flattening: without
// it the binder reads one wire value into two fields and the OpenAPI carries
// a duplicate parameter.

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

// Two different cross-package mixins that each bind the same wire name
// collide even though neither field is visible to the per-package pass.
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

// Header wire names case-fold across packages just like within one, so
// `X-Trace` and a mixin's `@header("x-trace")` collide.
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

// Distinct wire names across the local body and a cross-package mixin do NOT
// collide - the check must not false-positive on merely co-embedded bindings.
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

// A purely same-package duplicate is reported EXACTLY once - by the
// per-package pass. The cross-package twin must not double-report it.
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
