package otel_test

// Cross-signal tests for the HTTP middleware. They live in an external
// test package because the assertions need `pkg/metrics`, which the
// middleware now consults - importing it from the internal test package
// would be fine, but keeping these black-box mirrors how a project wires
// the two InitFromConfig helpers in main.go.

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
// the test owns, so runs stay independent of the package's shared
// Prometheus registry.
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

// serveOnce drives one request through a mux so the route pattern reaches
// otelhttp - `http.route` only exists because Go's ServeMux records the
// matched pattern on the request.
func serveOnce(t *testing.T, mw func(http.Handler) http.Handler) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /api/todos/{id}", mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/todos/42", nil))
}

// TestHTTPMiddlewareRecordsMetricsWithTracingDisabled pins the decoupling.
// One `otelhttp` wrapper produces BOTH spans and the HTTP instruments, so
// gating it on the tracing flag alone threw away every `http.server.*`
// metric whenever a project ran metrics without traces - which the config
// actively invites, `otel.enabled` and `metrics.enabled` being separate
// switches. The symptom was an empty dashboard with a healthy scrape.
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

// TestHTTPMiddlewareInertWhenBothSignalsOff pins the other half of the
// gate: with neither signal active the middleware stays a pass-through,
// which is the cheapness the gate exists for. Both flags have to be
// cleared now - clearing only the tracing one is exactly the case that
// used to lose every HTTP metric.
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

// BenchmarkHTTPMiddleware guards the hoist. `otelhttp.NewHandler` resolves
// the global providers and creates its instruments eagerly, so building it
// inside the request path re-made three histograms per call. Keep an eye on
// allocs/op here: the per-request form cost roughly 1.8x this one.
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
