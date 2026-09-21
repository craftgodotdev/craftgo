package rpc

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// Timeout bounds every unary call to d: the handler's context carries the
// deadline, and a shorter client deadline still wins. A handler that
// returns after the deadline has its response dropped for DeadlineExceeded,
// so a late success is never delivered. Streams are not bounded - a
// long-lived stream is the point of one. d <= 0 installs nothing, so
// main.go can pass the configured default unconditionally.
func Timeout(d time.Duration) Interceptor {
	if d <= 0 {
		return Interceptor{}
	}
	return Unary(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		resp, err := handler(ctx, req)
		if err == nil && ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		return resp, err
	})
}
