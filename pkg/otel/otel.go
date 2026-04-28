// Package otel exposes a runtime-toggleable OpenTelemetry HTTP
// middleware for craftgo Servers. Until [Init] is called the wrapper
// is a pass-through, so projects that don't want telemetry pay
// nothing. Calling [Disable] returns the middleware to no-op mode —
// handy for tests that want a deterministic environment.
package otel

import (
	"net/http"
	"sync/atomic"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dropship-dev/craftgo/pkg/server"
)

// enabled tracks whether instrumentation should run. Stored as an
// atomic.Bool so toggling is safe from any goroutine.
var enabled atomic.Bool

// Init turns OpenTelemetry HTTP instrumentation ON. The actual SDK
// (tracer / meter / propagator) is configured by the caller via the
// standard `go.opentelemetry.io/otel` package — this function only
// flips the gate that [HTTPMiddleware] checks at request time.
//
// Idempotent: calling Init twice is safe.
func Init() { enabled.Store(true) }

// InitDefault enables instrumentation AND wires a default in-process
// SDK so trace_id and span_id show up in logs immediately. The default
// TracerProvider has no exporter — spans are generated with valid IDs
// but go nowhere — perfect for local `go run` sessions where the only
// observability you need is the IDs flowing into the structured log.
//
// For production, call [Init] (just flip the gate) and configure your
// own TracerProvider via the standard OTel SDK so spans actually ship.
func InitDefault() {
	tp := sdktrace.NewTracerProvider()
	otelapi.SetTracerProvider(tp)
	otelapi.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	enabled.Store(true)
}

// Disable returns the middleware to no-op mode without dismantling
// any SDK configuration. Tests use this to isolate runs.
func Disable() { enabled.Store(false) }

// IsEnabled reports the current toggle state.
func IsEnabled() bool { return enabled.Load() }

// HTTPMiddleware returns a server.Middleware that wraps every request
// with `otelhttp.NewHandler` when the gate is open, and is a
// pass-through otherwise. operation is the span name surfaced when the
// wrapper is active.
//
// Wire it with `srv.Use(otel.HTTPMiddleware("api"))`. Even on a
// disabled gate the call is cheap — a single atomic load — so it's
// fine to leave permanently in main.go.
func HTTPMiddleware(operation string) server.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled.Load() {
				next.ServeHTTP(w, r)
				return
			}
			otelhttp.NewHandler(next, operation).ServeHTTP(w, r)
		})
	}
}
