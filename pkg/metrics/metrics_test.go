package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
)

// TestInitInstallsMeterProvider pins the contract: Init replaces the
// global MeterProvider with the package's Prometheus-backed SDK
// implementation. Without this swap, `otelhttp.NewHandler` records
// against the no-op default and the scrape stays empty.
func TestInitInstallsMeterProvider(t *testing.T) {
	resetForTest(t)
	provider, err := Init()
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = provider.Shutdown(sctx)
	}()
	if !IsEnabled() {
		t.Error("IsEnabled() = false after Init")
	}
	got := otel.GetMeterProvider()
	if got == nil {
		t.Fatal("global MeterProvider is nil")
	}
	// Concrete-type identity check: confirm the global slot now holds
	// our SDK provider, not the API-package no-op.
	if got != provider {
		t.Errorf("global MeterProvider is not the one Init returned (got %T, want %T)", got, provider)
	}
}

// TestSnapshotHandlerEmitsPromTextFormat pins the wire format. The
// scrape MUST be Prometheus text exposition (the OpenMetrics-or-Prom
// negotiation kicks in based on Accept) so any standard scraper -
// Prometheus, Vector, Grafana Agent - drops in without translation.
func TestSnapshotHandlerEmitsPromTextFormat(t *testing.T) {
	resetForTest(t)
	provider, err := InitDefault()
	if err != nil {
		t.Fatalf("InitDefault: %v", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = provider.Shutdown(sctx)
	}()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	SnapshotHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") && !strings.Contains(ct, "openmetrics-text") {
		t.Errorf("Content-Type = %q, want a Prom/OpenMetrics text variant", ct)
	}
	body := rec.Body.String()
	// InitDefault wires the runtime collectors - the scrape MUST
	// surface at least `go_goroutines` (Go runtime) and `process_*`
	// (process collector). Their presence proves the registry is
	// alive and the handler is reading from it.
	for _, want := range []string{"go_goroutines", "process_"} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape body missing %q (truncated):\n%s",
				want, truncate(body, 400))
		}
	}
	// Each metric series MUST carry the standard Prom HELP / TYPE
	// preamble - confirms the exposition format, not arbitrary text.
	for _, want := range []string{"# HELP go_goroutines", "# TYPE go_goroutines"} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape body missing preamble %q", want)
		}
	}
}

// TestSnapshotHandlerEmptyBeforeInit pins the safe-default path: a
// scrape that hits the endpoint BEFORE Init still returns 200 with an
// empty body so health probes don't flap during startup.
func TestSnapshotHandlerEmptyBeforeInit(t *testing.T) {
	resetForTest(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	SnapshotHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with no metrics", rec.Code)
	}
}

// TestStartAdminEmptyAddrIsNoop pins the opt-out shape: passing an
// empty addr returns nil pointers so callers can leave the call site
// unconditional even when telemetry is off (env-flag-driven configs).
func TestStartAdminEmptyAddrIsNoop(t *testing.T) {
	s, errCh := StartAdmin("")
	if s != nil {
		t.Errorf("expected nil server, got %v", s)
	}
	if errCh != nil {
		t.Errorf("expected nil error channel, got %v", errCh)
	}
}

// TestStartAdminCustomPath pins the [WithPath] option: overriding the
// default `/metrics` route surfaces the snapshot under the chosen
// path and 404s the default - confirming the option actually rewires
// the mux instead of registering a second handler.
func TestStartAdminCustomPath(t *testing.T) {
	resetForTest(t)
	provider, err := InitDefault()
	if err != nil {
		t.Fatalf("InitDefault: %v", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = provider.Shutdown(sctx)
	}()

	s, _ := StartAdmin("127.0.0.1:0", WithPath("/internal/metrics"))
	if s == nil {
		t.Fatal("expected non-nil server")
	}
	defer ShutdownAdmin(context.Background(), s)
	addr := s.Addr

	resp, err := http.Get("http://" + addr + "/internal/metrics")
	if err != nil {
		t.Fatalf("GET /internal/metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	// Default path should 404 - confirms the option REPLACED the
	// path rather than ADDING one.
	resp2, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("default path status = %d, want 404 (option should replace)", resp2.StatusCode)
	}
}

// TestInitWithOTLPgRPCReaderInstallsPushExporter pins the push path:
// passing the OTLP gRPC option attaches a periodic reader to the
// provider so otelhttp's recorded histograms get pushed to a
// collector. We don't run a real collector here - bind failures /
// unreachable endpoints surface on the next push tick, not at Init
// time, so the constructor returning success is the right contract.
func TestInitWithOTLPgRPCReaderInstallsPushExporter(t *testing.T) {
	resetForTest(t)
	ctx := context.Background()
	provider, err := Init(WithOTLPgRPCReader(ctx, "127.0.0.1:4317"))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = provider.Shutdown(sctx)
	}()
	if !IsEnabled() {
		t.Error("IsEnabled() = false after Init")
	}
}

// TestInitWithOTLPHTTPReaderInstallsPushExporter pins the same
// guarantee for the HTTP/protobuf transport - useful for sidecar
// setups where the gRPC port is firewalled.
func TestInitWithOTLPHTTPReaderInstallsPushExporter(t *testing.T) {
	resetForTest(t)
	ctx := context.Background()
	provider, err := Init(WithOTLPHTTPReader(ctx, "127.0.0.1:4318"))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = provider.Shutdown(sctx)
	}()
	if !IsEnabled() {
		t.Error("IsEnabled() = false after Init")
	}
}

// TestInitCombinesPullAndPushReaders pins the multi-reader path:
// stacking [WithPrometheusReader] + an OTLP push option produces a
// MeterProvider that fans every recorded metric to BOTH outputs —
// the side-by-side shape (Prometheus scrape stays live while the
// OTLP collector is validated).
func TestInitCombinesPullAndPushReaders(t *testing.T) {
	resetForTest(t)
	ctx := context.Background()
	provider, err := Init(
		WithPrometheusReader(),
		WithOTLPgRPCReader(ctx, "127.0.0.1:4317"),
	)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = provider.Shutdown(sctx)
	}()
	// Both readers should be active - the Prometheus side surfaces
	// via SnapshotHandler, the push side via the periodic reader
	// (verified by Init not erroring; the actual transmission is
	// the SDK's responsibility).
	rec := httptest.NewRecorder()
	SnapshotHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("scrape failed alongside push reader: status %d", rec.Code)
	}
}

// TestStartAdminBindFailureSurfacesOnChannel pins the error path:
// trying to bind a port already in use routes the failure to the
// returned channel rather than panicking. The caller decides whether
// to log + continue (admin failures usually shouldn't kill the public
// server) or hard-exit.
func TestStartAdminBindFailureSurfacesOnChannel(t *testing.T) {
	first, _ := StartAdmin("127.0.0.1:0")
	if first == nil {
		t.Fatal("expected first listener")
	}
	defer ShutdownAdmin(context.Background(), first)

	_, errCh := StartAdmin(first.Addr) // collide with port already bound
	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected bind error, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Error("timed out waiting for bind error")
	}
}

// resetForTest clears the package's global state so tests run in
// isolation. Re-creates the Prometheus registry (the old one keeps
// the already-registered collectors and would reject re-Init from a
// sibling test with `AlreadyRegisteredError`) and clears the enabled
// flag.
func resetForTest(t *testing.T) {
	t.Helper()
	registry = prom.NewRegistry()
	enabled.Store(false)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
