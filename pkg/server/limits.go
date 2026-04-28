package server

import (
	"net/http"
	"time"
)

// Limits bundles the per-method runtime guards that the DSL surfaces
// via `@readTimeout`, `@writeTimeout`, `@maxBodySize`, and
// `@maxHeaderSize`. Zero values mean "no limit" for that dimension —
// the wrapper only applies a guard when the corresponding field is
// non-zero.
//
// Header-size guards live at the server level (Go's stdlib doesn't
// expose a per-handler header cap), so MaxHeaderSize is informational
// here; the framework picks up the largest declared value when sizing
// the global server.
type Limits struct {
	// ReadTimeout caps the duration of a single in-flight request.
	// Implemented via [http.TimeoutHandler]; the client receives a
	// 503 when the deadline elapses. Streaming endpoints should use a
	// per-write idle timeout instead — `@stream` skips ReadTimeout
	// emission for that reason.
	ReadTimeout time.Duration

	// WriteTimeout is currently informational. The stdlib expresses
	// write deadlines on the Server struct; per-handler enforcement
	// would require hijacking the connection, which we don't do today.
	WriteTimeout time.Duration

	// MaxBodySize caps the request body size in bytes. Wraps r.Body
	// with [http.MaxBytesReader] before the user's handler runs;
	// reads past the cap return an error, which the JSON decoder
	// surfaces as 400.
	MaxBodySize int64

	// MaxHeaderSize is informational (see type doc).
	MaxHeaderSize int64
}

// WithLimits returns h wrapped with the runtime guards declared in l.
// Zero-valued fields skip their respective wrapping so the function
// is a cheap pass-through when the DSL declared no limits.
//
// Wrapping order is innermost-first: MaxBodySize wraps r.Body before
// the handler reads it, then ReadTimeout wraps the whole chain so the
// timeout includes the body-read step.
func WithLimits(h http.Handler, l Limits) http.Handler {
	if l.MaxBodySize > 0 {
		h = maxBodySizeHandler(h, l.MaxBodySize)
	}
	if l.ReadTimeout > 0 {
		h = http.TimeoutHandler(h, l.ReadTimeout, "request timed out")
	}
	return h
}

// maxBodySizeHandler returns a middleware that swaps r.Body with a
// MaxBytesReader so reads past the cap fail loudly. Errors propagate
// up the stack as a normal Read error — the user's handler decides
// whether to translate them into 400 / 413.
func maxBodySizeHandler(h http.Handler, n int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, n)
		}
		h.ServeHTTP(w, r)
	})
}
