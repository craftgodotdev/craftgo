package server

import (
	"net/http"
	"runtime/debug"
	"time"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// contentTypeJSON is the Content-Type of the framework's JSON responses.
const contentTypeJSON = "application/json; charset=utf-8"

// trackingWriter records the status of the response written through it. The response is
// committed once a WriteHeader with a final status, a Write or a Flush has fixed its head.
type trackingWriter struct {
	http.ResponseWriter
	status int
}

// commit records status unless an earlier call committed the response.
func (w *trackingWriter) commit(status int) {
	if w.status == 0 {
		w.status = status
	}
}

// WriteHeader commits on a final status; net/http keeps the response open after a 1xx other
// than 101.
func (w *trackingWriter) WriteHeader(code int) {
	if code >= 200 || code == http.StatusSwitchingProtocols {
		w.commit(code)
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *trackingWriter) Write(p []byte) (int, error) {
	w.commit(http.StatusOK)
	return w.ResponseWriter.Write(p)
}

func (w *trackingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		w.commit(http.StatusOK)
		f.Flush()
	}
}

func (w *trackingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *trackingWriter) Committed() bool { return w.status != 0 }

// Status returns the committed status, or 200, what net/http sends for a handler that writes
// nothing.
func (w *trackingWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// Recovery answers a panic in next with a 500 text/plain response and logs it, with its
// stack, to logger. Once the response is committed it only logs; the client keeps what was sent.
func Recovery(logger log.Logger) Middleware {
	return recovery(func() log.Logger { return logger })
}

// recovery is [Recovery] with the logger looked up when a panic is recovered.
func recovery(logger func() log.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tw := &trackingWriter{ResponseWriter: w}
			defer func() {
				if rec := recover(); rec != nil {
					l := logger().WithContext(r.Context())
					if tw.Committed() {
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
					http.Error(tw, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(tw, r)
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
			tw := &trackingWriter{ResponseWriter: w}
			next.ServeHTTP(tw, r)
			fields := []log.Field{
				log.String("method", r.Method),
				log.String("path", r.URL.Path),
				log.Int("status", tw.Status()),
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
