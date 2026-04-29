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
	"strings"
	"testing"

	ordersapi "github.com/dropship-dev/craftgo/example/internal/types/orders"
	projectsapi "github.com/dropship-dev/craftgo/example/internal/types/projects"
	sharedapi "github.com/dropship-dev/craftgo/example/internal/types/shared"
	tasksapi "github.com/dropship-dev/craftgo/example/internal/types/tasks"
	usersapi "github.com/dropship-dev/craftgo/example/internal/types/users"
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
		Assignee: &assignee, // optional — present here.
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
// `if v.Avatar != nil { (*v.Avatar).Validate() }` — this test pins
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
// l := len(*v.Name); ...` — exercising both nil and present cases
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
// code for — see the comment in users/contact.craftgo. The
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
// Deep-nest + scalars — orders package showcase.
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
		Items:      []ordersapi.LineItem{{ProductID: "p1", Sku: "SKU-AAAA", Quantity: 1, UnitCents: 999}},
		AuditedBy:  usersapi.UserRef{ID: "u-admin", Name: "Admin"},
		TotalCents: 999,
		Status:     ordersapi.OrderStatusPending,
		Currency:   "USD",
		Tags:       []string{"web"},
		CreatedAt:  "2026-04-29T08:00:00Z",
	}
	if err := order.Validate(); err != nil {
		t.Fatalf("happy-path order should validate: %v", err)
	}

	// Each level's own Validate() is callable independently — that's
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
		ReceiptURL:  ptr[sharedapi.SafeURL]("https://example.com/r/1"),
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
// phone)`) — only the array-shortcut form
// (`@requiresOneOf(["email", "phone"])`) was wired through. The
// example's Contact type uses the bare-ident form deliberately so
// this test catches a regression on either side.
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
// exclusive — flipping both should be rejected.
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

// TestNumericPointerValidatorsCompile pins the numeric-validator
// codegen fix that handles pointer fields (T? / @nullable). Before
// the fix, `loyaltyPoints int @nullable @min(0)` produced
// `if v.LoyaltyPoints < 0` — invalid because *int can't be
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
