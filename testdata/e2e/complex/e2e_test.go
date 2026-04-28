// Package complex is the deeply-nested e2e fixture. It boots the
// generated ProfileService and exercises nested types, every validator,
// and asserts the shape of the generated types/validate/openapi files.
package complex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dropship-dev/craftgo/pkg/server"

	"github.com/dropship-dev/craftgo/testdata/e2e/complex/internal/middleware"
	adminroutes "github.com/dropship-dev/craftgo/testdata/e2e/complex/internal/routes/admin-service"
	"github.com/dropship-dev/craftgo/testdata/e2e/complex/internal/routes/profile-service"
	types "github.com/dropship-dev/craftgo/testdata/e2e/complex/internal/types/design"
	"github.com/dropship-dev/craftgo/testdata/e2e/complex/svccontext"
)

// boot stands up the generated routes against an httptest server.
// Every middleware field on ServiceContext must be non-nil before
// RegisterRoutes runs (the generated wrappers call svcCtx.X(...)
// directly), so we wire each field even when the test only exercises
// unprotected endpoints.
func boot(t *testing.T) (*httptest.Server, *svccontext.ServiceContext) {
	t.Helper()
	svc := svccontext.NewServiceContext()
	svc.AuthRequired = middleware.NewAuthRequiredMiddleware("Bearer secret-token")
	svc.RequestStamp = middleware.NewRequestStampMiddleware()
	srv := server.New(svc, server.WithoutDefaultHealth())
	profileservice.RegisterRoutes(srv, svc)
	adminroutes.RegisterRoutes(srv, svc)
	ts := httptest.NewServer(srv.Mux())
	t.Cleanup(ts.Close)
	return ts, svc
}

// bootSecure wires both ProfileService AND AdminService onto one Server
// with the AuthRequired middleware REGISTERED via
// Server.RegisterMiddleware. The implementation now lives in the
// generated `internal/middleware` scaffold (filled-in by hand on the
// first gen, never overwritten), so the test just imports the function
// and registers it under the same name the DSL declared.
func bootSecure(t *testing.T, _ string) (*httptest.Server, *svccontext.ServiceContext) {
	t.Helper()
	svc := svccontext.NewServiceContext()
	srv := server.New(svc, server.WithoutDefaultHealth())
	// Middleware lives in `internal/middleware/` (scaffold-once) and is
	// referenced via the embedded Middlewares struct on ServiceContext.
	// Routes don't need RegisterMiddleware — they reach the wired
	// values via svcCtx.<Name> directly.
	svc.AuthRequired = middleware.NewAuthRequiredMiddleware("Bearer secret-token")
	svc.RequestStamp = middleware.NewRequestStampMiddleware()
	profileservice.RegisterRoutes(srv, svc)
	adminroutes.RegisterRoutes(srv, svc)
	ts := httptest.NewServer(srv.Mux())
	t.Cleanup(ts.Close)
	return ts, svc
}

// httpJSON issues a JSON request and returns (status, body) for the
// caller to inspect.
func httpJSON(t *testing.T, ts *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// validProfile returns a CreateProfileReq that satisfies every validator.
// Each call uses a unique email so the in-memory store's duplicate-email
// check doesn't reject parallel test profiles.
func validProfile(name string) types.CreateProfileReq {
	lat := 10.5
	lng := 105.8
	return types.CreateProfileReq{
		DisplayName: name,
		Contacts: types.ContactInfo{
			Email: name + "@example.com",
		},
		Addresses: []types.Address{
			{
				Street:  "1 Main St",
				City:    "HCM",
				Country: "VN",
				Coords:  &types.Coords{Lat: lat, Lng: lng},
			},
		},
		Tags: []string{"vip"},
		Meta: map[string]string{"team": "core"},
	}
}

func TestComplexCreateAndGet(t *testing.T) {
	ts, _ := boot(t)
	status, body := httpJSON(t, ts, http.MethodPost, "/api/v1/profiles", validProfile("alice"))
	if status != http.StatusOK {
		t.Fatalf("CreateProfile status %d: %s", status, body)
	}
	var created types.Profile
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Errorf("expected server-assigned id, got empty")
	}
	if created.Contacts.Email != "alice@example.com" {
		t.Errorf("nested contact lost in roundtrip: %+v", created.Contacts)
	}
	if len(created.Addresses) != 1 {
		t.Fatalf("expected 1 address, got %d", len(created.Addresses))
	}
	if created.Addresses[0].Coords == nil || created.Addresses[0].Coords.Lat != 10.5 {
		t.Errorf("nested coords lost: %+v", created.Addresses[0].Coords)
	}

	status, body = httpJSON(t, ts, http.MethodGet, "/api/v1/profiles/"+created.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("GetProfile status %d: %s", status, body)
	}
	var got types.Profile
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "alice" || got.Contacts.Email != "alice@example.com" {
		t.Errorf("get returned wrong shape: %+v", got)
	}
}

