// Package main hosts the showcase application's smoke tests. They
// drive the generated Go directly (no HTTP) to verify three v1
// guarantees:
//
//  1. Cross-package nesting compiles and the generated structs hold
//     the right field types (Project.Owner is a users.UserRef, not a
//     stringly-typed id).
//  2. Validators reject the offending values with the expected error
//     surfaces, including the optional-pointer-then-nested-call
//     cascade (User.Avatar carries its own validators that are
//     invoked via `(*v.Avatar).Validate()`).
//  3. The shared response envelope (`shared.OkResp`) is the
//     canonical success shape and decodes from JSON cleanly.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/example/config"
	orderservice "github.com/craftgodotdev/craftgo/example/internal/transport/order-service"
	ordersapi "github.com/craftgodotdev/craftgo/example/internal/types/orders"
	projectsapi "github.com/craftgodotdev/craftgo/example/internal/types/projects"
	sharedapi "github.com/craftgodotdev/craftgo/example/internal/types/shared"
	tasksapi "github.com/craftgodotdev/craftgo/example/internal/types/tasks"
	usersapi "github.com/craftgodotdev/craftgo/example/internal/types/users"
	"github.com/craftgodotdev/craftgo/example/svccontext"
)

// TestNestedCrossPackageTypes pins the multi-package wire shape:
// projects.Project owns a users.UserRef, tasks.Task owns
// projects.ProjectRef + an optional users.UserRef, and tasks.Comment
// nests another users.UserRef. If the codegen ever stops adding the
// matching cross-package Go imports this won't compile.
func TestNestedCrossPackageTypes(t *testing.T) {
	owner := usersapi.UserRef{ID: "u1", Name: "Alice"}
	proj := projectsapi.Project{
		ID:    "p1",
		Name:  "Atlas",
		Owner: owner,
		Members: []usersapi.UserRef{
			{ID: "u1", Name: "Alice"},
			{ID: "u2", Name: "Bob"},
		},
	}
	if err := proj.Validate(); err != nil {
		t.Fatalf("project should validate: %v", err)
	}

	pref := projectsapi.ProjectRef{ID: "p1", Name: "Atlas"}
	assignee := usersapi.UserRef{ID: "u2", Name: "Bob"}
	task := tasksapi.Task{
		ID:       "t1",
		Title:    "Ship the showcase",
		Status:   tasksapi.TaskStatusInProgress,
		Project:  pref,
		Assignee: &assignee, // optional - present here.
		Comments: []tasksapi.Comment{
			{ID: "c1", Author: assignee, Body: "lgtm", CreatedAt: "2026-04-29T00:00:00Z"},
		},
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("task should validate: %v", err)
	}

	// Optional pointer absent: assignee omitted from wire.
	taskNoAssignee := task
	taskNoAssignee.Assignee = nil
	out, _ := json.Marshal(&taskNoAssignee)
	if strings.Contains(string(out), `"assignee"`) {
		t.Errorf("absent assignee should drop the JSON key, got:\n%s", out)
	}
}

// TestUserAvatarNestedValidation exercises the optional-pointer-
// then-recursive-validator cascade. v1's per-field Validate() does
// NOT auto-recurse into nested struct fields by default, BUT for an
// optional nested type with its own validators we explicitly emit
// `if v.Avatar != nil { (*v.Avatar).Validate() }` - this test pins
// that exact code path.
func TestUserAvatarNestedValidation(t *testing.T) {
	u := usersapi.User{
		ID:    "u1",
		Email: "alice@example.com",
		Name:  "Alice",
	}
	if err := u.Validate(); err != nil {
		t.Fatalf("user without avatar should validate: %v", err)
	}

	bad := &usersapi.Avatar{URL: "not-a-url", SizeBytes: 100}
	u.Avatar = bad
	err := u.Validate()
	if err == nil {
		t.Fatal("avatar with malformed URL should fail validation")
	}
	if !strings.Contains(err.Error(), "url") {
		t.Errorf("error should mention url, got: %v", err)
	}

	good := &usersapi.Avatar{URL: "https://example.com/a.png", SizeBytes: 1024}
	u.Avatar = good
	if err := u.Validate(); err != nil {
		t.Fatalf("good avatar should pass: %v", err)
	}
}

// TestUpdateUserReqOptionalFields covers the optional-string-with-
// validator path (`name string?  @length(1, 80)`). The bug that
// motivated the codegen fix was a malformed `if v.Name != nil &&
// l := len(*v.Name); ...` - exercising both nil and present cases
// asserts the new shape compiles AND validates.
func TestUpdateUserReqOptionalFields(t *testing.T) {
	// Nil name → validators short-circuit on the nil guard.
	req := usersapi.UpdateUserReq{ID: "u1"}
	if err := req.Validate(); err != nil {
		t.Fatalf("empty patch should validate: %v", err)
	}

	// Present but too long → length validator fires through the deref.
	long := strings.Repeat("x", 81)
	req.Name = &long
	if err := req.Validate(); err == nil {
		t.Errorf("over-long name should fail validation")
	}

	// Within range → passes.
	ok := "Alice"
	req.Name = &ok
	if err := req.Validate(); err != nil {
		t.Errorf("good name should pass: %v", err)
	}
}

