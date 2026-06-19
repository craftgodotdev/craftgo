package otel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// TestOTLPHTTPExporterHitsEndpointURL pins the endpoint contract:
// WithOTLPHTTPExporter parses the FULL URL (scheme + host + port) and
// POSTs spans there. Pointing it at a local test collector and
// asserting the collector is hit on /v1/traces proves the URL is
// parsed and dialed correctly.
func TestOTLPHTTPExporterHitsEndpointURL(t *testing.T) {
	hit := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case hit <- r.URL.Path:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := context.Background()
	// srv.URL is "http://127.0.0.1:PORT" - pass it verbatim, scheme and all.
	tp, err := Init(WithOTLPHTTPExporter(ctx, srv.URL))
	if err != nil {
		t.Fatalf("init with otlp http exporter: %v", err)
	}
	defer func() {
		_ = tp.Shutdown(ctx)
		Disable()
	}()

	_, span := tp.Tracer("test").Start(ctx, "probe")
	span.End()
	if err := tp.ForceFlush(ctx); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	select {
	case path := <-hit:
		if path != "/v1/traces" {
			t.Errorf("collector hit on %q, want /v1/traces", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OTLP collector was never hit - endpoint URL not parsed/connected correctly")
	}
}

// TestOTLPgRPCExporterAcceptsHostPortAndURL pins the address contract:
// the gRPC exporter accepts a bare host:port (plaintext) AND a full
// URL whose scheme selects TLS (https://) - both construct cleanly.
func TestOTLPgRPCExporterAcceptsHostPortAndURL(t *testing.T) {
	ctx := context.Background()
	for _, addr := range []string{"collector:4317", "http://collector:4317", "https://collector:4317"} {
		tp, err := Init(WithOTLPgRPCExporter(ctx, addr))
		if err != nil {
			t.Fatalf("grpc exporter for %q: %v", addr, err)
		}
		_ = tp.Shutdown(ctx)
		Disable()
	}
}
