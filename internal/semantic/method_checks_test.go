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