// TestSharedOkRespRoundTrip decodes the canonical write-success
// envelope to confirm both the field tag and the cross-package
// reusability work end-to-end.
func TestSharedOkRespRoundTrip(t *testing.T) {
	encoded, _ := json.Marshal(sharedapi.OkResp{Ok: true})
	var decoded sharedapi.OkResp
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !decoded.Ok {
		t.Errorf("ok flag lost on round trip: %+v", decoded)
	}
}

// TestNestedContactPerFieldValidators pins the per-field validator
// surface on the Contact nested type. The Contact decl carries
// @requiresOneOf / @mutuallyExclusive type-level decorators that v1
// records in the DSL for OpenAPI surfacing but doesn't yet emit Go
// code for - see the comment in users/contact.craftgo. The
// per-FIELD validators (`@format(email)`, `@pattern(...)`) DO emit
// and we verify them here.
func TestNestedContactPerFieldValidators(t *testing.T) {
	bad := usersapi.Contact{Email: ptr("not-an-email")}
	if err := bad.Validate(); err == nil {
		t.Errorf("malformed email should be rejected by @format(email)")
	}

	withEmail := usersapi.Contact{Email: ptr("alice@example.com")}
	if err := withEmail.Validate(); err != nil {
		t.Errorf("contact with valid email should validate: %v", err)
	}

	badPhone := usersapi.Contact{Phone: ptr("xx")}
	if err := badPhone.Validate(); err == nil {
		t.Errorf("malformed phone should be rejected by @pattern")
	}
}

// ptr is the inline pointer-helper for tests. Spelled out instead of
// pulled in from a shared util because tests should be transparent.
func ptr[T any](v T) *T { return &v }

// ----------------------------------------------------------------------
// Deep-nest + scalars - orders package showcase.
// ----------------------------------------------------------------------

// TestDeepNestSixLevels constructs a fully-valid Order that
// chains all six nested levels (Order → Customer → Address →
// Geocode → Precision → GpsFix), pulls cross-package references
// (`users.UserRef`), and rides on top of every scalar declared in
// the package. A clean Validate() pass on the top wrapper plus an
// explicit (*v.X).Validate() at each layer pins the deep tree.
func TestDeepNestSixLevels(t *testing.T) {
	gpsFix := ordersapi.GpsFix{
		Satellites: 12,
		Hdop:       0.8,
		Timestamp:  "2026-04-29T08:00:00Z",
		DeviceID:   ptr("dev_AB12CDEF"),
		Notes:      ptr("clear sky"),
	}
	prec := ordersapi.Precision{
		RadiusMeters: 5.5,
		Source:       ordersapi.GeoSourceGps,
		Confidence:   0.97,
		GpsFix:       &gpsFix,
	}
	geo := ordersapi.Geocode{
		Lat:        37.7749,
		Lng:        -122.4194,
		CapturedAt: "2026-04-29T08:00:00Z",
		Precision:  &prec,
	}
	addr := ordersapi.Address{
		Street:     "1 Market St",
		City:       "San Francisco",
		PostalCode: "94105",
		Country:    "US",
		Geo:        &geo,
	}
	cust := ordersapi.Customer{
		ID:             "550e8400-e29b-41d4-a716-446655440000",
		Email:          "alice@example.com",
		Name:           "Alice",
		PrimaryAddress: addr,
	}
	order := ordersapi.Order{
		ID:         "ord_ABC12345",
		Customer:   cust,
		Items:      []ordersapi.LineItem{{ProductID: "p1", Sku: "SKU-AAAA", Quantity: 1, UnitCents: 998}},
		AuditedBy:  usersapi.UserRef{ID: "u-admin", Name: "Admin"},
		TotalCents: 998,
		Status:     ordersapi.OrderStatusPending,
		Currency:   ptr(ordersapi.CurrencyCode("USD")),
		Tags:       []string{"web"},
		CreatedAt:  "2026-04-29T08:00:00Z",
	}
	if err := order.Validate(); err != nil {
		t.Fatalf("happy-path order should validate: %v", err)
	}

	// Each level's own Validate() is callable independently - that's
	// how a real client codepath descends the tree to surface the
	// most-specific failure rather than the wrapper's first.
	for name, v := range map[string]interface {
		Validate() error
	}{
		"customer": &cust,
		"address":  &addr,
		"geocode":  &geo,
		"precision": &prec,
		"gpsFix":   &gpsFix,
	} {
		if err := v.Validate(); err != nil {
			t.Errorf("%s should validate: %v", name, err)
		}
	}
}

