package semantic

import "testing"

// Enum values with one Go constant (`created`, `Created`) warn, naming the suffixed constant.
func TestEnumValueCollisionCreatedVsCreated(t *testing.T) {
	d := expectWarning(t, `package x
enum TaskStatus {
    Open    = "open"
    created = "okok"
    Created = "okok1"
}`, CodeEnumValueCollision)
	expectMessage(t, d, `"Created"`, `"created"`, `TaskStatusCreated`, `TaskStatusCreated_2`)
}

// Three enum values with one Go constant give two warnings.
func TestEnumValueCollisionThreeWayEmitsTwoWarnings(t *testing.T) {
	expectCodeCount(t, `package x
enum E {
    user_id = "a"
    userId  = "b"
    USER_ID = "c"
}`, CodeEnumValueCollision, 2)
}

// Distinct enum value names do not warn.
func TestEnumValueCollisionNoFalsePositive(t *testing.T) {
	expectClean(t, `package x
enum Color { Red Green Blue }`)
}
