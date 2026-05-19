// Package metrics is the OpenTelemetry-backed metrics surface for
// craftgo runtimes. It owns the project-wide MeterProvider and the
// Prometheus exporter that the admin server scrapes.
//
// The package itself records no metrics directly. The HTTP
// instruments (`http.server.request.duration` histogram, request /
// response size histograms, active-request gauge) are emitted by
// `otelhttp.NewHandler` - wired via [pkg/otel.HTTPMiddleware] -
// against whatever MeterProvider [Init] (or [InitDefault]) installs
// on the global slot. Application code that wants its own counters
// or histograms calls `otel.Meter("...")` directly; this package
// exists to bootstrap and expose, not to invent a parallel API.
package metrics

import (
	"context"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// enabled tracks whether [Init] / [InitDefault] has installed a real
// MeterProvider. The flag is mostly diagnostic - `otelhttp` emits
// metrics against the global provider regardless of this gate, so
// a never-Init'd process simply receives a no-op MeterProvider's
// silent recording.
var enabled atomic.Bool

// registry is the Prometheus registry the package's exporter writes
// into. Held at package scope so [SnapshotHandler] (and tests) can
// scrape without re-discovering the SDK plumbing on every call.
var registry = prom.NewRegistry()

// IsEnabled reports whether the package has installed a MeterProvider.
// Returns false until [Init] / [InitDefault] succeeds.
func IsEnabled() bool { return enabled.Load() }

// Registerer exposes the underlying Prometheus registry so application
// code can attach process / runtime collectors (`prometheus.NewGoCollector`,
// `prometheus.NewProcessCollector`) on top of the OTel-bridged metrics.
// The returned interface is the standard `prometheus.Registerer`, so
// any client_golang-compatible metric surfaces alongside the OTel ones
// on the same /metrics scrape.
func Registerer() prom.Registerer { return registry }

// Option configures the MeterProvider built by [Init]. Each option
// either appends a Reader to the provider (Prometheus pull, OTLP
// gRPC push, OTLP HTTP push) or attaches metadata. Errors that
// occur while building exporters are captured on the config; the
// first error short-circuits the rest and surfaces from [Init].
type Option func(*config)

// config carries the MeterProvider settings between [Option]
// closures and [Init]. Held off the package surface - callers only
// ever see the typed [Option] constructors.
type config struct {
	readers []sdkmetric.Reader
	err     error
}

// WithPrometheusReader adds a Prometheus pull exporter scoped to the
// package registry. Pair it with [StartAdmin] (or wire
// [SnapshotHandler] manually) to expose `/metrics` for scrapers.
//
// The default when [Init] is called with no options.
func WithPrometheusReader() Option {
	return func(c *config) {
		if c.err != nil {
			return
		}
		exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
		if err != nil {
			c.err = err
			return
		}
		c.readers = append(c.readers, exporter)
	}
}

// WithOTLPgRPCReader adds a periodic OTLP gRPC push exporter pointed
// at addr (e.g. `"otel-collector.observability:4317"`). Push interval
// defaults to 60s - the OTel SDK default; pass
// `otlpmetricgrpc.WithCompressor("gzip")` and friends through opts
// for transport tuning. By default the connection is INSECURE
// (plain-text, no TLS) so collectors on the local k8s network work
// out of the box; supply `otlpmetricgrpc.WithTLSCredentials(...)` to
// override for cross-cluster traffic.
func WithOTLPgRPCReader(ctx context.Context, addr string, opts ...otlpmetricgrpc.Option) Option {
	return func(c *config) {
		if c.err != nil {
			return
		}
		base := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(addr),
			otlpmetricgrpc.WithInsecure(),
		}
		exporter, err := otlpmetricgrpc.New(ctx, append(base, opts...)...)
		if err != nil {
			c.err = err
			return
		}
		c.readers = append(c.readers, sdkmetric.NewPeriodicReader(exporter))
	}
}

