package telemetry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/telemetry"
)

func serve(t *testing.T, tel *telemetry.Telemetry) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /api/things/{id}", tel.HTTPMiddleware()(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })))
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/things/42", nil))
}

// The top-level serviceName must reach both signals without being set twice.
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
	if !strings.Contains(body, `target_info{service_name="todo"}`) {
		t.Errorf("metrics carry no service_name=todo:\n%s", body[:min(len(body), 400)])
	}
	if !strings.Contains(body, "http_server_request_duration_seconds_count") {
		t.Errorf("no HTTP instruments in the scrape:\n%s", body[:min(len(body), 400)])
	}
}

// Metrics must survive tracing being off - the two are separate switches.
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

// Two stacks must not share state; the second must not disturb the first.
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
	}{{a, `target_info{service_name="a"}`}, {b, `target_info{service_name="b"}`}} {
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

// An unconfigured stack must add nothing to the request path, and a nil one
// must stay usable.
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

// seriesCount counts SERIES lines for a family, skipping the # HELP / # TYPE
// header lines a substring match would also hit.
func seriesCount(body, family string) int {
	n := 0
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, family) {
			n++
		}
	}
	return n
}
