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

// AccessLogFields appends the fields fn derives from the call to every
// `grpc access` line - the peer address, a metadata value. fn runs after
// the handler.
func AccessLogFields(fn func(ctx context.Context, fullMethod string) []log.Field) AccessLogOption {
	return func(c *accessLogConfig) { c.fields = fn }
}

// AccessLogSkipMethods keeps the named full methods (`/pkg.Service/Method`)
// out of the log. The health and reflection services need no entry: they
// never reach the chain.
func AccessLogSkipMethods(methods ...string) AccessLogOption {
	return func(c *accessLogConfig) {
		for _, m := range methods {
			c.skip[m] = true
		}
	}
}

// AccessLog logs one line per call once it has finished - for a stream,
// when the stream ends: message `grpc access` with `method`, `code` and
// `latency`, plus the `trace_id` / `span_id` the context carries (see
// [log.Logger.WithContext]). Install the telemetry stats handler so those
// ids are on the context.
func AccessLog(logger log.Logger, opts ...AccessLogOption) Interceptor {
	cfg := &accessLogConfig{skip: map[string]bool{}}
	for _, o := range opts {
		o(cfg)
	}
	logLine := func(ctx context.Context, fullMethod string, start time.Time, err error) {
		fields := []log.Field{
			log.String("method", fullMethod),
			log.String("code", status.Code(err).String()),
			log.Duration("latency", time.Since(start)),
		}
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
