package telemetry_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/craftgodotdev/craftgo/pkg/telemetry"
)

func serve(t *testing.T, tel *telemetry.Telemetry) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /api/things/{id}", tel.HTTPMiddleware()(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })))
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/things/42", nil))
}

// The top-level serviceName labels the scrape that carries the HTTP instruments.
func TestServiceNameReachesBothSignals(t *testing.T) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		ServiceName: "todo",
		OTel:        telemetry.OTelConfig{Enabled: true, Exporter: "none"},
		Metrics:     telemetry.MetricsConfig{Enabled: true, Exporter: "prometheus", AdminAddr: "127.0.0.1:0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	serve(t, tel)

	resp, err := http.Get("http://" + tel.ScrapeURL())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 1<<20)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	if !strings.Contains(body, `target_info{service_name="todo",`) {
		t.Errorf("metrics carry no service_name=todo:\n%s", body[:min(len(body), 400)])
	}
	if !strings.Contains(body, "http_server_request_duration_seconds_count") {
		t.Errorf("no HTTP instruments in the scrape:\n%s", body[:min(len(body), 400)])
	}
}

// HTTP metrics flow with tracing off.
func TestMetricsSurviveTracingDisabled(t *testing.T) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		ServiceName: "todo",
		OTel:        telemetry.OTelConfig{Enabled: false},
		Metrics:     telemetry.MetricsConfig{Enabled: true, Exporter: "prometheus", AdminAddr: "127.0.0.1:0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	serve(t, tel)

	resp, err := http.Get("http://" + tel.ScrapeURL())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 1<<20)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "http_server_request_duration_seconds_count") {
		t.Error("tracing off swallowed the HTTP metrics")
	}
}

// Two stacks keep separate registries, each scraping only its own service name.
func TestTwoStacksAreIndependent(t *testing.T) {
	cfg := func(name string) telemetry.Config {
		return telemetry.Config{
			ServiceName: name,
			Metrics:     telemetry.MetricsConfig{Enabled: true, Exporter: "prometheus", AdminAddr: "127.0.0.1:0"},
		}
	}
	a, err := telemetry.Init(context.Background(), cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	b, err := telemetry.Init(context.Background(), cfg("b"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Shutdown(context.Background()) })

	for _, tc := range []struct {
		tel  *telemetry.Telemetry
		want string
	}{{a, `target_info{service_name="a",`}, {b, `target_info{service_name="b",`}} {
		resp, err := http.Get("http://" + tc.tel.ScrapeURL())
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 1<<20)
		n, _ := resp.Body.Read(buf)
		resp.Body.Close()
		body := string(buf[:n])
		if !strings.Contains(body, tc.want) {
			t.Errorf("want %s in its own scrape", tc.want)
		}
		if n := seriesCount(body, "target_info"); n != 1 {
			t.Errorf("stacks are sharing a registry: %d target_info series", n)
		}
	}
}

// An unconfigured stack passes requests through and owns nothing; a nil one stays usable.
func TestUnconfiguredIsPassThrough(t *testing.T) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	leaf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	if got := tel.HTTPMiddleware()(leaf); got == nil {
		t.Fatal("middleware must never be nil")
	}
	rec := httptest.NewRecorder()
	tel.HTTPMiddleware()(leaf).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want the leaf's own", rec.Code)
	}
	if tel.ScrapeURL() != "" || tel.AdminErr() != nil || tel.Registerer() != nil {
		t.Error("an unconfigured stack must own nothing")
	}
	var nilTel *telemetry.Telemetry
	if err := nilTel.Shutdown(context.Background()); err != nil {
		t.Errorf("nil Shutdown = %v", err)
	}
	nilTel.HTTPMiddleware()(leaf).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
}

// seriesCount counts the series lines of family, not its # HELP / # TYPE lines.
func seriesCount(body, family string) int {
	n := 0
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, family) {
			n++
		}
	}
	return n
}

func shortShutdown(t *testing.T, tel *telemetry.Telemetry) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = tel.Shutdown(ctx)
}

