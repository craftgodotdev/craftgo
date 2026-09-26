package telemetry

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"

	"github.com/craftgodotdev/craftgo/pkg/server"
)

// propagator is the W3C trace-context and baggage propagator of a traced stack.
var propagator = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})

// noopPropagator is the propagator of a stack without traces: adopting a
// caller's context would make the no-op tracer echo the caller's span id back.
var noopPropagator = propagation.NewCompositeTextMapPropagator()

// HTTPMiddleware records a span and the http.server.* instruments for every
// request against this stack's providers; a traced stack also writes the W3C
// `traceparent` onto the response.
func (t *Telemetry) HTTPMiddleware() server.Middleware {
	if !t.instrumented() {
		return func(next http.Handler) http.Handler { return next }
	}
	prop := t.propagator()
	opts := []otelhttp.Option{
		otelhttp.WithTracerProvider(t.TracerProvider()),
		otelhttp.WithMeterProvider(t.MeterProvider()),
		otelhttp.WithPropagators(prop),
	}
	return func(next http.Handler) http.Handler {
		return instrument(next, prop, opts...)
	}
}

// instrumented reports whether the stack emits any signal.
func (t *Telemetry) instrumented() bool {
	return t != nil && (t.tracers != nil || t.meters != nil)
}

func (t *Telemetry) propagator() propagation.TextMapPropagator {
	if t.tracers != nil {
		return propagator
	}
	return noopPropagator
}

// instrument wraps next in otelhttp, which names a span after the method and the
// matched route, and writes prop's trace context onto the response before next
// runs. It builds instruments, so it runs once per wrap.
func instrument(next http.Handler, prop propagation.TextMapPropagator, opts ...otelhttp.Option) http.Handler {
	withTraceHeaders := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prop.Inject(r.Context(), propagation.HeaderCarrier(w.Header()))
		next.ServeHTTP(w, r)
	})
	return otelhttp.NewHandler(withTraceHeaders, "", opts...)
}