func TestComplexListReturnsItems(t *testing.T) {
	ts, _ := boot(t)
	for _, name := range []string{"a", "b", "c"} {
		_, _ = httpJSON(t, ts, http.MethodPost, "/api/v1/profiles", validProfile(name))
	}
	status, body := httpJSON(t, ts, http.MethodGet, "/api/v1/profiles", nil)
	if status != http.StatusOK {
		t.Fatalf("ListProfiles status %d: %s", status, body)
	}
	var page types.ListProfilesResp
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Items) != 3 {
		t.Errorf("expected 3 items, got %d (total %d)", len(page.Items), page.Total)
	}
}

func TestComplexValidationRejectsBadEmail(t *testing.T) {
	ts, _ := boot(t)
	req := validProfile("alice")
	req.Contacts.Email = "not-an-email"
	status, body := httpJSON(t, ts, http.MethodPost, "/api/v1/profiles", req)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 (nested ContactInfo.Validate), got %d body=%s", status, body)
	}
	if !strings.Contains(string(body), "email") {
		t.Errorf("expected error to mention `email`, got: %s", body)
	}
}

// TestComplexValidationRejectsBadCoords confirms the recursive validator
// reaches Address[].Coords.Validate() and surfaces the @range failure.
func TestComplexValidationRejectsBadCoords(t *testing.T) {
	ts, _ := boot(t)
	req := validProfile("alice")
	bad := 999.0
	req.Addresses[0].Coords = &types.Coords{Lat: bad, Lng: 0}
	status, body := httpJSON(t, ts, http.MethodPost, "/api/v1/profiles", req)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 (deep nested Coords.Validate), got %d body=%s", status, body)
	}
	if !strings.Contains(string(body), "lat") {
		t.Errorf("expected error to mention `lat`, got: %s", body)
	}
}

func TestComplexValidationRejectsEmptyAddresses(t *testing.T) {
	ts, _ := boot(t)
	req := validProfile("alice")
	req.Addresses = nil
	status, body := httpJSON(t, ts, http.MethodPost, "/api/v1/profiles", req)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 (minItems), got %d body=%s", status, body)
	}
	if !strings.Contains(string(body), "addresses") {
		t.Errorf("expected message to mention `addresses`, got: %s", body)
	}
}

func TestComplexValidationRejectsTooManyAddresses(t *testing.T) {
	ts, _ := boot(t)
	req := validProfile("alice")
	tooMany := make([]types.Address, 6)
	for i := range tooMany {
		tooMany[i] = req.Addresses[0]
	}
	req.Addresses = tooMany
	status, _ := httpJSON(t, ts, http.MethodPost, "/api/v1/profiles", req)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 (maxItems), got %d", status)
	}
}

func TestComplexValidationRejectsLongDisplayName(t *testing.T) {
	ts, _ := boot(t)
	req := validProfile(strings.Repeat("a", 100))
	status, body := httpJSON(t, ts, http.MethodPost, "/api/v1/profiles", req)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 (length), got %d body=%s", status, body)
	}
}