func scrape(t *testing.T, url string) (string, http.Header) {
	t.Helper()
	resp, err := http.Get("http://" + url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	return string(body), resp.Header
}

// A traced stack writes a W3C `traceparent` onto the response.
func TestTraceparentInjectedWhenTracing(t *testing.T) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		ServiceName: "todo",
		OTel:        telemetry.OTelConfig{Enabled: true, Exporter: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	rec := httptest.NewRecorder()
	tel.HTTPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want the leaf's own", rec.Code)
	}
	tp := rec.Header().Get("traceparent")
	if len(tp) != 55 || strings.Count(tp, "-") != 3 {
		t.Errorf("traceparent = %q, want W3C `00-<32hex>-<16hex>-<2hex>`", tp)
	}
}

// A metrics-only stack echoes no traceparent, even when another stack owns
// the process-wide tracer.
func TestNoTraceparentWithoutTracing(t *testing.T) {
	traced, err := telemetry.Init(context.Background(), telemetry.Config{
		OTel: telemetry.OTelConfig{Enabled: true, Exporter: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = traced.Shutdown(context.Background()) })
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		Metrics: telemetry.MetricsConfig{Enabled: true, Exporter: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	tel.HTTPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).
		ServeHTTP(rec, req)
	if got := rec.Header().Get("traceparent"); got != "" {
		t.Errorf("metrics-only stack echoed traceparent %q", got)
	}
}

// The stack initialised last is the process-wide default.
func TestLastStackIsProcessDefault(t *testing.T) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		OTel:    telemetry.OTelConfig{Enabled: true, Exporter: "none"},
		Metrics: telemetry.MetricsConfig{Enabled: true, Exporter: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	if otel.GetTracerProvider() != tel.TracerProvider() {
		t.Error("global TracerProvider is not the stack's")
	}
	if otel.GetMeterProvider() != tel.MeterProvider() {
		t.Error("global MeterProvider is not the stack's")
	}
}

// The otlp_http trace exporter posts spans to /v1/traces at the endpoint URL.
func TestOTLPHTTPTraceExporterHitsEndpoint(t *testing.T) {
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
	tel, err := telemetry.Init(ctx, telemetry.Config{
		OTel: telemetry.OTelConfig{Enabled: true, Exporter: "otlp_http", Endpoint: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, span := tel.TracerProvider().Tracer("test").Start(ctx, "probe")
	span.End()
	if err := tel.Shutdown(ctx); err != nil { // flushes the batch
		t.Fatal(err)
	}
	select {
	case path := <-hit:
		if path != "/v1/traces" {
			t.Errorf("collector hit on %q, want /v1/traces", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OTLP collector was never hit")
	}
}

// Init accepts a bare host:port and a full URL as an OTLP endpoint.
func TestOTLPEndpointsAcceptHostPortAndURL(t *testing.T) {
	for _, addr := range []string{"collector:4317", "http://collector:4317", "https://collector:4317"} {
		for _, exporter := range []string{"otlp_grpc", "otlp_http"} {
			tel, err := telemetry.Init(context.Background(), telemetry.Config{
				OTel:    telemetry.OTelConfig{Enabled: exporter == "otlp_grpc", Exporter: exporter, Endpoint: addr},
				Metrics: telemetry.MetricsConfig{Enabled: true, Exporter: exporter, Endpoint: addr},
			})
			if err != nil {
				t.Errorf("%s at %q: %v", exporter, addr, err)
				continue
			}
			shortShutdown(t, tel)
		}
	}
}

// Metrics exporter "none" starts no scrape listener and exposes no registry.
func TestNoneExporterDoesNotScrape(t *testing.T) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		Metrics: telemetry.MetricsConfig{Enabled: true, Exporter: "none", AdminAddr: "127.0.0.1:0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	if tel.ScrapeURL() != "" || tel.Registerer() != nil {
		t.Errorf("'none' started a scrape: url %q, registerer %v", tel.ScrapeURL(), tel.Registerer())
	}
}

// The listener, on its resolved port, and ScrapeHandler serve Prometheus text
// with the go_* and process_* collectors.
func TestScrapeExposition(t *testing.T) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		Metrics: telemetry.MetricsConfig{Enabled: true, Exporter: "prometheus", AdminAddr: "127.0.0.1:0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	if strings.HasSuffix(strings.TrimSuffix(tel.ScrapeURL(), "/metrics"), ":0") {
		t.Errorf("ScrapeURL = %q, want the resolved port", tel.ScrapeURL())
	}
	body, hdr := scrape(t, tel.ScrapeURL())
	if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") && !strings.Contains(ct, "openmetrics-text") {
		t.Errorf("Content-Type = %q, want a Prometheus / OpenMetrics text variant", ct)
	}
	for _, want := range []string{"go_goroutines", "process_", "# HELP go_goroutines", "# TYPE go_goroutines"} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape body missing %q", want)
		}
	}
	rec := httptest.NewRecorder()
	tel.ScrapeHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "go_goroutines") {
		t.Errorf("ScrapeHandler: status %d, body has go_goroutines: %v", rec.Code, strings.Contains(rec.Body.String(), "go_goroutines"))
	}
}

