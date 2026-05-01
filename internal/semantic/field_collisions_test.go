package semantic

import (
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/internal/lexer"
)

// TestFieldCollisionUserIdAndUserId pins the canonical case: two DSL
// field names normalising to the same Go identifier surface a
// `field/name-collision` warning whose message points at both DSL
// spellings AND the suffixed Go name codegen will actually emit.
// Severity is warning so existing projects with intentional aliases
// keep building.
func TestFieldCollisionUserIdAndUserId(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
type User {
    user_id string
    userId  string
}`))
	d := findCode(diags, CodeFieldNameCollision)
	if d == nil {
		t.Fatalf("expected %s warning, got %v", CodeFieldNameCollision, codes(diags))
	}
	if d.Severity != lexer.SeverityWarning {
		t.Errorf("expected warning severity, got %v", d.Severity)
	}
	for _, want := range []string{`"userId"`, `"user_id"`, `"UserID"`, `"UserID_2"`} {
		if !strings.Contains(d.Msg, want) {
			t.Errorf("warning must mention %s; got %q", want, d.Msg)
		}
	}
}

// TestFieldCollisionFourWayEmitsThreeWarnings pins the per-duplicate
// emit rule. When 4 DSL spellings normalise to the same Go name, the
// first occurrence keeps the canonical Go name and the OTHER three
// each get a dedicated warning anchored at their own position. This
// matters in editor squiggles: every offending field shows up, not
// just the first.
func TestFieldCollisionFourWayEmitsThreeWarnings(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
type T {
    create_user_request string
    create_userRequest  string
    createUserRequest   string
    CreateUserRequest   string
}`))
	hits := 0
	for _, d := range diags {
		if d.Code == CodeFieldNameCollision {
			hits++
		}
	}
	if hits != 3 {
		t.Errorf("expected 3 warnings (one per duplicate beyond the canonical), got %d (%v)", hits, codes(diags))
	}
}

// TestFieldCollisionInsideError pins the same logic on an error
// body — error fields go through the same Go-struct emission path
// so the same dedup applies.
func TestFieldCollisionInsideError(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
error BadRequest Validation {
    user_id string
    userId  string
}`))
	if findCode(diags, CodeFieldNameCollision) == nil {
		t.Fatalf("expected %s warning, got %v", CodeFieldNameCollision, codes(diags))
	}
}

// TestFieldCollisionNoFalsePositive confirms the warning does NOT
// fire on field sets that are genuinely distinct under
// [idents.GoFieldName] — the `firstName` / `last_name` mix is
// unambiguous because the converter title-cases each independently.
func TestFieldCollisionNoFalsePositive(t *testing.T) {
	mustClean(t, `package x
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
