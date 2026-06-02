// Runtime validation proofs for the cornercase fixtures.
//
// The committed generated code and the e2e orchestrator already prove the
// fixtures COMPILE and regenerate idempotently. These tests go one layer
// deeper: they construct the generated types and call Validate() to prove the
// runtime enforces exactly what the OpenAPI schema advertises. A change that
// silently drops a check — leaving code that still compiles — fails here
// loudly.
//
// Every case runs through rejects / accepts so the table reads as a
// spec: a one-line label, the value, and whether the contract admits it.
package matrix

import (
	"testing"

	combine "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/combine"
	regression "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/regression"
	scalars "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/scalars"
)

// ---- helpers ----

func strptr(s string) *string { return &s }

// rejects asserts the value fails validation (Validate returns an error).
func rejects(t *testing.T, name string, v interface{ Validate() error }) {
	t.Helper()
	if err := v.Validate(); err == nil {
		t.Errorf("%s: expected a validation error, got nil", name)
	}
}

// accepts asserts the value passes validation (Validate returns nil).
func accepts(t *testing.T, name string, v interface{ Validate() error }) {
	t.Helper()
	if err := v.Validate(); err != nil {
		t.Errorf("%s: expected no error, got %v", name, err)
	}
}

// ---------------------------------------------------------------------------
// Scalars and generics — a defined-type scalar (and enum) carries its own
// Validate(), so a field-level decorator stacked on a scalar field and a
// constrained scalar / enum reached through a generic both enforce at runtime.
// ---------------------------------------------------------------------------

// TestScalarFieldLevelOverride: a decorator on a scalar-typed field narrows
// the scalar's own bound, and the narrowed bound runs — including on an
// optional scalar field when it is present.
func TestScalarFieldLevelOverride(t *testing.T) {
	// Cents allows up to 1e9; the field narrows it to @lte(500).
	rejects(t, "amount over the field's @lte(500)",
		&scalars.ScalarFieldOverrides{Amount: scalars.Cents(700), Code: scalars.Tag("ok")})
	// Tag allows up to 20 chars; the field narrows it to @maxLength(5).
	rejects(t, "code over the field's @maxLength(5)",
		&scalars.ScalarFieldOverrides{Amount: scalars.Cents(100), Code: scalars.Tag("abcdef")})

	discount := scalars.Cents(200)
	rejects(t, "present optional discount over the field's @lte(100)",
		&scalars.ScalarFieldOverrides{Amount: scalars.Cents(100), Code: scalars.Tag("ok"), Discount: &discount})

	accepts(t, "every field within its narrowed bound (optional omitted)",
		&scalars.ScalarFieldOverrides{Amount: scalars.Cents(500), Code: scalars.Tag("abcde")})
}

// TestGenericOverScalar: a generic type's parametric Validate() reaches each
// element through a runtime probe, which resolves because the scalar element
// type carries its own Validate().
func TestGenericOverScalar(t *testing.T) {
	// Page[Email] over a constrained string scalar (@format(email) + length).
	rejects(t, "Page[Email] holding an invalid email",
		&scalars.Page[scalars.Email]{Items: []scalars.Email{"not-an-email"}})
	accepts(t, "Page[Email] holding a valid email",
		&scalars.Page[scalars.Email]{Items: []scalars.Email{"a@b.com"}})

	// Page[Cents] over a constrained numeric scalar (@gte(0) @lte(1e9)).
	rejects(t, "Page[Cents] holding an out-of-range amount",
		&scalars.Page[scalars.Cents]{Items: []scalars.Cents{2000000000}})
	accepts(t, "Page[Cents] holding an in-range amount",
		&scalars.Page[scalars.Cents]{Items: []scalars.Cents{500}})
}

