package rpc

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// AccessLogOption configures [AccessLog].
type AccessLogOption func(*accessLogConfig)

type accessLogConfig struct {
	skip   map[string]bool
	fields func(ctx context.Context, fullMethod string) []log.Field
}

// AccessLogFields appends fn's fields to every access line; fn runs after the
// handler.
func AccessLogFields(fn func(ctx context.Context, fullMethod string) []log.Field) AccessLogOption {
	return func(c *accessLogConfig) { c.fields = fn }
}

// AccessLogSkipMethods keeps the named full methods (`/pkg.Service/Method`)
// out of the log.
func AccessLogSkipMethods(methods ...string) AccessLogOption {
	return func(c *accessLogConfig) {
		for _, m := range methods {
			c.skip[m] = true
		}
	}
}

// AccessLog logs a `grpc access` line to logger when each call or stream ends,
// with `method`, `code`, `latency` and the trace ids that a stats handler
// installed with [WithStatsHandler] puts on the context.
func AccessLog(logger log.Logger, opts ...AccessLogOption) Interceptor {
	cfg := &accessLogConfig{skip: map[string]bool{}}
	for _, o := range opts {
		o(cfg)
	}
	logLine := func(ctx context.Context, fullMethod string, start time.Time, err error) {
		fields := callFields(fullMethod, err, start)
		if cfg.fields != nil {
			fields = append(fields, cfg.fields(ctx, fullMethod)...)
		}
		logger.WithContext(ctx).Info("grpc access", fields...)
	}
	return Interceptor{
		Unary: func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			if cfg.skip[info.FullMethod] {
				return handler(ctx, req)
			}
			start := time.Now()
			resp, err := handler(ctx, req)
			logLine(ctx, info.FullMethod, start, err)
			return resp, err
		},
		Stream: func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			if cfg.skip[info.FullMethod] {
				return handler(srv, ss)
			}
			start := time.Now()
			err := handler(srv, ss)
			logLine(ss.Context(), info.FullMethod, start, err)
			return err
		},
	}
}

// callFields returns the method, code and latency fields a call's log line starts with.
func callFields(method string, err error, start time.Time) []log.Field {
	return []log.Field{
		log.String("method", method),
		log.String("code", status.Code(err).String()),
		log.Duration("latency", time.Since(start)),
	}
}
