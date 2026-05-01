package semantic

import (
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/internal/lexer"
)

// TestEnumValueCollisionCreatedVsCreated pins the canonical
// case-flip collision: `created` and `Created` both normalise to
// the const `<Enum>Created`. Wire payloads stay distinct (`"okok"`
// vs `"okok1"`) but the Go const would clash; codegen suffixes the
// later occurrence and we surface a warning so the user sees it.
func TestEnumValueCollisionCreatedVsCreated(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
enum TaskStatus {
    Open    = "open"
    created = "okok"
    Created = "okok1"
}`))
	d := findCode(diags, CodeEnumValueCollision)
	if d == nil {
		t.Fatalf("expected %s warning, got %v", CodeEnumValueCollision, codes(diags))
	}
	if d.Severity != lexer.SeverityWarning {
		t.Errorf("expected warning severity, got %v", d.Severity)
	}
	for _, want := range []string{`"Created"`, `"created"`, `TaskStatusCreated`, `TaskStatusCreated_2`} {
		if !strings.Contains(d.Msg, want) {
			t.Errorf("warning must mention %s; got %q", want, d.Msg)
		}
	}
}

// TestEnumValueCollisionThreeWayEmitsTwoWarnings pins the
// per-duplicate emit behaviour: when 3 values collide, the first
// occurrence keeps the canonical const name and the OTHER two
// each get a dedicated warning anchored at their own row.
func TestEnumValueCollisionThreeWayEmitsTwoWarnings(t *testing.T) {
	_, diags := Analyze(parseFiles(t, `package x
enum E {
    user_id = "a"
    userId  = "b"
    USER_ID = "c"
}`))
	hits := 0
	for _, d := range diags {
		if d.Code == CodeEnumValueCollision {
			hits++
		}
	}
	if hits != 2 {
		t.Errorf("expected 2 warnings (one per duplicate beyond the canonical), got %d (%v)", hits, codes(diags))
	}
}

// TestEnumValueCollisionNoFalsePositive confirms a clean enum with
// genuinely distinct value names produces no warning. PascalCase
// enum values are the canonical form and must NOT trigger the
// collision rule on their own.
func TestEnumValueCollisionNoFalsePositive(t *testing.T) {
	mustClean(t, `package x
enum Color { Red Green Blue }`)
}
