package rpc

import (
	"context"
	"strings"

	"google.golang.org/grpc"
)

// Interceptor is one guard in both shapes gRPC needs: the unary and the
// stream interceptor. Either may be nil for a guard that only applies to
// one shape - [Timeout] bounds unary calls and leaves streams alone.
type Interceptor struct {
	Unary  grpc.UnaryServerInterceptor
	Stream grpc.StreamServerInterceptor
}

// Unary wraps a unary-only interceptor.
func Unary(f grpc.UnaryServerInterceptor) Interceptor { return Interceptor{Unary: f} }

// Stream wraps a stream-only interceptor.
func Stream(f grpc.StreamServerInterceptor) Interceptor { return Interceptor{Stream: f} }

// IsInfrastructureMethod reports whether fullMethod belongs to the health
// or reflection services the server registers itself. Those calls skip
// the Use chain and the telemetry filter keeps them out of the traces, as
// the HTTP probes never reach the middleware chain.
func IsInfrastructureMethod(fullMethod string) bool {
	return strings.HasPrefix(fullMethod, "/grpc.health.v1.") || strings.HasPrefix(fullMethod, "/grpc.reflection.")
}

func bypassUnary(next grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if IsInfrastructureMethod(info.FullMethod) {
			return handler(ctx, req)
		}
		return next(ctx, req, info, handler)
	}
}

func bypassStream(next grpc.StreamServerInterceptor) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if IsInfrastructureMethod(info.FullMethod) {
			return handler(srv, ss)
		}
		return next(srv, ss, info, handler)
	}
}
