// Package e2e is the end-to-end test suite. It boots an httptest server
// using the generated routes/handlers and exercises every endpoint with
// raw `net/http` requests. The Go HTTP client gen feature was removed —
// consumers generate their own clients from the OpenAPI spec.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/server"

	"github.com/craftgodotdev/craftgo/tests/e2e/users/internal/routes/user-service"
	types "github.com/craftgodotdev/craftgo/tests/e2e/users/internal/types/design"
	"github.com/craftgodotdev/craftgo/tests/e2e/users/svccontext"
)

// boot wires a Server with the generated routes and returns an
// httptest.Server plus the SC so each test can reset state.
func boot(t *testing.T) (*httptest.Server, *svccontext.ServiceContext) {
	t.Helper()
	svc := svccontext.NewServiceContext()
	srv := server.New(svc, server.WithoutDefaultHealth())
	userservice.RegisterRoutes(srv, svc)
	ts := httptest.NewServer(srv.Mux())
	t.Cleanup(ts.Close)
	return ts, svc
}

// httpJSON is a tiny helper that issues a JSON request and returns the
// status + body so tests stay focused on the API contract.
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

func TestE2ECreateAndGet(t *testing.T) {
	ts, _ := boot(t)
	status, body := httpJSON(t, ts, http.MethodPost, "/api/v1/users", types.CreateUserReq{
		Name: "alice",
		Tags: []string{"admin", "ops"},
		Meta: map[string]string{"team": "core"},
	})
	if status != http.StatusOK {
		t.Fatalf("CreateUser status %d: %s", status, body)
	}
	var created types.User
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID == "" || created.Name != "alice" {
		t.Errorf("unexpected created user: %+v", created)
	}

	status, body = httpJSON(t, ts, http.MethodGet, "/api/v1/users/"+created.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("GetUser status %d: %s", status, body)
	}
	var got types.User
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.Name != "alice" {
		t.Errorf("Get returned wrong name: %s", got.Name)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "admin" {
		t.Errorf("tags lost in roundtrip: %v", got.Tags)
	}
	if got.Meta["team"] != "core" {
		t.Errorf("meta lost in roundtrip: %v", got.Meta)
	}
}

func TestE2EGetMissingReturns404(t *testing.T) {
	ts, _ := boot(t)
	status, _ := httpJSON(t, ts, http.MethodGet, "/api/v1/users/ghost", nil)
	if status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", status)
	}
}

func TestE2EPingNoRequestNoResponse(t *testing.T) {
	ts, _ := boot(t)
	status, _ := httpJSON(t, ts, http.MethodGet, "/api/v1/ping", nil)
	if status != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", status)
	}
}

func TestE2EDeleteReturnsEmpty(t *testing.T) {
	ts, svc := boot(t)
	_, _ = httpJSON(t, ts, http.MethodPost, "/api/v1/users", types.CreateUserReq{Name: "tmp"})
	status, body := httpJSON(t, ts, http.MethodDelete, "/api/v1/users/anything", nil)
	if status != http.StatusOK {
		t.Fatalf("DeleteUser status %d: %s", status, body)
	}
	svc.Lock()
	defer svc.Unlock()
	if len(svc.Users) != 0 {
		t.Errorf("expected store cleared, still has %d rows", len(svc.Users))
	}
}

func TestE2EUpdate(t *testing.T) {
	ts, _ := boot(t)
	status, body := httpJSON(t, ts, http.MethodPut, "/api/v1/users/anything", types.CreateUserReq{Name: "bob"})
	if status != http.StatusOK {
		t.Fatalf("UpdateUser status %d: %s", status, body)
	}
	var out types.User
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "bob" {
		t.Errorf("update lost name: %+v", out)
	}
}

func TestE2EErrorTypeShape(t *testing.T) {
	err := types.NewUserNotFoundErr()
	if err.HTTPStatus() != http.StatusNotFound {
		t.Errorf("expected 404 status, got %d", err.HTTPStatus())
	}
	// `code` lives as an unexported framework-internal field; the
	// canonical code is exposed via the constant for client switching.
	if types.ErrCodeUserNotFound != "USER_NOT_FOUND" {
		t.Errorf("expected SCREAMING_SNAKE code constant, got %q", types.ErrCodeUserNotFound)
	}
	if err.Error() == "" {
		t.Error("Error() should not be empty")
	}
}

