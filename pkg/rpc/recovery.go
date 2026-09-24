package rpc

import (
	"context"
	"runtime/debug"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// Recovery answers a panic with codes.Internal and an opaque message, logging
// the value and stack, with the call's trace ids, to logger. [Server] installs
// it outermost.
func Recovery(logger log.Logger) Interceptor {
	recovered := func(ctx context.Context, rec any) error {
		logger.WithContext(ctx).Error("panic recovered",
			log.Any("panic", rec),
			log.String("stack", string(debug.Stack())),
		)
		return status.Error(codes.Internal, "internal server error")
	}
	return Interceptor{
		Unary: func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
			defer func() {
				if rec := recover(); rec != nil {
					resp, err = nil, recovered(ctx, rec)
				}
			}()
			return handler(ctx, req)
		},
		Stream: func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
			defer func() {
				if rec := recover(); rec != nil {
					err = recovered(ss.Context(), rec)
				}
			}()
			return handler(srv, ss)
		},
	}
}
