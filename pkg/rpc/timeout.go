package rpc

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// Timeout bounds every unary call's context to d (a shorter client deadline
// still wins) and answers DeadlineExceeded for a success that returns late.
// Streams are not bounded; d <= 0 installs nothing.
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