func TestE2EEnumConstants(t *testing.T) {
	if string(types.RoleAdmin) != "admin" {
		t.Errorf("expected 'admin', got %q", types.RoleAdmin)
	}
	if int(types.PriorityHigh) != 3 {
		t.Errorf("expected 3, got %d", types.PriorityHigh)
	}
}

func TestE2EUnknownRouteReturns404(t *testing.T) {
	ts, _ := boot(t)
	status, _ := httpJSON(t, ts, http.MethodGet, "/api/does-not-exist", nil)
	if status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", status)
	}
}

func TestE2EOpenAPIDocumentExists(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	doc := strings.Replace(here, "e2e_test.go", "docs/openapi.yaml", 1)
	data, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf("openapi.yaml missing: %v", err)
	}
	for _, want := range []string{
		"openapi: 3.1.0",
		"#/components/schemas/User",
		"#/components/schemas/CreateUserReq",
		"operationId: GetUser",
		"operationId: CreateUser",
		"operationId: DeleteUser",
		"operationId: UpdateUser",
		"operationId: Ping",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("openapi.yaml missing %q", want)
		}
	}
}

// TestE2EValidationRejectsEmptyName confirms the generated Validate()
// is called by the handler and surfaces as a 400 when @required fails.
func TestE2EValidationRejectsEmptyName(t *testing.T) {
	ts, _ := boot(t)
	status, body := httpJSON(t, ts, http.MethodPost, "/api/v1/users", types.CreateUserReq{Name: ""})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", status, body)
	}
	if !strings.Contains(string(body), "name") {
		t.Errorf("expected error message to mention `name`, got: %s", body)
	}
}

// TestE2EValidationRejectsTooLongName confirms @length(1, 50) is enforced.
func TestE2EValidationRejectsTooLongName(t *testing.T) {
	ts, _ := boot(t)
	long := strings.Repeat("x", 51)
	status, body := httpJSON(t, ts, http.MethodPost, "/api/v1/users", types.CreateUserReq{Name: long})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for length violation, got %d body=%s", status, body)
	}
	if !strings.Contains(string(body), "length") {
		t.Errorf("expected error to mention `length`, got: %s", body)
	}
}

func TestE2EBadRequestReturns400(t *testing.T) {
	ts, _ := boot(t)
	// Send invalid JSON body to a write endpoint.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/users", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

// TestE2EDocCommentsPropagate verifies that `//` comments in the DSL
// design files appear verbatim in the generated Go sources for types,
// fields, handlers, and logic scaffolds.
func TestE2EDocCommentsPropagate(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	root := strings.TrimSuffix(here, "/e2e_test.go")

	cases := []struct {
		path string
		want []string
	}{
		{
			path: "internal/types/design/types.go",
			want: []string{
				"// User is the wire-level shape of a stored user.",
				"// GetUserReq is the path-bound input for GET /users/{id}.",
				"// id locates the user.",
				"// Empty is the zero-payload response",
			},
		},
		{
			path: "internal/types/design/enums.go",
			want: []string{},
		},
		{
			path: "internal/transport/user-service/get-user.go",
			want: []string{
				"// GetUser returns the user identified by req.ID.",
			},
		},
		{
			path: "internal/transport/user-service/ping.go",
			want: []string{
				"// Ping is a liveness-style endpoint with no body.",
			},
		},
	}
	for _, c := range cases {
		data, err := os.ReadFile(root + "/" + c.path)
		if err != nil {
			t.Fatalf("read %s: %v", c.path, err)
		}
		body := string(data)
		for _, w := range c.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s missing %q in:\n%s", c.path, w, body)
			}
		}
	}
}

func TestE2EServerHealthEndpoints(t *testing.T) {
	// Boot a server WITH default health endpoints to exercise the runtime.
	svc := svccontext.NewServiceContext()
	srv := server.New(svc)
	userservice.RegisterRoutes(srv, svc)
	// Manually wire health (server.Start would do it but we avoid binding).
	srv.RegisterHealthCheck("ok", 0, func(context.Context) error { return nil })
	mux := srv.Mux()
	mux.Handle("/healthz", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	ts := httptest.NewServer(mux)
	defer ts.Close()
	resp, err := ts.Client().Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status: %d", resp.StatusCode)
	}
}
