package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func corsOK() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
}

// An echoed origin comes with Vary: Origin.
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

// A wildcard Allow-Origin comes without Vary.
func TestCORSWildcardNoVary(t *testing.T) {
	h := corsMiddleware(CORSPermissive())(corsOK())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://anything.example.com")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Allow-Origin = %q, want *", got)
	}
	if got := rec.Header().Get("Vary"); got != "" {
		t.Errorf("wildcard * must not set Vary, got %q", got)
	}
}

// A preflight from an allowed origin is answered 204 with Allow-Methods.
func TestCORSGenuinePreflightShortCircuits(t *testing.T) {
	h := corsMiddleware(CORSStrict("https://app.example.com"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a genuine preflight must not reach the handler")
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("genuine preflight should be 204, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("genuine preflight should carry Allow-Methods")
	}
}

// An OPTIONS without Access-Control-Request-Method reaches the handler.
func TestCORSNonPreflightOptionsFallsThrough(t *testing.T) {
	reached := false
	h := corsMiddleware(CORSStrict("https://app.example.com"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	h.ServeHTTP(rec, req)
	if !reached {
		t.Fatal("non-preflight OPTIONS should reach the handler, not be swallowed as a 204 preflight")
	}
	if rec.Code == http.StatusNoContent {
		t.Errorf("non-preflight OPTIONS should not return a 204 preflight, got %d", rec.Code)
	}
}

// A preflight from a disallowed origin reaches the handler without Allow-Origin.
func TestCORSDisallowedOriginPreflightFallsThrough(t *testing.T) {
	reached := false
	h := corsMiddleware(CORSStrict("https://app.example.com"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://evil.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	h.ServeHTTP(rec, req)
	if !reached {
		t.Fatal("disallowed-origin preflight should fall through, not be swallowed as a 204 preflight")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disallowed origin must get no Allow-Origin, got %q", got)
	}
}

// SetCORS answers a preflight ahead of the Use middlewares, so an auth middleware that refuses
// requests without credentials cannot refuse the browser's preflight; the actual request still
// passes through them with the CORS headers set.
func TestSetCORSRunsBeforeUseMiddlewares(t *testing.T) {
	srv := New(nil)
	srv.SetCORS(CORSStrict("https://app.example.com"))
	srv.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	srv.Handle("POST /x", corsOK())
	h := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Errorf("preflight: got %d, Allow-Origin %q; want 204 from CORS", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Errorf("request without credentials: got %d, Allow-Origin %q; want the middleware's 401 with CORS headers", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
	}
}
