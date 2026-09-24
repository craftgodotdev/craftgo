package server

import (
	"net/http"
	"runtime/debug"
	"time"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// contentTypeJSON is the Content-Type of the framework's JSON responses.
const contentTypeJSON = "application/json; charset=utf-8"

// committedResponseWriter records whether WriteHeader or Write has been called.
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

func (w *committedResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *committedResponseWriter) Committed() bool { return w.committed }

// Recovery answers a panic in next with a 500 text/plain response and logs it, with its
// stack, to logger. Once the response is committed it only logs; the client keeps what was sent.
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
	skip   map[string]bool
	fields func(*http.Request) []log.Field
}

// AccessLogFields adds the fields fn returns to every access line. fn runs after the
// handler, so r.Pattern holds the matched route.
func AccessLogFields(fn func(r *http.Request) []log.Field) AccessLogOption {
	return func(c *accessLogConfig) { c.fields = fn }
}

// AccessLogSkipPaths keeps requests whose URL path equals one of paths out of the log.
func AccessLogSkipPaths(paths ...string) AccessLogOption {
	return func(c *accessLogConfig) {
		for _, p := range paths {
			c.skip[p] = true
		}
	}
}

// AccessLog logs "http access" at Info after each request with method, path, status and
// latency, plus what [log.Logger.WithContext] adds (trace ids need an outer tracing middleware).
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
			fields := []log.Field{
				log.String("method", r.Method),
				log.String("path", r.URL.Path),
				log.Int("status", rw.status),
				log.Duration("latency", time.Since(start)),
			}
			if cfg.fields != nil {
				fields = append(fields, cfg.fields(r)...)
			}
			logger.WithContext(r.Context()).Info("http access", fields...)
		})
	}
}

// BodyLimit caps request bodies at maxBytes: a declared Content-Length above it is answered
// 413 before next runs, and a read past it fails with an [*http.MaxBytesError].
func BodyLimit(maxBytes int64) Middleware {
	return func(next http.Handler) http.Handler {
		return maxBodySizeHandler(next, maxBytes)
	}
}

// Timeout runs next under [http.TimeoutHandler]: past d the client gets 503 "request
// timeout". The response is buffered, so next can neither flush nor hijack.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, d, "request timeout")
	}
}

// statusRecorder records the last status passed to WriteHeader.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(c int) {
	s.status = c
	s.ResponseWriter.WriteHeader(c)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
