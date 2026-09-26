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

// A field whose Go name a generated member takes is rejected: the Validate
// and FillEmpty methods of every struct holding a body, and an error type's
// body struct.
func TestGeneratedMemberFieldNameRejected(t *testing.T) {
	for label, c := range map[string]struct{ src, msg string }{
		"type":         {"type T { validate string  x int }", `type T field "validate" maps to the Go name "Validate"`},
		"generic type": {"type Box<T> { validate T  x int }", `type Box field "validate" maps to the Go name "Validate"`},
		"error body":   {"error Internal E { validate string  x int }", `error E field "validate" maps to the Go name "Validate"`},
		"fill method":  {"type T { fillEmpty string  x int }", `type T field "fillEmpty" maps to the Go name "FillEmpty"`},
		"error body struct": {"error Internal Gone { goneBody string  why string }",
			`error Gone field "goneBody" maps to the Go name "GoneBody"`},
		"error body struct through a mixin": {"type Mx { goneBody string }\nerror Internal Gone { Mx  why string }",
			`error Gone field "goneBody", from mixin Mx, maps to the Go name "GoneBody"`},
		"mixin named like the method": {"type Validate { x int }\ntype Host { Validate  y int }",
			`type Host mixin Validate embeds as the Go field "Validate"`},
		"error method through a mixin of its name": {"type Error { error string @header(\"X-Error\") }\nerror BadRequest Oops { Error }",
			`error Oops field "error", from mixin Error, maps to the Go name "Error"`},
		"error method through a nested mixin": {"type Inner { errCode string @cookie(\"c\") }\ntype ErrCode { Inner }\nerror BadRequest Oops { ErrCode }",
			`error Oops field "errCode", from mixin ErrCode, maps to the Go name "ErrCode"`},
	} {
		t.Run(label, func(t *testing.T) {
			d := expectError(t, c.src, CodeInvalidGoName)
			expectMessage(t, d, c.msg)
			expectCodeCount(t, c.src, CodeInvalidGoName, 1)
		})
	}
}

// A mixin's field named like the method its own struct gets is reported
// once, where the mixin declares it; a field the Go names of its struct
// dedupe away from the method's name is accepted.
func TestGeneratedMemberFieldNameReportedOnce(t *testing.T) {
	src := "type Mx { validate string }\ntype Host { Mx  x int }\nerror Internal E { Mx  y int }"
	d := expectError(t, src, CodeInvalidGoName)
	if d.Pos.Line != 1 {
		t.Errorf("reported at line %d, want the mixin's field on line 1", d.Pos.Line)
	}
	expectCodeCount(t, src, CodeInvalidGoName, 1)
	expectCodeCount(t, "type T { validate string  Validate string }", CodeInvalidGoName, 1)
}

// A mixin named like an error method embeds into an error's body: the error
// type reads the fields it brings, never the mixin by name.
func TestErrorMixinNamedLikeAnErrorMethodClean(t *testing.T) {
	mustClean(t, "type Error { code string  message string }\nerror BadRequest Invalid { Error  field string }")
}

// An ordinary error header field is accepted.
func TestErrorWireFieldClean(t *testing.T) {
	expectNoCode(t, `error Internal E { traceId string @header("X-Trace")  detail string }`, CodeInvalidGoName)
}