// An empty adminAddr starts no listener but ScrapeHandler still serves the
// scrape; a nil stack's ScrapeHandler answers 200.
func TestEmptyAdminAddrServesThroughHandler(t *testing.T) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		Metrics: telemetry.MetricsConfig{Enabled: true, Exporter: "prometheus"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	if tel.ScrapeURL() != "" || tel.AdminErr() != nil {
		t.Errorf("empty adminAddr started a listener: %q", tel.ScrapeURL())
	}
	rec := httptest.NewRecorder()
	tel.ScrapeHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "go_goroutines") {
		t.Error("ScrapeHandler serves no runtime collectors")
	}
	var none *telemetry.Telemetry
	rec = httptest.NewRecorder()
	none.ScrapeHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("nil stack scrape: status %d, want 200", rec.Code)
	}
}

// A custom scrape path replaces the default route.
func TestCustomScrapePath(t *testing.T) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		Metrics: telemetry.MetricsConfig{Enabled: true, Exporter: "prometheus", AdminAddr: "127.0.0.1:0", Path: "/internal/metrics"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	if !strings.HasSuffix(tel.ScrapeURL(), "/internal/metrics") {
		t.Fatalf("ScrapeURL = %q, want the custom path", tel.ScrapeURL())
	}
	scrape(t, tel.ScrapeURL())
	resp, err := http.Get("http://" + strings.TrimSuffix(tel.ScrapeURL(), "/internal/metrics") + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("default path status = %d, want 404", resp.StatusCode)
	}
}

// A scrape listener bind failure arrives on AdminErr and does not fail Init.
func TestAdminBindFailureSurfacesOnAdminErr(t *testing.T) {
	first, err := telemetry.Init(context.Background(), telemetry.Config{
		Metrics: telemetry.MetricsConfig{Enabled: true, Exporter: "prometheus", AdminAddr: "127.0.0.1:0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Shutdown(context.Background()) })
	taken := strings.TrimSuffix(first.ScrapeURL(), "/metrics")
	second, err := telemetry.Init(context.Background(), telemetry.Config{
		Metrics: telemetry.MetricsConfig{Enabled: true, Exporter: "prometheus", AdminAddr: taken},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Shutdown(context.Background()) })
	if second.ScrapeURL() != "" {
		t.Errorf("ScrapeURL = %q after a failed bind, want empty", second.ScrapeURL())
	}
	select {
	case err := <-second.AdminErr():
		if err == nil {
			t.Error("expected a bind error")
		}
	case <-time.After(2 * time.Second):
		t.Error("timed out waiting for the bind error")
	}
}

// BenchmarkHTTPMiddleware measures a request through a stack with both signals on.
func BenchmarkHTTPMiddleware(b *testing.B) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		ServiceName: "bench",
		OTel:        telemetry.OTelConfig{Enabled: true, Exporter: "none"},
		Metrics:     telemetry.MetricsConfig{Enabled: true, Exporter: "none"},
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	h := tel.HTTPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	req := httptest.NewRequest(http.MethodGet, "/api/a", nil)
	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
}