func TestComplexGetMissingReturns404(t *testing.T) {
	ts, _ := boot(t)
	status, _ := httpJSON(t, ts, http.MethodGet, "/api/v1/profiles/ghost", nil)
	if status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", status)
	}
}

// TestComplexDuplicateEmailReturns409 covers the business-rule 409 path:
// CreateProfile detects the email already exists and returns the typed
// DuplicateEmail error.
func TestComplexDuplicateEmailReturns409(t *testing.T) {
	ts, _ := boot(t)
	first := validProfile("alice")
	if status, _ := httpJSON(t, ts, http.MethodPost, "/api/v1/profiles", first); status != http.StatusOK {
		t.Fatalf("first create failed: %d", status)
	}
	// Second request with the same email should hit the duplicate path.
	dup := validProfile("alice2")
	dup.Contacts.Email = first.Contacts.Email
	status, body := httpJSON(t, ts, http.MethodPost, "/api/v1/profiles", dup)
	if status != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", status, body)
	}
	var payload struct {
		Code    string `json:"code"`
		Email   string `json:"email"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "DUPLICATE_EMAIL" {
		t.Errorf("expected code DUPLICATE_EMAIL, got %q", payload.Code)
	}
	if payload.Email != first.Contacts.Email {
		t.Errorf("error did not echo the offending email: %+v", payload)
	}
}

// TestComplexReservedNameReturns422 covers the business-validation 422
// path with the structured `fields` payload.
func TestComplexReservedNameReturns422(t *testing.T) {
	ts, _ := boot(t)
	req := validProfile("admin")
	status, body := httpJSON(t, ts, http.MethodPost, "/api/v1/profiles", req)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", status, body)
	}
	var payload struct {
		Code   string   `json:"code"`
		Fields []string `json:"fields"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "PROFILE_VALIDATION_FAILED" {
		t.Errorf("expected PROFILE_VALIDATION_FAILED, got %q", payload.Code)
	}
	if len(payload.Fields) == 0 || payload.Fields[0] != "displayName" {
		t.Errorf("expected fields=[displayName], got %v", payload.Fields)
	}
}

// TestComplexRateLimitReturns429 covers the demo-quota 429 path. The
// CreateProfile logic raises RateLimited every fifth attempt; we issue
// five with unique emails and assert the last one is 429 with retry_after.
func TestComplexRateLimitReturns429(t *testing.T) {
	ts, _ := boot(t)
	var lastStatus int
	var lastBody []byte
	for i := 1; i <= 5; i++ {
		req := validProfile(fmt.Sprintf("user%d", i))
		lastStatus, lastBody = httpJSON(t, ts, http.MethodPost, "/api/v1/profiles", req)
	}
	if lastStatus != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 5th create, got %d body=%s", lastStatus, lastBody)
	}
	var payload struct {
		Code       string `json:"code"`
		RetryAfter int    `json:"retry_after"`
	}
	if err := json.Unmarshal(lastBody, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "RATE_LIMITED" || payload.RetryAfter != 30 {
		t.Errorf("unexpected payload: %+v", payload)
	}
}

// TestComplexCustomMessageDesignTime confirms the `@default("...")` form
// on the message field replaces the category default at gen time.
// RateLimited's design declares `message string @default("Slow down, please")`
// so every NewRateLimitedErr starts with that message.
func TestComplexCustomMessageDesignTime(t *testing.T) {
	got := types.NewRateLimitedErr(7).Message
	if got != "Slow down, please" {
		t.Errorf("design-time @default override missed; got %q", got)
	}
}

// TestComplexCustomMessageRuntime confirms the generated WithMessage
// fluent setter overrides the canonical message per call.
// GetProfile's logic returns NewProfileNotFoundErr().WithMessage(...).
func TestComplexCustomMessageRuntime(t *testing.T) {
	ts, _ := boot(t)
	status, body := httpJSON(t, ts, http.MethodGet, "/api/v1/profiles/ghost", nil)
	if status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", status)
	}
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "PROFILE_NOT_FOUND" {
		t.Errorf("code should stay canonical, got %q", payload.Code)
	}
	if payload.Message != "Profile ghost does not exist" {
		t.Errorf("WithMessage runtime override missed; got %q", payload.Message)
	}
}

