package semantic

import "testing"

// A multi-dimensional array field that auto-binds to @query is rejected, like an explicit one.
func TestAutoQueryMultiDimArrayRejected(t *testing.T) {
	expectError(t, `type Req { grid int[][] }
service S { get Op /x { request Req } }`, CodeBindingType)
}

// A 1-D array field that auto-binds to @query is accepted.
func TestAutoQuerySingleDimArrayClean(t *testing.T) {
	mustClean(t, `type Req { tags string[] }
service S { get Op /x { request Req } }`)
}

// A @nullable cross-package scalar field that auto-binds to @query is rejected.
func TestNullableCrossPkgAutoQueryRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"lib/l.craftgo": `package lib
scalar Email string @format(email)
enum Color { Red  Green  Blue }`,
		"api.craftgo": `package design
import "lib"
type XReq { nul lib.Email @nullable }
type XResp { ok bool }
service XSvc { get X /x { request XReq  response XResp } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	if findCode(diags, CodeDecoratorConflict) == nil {
		t.Fatalf("expected cross-pkg @nullable auto-@query rejection; got %v", codes(diags))
	}
}

// A cross-package binding error is reported once.
func TestCrossPkgBindingDiagnosticNotDuplicated(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": `package shared
scalar Blob bytes`,
		"api.craftgo": `package design
import "shared"
type SearchReq { q shared.Blob @query }
service S { post Do /do { request SearchReq } }`,
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	n := 0
	for i := range diags {
		if diags[i].Code == CodeBindingType {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 binding diagnostic, got %d: %v", n, codes(diags))
	}
}

// An auto-@path field rejects @nullable, `?` and @default; the route always has the segment.
func TestAutoPathFieldDecoratorsRejected(t *testing.T) {
	expectError(t, `type R { id string @nullable }
service S { get G /it/{id} { request R } }`, CodeDecoratorConflict)
	expectError(t, `type R { id string?  name string }
service S { post P /it/{id} { request R } }`, CodeDecoratorConflict)
	expectError(t, `type R { id string @default("x") }
service S { get G /it/{id} { request R } }`, CodeDecoratorConflict)
}

// A plain string auto-@path field is accepted.
func TestAutoPathPlainStringClean(t *testing.T) {
	mustClean(t, `type R { id string }
service S { get G /it/{id} { request R } }`)
}

// An auto-@path field of struct type is rejected.
func TestAutoPathNonBindableRejected(t *testing.T) {
	diags := analyzeOneFile(t, "package p\ntype Inner { a string }\ntype R { id Inner }\ntype Resp { ok bool }\nservice S { get G /u/{id} { request R  response Resp } }")
	if !hasDiagContaining(diags, "@path requires a non-optional") {
		t.Errorf("expected auto-path non-bindable reject, got: %v", diags)
	}
}

// A datetime travels only in a body: a wire binder has no parser for it.
func TestDateTimeIsNotWireBindable(t *testing.T) {
	expectError(t, `type SearchReq { since datetime @query }
service S { post Do /do { request SearchReq } }`, CodeBindingType)
}

// A raw bytes field cannot bind to @query, @header, @path, @cookie or @form.
func TestRawBytesIsNotWireBindable(t *testing.T) {
	for _, binding := range []string{"@query", "@header", "@path", "@cookie", "@form"} {
		expectError(t, `type SearchReq { filter bytes @format(raw) `+binding+` }
service S { post Do /do/{filter} { request SearchReq } }`, CodeBindingType)
	}
}