// TestScalarFieldsUseAliasedTypes verifies the codegen emits each
// scalar declaration as a Go type alias, so a field declared
// `currency CurrencyCode` accepts string literals interchangeably
// with the alias name. This is the property that lets scalar
// validators inherit at the wire boundary while logic code stays
// natural (`order.Currency = "USD"` works without a conversion).
func TestScalarFieldsUseAliasedTypes(t *testing.T) {
	// String-backed alias accepts a bare string literal.
	var country ordersapi.CountryCode = "US"
	var currency ordersapi.CurrencyCode = "USD"
	var orderID ordersapi.OrderID = "ord_ABC12345"

	// Aliased numeric scalars accept primitive ints / floats.
	var cents ordersapi.Cents = 999
	var lat ordersapi.Latitude = 37.7749

	// Cross-package scalars come through the shared package's
	// generated alias declarations.
	var nonEmpty sharedapi.NonEmptyID = "abc-123"
	var bps sharedapi.PercentBP = 750
	var url sharedapi.SafeURL = "https://example.com/receipt.pdf"

	// Use them so the compiler doesn't elide the assignments.
	_ = country
	_ = currency
	_ = orderID
	_ = cents
	_ = lat
	_ = nonEmpty
	_ = bps
	_ = url
}

// TestCrossPackageScalarInPaymentField demonstrates a Payment
// constructed with a cross-package SafeURL value plus an inline
// shared.NonEmptyID for referenceId. The struct compiles only
// because both shared/types.go and orders/types.go agree on the
// alias declaration, with the matching Go import added by codegen.
func TestCrossPackageScalarInPaymentField(t *testing.T) {
	pay := ordersapi.Payment{
		Method:      ordersapi.PaymentMethodCard,
		ReferenceID: "pay_ABCDEF",
		AuthCents:   1000,
		ReceiptURL:  ptr("https://example.com/r/1"),
	}
	if pay.AuthCents != 1000 {
		t.Errorf("AuthCents lost: %+v", pay)
	}
	if pay.ReceiptURL == nil || *pay.ReceiptURL == "" {
		t.Errorf("ReceiptURL not set: %+v", pay)
	}
}

// TestRequiresOneOfNowEnforced pins the bug fix where the
// validator emitter for `@requiresOneOf` previously failed to
// recognise the bare-ident argument shape (`@requiresOneOf(email,
// phone)`) - only the array-shortcut form
// (`@requiresOneOf(["email", "phone"])`) was wired through. The
// example's Contact type uses the bare-ident form so this test catches
// regressions on either side.
func TestRequiresOneOfNowEnforced(t *testing.T) {
	empty := usersapi.Contact{}
	if err := empty.Validate(); err == nil {
		t.Errorf("@requiresOneOf(email, phone) should reject empty Contact")
	}

	withEmail := usersapi.Contact{Email: ptr("alice@example.com")}
	if err := withEmail.Validate(); err != nil {
		t.Errorf("Contact with email should pass: %v", err)
	}
}

// TestMutuallyExclusiveNowEnforced exercises the second type-level
// validator that was previously dead due to the same arg-parsing
// bug. The Contact type marks `work` and `personal` as mutually
// exclusive - flipping both should be rejected.
func TestMutuallyExclusiveNowEnforced(t *testing.T) {
	both := usersapi.Contact{
		Email:    ptr("alice@example.com"),
		Work:     ptr(true),
		Personal: ptr(true),
	}
	if err := both.Validate(); err == nil {
		t.Errorf("@mutuallyExclusive(work, personal) should reject when both are true")
	}

	onlyWork := usersapi.Contact{
		Email: ptr("alice@example.com"),
		Work:  ptr(true),
	}
	if err := onlyWork.Validate(); err != nil {
		t.Errorf("only work=true should validate: %v", err)
	}
}

// TestLocalScalarInheritance verifies a field typed as a LOCAL
// scalar (`country CountryCode`) actually runs the scalar's
// validators (@length(2,2) + @pattern("^[A-Z]{2}$")) at the
// containing type's Validate() - the inheritance must NOT require
// the field-level decorator chain to repeat the scalar's checks.
func TestLocalScalarInheritance(t *testing.T) {
	good := ordersapi.Address{Street: "x", City: "x", PostalCode: "94105", Country: "US"}
	if err := good.Validate(); err != nil {
		t.Fatalf("ISO-2 country US should pass: %v", err)
	}

	// Pattern violation - lower-case fails the scalar's regex.
	bad := good
	bad.Country = "us"
	if err := bad.Validate(); err == nil {
		t.Errorf("country=%q should fail @pattern from CountryCode scalar", bad.Country)
	}

	// Length violation - three-letter fails the scalar's @length.
	bad2 := good
	bad2.Country = "USA"
	if err := bad2.Validate(); err == nil {
		t.Errorf("country=%q should fail @length(2,2) from CountryCode scalar", bad2.Country)
	}
}

