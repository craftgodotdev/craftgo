package semantic

import "testing"

// An auto-promoted @query field (no binding decorator, body-less verb) of a
// multi-dimensional array type must be rejected just like the explicit
// `int[][] @query` form - the depth guard lives in the shared
// isWireBindingType predicate so the auto-@query path catches it too.
func TestAutoQueryMultiDimArrayRejected(t *testing.T) {
	expectError(t, `type Req { grid int[][] }
service S { get Op /x { request Req } }`, CodeBindingType)
}

// A 1-D auto-@query array is fine - only nested arrays are rejected.
func TestAutoQuerySingleDimArrayClean(t *testing.T) {
	mustClean(t, `type Req { tags string[] }
service S { get Op /x { request Req } }`)
}

// A cross-package scalar / enum field carrying `@nullable` that auto-binds to
// @query on a body-less verb (GET/DELETE) must be rejected - `@nullable` lowers
// it to a pointer but the wire binder writes a non-pointer value into it
// (`req.Nul = lib.Email(...)` into a `*lib.Email`), non-compiling. The local
// equivalent is already rejected; this mirrors it for the qualified form (the
// check is structural, so it runs before the qualified-ref deferral).
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

// A cross-package binding error must be reported EXACTLY ONCE. The project
// binding pass used to walk every request body a second time (request types
// are already in pkg.Types), emitting byte-identical duplicate diagnostics.
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

// An auto-@path field (name matches a {segment}, no binding decorator) must
// reject @nullable / optional `?` / @default on any verb - a matched route
// always supplies the segment and the path binder writes a plain string, so
// these either non-compile (pointer mismatch) or are meaningless.
func TestAutoPathFieldDecoratorsRejected(t *testing.T) {
	expectError(t, `type R { id string @nullable }
service S { get G /it/{id} { request R } }`, CodeDecoratorConflict)
	expectError(t, `type R { id string?  name string }
service S { post P /it/{id} { request R } }`, CodeDecoratorConflict)
	expectError(t, `type R { id string @default("x") }
service S { get G /it/{id} { request R } }`, CodeDecoratorConflict)
}

// A normal auto-path string field is fine - the control.
func TestAutoPathPlainStringClean(t *testing.T) {
	mustClean(t, `type R { id string }
service S { get G /it/{id} { request R } }`)
}

// An auto-bound path field with a struct type is rejected; a cross-pkg
// scalar path field must NOT be false-rejected.
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
