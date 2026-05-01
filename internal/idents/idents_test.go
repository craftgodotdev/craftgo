package idents

import (
	"reflect"
	"testing"
)

func TestGoFieldNameTable(t *testing.T) {
	cases := map[string]string{
		"":            "",
		"name":        "Name",
		"Name":        "Name",
		"user_id":     "UserID",
		"userId":      "UserID",
		"USER_ID":     "UserID", // USER → User (title-case), ID stays as initialism
		"http":        "HTTP",
		"http_url":    "HTTPURL",
		"my_id":       "MyID",
		"DBError":     "DBError",
		"HTTPRequest": "HTTPRequest",
	}
	for in, want := range cases {
		got := GoFieldName(in)
		if got != want {
			t.Errorf("GoFieldName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDedupNoCollision(t *testing.T) {
	resolved, collisions := DedupGoFieldNames([]string{"name", "email", "age"})
	if !reflect.DeepEqual(resolved, []string{"Name", "Email", "Age"}) {
		t.Errorf("got %v", resolved)
	}
	if len(collisions) != 0 {
		t.Errorf("expected no collisions, got %v", collisions)
	}
}

// TestDedupUserIdVsUserID pins the canonical example: `user_id` and
// `userId` both map to `UserID`. The first occurrence keeps the bare
// Go name; the second is suffixed `_2` so the struct compiles. The
// collision record carries both DSL spellings so callers can warn.
func TestDedupUserIdVsUserID(t *testing.T) {
	resolved, collisions := DedupGoFieldNames([]string{"user_id", "userId"})
	want := []string{"UserID", "UserID_2"}
	if !reflect.DeepEqual(resolved, want) {
		t.Errorf("resolved = %v, want %v", resolved, want)
	}
	if len(collisions) != 1 {
		t.Fatalf("expected 1 collision group, got %d", len(collisions))
	}
	c := collisions[0]
	if c.CanonicalGoName != "UserID" {
		t.Errorf("canonical = %q", c.CanonicalGoName)
	}
	if !reflect.DeepEqual(c.DSLNames, []string{"user_id", "userId"}) {
		t.Errorf("DSL names = %v", c.DSLNames)
	}
	if !reflect.DeepEqual(c.ResolvedGoNames, []string{"UserID", "UserID_2"}) {
		t.Errorf("resolved = %v", c.ResolvedGoNames)
	}
}

// TestDedupThreeWayCollision pins the suffix sequencing - second
// duplicate gets `_2`, third gets `_3`, etc. The bare canonical is
// reserved for the first occurrence regardless of which DSL spelling
// appeared first in source. All three of `user_id`, `userId`, and
// `USER_ID` normalise to `UserID` under [GoFieldName] (the title-case
// + initialism rules collapse case differences in the input parts),
// so the trio collides as a single group.
func TestDedupThreeWayCollision(t *testing.T) {
	resolved, collisions := DedupGoFieldNames([]string{"user_id", "userId", "USER_ID"})
	want := []string{"UserID", "UserID_2", "UserID_3"}
	if !reflect.DeepEqual(resolved, want) {
		t.Errorf("resolved = %v, want %v", resolved, want)
	}
	if len(collisions) != 1 {
		t.Fatalf("expected 1 collision group of 3, got %d", len(collisions))
	}
	if len(collisions[0].DSLNames) != 3 {
		t.Errorf("collision should record all 3 DSL spellings, got %v", collisions[0].DSLNames)
	}
}

// TestDedupOrderStability pins the rule that the FIRST occurrence
// keeps the bare canonical Go name even when the user later adds a
// duplicate. Generated code remains stable for previously-published
// struct shapes - adding a colliding alias does not retroactively
// rename the original field.
func TestDedupOrderStability(t *testing.T) {
	resolved, _ := DedupGoFieldNames([]string{"userId", "user_id"})
	if resolved[0] != "UserID" {
		t.Errorf("first occurrence must keep canonical name; got %q", resolved[0])
	}
	if resolved[1] != "UserID_2" {
		t.Errorf("second occurrence must take the suffix; got %q", resolved[1])
	}
}