// TestComplexWithCodeRuntime confirms the WithCode fluent setter swaps
// the machine-readable code while leaving the rest of the payload alone.
func TestComplexWithCodeRuntime(t *testing.T) {
	got := types.NewProfileNotFoundErr().WithCode("CUSTOM_CODE")
	if got.Code != "CUSTOM_CODE" {
		t.Errorf("WithCode missed; got %q", got.Code)
	}
}

// TestComplexAllBindingsRoundtrip is the every-binding-kind test. It
// sends ONE request that exercises path + query + header + cookie + body
// and asserts each value arrives at the handler via the matching part of
// the HTTP request — proves the codegen wires every binder correctly.
func TestComplexAllBindingsRoundtrip(t *testing.T) {
	ts, _ := boot(t)
	body, _ := json.Marshal(types.PatchProfileReq{DisplayName: "alice"})
	req, err := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/profiles/p42?dryRun=true", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("idempotencyKey", "key-xyz")
	req.AddCookie(&http.Cookie{Name: "sessionToken", Value: "tok-abc"})

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	var got types.PatchProfileResp
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := types.PatchProfileResp{
		ID:             "p42",
		DryRun:         "true",
		IdempotencyKey: "key-xyz",
		SessionToken:   "tok-abc",
		DisplayName:    "alice",
	}
	if got != want {
		t.Errorf("binding mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

// TestComplexAllBindingsValidation confirms request-level validation
// fires when one of the parameter-bound fields fails its rules — proves
// Validate() runs after path/query/header/cookie binding too, not just
// body.
func TestComplexAllBindingsValidation(t *testing.T) {
	ts, _ := boot(t)
	body, _ := json.Marshal(types.PatchProfileReq{DisplayName: ""})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/profiles/p42?dryRun=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400 for empty displayName, got %d: %s", resp.StatusCode, raw)
	}
}

// TestSecurityRejectsMissingToken proves the @middlewares(AuthRequired)
// decorator wires the bearer-token middleware in front of
// AdminListProfiles — and that the unauthenticated 401 carries the
// structured error payload.
func TestSecurityRejectsMissingToken(t *testing.T) {
	ts, _ := bootSecure(t, "secret-token")
	resp, err := ts.Client().Get(ts.URL + "/api/v1/admin/profiles")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "UNAUTHORIZED" {
		t.Errorf("expected UNAUTHORIZED, got %q", payload.Code)
	}
}

// TestSecurityRejectsWrongToken proves the comparison is exact — a
// bearer header with a non-matching value still fails.
func TestSecurityRejectsWrongToken(t *testing.T) {
	ts, _ := bootSecure(t, "secret-token")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/admin/profiles", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// TestSecurityAcceptsValidToken proves a correctly authenticated request
// passes through the middleware and reaches the generated handler.
func TestSecurityAcceptsValidToken(t *testing.T) {
	ts, _ := bootSecure(t, "secret-token")
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/admin/profiles", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, raw)
	}
	var page types.ListProfilesResp
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.Total < 0 {
		t.Errorf("response shape unexpected: %+v", page)
	}
}

// TestSecurityUnprotectedRouteStaysOpen proves the gating is
// PER-METHOD: routes WITHOUT @middlewares(AuthRequired) keep accepting
// unauthenticated requests on the same Server instance.
func TestSecurityUnprotectedRouteStaysOpen(t *testing.T) {
	ts, _ := bootSecure(t, "secret-token")
	resp, err := ts.Client().Get(ts.URL + "/api/v1/profiles")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("unprotected /profiles should be open, got %d body=%s", resp.StatusCode, raw)
	}
}

// TestCrossFolderAdminPathRoutes verifies a service defined in
// `design/admin/admin-service.craftgo` (a separate folder from
// api.craftgo) generates routes whose path matches the design exactly:
// basePath(/api) + @prefix(/v1) + @group(admin) + method-path. Both the
// declared `/dashboard` and the path-less `Health` endpoint must show up.
func TestCrossFolderAdminPathRoutes(t *testing.T) {
	src := readGenerated(t, "internal/routes/admin-service/routes.go")
	for _, want := range []string{
		`"GET /api/v1/admin/dashboard"`,
		`"GET /api/v1/admin/health"`,
		// Service-level @middlewares wraps every method via embedded
		// fields on ServiceContext.
		`svcCtx.AuthRequired(handler.DashboardStatsHandler(svcCtx))`,
		`svcCtx.AuthRequired(handler.HealthHandler(svcCtx))`,
		// Method-level @middlewares chain on top of service-level.
		`svcCtx.AuthRequired(svcCtx.RequestStamp(handler.SnapshotHandler(svcCtx)))`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("expected %q in cross-folder routes.go:\n%s", want, src)
		}
	}
}

