package server

import (
	"net/http"
	"runtime/debug"
	"time"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// contentTypeJSON is the Content-Type the framework's JSON responses (health,
// error envelopes, served OpenAPI spec) set.
const contentTypeJSON = "application/json; charset=utf-8"

// committedResponseWriter wraps http.ResponseWriter to remember whether
// the response status / body has already been flushed. Recovery uses it
// to decide whether a 500 can still be written or whether the response
// is already half-sent (in which case the recovery message would be
// silently dropped by net/http and the client would see a corrupted
// body). The wrapper preserves http.Hijacker / http.Flusher / http.Pusher
// so downstream middleware that depends on them keeps working.
type committedResponseWriter struct {
	http.ResponseWriter
	committed bool
}

func (w *committedResponseWriter) WriteHeader(code int) {
	w.committed = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *committedResponseWriter) Write(p []byte) (int, error) {
	w.committed = true
	return w.ResponseWriter.Write(p)
}

func (w *committedResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush forwards to the underlying writer's Flusher when available so streaming
// handlers keep working through the Recovery wrapper (mirrors statusRecorder).
func (w *committedResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Committed reports whether the response status / body has already
// been flushed. Generated handlers and the validation-error hook use
// it (via a type assertion against `interface{ Committed() bool }`)
// to skip late writes that net/http would silently drop.
func (w *committedResponseWriter) Committed() bool { return w.committed }

// Recovery converts panics inside downstream handlers into a 500 response
// while logging a stack trace. Always installed by Server.Start as the
// outermost middleware. When the panic fires AFTER the handler has
// already committed to a status (called WriteHeader or Write), the 500
// cannot be written - net/http silently drops the second WriteHeader and
// the body bytes would corrupt the in-flight response. In that case the
// middleware logs the panic loudly and lets the connection terminate; the
// client sees the truncated original response and the server operator
// sees the stack trace.
func Recovery(logger log.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cw := &committedResponseWriter{ResponseWriter: w}
			defer func() {
				if rec := recover(); rec != nil {
					l := logger.WithContext(r.Context())
					if cw.committed {
						l.Error("panic recovered after response committed; client receives truncated body",
							log.Any("panic", rec),
							log.String("stack", string(debug.Stack())),
						)
						return
					}
					l.Error("panic recovered",
						log.Any("panic", rec),
						log.String("stack", string(debug.Stack())),
					)
					http.Error(cw, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(cw, r)
		})
	}
}

// AccessLogOption configures [AccessLog].
type AccessLogOption func(*accessLogConfig)

type accessLogConfig struct {
	skip map[string]bool
}

// AccessLogSkipPaths keeps requests whose `r.URL.Path` equals one of paths
// out of the log - a `/metrics` scrape served on the API port, for example.
// The health probes need no entry here: they never reach the middleware
// chain (see [Server.Handler]).
func AccessLogSkipPaths(paths ...string) AccessLogOption {
	return func(c *accessLogConfig) {
		for _, p := range paths {
			c.skip[p] = true
		}
	}
}

// AccessLog logs one line per request after the response has been written:
// message `http access` with `method`, `path`, `status` and `latency`, plus
// the `trace_id` / `span_id` the request context carries (see
// [log.Logger.WithContext]). Wire the telemetry HTTP middleware before
// AccessLog so those ids are on the context.
//
// Every request that reaches the middleware logs; [AccessLogSkipPaths]
// keeps chosen routes out.
func AccessLog(logger log.Logger, opts ...AccessLogOption) Middleware {
	cfg := &accessLogConfig{skip: map[string]bool{}}
	for _, o := range opts {
		o(cfg)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.skip[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			logger.WithContext(r.Context()).Info("http access",
				log.String("method", r.Method),
				log.String("path", r.URL.Path),
				log.Int("status", rw.status),
				log.Duration("latency", time.Since(start)),
			)
		})
	}
}

// BodyLimit returns a middleware that caps the request body at maxBytes for
// every route it wraps. A request whose declared Content-Length already
// exceeds the cap is rejected with 413 before the handler runs; a
// chunked/unknown-length body is capped on read via http.MaxBytesReader
// (surfaced by the downstream handler, typically as 400). It shares its
// implementation with the per-method @maxBodySize guard ([maxBodySizeHandler])
// so the global and per-method limits never drift.
func BodyLimit(maxBytes int64) Middleware {
	return func(next http.Handler) http.Handler {
		return maxBodySizeHandler(next, maxBytes)
	}
}

// Timeout enforces an upper bound on handler execution. Streaming methods
// should not use this - they need write-side per-message idle limits which
// belong to the streaming codec, not the request lifecycle.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, d, "request timeout")
	}
}

// statusRecorder is a tiny ResponseWriter wrapper that captures the
// status code so AccessLog can log it. Flush() is forwarded explicitly
// because Go's interface satisfaction does not promote methods from
// embedded interfaces beyond the interface itself - without this
// passthrough, SSE / NDJSON / chunked-encoding handlers downstream
// would lose access to http.Flusher.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader records the status code before delegating.
func (s *statusRecorder) WriteHeader(c int) {
	s.status = c
	s.ResponseWriter.WriteHeader(c)
}

// Flush forwards to the underlying writer's Flusher when available so
// streaming handlers keep working.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the wrapped writer so http.ResponseController (and net/http's
// hijack path) can walk past this layer to reach the underlying Hijacker /
// ReaderFrom - without it a WebSocket upgrade or raw Hijack under AccessLog
// fails with "feature not supported".
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
