package matrix

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/server"

	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/middleware"
	accountroutes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/routes/account-user-service"
	adminroutes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/routes/admin"
	adminlegacyroutes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/routes/admin/legacy"
	adminapiv1routes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/routes/admin/v1"
	adminapiv2routes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/routes/admin/v2"
	adminapiv3routes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/routes/admin/v3"
	catalogroutes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/routes/catalog-service"
	ordersroutes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/routes/orders-service"
	profileroutes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/routes/profile-service"
	designtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/design"
	"github.com/craftgodotdev/craftgo/tests/e2e/matrix/svccontext"
)

// authToken is the bearer the ProfileAuth middleware accepts.
const authToken = "Bearer secret-token"

// boot stands up every server-roundtrip service on one httptest server, with
// the profile-service middleware wired.
func boot(t *testing.T) (*httptest.Server, *svccontext.ServiceContext) {
	t.Helper()
	svc := svccontext.NewServiceContext()
	svc.ProfileAuth = middleware.NewProfileAuthMiddleware(authToken)
	svc.RequestStamp = middleware.NewRequestStampMiddleware()
	srv := server.New(svc, server.WithoutDefaultHealth())
	ordersroutes.RegisterRoutes(srv, svc)
	catalogroutes.RegisterRoutes(srv, svc)
	accountroutes.RegisterRoutes(srv, svc)
	profileroutes.RegisterRoutes(srv, svc)
	// AdminService and AdminApi each split across @group folders, so every
	// group hub registers separately (the umbrella RegisterAll does the same).
	adminroutes.RegisterRoutes(srv, svc)
	adminlegacyroutes.RegisterRoutes(srv, svc)
	adminapiv1routes.RegisterRoutes(srv, svc)
	adminapiv2routes.RegisterRoutes(srv, svc)
	adminapiv3routes.RegisterRoutes(srv, svc)
	ts := httptest.NewServer(srv.Mux())
	t.Cleanup(ts.Close)
	return ts, svc
}

// validProfile returns a CreateProfileReq passing every validator; the unique
// name keeps the duplicate-email check from rejecting parallel profiles.
func validProfile(name string) designtypes.CreateProfileReq {
	lat, lng := 10.5, 105.8
	return designtypes.CreateProfileReq{
		DisplayName: name,
		Contacts:    designtypes.ContactInfo{Email: name + "@example.com"},
		Addresses: []designtypes.PfAddress{
			{Street: "1 Main St", City: "HCM", Country: "VN", Coords: &designtypes.Coords{Lat: lat, Lng: lng}},
		},
		Tags: []string{"vip"},
		Meta: map[string]string{"team": "core"},
	}
}

// getJSON issues a GET and JSON-decodes a 2xx body into out; returns status.
func getJSON(t *testing.T, ts *httptest.Server, path string, out any) int {
	t.Helper()
	resp, err := ts.Client().Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		_ = json.Unmarshal(body, out)
	}
	return resp.StatusCode
}

// reqJSON issues method+body and returns (status, raw body).
func reqJSON(t *testing.T, ts *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, r)
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

// TestServer_HealthEndpoints boots WITH default health (the inverse of boot's
// WithoutDefaultHealth) and confirms a registered health check serves.
func TestServer_HealthEndpoints(t *testing.T) {
	svc := svccontext.NewServiceContext()
	srv := server.New(svc)
	accountroutes.RegisterRoutes(srv, svc)
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
		t.Fatalf("healthz status %d", resp.StatusCode)
	}
}