// TestCrossFolderAdminEndToEnd hits the actual HTTP endpoint to prove
// the cross-folder service definition reaches the wire correctly. It
// also exercises service-level @middlewares — every method on
// AdminService inherits AuthRequired, even the ones with no inline
// `@middlewares(...)` decorator.
func TestCrossFolderAdminEndToEnd(t *testing.T) {
	ts, _ := bootSecure(t, "secret-token")

	// Without bearer → 401.
	resp, err := ts.Client().Get(ts.URL + "/api/v1/admin/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("dashboard without bearer: expected 401, got %d", resp.StatusCode)
	}

	// With bearer → 200.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/admin/dashboard", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("dashboard with bearer: expected 200, got %d body=%s", resp.StatusCode, raw)
	}
	var stats types.ListProfilesResp
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if stats.Total < 0 {
		t.Errorf("unexpected stats payload: %+v", stats)
	}

	// Path-less Health method also reachable through the @group prefix.
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/admin/health", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("admin health: expected 204, got %d", resp.StatusCode)
	}
}

// TestComplexGlobalMiddlewareViaUse confirms the runtime path for
// global middleware: anything registered via `srv.Use(...)` wraps EVERY
// request — protected and unprotected alike — without touching the
// design files. The test installs a counter middleware and asserts it
// fires for both an unprotected GET (/api/v1/profiles) and a protected
// GET (/api/v1/admin/profiles via bearer).
func TestComplexGlobalMiddlewareViaUse(t *testing.T) {
	svc := svccontext.NewServiceContext()
	svc.AuthRequired = middleware.NewAuthRequiredMiddleware("Bearer secret-token")
	svc.RequestStamp = middleware.NewRequestStampMiddleware()

	var hits int32
	counter := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			next.ServeHTTP(w, r)
		})
	}

	srv := server.New(svc, server.WithoutDefaultHealth())
	srv.Use(counter)
	profileservice.RegisterRoutes(srv, svc)
	adminroutes.RegisterRoutes(srv, svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Unprotected route — counter must fire.
	resp, err := ts.Client().Get(ts.URL + "/api/v1/profiles")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Protected route with bearer — counter must fire here too,
	// because Use is OUTERMOST (it wraps the entire mux, including
	// per-method @middlewares chains).
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/admin/profiles", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("global middleware ran %d times, want 2", got)
	}
}

// TestComplexTagsDecoratorPropagation confirms the @tags decorator is
// honoured at both service and method level: AdminService declares
// @tags(admin, ops) and Snapshot adds @tags(snapshot), so the resulting
// operation should carry all three. ProfileService methods (no tags)
// keep the default (the service name).
func TestComplexTagsDecoratorPropagation(t *testing.T) {
	src := readGenerated(t, "docs/openapi.yaml")
	// Snapshot inherits service-level tags + its own.
	for _, want := range []string{
		"operationId: Snapshot",
		"- admin",
		"- ops",
		"- snapshot",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("Snapshot op missing tag %q in:\n%s", want, src[:min(len(src), 800)])
		}
	}
	// ProfileService methods keep the default (service name) tag.
	if !strings.Contains(src, "- ProfileService") {
		t.Errorf("expected default ProfileService tag fallback")
	}
}

