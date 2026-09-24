package semantic

import (
	"strings"
	"testing"
)

// A @status(204) method with a response body is rejected.
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

// A bare scalar or enum request type is rejected.
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

// A built-in primitive as the request or response type is rejected.
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

// A built-in primitive clause is rejected on a raw side too; the OpenAPI still documents it.
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

// A scalar or enum response type is accepted.
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

// A qualified clause type spelt like a primitive (`other.string`) is not a built-in.
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
