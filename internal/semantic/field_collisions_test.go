package semantic

import (
	"testing"
)

// TestFieldCollisionUserIdAndUserId pins the canonical case: two DSL
// field names normalise to the same Go identifier under
// [idents.GoFieldName]. The warning carries both DSL spellings AND
// the suffixed Go name codegen will actually emit.
func TestFieldCollisionUserIdAndUserId(t *testing.T) {
	d := expectWarning(t, `package x
type User {
    user_id string
    userId  string
}`, CodeFieldNameCollision)
	expectMessage(t, d, `"userId"`, `"user_id"`, `"UserID"`, `"UserID_2"`)
}

// TestFieldCollisionFourWayEmitsThreeWarnings pins the per-duplicate
// emit rule. When 4 DSL spellings normalise to the same Go name, the
// first occurrence keeps the canonical Go name and the OTHER three
// each get a dedicated warning anchored at their own position.
func TestFieldCollisionFourWayEmitsThreeWarnings(t *testing.T) {
	expectCodeCount(t, `package x
type T {
    create_user_request string
    create_userRequest  string
    createUserRequest   string
    CreateUserRequest   string
}`, CodeFieldNameCollision, 3)
}

// TestFieldCollisionInsideError pins the same logic on an error
// body - error fields go through the same Go-struct emission path
// so the same dedup applies.
func TestFieldCollisionInsideError(t *testing.T) {
	expectWarning(t, `package x
error BadRequest Validation {
    user_id string
    userId  string
}`, CodeFieldNameCollision)
}

// TestFieldCollisionNoFalsePositive confirms the warning does NOT
// fire on field sets that are genuinely distinct under
// [idents.GoFieldName] - `firstName` / `last_name` are unambiguous
// because the converter title-cases each independently.
func TestFieldCollisionNoFalsePositive(t *testing.T) {
	expectClean(t, `package x
type User {
    firstName string
    last_name string
    email     string
}`)
}

// TestFieldCollisionEmptyNameSkipped guards against parser-recovery
// artefacts: an empty field name (placeholder for "user typed
// nothing") must not be treated as colliding with another empty-named
// field. Pure noise reporting otherwise.
func TestFieldCollisionEmptyNameSkipped(t *testing.T) {
	a := &analyzer{pkg: &Package{}}
	a.warnFieldCollisions("type Foo", nil)
	for _, d := range a.diags {
		if d.Code == CodeFieldNameCollision {
			t.Errorf("empty member list must not produce collision warning; got %q", d.Msg)
		}
	}
}

// A field name that normalises to an invalid Go identifier - empty (`_`, `__`)
// or digit-leading (`_2`) - is rejected rather than silently producing
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
