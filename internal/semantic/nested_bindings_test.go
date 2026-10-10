package semantic

import (
	"strings"
	"testing"
)

// A binding decorator on a field that a request, a response or an error body
// holds only as a nested value is reported once, at the field; one a mixin
// brings to the top level binds.
func TestBindingOnANestedFieldIsReported(t *testing.T) {
	for name, c := range map[string]struct {
		src, want string
	}{
		"request": {
			"type Inner { q string @query  h string @header(\"X-H\") }\ntype R { inner Inner  name string }\nservice S { post Do /do { request R } }",
			"field Inner.q binds @query, but R.inner holds it as a nested value",
		},
		"array element": {
			"type Inner { c string @cookie }\ntype R { items Inner[] }\nservice S { post Do /do { request R } }",
			"field Inner.c binds @cookie, but R.items holds it as a nested value",
		},
		"generic argument": {
			"type Inner { id string @path }\ntype Page<T> { items T[] }\nservice S { post Do /do { request Page<Inner> } }",
			"field Inner.id binds @path, but Page<Inner>.items holds it as a nested value",
		},
		"response": {
			"type Meta { total int @header(\"X-Total\") }\ntype Resp { meta Meta }\nservice S { get Do /do { response Resp } }",
			"field Meta.total binds @header, but Resp.meta holds it as a nested value",
		},
		"error body": {
			"type Hint { retry int @header(\"Retry-After\") }\nerror TooManyRequests Slow { hint Hint }",
			"field Hint.retry binds @header, but Slow.hint holds it as a nested value",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, diags := Analyze(parseFiles(t, c.src))
			var got []string
			for _, d := range diags {
				if d.Code == CodeBindingNested {
					got = append(got, d.Msg)
				}
			}
			if len(got) == 0 || !strings.HasPrefix(got[0], c.want) {
				t.Fatalf("binding/nested diagnostics = %q, want the first to start %q", got, c.want)
			}
		})
	}
	expectCodeCount(t, "type Inner { q string @query }\ntype R { a Inner  b Inner }\nservice S {\n\tpost A /a { request R }\n\tpost B /b { request R }\n}", CodeBindingNested, 1)
	for _, src := range []string{
		"type Base { q string @query }\ntype R { Base  name string }\nservice S { post Do /do { request R } }",
		"type R { q string @query  h string @header(\"X-H\") }\nservice S { post Do /do { request R } }",
		"type Resp { total int @header(\"X-Total\") }\nservice S { get Do /do { response Resp } }",
		"type Inner { q string }\ntype R { inner Inner }\nservice S { post Do /do { request R } }",
	} {
		expectNoCode(t, src, CodeBindingNested)
	}
}
