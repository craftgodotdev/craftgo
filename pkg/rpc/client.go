package rpc

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

type clientConfig struct {
	stats   stats.Handler
	creds   credentials.TransportCredentials
	timeout time.Duration
	logger  log.Logger
	extra   []grpc.DialOption
}

// ClientOption configures [Dial].
type ClientOption func(*clientConfig)

// WithClientStatsHandler installs h, such as the telemetry stack's
// GRPCClientHandler. Without a stats handler a gRPC client sends no
// traceparent, so the server starts a new trace. A nil h is ignored.
func WithClientStatsHandler(h stats.Handler) ClientOption {
	return func(c *clientConfig) {
		if h != nil {
			c.stats = h
		}
	}
}

// WithClientTransportCredentials sets the transport security, which defaults
// to [insecure.NewCredentials]; nil creds are ignored.
func WithClientTransportCredentials(creds credentials.TransportCredentials) ClientOption {
	return func(c *clientConfig) {
		if creds != nil {
			c.creds = creds
		}
	}
}

// WithClientTimeout gives every unary call whose context has no deadline a
// deadline of d. Streams are not bounded; d <= 0 installs nothing.
func WithClientTimeout(d time.Duration) ClientOption {
	return func(c *clientConfig) { c.timeout = d }
}

// WithClientAccessLog logs a `grpc client` line to l for every unary call, with
// `method`, `code`, `latency` and the context's trace ids.
func WithClientAccessLog(l log.Logger) ClientOption {
	return func(c *clientConfig) { c.logger = l }
}

// WithDialOptions passes opts through to grpc.NewClient.
func WithDialOptions(opts ...grpc.DialOption) ClientOption {
	return func(c *clientConfig) { c.extra = append(c.extra, opts...) }
}

// Dial returns a client connection to target, which connects lazily: it fails
// on a bad target or option, never on an unreachable server. The caller closes
// the connection.
func Dial(target string, opts ...ClientOption) (*grpc.ClientConn, error) {
	cfg := &clientConfig{creds: insecure.NewCredentials()}
	for _, o := range opts {
		o(cfg)
	}
	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(cfg.creds)}
	if cfg.stats != nil {
		dialOpts = append(dialOpts, grpc.WithStatsHandler(cfg.stats))
	}
	if unary := cfg.unaryChain(); len(unary) > 0 {
		dialOpts = append(dialOpts, grpc.WithChainUnaryInterceptor(unary...))
	}
	dialOpts = append(dialOpts, cfg.extra...)
	return grpc.NewClient(target, dialOpts...)
}

// unaryChain puts the deadline outside the access log, so the logged latency is
// the bounded call.
func (c *clientConfig) unaryChain() []grpc.UnaryClientInterceptor {
	var chain []grpc.UnaryClientInterceptor
	if c.timeout > 0 {
		chain = append(chain, clientTimeout(c.timeout))
	}
	if c.logger != nil {
		chain = append(chain, clientAccessLog(c.logger))
	}
	return chain
}

// clientTimeout gives a call with no deadline of its own the default one.
func clientTimeout(d time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func clientAccessLog(logger log.Logger) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		start := time.Now()
		err := invoker(ctx, method, req, reply, cc, opts...)
		logger.WithContext(ctx).Info("grpc client",
			log.String("method", method),
			log.String("code", status.Code(err).String()),
			log.Duration("latency", time.Since(start)),
		)
		return err
	}
}
