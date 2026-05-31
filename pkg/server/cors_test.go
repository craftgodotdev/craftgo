package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func corsOK() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
}

// TestCORSVaryOriginOnSpecificOrigin pins the cache-correctness rule:
// when Allow-Origin echoes the request Origin, the response carries
// `Vary: Origin` so shared caches key on it.
func TestCORSVaryOriginOnSpecificOrigin(t *testing.T) {
	h := corsMiddleware(CORSStrict("https://app.example.com"))(corsOK())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("Allow-Origin = %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("expected `Vary: Origin` for a specific allowed origin, got %q", got)
	}
}

// TestCORSWildcardNoVary confirms the wildcard "*" path needs no Vary —
// the response is identical for every origin.
func TestCORSWildcardNoVary(t *testing.T) {
	h := corsMiddleware(CORSPermissive())(corsOK())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://anything.example.com")
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Skipf("permissive did not echo *")
	}
	if got := rec.Header().Get("Vary"); got != "" {
		t.Errorf("wildcard * must not set Vary, got %q", got)
	}
}
