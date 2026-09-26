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
)

// resourceFor is the SDK default resource with `service.name` set to
// serviceName when given, or that name alone if the merge fails.
func resourceFor(serviceName string) *sdkresource.Resource {
	if serviceName == "" {
		return sdkresource.Default()
	}
	svc := sdkresource.NewSchemaless(semconv.ServiceName(serviceName))
	merged, err := sdkresource.Merge(sdkresource.Default(), svc)
	if err != nil {
		return svc
	}
	return merged
}

// metricReader returns the reader c.Exporter selects, falling back to the
// Prometheus scrape into reg, and whether it is that scrape.
func metricReader(ctx context.Context, c MetricsConfig, reg prom.Registerer) (sdkmetric.Reader, bool, error) {
	switch c.Exporter {
	case ExporterOTLPgRPC:
		endpoint, err := otlpGRPCEndpoint(c.Endpoint,
			otlpmetricgrpc.WithEndpointURL, otlpmetricgrpc.WithEndpoint, otlpmetricgrpc.WithInsecure)
		if err != nil {
			return nil, false, err
		}
		exp, err := otlpmetricgrpc.New(ctx, endpoint...)
		if err != nil {
			return nil, false, fmt.Errorf("otlp grpc metric exporter: %w", err)
		}
		return sdkmetric.NewPeriodicReader(exp), false, nil
	case ExporterOTLPHTTP:
		endpoint, err := otlpHTTPEndpoint(c.Endpoint, otlpmetrichttp.WithEndpointURL)
		if err != nil {
			return nil, false, err
		}
		exp, err := otlpmetrichttp.New(ctx, endpoint...)
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

// registerRuntimeCollectors adds the Go runtime and process collectors to reg,
// tolerating ones already registered.
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
