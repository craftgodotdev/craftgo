// Package otel exposes the OpenTelemetry HTTP middleware + tracer
// bootstrap helpers for craftgo runtimes. The package owns the global
// TracerProvider; HTTP instrumentation is gated by an atomic flag so a
// never-Init'd process pays one atomic load per request and nothing
// else.
//
// Pick one of:
//
//	otel.InitDefault()                        // in-process spans, ids in logs only
//	otel.Init(otel.WithStdoutExporter())      // JSON spans on stdout
//	otel.Init(otel.WithOTLPgRPCExporter(ctx, "collector:4317"))
//	otel.Init(otel.WithOTLPHTTPExporter(ctx, "http://collector:4318"))
//
// Multiple exporter options can be stacked — every span is fanned to
// each registered processor (canonical migration shape: stdout for
// debugging while OTLP is being validated).
package otel

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/dropship-dev/craftgo/pkg/server"
)

// enabled tracks whether instrumentation should run. Stored as an
// atomic.Bool so toggling is safe from any goroutine.
var enabled atomic.Bool

// Option configures the TracerProvider built by [Init]. Each option
// either appends a SpanProcessor (stdout, OTLP gRPC, OTLP HTTP) or
// attaches resource metadata. Errors that occur while building
// exporters are captured on the config; the first error
// short-circuits the rest and surfaces from [Init].
type Option func(*config)

// config carries the TracerProvider settings between [Option]
// closures and [Init]. Held off the package surface — callers only
// ever see the typed [Option] constructors.
type config struct {
	processors  []sdktrace.SpanProcessor
	serviceName string
	err         error
}

// WithServiceName attaches the supplied service.name to the
// TracerProvider's Resource so spans self-label correctly when shipped
// to a backend. Defaults to "craftgo" when no option is supplied.
func WithServiceName(name string) Option {
	return func(c *config) {
		if name != "" {
			c.serviceName = name
		}
	}
}

// WithStdoutExporter writes every span as a JSON object on stdout.
// Useful for local debugging where running a full collector is
// overkill — `tail -f` the program output and you have a span trace.
func WithStdoutExporter() Option {
	return func(c *config) {
		if c.err != nil {
			return
		}
		exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			c.err = fmt.Errorf("stdout trace exporter: %w", err)
			return
		}
		c.processors = append(c.processors, sdktrace.NewBatchSpanProcessor(exp))
	}
}

// WithOTLPgRPCExporter pushes spans to an OTLP collector via gRPC. By
// default the connection is INSECURE (plain-text, no TLS) so collectors
// on the local k8s network work out of the box; supply
// `otlptracegrpc.WithTLSCredentials(...)` to override for cross-cluster
// traffic. addr is the collector's `host:port`
// (e.g. `"otel-collector.observability:4317"`).
func WithOTLPgRPCExporter(ctx context.Context, addr string, opts ...otlptracegrpc.Option) Option {
	return func(c *config) {
		if c.err != nil {
			return
		}
		base := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(addr),
			otlptracegrpc.WithInsecure(),
		}
		exp, err := otlptracegrpc.New(ctx, append(base, opts...)...)
		if err != nil {
			c.err = fmt.Errorf("otlp grpc trace exporter: %w", err)
			return
		}
		c.processors = append(c.processors, sdktrace.NewBatchSpanProcessor(exp))
	}
}

// WithOTLPHTTPExporter pushes spans to an OTLP collector via
// HTTP/protobuf. endpoint is the full URL (`"http://collector:4318"` or
// `"https://collector.example.com"`). Same insecure-by-default
// trade-off as [WithOTLPgRPCExporter] — pass
// `otlptracehttp.WithTLSClientConfig(...)` to opt into TLS.
func WithOTLPHTTPExporter(ctx context.Context, endpoint string, opts ...otlptracehttp.Option) Option {
	return func(c *config) {
		if c.err != nil {
			return
		}
		base := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(endpoint),
			otlptracehttp.WithInsecure(),
		}
		exp, err := otlptracehttp.New(ctx, append(base, opts...)...)
		if err != nil {
			c.err = fmt.Errorf("otlp http trace exporter: %w", err)
			return
		}
		c.processors = append(c.processors, sdktrace.NewBatchSpanProcessor(exp))
	}
}

// Init wires a TracerProvider with the supplied options, installs it
// on the global slot, and flips the HTTP middleware gate ON. With
// zero options the provider has no exporter — spans get valid IDs but
// go nowhere, which is exactly what you want for `go run` sessions
// where the only observability signal needed is trace_id / span_id
// flowing into the logs.
//
// Returns the configured provider so callers can shut it down via
// `provider.Shutdown(ctx)` during graceful termination — important
// for OTLP exporters so the final batch flushes before the process
// exits.
func Init(opts ...Option) (*sdktrace.TracerProvider, error) {
	cfg := &config{serviceName: "craftgo"}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.err != nil {
		return nil, cfg.err
	}
	// NewWithAttributes (not Merge) avoids the SchemaURL-conflict
	// error path the OTel SDK raises when the project default
	// resource and our user-supplied attributes carry different
	// schema versions. Service.name is the only resource attribute
	// the framework owns by default; users wanting more attach
	// their own provider via sdkmetric/SetTracerProvider.
	res := sdkresource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.serviceName),
	)
	tpOpts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	for _, p := range cfg.processors {
		tpOpts = append(tpOpts, sdktrace.WithSpanProcessor(p))
	}
	tp := sdktrace.NewTracerProvider(tpOpts...)
	otelapi.SetTracerProvider(tp)
	otelapi.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	enabled.Store(true)
	return tp, nil
}

// InitDefault is the dev-friendly shorthand: no exporter, no resource
// customisation, just a TracerProvider that produces valid trace ids
// for log correlation. Production code calls [Init] with the
// exporters it actually needs.
func InitDefault() *sdktrace.TracerProvider {
	tp, _ := Init()
	return tp
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
// Once the span is active on the request context, the middleware also
// injects the W3C trace context onto the response via the configured
// `OTel TextMapPropagator` (`otelapi.GetTextMapPropagator`). With the
// default `propagation.TraceContext{}` propagator that lands a standard
// `traceparent` (and `tracestate` when set) header on the response —
// the same wire format the spec uses for request propagation, so
// downstream services / clients can re-attach to the same trace tree
// without bespoke header handling. Headers are written BEFORE the
// downstream handler runs, which keeps them in the outbound response
// regardless of whether the handler streams or writes a single body.
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
			// otelhttp creates the span on a NEW request context, then
			// calls its `next` with that updated request. Inserting an
			// inner handler here gives us a hook AFTER the span exists
			// but BEFORE the user's handler writes anything — the only
			// safe window to inject response headers.
			withTraceHeaders := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				otelapi.GetTextMapPropagator().Inject(r.Context(), propagation.HeaderCarrier(w.Header()))
				next.ServeHTTP(w, r)
			})
			otelhttp.NewHandler(withTraceHeaders, operation).ServeHTTP(w, r)
		})
	}
}