// TestCrossPackageScalarInheritance verifies a field typed as a
// CROSS-PACKAGE scalar (`bpsBonus shared.PercentBP`) inherits the
// scalar's @min/@max from the sibling package - the validator
// codegen looks up shared.PercentBP via the project-level scalar
// table built by [BuildScalarTable].
func TestCrossPackageScalarInheritance(t *testing.T) {
	// Happy path inside the 0..10000 basis-points range.
	good := ordersapi.Discount{Code: "SUMMER", Percent: 10.0, BpsBonus: 500}
	if err := good.Validate(); err != nil {
		t.Fatalf("BpsBonus=500 should pass: %v", err)
	}

	// Below the scalar's @min(0) - must fail.
	bad := good
	bad.BpsBonus = -1
	if err := bad.Validate(); err == nil {
		t.Errorf("BpsBonus=-1 should fail @min(0) from shared.PercentBP")
	}

	// Above the scalar's @max(10000) - must fail.
	bad2 := good
	bad2.BpsBonus = 99999
	if err := bad2.Validate(); err == nil {
		t.Errorf("BpsBonus=99999 should fail @max(10000) from shared.PercentBP")
	}
}

// TestCrossPackageScalarInheritanceOptional confirms scalar
// inheritance respects the field's optional/pointer wrapping.
// `receiptURL shared.SafeURL?` carries the scalar's @format(url)
// + @maxLength(2048) but the validator must nil-guard before
// dereferencing the pointer - exactly the same pattern as plain
// optional-string validators.
func TestCrossPackageScalarInheritanceOptional(t *testing.T) {
	// Nil pointer → validators short-circuit, no error.
	pay := ordersapi.Payment{
		Method:      ordersapi.PaymentMethodCard,
		ReferenceID: "pay_ABCD",
		AuthCents:   100,
	}
	if err := pay.Validate(); err != nil {
		t.Fatalf("nil ReceiptURL should validate: %v", err)
	}

	// Bad URL → @format(url) inherited from shared.SafeURL fires.
	pay.ReceiptURL = ptr("not-a-url")
	if err := pay.Validate(); err == nil {
		t.Errorf("malformed ReceiptURL should fail inherited @format(url)")
	}

	// Good URL → passes.
	pay.ReceiptURL = ptr("https://example.com/receipt")
	if err := pay.Validate(); err != nil {
		t.Errorf("valid ReceiptURL should pass: %v", err)
	}
}

// TestArrayOfScalarInheritance pins the per-element validator
// inheritance for `tags Tag[]`. Each scalar decorator on Tag
// (@minLength, @maxLength, @pattern) runs once per slice entry
// inside a `for i := range v.Tags` loop emitted by codegen; the
// field-level array decorators (@minItems / @maxItems /
// @uniqueItems) run on the slice as a whole. A single bad
// element fails the whole Order.
func TestArrayOfScalarInheritance(t *testing.T) {
	base := func(tags []ordersapi.Tag) ordersapi.Order {
		return ordersapi.Order{
			ID:       "ord_ABC12345",
			Customer: ordersapi.Customer{ID: "550e8400-e29b-41d4-a716-446655440000", Email: "x@y.com", Name: "X", PrimaryAddress: ordersapi.Address{Street: "x", City: "x", PostalCode: "94105", Country: "US"}},
			Items:    []ordersapi.LineItem{{ProductID: "p1", Sku: "SKU-AAAA", Quantity: 1, UnitCents: 2}},
			AuditedBy: usersapi.UserRef{ID: "u-admin", Name: "Admin"},
			TotalCents: 2,
			Status:   ordersapi.OrderStatusPending,
			Currency: ptr(ordersapi.CurrencyCode("USD")),
			Tags:     tags,
			CreatedAt: "2026-04-29T08:00:00Z",
		}
	}

	// Happy path - every tag matches Tag's scalar pattern.
	good := base([]ordersapi.Tag{"web", "mobile-2"})
	if err := good.Validate(); err != nil {
		t.Fatalf("clean tags should pass: %v", err)
	}

	// Per-element @pattern fires (Tag scalar requires
	// `^[a-z][a-z0-9-]*$`).
	uppercased := base([]ordersapi.Tag{"Web"})
	if err := uppercased.Validate(); err == nil {
		t.Errorf(`"Web" should fail Tag's @pattern via per-element loop`)
	}

	// Per-element @minLength fires - empty string in a slot.
	withEmpty := base([]ordersapi.Tag{"web", ""})
	if err := withEmpty.Validate(); err == nil {
		t.Errorf("empty tag entry should fail Tag's @minLength(1) via per-element loop")
	}

	// Per-element @maxLength fires - 33-char string.
	tooLong := base([]ordersapi.Tag{strings.Repeat("a", 33)})
	if err := tooLong.Validate(); err == nil {
		t.Errorf("33-char tag should fail Tag's @maxLength(32) via per-element loop")
	}

	// Field-level @minItems still fires when scalar checks pass.
	empty := base([]ordersapi.Tag{})
	if err := empty.Validate(); err == nil {
		t.Errorf("empty tags slice should fail @minItems(1)")
	}

	// Field-level @uniqueItems still fires alongside scalar checks.
	dup := base([]ordersapi.Tag{"web", "web"})
	if err := dup.Validate(); err == nil {
		t.Errorf("duplicate tags should fail @uniqueItems")
	}
}

