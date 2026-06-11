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
	"net/http"
	"strings"
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

// WithOTLPgRPCReader adds a periodic OTLP gRPC push exporter pointed at addr,
// which is either:
//
//   - a bare `host:port` (e.g. `"otel-collector.observability:4317"`) —
//     connects INSECURE (plain-text), the convention for collectors on a
//     trusted local network; or
//   - a full URL whose SCHEME selects transport security —
//     `http://host:4317` (plaintext) or `https://host:4317` (TLS).
//
// The URL form lets a project enable TLS straight from config.yaml
// (`endpoint: https://...`) instead of needing code. Push interval defaults to
// 60s — the OTel SDK default; pass `otlpmetricgrpc.WithCompressor("gzip")` and
// friends through opts for transport tuning.
func WithOTLPgRPCReader(ctx context.Context, addr string, opts ...otlpmetricgrpc.Option) Option {
	return func(c *config) {
		if c.err != nil {
			return
		}
		var base []otlpmetricgrpc.Option
		if strings.Contains(addr, "://") {
			// URL form — WithEndpointURL parses host/port and derives
			// insecure-vs-TLS from the scheme.
			base = []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpointURL(addr)}
		} else {
			// Bare host:port form — plaintext (insecure).
			base = []otlpmetricgrpc.Option{
				otlpmetricgrpc.WithEndpoint(addr),
				otlpmetricgrpc.WithInsecure(),
			}
		}
		exporter, err := otlpmetricgrpc.New(ctx, append(base, opts...)...)
		if err != nil {
			c.err = err
			return
		}
		c.readers = append(c.readers, sdkmetric.NewPeriodicReader(exporter))
	}
}

// WithOTLPHTTPReader adds a periodic OTLP HTTP/protobuf push exporter pointed at
// endpoint, a full URL including scheme: `"http://collector:4318"` (plaintext)
// or `"https://collector.example.com"` (TLS). The URL SCHEME selects transport
// security — `http` connects insecurely, `https` uses TLS — so no separate
// insecure/TLS toggle is needed; pass `otlpmetrichttp.WithTLSClientConfig(...)`
// in opts only for a custom certificate.
func WithOTLPHTTPReader(ctx context.Context, endpoint string, opts ...otlpmetrichttp.Option) Option {
	return func(c *config) {
		if c.err != nil {
			return
		}
		base := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpointURL(endpoint),
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

// Exporter selector values for [Config.Exporter]. An empty or unknown value
// falls back to [ExporterPrometheus] so a typo never silently disables
// metrics.
const (
	ExporterPrometheus = "prometheus"
	ExporterOTLPgRPC   = "otlp_grpc"
	ExporterOTLPHTTP   = "otlp_http"
	ExporterNone       = "none"
)

// Config is the YAML-shaped meter configuration the generated runtime
// hands to [InitFromConfig]. Mirrors the `metrics:` block in
// `config/config.yaml` so the call site reads
// `metrics.InitFromConfig(ctx, cfg.Metrics)`. Defining the type here
// keeps the exporter dispatch + admin-listener wiring in the library.
type Config struct {
	// Enabled toggles the MeterProvider install AND the admin
	// listener startup. False = no-op meter (otelhttp's recorder
	// stays silent).
	Enabled bool `yaml:"enabled"`
	// Exporter selects the data path:
	//   - "prometheus" / "" - pull on AdminAddr (default)
	//   - "otlp_grpc"  - push via OTLP gRPC
	//   - "otlp_http"  - push via OTLP HTTP/protobuf
	//   - "none"       - meter installed without exporter (testing)
	Exporter string `yaml:"exporter"`
	// Endpoint is the collector address for OTLP exporters. Ignored
	// for "prometheus" / "none".
	Endpoint string `yaml:"endpoint"`
	// AdminAddr is the bind address for the Prometheus scrape
	// listener. Ignored unless Exporter == "prometheus".
	AdminAddr string `yaml:"adminAddr"`
	// Path is the scrape route (default "/metrics"). Override when a
	// reverse proxy already claims that path.
	Path string `yaml:"path"`
}

// InitFromConfig dispatches the exporter selection encoded in c, then
// starts the admin scrape listener for the prometheus path. Returns
// the active MeterProvider and the admin server (nil when no admin
// listener was needed) so the caller can defer Shutdown on both.
//
// When c.Enabled is false everything is (nil, nil, nil) - the caller
// can keep its shutdown code single-pathed.
func InitFromConfig(ctx context.Context, c Config) (*sdkmetric.MeterProvider, *adminServer, error) {
	if !c.Enabled {
		return nil, nil, nil
	}
	var (
		opts         []Option
		startScrape  bool
		runtimeStats bool
	)
	switch c.Exporter {
	case ExporterOTLPgRPC:
		opts = append(opts, WithOTLPgRPCReader(ctx, c.Endpoint))
	case ExporterOTLPHTTP:
		opts = append(opts, WithOTLPHTTPReader(ctx, c.Endpoint))
	case ExporterNone:
		// Install a manual reader so the meter exists but is silent: a
		// ManualReader only collects on an explicit Collect() call, which
		// nothing here makes, so nothing is ever pushed or served. Without
		// a reader, Init() falls back to the Prometheus default — which
		// would secretly serve metrics that "none" promises to suppress.
		opts = append(opts, WithReader(sdkmetric.NewManualReader()))
	default:
		// "prometheus" + any unknown value default to scrape so a typo
		// never silently turns metrics off.
		opts = append(opts, WithPrometheusReader())
		startScrape = true
		runtimeStats = true
	}

	provider, err := Init(opts...)
	if err != nil {
		return nil, nil, err
	}
	if runtimeStats {
		_ = RegisterRuntimeCollectors()
	}
	if !startScrape {
		return provider, nil, nil
	}
	srv, errCh := StartAdmin(c.AdminAddr, WithPath(c.Path))
	return provider, &adminServer{srv: srv, errCh: errCh, addr: c.AdminAddr, path: c.Path}, nil
}

// adminServer bundles the admin http.Server with the post-Serve error
// channel StartAdmin returns. Callers receive it from [InitFromConfig]
// and pass it to [ShutdownAdminFromConfig] for graceful teardown. Kept
// unexported so the field set can grow without breaking the call site.
type adminServer struct {
	srv   *http.Server
	errCh <-chan error
	addr  string
	path  string
}

// HTTPServer returns the underlying *http.Server (nil when the admin
// listener was not started). Tests inspect this; production code only
// needs the value to pass through to ShutdownAdmin.
func (a *adminServer) HTTPServer() *http.Server {
	if a == nil {
		return nil
	}
	return a.srv
}

// ErrCh exposes the StartAdmin error channel for callers that want to
// log a non-fatal exit from the scrape listener.
func (a *adminServer) ErrCh() <-chan error {
	if a == nil {
		return nil
	}
	return a.errCh
}

// Addr / Path expose the bound values for log lines ("scrape listening
// on :9090/metrics").
func (a *adminServer) Addr() string {
	if a == nil {
		return ""
	}
	return a.addr
}

func (a *adminServer) Path() string {
	if a == nil {
		return ""
	}
	return a.path
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
