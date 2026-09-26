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

	notFound  http.Handler
	telemetry Middleware
}

// Logger is an alias of [log.Logger].
//
// Deprecated: use [log.Logger].
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

// WithTelemetry installs mw, such as the telemetry stack's HTTPMiddleware, outside [Recovery]
// and every [Server.Use] middleware, so their log lines carry the span it opens; a panic in mw
// itself is not recovered. The health probes bypass it; nil installs nothing.
func WithTelemetry(mw Middleware) Option {
	return func(s *Server) {
		if mw != nil {
			s.telemetry = mw
		}
	}
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
//
// Deprecated: pass the middleware to [Server.Handle] or [Server.Use].
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
//
// Deprecated: pass the middlewares to [Server.Handle], or wrap h in a [Chain].
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
func (s *Server) SetLogger(l log.Logger) *Server {
	log.SetDefault(l)
	return s
}

// Logger returns a [log.Follow] logger: each line goes to [log.Default] as it is then.
func (s *Server) Logger() log.Logger { return log.Follow() }

// Codec returns the codec in effect, the one [JSON] returns.
func (s *Server) Codec() JSONCodec { return JSON() }

// Handler returns what [Server.Start] serves: the [WithTelemetry] middleware, [Recovery] logging
// to [log.Default], CORS when set, the [Server.Use] middlewares in order, then the mux. CORS
// answers a preflight before any Use middleware sees it. The health probes are answered ahead of
// that chain, wrapped in Recovery only.
func (s *Server) Handler() http.Handler {
	s.mu.Lock()
	chain := NewChain(recovery(log.Default))
	if s.cors != nil {
		chain = chain.Append(corsMiddleware(*s.cors))
	}
	chain = chain.Append(s.chain...)
	app := chain.Then(s.muxLocked())
	if s.telemetry != nil {
		app = s.telemetry(app)
	}
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

// SetHandleNotFound sets the handler for the requests the mux answers 404, which otherwise
// get 404 {"message":"not found"}; a method mismatch keeps its 405. nil restores the default.
func (s *Server) SetHandleNotFound(h http.Handler) *Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notFound = h
	return s
}

// muxLocked returns s.mux answering an unmatched request with the not-found handler, or a JSON 405
// with its Allow header; a redirect stays the mux's. The caller holds s.mu.
func (s *Server) muxLocked() http.Handler {
	notFound := s.notFound
	if notFound == nil {
		notFound = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeStatusError(w, http.StatusNotFound)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := s.mux.Handler(r)
		if pattern != "" {
			s.mux.ServeHTTP(w, r)
			return
		}
		p := &statusProbe{header: http.Header{}}
		h.ServeHTTP(p, r)
		switch p.status {
		case http.StatusNotFound:
			notFound.ServeHTTP(w, r)
		case http.StatusMethodNotAllowed:
			w.Header().Set("Allow", p.header.Get("Allow"))
			writeStatusError(w, http.StatusMethodNotAllowed)
		default:
			s.mux.ServeHTTP(w, r)
		}
	})
}

// statusProbe is a ResponseWriter that discards the body and keeps the status and header.
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