// TestMapScalarInheritance pins per-element validator inheritance
// for `metadata map<Tag, shared.SafeURL>?`. Tag's per-key
// validators (@minLength, @maxLength, @pattern) run inside a
// `for k := range v.Metadata` loop; SafeURL's per-value
// validators (@format(url), @maxLength) run inside a separate
// `for _, val := range v.Metadata` loop. Either side rejects.
func TestMapScalarInheritance(t *testing.T) {
	base := func(meta map[ordersapi.Tag]sharedapi.SafeURL) ordersapi.Order {
		return ordersapi.Order{
			ID:        "ord_ABC12345",
			Customer:  ordersapi.Customer{ID: "550e8400-e29b-41d4-a716-446655440000", Email: "x@y.com", Name: "X", PrimaryAddress: ordersapi.Address{Street: "x", City: "x", PostalCode: "94105", Country: "US"}},
			Items:     []ordersapi.LineItem{{ProductID: "p1", Sku: "SKU-AAAA", Quantity: 1, UnitCents: 2}},
			AuditedBy: usersapi.UserRef{ID: "u-admin", Name: "Admin"},
			TotalCents: 2,
			Status:    ordersapi.OrderStatusPending,
			Currency:  ptr(ordersapi.CurrencyCode("USD")),
			Tags:      []ordersapi.Tag{"web"},
			Metadata:  meta,
			CreatedAt: "2026-04-29T08:00:00Z",
		}
	}

	// Nil map → both for-range loops iterate zero times → pass.
	nilOrder := base(nil)
	if err := nilOrder.Validate(); err != nil {
		t.Fatalf("nil metadata should validate: %v", err)
	}

	// Empty map → same.
	emptyOrder := base(map[ordersapi.Tag]sharedapi.SafeURL{})
	if err := emptyOrder.Validate(); err != nil {
		t.Fatalf("empty metadata should validate: %v", err)
	}

	// Happy path - keys are kebab-lowercase Tag, values are URLs.
	good := base(map[ordersapi.Tag]sharedapi.SafeURL{
		"primary": "https://example.com/a",
		"backup":  "https://example.com/b",
	})
	if err := good.Validate(); err != nil {
		t.Fatalf("clean metadata should validate: %v", err)
	}

	// Bad KEY: uppercase fails Tag's @pattern.
	badKey := base(map[ordersapi.Tag]sharedapi.SafeURL{
		"Primary": "https://example.com/a",
	})
	if err := badKey.Validate(); err == nil {
		t.Errorf("uppercase key should fail Tag's @pattern via map-key loop")
	}

	// Bad KEY: empty string fails Tag's @minLength.
	emptyKey := base(map[ordersapi.Tag]sharedapi.SafeURL{
		"": "https://example.com/a",
	})
	if err := emptyKey.Validate(); err == nil {
		t.Errorf("empty key should fail Tag's @minLength via map-key loop")
	}

	// Bad VALUE: not a URL fails SafeURL's @format(url).
	badVal := base(map[ordersapi.Tag]sharedapi.SafeURL{
		"primary": "not-a-url",
	})
	if err := badVal.Validate(); err == nil {
		t.Errorf("malformed value should fail SafeURL's @format(url) via map-value loop")
	}
}

// TestMapValueArrayOfScalar pins the doubly-nested validator
// inheritance for `map<string, Tag[]>`. Each scalar decorator on
// Tag produces a `for _, val := range v.M { for i := range val
// { ... } }` pair so the per-element check fires on every Tag in
// every slice value across the whole map.
func TestMapValueArrayOfScalar(t *testing.T) {
	customer := func(channels map[string][]ordersapi.Tag) ordersapi.Customer {
		return ordersapi.Customer{
			ID:             "550e8400-e29b-41d4-a716-446655440000",
			Email:          "x@y.com",
			Name:           "X",
			PrimaryAddress: ordersapi.Address{Street: "x", City: "x", PostalCode: "94105", Country: "US"},
			Channels:       channels,
		}
	}

	// Nil / empty map → no inner iteration → pass.
	clean := customer(nil)
	if err := clean.Validate(); err != nil {
		t.Fatalf("nil channels should validate: %v", err)
	}

	// Happy path - every Tag matches the scalar's pattern.
	good := customer(map[string][]ordersapi.Tag{
		"email": {"welcome", "newsletter"},
		"sms":   {"otp"},
	})
	if err := good.Validate(); err != nil {
		t.Fatalf("clean channels should validate: %v", err)
	}

	// One bad element inside one slice - outer loop reaches the
	// bad bucket, inner loop hits the bad tag, @pattern fails.
	badInner := customer(map[string][]ordersapi.Tag{
		"email": {"welcome", "BadTag"}, // uppercase fails Tag's @pattern
	})
	if err := badInner.Validate(); err == nil {
		t.Errorf("uppercase tag inside a channel slice should fail Tag's @pattern via nested loop")
	}

	// Empty string in a different bucket - fails Tag's @minLength
	// from inside the inner loop.
	emptyInner := customer(map[string][]ordersapi.Tag{
		"sms": {""},
	})
	if err := emptyInner.Validate(); err == nil {
		t.Errorf("empty tag in nested slice should fail Tag's @minLength via inner loop")
	}

	// 33-char element - fails Tag's @maxLength from the inner loop.
	longInner := customer(map[string][]ordersapi.Tag{
		"email": {strings.Repeat("a", 33)},
	})
	if err := longInner.Validate(); err == nil {
		t.Errorf("33-char tag in nested slice should fail Tag's @maxLength via inner loop")
	}
}

