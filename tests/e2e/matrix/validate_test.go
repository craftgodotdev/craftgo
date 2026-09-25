package matrix

import (
	"io"
	"net/http"
	"strings"
	"testing"

	collections "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/collections"
	combine "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/combine"
	regression "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/regression"
	scalars "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/scalars"
	strtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/strings"
)

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

// A decorator on a scalar-typed field narrows the scalar's own bound, on a
// present optional field too.
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

// A generic's Validate reaches each scalar element through the scalar's own
// Validate method.
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

// A generic's Validate rejects an enum element outside the value set.
func TestGenericOverEnum(t *testing.T) {
	rejects(t, "Page[Priority] holding a value outside the enum set",
		&scalars.Page[scalars.Priority]{Items: []scalars.Priority{scalars.Priority("bogus")}})
	accepts(t, "Page[Priority] holding a valid enum value",
		&scalars.Page[scalars.Priority]{Items: []scalars.Priority{scalars.PriorityHigh}})
}

// @requiresOneOf and @mutuallyExclusive run beside the per-field validators
// and apply independently when stacked.
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

// A cross-field group can name mixin-promoted fields: Rg3Contact's
// @requiresOneOf(email, phone) targets fields from Rg3Pair.
func TestCrossFieldOverMixinPromotedField(t *testing.T) {
	rejects(t, "neither promoted field set", &regression.Rg3Contact{})
	accepts(t, "a promoted field satisfies the group",
		&regression.Rg3Contact{Rg3Pair: regression.Rg3Pair{Email: strptr("a@b.com")}})
}

// A @nullable bytes or slice field passes as nil, and its length or items
// check still rejects a short value.
func TestNilableNullableNilGuard(t *testing.T) {
	accepts(t, "all fields null", &regression.Rg5Nilable{})
	rejects(t, "blob shorter than @minLength(4)", &regression.Rg5Nilable{Blob: []byte("ab")})
	rejects(t, "ids shorter than @minItems(2)", &regression.Rg5Nilable{Ids: []int{1}})
	accepts(t, "blob at the @minLength(4) bound", &regression.Rg5Nilable{Blob: []byte("abcd")})
}

// Stacked bounds enforce their intersection: `@gte(10) @lte(90) @range(0,100)`
// is 10..90, and `@length(5) @minLength(3) @maxLength(10)` is exactly 5.
func TestStackedBoundsIntersect(t *testing.T) {
	accepts(t, "b and a within the tightest bounds", &regression.Rg6Stacked{B: 50, A: "abcde"})
	rejects(t, "b below @gte(10), which @range(0,100) must not loosen", &regression.Rg6Stacked{B: 5, A: "abcde"})
	rejects(t, "b above @lte(90)", &regression.Rg6Stacked{B: 95, A: "abcde"})
	rejects(t, "a not exactly @length(5)", &regression.Rg6Stacked{B: 50, A: "abc"})
}

// A constraint inside a composite generic argument is enforced: Rg5Item.sku
// is @minLength(2) inside Rg5Page<map<string, Rg5Item>>.
func TestConstraintThroughCompositeGeneric(t *testing.T) {
	page := func(sku string) *regression.Rg5Composite {
		return &regression.Rg5Composite{Page: regression.Rg5Page[map[string]regression.Rg5Item]{
			Items: []map[string]regression.Rg5Item{{"k": {Sku: sku}}},
		}}
	}
	rejects(t, "sku below @minLength(2) deep inside Page<map<...>>", page("x"))
	accepts(t, "valid sku inside Page<map<...>>", page("ok"))
}

// A map validates both its scalar key and its value: Rg5Bag.byUser is
// map<Rg5UserID, Rg5Tag>, the key @gte(1) and the value's name @minLength(1).
func TestScalarMapKeyAndValueValidated(t *testing.T) {
	bag := func(id regression.Rg5UserID, name string) *regression.Rg5Bag {
		return &regression.Rg5Bag{ByUser: map[regression.Rg5UserID]regression.Rg5Tag{id: {Name: name}}}
	}
	rejects(t, "key 0 below the key's @gte(1)", bag(0, "ok"))
	rejects(t, "value empty below the value's @minLength(1)", bag(1, ""))
	accepts(t, "valid key and value", bag(1, "ok"))
}

