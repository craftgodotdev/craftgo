package semantic

import (
	"fmt"
	"slices"
	"testing"
)

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

// Each value sharing a Go constant with an earlier one is reported where it
// stands and related to the first, exact duplicates included.
func TestEnumValueCollisionRelatesTheFirstValue(t *testing.T) {
	_, diags := Analyze(parseFiles(t, "enum E { A  B  A  A }"))
	var got []string
	for _, d := range diags {
		if d.Code != CodeEnumValueCollision {
			continue
		}
		related := 0
		if len(d.Related) == 1 {
			related = d.Related[0].Pos.Column
		}
		got = append(got, fmt.Sprintf("%d->%d", d.Pos.Column, related))
	}
	if want := []string{"16->10", "19->10"}; !slices.Equal(got, want) {
		t.Errorf("collisions at column->related %v, want %v", got, want)
	}
}

// Distinct enum value names do not warn.
func TestEnumValueCollisionNoFalsePositive(t *testing.T) {
	mustClean(t, `package x
enum Color { Red Green Blue }`)
}
