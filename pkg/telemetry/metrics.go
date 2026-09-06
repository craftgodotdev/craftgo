package telemetry

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/craftgodotdev/craftgo/internal/otlpaddr"
)

// resourceFor is the resource every signal reports under: the SDK
// defaults (telemetry.sdk.*, `unknown_service:<binary>` as the service
// name) with `service.name` replaced by serviceName when given. Merge
// fails on a schema-URL mismatch, and then the service-only resource
// wins: losing telemetry.sdk.* beats losing the service name.
func resourceFor(serviceName string) *sdkresource.Resource {
	if serviceName == "" {
		return sdkresource.Default()
	}
	svc := sdkresource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(serviceName))
	merged, err := sdkresource.Merge(sdkresource.Default(), svc)
	if err != nil {
		return svc
	}
	return merged
}

// metricReader returns the reader c.Exporter selects and whether it is
// the Prometheus scrape into reg: a periodic OTLP push for the otlp_*
// kinds, a manual reader nothing collects from for "none" (instruments
// resolve, nothing leaves), and the scrape for "prometheus" and any
// other value, so a typo never silently turns metrics off.
func metricReader(ctx context.Context, c MetricsConfig, reg prom.Registerer) (sdkmetric.Reader, bool, error) {
	switch c.Exporter {
	case ExporterOTLPgRPC:
		exp, err := otlpmetricgrpc.New(ctx, otlpaddr.Base(c.Endpoint,
			otlpmetricgrpc.WithEndpointURL, otlpmetricgrpc.WithEndpoint, otlpmetricgrpc.WithInsecure)...)
		if err != nil {
			return nil, false, fmt.Errorf("otlp grpc metric exporter: %w", err)
		}
		return sdkmetric.NewPeriodicReader(exp), false, nil
	case ExporterOTLPHTTP:
		exp, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(c.Endpoint))
		if err != nil {
			return nil, false, fmt.Errorf("otlp http metric exporter: %w", err)
		}
		return sdkmetric.NewPeriodicReader(exp), false, nil
	case ExporterNone:
		return sdkmetric.NewManualReader(), false, nil
	}
	exp, err := otelprom.New(otelprom.WithRegisterer(reg))
	if err != nil {
		return nil, false, fmt.Errorf("prometheus exporter: %w", err)
	}
	return exp, true, nil
}

// registerRuntimeCollectors adds the Go runtime and process collectors to
// reg, so the scrape carries `go_*` and `process_*` series alongside the
// HTTP instruments. A collector already registered is left alone.
func registerRuntimeCollectors(reg prom.Registerer) error {
	for _, c := range []prom.Collector{
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	} {
		var dup prom.AlreadyRegisteredError
		if err := reg.Register(c); err != nil && !errors.As(err, &dup) {
			return err
		}
	}
	return nil
}
