package matrix

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	bindings "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/bindings"
	collections "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/collections"
	combine "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/combine"
	matrixfmt "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/fmt"
	numbers "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/numbers"
	scalars "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/scalars"
	strtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/strings"
	xrefs "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/xrefs"
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

// A cross-field group can name mixin-promoted fields: PromotedContact's
// @requiresOneOf(email, phone) targets fields from ContactPair.
func TestCrossFieldOverMixinPromotedField(t *testing.T) {
	rejects(t, "neither promoted field set", &combine.PromotedContact{})
	accepts(t, "a promoted field satisfies the group",
		&combine.PromotedContact{ContactPair: combine.ContactPair{Email: strptr("a@b.com")}})
}

// A @nullable bytes or slice field passes as nil, and its length or items
// check still rejects a short value.
func TestNilableNullableNilGuard(t *testing.T) {
	accepts(t, "all fields null", &combine.NilableNullable{})
	rejects(t, "blob shorter than @minLength(4)", &combine.NilableNullable{Blob: []byte("ab")})
	rejects(t, "ids shorter than @minItems(2)", &combine.NilableNullable{Ids: []int{1}})
	accepts(t, "blob at the @minLength(4) bound", &combine.NilableNullable{Blob: []byte("abcd")})
}

// Stacked bounds enforce their intersection: `@gte(10) @lte(90) @range(0,100)`
// is 10..90, and `@length(5) @minLength(3) @maxLength(10)` is exactly 5.
func TestStackedBoundsIntersect(t *testing.T) {
	accepts(t, "b and a within the tightest bounds", &combine.PairsStacked{B: 50, A: "abcde"})
	rejects(t, "b below @gte(10), which @range(0,100) must not loosen", &combine.PairsStacked{B: 5, A: "abcde"})
	rejects(t, "b above @lte(90)", &combine.PairsStacked{B: 95, A: "abcde"})
	rejects(t, "a not exactly @length(5)", &combine.PairsStacked{B: 50, A: "abc"})
}

// A constraint inside a composite generic argument is enforced: SkuItem.sku
// is @minLength(2) inside SkuPage<map<string, SkuItem>>.
func TestConstraintThroughCompositeGeneric(t *testing.T) {
	page := func(sku string) *scalars.CompositeArg {
		return &scalars.CompositeArg{Page: scalars.SkuPage[map[string]scalars.SkuItem]{
			Items: []map[string]scalars.SkuItem{{"k": {Sku: sku}}},
		}}
	}
	rejects(t, "sku below @minLength(2) deep inside Page<map<...>>", page("x"))
	accepts(t, "valid sku inside Page<map<...>>", page("ok"))
}

