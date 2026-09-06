package telemetry

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"

	"github.com/craftgodotdev/craftgo/pkg/server"
)

// propagator carries the W3C trace context and baggage on the wire. The
// HTTP wrapper of a traced stack reads and writes it, and [Init] installs
// it as the process-wide default.
var propagator = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})

// noopPropagator extracts and injects nothing. A stack without traces
// must not adopt a caller's trace context: the no-op tracer would carry
// the caller's span through, and the response would echo the caller's
// own span id as the server's.
var noopPropagator = propagation.NewCompositeTextMapPropagator()

// HTTPMiddleware instruments every request against this stack's
// providers, never the global slots: one otelhttp wrapper emits the span
// and the http.server.* instruments, and a traced stack injects the W3C
// trace context (`traceparent`, plus `tracestate` when set) onto the
// response so clients can attach to the same trace. A signal that is off
// uses the no-op provider, and with both off the middleware is a plain
// pass-through, so an unconfigured process pays nothing.
func (t *Telemetry) HTTPMiddleware() server.Middleware {
	if t == nil || (t.tracers == nil && t.meters == nil) {
		return func(next http.Handler) http.Handler { return next }
	}
	prop := propagation.TextMapPropagator(noopPropagator)
	if t.tracers != nil {
		prop = propagator
	}
	opts := []otelhttp.Option{
		otelhttp.WithTracerProvider(t.TracerProvider()),
		otelhttp.WithMeterProvider(t.MeterProvider()),
		otelhttp.WithPropagators(prop),
	}
	return func(next http.Handler) http.Handler {
		return instrument(next, t.serviceName, prop, opts...)
	}
}

// instrument wraps next in otelhttp under the span name operation and
// writes the trace context prop carries onto the response inside the
// wrapper - the only window where the span exists but nothing has been
// written yet. Built once per wrap, never per request: NewHandler
// resolves providers and creates instruments eagerly.
func instrument(next http.Handler, operation string, prop propagation.TextMapPropagator, opts ...otelhttp.Option) http.Handler {
	withTraceHeaders := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prop.Inject(r.Context(), propagation.HeaderCarrier(w.Header()))
		next.ServeHTTP(w, r)
	})
	return otelhttp.NewHandler(withTraceHeaders, operation, opts...)
}
