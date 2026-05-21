package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// requestIDHeader is the canonical header name read and written by the
// RequestID middleware.
const requestIDHeader = "X-Request-Id"

// ctxKey is a private type so request-scoped values don't collide with
// other packages' context keys.
type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
)

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

// RequestID extracts an existing X-Request-Id header or generates a new
// hex string, then stores it on the context (under both this package's
// internal key AND pkg/log's canonical key, so log.WithContext can
// surface it without an import cycle) and echoes it back in the
// response so clients can correlate logs.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(requestIDHeader)
			if id == "" {
				id = newRequestID()
			}
			w.Header().Set(requestIDHeader, id)
			ctx := withRequestID(r.Context(), id)
			ctx = log.WithRequestID(ctx, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AccessLog logs one structured line per request after the response has
// been written, including method, path, status, and elapsed time.
//
// Tracing identifiers (`trace_id`, `span_id`, `request_id`) are not
// added explicitly - `WithContext(ctx)` extracts them from the request
// context. Wire `otel.HTTPMiddleware(...)` and / or `RequestID()`
// upstream of AccessLog to populate the context.
func AccessLog(logger log.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// BodyLimit returns a middleware that caps `r.Body` at the supplied byte
// size. Requests that exceed it surface as a read-side error in the
// downstream handler (typical clients see 413).
func BodyLimit(maxBytes int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
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

// newRequestID returns a 16-char hex string suitable for X-Request-Id.
// Uses crypto/rand so collisions across nodes are vanishingly unlikely.
func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// withRequestID stores id on ctx so downstream handlers can retrieve it.
func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// RequestIDFromContext returns the request ID stored by RequestID, or "".
func RequestIDFromContext(ctx context.Context) string {
	if v := ctx.Value(ctxKeyRequestID); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
