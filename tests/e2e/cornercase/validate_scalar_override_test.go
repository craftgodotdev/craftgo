// Runtime proof that a field-level decorator STACKED on a scalar-typed
// field is enforced (regression: it used to be silently dropped because
// a defined-type scalar like `Cents`/`Tag` failed the numeric/string
// type-guards, so the emitter produced no check while OpenAPI still
// advertised the tighter bound). The fix derefs+casts the scalar to its
// primitive in a `_sv` local, then runs each stacked decorator.
package cornercase

import (
	"testing"

	scalars "github.com/craftgodotdev/craftgo/tests/e2e/cornercase/internal/types/scalars"
)

func TestScalarFieldLevelOverrideEnforced(t *testing.T) {
	// amount: Cents allows up to 1e9; the field narrows to @lte(500).
	bad := scalars.ScalarFieldOverrides{Amount: scalars.Cents(700), Code: scalars.Tag("ok")}
	if err := bad.Validate(); err == nil {
		t.Fatal("amount=700 accepted — stacked field-level @lte(500) on a scalar field was dropped")
	}

	// code: Tag allows up to 20 chars; the field narrows to @maxLength(5).
	longCode := scalars.ScalarFieldOverrides{Amount: scalars.Cents(100), Code: scalars.Tag("abcdef")}
	if err := longCode.Validate(); err == nil {
		t.Fatal("code len 6 accepted — stacked field-level @maxLength(5) on a scalar field was dropped")
	}

	// optional discount: present + over the field bound must reject.
	d := scalars.Cents(200)
	overDiscount := scalars.ScalarFieldOverrides{Amount: scalars.Cents(100), Code: scalars.Tag("ok"), Discount: &d}
	if err := overDiscount.Validate(); err == nil {
		t.Fatal("discount=200 accepted — stacked field-level @lte(100) on an OPTIONAL scalar field was dropped")
	}

	// All-valid (incl. omitted optional) must pass.
	ok := scalars.ScalarFieldOverrides{Amount: scalars.Cents(500), Code: scalars.Tag("abcde")}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid ScalarFieldOverrides rejected: %v", err)
	}
}
