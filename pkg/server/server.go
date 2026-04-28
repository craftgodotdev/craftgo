// Package server is the thin `net/http` wrapper that craftgo's generated
// routes register against. It owns a `*http.ServeMux`, a middleware stack,
// configurable JSON codec / logger, default per-method limits, and the
// `Start`/`Stop` lifecycle.
package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/dropship-dev/craftgo/pkg/log"
)

// Server is craftgo's runtime. Methods follow a fluent style so a project
// `main.go` can chain configuration calls before `Start()`.
type Server struct {
	mu sync.Mutex

	mux     *http.ServeMux
	chain   []Middleware
	addr    string
	httpSrv *http.Server

	logger Logger
	codec  JSONCodec
	cors   *CORSOptions

	defaultReadTimeout  time.Duration
	defaultWriteTimeout time.Duration
	defaultMaxBodySize  int64
	defaultMaxHeaderKB  int

	healthChecks  map[string]healthCheck
	healthPaths   HealthPaths
	noHealth      bool
	healthMounted bool

	registeredMW map[string]Middleware
}

// Logger aliases the public Logger interface so handler-internal code does
// not need a second import.
type Logger = log.Logger

// Middleware wraps an http.Handler. Order: outermost first, so the slice is
// applied in reverse during Start.
type Middleware func(http.Handler) http.Handler

// Option configures a Server at construction time.
type Option func(*Server)

// HealthPaths is the override pair for `/healthz` and `/readyz`.
type HealthPaths struct {
	Liveness  string
	Readiness string
}

// healthCheck pairs a probe function with its timeout.
type healthCheck struct {
	timeout time.Duration
	fn      func(context.Context) error
}

// WithHealthPaths overrides the default `/healthz` and `/readyz` routes.
func WithHealthPaths(p HealthPaths) Option {
	return func(s *Server) { s.healthPaths = p }
}

// WithoutDefaultHealth disables the auto-registered health endpoints.
func WithoutDefaultHealth() Option { return func(s *Server) { s.noHealth = true } }

// New returns a Server with sensible defaults: JSON codec, slog logger,
// `/healthz` + `/readyz` health probes, no rate-limit, no CORS. Pass any
// number of [Option] values to override.
//
// `_` is the project's ServiceContext; it's accepted only to mirror the
// documented constructor signature — the runtime doesn't introspect it.
func New(_ any, opts ...Option) *Server {
	s := &Server{
		mux:                http.NewServeMux(),
		logger:             log.New(),
		codec:              defaultCodec{},
		healthChecks:       map[string]healthCheck{},
		healthPaths:        HealthPaths{Liveness: "/healthz", Readiness: "/readyz"},
		registeredMW:       map[string]Middleware{},
		defaultReadTimeout: 30 * time.Second,
		defaultMaxBodySize: 10 << 20, // 10 MiB
		defaultMaxHeaderKB: 32,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Mux returns the underlying `*http.ServeMux`. Generated routes call
// HandleFunc directly via the mux to keep the dependency surface small.
func (s *Server) Mux() *http.ServeMux { return s.mux }

// Use appends a middleware to the chain. Outer middlewares are added
// first; the chain is built in reverse at Start.
func (s *Server) Use(mw Middleware) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chain = append(s.chain, mw)
	return s
}

// RegisterMiddleware maps a DSL middleware name to its concrete
// implementation. The codegen layer can later resolve `@middlewares(Name)`
// against this map.
func (s *Server) RegisterMiddleware(name string, mw Middleware) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registeredMW[name] = mw
	return s
}

// HandleFunc registers a custom route on the underlying mux using Go 1.22
// pattern syntax (`"VERB /path"`).
func (s *Server) HandleFunc(pattern string, h http.HandlerFunc) *Server {
	s.mux.HandleFunc(pattern, h)
	return s
}

// Handle registers an http.Handler under the same Go 1.22 pattern
// syntax HandleFunc uses. The generated routes call this when methods
// declare `@middlewares(...)` because middleware chains return
// http.Handler, not http.HandlerFunc.
func (s *Server) Handle(pattern string, h http.Handler) *Server {
	s.mux.Handle(pattern, h)
	return s
}