// TestNestedMapScalarBothSides drives the recursive scalar walker
// through `index map<string, map<Tag, shared.SafeURL>>?` -
// validators inherited from BOTH inner sides (Tag on the inner
// key, shared.SafeURL on the inner value) cascade through a
// doubly-nested for-range emit. Either side rejecting a single
// element fails the whole Customer.
func TestNestedMapScalarBothSides(t *testing.T) {
	customer := func(idx map[string]map[ordersapi.Tag]sharedapi.SafeURL) ordersapi.Customer {
		return ordersapi.Customer{
			ID:             "550e8400-e29b-41d4-a716-446655440000",
			Email:          "x@y.com",
			Name:           "X",
			PrimaryAddress: ordersapi.Address{Street: "x", City: "x", PostalCode: "94105", Country: "US"},
			Index:          idx,
			GridLabels:     [][]ordersapi.Tag{{"row-1"}, {"row-2"}},
		}
	}

	// Happy path - every inner key matches Tag's pattern, every
	// inner value matches SafeURL's @format(url).
	good := customer(map[string]map[ordersapi.Tag]sharedapi.SafeURL{
		"primary": {"alpha": "https://example.com/a", "beta": "https://example.com/b"},
		"backup":  {"gamma": "https://example.com/c"},
	})
	if err := good.Validate(); err != nil {
		t.Fatalf("clean nested map should validate: %v", err)
	}

	// Bad inner KEY - uppercase fails Tag's @pattern, traversed
	// via the outer map's value iteration.
	badKey := customer(map[string]map[ordersapi.Tag]sharedapi.SafeURL{
		"primary": {"BadKey": "https://example.com"},
	})
	if err := badKey.Validate(); err == nil {
		t.Errorf("bad inner key should fail Tag's @pattern through nested map walker")
	}

	// Bad inner VALUE - non-URL fails SafeURL's @format(url).
	badVal := customer(map[string]map[ordersapi.Tag]sharedapi.SafeURL{
		"primary": {"alpha": "not-a-url"},
	})
	if err := badVal.Validate(); err == nil {
		t.Errorf("bad inner value should fail SafeURL's @format(url) through nested map walker")
	}
}

// TestMultiArrayScalarInheritance drives the recursive walker
// through `gridLabels Tag[][]` - a slice-of-slices that the
// extended parser/AST now accept. Each scalar decorator on Tag
// produces ONE doubly-nested for-loop pair, and a single bad
// element in any inner slice fails the wrapping struct.
func TestMultiArrayScalarInheritance(t *testing.T) {
	customer := func(grid [][]ordersapi.Tag) ordersapi.Customer {
		return ordersapi.Customer{
			ID:             "550e8400-e29b-41d4-a716-446655440000",
			Email:          "x@y.com",
			Name:           "X",
			PrimaryAddress: ordersapi.Address{Street: "x", City: "x", PostalCode: "94105", Country: "US"},
			GridLabels:     grid,
		}
	}

	// Happy path - every cell matches Tag's pattern.
	good := customer([][]ordersapi.Tag{
		{"row-1", "col-a"},
		{"row-2", "col-b", "col-c"},
	})
	if err := good.Validate(); err != nil {
		t.Fatalf("clean grid should validate: %v", err)
	}

	// Empty outer slice → no iteration → pass.
	nilCust := customer(nil)
	if err := nilCust.Validate(); err != nil {
		t.Fatalf("nil grid should validate: %v", err)
	}

	// Empty inner slice → outer iter visits but inner iter is
	// no-op → pass.
	emptyInner := customer([][]ordersapi.Tag{{}})
	if err := emptyInner.Validate(); err != nil {
		t.Fatalf("empty inner slice should validate: %v", err)
	}

	// Bad cell - uppercase fails Tag's @pattern via the inner
	// loop nested inside the outer loop.
	bad := customer([][]ordersapi.Tag{
		{"row-1", "BadCell"},
	})
	if err := bad.Validate(); err == nil {
		t.Errorf("bad cell should fail Tag's @pattern via doubly-nested loop")
	}

	// Bad cell deeper - second outer slice, first inner element.
	badDeeper := customer([][]ordersapi.Tag{
		{"row-1"},
		{strings.Repeat("a", 33)}, // fails @maxLength
	})
	if err := badDeeper.Validate(); err == nil {
		t.Errorf("33-char cell in second row should fail Tag's @maxLength via doubly-nested loop")
	}
}

