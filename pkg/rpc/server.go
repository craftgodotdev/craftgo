// Package rpc serves and calls gRPC: [Server] wraps *grpc.Server with panic
// recovery, a health service and an [Interceptor] chain, [Error] maps service
// errors onto gRPC status codes, and [Dial] opens client connections.
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

// Server collects interceptors, options and service registrations, and builds
// the *grpc.Server on the first call to [Server.GRPCServer], [Server.Serve] or
// [Server.Start].
type Server struct {
	mu         sync.Mutex
	chain      []Interceptor
	stats      stats.Handler
	extra      []grpc.ServerOption
	reflection bool
	noHealth   bool
	services   []registration
	inner      *grpc.Server
	health     *health.Server
}

// registration is a RegisterService call recorded until the server is built.
type registration struct {
	desc *grpc.ServiceDesc
	impl any
}

// Option configures a Server at construction time.
type Option func(*Server)

// WithStatsHandler installs h, such as the telemetry stack's GRPCServerHandler.
// It runs outside every interceptor, so its span is on the context they see.
// A nil h is ignored.
func WithStatsHandler(h stats.Handler) Option {
	return func(s *Server) {
		if h != nil {
			s.stats = h
		}
	}
}

// WithReflection registers the gRPC reflection service when on.
func WithReflection(on bool) Option { return func(s *Server) { s.reflection = on } }

// WithoutDefaultHealth disables the auto-registered grpc.health.v1 service.
func WithoutDefaultHealth() Option { return func(s *Server) { s.noHealth = true } }

// WithServerOptions passes opts through to grpc.NewServer.
func WithServerOptions(opts ...grpc.ServerOption) Option {
	return func(s *Server) { s.extra = append(s.extra, opts...) }
}

// New returns a Server with a grpc.health.v1 service reporting SERVING, no
// reflection and no stats handler. The first argument is unused.
func New(_ any, opts ...Option) *Server {
	s := &Server{}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Use appends i to the chain, outermost first, inside [Recovery]. Health and
// reflection calls bypass the chain, and a Use after the server is built has
// no effect.
func (s *Server) Use(i Interceptor) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chain = append(s.chain, i)
	return s
}

// SetLogger installs l as [log.Default], the logger the server's [Recovery]
// writes to; nil is ignored.
func (s *Server) SetLogger(l log.Logger) *Server {
	log.SetDefault(l)
	return s
}

// Logger returns [log.Default] as it is at the call; [log.Follow] returns a logger that
// follows a later [Server.SetLogger].
func (s *Server) Logger() log.Logger { return log.Default() }

// RegisterService implements [grpc.ServiceRegistrar] and reports the service
// SERVING on the health service. A registration once serving has begun makes
// grpc exit the process.
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

// GRPCServer builds the *grpc.Server on the first call, with [Recovery], logging
// to [log.Default], ahead of the Use chain, and returns the same one afterwards.
func (s *Server) GRPCServer() *grpc.Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inner != nil {
		return s.inner
	}
	guard := recovery(log.Default)
	unary := []grpc.UnaryServerInterceptor{guard.Unary}
	stream := []grpc.StreamServerInterceptor{guard.Stream}
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

// Start listens on addr and serves until [Server.Stop]; it returns nil once
// stopped, or the listen or serve error.
func (s *Server) Start(addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(lis)
}

// Serve serves on lis until [Server.Stop]; it returns nil once stopped, or the
// serve error.
func (s *Server) Serve(lis net.Listener) error {
	if err := s.GRPCServer().Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}

// Stop sets the health service NOT_SERVING and stops gracefully; if ctx ends
// first it cuts off the calls still running and returns ctx's error. Stop on a
// server never built does nothing.
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