// TestGenericOverEnum: the same parametric path enforces an enum's value-set
// check, because every enum carries a Validate() switch.
func TestGenericOverEnum(t *testing.T) {
	rejects(t, "Page[Priority] holding a value outside the enum set",
		&scalars.Page[scalars.Priority]{Items: []scalars.Priority{scalars.Priority("bogus")}})
	accepts(t, "Page[Priority] holding a valid enum value",
		&scalars.Page[scalars.Priority]{Items: []scalars.Priority{scalars.PriorityHigh}})
}

// ---------------------------------------------------------------------------
// Cross-field groups — @requiresOneOf / @mutuallyExclusive over pointer-backed
// fields, including fields a type inherits through a mixin.
// ---------------------------------------------------------------------------

// TestCrossFieldGroups: the group checks run alongside the per-field
// validators, and @mutuallyExclusive layered over @requiresOneOf behaves as
// two orthogonal rules.
func TestCrossFieldGroups(t *testing.T) {
	// @requiresOneOf(email, phone), with each field still independently checked.
	rejects(t, "neither email nor phone present", &combine.PairsContact{})
	rejects(t, "phone absent and email format invalid", &combine.PairsContact{Email: strptr("bad")})
	accepts(t, "a single valid phone satisfies the group", &combine.PairsContact{Phone: strptr("+123")})

	// @mutuallyExclusive(a, b) layered over @requiresOneOf(a, b, c).
	accepts(t, "c alone satisfies requiresOneOf", &combine.PairsChoice{C: strptr("x")})
	rejects(t, "a and b together violate mutuallyExclusive", &combine.PairsChoice{A: strptr("x"), B: strptr("y")})
	rejects(t, "none of a/b/c violates requiresOneOf", &combine.PairsChoice{})
}

// TestCrossFieldOverMixinPromotedField: a group may name fields the type
// inherits — Rg3Contact's @requiresOneOf(email, phone) targets fields promoted
// from the embedded Rg3Pair mixin.
func TestCrossFieldOverMixinPromotedField(t *testing.T) {
	rejects(t, "neither promoted field set", &regression.Rg3Contact{})
	accepts(t, "a promoted field satisfies the group",
		&regression.Rg3Contact{Rg3Pair: regression.Rg3Pair{Email: strptr("a@b.com")}})
}

// ---------------------------------------------------------------------------
// Constraints across type shapes — nil-guards on nilable types, bound
// intersection, constraints reached through composite generics and scalar map
// keys, big-integer precision, @multipleOf, and error bodies (incl. mixins).
// ---------------------------------------------------------------------------

// TestNilableNullableNilGuard: on a nilable Go type (bytes -> []byte, slice,
// map) an optional / nullable length or items check is nil-guarded, so an
// explicit null passes while a present-but-too-short value still fails —
// matching the OpenAPI null-union.
func TestNilableNullableNilGuard(t *testing.T) {
	accepts(t, "all fields null", &regression.Rg5Nilable{})
	rejects(t, "blob shorter than @minLength(4)", &regression.Rg5Nilable{Blob: []byte("ab")})
	rejects(t, "ids shorter than @minItems(2)", &regression.Rg5Nilable{Ids: []int{1}})
	accepts(t, "blob at the @minLength(4) bound", &regression.Rg5Nilable{Blob: []byte("abcd")})
}

// TestStackedBoundsIntersect: stacked same-family bounds enforce the tightest,
// matching the spec's intersection — `@gte(10) @lte(90) @range(0,100)` is
// 10..90, and `@length(5) @minLength(3) @maxLength(10)` is exactly 5.
func TestStackedBoundsIntersect(t *testing.T) {
	accepts(t, "b and a within the tightest bounds", &regression.Rg6Stacked{B: 50, A: "abcde"})
	rejects(t, "b below @gte(10), which @range(0,100) must not loosen", &regression.Rg6Stacked{B: 5, A: "abcde"})
	rejects(t, "b above @lte(90)", &regression.Rg6Stacked{B: 95, A: "abcde"})
	rejects(t, "a not exactly @length(5)", &regression.Rg6Stacked{B: 50, A: "abc"})
}

