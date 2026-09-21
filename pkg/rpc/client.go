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

// The caller's half of the runtime. A gRPC client sends nothing of the
// trace on its own: without a stats handler there is no `traceparent` in
// the request metadata, so the service it calls opens a trace of its own
// and the two halves of one request never meet. [Dial] is where that is
// decided once, beside the deadline and the access log the server side
// has.

// clientConfig is what [Dial] builds a connection from.
type clientConfig struct {
	stats   stats.Handler
	creds   credentials.TransportCredentials
	timeout time.Duration
	logger  log.Logger
	extra   []grpc.DialOption
}

// ClientOption configures [Dial].
type ClientOption func(*clientConfig)

// WithClientStatsHandler installs a stats handler - the telemetry
// stack's `GRPCClientHandler()` - which opens the client span, records
// the call duration and writes the trace context onto the wire. A nil
// handler is ignored: grpc would log an error for it.
func WithClientStatsHandler(h stats.Handler) ClientOption {
	return func(c *clientConfig) {
		if h != nil {
			c.stats = h
		}
	}
}

// WithClientTransportCredentials sets the transport security. The
// default is [insecure.NewCredentials], which is what a call inside a
// cluster or a mesh uses; a connection crossing a trust boundary passes
// its own.
func WithClientTransportCredentials(creds credentials.TransportCredentials) ClientOption {
	return func(c *clientConfig) {
		if creds != nil {
			c.creds = creds
		}
	}
}

// WithClientTimeout bounds every unary call whose context carries no
// deadline of its own; a caller that sets one keeps it, shorter or
// longer. Streams are not bounded - a long-lived stream is the point of
// one. d <= 0 installs nothing.
func WithClientTimeout(d time.Duration) ClientOption {
	return func(c *clientConfig) { c.timeout = d }
}

// WithClientAccessLog logs one `grpc client` line per unary call with
// `method`, `code` and `latency`, plus the trace ids the context carries
// - the caller-side twin of [AccessLog].
func WithClientAccessLog(l log.Logger) ClientOption {
	return func(c *clientConfig) { c.logger = l }
}

// WithDialOptions passes options straight to grpc.NewClient - a
// resolver, a load-balancing policy, keepalive, message size limits,
// interceptors of your own.
func WithDialOptions(opts ...grpc.DialOption) ClientOption {
	return func(c *clientConfig) { c.extra = append(c.extra, opts...) }
}

// Dial returns a connection to target with the guards a craftgo service
// makes calls under: the stats handler that carries the trace to the
// other side, a default deadline, and an access log. Nothing is dialed
// yet - grpc.NewClient connects lazily - so the error is a bad target or
// a bad option, not an unreachable server. The caller closes it.
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

// unaryChain is the interceptor chain in the order the server installs
// its own: the deadline first, so the access log measures the call the
// caller actually waited for.
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

// clientAccessLog logs one line per unary call once it has answered.
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
