// Package server is craftgo's HTTP runtime: [Server], a ServeMux with middleware, health
// probes and server-wide limits, plus the JSON codec, error rendering and request-binding
// helpers that handlers use.
package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// Server is an HTTP server around a ServeMux; configure it and register routes before
// [Server.Start]. Its methods are safe for concurrent use.
type Server struct {
	mu sync.Mutex

	mux     *http.ServeMux
	chain   []Middleware
	httpSrv *http.Server

	cors *CORSOptions

	defaultReadTimeout    time.Duration
	defaultWriteTimeout   time.Duration
	defaultHandlerTimeout time.Duration
	defaultMaxBodySize    int64
	defaultMaxHeaderKB    int

	healthChecks map[string]healthCheck
	healthPaths  HealthPaths
	noHealth     bool

	registeredMW map[string]Middleware

	notFound http.Handler
}

// Logger is an alias of [log.Logger].
type Logger = log.Logger

// Middleware wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// Option configures a Server at construction time.
type Option func(*Server)

// New returns a Server with the health probes, a 30s read timeout and a 32 KB header cap,
// then applies opts. The first argument is ignored.
func New(_ any, opts ...Option) *Server {
	s := &Server{
		mux:                http.NewServeMux(),
		healthChecks:       map[string]healthCheck{},
		healthPaths:        HealthPaths{Liveness: DefaultLivenessPath, Readiness: DefaultReadinessPath},
		registeredMW:       map[string]Middleware{},
		defaultReadTimeout: 30 * time.Second,
		defaultMaxBodySize: 0,
		defaultMaxHeaderKB: 32,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Mux returns the underlying ServeMux. Routes registered on it directly skip the default
// body cap and handler timeout that [Server.Handle] applies.
func (s *Server) Mux() *http.ServeMux { return s.mux }

// Use appends mw to the middleware chain, the first added outermost; see [Server.Handler].
func (s *Server) Use(mw Middleware) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chain = append(s.chain, mw)
	return s
}

// RegisterMiddleware registers mw under name for [Server.With].
func (s *Server) RegisterMiddleware(name string, mw Middleware) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registeredMW[name] = mw
	return s
}

// HandleFunc is [Server.Handle] for a handler function, without middlewares.
func (s *Server) HandleFunc(pattern string, h http.HandlerFunc) *Server {
	return s.Handle(pattern, h)
}

// Handle registers h under a ServeMux pattern, wrapped in mws with the first outermost. The
// default body cap and handler timeout set at this point also wrap h, each unless h is a
// [WithLimits] handler that sets its own.
func (s *Server) Handle(pattern string, h http.Handler, mws ...Middleware) *Server {
	s.mux.Handle(pattern, NewChain(mws...).Then(s.applyDefaults(h)))
	return s
}

// With wraps h in the middlewares registered under names, the first outermost, resolving
// them when With is called; an unknown name is skipped.
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

// SetDefaultReadTimeout sets the http.Server ReadTimeout [Server.Start] uses; the default is 30s.
func (s *Server) SetDefaultReadTimeout(d time.Duration) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultReadTimeout = d
	return s
}

// SetDefaultHandlerTimeout sets the request-context deadline ([Limits.Timeout]) that
// [Server.Handle] gives routes registered afterwards; 0, the default, sets none.
func (s *Server) SetDefaultHandlerTimeout(d time.Duration) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultHandlerTimeout = d
	return s
}

// SetDefaultWriteTimeout sets the http.Server WriteTimeout, a deadline on writing the whole
// response; 0, the default, leaves streaming and long downloads uncut.
func (s *Server) SetDefaultWriteTimeout(d time.Duration) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultWriteTimeout = d
	return s
}

// SetDefaultMaxBodySize sets the body cap in bytes ([BodyLimit]) that [Server.Handle] gives
// routes registered afterwards; 0, the default, sets none.
func (s *Server) SetDefaultMaxBodySize(bytes int64) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultMaxBodySize = bytes
	return s
}

