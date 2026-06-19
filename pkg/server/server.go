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

	"github.com/craftgodotdev/craftgo/pkg/log"
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

	// notFound is the handler invoked when a request reaches the
	// mux without matching any registered route. nil means "use the
	// stdlib default" (`404 page not found` plain-text body).
	notFound http.Handler
}

// Logger aliases the public Logger interface so handler-internal code does
// not need a second import.
type Logger = log.Logger

// Middleware wraps an http.Handler. Order: outermost first, so the slice is
// applied in reverse during Start.
type Middleware func(http.Handler) http.Handler

// Option configures a Server at construction time.
type Option func(*Server)

// DefaultLivenessPath / DefaultReadinessPath are the health routes a fresh
// [Server] mounts. Exported so other layers (the analyzer's reserved-route
// check) can reference the same values instead of re-spelling them.
const (
	DefaultLivenessPath  = "/healthz"
	DefaultReadinessPath = "/readyz"
)

// HealthPaths is the override pair for [DefaultLivenessPath] and
// [DefaultReadinessPath].
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
// documented constructor signature - the runtime doesn't introspect it.
func New(_ any, opts ...Option) *Server {
	s := &Server{
		mux:                http.NewServeMux(),
		logger:             log.New(),
		codec:              defaultCodec{},
		healthChecks:       map[string]healthCheck{},
		healthPaths:        HealthPaths{Liveness: DefaultLivenessPath, Readiness: DefaultReadinessPath},
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
// syntax HandleFunc uses. Optional variadic middlewares wrap the
// handler left-to-right so the FIRST entry ends up the outermost
// frame - the order a reader scans matches the order a request
// flows through. Order chosen so:
//
//	srv.Handle("POST /x", h, Auth, RateLimit, CORS)
//
// reads "Auth wraps RateLimit wraps CORS wraps h" - request hits
// Auth first, response leaves CORS last.
//
// The variadic form keeps the route line flat regardless of chain
// depth, so it scans top-to-bottom in the same outermost-first order
// the request actually flows through.
func (s *Server) Handle(pattern string, h http.Handler, mws ...Middleware) *Server {
	s.mux.Handle(pattern, NewChain(mws...).Then(h))
	return s
}

// With looks up every middleware name in the registered table and wraps
// h in the order given (first name = outermost). Unknown names are
// skipped silently so a route can declare `@middlewares(Optional)` even
// when the wiring isn't installed yet - the runtime will pick it up the
// moment RegisterMiddleware adds it.
func (s *Server) With(names []string, h http.HandlerFunc) http.HandlerFunc {
	if len(names) == 0 {
		return h
	}
	s.mu.Lock()
	chain := make(Chain, 0, len(names))
	for _, n := range names {
		if mw, ok := s.registeredMW[n]; ok {
			chain = append(chain, mw)
		}
	}
	s.mu.Unlock()
	return chain.Then(h).ServeHTTP
}

// SetDefaultReadTimeout configures the default per-method read timeout.
func (s *Server) SetDefaultReadTimeout(d time.Duration) *Server {
	s.defaultReadTimeout = d
	return s
}

// SetDefaultWriteTimeout sets the http.Server WriteTimeout - a hard deadline on
// the entire response write. It defaults to 0 (unbounded) so streaming, SSE,
// @passthrough, and large/slow downloads are not cut off mid-response; set a
// ceiling here for a server that only serves bounded JSON and wants socket-level
// slow-drain protection on top of the per-handler Timeout middleware.
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
// (in kilobytes - Go's http.Server uses a kilobyte unit internally).
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

// SetJSONCodec swaps the JSON codec used by generated handlers, the
// access-log middleware, and the health endpoints. The change is
// process-wide via [SetGlobalJSONCodec]; the per-Server field is kept
// for callers that want to introspect via [Server.Codec] but the
// authoritative value lives on the package-level atomic.
func (s *Server) SetJSONCodec(c JSONCodec) *Server {
	s.codec = c
	SetGlobalJSONCodec(c)
	return s
}

// SetLogger replaces the active Logger and mirrors it to the
// package-level [log.Default] so codegen-emitted logic files reach
// the same instance via `log.Default().WithContext(ctx)` without
// receiving a handle through ServiceContext.
func (s *Server) SetLogger(l Logger) *Server {
	s.logger = l
	log.SetDefault(l)
	return s
}

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
// This is the entry point both [Server.Start] and tests use - wrap
// `httptest.NewServer(srv.Handler())` to exercise the full chain
// without binding a real listener.
func (s *Server) Handler() http.Handler {
	s.mu.Lock()
	if !s.noHealth && !s.healthMounted {
		s.mux.Handle(s.healthPaths.Liveness, s.livenessHandler())
		s.mux.Handle(s.healthPaths.Readiness, s.readinessHandler())
		s.healthMounted = true
	}
	// Build the chain outermost-first: Recovery wraps the user chain
	// wraps CORS wraps the mux. CORS sits closest to the mux so it
	// observes the final response headers; Recovery sits outermost so
	// it catches panics from every other middleware too.
	chain := NewChain(Recovery(s.logger)).Append(s.chain...)
	if s.cors != nil {
		chain = chain.Append(corsMiddleware(*s.cors))
	}
	inner := s.muxWithNotFoundLocked()
	s.mu.Unlock()
	return chain.Then(inner)
}

// muxWithNotFoundLocked returns s.mux, or - if a custom NotFound
// handler is installed - a thin wrapper that dispatches unmatched
// requests to it instead of the stdlib default 404.
//
// Caller must hold s.mu; reads s.notFound.
func (s *Server) muxWithNotFoundLocked() http.Handler {
	if s.notFound == nil {
		return s.mux
	}
	notFound := s.notFound
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := s.mux.Handler(r); pattern == "" {
			notFound.ServeHTTP(w, r)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

// Start binds the server to addr and serves until Stop is called. The
// handler chain is built by [Server.Handler] so the wrapping order is
// identical between live serving and httptest-driven test runs.
func (s *Server) Start(addr string) error {
	// Handler() takes s.mu, so build it BEFORE we take the lock -
	// otherwise the same goroutine deadlocks on the sync.Mutex.
	handler := s.Handler()
	s.mu.Lock()
	s.addr = addr
	s.httpSrv = &http.Server{
		Addr:    addr,
		Handler: handler,
		// ReadHeaderTimeout caps the time a client may spend sending the
		// request line + headers. Without it a slow-read client (drip-
		// feeding 1 byte every 30s) can pin a goroutine indefinitely -
		// the classic Slowloris attack. The full ReadTimeout below also
		// helps but only after a request line arrives; this knob fires
		// before the handler ever runs. 10s matches Go's
		// http.DefaultClient default and is the floor net/http itself
		// recommends.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       s.defaultReadTimeout,
		// WriteTimeout is a HARD deadline on the whole response write, so it
		// defaults to 0 (unbounded) on purpose: a non-zero value would kill
		// legitimate slow/large downloads and streaming / SSE / @passthrough
		// handlers mid-response. The per-handler bound is the Timeout
		// middleware (`@timeout` / config handlerTimeout); set a socket-level
		// ceiling explicitly with SetDefaultWriteTimeout for plain JSON APIs.
		WriteTimeout: s.defaultWriteTimeout,
		// IdleTimeout reaps idle keep-alive connections between requests (it
		// does NOT touch an in-flight response), so a client that opens
		// connections and never reuses them can't accumulate goroutines
		// indefinitely - safe to default without breaking streaming.
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: s.defaultMaxHeaderKB * 1024,
	}
	srv := s.httpSrv
	s.mu.Unlock()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Stop gracefully shuts down the running server. Safe to call before
// Start (it becomes a no-op).
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	srv := s.httpSrv
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}
