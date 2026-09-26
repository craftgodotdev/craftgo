package semantic

import (
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

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

// PathParam names the route variable a request field binds without an error:
// its @path name, else its own when no other binding claims it.
func TestPathParam(t *testing.T) {
	proj, _ := AnalyzeProject(parseFiles(t, `enum Kind { A }
scalar Slug string
type Req {
	ok string
	kind Kind
	slug Slug
	code string @path("sku")
	id string @nullable
	sec string @sensitive
	n int @default(3)
	opt string?
	tags string[]
	q string @query
}`), Options{})
	want := map[string]string{"ok": "ok", "kind": "kind", "slug": "slug", "code": "sku"}
	for _, f := range ast.Fields(proj.Packages["test"].Types["Req"].Body) {
		name, ok := proj.PathParam("test", f)
		if ok != (want[f.Name] != "") || ok && name != want[f.Name] {
			t.Errorf("%s: PathParam = %q, %v; want %q", f.Name, name, ok, want[f.Name])
		}
	}
}

// A plain string auto-@path field is accepted.
func TestAutoPathPlainStringClean(t *testing.T) {
	mustClean(t, `type R { id string }
service S { get G /it/{id} { request R } }`)
}

// An auto-@path field of struct type is rejected.
func TestAutoPathNonBindableRejected(t *testing.T) {
	expectMsg(t, "@path requires a non-optional", "package p\ntype Inner { a string }\ntype R { id Inner }\ntype Resp { ok bool }\nservice S { get G /u/{id} { request R  response Resp } }")
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

// Beside a `file`, every body field rides a multipart form part, which carries
// only a wire-bindable value or a single-level array of one; a raw request is
// not bound.
func TestMultipartTextPartTypes(t *testing.T) {
	const head = "package app\ntype Box<T> { v T }\ntype Meta { x int }\nscalar Blob bytes\ntype Resp { ok bool }\n"
	for label, c := range map[string]struct{ types, request string }{
		"2-D array":       {`type R { f file  tags string[][] }`, "R"},
		"2-D array @body": {`type R { f file  tags string[][] @body }`, "R"},
		"2-D array mixin": {`type M { grid int[][] }
type R { f file  M }`, "R"},
		"struct":            {`type R { f file  meta Meta }`, "R"},
		"struct @body":      {`type R { f file  meta Meta @body }`, "R"},
		"struct array":      {`type R { f file  metas Meta[] }`, "R"},
		"map":               {`type R { f file  m map<string, string> }`, "R"},
		"generic":           {`type R { f file  b Box<string> }`, "R"},
		"any":               {`type R { f file  x any }`, "R"},
		"bytes":             {`type R { f file  x bytes }`, "R"},
		"raw bytes":         {`type R { f file  x bytes @format(raw) }`, "R"},
		"datetime":          {`type R { f file  at datetime }`, "R"},
		"scalar over bytes": {`type R { f file  x Blob }`, "R"},
		"type argument":     {`type Up<T> { f file  x T }`, "Up<Meta>"},
	} {
		t.Run(label, func(t *testing.T) {
			d := expectError(t, head+c.types+"\nservice S { post A /a { request "+c.request+"  response Resp } }", CodeBindingType)
			expectMessage(t, d, "multipart form part")
		})
	}
	mustClean(t, head+`enum Color { Red Blue }
scalar Email string @format(email)
type R { f file  a string  b int?  c Color  d Email[]  e float64[]?  g bool @form("gg")  h int @query }
service S { post A /a { request R  response Resp } }`)
	mustClean(t, head+`type R { f file  meta Meta  tags string[][] }
service S { @rawRequest post A /a { request R  response Resp } }`)
}

// An optional type parameter instantiated with an array is a pointer to a
// slice, which neither the query nor the multipart form binder can fill; a
// JSON body carries it, and a non-optional parameter binds as the slice.
func TestOptionalTypeParamOverArrayRefusedOnTheWire(t *testing.T) {
	d := expectError(t, `package app
type Box<T> { a T? }
type R1 { Box<string[]> }
type Resp { ok bool }
service S { get A /a { request R1  response Resp } }`, CodeBindingType)
	expectMessage(t, d, "R1.a", "auto-binds to @query", "optional type parameter over an array", "drop the `?`")
	d = expectError(t, `package app
type Box<T> { a T? }
type R1 { Box<string[]>  f file @form }
type Resp { ok bool }
service S { post A /a { request R1  response Resp } }`, CodeBindingType)
	expectMessage(t, d, "R1.a", "multipart form part", "optional type parameter over an array", "drop the `?`")
	mustClean(t, `package app
type Box<T> { a T? }
type R1 { Box<string[]>  b int? }
type Resp { ok bool }
service S { post A /a { request R1  response Resp } }`)
	mustClean(t, `package app
type Box<T> { a T }
type R1 { Box<string[]> }
type Resp { ok bool }
service S { get A /a { request R1  response Resp } }`)
}

// An optional type parameter instantiated with a `file` or a `file[]` is a
// pointer to the file header or to the slice, which the multipart binder
// cannot fill; a raw request is not bound, and a non-optional parameter
// binds as the file.
func TestOptionalTypeParamOverFileRefused(t *testing.T) {
	const head = "package app\ntype O<T> { f T?  n string }\ntype P<T> { f T  n string }\ntype R { O<file[]>  id string }\ntype Resp { ok bool }\n"
	for label, c := range map[string]struct{ request, field, arg string }{
		"file":       {"O<file>", "O.f", "(file)"},
		"file array": {"O<file[]>", "O.f", "(file[])"},
		"mixin":      {"R", "R.f", "(file[])"},
	} {
		t.Run(label, func(t *testing.T) {
			src := head + "service S { post A /a { request " + c.request + "  response Resp } }"
			d := expectError(t, src, CodeBindingType)
			expectMessage(t, d, "field "+c.field+":", "multipart file part", "optional type parameter over a file "+c.arg, "drop the `?`")
			expectCodeCount(t, src, CodeBindingType, 1)
		})
	}
	mustClean(t, head+`service S {
	@rawRequest post A /a { request O<file>  response Resp }
	post B /b { request P<file>  response Resp }
	post C /c { request P<file[]>  response Resp }
}`)
	src := "package app\ntype F<T> { f T? @form(\"upload\")  n string }\ntype Resp { ok bool }\nservice S { post A /a { request F<file>  response Resp } }"
	d := expectError(t, src, CodeBindingType)
	expectMessage(t, d, "@form requires")
	expectCodeCount(t, src, CodeBindingType, 1)
}
