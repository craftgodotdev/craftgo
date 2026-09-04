package otel_test

// Cross-signal tests for the HTTP middleware, kept black-box so they wire
// the two packages the way a project's main.go does.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/craftgodotdev/craftgo/pkg/metrics"
	craftotel "github.com/craftgodotdev/craftgo/pkg/otel"
)

// installMeter puts a MeterProvider on the global slot backed by a reader
// the test owns, keeping runs off the shared Prometheus registry.
func installMeter(t *testing.T, serviceName string) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	if _, err := metrics.Init(metrics.WithReader(reader), metrics.WithServiceName(serviceName)); err != nil {
		t.Fatal(err)
	}
	return reader
}

// httpMetricNames collects the instrument names the reader holds.
func httpMetricNames(t *testing.T, r *sdkmetric.ManualReader) []string {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out = append(out, m.Name)
		}
	}
	return out
}

// serveOnce drives a request through a mux so `http.route` gets populated -
// it comes from the pattern ServeMux records on the request.
func serveOnce(t *testing.T, mw func(http.Handler) http.Handler) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /api/todos/{id}", mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/todos/42", nil))
}

// TestHTTPMiddlewareRecordsMetricsWithTracingDisabled pins the decoupling.
// One otelhttp wrapper emits both signals, so gating on the tracing flag
// dropped every http.server.* metric when a project ran metrics without
// traces - an empty dashboard behind a healthy scrape.
func TestHTTPMiddlewareRecordsMetricsWithTracingDisabled(t *testing.T) {
	craftotel.Disable()
	t.Cleanup(craftotel.Disable)
	if _, err := craftotel.InitFromConfig(context.Background(), craftotel.Config{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if craftotel.IsEnabled() {
		t.Fatal("tracing should be off for this fixture")
	}
	reader := installMeter(t, "todo")
	serveOnce(t, craftotel.HTTPMiddleware("todo"))

	names := httpMetricNames(t, reader)
	want := "http.server.request.duration"
	for _, n := range names {
		if n == want {
			return
		}
	}
	t.Errorf("tracing off swallowed the HTTP metrics: want %q among %v", want, names)
}

// TestHTTPMiddlewareInertWhenBothSignalsOff: with neither signal active the
// middleware stays a pass-through. Both flags must be cleared - clearing
// only the tracing one is the case that used to lose every HTTP metric.
func TestHTTPMiddlewareInertWhenBothSignalsOff(t *testing.T) {
	reader := installMeter(t, "todo")
	craftotel.Disable()
	metrics.Disable()
	t.Cleanup(craftotel.Disable)
	called := false
	h := craftotel.HTTPMiddleware("todo")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if !called {
		t.Fatal("pass-through must still reach the wrapped handler")
	}
	if got := httpMetricNames(t, reader); len(got) != 0 {
		t.Errorf("both signals off but the reader collected %v", got)
	}
}

// BenchmarkHTTPMiddleware guards the hoist: building NewHandler per request
// cost roughly 1.8x the allocations measured here.
func BenchmarkHTTPMiddleware(b *testing.B) {
	reader := sdkmetric.NewManualReader()
	if _, err := metrics.Init(metrics.WithReader(reader), metrics.WithServiceName("bench")); err != nil {
		b.Fatal(err)
	}
	if _, err := craftotel.InitFromConfig(context.Background(), craftotel.Config{
		Enabled: true, ServiceName: "bench", Exporter: "none",
	}); err != nil {
		b.Fatal(err)
	}
	h := craftotel.HTTPMiddleware("bench")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/a", nil)
	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
}
