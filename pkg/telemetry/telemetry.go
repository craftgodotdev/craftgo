// Package telemetry wires a service's traces and metrics as one thing.
//
// The signals are configured separately but must agree on three facts:
// the `service.name` both report under, whether the HTTP layer is
// instrumented at all (one otelhttp wrapper emits both), and shutdown.
// [Init] returns a value that owns all three, plus the Prometheus
// registry and scrape listener - no package globals, so two stacks can
// coexist.
//
// [pkg/otel] and [pkg/metrics] keep their own entry points for projects
// that wire the signals by hand.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	prom "github.com/prometheus/client_golang/prometheus"

	"github.com/craftgodotdev/craftgo/pkg/metrics"
	craftotel "github.com/craftgodotdev/craftgo/pkg/otel"
	"github.com/craftgodotdev/craftgo/pkg/server"
)

// Config is the telemetry block of a project's config.yaml. ServiceName
// sits above both signals and fills in for an empty per-signal name.
type Config struct {
	ServiceName string        `yaml:"serviceName"`
	OTel        OTelConfig    `yaml:"otel"`
	Metrics     MetricsConfig `yaml:"metrics"`
}

// OTelConfig / MetricsConfig re-export the per-signal config shapes so a
// project can name them without importing both packages.
type (
	OTelConfig    = craftotel.Config
	MetricsConfig = metrics.Config
)

// Telemetry is a live stack: providers, the registry the scrape gathers,
// and the listener serving it. A nil *Telemetry is usable - every method
// degrades to a no-op, so callers never branch on it.
type Telemetry struct {
	serviceName string

	tracers *sdktrace.TracerProvider
	meters  *sdkmetric.MeterProvider

	registry *prom.Registry
	admin    *http.Server
	adminErr <-chan error
	adminURL string
}

// Init builds the stack described by c. Either signal, both, or neither
// may be enabled. The Prometheus scrape gets its own listener
// (`metrics.adminAddr`), not the public API port, so it can be firewalled
// separately.
func Init(ctx context.Context, c Config) (*Telemetry, error) {
	t := &Telemetry{serviceName: c.ServiceName}
	c.OTel.ServiceName = orDefault(c.OTel.ServiceName, c.ServiceName)
	c.Metrics.ServiceName = orDefault(c.Metrics.ServiceName, c.ServiceName)

	tp, err := craftotel.InitFromConfig(ctx, c.OTel)
	if err != nil {
		return nil, fmt.Errorf("telemetry: traces: %w", err)
	}
	t.tracers = tp

	if err := t.initMetrics(c.Metrics); err != nil {
		// Traces are already live - a failed Init must leave nothing running.
		_ = t.Shutdown(ctx)
		return nil, fmt.Errorf("telemetry: metrics: %w", err)
	}
	return t, nil
}

// initMetrics installs the meter on a registry this Telemetry owns.
func (t *Telemetry) initMetrics(c metrics.Config) error {
	if !c.Enabled {
		return nil
	}
	opts := []metrics.Option{metrics.WithServiceName(c.ServiceName)}
	scrape := false
	switch c.Exporter {
	case metrics.ExporterOTLPgRPC:
		opts = append(opts, metrics.WithOTLPgRPCReader(context.Background(), c.Endpoint))
	case metrics.ExporterOTLPHTTP:
		opts = append(opts, metrics.WithOTLPHTTPReader(context.Background(), c.Endpoint))
	case metrics.ExporterNone:
		// A reader nothing collects from: instruments resolve, nothing leaves.
		opts = append(opts, metrics.WithReader(sdkmetric.NewManualReader()))
	default:
		// Unknown values scrape too, so a typo never silently kills metrics.
		t.registry = metrics.NewRegistry()
		opts = append(opts, metrics.WithPrometheusReaderFor(t.registry))
		scrape = true
	}
	mp, err := metrics.Init(opts...)
	if err != nil {
		return err
	}
	t.meters = mp
	if !scrape {
		return nil
	}
	if err := metrics.RegisterRuntimeCollectorsOn(t.registry); err != nil {
		return err
	}
	srv, errCh := metrics.StartAdmin(c.AdminAddr,
		metrics.WithPath(c.Path),
		metrics.WithSnapshotHandler(metrics.SnapshotHandlerFor(t.registry)))
	if srv == nil {
		return nil // empty AdminAddr opts out of the listener
	}
	t.admin, t.adminErr = srv, errCh
	t.adminURL = srv.Addr + orDefault(c.Path, metrics.DefaultMetricsPath)
	return nil
}

// HTTPMiddleware instruments every request against THIS stack's
// providers, not the global slots. With neither signal configured it
// returns a plain pass-through, so an unconfigured process pays nothing.
func (t *Telemetry) HTTPMiddleware() server.Middleware {
	if t == nil || (t.tracers == nil && t.meters == nil) {
		return func(next http.Handler) http.Handler { return next }
	}
	opts := []otelhttp.Option{}
	if t.tracers != nil {
		opts = append(opts, otelhttp.WithTracerProvider(t.tracers))
	}
	if t.meters != nil {
		opts = append(opts, otelhttp.WithMeterProvider(t.meters))
	}
	return craftotel.HTTPMiddlewareWith(t.serviceName, opts...)
}

// TracerProvider / MeterProvider expose the stack's providers for code
// that wants its own spans or instruments. Both return the OTel no-op
// when that signal is off, so call sites never nil-check.
func (t *Telemetry) TracerProvider() oteltrace.TracerProvider {
	if t == nil || t.tracers == nil {
		return tracenoop.NewTracerProvider()
	}
	return t.tracers
}

func (t *Telemetry) MeterProvider() otelmetric.MeterProvider {
	if t == nil || t.meters == nil {
		return metricnoop.NewMeterProvider()
	}
	return t.meters
}

// Registerer exposes the registry backing the scrape, for attaching your
// own client_golang collectors. Nil when this stack has no scrape.
func (t *Telemetry) Registerer() prom.Registerer {
	if t == nil || t.registry == nil {
		return nil
	}
	return t.registry
}

// ScrapeURL is the `host:port/path` the listener bound to, or "" when
// none started. The port is the resolved one, so a `:0` bind is loggable.
func (t *Telemetry) ScrapeURL() string {
	if t == nil {
		return ""
	}
	return t.adminURL
}

// AdminErr surfaces a post-startup failure of the scrape listener, or nil
// when none runs. Callers must check for nil: receiving from a nil channel
// blocks forever.
func (t *Telemetry) AdminErr() <-chan error {
	if t == nil {
		return nil
	}
	return t.adminErr
}

// Shutdown closes everything this stack owns, listener first, then the
// providers - whose Shutdown flushes any pending push batch. Errors are
// collected, not short-circuited, so one failure cannot skip the rest.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	var errs []error
	if t.admin != nil {
		if err := metrics.ShutdownAdmin(ctx, t.admin); err != nil {
			errs = append(errs, fmt.Errorf("metrics admin: %w", err))
		}
		t.admin = nil
	}
	if t.meters != nil {
		if err := t.meters.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter provider: %w", err))
		}
		t.meters = nil
	}
	if t.tracers != nil {
		if err := t.tracers.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tracer provider: %w", err))
		}
		t.tracers = nil
	}
	return errors.Join(errs...)
}

// orDefault returns v, or fallback when v is empty.
func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
