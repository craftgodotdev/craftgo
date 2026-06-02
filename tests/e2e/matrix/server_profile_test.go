package matrix

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	designtypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/design"
)

// ---- profile service: nested validation + business errors ----

func TestServer_ProfileCreateAndGet(t *testing.T) {
	ts, _ := boot(t)
	st, body := reqJSON(t, ts, http.MethodPost, "/api/v1/profiles", validProfile("alice"))
	if st != http.StatusCreated {
		t.Fatalf("create %d: %s", st, body)
	}
	var created designtypes.Profile
	_ = json.Unmarshal(body, &created)
	if created.ID == "" || created.Contacts.Email != "alice@example.com" || len(created.Addresses) != 1 {
		t.Fatalf("bad created: %+v", created)
	}
	if created.Addresses[0].Coords == nil || created.Addresses[0].Coords.Lat != 10.5 {
		t.Errorf("nested coords lost: %+v", created.Addresses[0].Coords)
	}
	st, body = reqJSON(t, ts, http.MethodGet, "/api/v1/profiles/"+created.ID, nil)
	if st != http.StatusOK {
		t.Fatalf("get %d: %s", st, body)
	}
	var got designtypes.Profile
	_ = json.Unmarshal(body, &got)
	if got.DisplayName != "alice" {
		t.Errorf("got %+v", got)
	}
}

func TestServer_ProfileGetMissing404(t *testing.T) {
	ts, _ := boot(t)
	if st, _ := reqJSON(t, ts, http.MethodGet, "/api/v1/profiles/nope", nil); st != http.StatusNotFound {
		t.Fatalf("status %d", st)
	}
}

func TestServer_ProfileValidationRejects(t *testing.T) {
	ts, _ := boot(t)
	// 3-level nested: Coords.Validate() rejects lat 999 via HTTP.
	bad := validProfile("alice")
	bad.Addresses[0].Coords = &designtypes.Coords{Lat: 999, Lng: 0}
	if st, body := reqJSON(t, ts, http.MethodPost, "/api/v1/profiles", bad); st != http.StatusBadRequest || !strings.Contains(string(body), "lat") {
		t.Errorf("bad coords: status %d body %s", st, body)
	}
	// minItems on addresses.
	empty := validProfile("bob")
	empty.Addresses = nil
	if st, body := reqJSON(t, ts, http.MethodPost, "/api/v1/profiles", empty); st != http.StatusBadRequest || !strings.Contains(string(body), "addresses") {
		t.Errorf("empty addresses: status %d body %s", st, body)
	}
}

func TestServer_ProfileDuplicateEmail409(t *testing.T) {
	ts, _ := boot(t)
	first := validProfile("alice")
	reqJSON(t, ts, http.MethodPost, "/api/v1/profiles", first)
	dup := validProfile("alice2")
	dup.Contacts.Email = first.Contacts.Email
	st, body := reqJSON(t, ts, http.MethodPost, "/api/v1/profiles", dup)
	if st != http.StatusConflict {
		t.Fatalf("status %d: %s", st, body)
	}
	if !strings.Contains(string(body), "DUPLICATE_EMAIL") {
		t.Errorf("body %s", body)
	}
}

func TestServer_ProfileReservedName422(t *testing.T) {
	ts, _ := boot(t)
	st, body := reqJSON(t, ts, http.MethodPost, "/api/v1/profiles", validProfile("admin"))
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("status %d: %s", st, body)
	}
	if !strings.Contains(string(body), "PROFILE_VALIDATION_FAILED") || !strings.Contains(string(body), "displayName") {
		t.Errorf("body %s", body)
	}
}

func TestServer_ProfileRateLimit429(t *testing.T) {
	ts, _ := boot(t)
	var st int
	var body []byte
	for i := 1; i <= 5; i++ {
		st, body = reqJSON(t, ts, http.MethodPost, "/api/v1/profiles", validProfile(fmt.Sprintf("user%d", i)))
	}
	if st != http.StatusTooManyRequests || !strings.Contains(string(body), "RATE_LIMITED") {
		t.Errorf("5th create: status %d body %s", st, body)
	}
}

// ---- security: ProfileAuth middleware gates the admin endpoints ----

func authGet(t *testing.T, ts *httptest.Server, path, token string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestServer_SecurityGatesAdminEndpoints(t *testing.T) {
	ts, _ := boot(t)
	if s := authGet(t, ts, "/api/v1/admin/profiles", ""); s != http.StatusUnauthorized {
		t.Errorf("missing token: %d", s)
	}
	if s := authGet(t, ts, "/api/v1/admin/profiles", "Bearer wrong"); s != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", s)
	}
	if s := authGet(t, ts, "/api/v1/admin/profiles", authToken); s != http.StatusOK {
		t.Errorf("valid token: %d", s)
	}
	// Unprotected route stays open.
	if s := authGet(t, ts, "/api/v1/profiles/x", ""); s == http.StatusUnauthorized {
		t.Errorf("unprotected /profiles must not 401")
	}
}

func TestServer_CrossFolderAdminDashboard(t *testing.T) {
	ts, _ := boot(t)
	if s := authGet(t, ts, "/api/v1/admin/dashboard", ""); s != http.StatusUnauthorized {
		t.Errorf("dashboard no token: %d", s)
	}
	if s := authGet(t, ts, "/api/v1/admin/dashboard", authToken); s != http.StatusOK {
		t.Errorf("dashboard valid token: %d", s)
	}
}