// A map's key and value errors both name the field's JSON key:
// Map_JSONKey.index is map<NonEmptyID, Email> @json("by_id").
func TestMapErrorsNameTheJSONKey(t *testing.T) {
	cases := map[string]*collections.Map_JSONKey{
		"empty key":     {Index: map[collections.NonEmptyID]collections.Email{"": "a@b.co"}},
		"invalid email": {Index: map[collections.NonEmptyID]collections.Email{"id": "nope"}},
	}
	for name, v := range cases {
		if err := v.Validate(); err == nil || !strings.HasPrefix(err.Error(), "by_id: ") {
			t.Errorf("%s: want an error naming by_id, got %v", name, err)
		}
	}
	accepts(t, "valid key and value",
		&collections.Map_JSONKey{Index: map[collections.NonEmptyID]collections.Email{"id": "a@b.co"}})
}

// A query parameter that fails validation is reported under the name the
// request carried it: PageReq.pageSize auto-binds to `?pageSize` although it
// carries @json("page_size").
func TestAutoBoundQueryErrorNamesTheParameter(t *testing.T) {
	ts := bootAll(t)
	resp, err := ts.Client().Get(ts.URL + "/api/bindings/page?pageSize=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest || !strings.HasPrefix(string(body), "pageSize: ") {
		t.Errorf("want 400 naming pageSize, got %d %q", resp.StatusCode, body)
	}
}

// An integer bound past 2^53 is compared exactly: minId is @gte(2^53 + 1).
func TestBigIntegerBoundKeepsPrecision(t *testing.T) {
	const maxInt64 = 9223372036854775807
	accepts(t, "minId exactly 2^53+1", &regression.Rg2Big{MinID: 9007199254740993, Bigmin: maxInt64, Small: 50})
	rejects(t, "minId one below the bound", &regression.Rg2Big{MinID: 9007199254740992, Bigmin: maxInt64, Small: 50})
}

// @multipleOf rejects a non-multiple.
func TestMultipleOfEnforced(t *testing.T) {
	accepts(t, "qty 15 is a multiple of 5", &regression.Rg3MultipleOf{Qty: 15})
	rejects(t, "qty 7 is not a multiple of 5", &regression.Rg3MultipleOf{Qty: 7})
}

// A declared error-body field is validated: code is 3..8 chars matching
// ^E_[A-Z]+$, message at most 50.
func TestErrorBodyConstraintsEnforced(t *testing.T) {
	accepts(t, "valid code and message", &regression.Rg5CodeErrBody{Code: "E_FOO", Message: "ok"})
	rejects(t, "code shorter than @minLength(3)", &regression.Rg5CodeErrBody{Code: "E_", Message: "ok"})
	rejects(t, "code violating ^E_[A-Z]+$", &regression.Rg5CodeErrBody{Code: "bad", Message: "ok"})
}

// An error body validates fields promoted from its mixin: Rg5HdrMeta.note is
// @minLength(1).
func TestErrorBodyMixinValidated(t *testing.T) {
	rejects(t, "promoted note empty below @minLength(1)",
		&regression.Rg5HdrErrorBody{Rg5HdrMeta: regression.Rg5HdrMeta{Note: ""}})
	accepts(t, "valid promoted note",
		&regression.Rg5HdrErrorBody{Rg5HdrMeta: regression.Rg5HdrMeta{Note: "ok"}})
}

// String length decorators count characters, not bytes: Str_Lengths.exact is
// @length(5, 5).
func TestStringLengthCountsCharacters(t *testing.T) {
	mk := func(exact string) *strtypes.Str_Lengths {
		return &strtypes.Str_Lengths{ZeroLower: "x", NonEmpty: "x", Exact: exact, OnlyMin: "x", OnlyMax: "x"}
	}
	// 5 characters, 6 bytes.
	accepts(t, "café! is 5 characters at @length(5,5)", mk("café!"))
	// 4 characters, 5 bytes.
	rejects(t, "café is 4 characters, not 5, at @length(5,5)", mk("café"))
	rejects(t, "abcdef is 6 characters at @length(5,5)", mk("abcdef"))
}
