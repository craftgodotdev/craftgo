package otel

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
// span emission is the contrib package's responsibility - we only
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

// TestHTTPMiddlewareInjectsTraceparent pins the response-header
// contract: when otel is enabled and a span is active on the request
// context, the wrapper invokes the configured TextMapPropagator to
// emit the W3C tracecontext `traceparent` header on the response -
// no bespoke header names. The format is the standard
// `<version>-<trace-id>-<span-id>-<flags>` quad, which clients can
// re-feed into their own propagator to attach to the same trace tree.
func TestHTTPMiddlewareInjectsTraceparent(t *testing.T) {
	InitDefault()
	defer Disable()
	mw := HTTPMiddleware("test")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	tp := rec.Header().Get("traceparent")
	if tp == "" {
		t.Fatalf("expected `traceparent` response header, got empty")
	}
	// W3C Level 1 traceparent: `00-<32 hex>-<16 hex>-<2 hex>` →
	// length 55 with three dashes.
	if len(tp) != 55 || strings.Count(tp, "-") != 3 {
		t.Errorf("traceparent = %q, want W3C `00-<32hex>-<16hex>-<2hex>`", tp)
	}
}

// TestHTTPMiddlewareDisabledOmitsTraceparent pins the negative case:
// with the gate closed, no trace headers leak - important for test
// harnesses running under `Disable()` that assert on a deterministic
// header set.
func TestHTTPMiddlewareDisabledOmitsTraceparent(t *testing.T) {
	Disable()
	mw := HTTPMiddleware("test")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if got := rec.Header().Get("traceparent"); got != "" {
		t.Errorf("expected no `traceparent` when otel is disabled, got %q", got)
	}
}
