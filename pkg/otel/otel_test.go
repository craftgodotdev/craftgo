package otel

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHTTPMiddlewareDisabledByDefault confirms the runtime gate:
// HTTPMiddleware is a pass-through unless Init has been called.
func TestHTTPMiddlewareDisabledByDefault(t *testing.T) {
	Disable()
	if IsEnabled() {
		t.Fatal("expected disabled by default after Disable")
	}
	called := false
	mw := HTTPMiddleware("test")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if !called {
		t.Error("downstream handler should still run when otel is off")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

// TestHTTPMiddlewareEnabled confirms Init flips the gate. The actual
// span emission is the contrib package's responsibility — we only
// assert the wrapper now routes through otelhttp (downstream still
// runs and the response status survives).
func TestHTTPMiddlewareEnabled(t *testing.T) {
	Init()
	defer Disable()
	if !IsEnabled() {
		t.Fatal("expected enabled after Init")
	}
	mw := HTTPMiddleware("test")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d", rec.Code)
	}
}