// TestSecurityDecoratorInOpenAPI confirms the @security(...) decorator
// produces the matching `security:` clause on the operation AND a
// `components.securitySchemes` entry. The complex DSL declares
// @security(AuthRequired) on AdminListProfiles inside ProfileService.
func TestSecurityDecoratorInOpenAPI(t *testing.T) {
	src := readGenerated(t, "docs/openapi.yaml")
	for _, want := range []string{
		// The security scheme is registered once in components.
		"securitySchemes:",
		"AuthRequired:",
		"scheme: bearer",
		"type: http",
		// AdminListProfiles operation must carry the requirement.
		"operationId: AdminListProfiles",
		"- AuthRequired: []",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("openapi.yaml missing %q", want)
		}
	}
}

// TestMiddlewareScaffoldExists confirms the codegen scaffolded a file
// for every `middleware Name` declared in the DSL and that the
// hand-filled implementation survived the last `craftgo gen` run
// (gen-once / skip-if-exists). The assertion looks for a sentinel string
// that only the user-filled body contains — if the codegen had
// overwritten the file, the bearer check would be replaced by the
// no-op TODO scaffold and the marker would disappear.
func TestMiddlewareScaffoldExists(t *testing.T) {
	src := readGenerated(t, "internal/middleware/auth-required-middleware.go")
	// Sentinel only the user-filled body contains; if the codegen had
	// re-overwritten the file, this string would be replaced by the
	// no-op TODO scaffold.
	if !strings.Contains(src, "expectedAuthHeader") {
		t.Errorf("user-filled AuthRequired body lost — gen-once contract broken:\n%s", src)
	}
	if !strings.Contains(src, "func NewAuthRequiredMiddleware(") {
		t.Errorf("middleware constructor signature changed:\n%s", src)
	}
}

// TestSecurityRoutesGoFileWrapsHandler scans the generated routes.go to
// confirm the codegen actually emitted the `svcCtx.AuthRequired(...)`
// wrapper for the protected method.
func TestSecurityRoutesGoFileWrapsHandler(t *testing.T) {
	src := readGenerated(t, "internal/routes/profile-service/routes.go")
	if !strings.Contains(src, `svcCtx.AuthRequired(handler.AdminListProfilesHandler(svcCtx))`) {
		t.Errorf("expected svcCtx.AuthRequired wrapper in:\n%s", src)
	}
	// Unprotected routes must NOT carry the wrapper.
	if strings.Contains(src, `svcCtx.AuthRequired(handler.ListProfilesHandler(svcCtx))`) {
		t.Errorf("unprotected ListProfiles wrongly wrapped:\n%s", src)
	}
}

// TestComplexErrorTypesShape pins the constructor surfaces of every
// generated business error so a future refactor cannot silently change
// the number/order of arguments.
func TestComplexErrorTypesShape(t *testing.T) {
	dup := types.NewDuplicateEmailErr("a@b.com")
	if dup.HTTPStatus() != http.StatusConflict || dup.Code != "DUPLICATE_EMAIL" {
		t.Errorf("DuplicateEmail mis-shaped: %+v", dup)
	}
	val := types.NewProfileValidationFailedErr([]string{"x"})
	if val.HTTPStatus() != http.StatusUnprocessableEntity {
		t.Errorf("ProfileValidationFailed status: %d", val.HTTPStatus())
	}
	rl := types.NewRateLimitedErr(7)
	if rl.HTTPStatus() != http.StatusTooManyRequests || rl.RetryAfter != 7 {
		t.Errorf("RateLimited mis-shaped: %+v", rl)
	}
	perm := types.NewInsufficientPermissionsErr("admin")
	if perm.HTTPStatus() != http.StatusForbidden || perm.RequiredRole != "admin" {
		t.Errorf("InsufficientPermissions mis-shaped: %+v", perm)
	}
	stale := types.NewStaleVersionErr(2, 5)
	if stale.HTTPStatus() != http.StatusPreconditionFailed || stale.ExpectedVersion != 2 || stale.ActualVersion != 5 {
		t.Errorf("StaleVersion mis-shaped: %+v", stale)
	}
	nf := types.NewProfileNotFoundErr()
	if nf.HTTPStatus() != http.StatusNotFound {
		t.Errorf("ProfileNotFound status: %d", nf.HTTPStatus())
	}
}

