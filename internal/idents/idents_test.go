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
// duplicate. Generated code remains stable for already-published
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

func TestFileName(t *testing.T) {
	cases := []struct{ name, kebab, snake, camel string }{
		{"CreateUser", "create-user", "create_user", "createUser"},
		{"UserService", "user-service", "user_service", "userService"},
		{"ListV2Items", "list-v2items", "list_v2items", "listV2items"},
		{"ping", "ping", "ping", "ping"},
	}
	for _, c := range cases {
		if got := FileName(c.name, "kebab"); got != c.kebab {
			t.Errorf("FileName(%q, kebab) = %q, want %q", c.name, got, c.kebab)
		}
		if got := FileName(c.name, "snake"); got != c.snake {
			t.Errorf("FileName(%q, snake) = %q, want %q", c.name, got, c.snake)
		}
		if got := FileName(c.name, "camel"); got != c.camel {
			t.Errorf("FileName(%q, camel) = %q, want %q", c.name, got, c.camel)
		}
		// An empty/unknown style falls back to kebab, byte-identical to KebabCase.
		if got := FileName(c.name, ""); got != KebabCase(c.name) {
			t.Errorf("FileName(%q, \"\") = %q, want KebabCase %q", c.name, got, KebabCase(c.name))
		}
	}
}

func TestFileNameWordsSuffix(t *testing.T) {
	// The middleware file appends a literal "middleware" word so the separator
	// between the name and the suffix follows the chosen case.
	words := append(SplitFieldName("AuthRequired"), "middleware")
	want := map[string]string{
		"kebab": "auth-required-middleware",
		"snake": "auth_required_middleware",
		"camel": "authRequiredMiddleware",
	}
	for style, exp := range want {
		if got := FileNameWords(style, words); got != exp {
			t.Errorf("FileNameWords(%q) = %q, want %q", style, got, exp)
		}
	}
}
