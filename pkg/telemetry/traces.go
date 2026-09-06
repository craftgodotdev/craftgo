package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// newTracerProvider builds the tracer c selects: the resource
// [resourceFor] stamps plus, when c names an exporter, one batch
// processor feeding it. Without an exporter spans get valid ids (for log
// correlation) but go nowhere.
func newTracerProvider(ctx context.Context, c OTelConfig) (*sdktrace.TracerProvider, error) {
	opts := []sdktrace.TracerProviderOption{sdktrace.WithResource(resourceFor(c.ServiceName))}
	exp, err := traceExporter(ctx, c)
	if err != nil {
		return nil, err
	}
	if exp != nil {
		opts = append(opts, sdktrace.WithBatcher(exp))
	}
	return sdktrace.NewTracerProvider(opts...), nil
}

// traceExporter returns the span exporter c.Exporter names, or nil for
// "none" and any other value.
func traceExporter(ctx context.Context, c OTelConfig) (sdktrace.SpanExporter, error) {
	switch c.Exporter {
	case ExporterStdout:
		exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, fmt.Errorf("stdout trace exporter: %w", err)
		}
		return exp, nil
	case ExporterOTLPgRPC:
		exp, err := otlptracegrpc.New(ctx, otlpEndpoint(c.Endpoint,
			otlptracegrpc.WithEndpointURL, otlptracegrpc.WithEndpoint, otlptracegrpc.WithInsecure)...)
		if err != nil {
			return nil, fmt.Errorf("otlp grpc trace exporter: %w", err)
		}
		return exp, nil
	case ExporterOTLPHTTP:
		exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(c.Endpoint))
		if err != nil {
			return nil, fmt.Errorf("otlp http trace exporter: %w", err)
		}
		return exp, nil
	}
	return nil, nil
}
