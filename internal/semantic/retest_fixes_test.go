package semantic

import "testing"

// Regression tests for the re-test workflow's confirmed edge bugs.

// A field name that normalises to an invalid Go identifier — empty (`_`, `__`)
// or digit-leading (`_2`) — is rejected rather than silently producing
// uncompilable / unexported Go.
func TestInvalidGoFieldNameRejected(t *testing.T) {
	for _, name := range []string{"_2", "_1_2", "_", "__"} {
		expectError(t, "type R { "+name+" string  x string }", CodeInvalidGoName)
	}
}

// A leading underscore that still leaves a letter (`_foo` → `Foo`) is valid.
func TestLeadingUnderscoreFieldClean(t *testing.T) {
	expectNoCode(t, "type R { _foo string  x string }", CodeInvalidGoName)
}

// Two fields bound to case-variant HTTP header names (`X-Trace` / `x-trace`)
// collide — net/http canonicalises both to one header.
func TestDuplicateWireNameHeaderCase(t *testing.T) {
	expectError(t, `type R { a string @header("X-Trace")  b string @header("x-trace") }`, CodeDuplicateWireName)
}

// A wire binding promoted through a same-package mixin collides with a re-bind
// of the same name in the host body.
func TestDuplicateWireNameMixin(t *testing.T) {
	expectError(t, `type Base { a string @query("q") }
type R { Base  b string @query("q") }`, CodeDuplicateWireName)
}

// An undecorated field auto-binding to a {segment} path collides with an
// explicit @path of the same name on a sibling.
func TestDuplicateAutoPathWireName(t *testing.T) {
	expectError(t, `type R { id string  other string @path("id") }
service S { get G /g/{id} { request R } }`, CodeDuplicateWireName)
}

// An undecorated field auto-binding to @query (body-less verb) collides with
// an explicit @query of the same name.
func TestDuplicateAutoQueryWireName(t *testing.T) {
	expectError(t, `type R { sort string  order string @query("sort") }
type Resp { ok bool }
service S { get G /g { request R  response Resp } }`, CodeDuplicateWireName)
}

// Control: distinct wire names are clean.
func TestDistinctWireNamesClean(t *testing.T) {
	expectNoCode(t, `type R { sortBy string @query("sortBy")  order string @query("order") }
type Resp { ok bool }
service S { get G /g { request R  response Resp } }`, CodeDuplicateWireName)
}

// An error body field whose Go name collides with a generated error method
// (errCode→ErrCode, error→Error, httpStatus→HTTPStatus) is rejected.
func TestErrorReservedFieldNameRejected(t *testing.T) {
	for _, name := range []string{"errCode", "error", "httpStatus"} {
		expectError(t, "error Internal E { "+name+" string @header(\"X-E\")  detail string }", CodeInvalidGoName)
	}
}

// Control: a normal error wire field is clean.
func TestErrorWireFieldClean(t *testing.T) {
	expectNoCode(t, `error Internal E { traceId string @header("X-Trace")  detail string }`, CodeInvalidGoName)
}