// With looks up every middleware name in the registered table and wraps
// h in the order given (first name = outermost). Unknown names are
// skipped silently so a route can declare `@middlewares(Optional)` even
// when the wiring isn't installed yet — the runtime will pick it up the
// moment RegisterMiddleware adds it.
func (s *Server) With(names []string, h http.HandlerFunc) http.HandlerFunc {
	if len(names) == 0 {
		return h
	}
	s.mu.Lock()
	chain := make([]Middleware, 0, len(names))
	for _, n := range names {
		if mw, ok := s.registeredMW[n]; ok {
			chain = append(chain, mw)
		}
	}
	s.mu.Unlock()
	if len(chain) == 0 {
		return h
	}
	var wrapped http.Handler = h
	for i := len(chain) - 1; i >= 0; i-- {
		wrapped = chain[i](wrapped)
	}
	return wrapped.ServeHTTP
}

// SetDefaultReadTimeout configures the default per-method read timeout.
func (s *Server) SetDefaultReadTimeout(d time.Duration) *Server {
	s.defaultReadTimeout = d
	return s
}

// SetDefaultWriteTimeout configures the default per-method write timeout.
func (s *Server) SetDefaultWriteTimeout(d time.Duration) *Server {
	s.defaultWriteTimeout = d
	return s
}

// SetDefaultMaxBodySize configures the default request body size cap.
func (s *Server) SetDefaultMaxBodySize(bytes int64) *Server {
	s.defaultMaxBodySize = bytes
	return s
}

// SetDefaultMaxHeaderSize configures the default request header size cap
// (in kilobytes — Go's http.Server uses a kilobyte unit internally).
func (s *Server) SetDefaultMaxHeaderSize(kb int) *Server {
	s.defaultMaxHeaderKB = kb
	return s
}

// SetCORS attaches a CORS middleware configured by opts. Calling SetCORS
// twice replaces the previous configuration.
func (s *Server) SetCORS(opts CORSOptions) *Server {
	s.cors = &opts
	return s
}

// SetJSONCodec swaps the JSON codec used by handlers and the access-log
// access path.
func (s *Server) SetJSONCodec(c JSONCodec) *Server { s.codec = c; return s }

// SetLogger replaces the active Logger.
func (s *Server) SetLogger(l Logger) *Server { s.logger = l; return s }

// Logger exposes the active logger for handlers and middleware.
func (s *Server) Logger() Logger { return s.logger }

// Codec exposes the active JSON codec for handlers and tooling.
func (s *Server) Codec() JSONCodec { return s.codec }

// RegisterHealthCheck adds a named probe to `/readyz`. The function is
// invoked under a context with the supplied timeout; a non-nil error or
// timeout flips the readiness response to 503.
func (s *Server) RegisterHealthCheck(name string, timeout time.Duration, fn func(context.Context) error) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthChecks[name] = healthCheck{timeout: timeout, fn: fn}
	return s
}

// Handler returns the fully-wrapped http.Handler: mux + every global
// middleware registered via [Server.Use] + CORS (when configured) +
// Recovery (always outermost). Health endpoints are wired the first
// time Handler is called unless [WithoutDefaultHealth] was set.
//
// This is the entry point both [Server.Start] and tests use — wrap
// `httptest.NewServer(srv.Handler())` to exercise the full chain
// without binding a real listener.
func (s *Server) Handler() http.Handler {
	s.mu.Lock()
	if !s.noHealth && !s.healthMounted {
		s.mux.Handle(s.healthPaths.Liveness, s.livenessHandler())
		s.mux.Handle(s.healthPaths.Readiness, s.readinessHandler())
		s.healthMounted = true
	}
	chain := append([]Middleware(nil), s.chain...)
	cors := s.cors
	logger := s.logger
	s.mu.Unlock()

	var h http.Handler = s.mux
	if cors != nil {
		h = corsMiddleware(*cors)(h)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		h = chain[i](h)
	}
	return Recovery(logger)(h)
}

// Start binds the server to addr and serves until Stop is called. The
// handler chain is built by [Server.Handler] so the wrapping order is
// identical between live serving and httptest-driven test runs.
func (s *Server) Start(addr string) error {
	s.addr = addr
	s.httpSrv = &http.Server{
		Addr:           addr,
		Handler:        s.Handler(),
		ReadTimeout:    s.defaultReadTimeout,
		WriteTimeout:   s.defaultWriteTimeout,
		MaxHeaderBytes: s.defaultMaxHeaderKB * 1024,
	}
	if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Stop gracefully shuts down the running server. Safe to call before
// Start (it becomes a no-op).
func (s *Server) Stop(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}
