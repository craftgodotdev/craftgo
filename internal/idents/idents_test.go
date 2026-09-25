package idents

import (
	"reflect"
	"testing"
)

func TestGoFieldNameTable(t *testing.T) {
	cases := map[string]string{
		"":            "",
		"id":          "ID",
		"firstName":   "FirstName",
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

// TestDedupUserIdVsUserID checks that `user_id` and `userId` collide on
// `UserID` and the second becomes `UserID_2`.
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

// TestDedupThreeWayCollision checks that three names mapping to `UserID` form
// one group, suffixed `_2` and `_3`.
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

// TestDedupOrderStability checks that the first occurrence keeps the bare
// name whichever spelling it uses.
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
		if got := FileName(c.name, FileCaseKebab); got != c.kebab {
			t.Errorf("FileName(%q, kebab) = %q, want %q", c.name, got, c.kebab)
		}
		if got := FileName(c.name, FileCaseSnake); got != c.snake {
			t.Errorf("FileName(%q, snake) = %q, want %q", c.name, got, c.snake)
		}
		if got := FileName(c.name, FileCaseCamel); got != c.camel {
			t.Errorf("FileName(%q, camel) = %q, want %q", c.name, got, c.camel)
		}
		// An empty style is the default case.
		if got := FileName(c.name, ""); got != FileName(c.name, DefaultFileCase) {
			t.Errorf("FileName(%q, \"\") = %q, want %q", c.name, got, FileName(c.name, DefaultFileCase))
		}
		if got := KebabCase(c.name); got != c.kebab {
			t.Errorf("KebabCase(%q) = %q, want %q", c.name, got, c.kebab)
		}
	}
}

func TestFileNameWordsSuffix(t *testing.T) {
	// An appended literal word takes the case's separator.
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

func TestPascalCase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"orders", "Orders"},
		{"user_profile", "UserProfile"},
		{"order-service", "OrderService"},
		{"admin/ops", "AdminOps"},
		{"admin/legacy/v2", "AdminLegacyV2"},
		{"admin//ops", "AdminOps"},
		{"/admin", "Admin"},
		{"trailing_", "Trailing"},
		{"_leading", "Leading"},
		{"_", ""},
		{"Already", "Already"},
		{"v2", "V2"},
		{"a1_b2", "A1B2"},
	}
	for _, c := range cases {
		if got := PascalCase(c.in); got != c.want {
			t.Errorf("PascalCase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestErrorNames(t *testing.T) {
	cases := []struct{ dsl, typeName, bodyName, codeName, ctorName string }{
		{"UserGone", "UserGoneErr", "UserGoneBody", "ErrCodeUserGone", "NewUserGoneErr"},
		{"QuotaErr", "QuotaErr", "QuotaErrBody", "ErrCodeQuotaErr", "NewQuotaErr"},
		{"AuthError", "AuthError", "AuthErrorBody", "ErrCodeAuthError", "NewAuthError"},
	}
	for _, c := range cases {
		if got := ErrorTypeName(c.dsl); got != c.typeName {
			t.Errorf("ErrorTypeName(%q) = %q, want %q", c.dsl, got, c.typeName)
		}
		if got := ErrorBodyName(c.dsl); got != c.bodyName {
			t.Errorf("ErrorBodyName(%q) = %q, want %q", c.dsl, got, c.bodyName)
		}
		if got := ErrorCodeName(c.dsl); got != c.codeName {
			t.Errorf("ErrorCodeName(%q) = %q, want %q", c.dsl, got, c.codeName)
		}
		if got := ErrorConstructorName(c.dsl); got != c.ctorName {
			t.Errorf("ErrorConstructorName(%q) = %q, want %q", c.dsl, got, c.ctorName)
		}
	}
}

func TestEnumConstNames(t *testing.T) {
	got := EnumConstNames("Status", []string{"Active", "active", "on_hold"})
	want := []string{"StatusActive", "StatusActive_2", "StatusOnHold"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EnumConstNames = %v, want %v", got, want)
	}
}

func TestEventContractName(t *testing.T) {
	if got := EventContractName("OrderPlaced"); got != "OrderPlacedContract" {
		t.Errorf("EventContractName = %q, want OrderPlacedContract", got)
	}
}
