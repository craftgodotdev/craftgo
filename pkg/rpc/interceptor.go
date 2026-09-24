package rpc

import (
	"context"
	"strings"

	"google.golang.org/grpc"
)

// Interceptor pairs the unary and stream forms of one guard; either may be nil.
type Interceptor struct {
	Unary  grpc.UnaryServerInterceptor
	Stream grpc.StreamServerInterceptor
}

// Unary wraps a unary-only interceptor.
func Unary(f grpc.UnaryServerInterceptor) Interceptor { return Interceptor{Unary: f} }

// Stream wraps a stream-only interceptor.
func Stream(f grpc.StreamServerInterceptor) Interceptor { return Interceptor{Stream: f} }

// IsInfrastructureMethod reports whether fullMethod belongs to the gRPC health
// or reflection service.
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
