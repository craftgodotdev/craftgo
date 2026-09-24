// Package telemetry builds a service's traces and metrics as one stack.
// [Init] returns a [Telemetry] that owns the providers, the Prometheus scrape
// and their shutdown; it instruments HTTP with [Telemetry.HTTPMiddleware] and
// gRPC with [Telemetry.GRPCServerHandler] and [Telemetry.GRPCClientHandler].
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel"
	otelmetric "go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	prom "github.com/prometheus/client_golang/prometheus"
)

// Telemetry is a live stack built by [Init]. With both signals off, or on a
// nil *Telemetry, every method degrades to a no-op.
type Telemetry struct {
	serviceName string

	tracers *sdktrace.TracerProvider
	meters  *sdkmetric.MeterProvider

	registry *prom.Registry
	admin    *http.Server
	adminErr <-chan error
	adminURL string
}

// Init builds the stack c describes and makes each enabled signal the
// process-wide otel default. It fails, leaving nothing running, when a signal
// cannot be set up; a scrape listener bind failure arrives on [Telemetry.AdminErr].
func Init(ctx context.Context, c Config) (*Telemetry, error) {
	t := &Telemetry{serviceName: c.ServiceName}
	c.OTel.ServiceName = orDefault(c.OTel.ServiceName, c.ServiceName)
	c.Metrics.ServiceName = orDefault(c.Metrics.ServiceName, c.ServiceName)
	if err := t.initTraces(ctx, c.OTel); err != nil {
		return nil, fmt.Errorf("telemetry: traces: %w", err)
	}
	if err := t.initMetrics(ctx, c.Metrics); err != nil {
		_ = t.Shutdown(ctx)
		return nil, fmt.Errorf("telemetry: metrics: %w", err)
	}
	return t, nil
}

// initTraces installs the tracer c selects, and the W3C propagator, as the
// process-wide defaults.
func (t *Telemetry) initTraces(ctx context.Context, c OTelConfig) error {
	if !c.Enabled {
		return nil
	}
	tp, err := newTracerProvider(ctx, c)
	if err != nil {
		return err
	}
	t.tracers = tp
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagator)
	return nil
}

// initMetrics installs the meter c selects as the process-wide default; a
// scrape also gets the runtime collectors and, given an AdminAddr, its listener.
func (t *Telemetry) initMetrics(ctx context.Context, c MetricsConfig) error {
	if !c.Enabled {
		return nil
	}
	reg := prom.NewRegistry()
	reader, scrape, err := metricReader(ctx, c, reg)
	if err != nil {
		return err
	}
	t.meters = sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(resourceFor(c.ServiceName)),
		sdkmetric.WithReader(reader),
	)
	otel.SetMeterProvider(t.meters)
	if !scrape {
		return nil
	}
	t.registry = reg
	if err := registerRuntimeCollectors(reg); err != nil {
		return err
	}
	if c.AdminAddr == "" {
		return nil
	}
	path := orDefault(c.Path, DefaultMetricsPath)
	srv, errCh := startAdmin(c.AdminAddr, path, scrapeHandler(reg))
	t.adminErr = errCh
	if srv != nil {
		t.admin = srv
		t.adminURL = srv.Addr + path
	}
	return nil
}

// TracerProvider returns the stack's tracer provider, or the otel no-op when
// traces are off.
func (t *Telemetry) TracerProvider() oteltrace.TracerProvider {
	if t == nil || t.tracers == nil {
		return tracenoop.NewTracerProvider()
	}
	return t.tracers
}

// MeterProvider returns the stack's meter provider, or the otel no-op when
// metrics are off.
func (t *Telemetry) MeterProvider() otelmetric.MeterProvider {
	if t == nil || t.meters == nil {
		return metricnoop.NewMeterProvider()
	}
	return t.meters
}

// Registerer returns the registry the scrape gathers, for your own collectors,
// or nil when the stack has no scrape.
func (t *Telemetry) Registerer() prom.Registerer {
	if t == nil || t.registry == nil {
		return nil
	}
	return t.registry
}

// ScrapeHandler serves the stack's scrape from a server of your own, for an
// empty [MetricsConfig.AdminAddr]. Without a scrape it serves an empty
// exposition.
func (t *Telemetry) ScrapeHandler() http.Handler {
	if t == nil || t.registry == nil {
		return scrapeHandler(prom.NewRegistry())
	}
	return scrapeHandler(t.registry)
}

// ScrapeURL returns the scrape listener's `host:port/path`, with the port
// resolved, or "" when no listener runs.
func (t *Telemetry) ScrapeURL() string {
	if t == nil {
		return ""
	}
	return t.adminURL
}

// AdminErr returns a channel that carries a scrape listener bind or serve
// failure and closes when the listener stops. It is nil when no listener was
// configured.
func (t *Telemetry) AdminErr() <-chan error {
	if t == nil {
		return nil
	}
	return t.adminErr
}

// Shutdown stops the scrape listener, then flushes and closes the providers.
// It runs every step and returns their errors joined.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	var errs []error
	if t.admin != nil {
		if err := t.admin.Shutdown(ctx); err != nil {
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

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