// SetDefaultMaxHeaderSize sets the http.Server header cap in kilobytes; the default is 32.
func (s *Server) SetDefaultMaxHeaderSize(kb int) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultMaxHeaderKB = kb
	return s
}

// SetCORS adds CORS handling with opts to the chain; a second call replaces the first.
func (s *Server) SetCORS(opts CORSOptions) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cors = &opts
	return s
}

// SetJSONCodec calls the process-wide [SetGlobalJSONCodec].
func (s *Server) SetJSONCodec(c JSONCodec) error { return SetGlobalJSONCodec(c) }

// SetStrictJSON calls the process-wide [SetStrictJSON].
func (s *Server) SetStrictJSON(strict bool) error { return SetStrictJSON(strict) }

// SetLogger installs l as [log.Default], the logger the server's [Recovery] writes to; nil is
// ignored.
func (s *Server) SetLogger(l Logger) *Server {
	log.SetDefault(l)
	return s
}

// Logger returns [log.Default].
func (s *Server) Logger() Logger { return log.Default() }

// Codec returns the codec in effect, the one [JSON] returns.
func (s *Server) Codec() JSONCodec { return JSON() }

// Handler returns what [Server.Start] serves: [Recovery], logging to [log.Default], then the
// [Server.Use] middlewares in order, then CORS when set, then the mux. The health probes are
// answered ahead of that chain, wrapped in Recovery only, so no other middleware sees them.
func (s *Server) Handler() http.Handler {
	s.mu.Lock()
	chain := NewChain(recovery(log.Default)).Append(s.chain...)
	if s.cors != nil {
		chain = chain.Append(corsMiddleware(*s.cors))
	}
	app := chain.Then(s.muxWithNotFoundLocked())
	probes := s.probesLocked()
	s.mu.Unlock()
	if probes == nil {
		return app
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := probes[r.URL.Path]; ok {
			h.ServeHTTP(w, r)
			return
		}
		app.ServeHTTP(w, r)
	})
}

// SetHandleNotFound sets the handler for the requests the mux answers 404; a method mismatch
// keeps its 405. nil restores the mux's own answer.
func (s *Server) SetHandleNotFound(h http.Handler) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notFound = h
	return s
}

// muxWithNotFoundLocked returns s.mux, handing the requests it would answer 404 to s.notFound
// when set; the caller holds s.mu.
func (s *Server) muxWithNotFoundLocked() http.Handler {
	if s.notFound == nil {
		return s.mux
	}
	notFound := s.notFound
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, pattern := s.mux.Handler(r); pattern == "" && answersNotFound(h, r) {
			notFound.ServeHTTP(w, r)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

// answersNotFound reports whether h, the mux's answer to a request no route matches, is a
// 404 rather than a 405 or a redirect to the cleaned path.
func answersNotFound(h http.Handler, r *http.Request) bool {
	p := &statusProbe{header: http.Header{}}
	h.ServeHTTP(p, r)
	return p.status == http.StatusNotFound
}

// statusProbe is a ResponseWriter that discards the response and keeps its status.
type statusProbe struct {
	header http.Header
	status int
}

func (p *statusProbe) Header() http.Header { return p.header }

func (p *statusProbe) WriteHeader(code int) {
	if p.status == 0 {
		p.status = code
	}
}

func (p *statusProbe) Write(b []byte) (int, error) {
	p.WriteHeader(http.StatusOK)
	return len(b), nil
}

// Start serves [Server.Handler] on addr until [Server.Stop] and returns nil after a graceful
// stop. Request headers must arrive within 10s, and idle connections close after 120s.
func (s *Server) Start(addr string) error {
	// Handler locks s.mu, so it runs before the lock is taken.
	handler := s.Handler()
	s.mu.Lock()
	s.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       s.defaultReadTimeout,
		WriteTimeout:      s.defaultWriteTimeout,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    s.defaultMaxHeaderKB * 1024,
	}
	srv := s.httpSrv
	s.mu.Unlock()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Stop shuts the server down gracefully with [http.Server.Shutdown]; before [Server.Start]
// it does nothing.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	srv := s.httpSrv
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}