// TestConstraintThroughCompositeGeneric: a constraint on an element reached
// through a composite generic argument (Rg5Page over map<string, Rg5Item>,
// where Rg5Item.sku carries @minLength(2)) is enforced via the generic
// reflection fallback.
func TestConstraintThroughCompositeGeneric(t *testing.T) {
	page := func(sku string) *regression.Rg5Composite {
		return &regression.Rg5Composite{Page: regression.Rg5Page[map[string]regression.Rg5Item]{
			Items: []map[string]regression.Rg5Item{{"k": {Sku: sku}}},
		}}
	}
	rejects(t, "sku below @minLength(2) deep inside Page<map<...>>", page("x"))
	accepts(t, "valid sku inside Page<map<...>>", page("ok"))
}

// TestScalarMapKeyAndValueValidated: a non-string scalar map key carries its
// own constraint at runtime, as does the value — Rg5Bag.byUser is
// map<Rg5UserID @gte(1), Rg5Tag @minLength(1)>.
func TestScalarMapKeyAndValueValidated(t *testing.T) {
	bag := func(id regression.Rg5UserID, name string) *regression.Rg5Bag {
		return &regression.Rg5Bag{ByUser: map[regression.Rg5UserID]regression.Rg5Tag{id: {Name: name}}}
	}
	rejects(t, "key 0 below the key's @gte(1)", bag(0, "ok"))
	rejects(t, "value empty below the value's @minLength(1)", bag(1, ""))
	accepts(t, "valid key and value", bag(1, "ok"))
}

// TestBigIntegerBoundKeepsPrecision: an integer bound beyond 2^53 keeps exact
// precision in the validator (a float64 round-trip would lose it). minId is
// bounded at 2^53 + 1.
func TestBigIntegerBoundKeepsPrecision(t *testing.T) {
	const maxInt64 = 9223372036854775807
	accepts(t, "minId exactly 2^53+1", &regression.Rg2Big{MinID: 9007199254740993, Bigmin: maxInt64, Small: 50})
	rejects(t, "minId one below the bound", &regression.Rg2Big{MinID: 9007199254740992, Bigmin: maxInt64, Small: 50})
}

// TestMultipleOfEnforced: @multipleOf rejects a non-multiple at runtime.
func TestMultipleOfEnforced(t *testing.T) {
	accepts(t, "qty 15 is a multiple of 5", &regression.Rg3MultipleOf{Qty: 15})
	rejects(t, "qty 7 is not a multiple of 5", &regression.Rg3MultipleOf{Qty: 7})
}

// TestErrorBodyConstraintsEnforced: a user-declared error-body field is
// validated at runtime (and emitted in the OpenAPI error schema). code is
// 3..8 chars matching ^E_[A-Z]+$; message is at most 50.
func TestErrorBodyConstraintsEnforced(t *testing.T) {
	accepts(t, "valid code and message", &regression.Rg5CodeErrBody{Code: "E_FOO", Message: "ok"})
	rejects(t, "code shorter than @minLength(3)", &regression.Rg5CodeErrBody{Code: "E_", Message: "ok"})
	rejects(t, "code violating ^E_[A-Z]+$", &regression.Rg5CodeErrBody{Code: "bad", Message: "ok"})
}

// TestErrorBodyMixinValidated: an error body that embeds a mixin validates the
// mixin's promoted fields. Rg5HdrError embeds Rg5HdrMeta, whose note carries
// @minLength(1), so the generated Rg5HdrErrorBody.Validate dispatches to it.
func TestErrorBodyMixinValidated(t *testing.T) {
	rejects(t, "promoted note empty below @minLength(1)",
		&regression.Rg5HdrErrorBody{Rg5HdrMeta: regression.Rg5HdrMeta{Note: ""}})
	accepts(t, "valid promoted note",
		&regression.Rg5HdrErrorBody{Rg5HdrMeta: regression.Rg5HdrMeta{Note: "ok"}})
}