// TestNumericPointerValidatorsCompile pins the numeric-validator
// codegen fix that handles pointer fields (T? / @nullable). Before
// the fix, `loyaltyPoints int @nullable @min(0)` produced
// `if v.LoyaltyPoints < 0` - invalid because *int can't be
// compared to an untyped int. The fix injects the nil-guard +
// deref so the comparison succeeds.
func TestNumericPointerValidatorsCompile(t *testing.T) {
	// Nil pointer → validators skip silently.
	customer := ordersapi.Customer{
		ID:             "550e8400-e29b-41d4-a716-446655440000",
		Email:          "alice@example.com",
		Name:           "Alice",
		PrimaryAddress: ordersapi.Address{Street: "x", City: "x", PostalCode: "94105", Country: "US"},
	}
	if err := customer.Validate(); err != nil {
		t.Fatalf("customer with nil loyaltyPoints should validate: %v", err)
	}

	// Out-of-range value → @max kicks in via deref.
	bad := 9_999_999
	customer.LoyaltyPoints = &bad
	if err := customer.Validate(); err == nil {
		t.Errorf("loyaltyPoints=%d should be rejected by @max(1000000)", bad)
	}

	// In-range → passes.
	good := 500
	customer.LoyaltyPoints = &good
	if err := customer.Validate(); err != nil {
		t.Errorf("loyaltyPoints=500 should pass: %v", err)
	}
}

// TestUserDomainErrorsConstruct exercises the generated error types
// in `internal/types/users/errors.go`. Each declared `error <Cat>
// <Name>` produces a typed Go struct with a `New<Name>Err(...)`
// constructor and Error() / HTTPStatus() methods, so logic can
// `return users.NewEmailTakenErr("a@b", nil)` and the error layer
// surfaces the right wire shape.
func TestUserDomainErrorsConstruct(t *testing.T) {
	// 404 - bodyless. The constructor takes no args; the framework's
	// `code` / `message` metadata is unexported and never on the wire.
	notFound := usersapi.NewUserNotFoundErr()
	if notFound.HTTPStatus() != 404 {
		t.Errorf("UserNotFoundErr.HTTPStatus() = %d, want 404", notFound.HTTPStatus())
	}
	if notFound.Error() == "" {
		t.Errorf("UserNotFoundErr.Error() returned empty string")
	}

	// 409 with body - EmailTaken carries the offending email and
	// optionally the existing user id. The constructor takes a single
	// EmailTakenBody struct; user-declared fields are accessed via the
	// embedded body.
	existing := "u_42"
	taken := usersapi.NewEmailTakenErr(usersapi.EmailTakenBody{
		Email:      "alice@example.com",
		ExistingID: &existing,
	})
	if taken.HTTPStatus() != 409 {
		t.Errorf("EmailTakenErr.HTTPStatus() = %d, want 409", taken.HTTPStatus())
	}
	if taken.Email != "alice@example.com" {
		t.Errorf("EmailTakenErr.Email = %q, want alice@example.com", taken.Email)
	}
	if taken.ExistingID == nil || *taken.ExistingID != "u_42" {
		t.Errorf("EmailTakenErr.ExistingID = %v, want pointer to u_42", taken.ExistingID)
	}

	// 422 ValidationFailed - fields slice + optional hint.
	vf := usersapi.NewValidationFailedErr(usersapi.ValidationFailedBody{
		Fields: []string{"/email", "/avatar/url"},
	})
	if vf.HTTPStatus() != 422 {
		t.Errorf("ValidationFailedErr.HTTPStatus() = %d, want 422", vf.HTTPStatus())
	}
	if len(vf.Fields) != 2 {
		t.Errorf("ValidationFailedErr.Fields = %v, want 2 entries", vf.Fields)
	}

	// 429 RateLimited - body struct carries user-declared wire fields.
	// User's `message` field is now an exported wire field on the body
	// struct; if the caller doesn't set it, it stays at the Go zero
	// value (callers wrap construction with their own helpers when
	// they want a default value applied automatically).
	rl := usersapi.NewRateLimitedErr(usersapi.RateLimitedBody{
		Message:    ptr("Slow down, please"),
		RetryAfter: 30,
	})
	if rl.HTTPStatus() != 429 {
		t.Errorf("RateLimitedErr.HTTPStatus() = %d, want 429", rl.HTTPStatus())
	}
	if rl.RetryAfter != 30 {
		t.Errorf("RateLimitedErr.RetryAfter = %d, want 30", rl.RetryAfter)
	}
	if rl.Message == nil || *rl.Message != "Slow down, please" {
		t.Errorf("RateLimitedErr.Message = %v, want \"Slow down, please\"", rl.Message)
	}

	// 412 StaleVersion - both versions echo back so the client can
	// reload + diff before retrying.
	sv := usersapi.NewStaleVersionErr(usersapi.StaleVersionBody{
		ExpectedVersion: 4,
		ActualVersion:   7,
	})
	if sv.HTTPStatus() != 412 {
		t.Errorf("StaleVersionErr.HTTPStatus() = %d, want 412", sv.HTTPStatus())
	}
	if sv.ExpectedVersion != 4 || sv.ActualVersion != 7 {
		t.Errorf("StaleVersionErr versions = (%d, %d), want (4, 7)", sv.ExpectedVersion, sv.ActualVersion)
	}
}