// TestComplexGeneratedTypesShape pins the exact Go layout of the deeply
// nested types so a refactor can't quietly change the wire contract.
func TestComplexGeneratedTypesShape(t *testing.T) {
	src := readGenerated(t, "internal/types/design/types.go")
	// gofmt collapses contiguous whitespace, so we normalise the file
	// before searching to make assertions resilient to layout shifts.
	norm := strings.Join(strings.Fields(src), " ")
	for _, want := range []string{
		"type Coords struct",
		"Lat float64",
		"Lng float64",
		"type Address struct",
		"Coords *Coords",
		"type ContactInfo struct",
		"Email string",
		"Phone *string",
		"type Profile struct",
		"Contacts ContactInfo",
		"Addresses []Address",
		"Meta map[string]string",
		"type CreateProfileReq struct",
		"type GetProfileReq struct",
		"type ListProfilesResp struct",
		"Items []Profile",
	} {
		if !strings.Contains(norm, want) {
			t.Errorf("types.go missing %q", want)
		}
	}
}

// TestComplexGeneratedValidatorShape pins the exact Validate() bodies the
// codegen emits so silent regressions in validate.tmpl are caught fast.
func TestComplexGeneratedValidatorShape(t *testing.T) {
	src := readGenerated(t, "internal/types/design/validate.go")
	for _, want := range []string{
		"func (v *CreateProfileReq) Validate() error",
		`displayName: required`,
		`displayName: length out of range [1, 50]`,
		`addresses: minItems 1`,
		`addresses: maxItems 5`,
		`tags: maxItems 10`,
		"func (v *Coords) Validate() error",
		`lat: out of range [-90, 90]`,
		`lng: out of range [-180, 180]`,
		"func (v *Address) Validate() error",
		`country: length out of range [2, 2]`,
		"func (v *ContactInfo) Validate() error",
		"regexp.MustCompile",
		`email: required`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("validate.go missing %q", want)
		}
	}
}

// TestComplexGeneratedOpenAPIShape verifies the OpenAPI doc reflects every
// nested $ref and that the path operations end up under the right verb.
func TestComplexGeneratedOpenAPIShape(t *testing.T) {
	src := readGenerated(t, "docs/openapi.yaml")
	for _, want := range []string{
		"openapi: 3.1.0",
		"#/components/schemas/Profile",
		"#/components/schemas/Address",
		"#/components/schemas/Coords",
		"#/components/schemas/ContactInfo",
		"#/components/schemas/ListProfilesResp",
		// Body / Query get grouped <Method>Req<Kind> schemas.
		"CreateProfileReqBody:",
		// Path / Header / Cookie stay inline as parameters — no schema.
		"$ref: '#/components/schemas/CreateProfileReqBody'",
		// Response side mirrors the convention.
		"CreateProfileRespBody:",
		"GetProfileRespBody:",
		"ListProfilesRespBody:",
		"$ref: '#/components/schemas/CreateProfileRespBody'",
		"operationId: CreateProfile",
		"operationId: GetProfile",
		"operationId: ListProfiles",
		"/api/v1/profiles",
		"/api/v1/profiles/{id}",
		// GET /api/v1/profiles/{id} exposes id as a path parameter,
		// not a request body or a grouped schema.
		"in: path",
		"name: id",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("openapi.yaml missing %q", want)
		}
	}
}

// readGenerated reads a file relative to this test's directory.
func readGenerated(t *testing.T, rel string) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	dir := strings.TrimSuffix(here, "/e2e_test.go")
	data, err := os.ReadFile(dir + "/" + rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}
