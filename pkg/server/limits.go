package server

import (
	"net/http"
	"time"
)

// Limits bundles the per-method runtime guards the DSL surfaces via
// `@timeout` and `@maxBodySize`. Zero values mean "no limit" for that
// dimension - the wrapper only applies a guard when the corresponding
// field is non-zero.
//
// Transport-level deadlines (`http.Server.ReadTimeout`,
// `WriteTimeout`, `IdleTimeout`, `ReadHeaderTimeout`,
// `MaxHeaderBytes`) are NOT modelled here - those are server-wide
// concerns the user configures on the underlying [http.Server]
// directly when the stdlib defaults are insufficient. This package
// only owns guards that can be enforced per-handler.
type Limits struct {
	// Timeout caps the full handler lifecycle (decode body → user
	// logic → encode response) via [http.TimeoutHandler]; the
	// client receives a 503 when the deadline elapses and the
	// handler context is cancelled. Passthrough endpoints opt out
	// - `http.TimeoutHandler` would prematurely cut whatever
	// stream their handler decides to produce.
	Timeout time.Duration

	// MaxBodySize caps the request body size in bytes. Wraps r.Body
	// with [http.MaxBytesReader] before the user's handler runs;
	// reads past the cap return an error, which the JSON decoder
	// surfaces as 400.
	MaxBodySize int64
}

// WithLimits returns h wrapped with the runtime guards declared in l.
// Zero-valued fields skip their respective wrapping so the function
// is a cheap pass-through when the DSL declared no limits.
//
// Wrapping order is innermost-first: MaxBodySize wraps r.Body before
// the handler reads it, then Timeout wraps the whole chain so the
// timeout includes the body-read step.
func WithLimits(h http.Handler, l Limits) http.Handler {
	if l.MaxBodySize > 0 {
		h = maxBodySizeHandler(h, l.MaxBodySize)
	}
	if l.Timeout > 0 {
		h = http.TimeoutHandler(h, l.Timeout, "request timed out")
	}
	return h
}

// maxBodySizeHandler returns a middleware that swaps r.Body with a
// MaxBytesReader so reads past the cap fail loudly. Errors propagate
// up the stack as a normal Read error - the user's handler decides
// whether to translate them into 400 / 413.
func maxBodySizeHandler(h http.Handler, n int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, n)
		}
		h.ServeHTTP(w, r)
	})
}