// WithOTLPHTTPReader adds a periodic OTLP HTTP/protobuf push exporter
// pointed at endpoint (e.g. `"http://collector:4318"` for plain HTTP
// or `"https://collector.example.com"` for TLS). Same insecure-by-
// default trade-off as [WithOTLPgRPCReader]; pass
// `otlpmetrichttp.WithTLSClientConfig(...)` to opt into TLS.
func WithOTLPHTTPReader(ctx context.Context, endpoint string, opts ...otlpmetrichttp.Option) Option {
	return func(c *config) {
		if c.err != nil {
			return
		}
		base := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(endpoint),
			otlpmetrichttp.WithInsecure(),
		}
		exporter, err := otlpmetrichttp.New(ctx, append(base, opts...)...)
		if err != nil {
			c.err = err
			return
		}
		c.readers = append(c.readers, sdkmetric.NewPeriodicReader(exporter))
	}
}

// WithReader is the escape hatch: hand any pre-built `sdkmetric.Reader`
// to [Init]. Use it when the project needs a custom exporter (in-memory
// for tests, third-party SaaS, exotic transport) the Prometheus / OTLP
// helpers don't cover. Stack as many WithReader options as needed -
// the same MeterProvider fans every metric to all readers.
func WithReader(r sdkmetric.Reader) Option {
	return func(c *config) {
		if c.err != nil {
			return
		}
		c.readers = append(c.readers, r)
	}
}

// Init wires an OTel MeterProvider with the supplied readers. With
// zero options it defaults to [WithPrometheusReader] so the dev-mode
// `/metrics` scrape works without extra plumbing. Production
// deployments compose the readers they actually need:
//
//	// Pull (Prometheus scrape):
//	metrics.Init(metrics.WithPrometheusReader())
//
//	// Push (OTLP gRPC to collector):
//	metrics.Init(metrics.WithOTLPgRPCReader(ctx, "collector:4317"))
//
//	// Both — side-by-side scrape and push:
//	metrics.Init(
//	    metrics.WithPrometheusReader(),
//	    metrics.WithOTLPgRPCReader(ctx, "collector:4317"),
//	)
//
// Returns the configured provider so callers can shut it down via
// `provider.Shutdown(ctx)` during graceful termination - important
// for OTLP push so the final batch flushes before the process exits.
func Init(opts ...Option) (*sdkmetric.MeterProvider, error) {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.err != nil {
		return nil, cfg.err
	}
	if len(cfg.readers) == 0 {
		WithPrometheusReader()(cfg)
		if cfg.err != nil {
			return nil, cfg.err
		}
	}
	sdkOpts := make([]sdkmetric.Option, 0, len(cfg.readers))
	for _, r := range cfg.readers {
		sdkOpts = append(sdkOpts, sdkmetric.WithReader(r))
	}
	provider := sdkmetric.NewMeterProvider(sdkOpts...)
	otel.SetMeterProvider(provider)
	enabled.Store(true)
	return provider, nil
}

// InitDefault is the dev-friendly shorthand: it calls [Init] with the
// Prometheus reader and installs the standard Go runtime / process
// collectors so the `/metrics` scrape surfaces `go_*` (goroutines,
// GC, memory) and `process_*` (RSS, CPU, FDs) alongside the HTTP
// instruments `otelhttp` emits. Production projects pick the more
// granular [Init] when they want a different reader set.
//
// Errors from collector registration are surfaced; the MeterProvider
// itself is already installed by then, so the HTTP histogram path
// keeps working even if a runtime collector fails to register (rare
// but possible if a host strips procfs).
func InitDefault() (*sdkmetric.MeterProvider, error) {
	provider, err := Init(WithPrometheusReader())
	if err != nil {
		return nil, err
	}
	if err := RegisterRuntimeCollectors(); err != nil {
		return provider, err
	}
	return provider, nil
}

// RegisterRuntimeCollectors attaches the standard Go runtime and
// process Prometheus collectors to the package registry. Call it
// after [Init] when you want `go_*` / `process_*` series alongside
// the OTel-bridged metrics - the config-driven main.tmpl pipeline
// uses it for the prometheus exporter path. Idempotent: a duplicate
// registration is silently swallowed so repeated boots in tests
// don't break.
func RegisterRuntimeCollectors() error {
	if regErr := registry.Register(collectors.NewGoCollector()); regErr != nil {
		if _, dup := regErr.(prom.AlreadyRegisteredError); !dup {
			return regErr
		}
	}
	if regErr := registry.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); regErr != nil {
		if _, dup := regErr.(prom.AlreadyRegisteredError); !dup {
			return regErr
		}
	}
	return nil
}
