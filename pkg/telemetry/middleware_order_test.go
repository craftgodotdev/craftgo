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

// HTTPMiddleware puts the span on the request context; AccessLog reads that
// context through WithContext. Wrapped in that order the access line carries
// trace_id and span_id, and in the other order it carries neither.
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
