package telemetry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/craftgodotdev/craftgo/pkg/log"
	"github.com/craftgodotdev/craftgo/pkg/server"
	"github.com/craftgodotdev/craftgo/pkg/telemetry"
)

// A panic's log line carries the request's trace ids when HTTPMiddleware is installed with
// server.WithTelemetry, outside Recovery.
func TestPanicLineCarriesTraceIDsBehindWithTelemetry(t *testing.T) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		ServiceName: "ordering",
		OTel:        telemetry.OTelConfig{Enabled: true, Exporter: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	core, logs := observer.New(zap.InfoLevel)
	prev := log.Default()
	log.SetDefault(log.NewZap(zap.New(core)))
	t.Cleanup(func() { log.SetDefault(prev) })

	srv := server.New(nil, server.WithTelemetry(tel.HTTPMiddleware()))
	srv.Mux().HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	lines := logs.FilterMessage("panic recovered").All()
	if rec.Code != http.StatusInternalServerError || len(lines) != 1 {
		t.Fatalf("status %d with %d panic lines, want 500 and one", rec.Code, len(lines))
	}
	fields := lines[0].ContextMap()
	for _, key := range []string{"trace_id", "span_id"} {
		if id, _ := fields[key].(string); id == "" {
			t.Errorf("the panic line has no %s: %v", key, fields)
		}
	}
}

// The access line carries trace_id and span_id only when AccessLog runs inside HTTPMiddleware.
func TestAccessLogReportsTraceIDsOnlyBehindTheWrapper(t *testing.T) {
	tel, err := telemetry.Init(context.Background(), telemetry.Config{
		ServiceName: "ordering",
		OTel:        telemetry.OTelConfig{Enabled: true, Exporter: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })

	for _, c := range []struct {
		name         string
		wrapperFirst bool
		wantIDs      bool
	}{
		{"wrapper then access log", true, true},
		{"access log then wrapper", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			core, logs := observer.New(zap.InfoLevel)
			srv := server.New(nil)
			if c.wrapperFirst {
				srv.Use(tel.HTTPMiddleware())
				srv.Use(server.AccessLog(log.NewZap(zap.New(core))))
			} else {
				srv.Use(server.AccessLog(log.NewZap(zap.New(core))))
				srv.Use(tel.HTTPMiddleware())
			}
			srv.Mux().HandleFunc("GET /things", func(w http.ResponseWriter, r *http.Request) {})
			srv.Handler().ServeHTTP(httptest.NewRecorder(),
				httptest.NewRequest(http.MethodGet, "/things", nil))

			if logs.Len() != 1 {
				t.Fatalf("want 1 access line, got %d", logs.Len())
			}
			fields := logs.All()[0].ContextMap()
			for _, key := range []string{"trace_id", "span_id"} {
				id, _ := fields[key].(string)
				if got := id != ""; got != c.wantIDs {
					t.Errorf("%s present = %v, want %v (value %q)", key, got, c.wantIDs, id)
				}
			}
		})
	}
}
