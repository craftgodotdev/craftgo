package semantic

import (
	"strings"
	"testing"
)

// A no-content success status on a body-returning method must be rejected.
func TestNoContentStatusWithBodyRejected(t *testing.T) {
	src := `package p
type Out { ok bool }
type Req { id string @path }
service S {
  @status(204)
  get G /things/{id} { request Req  response Out }
}`
	diags := analyzeOneFile(t, src)
	if !hasDiagContaining(diags, "no-content status and cannot carry a response body") {
		t.Errorf("expected @status(204)+body reject, got: %v", diags)
	}
}

// A bare scalar/enum request type has no fields to bind/decode - reject it.
func TestBareScalarEnumRequestRejected(t *testing.T) {
	for _, src := range []string{
		"package p\nscalar Token string\nservice S { post Do /do { request Token  response Token } }",
		"package p\nenum Color { red green }\nservice S { post Do /do { request Color  response Color } }",
	} {
		diags := analyzeOneFile(t, src)
		if !hasDiagContaining(diags, "has no fields to bind or decode") {
			t.Errorf("expected bare scalar/enum request reject for %q, got: %v", strings.TrimSpace(src), diags)
		}
	}
}

// A built-in primitive in `request` or `response` names no generated
// type: the transport would declare `var req types.string` and call a
// Validate() nothing emits, the stub would return `(*types.string,
// error)`, and the OpenAPI body would $ref a `#/components/schemas/
// string` the document never declares. Every primitive spelling is
// rejected in both clauses.
func TestBuiltinPrimitiveClauseRejected(t *testing.T) {
	for _, prim := range []string{"string", "int", "int64", "float64", "bool", "bytes", "any", "datetime", "file"} {
		t.Run("request "+prim, func(t *testing.T) {
			d := expectDiag(t, "package p\ntype Ok { v string }\nservice S { post Do /do { request "+prim+"  response Ok } }", CodeBindingType)
			want := "request type \"" + prim + "\" is a built-in primitive, which has no fields to bind or decode as a request body - wrap it in a type (`type Req { value " + prim + " }`)"
			if d.Msg != want {
				t.Errorf("message = %q, want %q", d.Msg, want)
			}
		})
		t.Run("response "+prim, func(t *testing.T) {
			d := expectDiag(t, "package p\ntype Ok { v string }\nservice S { post Do /do { request Ok  response "+prim+" } }", CodeBindingType)
			want := "response type \"" + prim + "\" is a built-in primitive, which names no generated type to encode as a response body - wrap it in a type (`type Resp { value " + prim + " }`)"
			if d.Msg != want {
				t.Errorf("message = %q, want %q", d.Msg, want)
			}
		})
	}
}

// A raw side makes the clause docs-only for the TRANSPORT, but the
// OpenAPI document is still emitted from it - so a primitive there still
// produces a dangling `$ref` and is still rejected, exactly as the
// parser's bare-array reject fires under the same flags.
func TestBuiltinPrimitiveClauseRejectedOnRawSides(t *testing.T) {
	for _, c := range []struct{ label, src string }{
		{"@rawRequest request", "package p\ntype Ok { v string }\nservice S { @rawRequest post Do /do { request string  response Ok } }"},
		{"@rawResponse response", "package p\ntype Ok { v string }\nservice S { @rawResponse post Do /do { request Ok  response string } }"},
		{"@passthrough request", "package p\ntype Ok { v string }\nservice S { @passthrough post Do /do { request string  response Ok } }"},
		{"@passthrough response", "package p\ntype Ok { v string }\nservice S { @passthrough post Do /do { request Ok  response string } }"},
	} {
		t.Run(c.label, func(t *testing.T) {
			expectDiag(t, c.src, CodeBindingType)
		})
	}
}

// The response side takes a scalar and an enum. Nothing binds a
// response, and both generate a real named Go type whose OpenAPI schema
// IS emitted - so the reject that covers them on the request side must
// not reach across.
func TestScalarAndEnumResponseAccepted(t *testing.T) {
	for _, c := range []struct{ label, src string }{
		{"scalar", "package p\nscalar Token string\ntype Req { v string }\nservice S { post Do /do { request Req  response Token } }"},
		{"enum", "package p\nenum Color { red green }\ntype Req { v string }\nservice S { post Do /do { request Req  response Color } }"},
	} {
		t.Run(c.label, func(t *testing.T) {
			expectNoDiags(t, analyzeOneFile(t, c.src))
		})
	}
}

// A QUALIFIED reference never names a built-in, so the primitive reject
// must not fire on one whose final segment happens to be spelled like a
// primitive - that is an unresolved-symbol case for the reference pass.
func TestQualifiedClauseRefIsNotABuiltin(t *testing.T) {
	src := "package p\ntype Ok { v string }\nservice S { post Do /do { request other.string  response Ok } }"
	for _, d := range analyzeOneFile(t, src) {
		if d.Code == CodeBindingType {
			t.Errorf("qualified ref reported as a built-in primitive: %v", d)
		}
	}
}

// @status(205) (Reset Content) with a response body is rejected.
func TestStatus205WithBodyRejected(t *testing.T) {
	src := `package p
type Req { n string }
type Resp { ok bool }
service S {
  @status(205)
  post M /m { request Req  response Resp }
}`
	diags := analyzeOneFile(t, src)
	if !hasDiagContaining(diags, "no-content status and cannot carry a response body") {
		t.Errorf("expected @status(205)+body reject, got: %v", diags)
	}
}
