// Package telemetry wires a service's traces and metrics as one stack.
//
// The two signals must agree on three facts: the `service.name` both
// report under, whether the HTTP layer is instrumented at all (one
// otelhttp wrapper emits both), and shutdown. [Init] returns a value that
// owns all three, plus the Prometheus registry and scrape listener, so
// two stacks can coexist in one process. The stack initialised last also
// becomes the process-wide default: `otel.Tracer` / `otel.Meter` in
// application code report through it.
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
	if err := t.initTraces(ctx, c.OTel); err != nil {
		return nil, fmt.Errorf("telemetry: traces: %w", err)
	}
	if err := t.initMetrics(ctx, c.Metrics); err != nil {
		// Traces are already live - a failed Init must leave nothing running.
		_ = t.Shutdown(ctx)
		return nil, fmt.Errorf("telemetry: metrics: %w", err)
	}
	return t, nil
}

// initTraces installs the tracer c selects and makes it, together with
// the W3C propagator, the process-wide default.
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

// initMetrics installs the meter c selects on a registry this stack owns
// and makes it the process-wide default. For the scrape it also registers
// the runtime collectors and starts the listener.
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

// ScrapeHandler serves this stack's scrape in Prometheus exposition, for
// deployments that route `/metrics` on the public server instead of a
// dedicated listener (`metrics.adminAddr` empty). Without a scrape it
// serves an empty, valid exposition, so probes still see 200.
func (t *Telemetry) ScrapeHandler() http.Handler {
	if t == nil || t.registry == nil {
		return scrapeHandler(prom.NewRegistry())
	}
	return scrapeHandler(t.registry)
}

// ScrapeURL is the `host:port/path` the listener bound to, or "" when
// none started or the bind failed (see [Telemetry.AdminErr]). The port is
// the resolved one, so a `:0` bind is loggable.
func (t *Telemetry) ScrapeURL() string {
	if t == nil {
		return ""
	}
	return t.adminURL
}

// AdminErr surfaces a bind or post-startup failure of the scrape
// listener, or nil when none runs. Callers must check for nil: receiving
// from a nil channel blocks forever.
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

// orDefault returns v, or fallback when v is empty.
func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