// TestUserDomainErrorsImplementErrorInterface confirms every
// generated error type satisfies Go's `error` interface so logic
// code can `return err` without per-type wrapping. The framework
// relies on this for the `writeError(w, err)` helper that maps
// typed errors to their declared HTTP status.
func TestUserDomainErrorsImplementErrorInterface(t *testing.T) {
	// Each entry doubles as a smoke check that the generated code
	// compiles with the right interface - the slice literal forces
	// every value into an `error` slot.
	errs := []error{
		usersapi.NewUserNotFoundErr(),
		usersapi.NewUserGoneErr(),
		usersapi.NewInvalidTokenErr(),
		usersapi.NewInternalError(),
		usersapi.NewEmailTakenErr(usersapi.EmailTakenBody{Email: "a@b"}),
		usersapi.NewRateLimitedErr(usersapi.RateLimitedBody{RetryAfter: 1}),
		usersapi.NewValidationFailedErr(usersapi.ValidationFailedBody{}),
	}
	for i, e := range errs {
		if e.Error() == "" {
			t.Errorf("errs[%d] Error() returned empty string", i)
		}
	}
}

// TestUserDomainErrorsJSONShape pins the on-the-wire JSON shape under
// the new design: only user-declared body fields are marshalled - the
// framework's internal `code` / `message` are unexported and skipped
// by encoding/json. EmailTaken's body declares only `email` and an
// optional `existingId`, so the JSON wire echoes those alone. Clients
// who want the canonical machine-readable code on the wire declare
// TestDefaultsShowcasePreFill drives the generated DefaultsShowcase
// handler with an empty body and asserts every supported @default
// shape lands in the request struct before the logic runs. The
// handler is invoked directly (no live network) so the test stays
// hermetic; the logic just echoes the request, so the JSON response
// reflects what the framework pre-filled.
func TestDefaultsShowcasePreFill(t *testing.T) {
	cfg := &config.Config{}
	svc := svccontext.NewServiceContext(cfg)
	handler := orderservice.DefaultsShowcase(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/defaults", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got ordersapi.DefaultsShowcaseReq
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}

	// Plain primitives - all pointers now since fields with @default
	// are auto-marked optional by the formatter.
	if got.Str == nil || *got.Str != "anon" {
		t.Errorf("Str = %v, want non-nil &\"anon\"", got.Str)
	}
	if got.Num == nil || *got.Num != 20 {
		t.Errorf("Num = %v, want non-nil &20", got.Num)
	}
	if got.Flag == nil || *got.Flag != true {
		t.Errorf("Flag = %v, want non-nil &true", got.Flag)
	}
	// Optional pre-fill: pointer should be non-nil pointing at the default.
	if got.Maybe == nil || *got.Maybe != "opt-default" {
		t.Errorf("Maybe = %v, want non-nil &\"opt-default\"", got.Maybe)
	}
	// Scalar wrapping primitive.
	if got.Currency == nil || *got.Currency != "USD" {
		t.Errorf("Currency = %v, want non-nil &\"USD\"", got.Currency)
	}
	// Enum value.
	if got.Status == nil || *got.Status != ordersapi.OrderStatusPending {
		t.Errorf("Status = %v, want non-nil &OrderStatusPending", got.Status)
	}
	// Empty array preset. omitempty + empty slice round-trips as
	// JSON absence on the wire, so the decoded struct lands with
	// nil (not a non-nil empty slice). Both shapes are semantically
	// "no items" - the test treats them as equivalent.
	if len(got.Tags) != 0 {
		t.Errorf("Tags = %v, want empty/nil", got.Tags)
	}
	// Non-empty string array.
	if len(got.Preset) != 2 || got.Preset[0] != "standard" || got.Preset[1] != "expedited" {
		t.Errorf("Preset = %v, want [standard expedited]", got.Preset)
	}
	// Enum array.
	if len(got.AllowedMethods) != 2 ||
		got.AllowedMethods[0] != ordersapi.PaymentMethodCard ||
		got.AllowedMethods[1] != ordersapi.PaymentMethodBank {
		t.Errorf("AllowedMethods = %v, want [Card Bank]", got.AllowedMethods)
	}
}

// `code string @default("...")` in the DSL (then it surfaces as the
// exported `Code` body field).
func TestUserDomainErrorsJSONShape(t *testing.T) {
	taken := usersapi.NewEmailTakenErr(usersapi.EmailTakenBody{
		Email: "alice@example.com",
	})
	out, err := json.Marshal(taken)
	if err != nil {
		t.Fatalf("marshal EmailTakenErr: %v", err)
	}
	js := string(out)
	if !strings.Contains(js, `"email":"alice@example.com"`) {
		t.Errorf("EmailTakenErr JSON missing email: %s", js)
	}
	for _, forbidden := range []string{`"code"`, `"message"`} {
		if strings.Contains(js, forbidden) {
			t.Errorf("EmailTakenErr JSON should not contain %s (internal-only): %s", forbidden, js)
		}
	}
}
