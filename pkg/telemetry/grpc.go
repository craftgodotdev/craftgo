package telemetry

import (
	"context"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc/stats"

	"github.com/craftgodotdev/craftgo/pkg/rpc"
)

// GRPCServerHandler instruments every call against this stack's
// providers, never the global slots: one otelgrpc stats handler emits the
// span and the `rpc.server.call.duration` instrument, the gRPC twin of
// [Telemetry.HTTPMiddleware]. It runs in the transport, so the span is on
// the context before any interceptor - the access log included - reads
// it, and a traced stack adopts the caller's W3C trace context from the
// request metadata. Health and reflection calls are left out, as the
// HTTP probes bypass the middleware chain. With both signals off the
// handler does nothing; it is never nil, which grpc would refuse.
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

// noopStats is the handler of an unconfigured stack.
type noopStats struct{}

func (noopStats) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context   { return ctx }
func (noopStats) HandleRPC(context.Context, stats.RPCStats)                         {}
func (noopStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (noopStats) HandleConn(context.Context, stats.ConnStats)                       {}
