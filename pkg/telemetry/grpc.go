package telemetry

import (
	"context"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc/stats"

	"github.com/craftgodotdev/craftgo/pkg/rpc"
)

// GRPCServerHandler returns the stats handler for [rpc.WithStatsHandler]: it
// records a span, continuing the caller's trace, and `rpc.server.call.duration`
// for every call except health and reflection.
func (t *Telemetry) GRPCServerHandler() stats.Handler {
	if !t.instrumented() {
		return noopStats{}
	}
	return otelgrpc.NewServerHandler(
		otelgrpc.WithTracerProvider(t.TracerProvider()),
		otelgrpc.WithMeterProvider(t.MeterProvider()),
		otelgrpc.WithPropagators(t.propagator()),
		otelgrpc.WithFilter(func(info *stats.RPCTagInfo) bool { return !rpc.IsInfrastructureMethod(info.FullMethodName) }),
	)
}

// GRPCClientHandler returns the stats handler for [rpc.WithClientStatsHandler]:
// it records a span and `rpc.client.call.duration` for every call except health
// and reflection, and writes the trace context into the request metadata.
func (t *Telemetry) GRPCClientHandler() stats.Handler {
	if !t.instrumented() {
		return noopStats{}
	}
	return otelgrpc.NewClientHandler(
		otelgrpc.WithTracerProvider(t.TracerProvider()),
		otelgrpc.WithMeterProvider(t.MeterProvider()),
		otelgrpc.WithPropagators(t.propagator()),
		otelgrpc.WithFilter(func(info *stats.RPCTagInfo) bool { return !rpc.IsInfrastructureMethod(info.FullMethodName) }),
	)
}

// noopStats is the handler of an unconfigured stack.
type noopStats struct{}

func (noopStats) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context   { return ctx }
func (noopStats) HandleRPC(context.Context, stats.RPCStats)                         {}
func (noopStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (noopStats) HandleConn(context.Context, stats.ConnStats)                       {}