// A map validates both its scalar key and its value: Map_ScalarKey.byUser is
// map<MemberID, MemberTag>, the key @gte(1) and the value's name @minLength(1).
func TestScalarMapKeyAndValueValidated(t *testing.T) {
	bag := func(id collections.MemberID, name string) *collections.Map_ScalarKey {
		return &collections.Map_ScalarKey{ByUser: map[collections.MemberID]collections.MemberTag{id: {Name: name}}}
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
	var got struct{ Message string }
	_ = json.Unmarshal(body, &got)
	if resp.StatusCode != http.StatusBadRequest || !strings.HasPrefix(got.Message, "pageSize: ") {
		t.Errorf("want 400 naming pageSize, got %d %q", resp.StatusCode, body)
	}
}

// A body value of the wrong JSON type is reported under its JSON key, a mixin's field
// included, naming no Go struct or type.
func TestDecodeErrorsNameTheWireField(t *testing.T) {
	ts := bootAll(t)
	for _, tc := range []struct{ path, body, want string }{
		{"/api/combine/defaults/enum", `{"c": 5}`, "c: expected string, got number"},
		{"/api/combine/pairs/renamed/x", `{"primary_email": 5}`, "primary_email: expected string, got number"},
		{"/api/combine/pairs/renamed/x", `{"backup_email": ["a"]}`, "backup_email: expected string, got array"},
		{"/api/numbers/counter", `{"body": {"rangeInt8": 300}}`, "body.rangeInt8: 300 is out of range"},
	} {
		resp, err := ts.Client().Post(ts.URL+tc.path, "application/json", strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var got struct{ Message string }
		_ = json.Unmarshal(body, &got)
		if resp.StatusCode != http.StatusBadRequest || got.Message != tc.want {
			t.Errorf("POST %s %s: got %d %q, want 400 %q", tc.path, tc.body, resp.StatusCode, body, tc.want)
		}
	}
}

// A float query parameter refuses NaN, which passes every bound, and the infinities, which an
// unbounded field would take; a finite value binds.
func TestFloatQueryRejectsNonFinite(t *testing.T) {
	ts := bootAll(t)
	for query, want := range map[string]int{
		"ratio=NaN":            http.StatusBadRequest,
		"ratio=0.5&scale=Inf":  http.StatusBadRequest,
		"ratio=0.5&scale=-inf": http.StatusBadRequest,
		"ratio=0.5&scale=1e3":  http.StatusOK,
	} {
		resp, err := ts.Client().Get(ts.URL + "/api/bindings/query-float?" + query)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("?%s: got %d %q, want %d", query, resp.StatusCode, body, want)
		}
		if want == http.StatusBadRequest && !strings.Contains(string(body), "not a finite number") {
			t.Errorf("?%s: body %q does not say the value is not finite", query, body)
		}
	}
}

// A type naming packages called like the standard packages its files import
// validates through them: XStdNames.rows is fmt.Row[] @uniqueItems.
func TestStdNamedPackagesValidate(t *testing.T) {
	row := matrixfmt.Row{Cell: "a"}
	rejects(t, "repeated rows", &xrefs.XStdNames{Rows: []matrixfmt.Row{row, row}})
	accepts(t, "distinct rows", &xrefs.XStdNames{Rows: []matrixfmt.Row{row, {Cell: "b"}}})
}

// An integer bound past 2^53 is compared exactly: minId is @gte(2^53 + 1).
func TestBigIntegerBoundKeepsPrecision(t *testing.T) {
	const maxInt64 = 9223372036854775807
	accepts(t, "minId exactly 2^53+1", &numbers.NumberBigBounds{MinID: 9007199254740993, Bigmin: maxInt64, Small: 50})
	rejects(t, "minId one below the bound", &numbers.NumberBigBounds{MinID: 9007199254740992, Bigmin: maxInt64, Small: 50})
}

// @multipleOf rejects a non-multiple.
func TestMultipleOfEnforced(t *testing.T) {
	accepts(t, "qty 15 is a multiple of 5", &numbers.NumberMultipleOf{Qty: 15})
	rejects(t, "qty 7 is not a multiple of 5", &numbers.NumberMultipleOf{Qty: 7})
}

// A declared error-body field is validated: code is 3..8 chars matching
// ^E_[A-Z]+$, message at most 50.
func TestErrorBodyConstraintsEnforced(t *testing.T) {
	accepts(t, "valid code and message", &bindings.CodeMessageErrBody{Code: "E_FOO", Message: "ok"})
	rejects(t, "code shorter than @minLength(3)", &bindings.CodeMessageErrBody{Code: "E_", Message: "ok"})
	rejects(t, "code violating ^E_[A-Z]+$", &bindings.CodeMessageErrBody{Code: "bad", Message: "ok"})
}

// An error body validates fields promoted from its mixin: ErrorHeaderMeta.note
// is @minLength(1).
func TestErrorBodyMixinValidated(t *testing.T) {
	rejects(t, "promoted note empty below @minLength(1)",
		&bindings.HeaderMixinErrorBody{ErrorHeaderMeta: bindings.ErrorHeaderMeta{Note: ""}})
	accepts(t, "valid promoted note",
		&bindings.HeaderMixinErrorBody{ErrorHeaderMeta: bindings.ErrorHeaderMeta{Note: "ok"}})
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

// A type parameter spelled like a declaration takes its argument's type:
// Shadowed<int, string>'s optional `payload` is an *int, unset or set, and
// its `level` any string, though Priority names an enum.
func TestTypeParamShadowsDeclaration(t *testing.T) {
	n := 7
	accepts(t, "unset payload", &scalars.ShadowedHost{S: scalars.Shadowed[int, string]{Level: "any"}})
	accepts(t, "set payload", &scalars.ShadowedHost{S: scalars.Shadowed[int, string]{Payload: &n, Level: "x"}})
}
