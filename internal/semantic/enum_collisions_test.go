package semantic

import "testing"

// TestEnumValueCollisionCreatedVsCreated pins the canonical
// case-flip collision: `created` and `Created` both normalise to
// the const `<Enum>Created`. Wire payloads stay distinct (`"okok"`
// vs `"okok1"`) but the Go const would clash; codegen suffixes the
// later occurrence and we surface a warning so the user sees it.
func TestEnumValueCollisionCreatedVsCreated(t *testing.T) {
	d := expectWarning(t, `package x
enum TaskStatus {
    Open    = "open"
    created = "okok"
    Created = "okok1"
}`, CodeEnumValueCollision)
	expectMessage(t, d, `"Created"`, `"created"`, `TaskStatusCreated`, `TaskStatusCreated_2`)
}

// TestEnumValueCollisionThreeWayEmitsTwoWarnings pins per-duplicate
// emit: 3 colliding values → 2 warnings (first keeps the canonical
// const name, the other two each get squiggled).
func TestEnumValueCollisionThreeWayEmitsTwoWarnings(t *testing.T) {
	expectCodeCount(t, `package x
enum E {
    user_id = "a"
    userId  = "b"
    USER_ID = "c"
}`, CodeEnumValueCollision, 2)
}

// TestEnumValueCollisionNoFalsePositive confirms a clean enum with
// genuinely distinct value names produces no warning.
func TestEnumValueCollisionNoFalsePositive(t *testing.T) {
	expectClean(t, `package x
enum Color { Red Green Blue }`)
}
