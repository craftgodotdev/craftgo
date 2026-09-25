package semantic

import (
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/internal/ast"
)

// Two field names with one Go name warn, naming both spellings and the suffixed Go name.
func TestFieldCollisionUserIdAndUserId(t *testing.T) {
	d := expectWarning(t, `package x
type User {
    user_id string
    userId  string
}`, CodeFieldNameCollision)
	expectMessage(t, d, `"userId"`, `"user_id"`, `"UserID"`, `"UserID_2"`)
}

// Four field names with one Go name give three warnings, one per later field.
func TestFieldCollisionFourWayEmitsThreeWarnings(t *testing.T) {
	expectCodeCount(t, `package x
type T {
    create_user_request string
    create_userRequest  string
    createUserRequest   string
    CreateUserRequest   string
}`, CodeFieldNameCollision, 3)
}

// Error body fields with one Go name warn too.
func TestFieldCollisionInsideError(t *testing.T) {
	expectWarning(t, `package x
error BadRequest Validation {
    user_id string
    userId  string
}`, CodeFieldNameCollision)
}

// Field names with distinct Go names do not warn.
func TestFieldCollisionNoFalsePositive(t *testing.T) {
	mustClean(t, `package x
type User {
    firstName string
    last_name string
    email     string
}`)
}

// A nameless field, as a parse error leaves it, takes part in no collision.
func TestFieldCollisionEmptyNameSkipped(t *testing.T) {
	a := newTestAnalyzer(&Package{})
	a.warnFieldCollisions("type Foo", []ast.TypeMember{&ast.Field{}, &ast.Field{}})
	if d := findCode(a.diags, CodeFieldNameCollision); d != nil {
		t.Errorf("two nameless fields collide: %q", d.Msg)
	}
}

// A field name whose Go name is empty or starts with a digit (`_`, `_2`) is rejected.
func TestInvalidGoFieldNameRejected(t *testing.T) {
	for _, name := range []string{"_2", "_1_2", "_", "__"} {
		expectError(t, "type R { "+name+" string  x string }", CodeInvalidGoName)
	}
}

// A leading underscore before a letter (`_foo` → `Foo`) is accepted.
func TestLeadingUnderscoreFieldClean(t *testing.T) {
	expectNoCode(t, "type R { _foo string  x string }", CodeInvalidGoName)
}

// An error field whose Go name matches an error method (ErrCode, Error, HTTPStatus, MarshalJSON) is rejected.
func TestErrorReservedFieldNameRejected(t *testing.T) {
	for _, name := range []string{"errCode", "error", "httpStatus", "marshalJSON"} {
		expectError(t, "error Internal E { "+name+" string @header(\"X-E\")  detail string }", CodeInvalidGoName)
	}
}

// A field a mixin brings into an error, directly or through a nested mixin,
// is held to the error method names, reported at the error's mixin.
func TestErrorReservedFieldNameThroughMixinRejected(t *testing.T) {
	for _, name := range []string{"errCode", "error", "httpStatus", "marshalJSON", "writeResponseHeaders"} {
		for label, decls := range map[string]string{
			"mixin":        "type Mx { " + name + " string @header(\"X-M\") }\n",
			"nested mixin": "type Inner { " + name + " string }\ntype Mx { Inner  n int }\n",
		} {
			src := decls + "error Conflict Clash {\n\tMx\n\treason string\n}"
			d := expectError(t, src, CodeInvalidGoName)
			expectMessage(t, d, "error Clash field \""+name+"\", from mixin Mx,")
			if want := strings.Count(decls, "\n") + 2; d.Pos.Line != want {
				t.Errorf("%s %s: reported at line %d, want the mixin's line %d", label, name, d.Pos.Line, want)
			}
		}
	}
}

// An ordinary error header field is accepted.
func TestErrorWireFieldClean(t *testing.T) {
	expectNoCode(t, `error Internal E { traceId string @header("X-Trace")  detail string }`, CodeInvalidGoName)
}
