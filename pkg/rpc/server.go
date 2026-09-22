// Package rpc is the gRPC runtime the generated server layer targets: a
// thin wrapper over *grpc.Server that installs the same guards the HTTP
// [server] installs - recovery outermost, an access log, a default
// deadline - and maps the errors service logic returns onto gRPC status
// codes the way [server.WriteError] maps them onto HTTP statuses. One
// ServiceContext, one logger and one telemetry stack serve both listeners.
package rpc

import (
	"context"
	"errors"
	"net"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/stats"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// Server collects interceptors, options and service registrations, then
// builds the *grpc.Server once - on the first of [Server.GRPCServer],
// [Server.Serve] or [Server.Start] - the way the HTTP server builds its
// handler chain once in Handler().
type Server struct {
	mu         sync.Mutex
	logger     log.Logger
	chain      []Interceptor
	stats      stats.Handler
	extra      []grpc.ServerOption
	reflection bool
	noHealth   bool
	services   []registration
	inner      *grpc.Server
	health     *health.Server
}

// registration is one RegisterService call recorded before the server is
// built and replayed onto it.
type registration struct {
	desc *grpc.ServiceDesc
	impl any
}

// Option configures a Server at construction time.
type Option func(*Server)

// WithStatsHandler installs a stats handler - the telemetry stack's gRPC
// handler, which opens the span and records the call duration. It runs in
// the transport, outside every interceptor, so the span is on the context
// before the access log reads it. A nil handler is ignored: grpc would
// log an error for it.
func WithStatsHandler(h stats.Handler) Option {
	return func(s *Server) {
		if h != nil {
			s.stats = h
		}
	}
}

// WithReflection serves the gRPC reflection service so grpcurl and grpcui
// can call the server without the .proto files.
func WithReflection(on bool) Option { return func(s *Server) { s.reflection = on } }

// WithoutDefaultHealth disables the auto-registered grpc.health.v1 service.
func WithoutDefaultHealth() Option { return func(s *Server) { s.noHealth = true } }

// WithServerOptions passes options straight to grpc.NewServer - message
// size limits, keepalive policy, credentials.
func WithServerOptions(opts ...grpc.ServerOption) Option {
	return func(s *Server) { s.extra = append(s.extra, opts...) }
}

// New returns a Server with the framework defaults: the process logger, a
// grpc.health.v1 service reporting SERVING, no reflection, no stats
// handler. `_` is the project's ServiceContext, accepted to mirror the
// HTTP constructor; the runtime does not introspect it.
func New(_ any, opts ...Option) *Server {
	s := &Server{logger: log.New()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Use appends an interceptor to the chain, outermost first. Recovery is
// always ahead of the chain, and infrastructure methods - health,
// reflection - bypass it, as the HTTP probes bypass the middleware chain.
// The chain is read once, when the server is built, so a Use after
// [Server.GRPCServer] or [Server.Serve] changes nothing - as a Use after
// the HTTP Start does.
func (s *Server) Use(i Interceptor) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chain = append(s.chain, i)
	return s
}

// SetLogger replaces the logger the recovery interceptor and the generated
// logic reach through [log.Default].
func (s *Server) SetLogger(l log.Logger) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = l
	log.SetDefault(l)
	return s
}

// Logger exposes the active logger for interceptors.
func (s *Server) Logger() log.Logger {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logger
}

// RegisterService is [grpc.ServiceRegistrar], so the generated
// `pb.RegisterXServer(srv, impl)` takes the Server directly. Before the
// server is built the registration is recorded; afterwards it goes
// straight to grpc, and the health service learns the name - grpc itself
// exits the process for a registration once it is serving.
func (s *Server) RegisterService(desc *grpc.ServiceDesc, impl any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inner != nil {
		s.inner.RegisterService(desc, impl)
		if s.health != nil {
			s.health.SetServingStatus(desc.ServiceName, healthpb.HealthCheckResponse_SERVING)
		}
		return
	}
	s.services = append(s.services, registration{desc: desc, impl: impl})
}

// GRPCServer builds the *grpc.Server on first call and returns the same
// one afterwards. The chain is Recovery, then the Use interceptors with
// the infrastructure bypass, then the handler; the stats handler sits in
// the transport around all of it. Tests serve it on a bufconn listener
// through [Server.Serve].
func (s *Server) GRPCServer() *grpc.Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inner != nil {
		return s.inner
	}
	unary := []grpc.UnaryServerInterceptor{Recovery(s.logger).Unary}
	stream := []grpc.StreamServerInterceptor{Recovery(s.logger).Stream}
	for _, i := range s.chain {
		if i.Unary != nil {
			unary = append(unary, bypassUnary(i.Unary))
		}
		if i.Stream != nil {
			stream = append(stream, bypassStream(i.Stream))
		}
	}
	opts := []grpc.ServerOption{grpc.ChainUnaryInterceptor(unary...), grpc.ChainStreamInterceptor(stream...)}
	if s.stats != nil {
		opts = append(opts, grpc.StatsHandler(s.stats))
	}
	opts = append(opts, s.extra...)
	srv := grpc.NewServer(opts...)
	for _, r := range s.services {
		srv.RegisterService(r.desc, r.impl)
	}
	if !s.noHealth {
		s.health = health.NewServer()
		healthpb.RegisterHealthServer(srv, s.health)
		for _, r := range s.services {
			s.health.SetServingStatus(r.desc.ServiceName, healthpb.HealthCheckResponse_SERVING)
		}
	}
	if s.reflection {
		reflection.Register(srv)
	}
	s.inner = srv
	return srv
}

// Start listens on addr and serves until Stop; it returns nil once the
// server has been stopped, the way the HTTP Start swallows ErrServerClosed.
func (s *Server) Start(addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(lis)
}

// Serve serves on lis until Stop. It is the entry point tests use with an
// in-memory listener.
func (s *Server) Serve(lis net.Listener) error {
	if err := s.GRPCServer().Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}

// Stop drains the server: the health service flips to NOT_SERVING (a
// health Watch in progress sees it; a later Check meets the closed
// listener), the listener closes, in-flight RPCs finish, and when ctx
// expires first the rest are cut off and ctx's error is returned. A
// server never built is a no-op.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	srv, h := s.inner, s.health
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	if h != nil {
		h.Shutdown()
	}
	done := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		srv.Stop()
		<-done
		return ctx.Err()
	}
}
