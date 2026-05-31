// Runtime proof for the generic-over-scalar / generic-over-enum
// validation regression (RC-2).
//
// A generic type's Validate() is PARAMETRIC: it reaches each element
// through the runtime probe `any(&elem).(interface{ Validate() error })`.
// That probe only resolves when the element type actually carries a
// Validate() method. Scalars used to emit as Go aliases (`type Email =
// string`), which cannot carry methods, so `Page[Email]` silently
// skipped @format(email) / @maxLength while the generated OpenAPI still
// advertised those constraints — a spec-vs-runtime lie. Enums were
// defined types but had no Validate() at all, so `Page[Priority]`
// dropped the value-set check the same way.
//
// Emitting scalars as DEFINED types with their own Validate() (and
// giving every enum a Validate() switch) closes both. These assertions
// fail loudly if the dispatch is ever dropped again.
package cornercase

import (
	"testing"

	scalars "github.com/craftgodotdev/craftgo/tests/e2e/cornercase/internal/types/scalars"
)

func TestGenericOverScalarValidates(t *testing.T) {
	// Page[Email] over a constrained string scalar.
	bad := scalars.Page[scalars.Email]{Items: []scalars.Email{"not-an-email"}}
	if err := bad.Validate(); err == nil {
		t.Fatal("Page[Email] accepted an invalid email — generic-over-scalar validation was dropped")
	}
	good := scalars.Page[scalars.Email]{Items: []scalars.Email{"a@b.com"}}
	if err := good.Validate(); err != nil {
		t.Fatalf("Page[Email] rejected a valid email: %v", err)
	}

	// Page[Cents] over a constrained numeric scalar (@gte(0) @lte(1e9)).
	overBad := scalars.Page[scalars.Cents]{Items: []scalars.Cents{2000000000}}
	if err := overBad.Validate(); err == nil {
		t.Fatal("Page[Cents] accepted an out-of-range amount — numeric scalar bound dropped")
	}
	overGood := scalars.Page[scalars.Cents]{Items: []scalars.Cents{500}}
	if err := overGood.Validate(); err != nil {
		t.Fatalf("Page[Cents] rejected an in-range amount: %v", err)
	}
}

func TestGenericOverEnumValidates(t *testing.T) {
	bad := scalars.Page[scalars.Priority]{Items: []scalars.Priority{scalars.Priority("bogus")}}
	if err := bad.Validate(); err == nil {
		t.Fatal("Page[Priority] accepted a value outside the enum set — generic-over-enum validation was dropped")
	}
	good := scalars.Page[scalars.Priority]{Items: []scalars.Priority{scalars.PriorityHigh}}
	if err := good.Validate(); err != nil {
		t.Fatalf("Page[Priority] rejected a valid enum value: %v", err)
	}
}
